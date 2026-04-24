package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/fookiejs/fookie/pkg/parser"
)

var (
	schemaFile = flag.String("schema", "", "FSL schema file to parse")
	outputSQL  = flag.Bool("sql", false, "Generate SQL from schema")
	validate   = flag.Bool("validate", false, "Validate schema")
)

func main() {
	flag.Parse()

	if *schemaFile == "" {
		fmt.Println("Usage: parser -schema <file> [-sql] [-validate]")
		os.Exit(1)
	}

	content, err := ioutil.ReadFile(*schemaFile)
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	lexer := parser.NewLexer(string(content))
	tokens := lexer.Tokenize()

	fmt.Printf("Lexed %d tokens\n", len(tokens))

	p := parser.NewParser(tokens)
	schema, err := p.Parse()
	if err != nil {
		log.Fatalf("Parse error: %v", err)
	}

	fmt.Printf("\nParsed:\n")
	fmt.Printf("  Models: %d\n", len(schema.Models))
	for _, m := range schema.Models {
		fmt.Printf("    - %s (fields: %d, operations: %d)\n", m.Name, len(m.Fields), len(m.CRUD))
		for op := range m.CRUD {
			fmt.Printf("      - %s\n", op)
		}
	}

	fmt.Printf("  Externals: %d\n", len(schema.Externals))
	for _, e := range schema.Externals {
		fmt.Printf("    - %s (body: %d, output: %d)\n", e.Name, len(e.Body), len(e.Output))
	}

	fmt.Printf("  Modules: %d\n", len(schema.Modules))
	for _, m := range schema.Modules {
		fmt.Printf("    - %s\n", m.Name)
	}

	if *outputSQL {
		fmt.Println("\n--- SQL Generation ---")
		sqlGen := compiler.NewSQLGenerator(schema)
		sqls, err := sqlGen.Generate()
		if err != nil {
			log.Fatalf("SQL generation error: %v", err)
		}

		for i, sql := range sqls {
			fmt.Printf("\n-- Statement %d\n%s\n", i+1, sql)
		}
	}
}
