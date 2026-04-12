package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/validator"
	"github.com/google/uuid"
)

type Executor struct {
	db      *sql.DB
	schema  *ast.Schema
	extMgr  *ExternalManager
	sqlGen  *compiler.SQLGenerator
	logger  Logger
}

func NewExecutor(db *sql.DB, schema *ast.Schema, logger Logger) *Executor {
	return &Executor{
		db:     db,
		schema: schema,
		extMgr: NewExternalManager(),
		sqlGen: compiler.NewSQLGenerator(schema),
		logger: logger,
	}
}

func (e *Executor) ExternalManager() *ExternalManager { return e.extMgr }

func (e *Executor) Create(ctx context.Context, modelName string, input map[string]interface{}) (map[string]interface{}, error) {
	op, model, err := e.resolveOp(modelName, "create")
	if err != nil {
		return nil, err
	}

	rc := newRunCtx(input)

	if err := e.execBlock(ctx, op.Role, rc); err != nil {
		return nil, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, op.Rule, rc); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	row := map[string]interface{}{
		"id":         uuid.New().String(),
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
		"status":     "initiate",
	}
	if op.Modify != nil {
		for _, stmt := range op.Modify.Statements {
			if ma, ok := stmt.(*ast.ModifyAssignment); ok {
				val, err := e.evalExpr(ctx, ma.Value, rc)
				if err != nil {
					return nil, fmt.Errorf("modify %s: %w", ma.Field, err)
				}
				row[ma.Field] = val
			}
		}
	}

	sqlStr, keyOrder := e.sqlGen.CompileInsert(model, row)
	args := make([]interface{}, len(keyOrder))
	for i, k := range keyOrder {
		args[i] = row[k]
	}

	var id string
	var createdAt time.Time
	var status string
	if err := e.db.QueryRowContext(ctx, sqlStr, args...).Scan(&id, &createdAt, &status); err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	rc.output["id"] = id
	rc.output["created_at"] = createdAt
	rc.output["status"] = status
	for k, v := range row {
		rc.output[k] = v
	}

	if op.Effect != nil {
		if err := e.queueEffects(ctx, op.Effect, modelName, id, rc); err != nil {
			e.logger.Warn("effect queue failed", "err", err)
		} else {
			_, _ = e.db.ExecContext(ctx,
				fmt.Sprintf("UPDATE %s SET status = 'progress', updated_at = NOW() WHERE id = $1", compiler.SnakeCase(modelName)),
				id)
			rc.output["status"] = "progress"
		}
	} else {
		_, _ = e.db.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET status = 'done', updated_at = NOW() WHERE id = $1", compiler.SnakeCase(modelName)),
			id)
		rc.output["status"] = "done"
	}

	e.logger.Info("created", "model", modelName, "id", id)
	return rc.output, nil
}

func (e *Executor) Read(ctx context.Context, modelName string, input map[string]interface{}) ([]map[string]interface{}, error) {
	op, model, err := e.resolveOp(modelName, "read")
	if err != nil {
		return nil, err
	}

	rc := newRunCtx(input)

	if err := e.execBlock(ctx, op.Role, rc); err != nil {
		return nil, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, op.Rule, rc); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileRead(model, op)
	e.logger.Info("read query", "sql", sqlStr)

	rows, err := e.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func (e *Executor) Update(ctx context.Context, modelName string, id string, input map[string]interface{}) (map[string]interface{}, error) {
	op, model, err := e.resolveOp(modelName, "update")
	if err != nil {
		return nil, err
	}

	rc := newRunCtx(input)
	rc.output["id"] = id

	existing, err := e.fetchByID(ctx, modelName, id)
	if err != nil {
		return nil, fmt.Errorf("fetch existing: %w", err)
	}
	for k, v := range existing {
		rc.output[k] = v
	}

	if err := e.execBlock(ctx, op.Role, rc); err != nil {
		return nil, fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, op.Rule, rc); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	patch := map[string]interface{}{}
	if op.Modify != nil {
		for _, stmt := range op.Modify.Statements {
			if ma, ok := stmt.(*ast.ModifyAssignment); ok {
				val, err := e.evalExpr(ctx, ma.Value, rc)
				if err != nil {
					return nil, fmt.Errorf("modify %s: %w", ma.Field, err)
				}
				patch[ma.Field] = val
			}
		}
	}

	if len(patch) == 0 {
		return rc.output, nil
	}

	sqlStr, keyOrder := e.sqlGen.CompileUpdate(model, patch)
	args := make([]interface{}, len(keyOrder)+1)
	for i, k := range keyOrder {
		args[i] = patch[k]
	}
	args[len(keyOrder)] = id

	var updatedAt time.Time
	var status string
	if err := e.db.QueryRowContext(ctx, sqlStr, args...).Scan(&id, &updatedAt, &status); err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	rc.output["updated_at"] = updatedAt
	rc.output["status"] = status
	for k, v := range patch {
		rc.output[k] = v
	}

	if op.Effect != nil {
		if err := e.queueEffects(ctx, op.Effect, modelName, id, rc); err != nil {
			e.logger.Warn("effect queue failed", "err", err)
		}
	}

	return rc.output, nil
}

func (e *Executor) Delete(ctx context.Context, modelName string, id string, input map[string]interface{}) error {
	op, model, err := e.resolveOp(modelName, "delete")
	if err != nil {
		return err
	}

	rc := newRunCtx(input)

	if err := e.execBlock(ctx, op.Role, rc); err != nil {
		return fmt.Errorf("role: %w", err)
	}
	if err := e.execBlock(ctx, op.Rule, rc); err != nil {
		return fmt.Errorf("rule: %w", err)
	}

	sqlStr := e.sqlGen.CompileSoftDelete(model)
	if _, err := e.db.ExecContext(ctx, sqlStr, id); err != nil {
		return fmt.Errorf("soft-delete: %w", err)
	}

	if op.Effect != nil {
		if err := e.queueEffects(ctx, op.Effect, modelName, id, rc); err != nil {
			e.logger.Warn("effect queue failed", "err", err)
		}
	}

	return nil
}

func (e *Executor) execBlock(ctx context.Context, block *ast.Block, rc *runCtx) error {
	if block == nil {
		return nil
	}
	for _, stmt := range block.Statements {
		switch s := stmt.(type) {
		case *ast.Assignment:
			val, err := e.evalExpr(ctx, s.Value, rc)
			if err != nil {
				return fmt.Errorf("assign %s: %w", s.Name, err)
			}
			rc.vars[s.Name] = val

		case *ast.PredicateExpr:
			val, err := e.evalExpr(ctx, s.Expr, rc)
			if err != nil {
				return fmt.Errorf("predicate eval: %w", err)
			}
			if b, ok := val.(bool); ok && !b {
				return fmt.Errorf("assertion failed")
			}
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
		return e.extMgr.Call(ctx, ex.Name, params)

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
		fn, ok := validator.GetBuiltin(ex.Name)
		if !ok {
			return nil, fmt.Errorf("unknown builtin validator: %s", ex.Name)
		}
		args := make([]interface{}, len(ex.Args))
		for i, arg := range ex.Args {
			val, err := e.evalExpr(ctx, arg, rc)
			if err != nil {
				return nil, fmt.Errorf("builtin arg %d: %w", i, err)
			}
			args[i] = val
		}
		return fn(args...)
	}
	return nil, fmt.Errorf("unsupported expression: %T", expr)
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
	case float32:
		return float64(n), true
	}
	return 0, false
}

func (e *Executor) fetchByID(ctx context.Context, modelName string, id string) (map[string]interface{}, error) {
	table := compiler.SnakeCase(modelName)
	rows, err := e.db.QueryContext(ctx,
		fmt.Sprintf("SELECT * FROM %s WHERE id = $1 AND deleted_at IS NULL", table), id)
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

func (e *Executor) queueEffects(ctx context.Context, block *ast.Block, entityType, entityID string, rc *runCtx) error {
	for _, stmt := range block.Statements {
		var extName string
		var params map[string]interface{}

		switch s := stmt.(type) {
		case *ast.Assignment:
			if call, ok := s.Value.(*ast.ExternalCall); ok {
				extName = call.Name
				params = evalParams(ctx, call.Params, e, rc)
			}
		case *ast.PredicateExpr:
			if call, ok := s.Expr.(*ast.ExternalCall); ok {
				extName = call.Name
				params = evalParams(ctx, call.Params, e, rc)
			}
		}

		if extName == "" {
			continue
		}

		payload, _ := json.Marshal(params)
		_, err := e.db.ExecContext(ctx, `
			INSERT INTO outbox (entity_type, entity_id, external_name, payload)
			VALUES ($1, $2, $3, $4)`,
			entityType, entityID, extName, payload,
		)
		if err != nil {
			return fmt.Errorf("queue %s: %w", extName, err)
		}
	}
	return nil
}

func evalParams(ctx context.Context, rawParams map[string]ast.Expression, e *Executor, rc *runCtx) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range rawParams {
		val, _ := e.evalExpr(ctx, v, rc)
		out[k] = val
	}
	return out
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
			row[col] = vals[i]
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

type runCtx struct {
	input     map[string]interface{}
	principal map[string]interface{}
	output    map[string]interface{}
	vars      map[string]interface{}
}

func newRunCtx(input map[string]interface{}) *runCtx {
	return &runCtx{
		input:     input,
		principal: make(map[string]interface{}),
		output:    make(map[string]interface{}),
		vars:      make(map[string]interface{}),
	}
}

func (rc *runCtx) resolve(object string, fields []string) interface{} {
	var base interface{}
	switch object {
	case "input":
		base = rc.input
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
