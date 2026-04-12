package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ExternalManager manages external service calls with retry logic and caching
type ExternalManager struct {
	handlers map[string]ExternalHandler
	mu       sync.RWMutex
	cache    map[string]*CachedResult
}

// ExternalHandler processes external service calls
type ExternalHandler func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)

// CachedResult stores the result of an external call for idempotency
type CachedResult struct {
	Result    map[string]interface{}
	Timestamp time.Time
	TTL       time.Duration
}

// NewExternalManager creates a new external manager
func NewExternalManager() *ExternalManager {
	return &ExternalManager{
		handlers: make(map[string]ExternalHandler),
		cache:    make(map[string]*CachedResult),
	}
}

// Register registers an external handler
func (em *ExternalManager) Register(name string, handler ExternalHandler) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.handlers[name] = handler
}

// Call executes an external service call with retry and caching
func (em *ExternalManager) Call(ctx context.Context, name string, input map[string]interface{}) (map[string]interface{}, error) {
	em.mu.RLock()
	handler, exists := em.handlers[name]
	em.mu.RUnlock()

	if !exists {
		// Built-in handlers
		return em.handleBuiltin(ctx, name, input)
	}

	// Check cache for idempotency
	cacheKey := fmt.Sprintf("%s:%v", name, input)
	if cached := em.getCached(cacheKey); cached != nil {
		return cached, nil
	}

	// Call with retry logic
	result, err := em.callWithRetry(ctx, handler, input)
	if err != nil {
		return nil, fmt.Errorf("external %s failed: %v", name, err)
	}

	// Cache result
	em.cache[cacheKey] = &CachedResult{
		Result:    result,
		Timestamp: time.Now(),
		TTL:       5 * time.Minute, // Default TTL
	}

	return result, nil
}

// callWithRetry executes handler with exponential backoff retry
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
				backoff *= 2 // Exponential backoff
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	return nil, fmt.Errorf("external call failed after %d retries: %v", maxRetries, lastErr)
}

// getCached retrieves cached result if still valid
func (em *ExternalManager) getCached(key string) map[string]interface{} {
	em.mu.RLock()
	defer em.mu.RUnlock()

	cached, exists := em.cache[key]
	if !exists {
		return nil
	}

	if time.Since(cached.Timestamp) > cached.TTL {
		return nil // Expired
	}

	return cached.Result
}

// handleBuiltin handles built-in externals (for testing/mocking)
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

// Built-in external handlers (for testing/demo)

func (em *ExternalManager) handleValidateToken(input map[string]interface{}) (map[string]interface{}, error) {
	token, ok := input["token"].(string)
	if !ok || token == "" {
		return map[string]interface{}{
			"valid": false,
		}, fmt.Errorf("invalid token format")
	}

	// Mock validation: any non-empty token is valid
	return map[string]interface{}{
		"valid":    true,
		"userId":   "user-123",
		"issuer":   "fookie-auth",
		"expiresAt": time.Now().Add(24 * time.Hour),
	}, nil
}

func (em *ExternalManager) handleFraudCheck(input map[string]interface{}) (map[string]interface{}, error) {
	amount, ok := input["amount"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid amount")
	}

	// Mock fraud check: amounts > 10000 are flagged as high risk
	allowed := amount <= 10000
	score := int(amount / 100) // Higher amount = higher score

	return map[string]interface{}{
		"allowed": allowed,
		"score":   score,
	}, nil
}

// OutboxProcessor processes pending outbox jobs (external calls)
type OutboxProcessor struct {
	manager *ExternalManager
	db      interface{} // Would be *sql.DB in real implementation
	ticker  *time.Ticker
	done    chan struct{}
}

// NewOutboxProcessor creates a new outbox processor
func NewOutboxProcessor(manager *ExternalManager) *OutboxProcessor {
	return &OutboxProcessor{
		manager: manager,
		done:    make(chan struct{}),
	}
}

// Start begins processing outbox jobs
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

// Stop stops the processor
func (op *OutboxProcessor) Stop() {
	close(op.done)
}

// processPending retrieves and processes pending outbox jobs
func (op *OutboxProcessor) processPending() {
	// TODO: Query outbox table for pending jobs
	// For each job:
	// 1. Call external service
	// 2. Mark as processed or failed
	// 3. Emit event if needed
}

// EventEmitter emits events to event log for compensation/saga handling
type EventEmitter struct {
	db interface{}
}

// Emit creates an event in the event log
func (ee *EventEmitter) Emit(entityType string, entityID string, eventType string, payload map[string]interface{}) error {
	// TODO: INSERT into event_logs table
	return nil
}
