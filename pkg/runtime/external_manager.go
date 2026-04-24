package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/events"
	"github.com/redis/go-redis/v9"
)

type Store interface {
	Read(ctx context.Context, model string, args map[string]interface{}) ([]map[string]interface{}, error)
	Create(ctx context.Context, model string, body map[string]interface{}) (map[string]interface{}, error)
	Update(ctx context.Context, model string, id string, patch map[string]interface{}) (map[string]interface{}, error)
	Delete(ctx context.Context, model string, id string) error
}

type ReadStore = Store

type ExternalHandler func(ctx context.Context, input map[string]interface{}, store Store) (map[string]interface{}, error)

type ExternalManager struct {
	handlers map[string]ExternalHandler
	mu       sync.RWMutex
	cache    map[string]*CachedResult
	store    Store
	roomBus  *events.RoomBus
}

type CachedResult struct {
	Result    map[string]interface{}
	Timestamp time.Time
	TTL       time.Duration
}

func NewExternalManager() *ExternalManager {
	return &ExternalManager{
		handlers: make(map[string]ExternalHandler),
		cache:    make(map[string]*CachedResult),
	}
}

func (em *ExternalManager) Register(name string, handler ExternalHandler) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.handlers[name] = handler
}

func (em *ExternalManager) SetRoomBus(b *events.RoomBus) {
	em.roomBus = b
}

func (em *ExternalManager) Call(ctx context.Context, name string, input map[string]interface{}) (map[string]interface{}, error) {
	em.mu.RLock()
	handler, exists := em.handlers[name]
	em.mu.RUnlock()

	if !exists {
		return em.handleBuiltin(ctx, name, input)
	}

	cacheKey := fmt.Sprintf("%s:%v", name, input)
	if cached := em.getCached(cacheKey); cached != nil {
		return cached, nil
	}

	result, err := em.callWithRetry(ctx, handler, input)
	if err != nil {
		return nil, fmt.Errorf("external %s failed: %v", name, err)
	}

	if !resultHasHandlerSideEffects(result) {
		em.cache[cacheKey] = &CachedResult{
			Result:    result,
			Timestamp: time.Now(),
			TTL:       5 * time.Minute,
		}
	}

	return result, nil
}

func (em *ExternalManager) callWithRetry(ctx context.Context, handler ExternalHandler, input map[string]interface{}) (map[string]interface{}, error) {
	maxRetries := 3
	backoff := 100 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		result, err := handler(ctx, input, em.store)
		if err == nil {
			return result, nil
		}

		lastErr = err
		if attempt < maxRetries-1 {
			select {
			case <-time.After(backoff):
				backoff *= 2
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	return nil, fmt.Errorf("external call failed after %d retries: %v", maxRetries, lastErr)
}

func (em *ExternalManager) getCached(key string) map[string]interface{} {
	em.mu.RLock()
	defer em.mu.RUnlock()

	cached, exists := em.cache[key]
	if !exists {
		return nil
	}

	if time.Since(cached.Timestamp) > cached.TTL {
		return nil
	}

	return cached.Result
}

const demoOperatorUserID = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"

func (em *ExternalManager) handleBuiltin(ctx context.Context, name string, input map[string]interface{}) (map[string]interface{}, error) {
	switch name {
	case "ValidateToken":
		return em.handleValidateToken(input)
	case "FraudCheck":
		return em.handleFraudCheck(input)
	case "SendTransferNotification":
		return map[string]interface{}{"messageId": "stub", "sent": true}, nil
	case "EvaluateAllocationRisk":
		return em.handleEvaluateAllocationRisk(input)
	case "EmitInventorySignal":
		return map[string]interface{}{"signalId": "sig-stub-" + time.Now().Format("150405"), "delivered": true}, nil
	case "RollbackInventorySignal":
		return map[string]interface{}{"ok": true}, nil
	case "RoomGraphQLNotify":
		return em.handleRoomGraphQLNotify(input)
	default:
		return nil, fmt.Errorf("unknown external: %s", name)
	}
}

func (em *ExternalManager) handleRoomGraphQLNotify(input map[string]interface{}) (map[string]interface{}, error) {
	if em.roomBus == nil {
		return map[string]interface{}{"delivered": false}, nil
	}
	roomID, _ := input["room_id"].(string)
	if roomID == "" {
		return nil, fmt.Errorf("room_id is required")
	}
	method, _ := input["method"].(string)
	if method == "" {
		return nil, fmt.Errorf("method is required")
	}
	msg := map[string]interface{}{
		"room_id": roomID,
		"method":  method,
	}
	if model, ok := input["model"].(string); ok && model != "" {
		msg["model"] = model
	}
	if rid, ok := input["record_id"].(string); ok && rid != "" {
		msg["record_id"] = rid
	}
	payload := map[string]interface{}{}
	if q, ok := input["query"].(string); ok && q != "" {
		payload["query"] = q
	}
	if body, ok := input["body"]; ok && body != nil {
		switch b := body.(type) {
		case string:
			if b != "" {
				payload["body"] = b
			}
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				return nil, fmt.Errorf("body: %w", err)
			}
			payload["body"] = string(raw)
		}
	}
	if len(payload) > 0 {
		msg["payload"] = payload
	}
	em.roomBus.Publish(roomID, msg)
	return map[string]interface{}{"delivered": true}, nil
}

func (em *ExternalManager) handleValidateToken(input map[string]interface{}) (map[string]interface{}, error) {
	token, ok := input["token"].(string)
	if !ok || token == "" {
		return map[string]interface{}{"valid": false}, fmt.Errorf("invalid token format")
	}
	return map[string]interface{}{
		"valid":     true,
		"userId":    demoOperatorUserID,
		"issuer":    "fookie-auth",
		"expiresAt": time.Now().Add(24 * time.Hour),
	}, nil
}

func (em *ExternalManager) handleEvaluateAllocationRisk(input map[string]interface{}) (map[string]interface{}, error) {
	delta, ok := toFloat64(input["delta"])
	if !ok {
		return nil, fmt.Errorf("invalid delta")
	}
	abs := delta
	if abs < 0 {
		abs = -abs
	}
	return map[string]interface{}{"allowed": abs <= 9000, "score": abs / 100.0}, nil
}

func toFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func (em *ExternalManager) handleFraudCheck(input map[string]interface{}) (map[string]interface{}, error) {
	amount, ok := input["amount"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid amount")
	}
	return map[string]interface{}{"allowed": amount <= 10000, "score": int(amount / 100)}, nil
}

type outboxJob struct {
	id            string
	entityType    string
	entityID      sql.NullString
	externalName  string
	payload       []byte
	sagaID        sql.NullString
	sagaStep      int
	retryCount    int
	targetField   sql.NullString
	runAfter      sql.NullTime
	recurCron     sql.NullString
	rootRequestID sql.NullString
}

type OutboxProcessor struct {
	manager *ExternalManager
	exec    *Executor
	db      *sql.DB
	ticker  *time.Ticker
	done    chan struct{}
	rdb     *redis.Client // optional: nil = poll mode
}

func NewOutboxProcessor(exec *Executor) *OutboxProcessor {
	return &OutboxProcessor{
		manager: exec.ExternalManager(),
		exec:    exec,
		db:      exec.DB(),
		done:    make(chan struct{}),
	}
}

func NewOutboxProcessorWithRedis(exec *Executor, rdb *redis.Client) *OutboxProcessor {
	return &OutboxProcessor{
		manager: exec.ExternalManager(),
		exec:    exec,
		db:      exec.DB(),
		done:    make(chan struct{}),
		rdb:     rdb,
	}
}

// NotifyNewOutboxItem pushes the outbox row ID to Redis so workers pick it up instantly.
// Falls back silently if Redis not configured.
func (op *OutboxProcessor) NotifyNewOutboxItem(id string) {
	if op.rdb == nil {
		return
	}
	op.rdb.LPush(context.Background(), "fookie:outbox:pending", id)
}

func (op *OutboxProcessor) systemUpdateEntity(ctx context.Context, modelName, id string, input map[string]interface{}) error {
	if op.exec == nil {
		return fmt.Errorf("executor is nil")
	}
	_, err := op.exec.Update(ctx, modelName, id, WithSystemBody(input))
	return err
}

// Start begins processing. If Redis is configured, uses BLPOP (instant).
// Otherwise falls back to ticker-based polling.
func (op *OutboxProcessor) Start(interval time.Duration) {
	if op.rdb != nil {
		go op.runRedisMode()
		return
	}
	op.ticker = time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-op.ticker.C:
				op.processPending()
			case <-op.done:
				op.ticker.Stop()
				return
			}
		}
	}()
}

// runRedisMode uses BLPOP for instant, zero-poll outbox consumption.
func (op *OutboxProcessor) runRedisMode() {
	ctx := context.Background()
	for {
		select {
		case <-op.done:
			return
		default:
		}

		// BLPOP blocks until an item appears (or 2s timeout to check done channel)
		blpopResult := op.rdb.BLPop(ctx, 2*time.Second, "fookie:outbox:pending")
		if blpopResult.Err() != nil {
			// timeout or transient error — check done and loop
			continue
		}

		// Signal received — process pending items (any worker can process any item).
		op.processPending()
	}
}

func (op *OutboxProcessor) Stop() {
	close(op.done)
}

func (op *OutboxProcessor) processPending() {
	if op.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	op.processForwardStep(ctx)
	op.processCompensationStep(ctx)
}

func (op *OutboxProcessor) processForwardStep(ctx context.Context) {
	tx, err := op.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()

	var job outboxJob
	err = tx.QueryRowContext(ctx, `
		SELECT id, entity_type, entity_id, external_name, payload,
		       saga_id, saga_step, retry_count, target_field,
		       run_after, recur_cron, root_request_id
		FROM outbox
		WHERE status = 'pending'
		  AND is_compensation = FALSE
		  AND retry_count < 3
		  AND (run_after IS NULL OR run_after <= NOW())
		ORDER BY COALESCE(run_after, created_at) ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(&job.id, &job.entityType, &job.entityID, &job.externalName, &job.payload,
		&job.sagaID, &job.sagaStep, &job.retryCount, &job.targetField,
		&job.runAfter, &job.recurCron, &job.rootRequestID)

	if err == sql.ErrNoRows {
		tx.Commit()
		return
	}
	if err != nil {
		return
	}

	var params map[string]interface{}
	json.Unmarshal(job.payload, &params)

	dispatchCtx := ctx
	if job.entityType != "cron" && job.rootRequestID.Valid && job.rootRequestID.String != "" {
		dispatchCtx = withRootRequest(ctx, job.rootRequestID.String, 0)
	}

	var result map[string]interface{}
	var callErr error
	if job.entityType == "cron" {
		entry := FindCronEntry(op.exec.Schema(), job.externalName)
		if entry == nil {
			callErr = fmt.Errorf("cron entry %q not found in schema", job.externalName)
		} else {
			callErr = op.exec.ExecuteCronBody(dispatchCtx, entry)
			result = map[string]interface{}{}
		}
	} else {
		result, callErr = op.manager.Call(dispatchCtx, job.externalName, params)
	}

	if callErr == nil {
		resultJSON, _ := json.Marshal(result)
		tx.ExecContext(ctx, `UPDATE outbox SET status='processed', processed_at=NOW(), result_payload=$1 WHERE id=$2`, resultJSON, job.id)
		tx.Commit()

		if job.recurCron.Valid && job.recurCron.String != "" {
			nextRun := cronNextAfter(job.recurCron.String, time.Now())
			op.db.ExecContext(ctx, `
				INSERT INTO outbox (entity_type, entity_id, external_name, payload, status, recur_cron, run_after)
				VALUES ($1, NULL, $2, $3, 'pending', $4, $5)`,
				job.entityType, job.externalName, job.payload, job.recurCron.String, nextRun)
		}

		if job.targetField.Valid && job.targetField.String != "" {
			op.writeResultToEntity(ctx, job.entityType, job.entityID.String, job.targetField.String, result)
		}

		op.executeEffectActions(ctx, job, params, result)

		if dels, ok := result["__deletes"]; ok {
			op.processDeletes(ctx, dels)
		}
		if creates, ok := result["__creates"].(map[string]interface{}); ok {
			op.processCreates(ctx, creates)
		}
		if updates, ok := result["__updates"]; ok {
			op.processUpdates(ctx, updates)
		}
		if del, ok := result["__delete"].(bool); ok && del {
			if job.entityID.Valid && job.entityID.String != "" {
				if err := op.exec.Delete(ctx, job.entityType, job.entityID.String, WithSystemBody(map[string]interface{}{})); err != nil {
					log.Printf("outbox: __delete %s %s: %v", job.entityType, job.entityID.String, err)
				}
			}
		}

		if job.sagaID.Valid {
			op.checkSagaCompletion(ctx, job.sagaID.String, job.entityType, job.entityID.String)
		}
	} else {
		newRetryCount := job.retryCount + 1
		if newRetryCount >= 3 {
			tx.ExecContext(ctx, `UPDATE outbox SET status='failed', error_message=$1, retry_count=$2 WHERE id=$3`, callErr.Error(), newRetryCount, job.id)
			tx.Commit()
			if job.sagaID.Valid {
				op.triggerCompensation(ctx, job.sagaID.String, job.sagaStep, job.entityType, job.entityID.String)
			} else if job.entityID.Valid && job.entityID.String != "" {
				if err := op.systemUpdateEntity(ctx, job.entityType, job.entityID.String, map[string]interface{}{"status": "failed"}); err != nil {
					log.Printf("outbox: mark failed %s %s: %v", job.entityType, job.entityID.String, err)
				}
			}
		} else {
			tx.ExecContext(ctx, `UPDATE outbox SET retry_count=$1, error_message=$2 WHERE id=$3`, newRetryCount, callErr.Error(), job.id)
			tx.Commit()
		}
	}
}

func (op *OutboxProcessor) processCompensationStep(ctx context.Context) {
	tx, err := op.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()

	var job outboxJob
	err = tx.QueryRowContext(ctx, `
		SELECT id, entity_type, entity_id, external_name, payload, saga_id, saga_step, retry_count
		FROM outbox
		WHERE status = 'pending' AND is_compensation = TRUE
		ORDER BY saga_step DESC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(&job.id, &job.entityType, &job.entityID, &job.externalName, &job.payload,
		&job.sagaID, &job.sagaStep, &job.retryCount)

	if err == sql.ErrNoRows {
		tx.Commit()
		return
	}
	if err != nil {
		return
	}

	var params map[string]interface{}
	json.Unmarshal(job.payload, &params)

	_, callErr := op.manager.Call(ctx, job.externalName, params)

	if callErr == nil {
		tx.ExecContext(ctx, `UPDATE outbox SET status='compensated', processed_at=NOW() WHERE id=$1`, job.id)
		tx.Commit()
		if job.sagaID.Valid {
			op.checkCompensationCompletion(ctx, job.sagaID.String, job.entityType, job.entityID.String)
		}
	} else {
		tx.ExecContext(ctx, `UPDATE outbox SET status='failed', error_message=$1 WHERE id=$2`, callErr.Error(), job.id)
		tx.Commit()
		if job.entityID.Valid && job.entityID.String != "" {
			if err := op.systemUpdateEntity(ctx, job.entityType, job.entityID.String, map[string]interface{}{"status": "failed"}); err != nil {
				log.Printf("outbox: compensation mark failed %s %s: %v", job.entityType, job.entityID.String, err)
			}
		}
	}
}

func (op *OutboxProcessor) checkSagaCompletion(ctx context.Context, sagaID, entityType, entityID string) {
	if entityID == "" {
		return
	}
	var remaining int
	op.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE saga_id=$1 AND is_compensation=FALSE AND status NOT IN ('processed','cancelled')`, sagaID).Scan(&remaining)
	if remaining == 0 {
		op.db.ExecContext(ctx, `UPDATE outbox SET status='cancelled' WHERE saga_id=$1 AND is_compensation=TRUE AND status='held'`, sagaID)
		if err := op.systemUpdateEntity(ctx, entityType, entityID, map[string]interface{}{"status": "done"}); err != nil {
			log.Printf("outbox: saga complete %s %s: %v", entityType, entityID, err)
		}
	}
}

func (op *OutboxProcessor) triggerCompensation(ctx context.Context, sagaID string, failedStep int, entityType, entityID string) {
	op.db.ExecContext(ctx, `UPDATE outbox SET status='cancelled' WHERE saga_id=$1 AND is_compensation=TRUE AND saga_step >= $2 AND status='held'`, sagaID, failedStep)

	var count int
	op.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE saga_id=$1 AND is_compensation=TRUE AND saga_step < $2 AND status='held'`, sagaID, failedStep).Scan(&count)

	if count > 0 {
		op.db.ExecContext(ctx, `UPDATE outbox SET status='pending' WHERE saga_id=$1 AND is_compensation=TRUE AND saga_step < $2 AND status='held'`, sagaID, failedStep)
		if err := op.systemUpdateEntity(ctx, entityType, entityID, map[string]interface{}{"status": "compensating"}); err != nil {
			log.Printf("outbox: compensating %s %s: %v", entityType, entityID, err)
		}
	} else {
		if err := op.systemUpdateEntity(ctx, entityType, entityID, map[string]interface{}{"status": "failed"}); err != nil {
			log.Printf("outbox: saga failed %s %s: %v", entityType, entityID, err)
		}
	}
}

func (op *OutboxProcessor) checkCompensationCompletion(ctx context.Context, sagaID, entityType, entityID string) {
	var remaining int
	op.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE saga_id=$1 AND is_compensation=TRUE AND status='pending'`, sagaID).Scan(&remaining)
	if remaining == 0 {
		if err := op.systemUpdateEntity(ctx, entityType, entityID, map[string]interface{}{"status": "compensated"}); err != nil {
			log.Printf("outbox: compensated %s %s: %v", entityType, entityID, err)
		}
	}
}

func (op *OutboxProcessor) writeResultToEntity(ctx context.Context, entityType, entityID, targetField string, result map[string]interface{}) {
	if entityID == "" || op.exec == nil {
		return
	}
	inKey, err := op.exec.InputKeyForDBColumn(entityType, targetField)
	if err != nil {
		return
	}
	val, ok := result[targetField]
	if !ok {
		val, ok = result[snakeToCamel(targetField)]
	}
	if !ok {
		return
	}
	if err := op.systemUpdateEntity(ctx, entityType, entityID, map[string]interface{}{inKey: val}); err != nil {
		log.Printf("outbox: writeResultToEntity %s %s: %v", entityType, entityID, err)
	}
}

func (op *OutboxProcessor) executeEffectActions(ctx context.Context, job outboxJob, params map[string]interface{}, result map[string]interface{}) {
	if op.exec == nil {
		return
	}

	schema := op.exec.Schema()
	entityID := ""
	if job.entityID.Valid {
		entityID = job.entityID.String
	}

	var varName string
	var followingStmts []ast.Statement

	for _, m := range schema.Models {
		if !strings.EqualFold(m.Name, job.entityType) {
			continue
		}
		for _, crud := range m.CRUD {
			if crud.Effect == nil || job.sagaStep >= len(crud.Effect.Statements) {
				continue
			}
			stmt := crud.Effect.Statements[job.sagaStep]
			if extractStaticCallName(stmt) != job.externalName {
				continue
			}
			if a, ok := stmt.(*ast.Assignment); ok {
				varName = a.Name
			}
			for i := job.sagaStep + 1; i < len(crud.Effect.Statements); i++ {
				s := crud.Effect.Statements[i]
				if extractStaticCallName(s) != "" {
					break
				}
				followingStmts = append(followingStmts, s)
			}
			goto found
		}
	}
found:
	if len(followingStmts) == 0 {
		return
	}

	vars := make(map[string]interface{})
	if varName != "" {
		vars[varName] = result
	}

	if err := op.exec.ExecuteEffectActions(ctx, followingStmts, params, vars, entityID); err != nil {
		log.Printf("outbox: effect actions %s.%s: %v", job.entityType, job.externalName, err)
	}
}

func extractStaticCallName(stmt ast.Statement) string {
	switch s := stmt.(type) {
	case *ast.Assignment:
		if call, ok := s.Value.(*ast.ExternalCall); ok {
			return call.Name
		}
	case *ast.PredicateExpr:
		if call, ok := s.Expr.(*ast.ExternalCall); ok {
			return call.Name
		}
	}
	return ""
}

func (op *OutboxProcessor) processCreates(ctx context.Context, creates map[string]interface{}) {
	for modelName, rowsRaw := range creates {
		for _, row := range toRowSlice(rowsRaw) {
			chain := row["__chain"]
			rowCopy := make(map[string]interface{}, len(row))
			for k, v := range row {
				if k != "__chain" {
					rowCopy[k] = v
				}
			}

			created, err := op.exec.Create(ctx, modelName, WithSystemBody(rowCopy))
			if err != nil {
				log.Printf("outbox: __creates %s: %v", modelName, err)
				continue
			}

			if chain == nil {
				continue
			}
			chainMap, ok := chain.(map[string]interface{})
			if !ok {
				continue
			}
			createdID, _ := created["id"].(string)
			for chainModel, chainRowsRaw := range chainMap {
				for _, chainRow := range toRowSlice(chainRowsRaw) {
					chainCopy := make(map[string]interface{}, len(chainRow))
					for k, v := range chainRow {
						if sv, ok := v.(string); ok && sv == "__parent_id" {
							chainCopy[k] = createdID
						} else {
							chainCopy[k] = v
						}
					}
					if _, err := op.exec.Create(ctx, chainModel, WithSystemBody(chainCopy)); err != nil {
						log.Printf("outbox: __chain create %s: %v", chainModel, err)
					}
				}
			}
		}
	}
}

func (op *OutboxProcessor) processDeletes(ctx context.Context, dels interface{}) {
	for _, d := range toRowSlice(dels) {
		modelName, _ := d["__model"].(string)
		id, _ := d["id"].(string)
		if modelName == "" || id == "" {
			continue
		}
		if err := op.exec.Delete(ctx, modelName, id, WithSystemBody(map[string]interface{}{})); err != nil {
			log.Printf("outbox: __deletes %s %s: %v", modelName, id, err)
		}
	}
}

func (op *OutboxProcessor) processUpdates(ctx context.Context, updates interface{}) {
	for _, u := range toRowSlice(updates) {
		modelName, _ := u["__model"].(string)
		id, _ := u["id"].(string)
		if modelName == "" || id == "" {
			continue
		}
		patch := make(map[string]interface{}, len(u))
		for k, v := range u {
			if k != "__model" && k != "id" {
				patch[k] = v
			}
		}
		if len(patch) == 0 {
			continue
		}
		if _, err := op.exec.Update(ctx, modelName, id, WithSystemBody(patch)); err != nil {
			log.Printf("outbox: __updates %s %s: %v", modelName, id, err)
		}
	}
}

func resultHasHandlerSideEffects(m map[string]interface{}) bool {
	if m == nil {
		return false
	}

	if v, ok := m["__nocache"]; ok && v == true {
		return true
	}
	if _, ok := m["__creates"].(map[string]interface{}); ok {
		return true
	}
	if _, ok := m["__updates"]; ok {
		return true
	}
	if _, ok := m["__deletes"]; ok {
		return true
	}
	return false
}

func ApplyHandlerSideEffects(ctx context.Context, exec *Executor, result map[string]interface{}) {
	if exec == nil || !resultHasHandlerSideEffects(result) {
		return
	}
	op := &OutboxProcessor{exec: exec, db: exec.DB()}
	if dels, ok := result["__deletes"]; ok {
		op.processDeletes(ctx, dels)
	}
	if creates, ok := result["__creates"].(map[string]interface{}); ok {
		op.processCreates(ctx, creates)
	}
	if updates, ok := result["__updates"]; ok {
		op.processUpdates(ctx, updates)
	}
}

func toRowSlice(v interface{}) []map[string]interface{} {
	switch t := v.(type) {
	case []map[string]interface{}:
		return t
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(t))
		for _, item := range t {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]interface{}:
		return []map[string]interface{}{t}
	}
	return nil
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

type EventEmitter struct {
	db interface{}
}

func (ee *EventEmitter) Emit(entityType string, entityID string, eventType string, payload map[string]interface{}) error {
	return nil
}
