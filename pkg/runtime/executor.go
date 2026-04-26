package runtime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/events"
	"github.com/fookiejs/fookie/pkg/telemetry"
	"github.com/fookiejs/fookie/pkg/validator"
	"github.com/google/uuid"
)

type EventBus interface {
	PublishCRUD(op, model, id string, payload map[string]interface{})
}

type Executor struct {
	db            *sql.DB
	schema        *ast.Schema
	extMgr        *ExternalManager
	sqlGen        *compiler.SQLGenerator
	logger        Logger
	bus           EventBus
	logSink       LogSink
	roomBus       *events.RoomBus
	roomNameCache map[string]string
	outboxNotify  func(id string) // optional: called after every outbox INSERT (for Redis wake-up)
}

func NewExecutor(db *sql.DB, schema *ast.Schema, logger Logger) *Executor {
	extMgr := NewExternalManager()
	e := &Executor{
		db:            db,
		schema:        schema,
		extMgr:        extMgr,
		sqlGen:        compiler.NewSQLGenerator(schema),
		logger:        logger,
		logSink:       NewSink(),
		roomNameCache: make(map[string]string),
	}
	extMgr.store = &StoreAdapter{e: e}

	for _, ext := range schema.Externals {
		if ext.URL != "" {
			extMgr.RegisterURL(ext.Name, ext.URL)
		}
	}

	return e
}

func (e *Executor) SetLogSink(s LogSink)               { e.logSink = s }
func (e *Executor) SetOutboxNotify(fn func(id string)) { e.outboxNotify = fn }
func (e *Executor) notifyOutbox(id string) {
	if e.outboxNotify != nil {
		e.outboxNotify(id)
	}
}

func (e *Executor) rootRC(ctx context.Context, req map[string]interface{}, op, model string) (*runCtx, context.Context) {
	rc := newRunCtx(req)
	rc.operation = op
	rc.modelName = model
	if existing := RootRequestIDFromCtx(ctx); existing != "" {
		rc.rootRequestID = existing
		rc.depth = rootDepthFromCtx(ctx) + 1
	} else {
		rc.rootRequestID = newRootRequestID(ctx)
		rc.depth = 0
	}
	ctx = withRootRequest(ctx, rc.rootRequestID, rc.depth)
	return rc, ctx
}

func (e *Executor) emitLog(ctx context.Context, rc *runCtx, level, msg string, fields map[string]interface{}, lineNo int, source string) {
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
		Fields:    fields,
		LineNo:    lineNo,
		Source:    source,
	}
	if rc != nil {
		entry.RootRequestID = rc.rootRequestID
		entry.Operation = rc.operation
		entry.Model = rc.modelName
		entry.Block = rc.blockType
		entry.Depth = rc.depth
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		if sc.HasTraceID() {
			entry.TraceID = sc.TraceID().String()
		}
		if sc.HasSpanID() {
			entry.SpanID = sc.SpanID().String()
		}
	}
	if span := trace.SpanFromContext(ctx); span != nil && span.SpanContext().IsValid() {
		attrs := []attribute.KeyValue{
			attribute.String("log.level", level),
			attribute.String("log.source", source),
			attribute.Int("log.depth", entry.Depth),
		}
		if entry.Operation != "" {
			attrs = append(attrs, attribute.String("fookie.operation", entry.Operation))
		}
		if entry.Model != "" {
			attrs = append(attrs, attribute.String("fookie.model", entry.Model))
		}
		if entry.Block != "" {
			attrs = append(attrs, attribute.String("fookie.block", entry.Block))
		}
		if lineNo > 0 {
			attrs = append(attrs, attribute.Int("fookie.fsl.line", lineNo))
		}
		for k, v := range fields {
			attrs = append(attrs, attribute.String("log."+k, fmt.Sprintf("%v", v)))
		}
		span.AddEvent(msg, trace.WithAttributes(attrs...))
	}
	if e.logSink != nil {
		e.logSink.Emit(entry)
	}
}

func (e *Executor) emitRuntime(ctx context.Context, rc *runCtx, level, msg string, fields map[string]interface{}) {
	e.emitLog(ctx, rc, level, msg, fields, 0, "runtime")
}

func (e *Executor) SetEventBus(bus EventBus) {
	e.bus = bus
}

func (e *Executor) SetRoomBus(b *events.RoomBus) {
	e.roomBus = b
	e.extMgr.SetRoomBus(b)
}

func (e *Executor) RegisterRoomName(name, id string) {
	e.roomNameCache[name] = id
}

func (e *Executor) roomIDByName(name string) (string, bool) {
	id, ok := e.roomNameCache[name]
	return id, ok
}

type StoreAdapter struct{ e *Executor }

func (s *StoreAdapter) Read(ctx context.Context, model string, args map[string]interface{}) ([]map[string]interface{}, error) {
	return s.e.Read(ctx, model, args)
}
func (s *StoreAdapter) Create(ctx context.Context, model string, body map[string]interface{}) (map[string]interface{}, error) {
	return s.e.Create(ctx, model, WithSystemInput(body))
}
func (s *StoreAdapter) Update(ctx context.Context, model string, id string, patch map[string]interface{}) (map[string]interface{}, error) {
	return s.e.Update(ctx, model, id, WithSystemInput(patch))
}
func (s *StoreAdapter) Delete(ctx context.Context, model string, id string) error {
	return s.e.Delete(ctx, model, id, WithSystemInput(map[string]interface{}{}))
}

func (e *Executor) emit(op, model, id string, payload map[string]interface{}) {
	if e.bus != nil {
		e.bus.PublishCRUD(op, model, id, payload)
	}
}

func (e *Executor) ExternalManager() *ExternalManager { return e.extMgr }
func (e *Executor) DB() *sql.DB                       { return e.db }
func (e *Executor) Schema() *ast.Schema               { return e.schema }

func (e *Executor) InputKeyForDBColumn(modelName, dbColumn string) (string, error) {
	_, model, err := e.resolveOp(modelName, "update")
	if err != nil {
		return "", err
	}
	for _, f := range model.Fields {
		_, dbKey := fieldKeys(f)
		if dbKey == dbColumn {
			inKey, _ := fieldKeys(f)
			return inKey, nil
		}
	}
	if dbColumn == "status" {
		return "status", nil
	}
	return "", fmt.Errorf("column %q is not a declared field of model %s (and not status)", dbColumn, modelName)
}

func WithSystemBody(body map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"body": body, "__system": true}
}

func WithSystemInput(body map[string]interface{}) map[string]interface{} {
	return WithSystemBody(body)
}

func (e *Executor) Create(ctx context.Context, modelName string, req map[string]interface{}) (out map[string]interface{}, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.create "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "create", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "create")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "create"),
	)

	op, model, err := e.resolveOp(modelName, "create")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	rc, ctx := e.rootRC(ctx, req, "create", modelName)
	pay := rc.payload()

	txBeginCtx, txBeginSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_begin",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "create"),
		),
	)
	tx, err := e.db.BeginTx(txBeginCtx, nil)
	if err != nil {
		txBeginSpan.RecordError(err)
		txBeginSpan.SetStatus(codes.Error, err.Error())
		txBeginSpan.End()
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	txBeginSpan.End()
	defer tx.Rollback()

	ctx = withTx(ctx, tx)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return nil, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	row := map[string]interface{}{
		"id":         uuid.New().String(),
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
		"status":     "initiate",
	}
	for _, field := range model.Fields {
		inKey, dbKey := fieldKeys(field)
		if val, ok := pay[inKey]; ok {
			row[dbKey] = val
		}
	}

	if op.Modify != nil {
		mCtx, mSpan := telemetry.Tracer().Start(ctx, "fookie.modify")
		prevBlock := rc.blockType
		rc.blockType = "modify"
		for _, stmt := range op.Modify.Statements {
			switch s := stmt.(type) {
			case *ast.ModifyAssignment:
				val, err := e.evalExpr(mCtx, s.Value, rc)
				if err != nil {
					mSpan.RecordError(err)
					mSpan.SetStatus(codes.Error, err.Error())
					mSpan.End()
					rc.blockType = prevBlock
					return nil, fmt.Errorf("modify %s: %w", s.Field, err)
				}
				row[resolveDBKey(s.Field, model)] = val
			case *ast.PredicateExpr:
				if _, err := e.evalExpr(mCtx, s.Expr, rc); err != nil {
					mSpan.RecordError(err)
					mSpan.SetStatus(codes.Error, err.Error())
					mSpan.End()
					rc.blockType = prevBlock
					return nil, fmt.Errorf("modify stmt: %w", err)
				}
			}
		}
		rc.blockType = prevBlock
		mSpan.End()
	}

	plannedID, _ := row["id"].(string)
	rc.output["id"] = plannedID
	rc.vars["id"] = plannedID
	for k, v := range row {
		rc.output[k] = v
	}

	if op.Effect != nil {
		lockT, err := collectLockTargetsFromEffect(ctx, e, op.Effect, rc)
		if err != nil {
			return nil, fmt.Errorf("lock targets: %w", err)
		}
		pre, post := partitionCreateLockTargets(modelName, plannedID, lockT)
		if err := e.acquireRowLocksGlobalOrder(ctx, pre); err != nil {
			return nil, err
		}

		sqlStr, keyOrder := e.sqlGen.CompileInsert(model, row)
		args := make([]interface{}, len(keyOrder))
		for i, k := range keyOrder {
			args[i] = row[k]
		}

		dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.insert")
		dbSpan.SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", sqlStr),
		)
		var id string
		var createdAt time.Time
		var status string
		if err := e.execer(ctx).QueryRowContext(dbCtx, sqlStr, args...).Scan(&id, &createdAt, &status); err != nil {
			dbSpan.RecordError(err)
			dbSpan.SetStatus(codes.Error, err.Error())
			dbSpan.End()
			return nil, fmt.Errorf("insert: %w", err)
		}
		dbSpan.End()

		rc.output["id"] = id
		rc.output["created_at"] = createdAt
		rc.output["status"] = status
		rc.vars["id"] = id
		for k, v := range row {
			rc.output[k] = v
		}

		if err := e.acquireRowLocksGlobalOrder(ctx, post); err != nil {
			return nil, err
		}

		if err := e.runSyncEffectStatements(ctx, op.Effect, rc, id); err != nil {
			return nil, fmt.Errorf("sync effect: %w", err)
		}

		queued, err := e.queueEffects(ctx, op.Effect, op.Compensate, modelName, id, rc)
		if err != nil {
			return nil, fmt.Errorf("queue effects: %w", err)
		}
		if queued {
			if _, err := e.execer(ctx).ExecContext(ctx,
				fmt.Sprintf(`UPDATE "%s" SET status = 'progress', updated_at = NOW() WHERE id = $1`, compiler.SnakeCase(modelName)),
				id); err != nil {
				return nil, fmt.Errorf("status progress: %w", err)
			}
			rc.output["status"] = "progress"
		} else {
			if _, err := e.execer(ctx).ExecContext(ctx,
				fmt.Sprintf(`UPDATE "%s" SET status = 'done', updated_at = NOW() WHERE id = $1`, compiler.SnakeCase(modelName)),
				id); err != nil {
				return nil, fmt.Errorf("status done: %w", err)
			}
			rc.output["status"] = "done"
		}
	} else {
		sqlStr, keyOrder := e.sqlGen.CompileInsert(model, row)
		args := make([]interface{}, len(keyOrder))
		for i, k := range keyOrder {
			args[i] = row[k]
		}

		dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.insert")
		dbSpan.SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", sqlStr),
		)
		var id string
		var createdAt time.Time
		var status string
		if err := e.execer(ctx).QueryRowContext(dbCtx, sqlStr, args...).Scan(&id, &createdAt, &status); err != nil {
			dbSpan.RecordError(err)
			dbSpan.SetStatus(codes.Error, err.Error())
			dbSpan.End()
			return nil, fmt.Errorf("insert: %w", err)
		}
		dbSpan.End()

		rc.output["id"] = id
		rc.output["created_at"] = createdAt
		rc.output["status"] = status
		rc.vars["id"] = id
		for k, v := range row {
			rc.output[k] = v
		}

		if _, err := e.execer(ctx).ExecContext(ctx,
			fmt.Sprintf(`UPDATE "%s" SET status = 'done', updated_at = NOW() WHERE id = $1`, compiler.SnakeCase(modelName)),
			id); err != nil {
			return nil, fmt.Errorf("status done: %w", err)
		}
		rc.output["status"] = "done"
	}

	_, txCommitSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_commit",
		trace.WithAttributes(
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "create"),
		),
	)
	if err := tx.Commit(); err != nil {
		txCommitSpan.RecordError(err)
		txCommitSpan.SetStatus(codes.Error, err.Error())
		txCommitSpan.End()
		return nil, fmt.Errorf("commit: %w", err)
	}
	txCommitSpan.End()

	maskRestrictedFields(rc.output, model)

	id, _ := rc.output["id"].(string)
	e.emit("created", modelName, id, req)
	e.logger.Info("created", "model", modelName, "id", id)
	return rc.output, nil
}

func (e *Executor) Read(ctx context.Context, modelName string, req map[string]interface{}) (result []map[string]interface{}, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.read "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "read", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "read")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "read"),
	)

	op, model, err := e.resolveOp(modelName, "read")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	rc, ctx := e.rootRC(ctx, req, "read", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return nil, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	frag := ""
	args := []interface{}{}
	if w, ok := req["filter"].(map[string]interface{}); ok && len(w) > 0 {
		var err error
		frag, args, _, err = e.sqlGen.BuildWhereClause(model, w, 1)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("filter: %w", err)
		}
	}

	limit, offset := 0, 0
	if c, ok := req["cursor"].(map[string]interface{}); ok {
		limit = toInt(c["size"])
		offset = toInt(c["after"])
	}

	sqlStr := e.sqlGen.CompileReadWithFilter(model, op, frag, limit, offset)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.select")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	rows, err := e.execer(ctx).QueryContext(dbCtx, sqlStr, args...)
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		dbSpan.End()
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	result, err = scanRows(rows)
	dbSpan.SetAttributes(attribute.Int("fookie.row_count", len(result)))
	dbSpan.End()
	if err != nil {
		return nil, err
	}

	for _, row := range result {
		maskRestrictedFields(row, model)
	}

	if !rc.isSystem {
		e.emit("read", modelName, "", map[string]interface{}{"count": len(result)})
	}
	return result, nil
}

type ConnectionResult struct {
	Edges      []EdgeResult
	PageInfo   PageInfoResult
	TotalCount int
}

type EdgeResult struct {
	Node   map[string]interface{}
	Cursor string // base64-encoded keyset cursor
}

type PageInfoResult struct {
	HasNextPage bool
	HasPrevPage bool
	StartCursor string
	EndCursor   string
	TotalCount  int
}

func (e *Executor) ReadConnection(ctx context.Context, modelName string, req map[string]interface{}) (*ConnectionResult, error) {
	op, model, err := e.resolveOp(modelName, "read")
	if err != nil {
		return nil, err
	}

	rc, ctx := e.rootRC(ctx, req, "read", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return nil, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	first := 20
	var afterCursor *cursorKey
	if c, ok := req["cursor"].(map[string]interface{}); ok {
		if v := toInt(c["first"]); v > 0 {
			first = v
		}
		if first > 200 {
			first = 200
		}
		if s, ok := c["after"].(string); ok && s != "" {
			afterCursor, _ = decodeCursor(s)
		}
	}

	frag := ""
	filterArgs := []interface{}{}
	argN := 1
	if w, ok := req["filter"].(map[string]interface{}); ok && len(w) > 0 {
		var wErr error
		frag, filterArgs, argN, wErr = e.sqlGen.BuildWhereClause(model, w, argN)
		if wErr != nil {
			return nil, fmt.Errorf("filter: %w", wErr)
		}
	}

	table := compiler.SnakeCase(model.Name)
	countSQL := fmt.Sprintf(`SELECT COUNT(*) FROM %q WHERE "deleted_at" IS NULL`, table)
	if frag != "" {
		countSQL += " AND (" + frag + ")"
	}
	var total int
	e.execer(ctx).QueryRowContext(ctx, countSQL, filterArgs...).Scan(&total)

	queryArgs := append([]interface{}{}, filterArgs...)
	keyset := ""
	if afterCursor != nil {
		keyset = fmt.Sprintf(` AND ("created_at","id") > ($%d,$%d)`, argN, argN+1)
		queryArgs = append(queryArgs, afterCursor.CreatedAt, afterCursor.ID)
	}

	q := fmt.Sprintf(`SELECT * FROM %q WHERE "deleted_at" IS NULL`, table)
	if frag != "" {
		q += " AND (" + frag + ")"
	}
	q += keyset
	q += ` ORDER BY "created_at" ASC, "id" ASC`
	q += fmt.Sprintf(` LIMIT %d FOR SHARE`, first+1)

	rows, err := e.execer(ctx).QueryContext(ctx, q, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	rawRows, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	hasNext := len(rawRows) > first
	if hasNext {
		rawRows = rawRows[:first]
	}

	edges := make([]EdgeResult, len(rawRows))
	for i, row := range rawRows {
		maskRestrictedFields(row, model)
		ck := cursorKey{}
		if t, ok := row["created_at"]; ok {
			ck.CreatedAt, _ = t.(time.Time)
		}
		if id, ok := row["id"].(string); ok {
			ck.ID = id
		}
		edges[i] = EdgeResult{Node: row, Cursor: encodeCursor(ck)}
	}

	pi := PageInfoResult{
		HasNextPage: hasNext,
		TotalCount:  total,
	}
	if len(edges) > 0 {
		pi.StartCursor = edges[0].Cursor
		pi.EndCursor = edges[len(edges)-1].Cursor
	}

	return &ConnectionResult{Edges: edges, PageInfo: pi, TotalCount: total}, nil
}

type cursorKey struct {
	CreatedAt time.Time `json:"ca"`
	ID        string    `json:"id"`
}

func encodeCursor(ck cursorKey) string {
	b, _ := json.Marshal(ck)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeCursor(s string) (*cursorKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var ck cursorKey
	if err := json.Unmarshal(b, &ck); err != nil {
		return nil, err
	}
	return &ck, nil
}

func (e *Executor) UpdateMany(ctx context.Context, modelName string, req map[string]interface{}) (n int64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.update_many "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "update_many", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "update_many")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "update_many"),
	)

	op, model, err := e.resolveOp(modelName, "update")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}
	if op.Effect != nil || op.Compensate != nil {
		err := fmt.Errorf("updateMany is not supported when the update operation defines effect or compensate blocks")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	filter, _ := req["filter"].(map[string]interface{})
	rc, ctx := e.rootRC(ctx, req, "update", modelName)
	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	patch := map[string]interface{}{}
	pay := rc.payload()
	for _, field := range model.Fields {
		inKey, dbKey := fieldKeys(field)
		if val, ok := pay[inKey]; ok {
			patch[dbKey] = val
		}
	}
	if op.Modify != nil {
		mCtx, mSpan := telemetry.Tracer().Start(ctx, "fookie.modify")
		prevBlock := rc.blockType
		rc.blockType = "modify"
		for _, stmt := range op.Modify.Statements {
			switch s := stmt.(type) {
			case *ast.ModifyAssignment:
				val, err := e.evalExpr(mCtx, s.Value, rc)
				if err != nil {
					mSpan.RecordError(err)
					mSpan.SetStatus(codes.Error, err.Error())
					mSpan.End()
					rc.blockType = prevBlock
					return 0, fmt.Errorf("modify %s: %w", s.Field, err)
				}
				patch[resolveDBKey(s.Field, model)] = val
			case *ast.PredicateExpr:
				if _, err := e.evalExpr(mCtx, s.Expr, rc); err != nil {
					mSpan.RecordError(err)
					mSpan.SetStatus(codes.Error, err.Error())
					mSpan.End()
					rc.blockType = prevBlock
					return 0, fmt.Errorf("modify stmt: %w", err)
				}
			}
		}
		rc.blockType = prevBlock
		mSpan.End()
	}
	if len(patch) == 0 {
		return 0, fmt.Errorf("nothing to update")
	}

	sqlStr, args, err := e.sqlGen.CompileBulkUpdate(model, patch, filter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.update")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	res, err := e.db.ExecContext(dbCtx, sqlStr, args...)
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		dbSpan.End()
		return 0, fmt.Errorf("update many: %w", err)
	}
	n, _ = res.RowsAffected()
	dbSpan.SetAttributes(attribute.Int64("fookie.rows_affected", n))
	dbSpan.End()
	return n, nil
}

func (e *Executor) DeleteMany(ctx context.Context, modelName string, req map[string]interface{}) (n int64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.delete_many "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "delete_many", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "delete_many")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "delete_many"),
	)

	op, model, err := e.resolveOp(modelName, "delete")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}
	if op.Effect != nil || op.Compensate != nil {
		err := fmt.Errorf("deleteMany is not supported when the delete operation defines effect or compensate blocks")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	filter, _ := req["filter"].(map[string]interface{})
	rc, ctx := e.rootRC(ctx, req, "delete", modelName)
	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr, args, err := e.sqlGen.CompileBulkSoftDelete(model, filter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.delete")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	res, err := e.db.ExecContext(dbCtx, sqlStr, args...)
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		dbSpan.End()
		return 0, fmt.Errorf("delete many: %w", err)
	}
	n, _ = res.RowsAffected()
	dbSpan.SetAttributes(attribute.Int64("fookie.rows_affected", n))
	dbSpan.End()
	return n, nil
}

func (e *Executor) Update(ctx context.Context, modelName string, id string, req map[string]interface{}) (out map[string]interface{}, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.update "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "update", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "update")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "update"),
		attribute.String("fookie.id", id),
	)

	op, model, err := e.resolveOp(modelName, "update")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	rc, ctx := e.rootRC(ctx, req, "update", modelName)
	rc.output["id"] = id

	txBeginCtx, txBeginSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_begin",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "update"),
		),
	)
	tx, err := e.db.BeginTx(txBeginCtx, nil)
	if err != nil {
		txBeginSpan.RecordError(err)
		txBeginSpan.SetStatus(codes.Error, err.Error())
		txBeginSpan.End()
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	txBeginSpan.End()
	defer tx.Rollback()
	ctx = withTx(ctx, tx)

	existing, err := e.fetchByID(ctx, modelName, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("fetch existing: %w", err)
	}
	for k, v := range existing {
		rc.output[k] = v
	}

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return nil, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	patch := map[string]interface{}{}
	pay := rc.payload()
	for _, field := range model.Fields {
		inKey, dbKey := fieldKeys(field)
		if val, ok := pay[inKey]; ok {
			patch[dbKey] = val
		}
	}
	if op.Modify != nil {
		mCtx, mSpan := telemetry.Tracer().Start(ctx, "fookie.modify")
		prevBlock := rc.blockType
		rc.blockType = "modify"
		for _, stmt := range op.Modify.Statements {
			switch s := stmt.(type) {
			case *ast.ModifyAssignment:
				val, err := e.evalExpr(mCtx, s.Value, rc)
				if err != nil {
					mSpan.RecordError(err)
					mSpan.SetStatus(codes.Error, err.Error())
					mSpan.End()
					rc.blockType = prevBlock
					return nil, fmt.Errorf("modify %s: %w", s.Field, err)
				}
				if val != nil {
					patch[resolveDBKey(s.Field, model)] = val
				}
			case *ast.PredicateExpr:
				if _, err := e.evalExpr(mCtx, s.Expr, rc); err != nil {
					mSpan.RecordError(err)
					mSpan.SetStatus(codes.Error, err.Error())
					mSpan.End()
					rc.blockType = prevBlock
					return nil, fmt.Errorf("modify stmt: %w", err)
				}
			}
		}
		rc.blockType = prevBlock
		mSpan.End()
	}

	if len(patch) == 0 {
		_, txCommitSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_commit",
			trace.WithAttributes(
				attribute.String("fookie.model", modelName),
				attribute.String("fookie.operation", "update"),
			),
		)
		if err := tx.Commit(); err != nil {
			txCommitSpan.RecordError(err)
			txCommitSpan.SetStatus(codes.Error, err.Error())
			txCommitSpan.End()
			return nil, fmt.Errorf("commit: %w", err)
		}
		txCommitSpan.End()
		maskRestrictedFields(rc.output, model)
		return rc.output, nil
	}

	allLocks, err := collectLockTargetsForEntityAndEffect(ctx, e, modelName, id, op.Effect, rc)
	if err != nil {
		return nil, fmt.Errorf("lock targets: %w", err)
	}
	if err := e.acquireRowLocksGlobalOrder(ctx, allLocks); err != nil {
		return nil, err
	}

	sqlStr, keyOrder := e.sqlGen.CompileUpdate(model, patch)
	args := make([]interface{}, len(keyOrder)+1)
	for i, k := range keyOrder {
		args[i] = patch[k]
	}
	args[len(keyOrder)] = id

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.update")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var updatedAt time.Time
	var status string
	if err := e.execer(ctx).QueryRowContext(dbCtx, sqlStr, args...).Scan(&id, &updatedAt, &status); err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		dbSpan.End()
		return nil, fmt.Errorf("update: %w", err)
	}
	dbSpan.End()

	rc.output["updated_at"] = updatedAt
	rc.output["status"] = status
	for k, v := range patch {
		rc.output[k] = v
	}

	if op.Effect != nil {
		if err := e.runSyncEffectStatements(ctx, op.Effect, rc, id); err != nil {
			return nil, fmt.Errorf("sync effect: %w", err)
		}
		if _, err := e.queueEffects(ctx, op.Effect, op.Compensate, modelName, id, rc); err != nil {
			return nil, fmt.Errorf("queue effects: %w", err)
		}
	}

	_, txCommitSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_commit",
		trace.WithAttributes(
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "update"),
		),
	)
	if err := tx.Commit(); err != nil {
		txCommitSpan.RecordError(err)
		txCommitSpan.SetStatus(codes.Error, err.Error())
		txCommitSpan.End()
		return nil, fmt.Errorf("commit: %w", err)
	}
	txCommitSpan.End()

	maskRestrictedFields(rc.output, model)

	e.emit("updated", modelName, id, patch)
	return rc.output, nil
}

func (e *Executor) Delete(ctx context.Context, modelName string, id string, req map[string]interface{}) (err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.delete "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "delete", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "delete")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "delete"),
		attribute.String("fookie.id", id),
	)

	op, model, err := e.resolveOp(modelName, "delete")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	rc, ctx := e.rootRC(ctx, req, "delete", modelName)

	txBeginCtx, txBeginSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_begin",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "delete"),
		),
	)
	tx, err := e.db.BeginTx(txBeginCtx, nil)
	if err != nil {
		txBeginSpan.RecordError(err)
		txBeginSpan.SetStatus(codes.Error, err.Error())
		txBeginSpan.End()
		return fmt.Errorf("begin tx: %w", err)
	}
	txBeginSpan.End()
	defer tx.Rollback()
	ctx = withTx(ctx, tx)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return fmt.Errorf("rule: %w", err)
	}

	allLocks, err := collectLockTargetsForEntityAndEffect(ctx, e, modelName, id, op.Effect, rc)
	if err != nil {
		return fmt.Errorf("lock targets: %w", err)
	}
	if err := e.acquireRowLocksGlobalOrder(ctx, allLocks); err != nil {
		return err
	}
	if op.Effect != nil {
		if err := e.runSyncEffectStatements(ctx, op.Effect, rc, id); err != nil {
			return fmt.Errorf("sync effect: %w", err)
		}
	}

	sqlStr := e.sqlGen.CompileSoftDelete(model)
	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.delete")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	if _, err := e.execer(ctx).ExecContext(dbCtx, sqlStr, id); err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		dbSpan.End()
		return fmt.Errorf("soft-delete: %w", err)
	}
	dbSpan.End()

	if op.Effect != nil {
		if _, err := e.queueEffects(ctx, op.Effect, op.Compensate, modelName, id, rc); err != nil {
			return fmt.Errorf("queue effects: %w", err)
		}
	}

	_, txCommitSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_commit",
		trace.WithAttributes(
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "delete"),
		),
	)
	if err := tx.Commit(); err != nil {
		txCommitSpan.RecordError(err)
		txCommitSpan.SetStatus(codes.Error, err.Error())
		txCommitSpan.End()
		return fmt.Errorf("commit: %w", err)
	}
	txCommitSpan.End()

	e.emit("deleted", modelName, id, map[string]interface{}{"id": id})
	return nil
}

func (e *Executor) Restore(ctx context.Context, modelName string, id string, req map[string]interface{}) (err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.restore "+modelName)
	defer span.End()

	op, model, err := e.resolveOp(modelName, "delete")
	if err != nil {
		_, model, err = e.resolveOp(modelName, "update")
		if err != nil {
			return fmt.Errorf("restore: no delete or update op on model %s", modelName)
		}
		op = nil
	}

	rc, ctx := e.rootRC(ctx, req, "restore", modelName)

	txBeginCtx, txBeginSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_begin",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "restore"),
		),
	)
	tx, err := e.db.BeginTx(txBeginCtx, nil)
	if err != nil {
		txBeginSpan.RecordError(err)
		txBeginSpan.SetStatus(codes.Error, err.Error())
		txBeginSpan.End()
		return fmt.Errorf("begin tx: %w", err)
	}
	txBeginSpan.End()
	defer tx.Rollback()
	ctx = withTx(ctx, tx)

	if op != nil {
		if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
			return fmt.Errorf("role: %w", err)
		}
		if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
			return fmt.Errorf("rule: %w", err)
		}
	}

	table := compiler.SnakeCase(model.Name)
	if _, err := e.execer(ctx).ExecContext(ctx,
		fmt.Sprintf(`UPDATE %q SET "deleted_at" = NULL, "updated_at" = NOW() WHERE "id" = $1`, table),
		id,
	); err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	_, txCommitSpan := telemetry.Tracer().Start(ctx, "fookie.db.tx_commit",
		trace.WithAttributes(
			attribute.String("fookie.model", modelName),
			attribute.String("fookie.operation", "restore"),
		),
	)
	if err := tx.Commit(); err != nil {
		txCommitSpan.RecordError(err)
		txCommitSpan.SetStatus(codes.Error, err.Error())
		txCommitSpan.End()
		return fmt.Errorf("commit: %w", err)
	}
	txCommitSpan.End()

	e.emit("restored", modelName, id, map[string]interface{}{"id": id})
	return nil
}

func (e *Executor) execBlock(ctx context.Context, blockName string, block *ast.Block, rc *runCtx) error {
	if block == nil {
		return nil
	}

	if blockName == "role" && rc.isSystem {
		return nil
	}
	ctx, span := telemetry.Tracer().Start(ctx, "fookie."+blockName)
	defer span.End()

	prevBlock := rc.blockType
	rc.blockType = blockName
	defer func() { rc.blockType = prevBlock }()

	for _, stmt := range block.Statements {
		switch s := stmt.(type) {
		case *ast.Assignment:
			val, err := e.evalExpr(ctx, s.Value, rc)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return fmt.Errorf("assign %s: %w", s.Name, err)
			}
			if s.Name == "principal" {
				if m, ok := val.(map[string]interface{}); ok {
					for k, v := range m {
						rc.principal[k] = v
					}
				}
			} else {
				rc.vars[s.Name] = val
			}
			if m, ok := val.(map[string]interface{}); ok {
				ApplyHandlerSideEffects(ctx, e, m)
			}

		case *ast.PredicateExpr:
			val, err := e.evalExpr(ctx, s.Expr, rc)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return fmt.Errorf("predicate eval: %w", err)
			}
			if m, ok := val.(map[string]interface{}); ok {
				ApplyHandlerSideEffects(ctx, e, m)
			}
			if b, ok := val.(bool); ok && !b {
				err := fmt.Errorf("assertion failed")
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
			}

		case *ast.ForIn:
			iterVal, err := e.evalExpr(ctx, s.Iterable, rc)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return fmt.Errorf("for-in iterable: %w", err)
			}
			items, err := iterableToSlice(iterVal)
			if err != nil {
				return err
			}
			for _, elem := range items {
				rc.vars[s.Var] = elem
				if err := e.execBlock(ctx, blockName, s.Body, rc); err != nil {
					return err
				}
			}
			delete(rc.vars, s.Var)
		}
	}
	return nil
}

func (e *Executor) evalExpr(ctx context.Context, expr ast.Expression, rc *runCtx) (interface{}, error) {
	switch ex := expr.(type) {
	case *ast.Literal:
		return ex.Value, nil

	case *ast.FieldAccess:
		return rc.resolve(ex.Object, ex.Fields), nil

	case *ast.ExternalCall:
		params := make(map[string]interface{})
		for k, v := range ex.Params {
			val, err := e.evalExpr(ctx, v, rc)
			if err != nil {
				return nil, fmt.Errorf("param %s: %w", k, err)
			}
			params[k] = val
		}
		result, err := e.extMgr.Call(ctx, ex.Name, params)
		if err != nil {
			return nil, err
		}
		if err := e.validateExternalOutput(ex.Name, result); err != nil {
			return nil, err
		}
		return result, nil

	case *ast.BinaryOp:
		l, err := e.evalExpr(ctx, ex.Left, rc)
		if err != nil {
			return nil, err
		}
		r, err := e.evalExpr(ctx, ex.Right, rc)
		if err != nil {
			return nil, err
		}
		return evalBinary(l, ex.Op, r)

	case *ast.UnaryOp:
		r, err := e.evalExpr(ctx, ex.Right, rc)
		if err != nil {
			return nil, err
		}
		if b, ok := r.(bool); ok {
			return !b, nil
		}
		return nil, fmt.Errorf("unary ! requires bool")

	case *ast.InExpr:
		l, err := e.evalExpr(ctx, ex.Left, rc)
		if err != nil {
			return nil, err
		}
		for _, v := range ex.Values {
			r, err := e.evalExpr(ctx, v, rc)
			if err != nil {
				return nil, err
			}
			if l == r {
				return true, nil
			}
		}
		return false, nil

	case *ast.BuiltinCall:
		args := make([]interface{}, len(ex.Args))
		for i, arg := range ex.Args {
			val, err := e.evalExpr(ctx, arg, rc)
			if err != nil {
				return nil, fmt.Errorf("builtin arg %d: %w", i, err)
			}
			args[i] = val
		}
		if ex.Name == "log" {
			msg, fields := buildLogPayload(args)
			e.emitLog(ctx, rc, "info", msg, fields, ex.LineNo, "fsl")
			return true, nil
		}
		if ex.Name == "config" {
			if len(args) != 1 {
				return nil, fmt.Errorf("config requires 1 argument (key)")
			}
			key, ok := args[0].(string)
			if !ok || key == "" {
				return nil, fmt.Errorf("config key must be a non-empty string")
			}
			val, ok := e.configValue(key)
			if !ok {
				return nil, fmt.Errorf("unknown config key %q", key)
			}
			return val, nil
		}
		fn, ok := validator.GetBuiltin(ex.Name)
		if !ok {
			return nil, fmt.Errorf("unknown builtin validator: %s", ex.Name)
		}
		return fn(args...)

	case *ast.ArrayLiteral:
		out := make([]interface{}, len(ex.Items))
		for i, item := range ex.Items {
			v, err := e.evalExpr(ctx, item, rc)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported expression: %T", expr)
}

func (e *Executor) configValue(key string) (interface{}, bool) {
	if e == nil || e.schema == nil {
		return nil, false
	}
	for i := len(e.schema.Configs) - 1; i >= 0; i-- {
		c := e.schema.Configs[i]
		if c != nil && c.Key == key {
			return c.Value, true
		}
	}
	return nil, false
}

func iterableToSlice(v interface{}) ([]interface{}, error) {
	if v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case []interface{}:
		return t, nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("for-in requires array or slice, got %T", v)
	}
	n := rv.Len()
	out := make([]interface{}, n)
	for i := 0; i < n; i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, nil
}

func evalBinary(l interface{}, op string, r interface{}) (interface{}, error) {
	switch op {
	case "==":
		return l == r, nil
	case "!=":
		return l != r, nil
	case "&&":
		lb, _ := l.(bool)
		rb, _ := r.(bool)
		return lb && rb, nil
	case "||":
		lb, _ := l.(bool)
		rb, _ := r.(bool)
		return lb || rb, nil
	}

	lf, lok := toFloat(l)
	rf, rok := toFloat(r)
	if !lok || !rok {
		return nil, fmt.Errorf("numeric operator %s requires numbers, got %T and %T", op, l, r)
	}
	switch op {
	case ">":
		return lf > rf, nil
	case ">=":
		return lf >= rf, nil
	case "<":
		return lf < rf, nil
	case "<=":
		return lf <= rf, nil
	case "+":
		return lf + rf, nil
	case "-":
		return lf - rf, nil
	case "*":
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return lf / rf, nil
	}
	return nil, fmt.Errorf("unknown operator: %s", op)
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case float32:
		return float64(n), true
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f, true
		}
	case []byte:
		if f, err := strconv.ParseFloat(string(n), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func (e *Executor) fetchByID(ctx context.Context, modelName string, id string) (map[string]interface{}, error) {
	table := compiler.SnakeCase(modelName)
	rows, err := e.execer(ctx).QueryContext(ctx,
		fmt.Sprintf(`SELECT * FROM "%s" WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, table), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results, err := scanRows(rows)
	if err != nil || len(results) == 0 {
		return nil, fmt.Errorf("%s %s not found", modelName, id)
	}
	return results[0], nil
}

func extractCall(stmt ast.Statement, ctx context.Context, e *Executor, rc *runCtx) (string, string, map[string]interface{}) {
	switch s := stmt.(type) {
	case *ast.Assignment:
		if call, ok := s.Value.(*ast.ExternalCall); ok {
			return call.Name, s.Name, evalParams(ctx, call.Params, e, rc)
		}
	case *ast.PredicateExpr:
		if call, ok := s.Expr.(*ast.ExternalCall); ok {
			return call.Name, "", evalParams(ctx, call.Params, e, rc)
		}
	}
	return "", "", nil
}

func (e *Executor) queueEffects(ctx context.Context, effect *ast.Block, compensate *ast.Block, entityType, entityID string, rc *runCtx) (queuedAsync bool, err error) {
	sagaID := uuid.New().String()

	for step, stmt := range effect.Statements {
		extName, targetField, params := extractCall(stmt, ctx, e, rc)
		if extName == "" {
			continue
		}

		var runAfter *time.Time
		var recurCron *string
		if v, ok := params["run_after"]; ok {
			if t := toTimeValue(v); t != nil {
				runAfter = t
			}
			delete(params, "run_after")
		}
		if v, ok := params["recur_cron"]; ok {
			if s, ok2 := v.(string); ok2 {
				recurCron = &s
			}
			delete(params, "recur_cron")
		}

		payload, _ := json.Marshal(params)
		var targetFieldVal interface{}
		if targetField != "" {
			targetFieldVal = targetField
		}
		var insertedID string
		err := e.execer(ctx).QueryRowContext(ctx, `
			INSERT INTO outbox (entity_type, entity_id, external_name, payload, saga_id, saga_step, is_compensation, target_field, run_after, recur_cron, root_request_id)
			VALUES ($1, $2, $3, $4, $5, $6, FALSE, $7, $8, $9, $10)
			RETURNING id`,
			entityType, entityID, extName, payload, sagaID, step, targetFieldVal, runAfter, recurCron, rc.rootRequestID,
		).Scan(&insertedID)
		if err != nil {
			return false, fmt.Errorf("queue %s: %w", extName, err)
		}
		e.notifyOutbox(insertedID)
		queuedAsync = true
	}

	if compensate != nil {
		for step, stmt := range compensate.Statements {
			extName, _, params := extractCall(stmt, ctx, e, rc)
			if extName == "" {
				continue
			}
			payload, _ := json.Marshal(params)
			_, err := e.execer(ctx).ExecContext(ctx, `
				INSERT INTO outbox (entity_type, entity_id, external_name, payload, saga_id, saga_step, is_compensation, status, root_request_id)
				VALUES ($1, $2, $3, $4, $5, $6, TRUE, 'held', $7)`,
				entityType, entityID, extName, payload, sagaID, step, rc.rootRequestID,
			)
			if err != nil {
				return queuedAsync, fmt.Errorf("queue compensation %s: %w", extName, err)
			}
			queuedAsync = true
		}
	}

	return queuedAsync, nil
}

func evalParams(ctx context.Context, rawParams map[string]ast.Expression, e *Executor, rc *runCtx) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range rawParams {
		val, _ := e.evalExpr(ctx, v, rc)
		out[k] = val
	}
	return out
}

func syncEffectStatements(effect *ast.Block) []ast.Statement {
	if effect == nil {
		return nil
	}
	var out []ast.Statement
	for _, s := range effect.Statements {
		switch s.(type) {
		case *ast.EffectUpdateStmt, *ast.EffectDeleteStmt, *ast.EffectNotifyStmt:
			out = append(out, s)
		}
	}
	return out
}

func (e *Executor) applyEffectUpdateRow(ctx context.Context, modelName, id string, fields []*ast.ModifyAssignment, rc *runCtx) error {
	_, model, err := e.resolveOp(modelName, "update")
	if err != nil {
		return err
	}

	entityRC := rc
	table := compiler.SnakeCase(modelName)
	rows, qErr := e.execer(ctx).QueryContext(ctx,
		fmt.Sprintf(`SELECT * FROM %q WHERE id = $1 AND "deleted_at" IS NULL`, table), id)
	if qErr == nil {
		if scanned, sErr := scanRows(rows); sErr == nil && len(scanned) > 0 {
			child := &runCtx{
				req:           rc.req,
				body:          rc.body,
				principal:     rc.principal,
				output:        rc.output,
				isSystem:      rc.isSystem,
				rootRequestID: rc.rootRequestID,
				operation:     rc.operation,
				modelName:     rc.modelName,
				blockType:     rc.blockType,
				depth:         rc.depth,
			}
			child.vars = make(map[string]interface{}, len(rc.vars)+len(scanned[0]))
			for k, v := range rc.vars {
				child.vars[k] = v
			}
			for k, v := range scanned[0] {
				child.vars[k] = v
			}
			entityRC = child
		}
		rows.Close()
	}

	patch := make(map[string]interface{}, len(fields))
	for _, ma := range fields {
		val, err := e.evalExpr(ctx, ma.Value, entityRC)
		if err != nil {
			return fmt.Errorf("effect update %s.%s: %w", modelName, ma.Field, err)
		}
		patch[resolveDBKey(ma.Field, model)] = val
	}
	if len(patch) == 0 {
		return nil
	}
	sqlStr, keyOrder := e.sqlGen.CompileUpdate(model, patch)
	args := make([]interface{}, len(keyOrder)+1)
	for i, k := range keyOrder {
		args[i] = patch[k]
	}
	args[len(keyOrder)] = id
	var rid string
	var updatedAt time.Time
	var status string
	if err := e.execer(ctx).QueryRowContext(ctx, sqlStr, args...).Scan(&rid, &updatedAt, &status); err != nil {
		return fmt.Errorf("effect update %s %s: %w", modelName, id, err)
	}
	return nil
}

func (e *Executor) applyEffectSoftDeleteRow(ctx context.Context, modelName, id string) error {
	_, model, err := e.resolveOp(modelName, "delete")
	if err != nil {
		return err
	}
	sqlStr := e.sqlGen.CompileSoftDelete(model)
	if _, err := e.execer(ctx).ExecContext(ctx, sqlStr, id); err != nil {
		return fmt.Errorf("effect delete %s %s: %w", modelName, id, err)
	}
	return nil
}

func (e *Executor) executeEffectStmtList(ctx context.Context, stmts []ast.Statement, rc *runCtx) error {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.EffectUpdateStmt:
			idVal, err := e.evalExpr(ctx, s.IDExpr, rc)
			if err != nil {
				return fmt.Errorf("effect update %s id expr: %w", s.Model, err)
			}
			id, _ := idVal.(string)
			if id == "" {
				continue
			}
			if err := e.applyEffectUpdateRow(ctx, s.Model, id, s.Fields, rc); err != nil {
				return err
			}
		case *ast.EffectDeleteStmt:
			idVal, err := e.evalExpr(ctx, s.IDExpr, rc)
			if err != nil {
				return fmt.Errorf("effect delete %s id expr: %w", s.Model, err)
			}
			id, _ := idVal.(string)
			if id == "" {
				continue
			}
			if err := e.applyEffectSoftDeleteRow(ctx, s.Model, id); err != nil {
				return err
			}
		case *ast.EffectNotifyStmt:
			if e.roomBus == nil {
				continue
			}
			roomID, ok := e.roomIDByName(s.RoomName)
			if !ok {
				return fmt.Errorf("notify: room %q not registered (missing setup block?)", s.RoomName)
			}

			reserved := map[string]bool{"method": true, "model": true, "record_id": true}
			msg := make(map[string]interface{}, len(s.Payload)+1)
			msg["room_id"] = roomID
			extra := make(map[string]interface{})
			for k, expr := range s.Payload {
				val, err := e.evalExpr(ctx, expr, rc)
				if err != nil {
					return fmt.Errorf("notify payload %q: %w", k, err)
				}
				if reserved[k] {
					msg[k] = val
				} else {
					extra[k] = val
				}
			}
			if len(extra) > 0 {
				if b, jErr := json.Marshal(extra); jErr == nil {
					msg["payload"] = map[string]interface{}{"body": string(b)}
				}
			}
			e.roomBus.Publish(roomID, msg)
		}
	}
	return nil
}

func (e *Executor) runSyncEffectStatements(ctx context.Context, effect *ast.Block, rc *runCtx, entityID string) error {
	if effect == nil {
		return nil
	}
	rc.output["id"] = entityID
	rc.vars["id"] = entityID
	return e.executeEffectStmtList(ctx, syncEffectStatements(effect), rc)
}

func (e *Executor) ExecuteEffectActions(ctx context.Context, stmts []ast.Statement, input map[string]interface{}, vars map[string]interface{}, entityID string) error {
	rc := newRunCtx(WithSystemBody(input))
	for k, v := range vars {
		rc.vars[k] = v
	}
	rc.output["id"] = entityID
	rc.vars["id"] = entityID
	return e.executeEffectStmtList(ctx, stmts, rc)
}

func (e *Executor) resolveOp(modelName, opType string) (*ast.Operation, *ast.Model, error) {
	for _, m := range e.schema.Models {
		if strings.EqualFold(m.Name, modelName) {
			op, ok := m.CRUD[opType]
			if !ok {
				return nil, nil, fmt.Errorf("model %s has no %s operation", modelName, opType)
			}
			return op, m, nil
		}
	}
	return nil, nil, fmt.Errorf("model %s not found", modelName)
}

func scanRows(rows *sql.Rows) ([]map[string]interface{}, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]interface{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{})
		for i, col := range cols {
			row[col] = normalizeScanValue(vals[i])
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func normalizeScanValue(v interface{}) interface{} {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return v
	}
}

type runCtx struct {
	req       map[string]interface{}
	body      map[string]interface{}
	principal map[string]interface{}
	output    map[string]interface{}
	vars      map[string]interface{}
	isSystem  bool

	rootRequestID string
	operation     string
	modelName     string
	blockType     string
	depth         int
}

func newRunCtx(req map[string]interface{}) *runCtx {
	if req == nil {
		req = map[string]interface{}{}
	}
	rc := &runCtx{
		req:       req,
		principal: make(map[string]interface{}),
		output:    make(map[string]interface{}),
		vars:      make(map[string]interface{}),
	}
	if b, ok := req["body"].(map[string]interface{}); ok {
		rc.body = b
	}
	if v, ok := rc.req["__system"]; ok && v == true {
		rc.isSystem = true
		rc.principal["is_system"] = true
		delete(rc.req, "__system")
	}
	return rc
}

func (rc *runCtx) payload() map[string]interface{} {
	if rc.body != nil {
		return rc.body
	}
	skip := map[string]bool{"filter": true, "cursor": true, "token": true, "admin_key": true, "body": true}
	out := make(map[string]interface{})
	for k, v := range rc.req {
		if !skip[k] {
			out[k] = v
		}
	}
	return out
}

func (rc *runCtx) resolve(object string, fields []string) interface{} {
	var base interface{}
	switch object {
	case "body":
		base = rc.payload()
	case "request":
		base = rc.req
	case "filter":
		base = rc.req["filter"]
	case "principal":
		base = rc.principal
	case "output":
		base = rc.output
	default:
		base = rc.vars[object]
	}

	for _, f := range fields {
		if m, ok := base.(map[string]interface{}); ok {
			base = m[f]
		} else {
			return nil
		}
	}
	return base
}

func (e *Executor) validateExternalOutput(name string, result map[string]interface{}) error {
	for _, ext := range e.schema.Externals {
		if ext.Name != name {
			continue
		}
		for fieldName, fieldType := range ext.Output {
			val, exists := result[fieldName]
			if !exists {
				continue
			}
			if err := checkType(val, fieldType); err != nil {
				return fmt.Errorf("external %s.%s: %w", name, fieldName, err)
			}
		}
		return nil
	}
	return nil
}

func checkType(val interface{}, typeName string) error {
	if val == nil {
		return fmt.Errorf("expected %s, got nil", typeName)
	}
	switch typeName {
	case "string", "email", "url", "phone", "iban", "ipaddress", "color", "currency", "locale", "uuid", "id", "date", "timestamp":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("expected %s (string), got %T", typeName, val)
		}
	case "number":
		switch val.(type) {
		case float64, int, int64, float32:
		default:
			return fmt.Errorf("expected number, got %T", typeName)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", val)
		}
	}
	return nil
}

func fieldKeys(f *ast.Field) (string, string) {
	if f.Type == ast.TypeRelation {
		return f.Name + "_id", compiler.SnakeCase(f.Name) + "_id"
	}
	return f.Name, compiler.SnakeCase(f.Name)
}

func resolveDBKey(fieldName string, model *ast.Model) string {
	for _, f := range model.Fields {
		if f.Name == fieldName || compiler.SnakeCase(f.Name) == compiler.SnakeCase(fieldName) {
			_, dbKey := fieldKeys(f)
			return dbKey
		}
	}

	return compiler.SnakeCase(fieldName)
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func toTimeValue(v interface{}) *time.Time {
	switch t := v.(type) {
	case time.Time:
		return &t
	case *time.Time:
		return t
	case string:
		parsed, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return nil
		}
		return &parsed
	}
	return nil
}

func maskRestrictedFields(row map[string]interface{}, model *ast.Model) {
	if model == nil {
		return
	}
	for _, f := range model.Fields {

		hasRestricted := false
		for _, c := range f.Constraints {
			if c == "--restricted" {
				hasRestricted = true
				break
			}
		}
		if !hasRestricted {
			continue
		}

		_, dbKey := fieldKeys(f)

		if val, ok := row[dbKey]; ok {
			switch v := val.(type) {
			case string:
				if len(v) >= 3 {
					row[dbKey] = "..." + v[len(v)-3:]
				} else if len(v) > 0 {
					row[dbKey] = "..." + v
				} else {
					row[dbKey] = "..."
				}
			case []byte:
				str := string(v)
				if len(str) >= 3 {
					row[dbKey] = "..." + str[len(str)-3:]
				} else if len(str) > 0 {
					row[dbKey] = "..." + str
				} else {
					row[dbKey] = "..."
				}
			}
		}
	}
}

func (e *Executor) Sum(ctx context.Context, modelName, field string, req map[string]interface{}) (result float64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.sum "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "sum", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "sum")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "sum"),
		attribute.String("fookie.field", field),
	)

	op, model, err := e.resolveOp(modelName, "sum")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	rc, ctx := e.rootRC(ctx, req, "sum", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileSumQuery(model, field, op)
	e.logger.Info("sum query", "sql", sqlStr)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.aggregate")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var val sql.NullFloat64
	err = e.execer(ctx).QueryRowContext(dbCtx, sqlStr).Scan(&val)
	dbSpan.End()
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("query: %w", err)
	}

	if val.Valid {
		result = val.Float64
	}

	if err := e.execBlock(ctx, "modify", op.Modify, rc); err != nil {
		return 0, fmt.Errorf("modify: %w", err)
	}

	rc.vars["result"] = result
	if err := e.execBlock(ctx, "effect", op.Effect, rc); err != nil {
		return 0, fmt.Errorf("effect: %w", err)
	}

	return result, nil
}

func (e *Executor) Count(ctx context.Context, modelName string, req map[string]interface{}) (result float64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.count "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "count", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "count")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "count"),
	)

	op, model, err := e.resolveOp(modelName, "count")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	rc, ctx := e.rootRC(ctx, req, "count", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileCountQuery(model, op)
	e.logger.Info("count query", "sql", sqlStr)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.aggregate")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var val int64
	err = e.execer(ctx).QueryRowContext(dbCtx, sqlStr).Scan(&val)
	dbSpan.End()
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("query: %w", err)
	}

	result = float64(val)

	if err := e.execBlock(ctx, "modify", op.Modify, rc); err != nil {
		return 0, fmt.Errorf("modify: %w", err)
	}

	rc.vars["result"] = result
	if err := e.execBlock(ctx, "effect", op.Effect, rc); err != nil {
		return 0, fmt.Errorf("effect: %w", err)
	}

	return result, nil
}

func (e *Executor) Avg(ctx context.Context, modelName, field string, req map[string]interface{}) (result float64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.avg "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "avg", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "avg")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "avg"),
		attribute.String("fookie.field", field),
	)

	op, model, err := e.resolveOp(modelName, "avg")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	rc, ctx := e.rootRC(ctx, req, "avg", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileAvgQuery(model, field, op)
	e.logger.Info("avg query", "sql", sqlStr)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.aggregate")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var val sql.NullFloat64
	err = e.execer(ctx).QueryRowContext(dbCtx, sqlStr).Scan(&val)
	dbSpan.End()
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("query: %w", err)
	}

	if val.Valid {
		result = val.Float64
	}

	if err := e.execBlock(ctx, "modify", op.Modify, rc); err != nil {
		return 0, fmt.Errorf("modify: %w", err)
	}

	rc.vars["result"] = result
	if err := e.execBlock(ctx, "effect", op.Effect, rc); err != nil {
		return 0, fmt.Errorf("effect: %w", err)
	}

	return result, nil
}

func (e *Executor) Min(ctx context.Context, modelName, field string, req map[string]interface{}) (result float64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.min "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "min", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "min")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "min"),
		attribute.String("fookie.field", field),
	)

	op, model, err := e.resolveOp(modelName, "min")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	rc, ctx := e.rootRC(ctx, req, "min", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileMinQuery(model, field, op)
	e.logger.Info("min query", "sql", sqlStr)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.aggregate")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var val sql.NullFloat64
	err = e.execer(ctx).QueryRowContext(dbCtx, sqlStr).Scan(&val)
	dbSpan.End()
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("query: %w", err)
	}

	if val.Valid {
		result = val.Float64
	}

	if err := e.execBlock(ctx, "modify", op.Modify, rc); err != nil {
		return 0, fmt.Errorf("modify: %w", err)
	}

	rc.vars["result"] = result
	if err := e.execBlock(ctx, "effect", op.Effect, rc); err != nil {
		return 0, fmt.Errorf("effect: %w", err)
	}

	return result, nil
}

func (e *Executor) Max(ctx context.Context, modelName, field string, req map[string]interface{}) (result float64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.max "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "max", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "max")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "max"),
		attribute.String("fookie.field", field),
	)

	op, model, err := e.resolveOp(modelName, "max")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	rc, ctx := e.rootRC(ctx, req, "max", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileMaxQuery(model, field, op)
	e.logger.Info("max query", "sql", sqlStr)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.aggregate")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var val sql.NullFloat64
	err = e.execer(ctx).QueryRowContext(dbCtx, sqlStr).Scan(&val)
	dbSpan.End()
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("query: %w", err)
	}

	if val.Valid {
		result = val.Float64
	}

	if err := e.execBlock(ctx, "modify", op.Modify, rc); err != nil {
		return 0, fmt.Errorf("modify: %w", err)
	}

	rc.vars["result"] = result
	if err := e.execBlock(ctx, "effect", op.Effect, rc); err != nil {
		return 0, fmt.Errorf("effect: %w", err)
	}

	return result, nil
}

func (e *Executor) Stddev(ctx context.Context, modelName, field string, req map[string]interface{}) (result float64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.stddev "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "stddev", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "stddev")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "stddev"),
		attribute.String("fookie.field", field),
	)

	op, model, err := e.resolveOp(modelName, "stddev")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	rc, ctx := e.rootRC(ctx, req, "stddev", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileStddevQuery(model, field, op)
	e.logger.Info("stddev query", "sql", sqlStr)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.aggregate")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var val sql.NullFloat64
	err = e.execer(ctx).QueryRowContext(dbCtx, sqlStr).Scan(&val)
	dbSpan.End()
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("query: %w", err)
	}

	if val.Valid {
		result = val.Float64
	}

	if err := e.execBlock(ctx, "modify", op.Modify, rc); err != nil {
		return 0, fmt.Errorf("modify: %w", err)
	}

	rc.vars["result"] = result
	if err := e.execBlock(ctx, "effect", op.Effect, rc); err != nil {
		return 0, fmt.Errorf("effect: %w", err)
	}

	return result, nil
}

func (e *Executor) Variance(ctx context.Context, modelName, field string, req map[string]interface{}) (result float64, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "fookie.variance "+modelName)
	defer span.End()
	opStart := time.Now()
	defer func() {
		st := "ok"
		if err != nil {
			st = "error"
		}
		telemetry.RecordExecutorOperation(modelName, "variance", st, time.Since(opStart).Seconds())
	}()
	defer telemetry.BeginExecutorOp(modelName, "variance")()
	span.SetAttributes(
		attribute.String("fookie.model", modelName),
		attribute.String("fookie.operation", "variance"),
		attribute.String("fookie.field", field),
	)

	op, model, err := e.resolveOp(modelName, "variance")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	rc, ctx := e.rootRC(ctx, req, "variance", modelName)

	if err := e.execBlock(ctx, "role", op.Role, rc); err != nil {
		return 0, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, "rule", op.Rule, rc); err != nil {
		return 0, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileVarianceQuery(model, field, op)
	e.logger.Info("variance query", "sql", sqlStr)

	dbCtx, dbSpan := telemetry.Tracer().Start(ctx, "fookie.db.aggregate")
	dbSpan.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", sqlStr),
	)
	var val sql.NullFloat64
	err = e.execer(ctx).QueryRowContext(dbCtx, sqlStr).Scan(&val)
	dbSpan.End()
	if err != nil {
		dbSpan.RecordError(err)
		dbSpan.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("query: %w", err)
	}

	if val.Valid {
		result = val.Float64
	}

	if err := e.execBlock(ctx, "modify", op.Modify, rc); err != nil {
		return 0, fmt.Errorf("modify: %w", err)
	}

	rc.vars["result"] = result
	if err := e.execBlock(ctx, "effect", op.Effect, rc); err != nil {
		return 0, fmt.Errorf("effect: %w", err)
	}

	return result, nil
}
