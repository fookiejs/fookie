package tests

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fookiejs/fookie/pkg/validator"
)

func TestBuiltinRegistered(t *testing.T) {
	require.True(t, validator.BuiltinRegistered("notEmptyString"))
	require.True(t, validator.BuiltinRegistered("between"))
	require.True(t, validator.BuiltinRegistered("oneOf"))
	require.True(t, validator.BuiltinRegistered("requireAdminKey"))
	require.True(t, validator.BuiltinRegistered("nobody"))
	require.False(t, validator.BuiltinRegistered("noSuchBuiltin"))
}

func TestBetween(t *testing.T) {
	v, ok := validator.GetBuiltin("between")
	require.True(t, ok)
	out, err := v(5.0, 1.0, 10.0)
	require.NoError(t, err)
	require.Equal(t, true, out)
	out, err = v(0.0, 1.0, 10.0)
	require.NoError(t, err)
	require.Equal(t, false, out)
}

func TestOneOf(t *testing.T) {
	v, ok := validator.GetBuiltin("oneOf")
	require.True(t, ok)
	out, err := v("warrior", "mage", "warrior", "rogue")
	require.NoError(t, err)
	require.Equal(t, true, out)
	out, err = v("priest", "mage", "warrior")
	require.NoError(t, err)
	require.Equal(t, false, out)
}

func TestMinLenUnicode(t *testing.T) {
	v, ok := validator.GetBuiltin("minLen")
	require.True(t, ok)
	out, err := v("İİ", 2.0)
	require.NoError(t, err)
	require.Equal(t, true, out)
}

func TestRequireAdminKey(t *testing.T) {
	t.Setenv("FOOKEE_ADMIN_KEY", "secret")
	v, ok := validator.GetBuiltin("requireAdminKey")
	require.True(t, ok)
	out, err := v("wrong")
	require.NoError(t, err)
	require.Equal(t, false, out)
	out, err = v("secret")
	require.NoError(t, err)
	require.Equal(t, true, out)
}

func TestRequireAdminKeyUnsetEnv(t *testing.T) {
	t.Setenv("FOOKEE_ADMIN_KEY", "")
	v, ok := validator.GetBuiltin("requireAdminKey")
	require.True(t, ok)
	out, err := v("anything")
	require.NoError(t, err)
	require.Equal(t, false, out)
}

func TestNobody(t *testing.T) {
	v, ok := validator.GetBuiltin("nobody")
	require.True(t, ok)
	out, err := v()
	require.NoError(t, err)
	require.Equal(t, false, out)
}
