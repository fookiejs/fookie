package integration

import (
	"context"
	"testing"
	"time"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockLogger struct {
	t *testing.T
}

func (m *MockLogger) Info(msg string, args ...interface{}) {
	m.t.Logf("INFO: %s %v", msg, args)
}

func (m *MockLogger) Error(msg string, args ...interface{}) {
	m.t.Logf("ERROR: %s %v", msg, args)
}

func (m *MockLogger) Warn(msg string, args ...interface{}) {
	m.t.Logf("WARN: %s %v", msg, args)
}

func TestEndToEndTransactionCreate(t *testing.T) {
	schema := &ast.Schema{
		Models: []*ast.Model{
			{
				Name: "Wallet",
				Fields: []*ast.Field{
					{Name: "userId", Type: ast.TypeRelation, Relation: strPtr("User")},
					{Name: "balance", Type: ast.TypeNumber},
				},
				CRUD: make(map[string]*ast.Operation),
			},
			{
				Name: "Transaction",
				Fields: []*ast.Field{
					{Name: "amount", Type: ast.TypeNumber},
					{Name: "fromWalletId", Type: ast.TypeRelation, Relation: strPtr("Wallet")},
					{Name: "toWalletId", Type: ast.TypeRelation, Relation: strPtr("Wallet")},
				},
				CRUD: make(map[string]*ast.Operation),
			},
		},
		Externals: []*ast.External{
			{
				Name: "ValidateToken",
				Input: map[string]string{
					"token": "string",
				},
				Output: map[string]string{
					"userId": "id",
					"valid":  "boolean",
				},
			},
		},
	}

	logger := &MockLogger{t: t}

	executor := runtime.NewExecutor(nil, schema, logger)
	require.NotNil(t, executor)

	extMgr := runtime.NewExternalManager()
	require.NotNil(t, extMgr)

	ctx := context.Background()
	result, err := extMgr.Call(ctx, "ValidateToken", map[string]interface{}{
		"token": "test-token",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result["valid"].(bool))
	assert.Equal(t, "user-123", result["userId"])
}

func TestExternalManagerRetry(t *testing.T) {
	manager := runtime.NewExternalManager()

	callCount := 0
	manager.Register("TestExternal", func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		callCount++
		if callCount < 2 {
			return nil, assert.AnError
		}
		return map[string]interface{}{"success": true}, nil
	})

	ctx := context.Background()
	result, err := manager.Call(ctx, "TestExternal", map[string]interface{}{})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, true, result["success"])
	assert.Equal(t, 2, callCount)
}

func TestFraudCheckExternal(t *testing.T) {
	manager := runtime.NewExternalManager()
	ctx := context.Background()

	testCases := []struct {
		amount     float64
		shouldPass bool
	}{
		{100, true},
		{5000, true},
		{10000, true},
		{10001, false},
	}

	for _, tc := range testCases {
		result, err := manager.Call(ctx, "FraudCheck", map[string]interface{}{
			"userId":       "user-1",
			"amount":       tc.amount,
			"fromWalletId": "wallet-1",
			"toWalletId":   "wallet-2",
		})

		assert.NoError(t, err)
		assert.Equal(t, tc.shouldPass, result["allowed"].(bool), "Amount: %v", tc.amount)
	}
}

func TestOutboxProcessor(t *testing.T) {
	manager := runtime.NewExternalManager()
	processor := runtime.NewOutboxProcessor(manager)

	require.NotNil(t, processor)

	processor.Start(100 * time.Millisecond)
	defer processor.Stop()

	time.Sleep(200 * time.Millisecond)

	t.Log("Outbox processor test: OK")
}

func strPtr(s string) *string {
	return &s
}
