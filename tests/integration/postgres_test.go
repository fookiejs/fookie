package integration

import (
	"context"
	"database/sql"
	"fmt"
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

type testEnv struct {
	db       *sql.DB
	executor *runtime.Executor
	schema   *ast.Schema
}

func setupPostgres(t *testing.T) (*testEnv, func()) {
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

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping())

	schema := buildTestSchema()
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
			return nil, fmt.Errorf("empty token")
		}
		return map[string]interface{}{
			"valid":  true,
			"userId": "user-test-001",
		}, nil
	})

	exec.ExternalManager().Register("FraudCheck", func(_ context.Context, input map[string]interface{}) (map[string]interface{}, error) {
		amount, _ := input["amount"].(float64)
		return map[string]interface{}{
			"allowed": amount <= 10_000,
			"score":   float64(int(amount / 100)),
		}, nil
	})

	cleanup := func() {
		_ = db.Close()
		_ = pgContainer.Terminate(ctx)
	}

	return &testEnv{db: db, executor: exec, schema: schema}, cleanup
}

func buildTestSchema() *ast.Schema {
	strPtr := func(s string) *string { return &s }

	walletModel := &ast.Model{
		Name: "Wallet",
		Fields: []*ast.Field{
			{Name: "userId", Type: ast.TypeString},
			{Name: "balance", Type: ast.TypeNumber},
			{Name: "label", Type: ast.TypeString},
		},
		CRUD: map[string]*ast.Operation{
			"create": {
				Type:   "create",
				Modify: modifyBlock(map[string]ast.Expression{
					"userId":  fieldAccess("input", "userId"),
					"balance": fieldAccess("input", "balance"),
					"label":   fieldAccess("input", "label"),
				}),
			},
			"read": {
				Type: "read",
				Select: []*ast.SelectField{
					{Expr: ast.PlainField{Path: []string{"id"}}},
					{Expr: ast.PlainField{Path: []string{"balance"}}},
					{Expr: ast.PlainField{Path: []string{"label"}}},
					{Expr: ast.PlainField{Path: []string{"status"}}},
				},
			},
			"update": {
				Type: "update",
				Modify: modifyBlock(map[string]ast.Expression{
					"balance": fieldAccess("input", "balance"),
				}),
			},
			"delete": {
				Type: "delete",
			},
		},
	}

	txModel := &ast.Model{
		Name: "Transaction",
		Fields: []*ast.Field{
			{Name: "fromWalletId", Type: ast.TypeRelation, Relation: strPtr("Wallet")},
			{Name: "toWalletId", Type: ast.TypeRelation, Relation: strPtr("Wallet")},
			{Name: "amount", Type: ast.TypeNumber},
			{Name: "score", Type: ast.TypeNumber},
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
									"token": fieldAccess("input", "token"),
								},
							},
						},
					},
				},
				Rule: &ast.Block{
					Statements: []ast.Statement{
						&ast.PredicateExpr{
							Expr: &ast.BinaryOp{
								Left:  fieldAccess("input", "amount"),
								Op:    ">",
								Right: &ast.Literal{Value: float64(0)},
							},
						},
						&ast.Assignment{
							Name: "check",
							Value: &ast.ExternalCall{
								Name: "FraudCheck",
								Params: map[string]ast.Expression{
									"amount": fieldAccess("input", "amount"),
								},
							},
						},
						&ast.PredicateExpr{
							Expr: &ast.BinaryOp{
								Left:  fieldAccess("check", "allowed"),
								Op:    "==",
								Right: &ast.Literal{Value: true},
							},
						},
					},
				},
				Modify: modifyBlock(map[string]ast.Expression{
					"fromWalletId": fieldAccess("input", "fromWalletId"),
					"toWalletId":   fieldAccess("input", "toWalletId"),
					"amount":       fieldAccess("input", "amount"),
					"score":        fieldAccess("check", "score"),
				}),
			},
			"read": {
				Type: "read",
				Select: []*ast.SelectField{
					{Alias: "total", Expr: &ast.AggregateFunc{Fn: "sum", Field: []string{"amount"}}},
					{Alias: "count", Expr: &ast.AggregateFunc{Fn: "count", Field: []string{"id"}}},
				},
			},
		},
	}

	return &ast.Schema{
		Models: []*ast.Model{walletModel, txModel},
		Externals: []*ast.External{
			{
				Name:   "ValidateToken",
				Input:  map[string]string{"token": "string"},
				Output: map[string]string{"valid": "boolean", "userId": "id"},
			},
			{
				Name:   "FraudCheck",
				Input:  map[string]string{"amount": "number"},
				Output: map[string]string{"allowed": "boolean", "score": "number"},
			},
		},
	}
}

func TestCreateWallet(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	result, err := env.executor.Create(ctx, "Wallet", map[string]interface{}{
		"userId":  "user-001",
		"balance": 500.00,
		"label":   "Main Wallet",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result["id"])
	assert.Equal(t, "done", result["status"])
}

func TestReadWallets(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := env.executor.Create(ctx, "Wallet", map[string]interface{}{
			"userId":  fmt.Sprintf("user-%d", i),
			"balance": float64(i+1) * 100,
			"label":   fmt.Sprintf("Wallet %d", i),
		})
		require.NoError(t, err)
	}

	rows, err := env.executor.Read(ctx, "Wallet", map[string]interface{}{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(rows), 3)
}

func TestUpdateWallet(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	created, err := env.executor.Create(ctx, "Wallet", map[string]interface{}{
		"userId":  "user-upd",
		"balance": 200.00,
		"label":   "Update Test",
	})
	require.NoError(t, err)
	id := created["id"].(string)

	updated, err := env.executor.Update(ctx, "Wallet", id, map[string]interface{}{
		"balance": 999.00,
	})
	require.NoError(t, err)
	assert.Equal(t, float64(999), updated["balance"])
}

func TestSoftDeleteWallet(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	created, err := env.executor.Create(ctx, "Wallet", map[string]interface{}{
		"userId":  "user-del",
		"balance": 100.00,
		"label":   "Delete Test",
	})
	require.NoError(t, err)
	id := created["id"].(string)

	err = env.executor.Delete(ctx, "Wallet", id, map[string]interface{}{})
	require.NoError(t, err)

	rows, err := env.executor.Read(ctx, "Wallet", map[string]interface{}{})
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, id, r["id"])
	}
}

func TestCreateTransaction_ValidAmount(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	w1, _ := env.executor.Create(ctx, "Wallet", map[string]interface{}{"userId": "u1", "balance": 1000.0, "label": "W1"})
	w2, _ := env.executor.Create(ctx, "Wallet", map[string]interface{}{"userId": "u2", "balance": 500.0, "label": "W2"})

	result, err := env.executor.Create(ctx, "Transaction", map[string]interface{}{
		"token":        "valid-jwt-token",
		"fromWalletId": w1["id"],
		"toWalletId":   w2["id"],
		"amount":       250.0,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, result["id"])
	assert.Equal(t, float64(2), result["score"])
}

func TestCreateTransaction_AmountZero(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	_, err := env.executor.Create(ctx, "Transaction", map[string]interface{}{
		"token":        "valid-jwt-token",
		"fromWalletId": "wallet-1",
		"toWalletId":   "wallet-2",
		"amount":       0.0,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "assertion failed")
}

func TestCreateTransaction_FraudBlocked(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	_, err := env.executor.Create(ctx, "Transaction", map[string]interface{}{
		"token":        "valid-jwt-token",
		"fromWalletId": "wallet-1",
		"toWalletId":   "wallet-2",
		"amount":       99999.0,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "assertion failed")
}

func TestAggregateRead_SumAndCount(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()

	ctx := context.Background()

	w1, _ := env.executor.Create(ctx, "Wallet", map[string]interface{}{"userId": "ua1", "balance": 100.0, "label": "A"})
	w2, _ := env.executor.Create(ctx, "Wallet", map[string]interface{}{"userId": "ua2", "balance": 200.0, "label": "B"})

	for _, amount := range []float64{100, 200, 300} {
		_, err := env.executor.Create(ctx, "Transaction", map[string]interface{}{
			"token":        "valid-jwt-token",
			"fromWalletId": w1["id"],
			"toWalletId":   w2["id"],
			"amount":       amount,
		})
		require.NoError(t, err)
	}

	rows, err := env.executor.Read(ctx, "Transaction", map[string]interface{}{})
	require.NoError(t, err)
	require.Len(t, rows, 1, "aggregate query should return one row")

	row := rows[0]
	t.Logf("aggregate result: %+v", row)

	totalRaw := row["total"]
	assert.NotNil(t, totalRaw, "total sum should be present")

	countRaw := row["count"]
	assert.NotNil(t, countRaw, "count should be present")
}

func TestOutboxIsQueued(t *testing.T) {
	env, cleanup := setupPostgres(t)
	defer cleanup()
	ctx := context.Background()

	env.schema.Models[0].CRUD["create"].Effect = &ast.Block{
		Statements: []ast.Statement{
			&ast.PredicateExpr{
				Expr: &ast.ExternalCall{
					Name: "SendNotification",
					Params: map[string]ast.Expression{
						"userId": fieldAccess("output", "id"),
					},
				},
			},
		},
	}

	_, err := env.executor.Create(ctx, "Wallet", map[string]interface{}{
		"userId":  "user-notif",
		"balance": 300.0,
		"label":   "Notif Test",
	})
	require.NoError(t, err)

	var count int
	err = env.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE external_name = 'SendNotification'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func modifyBlock(assignments map[string]ast.Expression) *ast.Block {
	b := &ast.Block{}
	for field, expr := range assignments {
		b.Statements = append(b.Statements, &ast.ModifyAssignment{
			Field: field,
			Value: expr,
		})
	}
	return b
}

func fieldAccess(object string, fields ...string) *ast.FieldAccess {
	return &ast.FieldAccess{Object: object, Fields: fields}
}
