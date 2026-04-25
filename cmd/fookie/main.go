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

const usage = `Fookie CLI

Usage:
  fookie migrate plan    [--schema <file>] [--db <url>]
  fookie migrate apply   [--schema <file>] [--db <url>] [--label <name>]
  fookie migrate history [--db <url>]
  fookie serve           [--schema <file>] [--db <url>] [--port <:8080>]
  fookie init <dir>
  fookie doctor
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "migrate":
		cmdMigrate(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "doctor":
		cmdDoctor()
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

// ── migrate ──────────────────────────────────────────────────────────────────

func cmdMigrate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "migrate needs a subcommand: plan | apply | history")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	schemaPath := fs.String("schema", envOr("SCHEMA_PATH", "demo/schema.fql"), "Path to .fql schema file or directory")
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
    email string
  }

  @@unique([email], where: "deleted_at IS NULL")

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
