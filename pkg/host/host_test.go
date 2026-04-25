package host

import (
	"testing"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/runtime"
	"github.com/stretchr/testify/require"
)

func TestRegisterHandlersHook(t *testing.T) {
	exec := runtime.NewExecutor(nil, &ast.Schema{}, nil)
	called := false

	err := RegisterHandlers(exec, func(got *runtime.Executor) error {
		require.Same(t, exec, got)
		called = true
		return nil
	})

	require.NoError(t, err)
	require.True(t, called)
}
