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

func setupOnboardingEnv(t *testing.T) (*runtime.Executor, func()) {
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

	schema, err := ParseSchemaFromFile("user_onboarding")
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

	exec.ExternalManager().Register("ValidateIdentity", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"valid":       true,
			"fullName":    "John Doe",
			"dateOfBirth": "1990-01-01",
		}, nil
	})

	exec.ExternalManager().Register("SendWelcomeEmail", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"sent":      true,
			"messageId": "msg-welcome-001",
		}, nil
	})

	exec.ExternalManager().Register("MarkAccountFailed", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"marked": true}, nil
	})

	exec.ExternalManager().Register("GeoIPLookup", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"country": "TR",
			"city":    "Istanbul",
		}, nil
	})

	cleanup := func() {
		db.Close()
		pgContainer.Terminate(ctx)
	}

	return exec, cleanup
}

func TestOnboarding_CreateAccount(t *testing.T) {
	exec, cleanup := setupOnboardingEnv(t)
	defer cleanup()

	ctx := context.Background()

	acc, err := exec.Create(ctx, "Account", map[string]interface{}{
		"email":          "user@example.com",
		"phone":          "+905551234567",
		"registrationIp": "93.184.216.34",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, acc["id"])
	assert.Equal(t, "done", acc["status"])
}

func TestOnboarding_RejectPrivateIP(t *testing.T) {
	exec, cleanup := setupOnboardingEnv(t)
	defer cleanup()

	ctx := context.Background()

	_, err := exec.Create(ctx, "Account", map[string]interface{}{
		"email":          "user@example.com",
		"phone":          "+905551234567",
		"registrationIp": "192.168.1.1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule")
}

func TestOnboarding_RejectNonIPv4(t *testing.T) {
	exec, cleanup := setupOnboardingEnv(t)
	defer cleanup()

	ctx := context.Background()

	_, err := exec.Create(ctx, "Account", map[string]interface{}{
		"email":          "user@example.com",
		"phone":          "+905551234567",
		"registrationIp": "2001:db8::1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule")
}

func TestOnboarding_CreateProfile_WithIBANAndCoords(t *testing.T) {
	exec, cleanup := setupOnboardingEnv(t)
	defer cleanup()

	ctx := context.Background()

	acc, err := exec.Create(ctx, "Account", map[string]interface{}{
		"email":          "user@example.com",
		"phone":          "+905551234567",
		"registrationIp": "93.184.216.34",
	})
	require.NoError(t, err)

	profile, err := exec.Create(ctx, "Profile", map[string]interface{}{
		"accountId": acc["id"],
		"fullName":  "John Doe",
		"iban":      "GB82WEST12345698765432",
		"lat":       51.5074,
		"lng":       -0.1278,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, profile["id"])
}

func TestOnboarding_RejectNonGBIBAN(t *testing.T) {
	exec, cleanup := setupOnboardingEnv(t)
	defer cleanup()

	ctx := context.Background()

	acc, err := exec.Create(ctx, "Account", map[string]interface{}{
		"email":          "user@example.com",
		"phone":          "+905551234567",
		"registrationIp": "93.184.216.34",
	})
	require.NoError(t, err)

	_, err = exec.Create(ctx, "Profile", map[string]interface{}{
		"accountId": acc["id"],
		"fullName":  "John Doe",
		"iban":      "DE89370400440532013000",
		"lat":       52.5200,
		"lng":       13.4050,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule")
}

func TestOnboarding_SubmitKYC(t *testing.T) {
	exec, cleanup := setupOnboardingEnv(t)
	defer cleanup()

	ctx := context.Background()

	acc, _ := exec.Create(ctx, "Account", map[string]interface{}{
		"email":          "user@example.com",
		"phone":          "+905551234567",
		"registrationIp": "93.184.216.34",
	})

	profile, _ := exec.Create(ctx, "Profile", map[string]interface{}{
		"accountId": acc["id"],
		"fullName":  "John Doe",
		"iban":      "GB82WEST12345698765432",
		"lat":       51.5074,
		"lng":       -0.1278,
	})

	kyc, err := exec.Create(ctx, "KycVerification", map[string]interface{}{
		"profileId":      profile["id"],
		"documentType":   "passport",
		"documentNumber": "AB123456",
		"email":          "user@example.com",
		"fullName":       "John Doe",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, kyc["id"])
	assert.Equal(t, "progress", kyc["status"])

	var effectCount int
	exec.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE entity_id=$1 AND is_compensation=FALSE`,
		kyc["id"],
	).Scan(&effectCount)
	assert.Equal(t, 2, effectCount)

	var compensationCount int
	exec.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE entity_id=$1 AND is_compensation=TRUE AND status='held'`,
		kyc["id"],
	).Scan(&compensationCount)
	assert.Equal(t, 1, compensationCount)
}

func TestOnboarding_KYCSagaCompensation(t *testing.T) {
	exec, cleanup := setupOnboardingEnv(t)
	defer cleanup()

	ctx := context.Background()

	exec.ExternalManager().Register("SendWelcomeEmail", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return nil, errors.New("email provider unavailable")
	})

	acc, _ := exec.Create(ctx, "Account", map[string]interface{}{
		"email":          "user@example.com",
		"phone":          "+905551234567",
		"registrationIp": "93.184.216.34",
	})

	profile, _ := exec.Create(ctx, "Profile", map[string]interface{}{
		"accountId": acc["id"],
		"fullName":  "John Doe",
		"iban":      "GB82WEST12345698765432",
		"lat":       51.5074,
		"lng":       -0.1278,
	})

	kyc, err := exec.Create(ctx, "KycVerification", map[string]interface{}{
		"profileId":      profile["id"],
		"documentType":   "passport",
		"documentNumber": "AB123456",
		"email":          "user@example.com",
		"fullName":       "John Doe",
	})
	require.NoError(t, err)
	kycID := kyc["id"].(string)

	processor := runtime.NewOutboxProcessor(exec.ExternalManager(), exec.DB())
	processor.Start(100 * time.Millisecond)
	defer processor.Stop()

	deadline := time.Now().Add(15 * time.Second)
	var finalStatus string
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		var status string
		exec.DB().QueryRowContext(ctx,
			`SELECT status FROM "kyc_verification" WHERE id=$1`, kycID,
		).Scan(&status)
		if status == "compensating" || status == "compensated" || status == "failed" {
			finalStatus = status
			break
		}
	}

	assert.NotEmpty(t, finalStatus, "KYC should have reached a saga terminal state")

	var markCount int
	exec.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE entity_id=$1 AND is_compensation=TRUE AND status IN ('compensated','pending')`,
		kycID,
	).Scan(&markCount)
	assert.Greater(t, markCount, 0, "MarkAccountFailed compensation should have been triggered")
}
