package compiler

import (
	"fmt"
	"strings"

	"github.com/fookiejs/fookie/pkg/ast"
)

// SQLGenerator generates SQL from FSL AST
type SQLGenerator struct {
	schema *ast.Schema
	sqls   []string
	errors []string
}

func NewSQLGenerator(schema *ast.Schema) *SQLGenerator {
	return &SQLGenerator{
		schema: schema,
	}
}

// Generate produces SQL DDL and DML for the schema
func (sg *SQLGenerator) Generate() ([]string, error) {
	// Generate table DDLs first
	for _, model := range sg.schema.Models {
		sql := sg.generateModelDDL(model)
		sg.sqls = append(sg.sqls, sql)
	}

	// Generate implicit tables
	sg.sqls = append(sg.sqls, sg.generateAuditLogTable())
	sg.sqls = append(sg.sqls, sg.generateEventLogTable())
	sg.sqls = append(sg.sqls, sg.generateOutboxTable())

	if len(sg.errors) > 0 {
		return nil, fmt.Errorf("SQL generation errors: %v", sg.errors)
	}

	return sg.sqls, nil
}

func (sg *SQLGenerator) generateModelDDL(model *ast.Model) string {
	var cols []string

	// Implicit columns
	cols = append(cols, "id UUID PRIMARY KEY DEFAULT gen_random_uuid()")
	cols = append(cols, "created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP")
	cols = append(cols, "updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP")
	cols = append(cols, "status VARCHAR(50) DEFAULT 'initiate'")
	cols = append(cols, "deleted_at TIMESTAMP")

	// User-defined fields
	for _, field := range model.Fields {
		col := sg.generateFieldColumn(field)
		cols = append(cols, col)
	}

	tableName := toSnakeCase(model.Name)
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n  %s\n);",
		tableName, strings.Join(cols, ",\n  "))
}

func (sg *SQLGenerator) generateFieldColumn(field *ast.Field) string {
	colName := toSnakeCase(field.Name)
	var colType string

	switch field.Type {
	case ast.TypeString:
		colType = "VARCHAR(255)"
	case ast.TypeNumber:
		colType = "NUMERIC(18,2)"
	case ast.TypeBoolean:
		colType = "BOOLEAN"
	case ast.TypeID:
		colType = "UUID"
	case ast.TypeDate:
		colType = "DATE"
	case ast.TypeTimestamp:
		colType = "TIMESTAMP"
	case ast.TypeJSON:
		colType = "JSONB"
	case ast.TypeRelation:
		// Foreign key
		refTable := toSnakeCase(*field.Relation)
		return fmt.Sprintf("%s UUID REFERENCES %s(id)", colName, refTable)
	default:
		colType = "TEXT"
	}

	col := fmt.Sprintf("%s %s", colName, colType)

	// Add constraints
	for _, constraint := range field.Constraints {
		if constraint == "--unique" {
			col += " UNIQUE"
		} else if constraint == "--index" {
			// Index handled separately
		}
	}

	return col
}

func (sg *SQLGenerator) generateAuditLogTable() string {
	return `
CREATE TABLE IF NOT EXISTS audit_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transaction_id UUID,
  user_id UUID,
  action VARCHAR(255),
  details JSONB,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_transaction_id ON audit_logs(transaction_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
`
}

func (sg *SQLGenerator) generateEventLogTable() string {
	return `
CREATE TABLE IF NOT EXISTS event_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entity_type VARCHAR(255),
  entity_id UUID,
  event_type VARCHAR(255),
  payload JSONB,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_event_logs_entity ON event_logs(entity_type, entity_id);
`
}

func (sg *SQLGenerator) generateOutboxTable() string {
	return `
CREATE TABLE IF NOT EXISTS outbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entity_type VARCHAR(255),
  entity_id UUID,
  external_name VARCHAR(255),
  payload JSONB,
  status VARCHAR(50) DEFAULT 'pending',
  retry_count INT DEFAULT 0,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  processed_at TIMESTAMP,
  error_message TEXT
);

CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox(status);
CREATE INDEX IF NOT EXISTS idx_outbox_entity ON outbox(entity_type, entity_id);
`
}

// CompileCreateOperation generates SQL/pseudo-code for create operation
func (sg *SQLGenerator) CompileCreateOperation(model *ast.Model, op *ast.Operation) string {
	tableName := toSnakeCase(model.Name)

	var sql strings.Builder
	sql.WriteString("-- CREATE Operation\n")
	sql.WriteString("BEGIN TRANSACTION;\n\n")

	// Role block
	if op.Role != nil {
		sql.WriteString("-- Role: Extract principal from auth\n")
		for _, stmt := range op.Role.Statements {
			if assign, ok := stmt.(*ast.Assignment); ok {
				sql.WriteString(fmt.Sprintf("-- %s = ...\n", assign.Name))
			}
		}
	}

	// Rule block
	if op.Rule != nil {
		sql.WriteString("-- Rule: Validate business logic\n")
		sql.WriteString(fmt.Sprintf("SELECT * FROM %s WHERE id = $1;\n", tableName))
		for _, stmt := range op.Rule.Statements {
			if pred, ok := stmt.(*ast.PredicateExpr); ok {
				sql.WriteString(fmt.Sprintf("-- ASSERT: %v\n", pred.Expr))
			}
		}
	}

	// Modify block (INSERT)
	if op.Modify != nil {
		sql.WriteString("-- Modify: Persist to database\n")
		sql.WriteString(fmt.Sprintf(
			"INSERT INTO %s (id, created_at, updated_at, status) VALUES ($1, NOW(), NOW(), 'initiate');\n",
			tableName))
	}

	// Effect block (async operations)
	if op.Effect != nil {
		sql.WriteString("-- Effect: Queue async work to outbox\n")
		sql.WriteString("INSERT INTO outbox (entity_type, entity_id, external_name, payload, status) VALUES (?, ?, ?, ?, 'pending');\n")
	}

	sql.WriteString("\nCOMMIT;\n")

	return sql.String()
}

// CompileReadOperation generates SQL for read operation
func (sg *SQLGenerator) CompileReadOperation(model *ast.Model, op *ast.Operation) string {
	tableName := toSnakeCase(model.Name)

	var sql strings.Builder
	sql.WriteString(fmt.Sprintf("SELECT * FROM %s\n", tableName))

	// WHERE clause
	if op.Where != nil {
		sql.WriteString("WHERE\n")
		for i, cond := range op.Where.Conditions {
			if i > 0 {
				sql.WriteString("  AND ")
			} else {
				sql.WriteString("  ")
			}
			sql.WriteString(fmt.Sprintf("%v\n", cond))
		}
	}

	// ORDER BY
	if len(op.OrderBy) > 0 {
		sql.WriteString("ORDER BY ")
		for i, orderBy := range op.OrderBy {
			if i > 0 {
				sql.WriteString(", ")
			}
			sql.WriteString(toSnakeCase(orderBy.Field))
			if orderBy.Desc {
				sql.WriteString(" DESC")
			}
		}
		sql.WriteString("\n")
	}

	// LIMIT/OFFSET (Cursor)
	if op.Cursor != nil {
		sql.WriteString(fmt.Sprintf("LIMIT %d OFFSET %d;\n", op.Cursor.Size, op.Cursor.Offset))
	}

	return sql.String()
}

// toSnakeCase converts camelCase to snake_case
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}
