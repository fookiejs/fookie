package host

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/events"
	fookiegql "github.com/fookiejs/fookie/pkg/graphql"
	"github.com/fookiejs/fookie/pkg/runtime"
	schemapkg "github.com/fookiejs/fookie/pkg/schema"
	"github.com/fookiejs/fookie/pkg/telemetry"
)

type RegisterHandlersFunc func(*runtime.Executor) error

type ServerOptions struct {
	SchemaPath       string
	DBURL            string
	Port             string
	ServiceName      string
	RegisterHandlers RegisterHandlersFunc
}

type WorkerOptions struct {
	SchemaPath       string
	DBURL            string
	PollInterval     time.Duration
	MetricsListen    string
	ServiceName      string
	RegisterHandlers RegisterHandlersFunc
}

func DefaultSchemaPath() string {
	if v := os.Getenv("SCHEMA_PATH"); v != "" {
		return v
	}
	return "schema.fql"
}

func DefaultDBURL() string {
	if v := os.Getenv("DB_URL"); v != "" {
		return v
	}
	return "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"
}

func RunServer(opts ServerOptions) error {
	if opts.SchemaPath == "" {
		opts.SchemaPath = DefaultSchemaPath()
	}
	if opts.DBURL == "" {
		opts.DBURL = DefaultDBURL()
	}
	if opts.Port == "" {
		opts.Port = ":8080"
	}
	if opts.ServiceName == "" {
		opts.ServiceName = "fookie-server"
	}

	logger := newLogger(opts.ServiceName)
	shutdownTracer := initTracing(context.Background(), logger, opts.ServiceName)
	if shutdownTracer != nil {
		defer shutdownTracer()
	}

	schema, sqls, db, executor, err := prepareRuntime(opts.SchemaPath, opts.DBURL, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	idem := runtime.NewIdempotencyStore(db)
	ddlCtx, ddlCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ddlCancel()
	if err := idem.CreateTable(ddlCtx); err != nil {
		return fmt.Errorf("create idempotency_keys table: %w", err)
	}
	if err := execDDL(ddlCtx, db, sqls, logger); err != nil {
		return err
	}

	if err := RegisterHandlers(executor, opts.RegisterHandlers); err != nil {
		return err
	}

	rdb := connectRedis(logger)
	roomBus := buildRoomBus(rdb, logger)
	executor.SetRoomBus(roomBus)
	bus := events.NewBus()
	executor.SetEventBus(bus)

	proc := buildOutboxProcessor(executor, rdb, logger, 10*time.Millisecond)
	defer proc.Stop()

	initCtx, initCancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer initCancel()
	if err := runtime.ExecuteSeeds(initCtx, schema, executor); err != nil {
		return fmt.Errorf("seeds: %w", err)
	}
	logger.Infof("Seeds done (%d blocks)", len(schema.Seeds))
	if err := runtime.ExecuteSetups(initCtx, schema, executor); err != nil {
		return fmt.Errorf("setups: %w", err)
	}
	logger.Infof("Setups done (%d blocks)", len(schema.Setups))
	if err := runtime.ExecuteCrons(initCtx, schema, db); err != nil {
		return fmt.Errorf("crons: %w", err)
	}
	logger.Infof("Crons done (%d blocks)", len(schema.Crons))

	gqlSchema, err := fookiegql.BuildSchema(schema, bus, roomBus)
	if err != nil {
		return fmt.Errorf("GraphQL schema: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", telemetry.MetricsHandler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.Handle("/graphql", fookiegql.GraphiQLWrapper(fookiegql.NewHandler(executor, gqlSchema, idem)))
	mux.Handle("/graphql/ws", fookiegql.NewWSHandler(executor, gqlSchema))

	handler := otelhttp.NewHandler(mux, "fookie.http",
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)

	logger.Infof("Fookie server on %s -> /graphql (HTTP) /graphql/ws (WebSocket) /health", opts.Port)
	return http.ListenAndServe(opts.Port, handler)
}

func RunWorker(opts WorkerOptions) error {
	if opts.SchemaPath == "" {
		opts.SchemaPath = DefaultSchemaPath()
	}
	if opts.DBURL == "" {
		opts.DBURL = DefaultDBURL()
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 25 * time.Millisecond
	}
	if opts.MetricsListen == "" {
		opts.MetricsListen = os.Getenv("METRICS_LISTEN")
	}
	if opts.ServiceName == "" {
		opts.ServiceName = "fookie-worker"
	}

	logger := newLogger(opts.ServiceName)
	if opts.MetricsListen != "" {
		startMetricsServer(opts.MetricsListen, logger)
	}
	shutdownTracer := initTracing(context.Background(), logger, opts.ServiceName)
	if shutdownTracer != nil {
		defer shutdownTracer()
	}

	_, sqls, db, executor, err := prepareRuntime(opts.SchemaPath, opts.DBURL, logger)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	ddlCtx, ddlCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ddlCancel()
	if err := execDDL(ddlCtx, db, sqls, logger); err != nil {
		return err
	}

	if err := RegisterHandlers(executor, opts.RegisterHandlers); err != nil {
		return err
	}

	rdb := connectRedis(logger)
	processor := buildOutboxProcessor(executor, rdb, logger, opts.PollInterval)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	logger.Info("Shutting down worker...")
	processor.Stop()
	logger.Info("Worker shutdown complete")
	return nil
}

func prepareRuntime(schemaPath, dbURL string, logger *logrus.Logger) (*ast.Schema, []string, *sql.DB, *runtime.Executor, error) {
	schema, err := schemapkg.LoadSchema(schemaPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load schema: %w", err)
	}
	if os.Getenv("FOOKEE_DISABLE_ROOM_BUILTINS") != "true" {
		if err := schemapkg.MergeBuiltinRooms(schema); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("merge builtin rooms: %w", err)
		}
	}
	logger.Infof("Schema loaded: %d models, %d externals, %d modules", len(schema.Models), len(schema.Externals), len(schema.Modules))

	sqlGen := compiler.NewSQLGenerator(schema)
	sqls, err := sqlGen.Generate()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generate SQL: %w", err)
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open db: %w", err)
	}
	executor := runtime.NewExecutor(db, schema, runtime.NewLoggerWrapper(logger))
	return schema, sqls, db, executor, nil
}

func RegisterHandlers(exec *runtime.Executor, fn RegisterHandlersFunc) error {
	if fn == nil {
		return nil
	}
	if err := fn(exec); err != nil {
		return fmt.Errorf("register handlers: %w", err)
	}
	return nil
}

func execDDL(ctx context.Context, db *sql.DB, sqls []string, logger *logrus.Logger) error {
	for i, sqlStmt := range sqls {
		if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
			logger.Warnf("DDL %d: %v", i, err)
		}
	}
	return nil
}

func newLogger(serviceName string) *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	telemetry.InitPrometheus(serviceName)
	telemetry.RegisterLokiHookIfConfigured(logger, serviceName)
	return logger
}

func initTracing(ctx context.Context, logger *logrus.Logger, serviceName string) func() {
	shutdownTracer, err := telemetry.InitTracer(ctx, serviceName)
	if err != nil {
		logger.Warnf("OTel tracer init failed (traces disabled): %v", err)
		return nil
	}
	logger.Info("OpenTelemetry tracer initialised")
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracer(shutdownCtx); err != nil {
			logger.Warnf("OTel tracer shutdown error: %v", err)
		}
	}
}

func connectRedis(logger *logrus.Logger) *redis.Client {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		return nil
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Warnf("Invalid REDIS_URL, running without Redis: %v", err)
		return nil
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Warnf("Redis ping failed, running without Redis: %v", err)
		return nil
	}
	logger.Infof("Redis connected: %s", redisURL)
	return rdb
}

func buildRoomBus(rdb *redis.Client, logger *logrus.Logger) *events.RoomBus {
	if rdb != nil {
		roomBus := events.NewRoomBusWithRedis(rdb)
		go roomBus.StartRedisSubscriber(context.Background())
		logger.Info("RoomBus: Redis pub/sub mode (multi-server notify enabled)")
		return roomBus
	}
	logger.Info("RoomBus: local-only mode (single server)")
	return events.NewRoomBus()
}

func buildOutboxProcessor(exec *runtime.Executor, rdb *redis.Client, logger *logrus.Logger, interval time.Duration) *runtime.OutboxProcessor {
	var proc *runtime.OutboxProcessor
	if rdb != nil {
		proc = runtime.NewOutboxProcessorWithRedis(exec, rdb)
		exec.SetOutboxNotify(func(id string) { proc.NotifyNewOutboxItem(id) })
		logger.Info("Outbox: Redis BLPOP mode (instant wake-up)")
	} else {
		proc = runtime.NewOutboxProcessor(exec)
		logger.Infof("Outbox: poll mode (%v interval)", interval)
	}
	proc.Start(interval)
	return proc
}

func startMetricsServer(addr string, logger *logrus.Logger) {
	go func() {
		m := http.NewServeMux()
		m.Handle("/metrics", telemetry.MetricsHandler())
		srv := &http.Server{Addr: addr, Handler: m}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warnf("metrics listen %s: %v", addr, err)
		}
	}()
	logger.Infof("Prometheus metrics on %s/metrics", addr)
}
