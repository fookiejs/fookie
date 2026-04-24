package runtime

import (
	"context"
	"fmt"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
)

func executeSeedBlock(ctx context.Context, sb *ast.SeedBlock, exec *Executor) error {
	for _, entry := range sb.Entries {
		for _, record := range entry.Records {
			keyVal, ok := record[entry.KeyField]
			if !ok {
				continue
			}

			existing, err := exec.Read(ctx, entry.Model, map[string]interface{}{
				"filter": map[string]interface{}{
					compiler.SnakeCase(entry.KeyField): map[string]interface{}{"eq": keyVal},
				},
			})
			if err != nil {
				return fmt.Errorf("seed check %s.%s=%v: %w", entry.Model, entry.KeyField, keyVal, err)
			}
			if len(existing) > 0 {
				continue
			}

			if _, err := exec.Create(ctx, entry.Model, WithSystemInput(record)); err != nil {
				return fmt.Errorf("seed create %s %s=%v: %w", entry.Model, entry.KeyField, keyVal, err)
			}
		}
	}
	return nil
}

func ExecuteSeeds(ctx context.Context, schema *ast.Schema, exec *Executor) error {
	for _, sb := range schema.Seeds {
		if err := executeSeedBlock(ctx, sb, exec); err != nil {
			return err
		}
	}
	return nil
}
