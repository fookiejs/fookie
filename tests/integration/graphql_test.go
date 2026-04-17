package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fookiejs/fookie/pkg/compiler"
	fookiegql "github.com/fookiejs/fookie/pkg/graphql"
	"github.com/fookiejs/fookie/pkg/runtime"
)

type gqlResponse struct {
	Data   map[string]interface{} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func setupGraphQLServer(t *testing.T, schemaName string) (*httptest.Server, *runtime.Executor, func()) {
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

	schema, err := ParseSchemaFromFile(schemaName)
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

	gqlSchema, err := fookiegql.BuildSchema(schema)
	require.NoError(t, err)

	handler := fookiegql.NewHandler(exec, gqlSchema)
	ts := httptest.NewServer(handler)

	cleanup := func() {
		ts.Close()
		db.Close()
		pgContainer.Terminate(ctx)
	}

	return ts, exec, cleanup
}

func doGraphQL(t *testing.T, serverURL, query string, variables map[string]interface{}, token string) gqlResponse {
	t.Helper()
	body := map[string]interface{}{"query": query}
	if variables != nil {
		body["variables"] = variables
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", serverURL, bytes.NewReader(jsonBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result gqlResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

func TestGraphQLIntegration_CreateUser(t *testing.T) {
	ts, _, cleanup := setupGraphQLServer(t, "wallet_transfer")
	defer cleanup()

	result := doGraphQL(t, ts.URL, `
		mutation {
			createUser(input: { email: "test@example.com", name: "Test User" }) {
				id
				email
				name
				status
			}
		}
	`, nil, "")

	require.Empty(t, result.Errors, "expected no errors, got: %v", result.Errors)
	user := result.Data["createUser"].(map[string]interface{})
	assert.NotEmpty(t, user["id"])
	assert.Equal(t, "test@example.com", user["email"])
	assert.Equal(t, "Test User", user["name"])
	assert.Equal(t, "done", user["status"])
}

func TestGraphQLIntegration_ReadUsers(t *testing.T) {
	ts, _, cleanup := setupGraphQLServer(t, "wallet_transfer")
	defer cleanup()

	doGraphQL(t, ts.URL, `
		mutation { createUser(input: { email: "a@example.com", name: "Alice" }) { id } }
	`, nil, "")
	doGraphQL(t, ts.URL, `
		mutation { createUser(input: { email: "b@example.com", name: "Bob" }) { id } }
	`, nil, "")

	result := doGraphQL(t, ts.URL, `
		query { users { id email name status } }
	`, nil, "")

	require.Empty(t, result.Errors)
	users := result.Data["users"].([]interface{})
	assert.Len(t, users, 2)
}

func TestGraphQLIntegration_CreateWallet(t *testing.T) {
	ts, _, cleanup := setupGraphQLServer(t, "wallet_transfer")
	defer cleanup()

	userResult := doGraphQL(t, ts.URL, `
		mutation { createUser(input: { email: "user@example.com", name: "User" }) { id } }
	`, nil, "")
	userID := userResult.Data["createUser"].(map[string]interface{})["id"].(string)

	result := doGraphQL(t, ts.URL, `
		mutation($input: CreateWalletInput!) {
			createWallet(input: $input) { id userId balance currency status }
		}
	`, map[string]interface{}{
		"input": map[string]interface{}{
			"userId":   userID,
			"balance":  500.0,
			"currency": "USD",
			"label":    "Main",
		},
	}, "")

	require.Empty(t, result.Errors, "expected no errors, got: %v", result.Errors)
	wallet := result.Data["createWallet"].(map[string]interface{})
	assert.NotEmpty(t, wallet["id"])
	assert.Equal(t, "done", wallet["status"])
}

func TestGraphQLIntegration_CreateTransaction_WithAuth(t *testing.T) {
	ts, exec, cleanup := setupGraphQLServer(t, "wallet_transfer")
	defer cleanup()

	exec.ExternalManager().Register("ValidateToken", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		token, _ := input["token"].(string)
		if token == "valid-jwt" {
			return map[string]interface{}{"userId": "user-001", "valid": true}, nil
		}
		return map[string]interface{}{"valid": false}, nil
	})

	exec.ExternalManager().Register("SendTransferNotification", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"messageId": "msg-001", "sent": true}, nil
	})

	aliceRes := doGraphQL(t, ts.URL, `mutation { createUser(input: { email: "alice@example.com", name: "Alice" }) { id } }`, nil, "")
	aliceID := aliceRes.Data["createUser"].(map[string]interface{})["id"].(string)

	bobRes := doGraphQL(t, ts.URL, `mutation { createUser(input: { email: "bob@example.com", name: "Bob" }) { id } }`, nil, "")
	bobID := bobRes.Data["createUser"].(map[string]interface{})["id"].(string)

	walletARes := doGraphQL(t, ts.URL, `mutation($input: CreateWalletInput!) { createWallet(input: $input) { id } }`,
		map[string]interface{}{"input": map[string]interface{}{"userId": aliceID, "balance": 1000.0, "currency": "USD", "label": "Main"}}, "")
	walletAID := walletARes.Data["createWallet"].(map[string]interface{})["id"].(string)

	walletBRes := doGraphQL(t, ts.URL, `mutation($input: CreateWalletInput!) { createWallet(input: $input) { id } }`,
		map[string]interface{}{"input": map[string]interface{}{"userId": bobID, "balance": 0.0, "currency": "USD", "label": "Main"}}, "")
	walletBID := walletBRes.Data["createWallet"].(map[string]interface{})["id"].(string)

	result := doGraphQL(t, ts.URL, `
		mutation($input: CreateTransactionInput!) {
			createTransaction(input: $input) { id status }
		}
	`, map[string]interface{}{
		"input": map[string]interface{}{
			"fromWalletId":   walletAID,
			"toWalletId":     walletBID,
			"amount":         100.0,
			"currency":       "USD",
			"riskScore":      0,
			"recipientEmail": "bob@example.com",
		},
	}, "valid-jwt")

	require.Empty(t, result.Errors, "expected no errors, got: %v", result.Errors)
	tx := result.Data["createTransaction"].(map[string]interface{})
	assert.NotEmpty(t, tx["id"])
	assert.Equal(t, "progress", tx["status"])
}

func TestGraphQLIntegration_CreateTransaction_NoAuth(t *testing.T) {
	ts, exec, cleanup := setupGraphQLServer(t, "wallet_transfer")
	defer cleanup()

	exec.ExternalManager().Register("ValidateToken", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"valid": false}, nil
	})

	result := doGraphQL(t, ts.URL, `
		mutation($input: CreateTransactionInput!) {
			createTransaction(input: $input) { id status }
		}
	`, map[string]interface{}{
		"input": map[string]interface{}{
			"fromWalletId":   "00000000-0000-0000-0000-000000000001",
			"toWalletId":     "00000000-0000-0000-0000-000000000002",
			"amount":         100.0,
			"currency":       "USD",
			"riskScore":      0,
			"recipientEmail": "bob@example.com",
		},
	}, "")

	assert.NotEmpty(t, result.Errors, "should fail without valid auth token")
}

func TestGraphQLIntegration_CreateMerchant_PaymentSchema(t *testing.T) {
	ts, exec, cleanup := setupGraphQLServer(t, "payment_processing")
	defer cleanup()

	exec.ExternalManager().Register("VerifyMerchant", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"valid": true, "merchantId": "m-001", "tier": "standard"}, nil
	})

	result := doGraphQL(t, ts.URL, `
		mutation {
			createMerchant(input: {
				name: "Acme Corp"
				email: "billing@acme.com"
				webhookUrl: "https://acme.com/hook"
				apiKey: "sk_test_123"
			}) { id name email status }
		}
	`, nil, "")

	require.Empty(t, result.Errors, "expected no errors, got: %v", result.Errors)
	merchant := result.Data["createMerchant"].(map[string]interface{})
	assert.NotEmpty(t, merchant["id"])
	assert.Equal(t, "done", merchant["status"])
}

func TestGraphQLIntegration_InvalidEmail_Rejected(t *testing.T) {
	ts, _, cleanup := setupGraphQLServer(t, "wallet_transfer")
	defer cleanup()

	result := doGraphQL(t, ts.URL, `
		mutation {
			createUser(input: { email: "not-an-email", name: "Bad" }) { id }
		}
	`, nil, "")

	assert.NotEmpty(t, result.Errors, "should reject invalid email")
}
