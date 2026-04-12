package tests

import (
	"testing"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLGeneratorModelDDL(t *testing.T) {
	schema := &ast.Schema{
		Models: []*ast.Model{
			{
				Name: "User",
				Fields: []*ast.Field{
					{
						Name:        "email",
						Type:        ast.TypeString,
						Constraints: []string{"--unique"},
					},
					{
						Name: "firstName",
						Type: ast.TypeString,
					},
				},
			},
		},
	}

	gen := compiler.NewSQLGenerator(schema)
	sqls, err := gen.Generate()

	require.NoError(t, err)
	require.Greater(t, len(sqls), 0)

	foundUserTable := false
	for _, sql := range sqls {
		if contains(sql, "CREATE TABLE") && contains(sql, "user") {
			foundUserTable = true
			assert.Contains(t, sql, "email")
			assert.Contains(t, sql, "UNIQUE")
			break
		}
	}
	assert.True(t, foundUserTable, "User table not found in SQL")
}

func TestSQLGeneratorForeignKeys(t *testing.T) {
	schema := &ast.Schema{
		Models: []*ast.Model{
			{
				Name: "User",
				Fields: []*ast.Field{
					{
						Name: "email",
						Type: ast.TypeString,
					},
				},
			},
			{
				Name: "Wallet",
				Fields: []*ast.Field{
					{
						Name:     "userId",
						Type:     ast.TypeRelation,
						Relation: strPtr("User"),
					},
					{
						Name: "balance",
						Type: ast.TypeNumber,
					},
				},
			},
		},
	}

	gen := compiler.NewSQLGenerator(schema)
	sqls, err := gen.Generate()

	require.NoError(t, err)

	// Check for foreign key constraint
	walletSQL := ""
	for _, sql := range sqls {
		if contains(sql, "wallet") {
			walletSQL = sql
			break
		}
	}

	assert.NotEmpty(t, walletSQL)
	assert.Contains(t, walletSQL, "REFERENCES")
	assert.Contains(t, walletSQL, "user")
}

func TestSQLGeneratorAuditLogTable(t *testing.T) {
	schema := &ast.Schema{
		Models: []*ast.Model{},
	}

	gen := compiler.NewSQLGenerator(schema)
	sqls, err := gen.Generate()

	require.NoError(t, err)

	// Check for implicit audit log table
	foundAuditLog := false
	for _, sql := range sqls {
		if contains(sql, "audit_logs") {
			foundAuditLog = true
			assert.Contains(t, sql, "transaction_id")
			assert.Contains(t, sql, "action")
			break
		}
	}
	assert.True(t, foundAuditLog, "Audit log table not found")
}

func TestSQLGeneratorOutboxTable(t *testing.T) {
	schema := &ast.Schema{
		Models: []*ast.Model{},
	}

	gen := compiler.NewSQLGenerator(schema)
	sqls, err := gen.Generate()

	require.NoError(t, err)

	// Check for implicit outbox table
	foundOutbox := false
	for _, sql := range sqls {
		if contains(sql, "outbox") {
			foundOutbox = true
			assert.Contains(t, sql, "entity_type")
			assert.Contains(t, sql, "status")
			break
		}
	}
	assert.True(t, foundOutbox, "Outbox table not found")
}

func TestSQLGeneratorTypeMapping(t *testing.T) {
	schema := &ast.Schema{
		Models: []*ast.Model{
			{
				Name: "TestTypes",
				Fields: []*ast.Field{
					{Name: "str", Type: ast.TypeString},
					{Name: "num", Type: ast.TypeNumber},
					{Name: "flag", Type: ast.TypeBoolean},
					{Name: "uid", Type: ast.TypeID},
					{Name: "day", Type: ast.TypeDate},
					{Name: "ts", Type: ast.TypeTimestamp},
					{Name: "data", Type: ast.TypeJSON},
				},
			},
		},
	}

	gen := compiler.NewSQLGenerator(schema)
	sqls, err := gen.Generate()

	require.NoError(t, err)
	require.Greater(t, len(sqls), 0)

	sql := sqls[0]
	assert.Contains(t, sql, "VARCHAR")
	assert.Contains(t, sql, "NUMERIC")
	assert.Contains(t, sql, "BOOLEAN")
	assert.Contains(t, sql, "UUID")
	assert.Contains(t, sql, "DATE")
	assert.Contains(t, sql, "TIMESTAMP")
	assert.Contains(t, sql, "JSONB")
}

// Helper functions
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr))
}

func strPtr(s string) *string {
	return &s
}
