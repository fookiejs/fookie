package compiler

import (
	"testing"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildWhereClause_EqEmail(t *testing.T) {
	m := &ast.Model{
		Name: "User",
		Fields: []*ast.Field{
			{Name: "email", Type: ast.TypeEmail},
		},
	}
	sg := NewSQLGenerator(&ast.Schema{Models: []*ast.Model{m}})
	clause, args, _, err := sg.BuildWhereClause(m, map[string]interface{}{
		"email": map[string]interface{}{"eq": "a@b.com"},
	}, 1)
	require.NoError(t, err)
	assert.Contains(t, clause, `"email" = $1`)
	assert.Equal(t, []interface{}{"a@b.com"}, args)
}

func TestBuildWhereClause_AND(t *testing.T) {
	m := &ast.Model{
		Name: "User",
		Fields: []*ast.Field{
			{Name: "email", Type: ast.TypeEmail},
			{Name: "name", Type: ast.TypeString},
		},
	}
	sg := NewSQLGenerator(&ast.Schema{Models: []*ast.Model{m}})
	clause, args, _, err := sg.BuildWhereClause(m, map[string]interface{}{
		"AND": []interface{}{
			map[string]interface{}{
				"email": map[string]interface{}{"contains": "gmail"},
			},
			map[string]interface{}{
				"name": map[string]interface{}{"eq": "Bob"},
			},
		},
	}, 1)
	require.NoError(t, err)
	assert.Contains(t, clause, "AND")
	assert.GreaterOrEqual(t, len(args), 2)
}
