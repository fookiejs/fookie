package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/sirupsen/logrus"
)

var (
	dbURL        = flag.String("db", "postgres://user:password@localhost/fookie", "Database connection string")
	pollInterval = flag.Duration("poll-interval", 5*time.Second, "Poll interval for outbox")
)

func main() {
	flag.Parse()

	// Setup logger
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Connect to database
	db, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Test connection
	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	logger.Info("Connected to database")

	// Create external manager
	externalMgr := runtime.NewExternalManager()

	// Create outbox processor
	processor := runtime.NewOutboxProcessor(externalMgr)
	processor.Start(*pollInterval)

	logger.Infof("Started outbox processor with %v poll interval", *pollInterval)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	logger.Info("Shutting down worker...")

	processor.Stop()

	// Give in-flight operations time to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	<-ctx.Done()
	logger.Info("Worker shutdown complete")
}

// ProcessOutboxJobs continuously processes pending outbox items
func ProcessOutboxJobs(ctx context.Context, db *sql.DB, manager *runtime.ExternalManager, logger *logrus.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Query for pending jobs
		rows, err := db.QueryContext(ctx, `
			SELECT id, entity_type, entity_id, external_name, payload
			FROM outbox
			WHERE status = 'pending' AND retry_count < 3
			ORDER BY created_at ASC
			LIMIT 10
		`)
		if err != nil {
			logger.Warnf("Failed to query outbox: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		processed := 0
		for rows.Next() {
			var (
				id           string
				entityType   string
				entityID     string
				externalName string
				payload      string
			)

			if err := rows.Scan(&id, &entityType, &entityID, &externalName, &payload); err != nil {
				logger.Warnf("Failed to scan row: %v", err)
				continue
			}

			// Execute external call
			// TODO: Parse payload and call external service
			logger.Infof("Processing outbox job: %s (external: %s)", id, externalName)

			// Mark as processed
			_, err = db.ExecContext(ctx, `
				UPDATE outbox
				SET status = 'processed', processed_at = NOW()
				WHERE id = $1
			`, id)

			if err != nil {
				logger.Warnf("Failed to update outbox status: %v", err)
			}

			processed++
		}

		rows.Close()

		if processed == 0 {
			time.Sleep(5 * time.Second)
		}
	}
}
