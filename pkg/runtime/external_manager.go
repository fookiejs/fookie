package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ExternalManager struct {
	handlers map[string]ExternalHandler
	mu       sync.RWMutex
	cache    map[string]*CachedResult
}

type ExternalHandler func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)

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

	em.cache[cacheKey] = &CachedResult{
		Result:    result,
		Timestamp: time.Now(),
		TTL:       5 * time.Minute,
	}

	return result, nil
}

func (em *ExternalManager) callWithRetry(ctx context.Context, handler ExternalHandler, input map[string]interface{}) (map[string]interface{}, error) {
	maxRetries := 3
	backoff := 100 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		result, err := handler(ctx, input)
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

func (em *ExternalManager) handleBuiltin(ctx context.Context, name string, input map[string]interface{}) (map[string]interface{}, error) {
	switch name {
	case "ValidateToken":
		return em.handleValidateToken(input)

	case "FraudCheck":
		return em.handleFraudCheck(input)

	default:
		return nil, fmt.Errorf("unknown external: %s", name)
	}
}

func (em *ExternalManager) handleValidateToken(input map[string]interface{}) (map[string]interface{}, error) {
	token, ok := input["token"].(string)
	if !ok || token == "" {
		return map[string]interface{}{
			"valid": false,
		}, fmt.Errorf("invalid token format")
	}

	return map[string]interface{}{
		"valid":     true,
		"userId":    "user-123",
		"issuer":    "fookie-auth",
		"expiresAt": time.Now().Add(24 * time.Hour),
	}, nil
}

func (em *ExternalManager) handleFraudCheck(input map[string]interface{}) (map[string]interface{}, error) {
	amount, ok := input["amount"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid amount")
	}

	allowed := amount <= 10000
	score := int(amount / 100)

	return map[string]interface{}{
		"allowed": allowed,
		"score":   score,
	}, nil
}

type outboxJob struct {
	id           string
	entityType   string
	entityID     string
	externalName string
	payload      []byte
	sagaID       sql.NullString
	sagaStep     int
	retryCount   int
}

type OutboxProcessor struct {
	manager *ExternalManager
	db      *sql.DB
	ticker  *time.Ticker
	done    chan struct{}
}

func NewOutboxProcessor(manager *ExternalManager, db *sql.DB) *OutboxProcessor {
	return &OutboxProcessor{
		manager: manager,
		db:      db,
		done:    make(chan struct{}),
	}
}

func (op *OutboxProcessor) Start(interval time.Duration) {
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
		SELECT id, entity_type, entity_id, external_name, payload, saga_id, saga_step, retry_count
		FROM outbox
		WHERE status = 'pending' AND is_compensation = FALSE AND retry_count < 3
		ORDER BY created_at ASC
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

	result, callErr := op.manager.Call(ctx, job.externalName, params)

	if callErr == nil {
		resultJSON, _ := json.Marshal(result)
		tx.ExecContext(ctx, `
			UPDATE outbox SET status='processed', processed_at=NOW(), result_payload=$1
			WHERE id=$2`, resultJSON, job.id)
		tx.Commit()
		if job.sagaID.Valid {
			op.checkSagaCompletion(ctx, job.sagaID.String, job.entityType, job.entityID)
		}
	} else {
		newRetryCount := job.retryCount + 1
		if newRetryCount >= 3 {
			tx.ExecContext(ctx, `
				UPDATE outbox SET status='failed', error_message=$1, retry_count=$2
				WHERE id=$3`, callErr.Error(), newRetryCount, job.id)
			tx.Commit()
			if job.sagaID.Valid {
				op.triggerCompensation(ctx, job.sagaID.String, job.sagaStep, job.entityType, job.entityID)
			} else {
				table := sagaSnake(job.entityType)
				op.db.ExecContext(ctx, fmt.Sprintf(`UPDATE "%s" SET status='failed', updated_at=NOW() WHERE id=$1`, table), job.entityID)
			}
		} else {
			tx.ExecContext(ctx, `
				UPDATE outbox SET retry_count=$1, error_message=$2
				WHERE id=$3`, newRetryCount, callErr.Error(), job.id)
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
		tx.ExecContext(ctx, `
			UPDATE outbox SET status='compensated', processed_at=NOW()
			WHERE id=$1`, job.id)
		tx.Commit()
		if job.sagaID.Valid {
			op.checkCompensationCompletion(ctx, job.sagaID.String, job.entityType, job.entityID)
		}
	} else {
		tx.ExecContext(ctx, `
			UPDATE outbox SET status='failed', error_message=$1
			WHERE id=$2`, callErr.Error(), job.id)
		tx.Commit()
		table := sagaSnake(job.entityType)
		op.db.ExecContext(ctx, fmt.Sprintf(`UPDATE "%s" SET status='failed', updated_at=NOW() WHERE id=$1`, table), job.entityID)
	}
}

func (op *OutboxProcessor) checkSagaCompletion(ctx context.Context, sagaID, entityType, entityID string) {
	var remaining int
	op.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox
		WHERE saga_id=$1 AND is_compensation=FALSE AND status NOT IN ('processed','cancelled')
	`, sagaID).Scan(&remaining)

	if remaining == 0 {
		op.db.ExecContext(ctx, `
			UPDATE outbox SET status='cancelled'
			WHERE saga_id=$1 AND is_compensation=TRUE AND status='held'
		`, sagaID)
		table := sagaSnake(entityType)
		op.db.ExecContext(ctx, fmt.Sprintf(`UPDATE "%s" SET status='done', updated_at=NOW() WHERE id=$1`, table), entityID)
	}
}

func (op *OutboxProcessor) triggerCompensation(ctx context.Context, sagaID string, failedStep int, entityType, entityID string) {
	op.db.ExecContext(ctx, `
		UPDATE outbox SET status='cancelled'
		WHERE saga_id=$1 AND is_compensation=TRUE AND saga_step >= $2 AND status='held'
	`, sagaID, failedStep)

	var count int
	op.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox
		WHERE saga_id=$1 AND is_compensation=TRUE AND saga_step < $2 AND status='held'
	`, sagaID, failedStep).Scan(&count)

	table := sagaSnake(entityType)
	if count > 0 {
		op.db.ExecContext(ctx, `
			UPDATE outbox SET status='pending'
			WHERE saga_id=$1 AND is_compensation=TRUE AND saga_step < $2 AND status='held'
		`, sagaID, failedStep)
		op.db.ExecContext(ctx, fmt.Sprintf(`UPDATE "%s" SET status='compensating', updated_at=NOW() WHERE id=$1`, table), entityID)
	} else {
		op.db.ExecContext(ctx, fmt.Sprintf(`UPDATE "%s" SET status='failed', updated_at=NOW() WHERE id=$1`, table), entityID)
	}
}

func (op *OutboxProcessor) checkCompensationCompletion(ctx context.Context, sagaID, entityType, entityID string) {
	var remaining int
	op.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox
		WHERE saga_id=$1 AND is_compensation=TRUE AND status='pending'
	`, sagaID).Scan(&remaining)

	if remaining == 0 {
		table := sagaSnake(entityType)
		op.db.ExecContext(ctx, fmt.Sprintf(`UPDATE "%s" SET status='compensated', updated_at=NOW() WHERE id=$1`, table), entityID)
	}
}

func sagaSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteByte(byte(r + 32))
		} else {
			b.WriteRune(r)
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
