package runtime

import (
	"context"
	"database/sql"
	"time"

	"github.com/fookiejs/fookie/pkg/ast"
	cronlib "github.com/robfig/cron/v3"
)

func ExecuteCrons(ctx context.Context, schema *ast.Schema, db *sql.DB) error {
	for _, cb := range schema.Crons {
		for _, entry := range cb.Entries {
			var existing int
			db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM outbox
				WHERE external_name = $1
				  AND recur_cron    = $2
				  AND status        = 'pending'`,
				entry.Name, entry.CronExpr).Scan(&existing)
			if existing > 0 {
				continue
			}

			db.ExecContext(ctx, `
				INSERT INTO outbox
				  (entity_type, entity_id, external_name, payload, status, recur_cron, run_after)
				VALUES ('cron', NULL, $1, '{}', 'pending', $2, NOW())`,
				entry.Name, entry.CronExpr)
		}
	}
	return nil
}

func cronNextAfter(expr string, after time.Time) time.Time {
	schedule, err := cronlib.ParseStandard(expr)
	if err == nil {
		return schedule.Next(after)
	}
	secParser := cronlib.NewParser(
		cronlib.Second | cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow | cronlib.Descriptor,
	)
	schedule, err = secParser.Parse(expr)
	if err == nil {
		return schedule.Next(after)
	}
	return after.Add(time.Second)
}

func FindCronEntry(schema *ast.Schema, name string) *ast.CronEntry {
	for _, cb := range schema.Crons {
		for _, entry := range cb.Entries {
			if entry.Name == name {
				return entry
			}
		}
	}
	return nil
}

func (e *Executor) ExecuteCronBody(ctx context.Context, entry *ast.CronEntry) error {
	if entry == nil || entry.Body == nil {
		return nil
	}
	rc, ctx := e.rootRC(ctx, map[string]interface{}{"__system": true}, "cron", entry.Name)
	start := time.Now()
	e.emitRuntime(ctx, rc, "info", "cron firing", map[string]interface{}{
		"cron_name": entry.Name,
		"cron_expr": entry.CronExpr,
		"stmts":     len(entry.Body.Statements),
	})
	err := e.execBlock(ctx, "cron", entry.Body, rc)
	dur := time.Since(start)
	if err != nil {
		e.emitRuntime(ctx, rc, "error", "cron failed", map[string]interface{}{
			"cron_name":   entry.Name,
			"duration_ms": dur.Milliseconds(),
			"error":       err.Error(),
		})
		return err
	}
	e.emitRuntime(ctx, rc, "info", "cron done", map[string]interface{}{
		"cron_name":   entry.Name,
		"duration_ms": dur.Milliseconds(),
		"vars":        len(rc.vars),
	})
	return nil
}
