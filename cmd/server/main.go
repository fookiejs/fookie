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
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/fookiejs/fookie/demo/handlers"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/events"
	fookiegql "github.com/fookiejs/fookie/pkg/graphql"
	"github.com/fookiejs/fookie/pkg/parser"
	"github.com/fookiejs/fookie/pkg/runtime"
	schemamerge "github.com/fookiejs/fookie/pkg/schema"
	"github.com/fookiejs/fookie/pkg/telemetry"
)

func defaultSchemaPath() string {
	if v := os.Getenv("SCHEMA_PATH"); v != "" {
		return v
	}
	return "demo/schema.fql"
}

func main() {
	schemaPath := flag.String("schema", defaultSchemaPath(), "Path to FSL schema file (override with SCHEMA_PATH env)")
	dbURL := flag.String("db", "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable", "Database connection string")
	port := flag.String("port", ":8080", "Server port")
	flag.Parse()

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	svcName := os.Getenv("FOOKEE_SERVICE_NAME")
	if svcName == "" {
		svcName = "fookie-server"
	}
	telemetry.InitPrometheus(svcName)
	telemetry.RegisterLokiHookIfConfigured(logger, svcName)

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
		log.Fatalf("Failed to read schema: %v", err)
	}

	lexer := parser.NewLexer(string(schemaContent))
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()
	if err != nil {
		log.Fatalf("Failed to parse schema: %v", err)
	}

	if os.Getenv("FOOKEE_DISABLE_ROOM_BUILTINS") != "true" {
		if err := schemamerge.MergeBuiltinRooms(schema); err != nil {
			log.Fatalf("merge builtin rooms: %v", err)
		}
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

	ddlCtx, ddlCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ddlCancel()

	idem := runtime.NewIdempotencyStore(db)
	if err := idem.CreateTable(ddlCtx); err != nil {
		log.Fatalf("Failed to create idempotency_keys table: %v", err)
	}

	for i, sqlStmt := range sqls {
		if _, err := db.ExecContext(ddlCtx, sqlStmt); err != nil {
			logger.Warnf("Failed to execute SQL statement %d: %v", i, err)
		}
	}

	loggerWrapper := runtime.NewLoggerWrapper(logger)
	executor := runtime.NewExecutor(db, schema, loggerWrapper)

	roomBus := events.NewRoomBus()
	executor.SetRoomBus(roomBus)

	bus := events.NewBus()
	executor.SetEventBus(bus)
	logger.Info("SSE event bus attached")

	handlers.Register(executor)
	logger.Info("Simulation handlers registered")

	proc := runtime.NewOutboxProcessor(executor)
	proc.Start(10 * time.Millisecond)
	defer proc.Stop()
	logger.Info("Outbox worker started (10ms interval)")

	seedCtx, seedCancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer seedCancel()
	if err := runtime.ExecuteSeeds(seedCtx, schema, executor); err != nil {
		log.Fatalf("Seed failed: %v", err)
	}
	logger.Infof("Seed completed (%d seed blocks)", len(schema.Seeds))

	if err := runtime.ExecuteSetups(seedCtx, schema, executor); err != nil {
		log.Fatalf("Setup failed: %v", err)
	}
	logger.Infof("Setup completed (%d setup blocks)", len(schema.Setups))

	if err := runtime.ExecuteCrons(seedCtx, schema, db); err != nil {
		log.Fatalf("Cron setup failed: %v", err)
	}
	logger.Infof("Cron setup completed (%d cron blocks)", len(schema.Crons))

	if os.Getenv("AUTO_BOOTSTRAP") == "true" {
		logger.Info("Auto-bootstrapping bank...")
		w, u, err := handlers.BootstrapBank(seedCtx, executor)
		if err != nil {
			logger.Warnf("Bootstrap failed: %v", err)
		} else {
			logger.Infof("Bootstrapped: %d wallets, %d users", w, u)
		}
	}

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			printBankState(ctx, executor)
		}
	}()
	logger.Info("Console bank state logger started (2s interval)")

	gqlSchema, err := fookiegql.BuildSchema(schema, bus, roomBus)
	if err != nil {
		log.Fatalf("GraphQL schema: %v", err)
	}
	gqlHandler := fookiegql.GraphiQLWrapper(fookiegql.NewHandler(executor, gqlSchema, idem))

	mux := http.NewServeMux()
	mux.Handle("/metrics", telemetry.MetricsHandler())

	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/demo/stats", handleDemoStats(db))
	mux.HandleFunc("/demo", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/demo/", http.StatusFound)
	})
	mux.HandleFunc("/demo/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo/" && r.URL.Path != "/demo/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		_, _ = w.Write(demoIndexHTML)
	})
	mux.Handle("/graphql", gqlHandler)

	handler := otelhttp.NewHandler(mux, "fookie.http",
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)

	logger.Infof("Banking demo server on %s — /demo/  /demo/stats  /graphql", *port)
	log.Fatal(http.ListenAndServe(*port, handler))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok"}`)
}
