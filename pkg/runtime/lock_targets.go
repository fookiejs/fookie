package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/lib/pq"
)

type lockTarget struct {
	Model string
	ID    string
}

func (t lockTarget) sortKey() string {
	return fmt.Sprintf("%s:%s", t.Model, t.ID)
}

func dedupSortLockTargets(targets []lockTarget) []lockTarget {
	seen := make(map[string]bool)
	out := make([]lockTarget, 0, len(targets))
	for _, t := range targets {
		if t.ID == "" {
			continue
		}
		k := t.sortKey()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].sortKey() < out[j].sortKey()
	})
	return out
}

func collectLockTargetsFromEffect(ctx context.Context, e *Executor, effect *ast.Block, rc *runCtx) ([]lockTarget, error) {
	if effect == nil {
		return nil, nil
	}
	var targets []lockTarget
	for _, stmt := range effect.Statements {
		switch s := stmt.(type) {
		case *ast.EffectUpdateStmt:
			idVal, err := e.evalExpr(ctx, s.IDExpr, rc)
			if err != nil {
				return nil, err
			}
			id, _ := idVal.(string)
			if id != "" {
				targets = append(targets, lockTarget{Model: s.Model, ID: id})
			}
		case *ast.EffectDeleteStmt:
			idVal, err := e.evalExpr(ctx, s.IDExpr, rc)
			if err != nil {
				return nil, err
			}
			id, _ := idVal.(string)
			if id != "" {
				targets = append(targets, lockTarget{Model: s.Model, ID: id})
			}
		}
	}
	return dedupSortLockTargets(targets), nil
}

func partitionCreateLockTargets(modelName, plannedID string, targets []lockTarget) (pre, post []lockTarget) {
	for _, t := range targets {
		if strings.EqualFold(t.Model, modelName) && t.ID == plannedID {
			post = append(post, t)
		} else {
			pre = append(pre, t)
		}
	}
	return dedupSortLockTargets(pre), dedupSortLockTargets(post)
}

func collectLockTargetsForEntityAndEffect(ctx context.Context, e *Executor, entityModel, entityID string, effect *ast.Block, rc *runCtx) ([]lockTarget, error) {
	out := []lockTarget{{Model: entityModel, ID: entityID}}
	if effect == nil {
		return dedupSortLockTargets(out), nil
	}
	extra, err := collectLockTargetsFromEffect(ctx, e, effect, rc)
	if err != nil {
		return nil, err
	}
	out = append(out, extra...)
	return dedupSortLockTargets(out), nil
}

func (e *Executor) acquireRowLocksGlobalOrder(ctx context.Context, targets []lockTarget) error {
	targets = dedupSortLockTargets(targets)
	for i := 0; i < len(targets); {
		j := i + 1
		for j < len(targets) && targets[j].Model == targets[i].Model {
			j++
		}
		if err := e.lockOneModelRun(ctx, targets[i:j]); err != nil {
			return err
		}
		i = j
	}
	return nil
}

func scanIDFromRow(raw interface{}) (string, error) {
	switch v := raw.(type) {
	case []byte:
		return string(v), nil
	case string:
		return v, nil
	case nil:
		return "", fmt.Errorf("null id")
	default:
		return fmt.Sprint(v), nil
	}
}

func (e *Executor) lockOneModelRun(ctx context.Context, batch []lockTarget) error {
	if len(batch) == 0 {
		return nil
	}
	model := batch[0].Model
	for _, t := range batch[1:] {
		if t.Model != model {
			return fmt.Errorf("lock batch model mismatch")
		}
	}
	if _, _, err := e.resolveOp(model, "read"); err != nil {
		return fmt.Errorf("lock model %s: %w", model, err)
	}
	table := compiler.SnakeCase(model)
	ex := e.execer(ctx)
	if len(batch) == 1 {
		var one int
		err := ex.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT 1 FROM "%s" WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, table),
			batch[0].ID,
		).Scan(&one)
		if err != nil {
			return fmt.Errorf("FOR UPDATE %s %s: %w", model, batch[0].ID, err)
		}
		return nil
	}
	ids := make([]string, len(batch))
	for i, t := range batch {
		ids[i] = t.ID
	}
	q := fmt.Sprintf(
		`SELECT id FROM "%s" WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL ORDER BY id::text FOR UPDATE`,
		table,
	)
	rows, err := ex.QueryContext(ctx, q, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("FOR UPDATE batch %s: %w", model, err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(ids))
	for rows.Next() {
		var raw interface{}
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		id, err := scanIDFromRow(raw)
		if err != nil {
			return err
		}
		seen[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if !seen[id] {
			return fmt.Errorf("FOR UPDATE %s: row missing or deleted %s", model, id)
		}
	}
	return nil
}
