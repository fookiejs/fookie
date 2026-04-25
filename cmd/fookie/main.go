package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/migrate"
	"github.com/fookiejs/fookie/pkg/parser"
	schemapkg "github.com/fookiejs/fookie/pkg/schema"
)

const cliVersion = "0.1.0"

const usage = `Fookie CLI v` + cliVersion + `

Usage:
  fookie init <dir>                    scaffold schema.fql + .env
  fookie doctor                        check required tools
  fookie serve      [--schema] [--db] [--port]   start fookie-server
  fookie migrate plan    [--schema] [--db]        show pending DDL
  fookie migrate apply   [--schema] [--db] [--label]  apply DDL
  fookie migrate history [--db]                   show applied migrations
  fookie dlq list        [--db] [--limit N]       list failed jobs
  fookie dlq retry <id>  [--db]                   re-queue one job
  fookie dlq retry-all   [--db]                   re-queue all failed jobs
  fookie dlq purge       [--db] [--before date]   delete old failures
  fookie helm <args>                   passthrough to helm

Common flags:
  --schema <path>   path to .fql file or directory (env: SCHEMA_PATH)
  --db <url>        PostgreSQL connection string   (env: DB_URL)

See docs/getting-started.md for a full walkthrough.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "dlq":
		cmdDLQ(os.Args[2:])
	case "migrate":
		cmdMigrate(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "doctor":
		cmdDoctor()
	case "version", "--version", "-v":
		fmt.Printf("fookie v%s\n", cliVersion)
	case "help", "--help", "-h":
		fmt.Print(usage)
	// Legacy docker/helm wrappers kept for backward compat
	case "helm":
		root, _ := findRepoRoot()
		hargs := os.Args[2:]
		if len(hargs) == 0 {
			hargs = []string{"template", "fookie", filepath.Join("charts", "fookie")}
		}
		run(root, "helm", hargs...)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// ── dlq ──────────────────────────────────────────────────────────────────────

func cmdDLQ(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "dlq needs a subcommand: list | retry <id> | retry-all | purge")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("dlq", flag.ExitOnError)
	dbURL := fs.String("db", envOr("DB_URL", "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"), "PostgreSQL connection string")
	limit := fs.Int("limit", 50, "Max rows to list")
	beforeStr := fs.String("before", "", "Purge items created before this date (YYYY-MM-DD)")

	sub := args[0]
	fs.Parse(args[1:])

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := openDB(*dbURL)
	defer db.Close()

	switch sub {
	case "list":
		rows, err := db.QueryContext(ctx,
			`SELECT id, external_name, entity_type, entity_id, retry_count, error_message, created_at
			 FROM outbox WHERE status='failed'
			 ORDER BY created_at DESC LIMIT $1`, *limit)
		if err != nil {
			fatal(err)
		}
		defer rows.Close()
		fmt.Printf("%-36s  %-20s  %-5s  %s\n", "ID", "EXTERNAL", "RETRY", "ERROR (truncated)")
		fmt.Println(strings.Repeat("-", 90))
		n := 0
		for rows.Next() {
			var id, extName, eType string
			var eID sql.NullString
			var retryCount int
			var errMsg sql.NullString
			var createdAt time.Time
			if err := rows.Scan(&id, &extName, &eType, &eID, &retryCount, &errMsg, &createdAt); err != nil {
				fatal(err)
			}
			msg := ""
			if errMsg.Valid {
				msg = errMsg.String
				if len(msg) > 30 {
					msg = msg[:30] + "…"
				}
			}
			fmt.Printf("%-36s  %-20s  %-5d  %s\n", id, extName, retryCount, msg)
			n++
		}
		if n == 0 {
			fmt.Println("No failed items in the dead-letter queue.")
		}

	case "retry":
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "usage: fookie dlq retry <id>")
			os.Exit(2)
		}
		id := fs.Arg(0)
		res, err := db.ExecContext(ctx,
			`UPDATE outbox SET status='pending', retry_count=0, error_message=NULL, run_after=NULL
			 WHERE id=$1 AND status='failed'`, id)
		if err != nil {
			fatal(err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fmt.Fprintf(os.Stderr, "item %s not found or not in failed status\n", id)
			os.Exit(1)
		}
		fmt.Printf("✓ Re-queued %s\n", id)

	case "retry-all":
		res, err := db.ExecContext(ctx,
			`UPDATE outbox SET status='pending', retry_count=0, error_message=NULL, run_after=NULL
			 WHERE status='failed'`)
		if err != nil {
			fatal(err)
		}
		n, _ := res.RowsAffected()
		fmt.Printf("✓ Re-queued %d failed item(s)\n", n)

	case "purge":
		var before time.Time
		if *beforeStr != "" {
			var err error
			before, err = time.Parse("2006-01-02", *beforeStr)
			if err != nil {
				fatal(fmt.Errorf("invalid --before date %q (want YYYY-MM-DD): %w", *beforeStr, err))
			}
		} else {
			before = time.Now().AddDate(0, 0, -30) // default: 30 days ago
		}
		res, err := db.ExecContext(ctx,
			`DELETE FROM outbox WHERE status='failed' AND created_at < $1`, before)
		if err != nil {
			fatal(err)
		}
		n, _ := res.RowsAffected()
		fmt.Printf("✓ Purged %d failed item(s) older than %s\n", n, before.Format("2006-01-02"))

	default:
		fmt.Fprintf(os.Stderr, "unknown dlq subcommand: %s\n", sub)
		os.Exit(2)
	}
}

// ── migrate ──────────────────────────────────────────────────────────────────

func cmdMigrate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "migrate needs a subcommand: plan | apply | history")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	schemaPath := fs.String("schema", envOr("SCHEMA_PATH", "schema.fql"), "Path to .fql schema file or directory")
	dbURL := fs.String("db", envOr("DB_URL", "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"), "PostgreSQL connection string")
	label := fs.String("label", "manual-"+time.Now().Format("20060102-150405"), "Migration label (apply only)")

	sub := args[0]
	fs.Parse(args[1:])

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	switch sub {
	case "plan":
		schema, err := loadSchema(*schemaPath)
		if err != nil {
			fatal(err)
		}
		db := openDB(*dbURL)
		defer db.Close()
		stmts, err := migrate.Plan(ctx, schema, db)
		if err != nil {
			fatal(err)
		}
		if len(stmts) == 0 {
			fmt.Println("✓ Schema is up to date — nothing to migrate.")
			return
		}
		fmt.Printf("-- %d pending statement(s) --\n", len(stmts))
		for _, s := range stmts {
			fmt.Println(s)
			fmt.Println(";")
		}

	case "apply":
		schema, err := loadSchema(*schemaPath)
		if err != nil {
			fatal(err)
		}
		db := openDB(*dbURL)
		defer db.Close()
		stmts, err := migrate.Plan(ctx, schema, db)
		if err != nil {
			fatal(err)
		}
		if len(stmts) == 0 {
			fmt.Println("✓ Schema is up to date — nothing to apply.")
			return
		}
		n, err := migrate.Apply(ctx, db, stmts, *label)
		if err != nil {
			fmt.Fprintf(os.Stderr, "apply failed after %d statements: %v\n", n, err)
			os.Exit(1)
		}
		fmt.Printf("✓ Applied %d statement(s) (label: %s)\n", n, *label)

	case "history":
		db := openDB(*dbURL)
		defer db.Close()
		rows, err := migrate.History(ctx, db)
		if err != nil {
			fatal(err)
		}
		if len(rows) == 0 {
			fmt.Println("No migrations recorded yet.")
			return
		}
		fmt.Printf("%-5s  %-24s  %-8s  %s\n", "ID", "APPLIED AT", "LABEL", "STATEMENT (truncated)")
		fmt.Println(strings.Repeat("-", 80))
		for _, r := range rows {
			stmt := fmt.Sprintf("%v", r["statement"])
			if len(stmt) > 40 {
				stmt = stmt[:40] + "…"
			}
			fmt.Printf("%-5v  %-24v  %-8v  %s\n", r["id"], r["applied_at"], r["label"], stmt)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown migrate subcommand: %s\n", sub)
		os.Exit(2)
	}
}

// ── serve ────────────────────────────────────────────────────────────────────

func cmdServe(args []string) {
	// Delegates to cmd/server binary if available; otherwise prints guidance.
	serverBin, err := exec.LookPath("fookie-server")
	if err != nil {
		// Try to find it relative to this binary
		self, _ := os.Executable()
		candidate := filepath.Join(filepath.Dir(self), "fookie-server")
		if _, e := os.Stat(candidate); e == nil {
			serverBin = candidate
		}
	}
	if serverBin == "" {
		fmt.Fprintln(os.Stderr, "fookie-server not found in PATH. Build it with: go build ./cmd/server")
		os.Exit(1)
	}
	cmd := exec.Command(serverBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

// ── init ─────────────────────────────────────────────────────────────────────

func cmdInit(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal(err)
	}
	schemaFile := filepath.Join(dir, "schema.fql")
	if _, err := os.Stat(schemaFile); err == nil {
		fmt.Printf("schema.fql already exists in %s — skipping.\n", dir)
	} else {
		if err := os.WriteFile(schemaFile, []byte(initSchemaTemplate), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("Created %s\n", schemaFile)
	}

	envFile := filepath.Join(dir, ".env")
	if _, err := os.Stat(envFile); err == nil {
		fmt.Printf(".env already exists in %s — skipping.\n", dir)
	} else {
		if err := os.WriteFile(envFile, []byte(initEnvTemplate), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("Created %s\n", envFile)
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit schema.fql to define your models")
	fmt.Printf("  2. fookie migrate apply --schema %s/schema.fql\n", dir)
	fmt.Printf("  3. fookie serve --schema %s/schema.fql\n", dir)
}

// ── doctor ───────────────────────────────────────────────────────────────────

func cmdDoctor() {
	checks := []struct {
		name string
		fn   func() (string, bool)
	}{
		{"docker", func() (string, bool) {
			_, err := exec.LookPath("docker")
			if err != nil {
				return "not found in PATH", false
			}
			out, _ := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output()
			return "v" + strings.TrimSpace(string(out)), true
		}},
		{"go", func() (string, bool) {
			out, err := exec.Command("go", "version").Output()
			if err != nil {
				return "not found in PATH", false
			}
			return strings.TrimSpace(string(out)), true
		}},
		{"helm (optional)", func() (string, bool) {
			out, err := exec.Command("helm", "version", "--short").Output()
			if err != nil {
				return "not found (optional)", true // not a hard requirement
			}
			return strings.TrimSpace(string(out)), true
		}},
		{"fookie-server", func() (string, bool) {
			if _, err := exec.LookPath("fookie-server"); err == nil {
				return "found in PATH", true
			}
			self, _ := os.Executable()
			candidate := filepath.Join(filepath.Dir(self), "fookie-server")
			if _, e := os.Stat(candidate); e == nil {
				return "found next to fookie binary", true
			}
			return "not found — run: go build ./cmd/server -o fookie-server", false
		}},
	}

	ok := true
	for _, c := range checks {
		detail, pass := c.fn()
		mark := "✓"
		if !pass {
			mark = "✗"
			ok = false
		}
		fmt.Printf("[%s] %-20s %s\n", mark, c.name, detail)
	}
	if !ok {
		os.Exit(1)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func loadSchema(path string) (*ast.Schema, error) {
	return schemapkg.LoadSchema(path)
}

func openDB(url string) *sql.DB {
	db, err := sql.Open("postgres", url)
	if err != nil {
		fatal(fmt.Errorf("open db: %w", err))
	}
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(30 * time.Second)
	return db
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		dir = parent
	}
}

func run(dir, name string, arg ...string) {
	cmd := exec.Command(name, arg...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

// ── templates ────────────────────────────────────────────────────────────────

var initSchemaTemplate = `// Generated by fookie init
// Edit this file to define your data models.

model User {
  fields {
    name  string
    email string --unique
  }

  create {}
  read   {}
  update {}
  delete {}
}
`

var initEnvTemplate = `# Fookie environment — fill in your values
DB_URL=postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable
REDIS_URL=redis://localhost:6379
SCHEMA_PATH=./schema.fql
PORT=:8080
`

// Ensure parser is referenced (import side-effect via schemapkg).
var _ = parser.NewLexer
