package compiler

import (
	"fmt"
	"strings"

	"github.com/fookiejs/fookie/pkg/ast"
)

type SQLGenerator struct {
	schema *ast.Schema
}

func NewSQLGenerator(schema *ast.Schema) *SQLGenerator {
	return &SQLGenerator{schema: schema}
}

func (sg *SQLGenerator) Generate() ([]string, error) {
	var sqls []string

	for _, model := range sg.schema.Models {
		sqls = append(sqls, sg.modelDDL(model))
	}
	for _, model := range sg.schema.Models {
		sqls = append(sqls, sg.modelColumnMigrations(model)...)
	}

	sqls = append(sqls,
		sg.auditLogDDL(),
		sg.eventLogDDL(),
		sg.outboxDDL(),

		`ALTER TABLE "outbox" ADD COLUMN IF NOT EXISTS "target_field" VARCHAR(255)`,
		`ALTER TABLE "outbox" ADD COLUMN IF NOT EXISTS "run_after" TIMESTAMPTZ`,
		`ALTER TABLE "outbox" ADD COLUMN IF NOT EXISTS "recur_cron" VARCHAR(64)`,
		`ALTER TABLE "outbox" ADD COLUMN IF NOT EXISTS "root_request_id" TEXT`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_root_request ON "outbox"("root_request_id")`,
	)

	return sqls, nil
}

func (sg *SQLGenerator) modelDDL(m *ast.Model) string {
	cols := []string{
		`"id" UUID PRIMARY KEY DEFAULT gen_random_uuid()`,
		`"created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		`"updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		`"status" VARCHAR(64) NOT NULL DEFAULT 'initiate'`,
		`"deleted_at" TIMESTAMPTZ`,
	}

	for _, f := range m.Fields {
		cols = append(cols, sg.fieldDDL(f))
	}

	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS \"%s\" (\n  %s\n);",
		snake(m.Name), strings.Join(cols, ",\n  "),
	)
}

func (sg *SQLGenerator) modelColumnMigrations(m *ast.Model) []string {
	table := snake(m.Name)
	var out []string
	for _, f := range m.Fields {
		col := fieldColumn(f)
		sqlType := fieldSQLType(f)
		out = append(out, fmt.Sprintf(
			`ALTER TABLE "%s" ADD COLUMN IF NOT EXISTS "%s" %s`,
			table, col, sqlType,
		))
	}
	return out
}

func (sg *SQLGenerator) fieldDDL(f *ast.Field) string {
	col := fieldColumn(f)
	sqlType := fieldSQLType(f)
	def := fmt.Sprintf(`"%s" %s`, col, sqlType)

	for _, c := range f.Constraints {
		switch c {
		case "--unique":
			def += " UNIQUE"
		}
	}
	return def
}

func fieldColumn(f *ast.Field) string {
	col := snake(f.Name)
	if f.Type == ast.TypeRelation {
		col += "_id"
	}
	return col
}

func fieldSQLType(f *ast.Field) string {
	switch f.Type {
	case ast.TypeString:
		return "VARCHAR(255)"
	case ast.TypeNumber:
		return "NUMERIC(18,4)"
	case ast.TypeBoolean:
		return "BOOLEAN NOT NULL DEFAULT FALSE"
	case ast.TypeID:
		return "UUID"
	case ast.TypeDate:
		return "DATE"
	case ast.TypeTimestamp:
		return "TIMESTAMPTZ"
	case ast.TypeJSON:
		return "JSONB"
	case ast.TypeRelation:
		return fmt.Sprintf("UUID REFERENCES %s(id)", snake(*f.Relation))
	case ast.TypeEmail:
		return "VARCHAR(255)"
	case ast.TypeURL:
		return "VARCHAR(2048)"
	case ast.TypePhone:
		return "VARCHAR(20)"
	case ast.TypeUUID:
		return "UUID"
	case ast.TypeCoordinate:
		return "POINT"
	case ast.TypeColor:
		return "VARCHAR(20)"
	case ast.TypeCurrency:
		return "VARCHAR(3)"
	case ast.TypeLocale:
		return "VARCHAR(10)"
	case ast.TypeIBAN:
		return "VARCHAR(34)"
	case ast.TypeIPAddress:
		return "INET"
	default:
		return "TEXT"
	}
}

func (sg *SQLGenerator) auditLogDDL() string {
	return `CREATE TABLE IF NOT EXISTS "audit_logs" (
  "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "transaction_id" UUID,
  "user_id" UUID,
  "action" VARCHAR(255),
  "details" JSONB,
  "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tx ON "audit_logs"("transaction_id");
CREATE INDEX IF NOT EXISTS idx_audit_logs_usr ON "audit_logs"("user_id");`
}

func (sg *SQLGenerator) eventLogDDL() string {
	return `CREATE TABLE IF NOT EXISTS "event_logs" (
  "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "entity_type" VARCHAR(255),
  "entity_id" UUID,
  "event_type" VARCHAR(255),
  "payload" JSONB,
  "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_event_logs_entity ON "event_logs"("entity_type", "entity_id");`
}

func (sg *SQLGenerator) outboxDDL() string {
	return `CREATE TABLE IF NOT EXISTS "outbox" (
  "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "entity_type" VARCHAR(255) NOT NULL,
  "entity_id" UUID,
  "external_name" VARCHAR(255) NOT NULL,
  "payload" JSONB,
  "status" VARCHAR(64) NOT NULL DEFAULT 'pending',
  "retry_count" INT NOT NULL DEFAULT 0,
  "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "processed_at" TIMESTAMPTZ,
  "error_message" TEXT,
  "saga_id" UUID,
  "saga_step" INT NOT NULL DEFAULT 0,
  "is_compensation" BOOLEAN NOT NULL DEFAULT FALSE,
  "result_payload" JSONB,
  "target_field" VARCHAR(255),
  "run_after"  TIMESTAMPTZ,
  "recur_cron" VARCHAR(64)
);
CREATE INDEX IF NOT EXISTS idx_outbox_status ON "outbox"("status");
CREATE INDEX IF NOT EXISTS idx_outbox_entity ON "outbox"("entity_type", "entity_id");
CREATE INDEX IF NOT EXISTS idx_outbox_saga ON "outbox"("saga_id");`
}

func (sg *SQLGenerator) readQueryPrefix(model *ast.Model, op *ast.Operation) string {
	table := snake(model.Name)
	projection := sg.buildProjection(op.Select)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("SELECT %s\nFROM \"%s\"", projection, table))

	b.WriteString("\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'")

	if op.Filter != nil {
		for _, cond := range op.Filter.Conditions {
			b.WriteString(fmt.Sprintf("\n  AND %s", sg.compileExpr(cond)))
		}
	}

	return b.String()
}

func (sg *SQLGenerator) CompileRead(model *ast.Model, op *ast.Operation) string {
	return sg.CompileReadWithFilter(model, op, "", 0, 0)
}

func (sg *SQLGenerator) CompileReadWithFilter(model *ast.Model, op *ast.Operation, filterClause string, limit, offset int) string {
	q := sg.readQueryPrefix(model, op)
	if filterClause != "" {
		q += "\n  AND (" + filterClause + ")"
	}

	for i, ob := range op.OrderBy {
		if i == 0 {
			q += "\nORDER BY "
		} else {
			q += ", "
		}
		q += fmt.Sprintf(`"%s"`, snake(ob.Field))
		if ob.Desc {
			q += " DESC"
		}
	}

	effectiveLimit := limit
	if effectiveLimit == 0 && op.Cursor != nil && op.Cursor.Size > 0 {
		effectiveLimit = op.Cursor.Size
	}
	if effectiveLimit > 0 {
		q += fmt.Sprintf("\nLIMIT %d", effectiveLimit)
	}
	if offset > 0 {
		q += fmt.Sprintf("\nOFFSET %d", offset)
	}
	return q + "\nFOR SHARE;"
}

func (sg *SQLGenerator) buildProjection(fields []*ast.SelectField) string {
	if len(fields) == 0 {
		return "*"
	}

	var parts []string
	for _, sf := range fields {
		parts = append(parts, sg.compileSelectField(sf))
	}
	return strings.Join(parts, ", ")
}

func (sg *SQLGenerator) compileSelectField(sf *ast.SelectField) string {
	switch e := sf.Expr.(type) {
	case ast.PlainField:
		col := snake(strings.Join(e.Path, "_"))
		if sf.Alias != "" {
			return fmt.Sprintf("\"%s\" AS \"%s\"", col, sf.Alias)
		}
		return fmt.Sprintf("\"%s\"", col)

	case *ast.AggregateFunc:
		col := snake(strings.Join(e.Field, "_"))
		fn := strings.ToUpper(e.Fn)
		agg := fmt.Sprintf("%s(\"%s\")", fn, col)
		if sf.Alias != "" {
			return fmt.Sprintf("%s AS \"%s\"", agg, sf.Alias)
		}
		return agg

	default:
		return "*"
	}
}

func (sg *SQLGenerator) CompileInsert(model *ast.Model, data map[string]interface{}) (string, []string) {
	table := snake(model.Name)

	var cols, placeholders []string
	var orderedKeys []string

	i := 1
	for k := range data {
		cols = append(cols, fmt.Sprintf("\"%s\"", snake(k)))
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		orderedKeys = append(orderedKeys, k)
		i++
	}

	sql := fmt.Sprintf(
		"INSERT INTO \"%s\" (%s) VALUES (%s) RETURNING \"id\", \"created_at\", \"status\";",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)
	return sql, orderedKeys
}

func (sg *SQLGenerator) CompileUpdate(model *ast.Model, data map[string]interface{}) (string, []string) {
	table := snake(model.Name)

	var sets []string
	var orderedKeys []string

	i := 1
	for k := range data {
		sets = append(sets, fmt.Sprintf("\"%s\" = $%d", snake(k), i))
		orderedKeys = append(orderedKeys, k)
		i++
	}
	idPlaceholder := fmt.Sprintf("$%d", i)

	sql := fmt.Sprintf(
		"UPDATE \"%s\" SET %s, \"updated_at\" = NOW() WHERE \"id\" = %s AND \"deleted_at\" IS NULL RETURNING \"id\", \"updated_at\", \"status\";",
		table,
		strings.Join(sets, ", "),
		idPlaceholder,
	)
	return sql, orderedKeys
}

func (sg *SQLGenerator) CompileSoftDelete(model *ast.Model) string {
	return fmt.Sprintf(
		"UPDATE \"%s\" SET \"deleted_at\" = NOW(), \"updated_at\" = NOW() WHERE \"id\" = $1 AND \"deleted_at\" IS NULL;",
		snake(model.Name),
	)
}

func (sg *SQLGenerator) compileExpr(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.BinaryOp:
		op := e.Op
		if op == "==" {
			op = "="
		} else if op == "!=" {
			op = "<>"
		}
		return fmt.Sprintf("(%s %s %s)",
			sg.compileExpr(e.Left), op, sg.compileExpr(e.Right))

	case *ast.FieldAccess:
		if len(e.Fields) == 0 {
			return snake(e.Object)
		}
		return fmt.Sprintf("/* runtime:%s.%s */", e.Object, strings.Join(e.Fields, "."))

	case *ast.Literal:
		switch v := e.Value.(type) {
		case string:
			return fmt.Sprintf("'%s'", strings.ReplaceAll(v, "'", "''"))
		case float64:
			return fmt.Sprintf("%g", v)
		case bool:
			if v {
				return "TRUE"
			}
			return "FALSE"
		case nil:
			return "NULL"
		}

	case *ast.UnaryOp:
		if e.Op == "!" || e.Op == "not" {
			return fmt.Sprintf("NOT (%s)", sg.compileExpr(e.Right))
		}

	case *ast.InExpr:
		var vals []string
		for _, v := range e.Values {
			vals = append(vals, sg.compileExpr(v))
		}
		return fmt.Sprintf("%s IN (%s)",
			sg.compileExpr(e.Left), strings.Join(vals, ", "))
	}

	return "/* unsupported */"
}

func SnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func snake(s string) string { return SnakeCase(s) }

func (sg *SQLGenerator) appendFilter(sql string, filter *ast.FilterClause) string {
	if filter == nil {
		return sql
	}
	for _, cond := range filter.Conditions {
		sql += fmt.Sprintf("\n  AND %s", sg.compileExpr(cond))
	}
	return sql
}

func (sg *SQLGenerator) CompileSumQuery(model *ast.Model, field string, op *ast.Operation) string {
	table := snake(model.Name)
	col := snake(field)
	sql := fmt.Sprintf("SELECT COALESCE(SUM(\"%s\"), 0) FROM \"%s\"\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'", col, table)
	if op.Filter != nil {
		sql = sg.appendFilter(sql, op.Filter)
	}
	sql += ";"
	return sql
}

func (sg *SQLGenerator) CompileCountQuery(model *ast.Model, op *ast.Operation) string {
	table := snake(model.Name)
	sql := fmt.Sprintf("SELECT COUNT(*) FROM \"%s\"\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'", table)
	if op.Filter != nil {
		sql = sg.appendFilter(sql, op.Filter)
	}
	sql += ";"
	return sql
}

func (sg *SQLGenerator) CompileAvgQuery(model *ast.Model, field string, op *ast.Operation) string {
	table := snake(model.Name)
	col := snake(field)
	sql := fmt.Sprintf("SELECT COALESCE(AVG(\"%s\"), 0) FROM \"%s\"\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'", col, table)
	if op.Filter != nil {
		sql = sg.appendFilter(sql, op.Filter)
	}
	sql += ";"
	return sql
}

func (sg *SQLGenerator) CompileMinQuery(model *ast.Model, field string, op *ast.Operation) string {
	table := snake(model.Name)
	col := snake(field)
	sql := fmt.Sprintf("SELECT MIN(\"%s\") FROM \"%s\"\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'", col, table)
	if op.Filter != nil {
		sql = sg.appendFilter(sql, op.Filter)
	}
	sql += ";"
	return sql
}

func (sg *SQLGenerator) CompileMaxQuery(model *ast.Model, field string, op *ast.Operation) string {
	table := snake(model.Name)
	col := snake(field)
	sql := fmt.Sprintf("SELECT MAX(\"%s\") FROM \"%s\"\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'", col, table)
	if op.Filter != nil {
		sql = sg.appendFilter(sql, op.Filter)
	}
	sql += ";"
	return sql
}

func (sg *SQLGenerator) CompileStddevQuery(model *ast.Model, field string, op *ast.Operation) string {
	table := snake(model.Name)
	col := snake(field)
	sql := fmt.Sprintf("SELECT COALESCE(STDDEV(\"%s\"), 0) FROM \"%s\"\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'", col, table)
	if op.Filter != nil {
		sql = sg.appendFilter(sql, op.Filter)
	}
	sql += ";"
	return sql
}

func (sg *SQLGenerator) CompileVarianceQuery(model *ast.Model, field string, op *ast.Operation) string {
	table := snake(model.Name)
	col := snake(field)
	sql := fmt.Sprintf("SELECT COALESCE(VARIANCE(\"%s\"), 0) FROM \"%s\"\nWHERE \"deleted_at\" IS NULL AND \"status\" = 'done'", col, table)
	if op.Filter != nil {
		sql = sg.appendFilter(sql, op.Filter)
	}
	sql += ";"
	return sql
}
