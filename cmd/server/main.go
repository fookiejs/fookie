package main

import (
	"context"
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"

	"github.com/fookiejs/fookie/pkg/compiler"
	fookiegql "github.com/fookiejs/fookie/pkg/graphql"
	"github.com/fookiejs/fookie/pkg/parser"
	"github.com/fookiejs/fookie/pkg/runtime"
)

//go:embed all:demo
var demoRoot embed.FS

var (
	schemaPath = flag.String("schema", "schemas/wallet_transfer.fql", "Path to FSL schema file")
	dbURL      = flag.String("db", "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable", "Database connection string")
	port       = flag.String("port", ":8080", "Server port")
)

func main() {
	flag.Parse()

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	schemaContent, err := os.ReadFile(*schemaPath)
	if err != nil {
		log.Fatalf("Failed to read schema: %v", err)
	}

	lexer := parser.NewLexer(string(schemaContent))
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()
	if err != nil {
		log.Fatalf("Failed to parse schema: %v", err)
	}

	logger.Infof("Parsed schema with %d models, %d externals, %d modules",
		len(schema.Models), len(schema.Externals), len(schema.Modules))

	sqlGen := compiler.NewSQLGenerator(schema)
	sqls, err := sqlGen.Generate()
	if err != nil {
		log.Fatalf("Failed to generate SQL: %v", err)
	}

	logger.Infof("Generated %d SQL statements", len(sqls))

	db, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i, sqlStmt := range sqls {
		if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
			logger.Warnf("Failed to execute SQL statement %d: %v", i, err)
		}
	}

	loggerWrapper := runtime.NewLoggerWrapper(logger)
	executor := runtime.NewExecutor(db, schema, loggerWrapper)

	gqlSchema, err := fookiegql.BuildSchema(schema)
	if err != nil {
		log.Fatalf("Failed to build GraphQL schema: %v", err)
	}

	logger.Info("GraphQL schema built successfully")

	http.HandleFunc("/health", handleHealth)
	http.Handle("/graphql", fookiegql.NewHandler(executor, gqlSchema))

	demoFS, err := fs.Sub(demoRoot, "demo")
	if err != nil {
		log.Fatalf("demo static files: %v", err)
	}
	http.HandleFunc("/demo", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/demo/", http.StatusSeeOther)
	})
	http.Handle("/demo/", http.StripPrefix("/demo/", http.FileServer(http.FS(demoFS))))

	logger.Infof("Starting Fookie server on %s", *port)
	logger.Infof("Demo UI: http://127.0.0.1%s/demo/", *port)
	log.Fatal(http.ListenAndServe(*port, nil))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok"}`)
}
