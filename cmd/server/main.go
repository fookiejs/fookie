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

	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/events"
	fookiegql "github.com/fookiejs/fookie/pkg/graphql"
	"github.com/fookiejs/fookie/pkg/runtime"
	schemapkg "github.com/fookiejs/fookie/pkg/schema"
	"github.com/fookiejs/fookie/pkg/telemetry"
	"github.com/redis/go-redis/v9"
)

func defaultSchemaPath() string {
	if v := os.Getenv("SCHEMA_PATH"); v != "" {
		return v
	}
	return "demo/schema.fql"
}

func main() {
	schemaPath := flag.String("schema", defaultSchemaPath(), "Path to .fql file or directory of .fql files (override with SCHEMA_PATH env)")
	dbURL := flag.String("db", "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable", "Database connection string")
	port := flag.String("port", ":8080", "Server listen port")
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

	// Load schema — supports single file or directory of .fql files
	schema, err := schemapkg.LoadSchema(*schemaPath)
	if err != nil {
		log.Fatalf("load schema: %v", err)
	}

	if os.Getenv("FOOKEE_DISABLE_ROOM_BUILTINS") != "true" {
		if err := schemapkg.MergeBuiltinRooms(schema); err != nil {
			log.Fatalf("merge builtin rooms: %v", err)
		}
	}

	logger.Infof("Schema loaded: %d models, %d externals, %d modules",
		len(schema.Models), len(schema.Externals), len(schema.Modules))

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

	ddlCtx, ddlCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ddlCancel()

	idem := runtime.NewIdempotencyStore(db)
	if err := idem.CreateTable(ddlCtx); err != nil {
		log.Fatalf("create idempotency_keys table: %v", err)
	}

	for i, sqlStmt := range sqls {
		if _, err := db.ExecContext(ddlCtx, sqlStmt); err != nil {
			logger.Warnf("DDL %d: %v", i, err)
		}
	}

	loggerWrapper := runtime.NewLoggerWrapper(logger)
	executor := runtime.NewExecutor(db, schema, loggerWrapper)

	// Register demo external handlers (GrowUserbase, RunTransferBatch, RunAtmActivity).
	// These are no-ops if the schema doesn't use them.
	registerDemoHandlers(executor)

	// Redis — optional; enables multi-server pub/sub notify + instant outbox
	var rdb *redis.Client
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			logger.Warnf("Invalid REDIS_URL, running without Redis: %v", err)
		} else {
			rdb = redis.NewClient(opts)
			if err := rdb.Ping(context.Background()).Err(); err != nil {
				logger.Warnf("Redis ping failed, running without Redis: %v", err)
				rdb = nil
			} else {
				logger.Infof("Redis connected: %s", redisURL)
			}
		}
	}

	var roomBus *events.RoomBus
	if rdb != nil {
		roomBus = events.NewRoomBusWithRedis(rdb)
		go roomBus.StartRedisSubscriber(context.Background())
		logger.Info("RoomBus: Redis pub/sub mode (multi-server notify enabled)")
	} else {
		roomBus = events.NewRoomBus()
		logger.Info("RoomBus: local-only mode (single server)")
	}
	executor.SetRoomBus(roomBus)

	bus := events.NewBus()
	executor.SetEventBus(bus)

	var proc *runtime.OutboxProcessor
	if rdb != nil {
		proc = runtime.NewOutboxProcessorWithRedis(executor, rdb)
		executor.SetOutboxNotify(func(id string) { proc.NotifyNewOutboxItem(id) })
		logger.Info("Outbox: Redis BLPOP mode (instant wake-up)")
	} else {
		proc = runtime.NewOutboxProcessor(executor)
		logger.Info("Outbox: poll mode (10ms interval)")
	}
	proc.Start(10 * time.Millisecond)
	defer proc.Stop()

	// Schema-driven init: seeds, setups, crons
	initCtx, initCancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer initCancel()

	if err := runtime.ExecuteSeeds(initCtx, schema, executor); err != nil {
		log.Fatalf("seeds: %v", err)
	}
	logger.Infof("Seeds done (%d blocks)", len(schema.Seeds))

	if err := runtime.ExecuteSetups(initCtx, schema, executor); err != nil {
		log.Fatalf("setups: %v", err)
	}
	logger.Infof("Setups done (%d blocks)", len(schema.Setups))

	if err := runtime.ExecuteCrons(initCtx, schema, db); err != nil {
		log.Fatalf("crons: %v", err)
	}
	logger.Infof("Crons done (%d blocks)", len(schema.Crons))

	// Build GraphQL schema
	gqlSchema, err := fookiegql.BuildSchema(schema, bus, roomBus)
	if err != nil {
		log.Fatalf("GraphQL schema: %v", err)
	}
	gqlHandler := fookiegql.GraphiQLWrapper(fookiegql.NewHandler(executor, gqlSchema, idem))
	wsHandler := fookiegql.NewWSHandler(executor, gqlSchema)

	mux := http.NewServeMux()
	mux.Handle("/metrics", telemetry.MetricsHandler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.Handle("/graphql", gqlHandler)
	mux.Handle("/graphql/ws", wsHandler) // graphql-transport-ws protocol

	handler := otelhttp.NewHandler(mux, "fookie.http",
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)

	logger.Infof("Fookie server on %s  →  /graphql (HTTP)  /graphql/ws (WebSocket)  /health", *port)
	log.Fatal(http.ListenAndServe(*port, handler))
}
