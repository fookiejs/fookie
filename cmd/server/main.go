package main

import (
	"flag"
	"log"

	"github.com/fookiejs/fookie/pkg/host"
)

func main() {
	schemaPath := flag.String("schema", host.DefaultSchemaPath(), "Path to .fql file or directory of .fql files (override with SCHEMA_PATH env)")
	dbURL := flag.String("db", host.DefaultDBURL(), "Database connection string")
	port := flag.String("port", ":8080", "Server listen port")
	flag.Parse()

	if err := host.RunServer(host.ServerOptions{
		SchemaPath: *schemaPath,
		DBURL:      *dbURL,
		Port:       *port,
	}); err != nil {
		log.Fatal(err)
	}
}
