package integration

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/runtime"
)

func setupWalletTransferEnv(t *testing.T) (*runtime.Executor, func()) {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:15-alpine"),
		postgres.WithDatabase("fookie_test"),
		postgres.WithUsername("fookie"),
		postgres.WithPassword("fookie_test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := OpenDB("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping())

	schema, err := ParseSchemaFromFile("wallet_transfer")
	require.NoError(t, err)
	sqlGen := compiler.NewSQLGenerator(schema)
	sqls, err := sqlGen.Generate()
	require.NoError(t, err)

	for _, s := range sqls {
		_, err := db.ExecContext(ctx, s)
		require.NoError(t, err, "migration failed:\n%s", s)
	}

	_, _ = db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS "pgcrypto";`)

	logger := &testLogger{t: t}
	exec := runtime.NewExecutor(db, schema, logger)

	exec.ExternalManager().Register("ValidateToken", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		token, _ := input["token"].(string)
		if token == "" {
			return nil, nil
		}
		return map[string]interface{}{
			"valid":  true,
			"userId": "user-test-001",
		}, nil
	})

	exec.ExternalManager().Register("FraudCheck", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		amount, _ := input["amount"].(float64)
		return map[string]interface{}{
			"allowed":   amount <= 1000000,
			"riskScore": 0.0,
		}, nil
	})

	exec.ExternalManager().Register("SendTransferNotification", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"messageId": "msg-123",
			"sent":      true,
		}, nil
	})

	cleanup := func() {
		_ = db.Close()
		_ = pgContainer.Terminate(ctx)
	}

	return exec, cleanup
}


func TestWalletTransferScenario_CreateUser(t *testing.T) {
	exec, cleanup := setupWalletTransferEnv(t)
	defer cleanup()

	ctx := context.Background()

	result, err := exec.Create(ctx, "User", map[string]interface{}{
		"email": "alice@example.com",
		"name":  "Alice",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result["id"])
	assert.Equal(t, "alice@example.com", result["email"])
	assert.Equal(t, "Alice", result["name"])
}

func TestWalletTransferScenario_CreateWallets(t *testing.T) {
	exec, cleanup := setupWalletTransferEnv(t)
	defer cleanup()

	ctx := context.Background()

	user, _ := exec.Create(ctx, "User", map[string]interface{}{
		"email": "alice@example.com",
		"name":  "Alice",
	})

	w1, err := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   user["id"],
		"balance":  1000.0,
		"currency": "USD",
		"label":    "Checking",
	})
	require.NoError(t, err)
	assert.Equal(t, 1000.0, w1["balance"])

	w2, err := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   user["id"],
		"balance":  0.0,
		"currency": "USD",
		"label":    "Savings",
	})
	require.NoError(t, err)
	assert.Equal(t, 0.0, w2["balance"])
}

func TestWalletTransferScenario_TransferWithValidation(t *testing.T) {
	exec, cleanup := setupWalletTransferEnv(t)
	defer cleanup()

	ctx := context.Background()

	alice, _ := exec.Create(ctx, "User", map[string]interface{}{
		"email": "alice@example.com",
		"name":  "Alice",
	})
	bob, _ := exec.Create(ctx, "User", map[string]interface{}{
		"email": "bob@example.com",
		"name":  "Bob",
	})

	walletA, _ := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   alice["id"],
		"balance":  500.0,
		"currency": "USD",
		"label":    "Main",
	})
	walletB, _ := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   bob["id"],
		"balance":  0.0,
		"currency": "USD",
		"label":    "Main",
	})

	tx, err := exec.Create(ctx, "Transaction", map[string]interface{}{
		"token":              "valid-jwt",
		"fromWalletId":       walletA["id"],
		"toWalletId":         walletB["id"],
		"amount":             100.0,
		"currency":           "USD",
		"recipientEmail":     "bob@example.com",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, tx["id"])
	assert.Equal(t, "pending", tx["status"])
	assert.Equal(t, 100.0, tx["amount"])
}

func TestWalletTransferScenario_TransferFails_InvalidToken(t *testing.T) {
	exec, cleanup := setupWalletTransferEnv(t)
	defer cleanup()

	ctx := context.Background()

	alice, _ := exec.Create(ctx, "User", map[string]interface{}{
		"email": "alice@example.com",
		"name":  "Alice",
	})
	bob, _ := exec.Create(ctx, "User", map[string]interface{}{
		"email": "bob@example.com",
		"name":  "Bob",
	})

	walletA, _ := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   alice["id"],
		"balance":  500.0,
		"currency": "USD",
		"label":    "Main",
	})
	walletB, _ := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   bob["id"],
		"balance":  0.0,
		"currency": "USD",
		"label":    "Main",
	})

	_, err := exec.Create(ctx, "Transaction", map[string]interface{}{
		"token":              "",
		"fromWalletId":       walletA["id"],
		"toWalletId":         walletB["id"],
		"amount":             100.0,
		"currency":           "USD",
		"recipientEmail":     "bob@example.com",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assertion failed")
}

func TestWalletTransferScenario_AggregateQuery(t *testing.T) {
	exec, cleanup := setupWalletTransferEnv(t)
	defer cleanup()

	ctx := context.Background()

	alice, _ := exec.Create(ctx, "User", map[string]interface{}{
		"email": "alice@example.com",
		"name":  "Alice",
	})
	bob, _ := exec.Create(ctx, "User", map[string]interface{}{
		"email": "bob@example.com",
		"name":  "Bob",
	})

	walletA, _ := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   alice["id"],
		"balance":  1000.0,
		"currency": "USD",
		"label":    "Main",
	})
	walletB, _ := exec.Create(ctx, "Wallet", map[string]interface{}{
		"userId":   bob["id"],
		"balance":  0.0,
		"currency": "USD",
		"label":    "Main",
	})

	for _, amount := range []float64{50, 75, 125} {
		exec.Create(ctx, "Transaction", map[string]interface{}{
			"token":              "valid-jwt",
			"fromWalletId":       walletA["id"],
			"toWalletId":         walletB["id"],
			"amount":             amount,
			"currency":           "USD",
			"recipientEmail":     "bob@example.com",
		})
	}

	exec.Update(ctx, "Transaction", "dummy-id", map[string]interface{}{
		"status": "done",
	})

	rows, err := exec.Read(ctx, "Transaction", map[string]interface{}{})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	assert.NotNil(t, row["total"])
	assert.NotNil(t, row["count"])
	assert.NotNil(t, row["avgRisk"])
}
