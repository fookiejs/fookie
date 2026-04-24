package runtime

import (
	"context"
	"testing"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noopLogger struct{}

func (noopLogger) Info(string, ...interface{})  {}
func (noopLogger) Warn(string, ...interface{})  {}
func (noopLogger) Error(string, ...interface{}) {}

func TestDedupSortLockTargets_orderAndDedup(t *testing.T) {
	in := []lockTarget{
		{Model: "BankWallet", ID: "b"},
		{Model: "BankWallet", ID: "a"},
		{Model: "BankWallet", ID: "b"},
		{Model: "BankUser", ID: "u1"},
	}
	out := dedupSortLockTargets(in)
	require.Len(t, out, 3)
	assert.Equal(t, "BankUser", out[0].Model)
	assert.Equal(t, "u1", out[0].ID)
	assert.Equal(t, "BankWallet", out[1].Model)
	assert.Equal(t, "a", out[1].ID)
	assert.Equal(t, "BankWallet", out[2].Model)
	assert.Equal(t, "b", out[2].ID)
}

func TestPartitionCreateLockTargets(t *testing.T) {
	all := []lockTarget{
		{Model: "WalletTransfer", ID: "t1"},
		{Model: "BankWallet", ID: "self"},
		{Model: "BankWallet", ID: "other"},
	}
	pre, post := partitionCreateLockTargets("BankWallet", "self", all)
	require.Len(t, post, 1)
	assert.Equal(t, "self", post[0].ID)
	require.Len(t, pre, 2)
	keys := map[string]bool{}
	for _, p := range pre {
		keys[p.Model+":"+p.ID] = true
	}
	assert.True(t, keys["BankWallet:other"])
	assert.True(t, keys["WalletTransfer:t1"])
}

func TestCollectLockTargetsFromEffect_literalAndFieldAccess(t *testing.T) {
	e := NewExecutor(nil, &ast.Schema{}, noopLogger{})
	rc := newRunCtx(nil)
	rc.output["entity_id"] = "out-1"

	effect := &ast.Block{
		Statements: []ast.Statement{
			&ast.EffectUpdateStmt{
				Model:  "Foo",
				IDExpr: &ast.Literal{Value: "z"},
			},
			&ast.EffectUpdateStmt{
				Model:  "Foo",
				IDExpr: &ast.Literal{Value: "z"},
			},
			&ast.EffectUpdateStmt{
				Model:  "Bar",
				IDExpr: &ast.Literal{Value: "a"},
			},
			&ast.EffectDeleteStmt{
				Model:  "Foo",
				IDExpr: &ast.FieldAccess{Object: "output", Fields: []string{"entity_id"}},
			},
		},
	}

	got, err := collectLockTargetsFromEffect(context.Background(), e, effect, rc)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "Bar", got[0].Model)
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, "Foo", got[1].Model)
	assert.Equal(t, "out-1", got[1].ID)
	assert.Equal(t, "Foo", got[2].Model)
	assert.Equal(t, "z", got[2].ID)
}

func TestCollectLockTargetsForEntityAndEffect_selfOnly(t *testing.T) {
	e := NewExecutor(nil, &ast.Schema{}, noopLogger{})
	rc := newRunCtx(nil)
	got, err := collectLockTargetsForEntityAndEffect(context.Background(), e, "M", "id-1", nil, rc)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "M", got[0].Model)
	assert.Equal(t, "id-1", got[0].ID)
}
