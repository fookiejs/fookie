package runtime

import (
	"context"
	"testing"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/stretchr/testify/require"
)

func TestEvalConfigBuiltin(t *testing.T) {
	exec := NewExecutor(nil, &ast.Schema{
		Configs: []*ast.ConfigEntry{
			{Key: "query_page_size", Type: ast.TypeNumber, Value: 50.0},
		},
	}, nil)

	val, err := exec.evalExpr(context.Background(), &ast.BuiltinCall{
		Name: "config",
		Args: []ast.Expression{
			&ast.Literal{Value: "query_page_size"},
		},
	}, newRunCtx(map[string]interface{}{}))

	require.NoError(t, err)
	require.Equal(t, 50.0, val)
}
