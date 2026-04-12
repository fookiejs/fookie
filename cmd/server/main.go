package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/fookiejs/fookie/pkg/parser"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/sirupsen/logrus"
)

var (
	schemaPath = flag.String("schema", "schemas/main.fql", "Path to FSL schema file")
	dbURL      = flag.String("db", "postgres://user:password@localhost/fookie", "Database connection string")
	port       = flag.String("port", ":8080", "Server port")
)

type Server struct {
	db       *sql.DB
	executor *runtime.Executor
	logger   *logrus.Logger
}

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

	srv := &Server{
		db:       db,
		executor: executor,
		logger:   logger,
	}

	http.HandleFunc("/health", srv.handleHealth)
	http.HandleFunc("/operations", srv.handleOperations)
	http.HandleFunc("/models", srv.handleModels)

	logger.Infof("Starting Fookie server on %s", *port)
	log.Fatal(http.ListenAndServe(*port, nil))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok"}`)
}

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"operations":[]}`)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"models":[]}`)
}
