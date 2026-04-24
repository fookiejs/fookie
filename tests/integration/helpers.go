package integration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/parser"
	fookieruntime "github.com/fookiejs/fookie/pkg/runtime"
)

func projectRoot() string {
	_, filename, _, _ := goruntime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

func parseDemoSchema(t *testing.T) *ast.Schema {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(projectRoot(), "demo", "schema.fql"))
	require.NoError(t, err)

	lexer := parser.NewLexer(string(content))
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()
	require.NoError(t, err)
	return schema
}

func setupDB(t *testing.T) (*fookieruntime.Executor, *sql.DB, func()) {
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

	schema := parseDemoSchema(t)
	sqlGen := compiler.NewSQLGenerator(schema)
	sqls, err := sqlGen.Generate()
	require.NoError(t, err)

	for _, s := range sqls {
		_, err := db.ExecContext(ctx, s)
		require.NoError(t, err, "DDL failed:\n%s", s)
	}

	logger := &testLogger{t: t}
	exec := fookieruntime.NewExecutor(db, schema, logger)

	cleanup := func() {
		db.Close()
		pgContainer.Terminate(ctx)
	}
	return exec, db, cleanup
}

type testLogger struct{ t *testing.T }

func (l *testLogger) Info(msg string, args ...interface{})  { l.t.Logf("INFO  "+msg, args...) }
func (l *testLogger) Warn(msg string, args ...interface{})  { l.t.Logf("WARN  "+msg, args...) }
func (l *testLogger) Error(msg string, args ...interface{}) { l.t.Logf("ERROR "+msg, args...) }
