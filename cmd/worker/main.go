package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/parser"
	"github.com/fookiejs/fookie/pkg/runtime"
	schemamerge "github.com/fookiejs/fookie/pkg/schema"
	"github.com/fookiejs/fookie/pkg/telemetry"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

func defaultSchemaPath() string {
	if v := os.Getenv("SCHEMA_PATH"); v != "" {
		return v
	}
	return "demo/schema.fql"
}

func main() {
	defaultDB := os.Getenv("DB_URL")
	if defaultDB == "" {
		defaultDB = "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"
	}
	dbURL := flag.String("db", defaultDB, "Database connection string (default from DB_URL if set)")
	schemaPath := flag.String("schema", defaultSchemaPath(), "Path to FSL schema (override with SCHEMA_PATH)")
	pollInterval := flag.Duration("poll-interval", 25*time.Millisecond, "Poll interval for outbox")
	flag.Parse()

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	svcName := os.Getenv("FOOKEE_SERVICE_NAME")
	if svcName == "" {
		svcName = "fookie-worker"
	}
	telemetry.InitPrometheus(svcName)
	telemetry.RegisterLokiHookIfConfigured(logger, svcName)

	if ml := os.Getenv("METRICS_LISTEN"); ml != "" {
		go func(addr string) {
			m := http.NewServeMux()
			m.Handle("/metrics", telemetry.MetricsHandler())
			srv := &http.Server{Addr: addr, Handler: m}
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("metrics listen %s: %v", addr, err)
			}
		}(ml)
		logger.Infof("Prometheus metrics on %s/metrics", ml)
	}

	ctx := context.Background()
	shutdownTracer, err := telemetry.InitTracer(ctx, svcName)
	if err != nil {
		logger.Warnf("OTel tracer init failed (traces disabled): %v", err)
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracer(shutdownCtx); err != nil {
				logger.Warnf("OTel tracer shutdown error: %v", err)
			}
		}()
		logger.Info("OpenTelemetry tracer initialised")
	}

	schemaContent, err := os.ReadFile(*schemaPath)
	if err != nil {
		log.Fatalf("read schema: %v", err)
	}
	lexer := parser.NewLexer(string(schemaContent))
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()
	if err != nil {
		log.Fatalf("parse schema: %v", err)
	}

	if os.Getenv("FOOKEE_DISABLE_ROOM_BUILTINS") != "true" {
		if err := schemamerge.MergeBuiltinRooms(schema); err != nil {
			log.Fatalf("merge builtin rooms: %v", err)
		}
	}

	sqlGen := compiler.NewSQLGenerator(schema)
	sqls, err := sqlGen.Generate()
	if err != nil {
		log.Fatalf("generate SQL: %v", err)
	}

	db, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	ddlCtx, ddlCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ddlCancel()
	for i, sqlStmt := range sqls {
		if _, err := db.ExecContext(ddlCtx, sqlStmt); err != nil {
			logger.Warnf("DDL statement %d: %v", i, err)
		}
	}

	loggerWrapper := runtime.NewLoggerWrapper(logger)
	executor := runtime.NewExecutor(db, schema, loggerWrapper)
	processor := runtime.NewOutboxProcessor(executor)
	processor.Start(*pollInterval)

	logger.Infof("Outbox worker started (poll %v, schema %s)", *pollInterval, *schemaPath)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	logger.Info("Shutting down worker...")
	processor.Stop()
	logger.Info("Worker shutdown complete")
}
