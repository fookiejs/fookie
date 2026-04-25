package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/parser"
)

// LoadSchema loads a single .fql file or all *.fql files inside a directory.
// Multiple files are merged deterministically (sorted by filename).
// Duplicate model/external names are silently deduplicated — first definition wins.
func LoadSchema(pathOrDir string) (*ast.Schema, error) {
	info, err := os.Stat(pathOrDir)
	if err != nil {
		return nil, fmt.Errorf("schema path %q: %w", pathOrDir, err)
	}

	var paths []string
	if info.IsDir() {
		matches, err := filepath.Glob(filepath.Join(pathOrDir, "*.fql"))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no *.fql files found in directory %q", pathOrDir)
		}
		sort.Strings(matches) // deterministic order
		paths = matches
	} else {
		paths = []string{pathOrDir}
	}

	if len(paths) == 1 {
		return parseFile(paths[0])
	}

	// Merge multiple schema files
	merged := &ast.Schema{}
	for _, p := range paths {
		s, err := parseFile(p)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", p, err)
		}
		mergeInto(merged, s)
	}
	return merged, nil
}

func parseFile(path string) (*ast.Schema, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lex := parser.NewLexer(string(content))
	p := parser.NewParser(lex.Tokenize())
	return p.Parse()
}

// mergeInto appends models, externals, modules, seeds, setups, and crons
// from src into dst. Duplicates (by name) are skipped.
func mergeInto(dst, src *ast.Schema) {
	for _, m := range src.Models {
		if modelByName(dst, m.Name) == nil {
			dst.Models = append(dst.Models, m)
		}
	}
	for _, e := range src.Externals {
		if !externalByName(dst, e.Name) {
			dst.Externals = append(dst.Externals, e)
		}
	}
	dst.Modules = append(dst.Modules, src.Modules...)
	dst.Seeds = append(dst.Seeds, src.Seeds...)
	dst.Setups = append(dst.Setups, src.Setups...)
	dst.Crons = append(dst.Crons, src.Crons...)
	mergeConfigs(dst, src.Configs)
}

func mergeConfigs(dst *ast.Schema, src []*ast.ConfigEntry) {
	for _, c := range src {
		found := false
		for i, existing := range dst.Configs {
			if existing.Key == c.Key {
				dst.Configs[i] = c
				found = true
				break
			}
		}
		if !found {
			dst.Configs = append(dst.Configs, c)
		}
	}
}
