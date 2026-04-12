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

	"github.com/fookiejs/fookie/pkg/ast"
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

	schema := buildWalletTransferSchema()
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

func buildWalletTransferSchema() *ast.Schema {
	strPtr := func(s string) *string { return &s }

	userModel := &ast.Model{
		Name: "User",
		Fields: []*ast.Field{
			{Name: "email", Type: ast.TypeEmail},
			{Name: "name", Type: ast.TypeString},
		},
		CRUD: map[string]*ast.Operation{
			"create": {
				Type: "create",
				Modify: &ast.Block{
					Statements: []ast.Statement{
						&ast.ModifyAssignment{
							Field: "email",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"email"}},
						},
						&ast.ModifyAssignment{
							Field: "name",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"name"}},
						},
					},
				},
			},
			"read": {
				Type: "read",
				Select: []*ast.SelectField{
					{Expr: ast.PlainField{Path: []string{"id"}}},
					{Expr: ast.PlainField{Path: []string{"email"}}},
					{Expr: ast.PlainField{Path: []string{"name"}}},
				},
			},
		},
	}

	walletModel := &ast.Model{
		Name: "Wallet",
		Fields: []*ast.Field{
			{Name: "userId", Type: ast.TypeID},
			{Name: "balance", Type: ast.TypeNumber},
			{Name: "currency", Type: ast.TypeCurrency},
			{Name: "label", Type: ast.TypeString},
		},
		CRUD: map[string]*ast.Operation{
			"create": {
				Type: "create",
				Rule: &ast.Block{
					Statements: []ast.Statement{
						&ast.PredicateExpr{
							Expr: &ast.BinaryOp{
								Left:  &ast.FieldAccess{Object: "input", Fields: []string{"balance"}},
								Op:    ">=",
								Right: &ast.Literal{Value: 0.0},
							},
						},
					},
				},
				Modify: &ast.Block{
					Statements: []ast.Statement{
						&ast.ModifyAssignment{
							Field: "userId",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"userId"}},
						},
						&ast.ModifyAssignment{
							Field: "balance",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"balance"}},
						},
						&ast.ModifyAssignment{
							Field: "currency",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"currency"}},
						},
						&ast.ModifyAssignment{
							Field: "label",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"label"}},
						},
					},
				},
			},
			"read": {
				Type: "read",
				Select: []*ast.SelectField{
					{Expr: ast.PlainField{Path: []string{"id"}}},
					{Expr: ast.PlainField{Path: []string{"userId"}}},
					{Expr: ast.PlainField{Path: []string{"balance"}}},
					{Expr: ast.PlainField{Path: []string{"currency"}}},
				},
			},
		},
	}

	txModel := &ast.Model{
		Name: "Transaction",
		Fields: []*ast.Field{
			{Name: "fromWalletId", Type: ast.TypeRelation, Relation: strPtr("Wallet")},
			{Name: "toWalletId", Type: ast.TypeRelation, Relation: strPtr("Wallet")},
			{Name: "amount", Type: ast.TypeNumber},
			{Name: "currency", Type: ast.TypeCurrency},
			{Name: "status", Type: ast.TypeString},
			{Name: "riskScore", Type: ast.TypeNumber},
		},
		CRUD: map[string]*ast.Operation{
			"create": {
				Type: "create",
				Role: &ast.Block{
					Statements: []ast.Statement{
						&ast.Assignment{
							Name: "principal",
							Value: &ast.ExternalCall{
								Name: "ValidateToken",
								Params: map[string]ast.Expression{
									"token": &ast.FieldAccess{Object: "input", Fields: []string{"token"}},
								},
							},
						},
					},
				},
				Rule: &ast.Block{
					Statements: []ast.Statement{
						&ast.PredicateExpr{
							Expr: &ast.BinaryOp{
								Left:  &ast.FieldAccess{Object: "principal", Fields: []string{"valid"}},
								Op:    "==",
								Right: &ast.Literal{Value: true},
							},
						},
						&ast.PredicateExpr{
							Expr: &ast.BinaryOp{
								Left:  &ast.FieldAccess{Object: "input", Fields: []string{"amount"}},
								Op:    ">",
								Right: &ast.Literal{Value: 0.0},
							},
						},
						&ast.PredicateExpr{
							Expr: &ast.BinaryOp{
								Left:  &ast.FieldAccess{Object: "input", Fields: []string{"amount"}},
								Op:    "<=",
								Right: &ast.Literal{Value: 1000000.0},
							},
						},
					},
				},
				Modify: &ast.Block{
					Statements: []ast.Statement{
						&ast.ModifyAssignment{
							Field: "fromWalletId",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"fromWalletId"}},
						},
						&ast.ModifyAssignment{
							Field: "toWalletId",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"toWalletId"}},
						},
						&ast.ModifyAssignment{
							Field: "amount",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"amount"}},
						},
						&ast.ModifyAssignment{
							Field: "currency",
							Value: &ast.FieldAccess{Object: "input", Fields: []string{"currency"}},
						},
						&ast.ModifyAssignment{
							Field: "status",
							Value: &ast.Literal{Value: "pending"},
						},
						&ast.ModifyAssignment{
							Field: "riskScore",
							Value: &ast.Literal{Value: 0.0},
						},
					},
				},
				Effect: &ast.Block{
					Statements: []ast.Statement{
						&ast.PredicateExpr{
							Expr: &ast.ExternalCall{
								Name: "SendTransferNotification",
								Params: map[string]ast.Expression{
									"userId":           &ast.FieldAccess{Object: "principal", Fields: []string{"userId"}},
									"amount":           &ast.FieldAccess{Object: "input", Fields: []string{"amount"}},
									"recipientEmail":   &ast.FieldAccess{Object: "input", Fields: []string{"recipientEmail"}},
								},
							},
						},
					},
				},
			},
			"read": {
				Type: "read",
				Select: []*ast.SelectField{
					{Alias: "total", Expr: &ast.AggregateFunc{Fn: "sum", Field: []string{"amount"}}},
					{Alias: "count", Expr: &ast.AggregateFunc{Fn: "count", Field: []string{"id"}}},
					{Alias: "avgRisk", Expr: &ast.AggregateFunc{Fn: "avg", Field: []string{"riskScore"}}},
				},
			},
		},
	}

	return &ast.Schema{
		Models: []*ast.Model{userModel, walletModel, txModel},
		Externals: []*ast.External{
			{
				Name: "ValidateToken",
				Input: map[string]string{"token": "string"},
				Output: map[string]string{"valid": "boolean", "userId": "id"},
			},
			{
				Name: "FraudCheck",
				Input: map[string]string{"amount": "number", "fromWalletId": "id", "toWalletId": "id"},
				Output: map[string]string{"allowed": "boolean", "riskScore": "number"},
			},
			{
				Name: "SendTransferNotification",
				Input: map[string]string{"userId": "id", "amount": "number", "recipientEmail": "email"},
				Output: map[string]string{"messageId": "string", "sent": "boolean"},
			},
		},
	}
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
