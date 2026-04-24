package schema

import (
	_ "embed"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/parser"
)

//go:embed builtin_rooms.fql
var builtinRoomsFQL string

func modelByName(s *ast.Schema, name string) *ast.Model {
	for _, m := range s.Models {
		if m.Name == name {
			return m
		}
	}
	return nil
}

func externalByName(s *ast.Schema, name string) bool {
	for _, e := range s.Externals {
		if e.Name == name {
			return true
		}
	}
	return false
}

func MergeBuiltinRooms(dst *ast.Schema) error {
	lex := parser.NewLexer(builtinRoomsFQL)
	tokens := lex.Tokenize()
	p := parser.NewParser(tokens)
	extra, err := p.Parse()
	if err != nil {
		return err
	}
	for _, m := range extra.Models {
		if modelByName(dst, m.Name) != nil {
			continue
		}
		dst.Models = append(dst.Models, m)
	}
	for _, ext := range extra.Externals {
		if externalByName(dst, ext.Name) {
			continue
		}
		dst.Externals = append(dst.Externals, ext)
	}
	return nil
}
