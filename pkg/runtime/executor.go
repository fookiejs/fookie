package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/google/uuid"
)

// Executor handles operation execution with status progression and external orchestration
type Executor struct {
	db           *sql.DB
	schema       *ast.Schema
	externalMgr  *ExternalManager
	logger       Logger
}

// Logger interface for structured logging
type Logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
}

type ExternalCall struct {
	Name      string
	Input     map[string]ast.Expression
	Timestamp time.Time
}

type ExecutionResult struct {
	OperationID string
	EntityID    string
	Status      string
	Output      map[string]interface{}
	Errors      []string
	Duration    time.Duration
	ExternalCalls []ExternalCall
}

func NewExecutor(db *sql.DB, schema *ast.Schema, logger Logger) *Executor {
	return &Executor{
		db:          db,
		schema:      schema,
		externalMgr: NewExternalManager(),
		logger:      logger,
	}
}

// ExecuteCreate handles model creation with full lifecycle
func (e *Executor) ExecuteCreate(ctx context.Context, modelName string, input map[string]interface{}) (*ExecutionResult, error) {
	start := time.Now()
	result := &ExecutionResult{
		OperationID: uuid.New().String(),
		EntityID:    uuid.New().String(),
		Status:      "initiate",
		Output:      make(map[string]interface{}),
	}

	// Find model
	model := e.findModel(modelName)
	if model == nil {
		result.Errors = append(result.Errors, fmt.Sprintf("model %s not found", modelName))
		return result, fmt.Errorf("model not found")
	}

	// Find create operation
	createOp, ok := model.CRUD["create"]
	if !ok {
		result.Errors = append(result.Errors, "create operation not defined")
		return result, fmt.Errorf("create operation not found")
	}

	// Create context
	context := &ast.Context{
		Input:         input,
		Variables:     make(map[string]interface{}),
		Principal:     make(map[string]interface{}),
		Output:        make(map[string]interface{}),
		TransactionID: result.OperationID,
		Timestamp:     time.Now(),
	}

	// Execute role block (authentication, principal extraction)
	if createOp.Role != nil {
		e.logger.Info("Executing role block", "operation", "create", "model", modelName)
		if err := e.executeBlock(ctx, createOp.Role, context, &result.ExternalCalls); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("role error: %v", err))
			result.Status = "failed"
			return result, err
		}
	}

	// Execute rule block (validation)
	if createOp.Rule != nil {
		e.logger.Info("Executing rule block", "operation", "create", "model", modelName)
		if err := e.executeBlock(ctx, createOp.Rule, context, &result.ExternalCalls); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("rule failed: %v", err))
			result.Status = "failed"
			return result, err
		}
	}

	// Persist: Execute modify block and INSERT into DB
	if createOp.Modify != nil {
		e.logger.Info("Executing modify block and persisting", "operation", "create", "model", modelName)
		if err := e.persistEntity(ctx, model, createOp.Modify, context); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("persist error: %v", err))
			result.Status = "failed"
			return result, err
		}
		result.Output = context.Output
	}

	// Queue async work: Effect block → outbox
	if createOp.Effect != nil {
		e.logger.Info("Queuing effect (async) work", "operation", "create", "model", modelName)
		result.Status = "progress" // Transition to progress while effects are queued
		if err := e.queueEffects(ctx, createOp.Effect, context); err != nil {
			// Effect failures don't fail the operation, just log
			result.Errors = append(result.Errors, fmt.Sprintf("effect error: %v", err))
		}
	}

	// If no effects or all succeed, mark done
	if createOp.Effect == nil || len(result.Errors) == 0 {
		result.Status = "done"
	}

	result.Duration = time.Since(start)
	return result, nil
}

// ExecuteRead retrieves entities with filtering and pagination
func (e *Executor) ExecuteRead(ctx context.Context, modelName string, filters map[string]interface{}) ([]*ExecutionResult, error) {
	model := e.findModel(modelName)
	if model == nil {
		return nil, fmt.Errorf("model not found")
	}

	readOp, ok := model.CRUD["read"]
	if !ok {
		return nil, fmt.Errorf("read operation not defined")
	}

	// Role block: auth
	context := &ast.Context{
		Variables: make(map[string]interface{}),
		Principal: make(map[string]interface{}),
	}

	if readOp.Role != nil {
		var calls []ExternalCall
		if err := e.executeBlock(ctx, readOp.Role, context, &calls); err != nil {
			return nil, fmt.Errorf("auth failed: %v", err)
		}
	}

	// Build and execute SQL query
	query := fmt.Sprintf("SELECT * FROM %s", toSnakeCase(modelName))
	if readOp.Where != nil {
		// TODO: compile WHERE conditions to SQL
		query += " WHERE status != 'failed'"
	}
	if len(readOp.OrderBy) > 0 {
		query += " ORDER BY"
		for i, ob := range readOp.OrderBy {
			if i > 0 {
				query += ","
			}
			query += fmt.Sprintf(" %s", toSnakeCase(ob.Field))
			if ob.Desc {
				query += " DESC"
			}
		}
	}
	if readOp.Cursor != nil {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", readOp.Cursor.Size, readOp.Cursor.Offset)
	}

	rows, err := e.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query error: %v", err)
	}
	defer rows.Close()

	var results []*ExecutionResult
	for rows.Next() {
		result := &ExecutionResult{
			OperationID: uuid.New().String(),
			Status:      "done",
			Output:      make(map[string]interface{}),
		}
		// TODO: Scan rows into result.Output
		results = append(results, result)
	}

	return results, nil
}

// ExecuteDelete (soft delete via status or deletedAt)
func (e *Executor) ExecuteDelete(ctx context.Context, modelName string, entityID string) (*ExecutionResult, error) {
	result := &ExecutionResult{
		OperationID: uuid.New().String(),
		EntityID:    entityID,
		Status:      "done",
	}

	model := e.findModel(modelName)
	if model == nil {
		return nil, fmt.Errorf("model not found")
	}

	deleteOp, ok := model.CRUD["delete"]
	if !ok {
		return nil, fmt.Errorf("delete operation not defined")
	}

	// Soft delete: set deleted_at
	query := fmt.Sprintf("UPDATE %s SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1", toSnakeCase(modelName))
	_, err := e.db.ExecContext(ctx, query, entityID)
	if err != nil {
		result.Status = "failed"
		return result, err
	}

	// Queue effects if defined
	if deleteOp.Effect != nil {
		context := &ast.Context{
			Variables:     make(map[string]interface{}),
			TransactionID: result.OperationID,
		}
		var calls []ExternalCall
		if err := e.executeBlock(ctx, deleteOp.Effect, context, &calls); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("effect error: %v", err))
		}
	}

	return result, nil
}

// executeBlock runs statements within a block (role, rule, modify, effect)
func (e *Executor) executeBlock(ctx context.Context, block *ast.Block, context *ast.Context, calls *[]ExternalCall) error {
	if block == nil {
		return nil
	}

	for _, stmt := range block.Statements {
		switch s := stmt.(type) {
		case *ast.Assignment:
			// Execute assignment: x = ExternalCall(...) or x = read Model(...)
			result, err := e.evaluateExpression(ctx, s.Value, context)
			if err != nil {
				return err
			}
			context.Variables[s.Name] = result
			// Track external call if it is one
			if extCall, ok := s.Value.(*ast.ExternalCall); ok {
				*calls = append(*calls, ExternalCall{
					Name:      extCall.Name,
					Input:     extCall.Params,
					Timestamp: time.Now(),
				})
			}

		case *ast.PredicateExpr:
			// Evaluate predicate for validation
			result, err := e.evaluateExpression(ctx, s.Expr, context)
			if err != nil {
				return err
			}
			// If predicate is false, fail
			if boolVal, ok := result.(bool); ok && !boolVal {
				return fmt.Errorf("predicate failed")
			}
		}
	}

	return nil
}

// evaluateExpression evaluates FSL expressions
func (e *Executor) evaluateExpression(ctx context.Context, expr ast.Expression, context *ast.Context) (interface{}, error) {
	switch ex := expr.(type) {
	case *ast.Literal:
		return ex.Value, nil

	case *ast.FieldAccess:
		obj := context.Variables[ex.Object]
		if obj == nil {
			obj = context.Input[ex.Object]
		}
		if obj == nil {
			obj = context.Principal[ex.Object]
		}
		// Navigate fields
		for _, field := range ex.Fields {
			if m, ok := obj.(map[string]interface{}); ok {
				obj = m[field]
			}
		}
		return obj, nil

	case *ast.ExternalCall:
		// Call external service
		// Evaluate param expressions to values
		params := make(map[string]interface{})
		for k, v := range ex.Params {
			val, err := e.evaluateExpression(ctx, v, context)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate param %s: %v", k, err)
			}
			params[k] = val
		}

		result, err := e.externalMgr.Call(ctx, ex.Name, params)
		if err != nil {
			return nil, fmt.Errorf("external %s failed: %v", ex.Name, err)
		}
		return result, nil

	case *ast.BinaryOp:
		left, err := e.evaluateExpression(ctx, ex.Left, context)
		if err != nil {
			return nil, err
		}
		right, err := e.evaluateExpression(ctx, ex.Right, context)
		if err != nil {
			return nil, err
		}
		return e.evaluateBinaryOp(left, ex.Op, right)

	default:
		return nil, fmt.Errorf("unsupported expression type")
	}
}

// evaluateBinaryOp evaluates binary operations
func (e *Executor) evaluateBinaryOp(left interface{}, op string, right interface{}) (interface{}, error) {
	switch op {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	case ">":
		// Compare numbers
		if l, ok := left.(float64); ok {
			if r, ok := right.(float64); ok {
				return l > r, nil
			}
		}
		return false, fmt.Errorf("cannot compare types")
	case "<":
		if l, ok := left.(float64); ok {
			if r, ok := right.(float64); ok {
				return l < r, nil
			}
		}
		return false, nil
	case ">=":
		if l, ok := left.(float64); ok {
			if r, ok := right.(float64); ok {
				return l >= r, nil
			}
		}
		return false, nil
	case "<=":
		if l, ok := left.(float64); ok {
			if r, ok := right.(float64); ok {
				return l <= r, nil
			}
		}
		return false, nil
	case "&&":
		if l, ok := left.(bool); ok {
			if r, ok := right.(bool); ok {
				return l && r, nil
			}
		}
		return false, nil
	case "||":
		if l, ok := left.(bool); ok {
			if r, ok := right.(bool); ok {
				return l || r, nil
			}
		}
		return false, nil
	default:
		return nil, fmt.Errorf("unsupported operator: %s", op)
	}
}

// persistEntity inserts entity into database from modify block
func (e *Executor) persistEntity(ctx context.Context, model *ast.Model, modifyBlock *ast.Block, context *ast.Context) error {
	// Set implicit fields
	context.Output["id"] = uuid.New().String()
	context.Output["created_at"] = time.Now()
	context.Output["updated_at"] = time.Now()
	context.Output["status"] = "initiate"

	// Execute modify statements to populate output
	var calls []ExternalCall
	if err := e.executeBlock(ctx, modifyBlock, context, &calls); err != nil {
		return err
	}

	// Build INSERT statement
	var cols, vals []string
	var args []interface{}
	argIndex := 1

	for key, val := range context.Output {
		cols = append(cols, toSnakeCase(key))
		vals = append(vals, fmt.Sprintf("$%d", argIndex))
		args = append(args, val)
		argIndex++
	}

	tableName := toSnakeCase(model.Name)
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		tableName, joinStrings(cols, ", "), joinStrings(vals, ", "))

	_, err := e.db.ExecContext(ctx, query, args...)
	return err
}

// queueEffects adds effect operations to outbox for async processing
func (e *Executor) queueEffects(ctx context.Context, effectBlock *ast.Block, context *ast.Context) error {
	for _, stmt := range effectBlock.Statements {
		if assign, ok := stmt.(*ast.Assignment); ok {
			if extCall, ok := assign.Value.(*ast.ExternalCall); ok {
				// Queue external call to outbox
				query := `
					INSERT INTO outbox (entity_type, entity_id, external_name, payload, status)
					VALUES ($1, $2, $3, $4, 'pending')
				`
				_, err := e.db.ExecContext(ctx, query,
					"Effect",
					context.Output["id"],
					extCall.Name,
					extCall.Params)
				if err != nil {
					return fmt.Errorf("failed to queue effect: %v", err)
				}
			}
		}
	}
	return nil
}

func (e *Executor) findModel(name string) *ast.Model {
	for _, model := range e.schema.Models {
		if model.Name == name {
			return model
		}
	}
	return nil
}

func toSnakeCase(s string) string {
	var result string
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result += "_"
		}
		result += string(r)
	}
	return result
}

func joinStrings(strs []string, sep string) string {
	var result string
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
