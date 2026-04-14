package integration

import (
	"context"
	"errors"
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

func setupPaymentEnv(t *testing.T) (*runtime.Executor, func()) {
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

	schema, err := ParseSchemaFromFile("payment_processing")
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

	exec.ExternalManager().Register("VerifyMerchant", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		apiKey, _ := input["apiKey"].(string)
		if apiKey == "" {
			return map[string]interface{}{"valid": false}, nil
		}
		return map[string]interface{}{
			"valid":      true,
			"merchantId": "merchant-001",
			"tier":       "standard",
		}, nil
	})

	exec.ExternalManager().Register("ChargeCard", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"chargeId": "ch_test_123",
			"success":  true,
		}, nil
	})

	exec.ExternalManager().Register("RefundCard", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"refundId": "re_test_456",
			"success":  true,
		}, nil
	})

	exec.ExternalManager().Register("NotifyMerchant", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"delivered": true}, nil
	})

	exec.ExternalManager().Register("FraudScore", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"score":   0.1,
			"blocked": false,
		}, nil
	})

	cleanup := func() {
		db.Close()
		pgContainer.Terminate(ctx)
	}

	return exec, cleanup
}

func TestPayment_CreateMerchant(t *testing.T) {
	exec, cleanup := setupPaymentEnv(t)
	defer cleanup()

	ctx := context.Background()

	m, err := exec.Create(ctx, "Merchant", map[string]interface{}{
		"name":       "Acme Corp",
		"email":      "billing@acme.com",
		"webhookUrl": "https://acme.com/webhooks/payments",
		"apiKey":     "sk_test_abc123",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, m["id"])
	assert.Equal(t, "done", m["status"])
}

func TestPayment_CreateMerchant_InvalidEmail(t *testing.T) {
	exec, cleanup := setupPaymentEnv(t)
	defer cleanup()

	ctx := context.Background()

	_, err := exec.Create(ctx, "Merchant", map[string]interface{}{
		"name":       "Bad Corp",
		"email":      "not-an-email",
		"webhookUrl": "https://bad.com/hook",
		"apiKey":     "sk_test_xyz",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule")
}

func TestPayment_AddIBANMethod(t *testing.T) {
	exec, cleanup := setupPaymentEnv(t)
	defer cleanup()

	ctx := context.Background()

	pm, err := exec.Create(ctx, "PaymentMethod", map[string]interface{}{
		"merchantId":  "merchant-001",
		"type":        "iban",
		"value":       "GB82WEST12345698765432",
		"holderName":  "John Doe",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, pm["id"])
}

func TestPayment_AddIBANMethod_InvalidIBAN(t *testing.T) {
	exec, cleanup := setupPaymentEnv(t)
	defer cleanup()

	ctx := context.Background()

	_, err := exec.Create(ctx, "PaymentMethod", map[string]interface{}{
		"merchantId":  "merchant-001",
		"type":        "iban",
		"value":       "INVALID_IBAN",
		"holderName":  "Jane Doe",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule")
}

func TestPayment_ProcessPayment_HappyPath(t *testing.T) {
	exec, cleanup := setupPaymentEnv(t)
	defer cleanup()

	ctx := context.Background()

	rec, err := exec.Create(ctx, "PaymentRecord", map[string]interface{}{
		"apiKey":     "sk_test_abc123",
		"merchantId": "merchant-001",
		"methodId":   "method-001",
		"amount":     250.0,
		"currency":   "USD",
		"buyerEmail": "buyer@example.com",
		"cardToken":  "tok_visa_4242",
		"webhookUrl": "https://acme.com/webhooks/payments",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, rec["id"])
	assert.Equal(t, "progress", rec["status"])

	var outboxCount int
	err = exec.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE entity_id=$1 AND is_compensation=FALSE`,
		rec["id"],
	).Scan(&outboxCount)
	require.NoError(t, err)
	assert.Equal(t, 2, outboxCount)

	var compensationCount int
	err = exec.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE entity_id=$1 AND is_compensation=TRUE AND status='held'`,
		rec["id"],
	).Scan(&compensationCount)
	require.NoError(t, err)
	assert.Equal(t, 1, compensationCount)
}

func TestPayment_SagaCompensation(t *testing.T) {
	exec, cleanup := setupPaymentEnv(t)
	defer cleanup()

	ctx := context.Background()

	exec.ExternalManager().Register("NotifyMerchant", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return nil, errors.New("webhook endpoint unreachable")
	})

	rec, err := exec.Create(ctx, "PaymentRecord", map[string]interface{}{
		"apiKey":     "sk_test_abc123",
		"merchantId": "merchant-001",
		"methodId":   "method-001",
		"amount":     100.0,
		"currency":   "EUR",
		"buyerEmail": "buyer@example.com",
		"cardToken":  "tok_mc_5555",
		"webhookUrl": "https://acme.com/webhooks/payments",
	})
	require.NoError(t, err)
	entityID := rec["id"].(string)

	processor := runtime.NewOutboxProcessor(exec.ExternalManager(), exec.DB())
	processor.Start(100 * time.Millisecond)
	defer processor.Stop()

	deadline := time.Now().Add(15 * time.Second)
	var finalStatus string
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		var status string
		exec.DB().QueryRowContext(ctx,
			`SELECT status FROM "payment_record" WHERE id=$1`, entityID,
		).Scan(&status)
		if status == "compensating" || status == "compensated" || status == "failed" {
			finalStatus = status
			break
		}
	}

	assert.NotEmpty(t, finalStatus, "entity should have reached a saga terminal state")

	var refundRow int
	exec.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE entity_id=$1 AND is_compensation=TRUE AND status IN ('compensated','pending')`,
		entityID,
	).Scan(&refundRow)
	assert.Greater(t, refundRow, 0, "RefundCard compensation should have been triggered")
}

func TestPayment_CreateRefund(t *testing.T) {
	exec, cleanup := setupPaymentEnv(t)
	defer cleanup()

	ctx := context.Background()

	refund, err := exec.Create(ctx, "Refund", map[string]interface{}{
		"paymentId": "pay_001",
		"amount":    50.0,
		"reason":    "Customer request",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, refund["id"])
}
