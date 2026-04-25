package main

import (
	"flag"
	"log"
	"time"

	"github.com/fookiejs/fookie/pkg/host"
)

func main() {
	dbURL := flag.String("db", host.DefaultDBURL(), "Database connection string (default from DB_URL if set)")
	schemaPath := flag.String("schema", host.DefaultSchemaPath(), "Path to FQL schema (override with SCHEMA_PATH)")
	pollInterval := flag.Duration("poll-interval", 25*time.Millisecond, "Poll interval for outbox")
	flag.Parse()

	if err := host.RunWorker(host.WorkerOptions{
		SchemaPath:   *schemaPath,
		DBURL:        *dbURL,
		PollInterval: *pollInterval,
	}); err != nil {
		log.Fatal(err)
	}
}
