package integration

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/parser"
)

func OpenDB(driver, dsn string) (*sql.DB, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func ParseSchemaFromFile(filename string) (*ast.Schema, error) {
	fqlPath := filepath.Join("../..", "schemas", filename+".fql")

	absPath, err := filepath.Abs(fqlPath)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	lexer := parser.NewLexer(string(content))
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	return p.Parse()
}

type testLogger struct {
	t *testing.T
}

func (l *testLogger) Info(msg string, args ...interface{})  {
	l.t.Logf("INFO  "+msg, args...)
}

func (l *testLogger) Warn(msg string, args ...interface{})  {
	l.t.Logf("WARN  "+msg, args...)
}

func (l *testLogger) Error(msg string, args ...interface{}) {
	l.t.Logf("ERROR "+msg, args...)
}
