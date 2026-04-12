package runtime

import (
	"context"
	"fmt"
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

	allowed := amount <= 10000
	score := int(amount / 100)

	return map[string]interface{}{
		"allowed": allowed,
		"score":   score,
	}, nil
}

type OutboxProcessor struct {
	manager *ExternalManager
	db      interface{}
	ticker  *time.Ticker
	done    chan struct{}
}

func NewOutboxProcessor(manager *ExternalManager) *OutboxProcessor {
	return &OutboxProcessor{
		manager: manager,
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
}

type EventEmitter struct {
	db interface{}
}

func (ee *EventEmitter) Emit(entityType string, entityID string, eventType string, payload map[string]interface{}) error {
	return nil
}
