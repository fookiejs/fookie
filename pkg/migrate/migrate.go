// Package migrate provides schema diffing and migration tracking for fookie.
//
// It compares the SQL DDL that the FQL compiler would generate against the
// live database state (via information_schema) and produces a minimal set of
// idempotent ALTER TABLE / CREATE TABLE / CREATE INDEX statements.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
)

// ColumnInfo describes a column as it exists in the live database.
type ColumnInfo struct {
	Table    string
	Column   string
	DataType string
	Nullable bool
}

// LiveSchema holds the current state of the database inferred from information_schema.
type LiveSchema struct {
	Tables  map[string]bool                  // table name → exists
	Columns map[string]map[string]ColumnInfo // table → column → info
	Indexes map[string]bool                  // index name → exists
}

// Introspect reads the live schema from the database.
func Introspect(ctx context.Context, db *sql.DB) (*LiveSchema, error) {
	ls := &LiveSchema{
		Tables:  make(map[string]bool),
		Columns: make(map[string]map[string]ColumnInfo),
		Indexes: make(map[string]bool),
	}

	// Tables
	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
	`)
	if err != nil {
		return nil, fmt.Errorf("introspect tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		ls.Tables[t] = true
		ls.Columns[t] = make(map[string]ColumnInfo)
	}

	// Columns
	rows2, err := db.QueryContext(ctx, `
		SELECT table_name, column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`)
	if err != nil {
		return nil, fmt.Errorf("introspect columns: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var tbl, col, dtype, nullable string
		if err := rows2.Scan(&tbl, &col, &dtype, &nullable); err != nil {
			return nil, err
		}
		if _, ok := ls.Columns[tbl]; !ok {
			ls.Columns[tbl] = make(map[string]ColumnInfo)
		}
		ls.Columns[tbl][col] = ColumnInfo{
			Table:    tbl,
			Column:   col,
			DataType: dtype,
			Nullable: nullable == "YES",
		}
	}

	// Index names
	rows3, err := db.QueryContext(ctx, `
		SELECT indexname FROM pg_indexes WHERE schemaname = 'public'
	`)
	if err != nil {
		return nil, fmt.Errorf("introspect indexes: %w", err)
	}
	defer rows3.Close()
	for rows3.Next() {
		var idx string
		if err := rows3.Scan(&idx); err != nil {
			return nil, err
		}
		ls.Indexes[idx] = true
	}

	return ls, nil
}

// Plan generates the SQL statements needed to bring the database up to date
// with the FQL schema, skipping statements that are already applied.
func Plan(ctx context.Context, schema *ast.Schema, db *sql.DB) ([]string, error) {
	live, err := Introspect(ctx, db)
	if err != nil {
		return nil, err
	}

	sg := compiler.NewSQLGenerator(schema)
	desired, err := sg.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate DDL: %w", err)
	}

	var pending []string
	for _, stmt := range desired {
		if isAlreadyApplied(stmt, live) {
			continue
		}
		pending = append(pending, stmt)
	}
	return pending, nil
}

// isAlreadyApplied returns true when we can determine the statement is a no-op
// based on the live schema (table exists, column exists, index exists).
func isAlreadyApplied(stmt string, live *LiveSchema) bool {
	up := strings.ToUpper(strings.TrimSpace(stmt))

	// CREATE TABLE IF NOT EXISTS "foo" — already exists?
	if strings.HasPrefix(up, "CREATE TABLE IF NOT EXISTS") {
		tbl := extractQuotedName(stmt)
		return tbl != "" && live.Tables[tbl]
	}

	// ALTER TABLE "foo" ADD COLUMN IF NOT EXISTS "bar" ...
	if strings.Contains(up, "ADD COLUMN IF NOT EXISTS") {
		tbl := extractQuotedName(stmt)
		col := extractSecondQuotedName(stmt)
		if tbl != "" && col != "" {
			cols, ok := live.Columns[tbl]
			if ok {
				_, colExists := cols[col]
				return colExists
			}
		}
		return false
	}

	// CREATE [UNIQUE] INDEX IF NOT EXISTS "idx_name" ...
	if strings.Contains(up, "INDEX IF NOT EXISTS") {
		idx := extractQuotedName(stmt)
		return idx != "" && live.Indexes[idx]
	}

	return false
}

// extractQuotedName returns the first "double-quoted" token in the statement.
func extractQuotedName(stmt string) string {
	i := strings.Index(stmt, `"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(stmt[i+1:], `"`)
	if j < 0 {
		return ""
	}
	return stmt[i+1 : i+1+j]
}

// extractSecondQuotedName returns the second "double-quoted" token.
func extractSecondQuotedName(stmt string) string {
	i := strings.Index(stmt, `"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(stmt[i+1:], `"`)
	if j < 0 {
		return ""
	}
	rest := stmt[i+1+j+1:]
	return extractQuotedName(rest)
}

// Apply executes the pending SQL statements and records the migration in
// schema_migrations. Each statement is wrapped in its own transaction.
// Returns the number of statements applied.
func Apply(ctx context.Context, db *sql.DB, stmts []string, label string) (int, error) {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return 0, err
	}

	applied := 0
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return applied, fmt.Errorf("apply %q: %w", truncate(stmt, 80), err)
		}
		h := fmt.Sprintf("%x", sha256.Sum256([]byte(stmt)))
		db.ExecContext(ctx, // best-effort
			`INSERT INTO schema_migrations (label, statement_hash, statement, applied_at)
			 VALUES ($1, $2, $3, $4) ON CONFLICT (statement_hash) DO NOTHING`,
			label, h, stmt, time.Now(),
		)
		applied++
	}
	return applied, nil
}

// EnsureMigrationsTable creates the schema_migrations table if it doesn't exist.
func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			id             BIGSERIAL PRIMARY KEY,
			label          TEXT NOT NULL,
			statement_hash TEXT NOT NULL,
			statement      TEXT NOT NULL,
			applied_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(statement_hash)
		)
	`)
	return err
}

// History returns the list of applied migrations ordered by id.
func History(ctx context.Context, db *sql.DB) ([]map[string]interface{}, error) {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, label, statement_hash, statement, applied_at
		FROM schema_migrations ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]interface{}
	for rows.Next() {
		var id int64
		var label, hash, stmt string
		var at time.Time
		if err := rows.Scan(&id, &label, &hash, &stmt, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{
			"id": id, "label": label,
			"statement_hash": hash, "statement": stmt, "applied_at": at,
		})
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
