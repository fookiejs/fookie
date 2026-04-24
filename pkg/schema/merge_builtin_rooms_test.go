package schema

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fookiejs/fookie/pkg/parser"
)

func TestMergeBuiltinRooms_ParseAndMerge(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "..", "..")
	demo, err := os.ReadFile(filepath.Join(root, "demo", "schema.fql"))
	require.NoError(t, err)
	lex := parser.NewLexer(string(demo))
	p := parser.NewParser(lex.Tokenize())
	s, err := p.Parse()
	require.NoError(t, err)
	require.Nil(t, modelByName(s, "Room"))
	err = MergeBuiltinRooms(s)
	require.NoError(t, err)
	require.NotNil(t, modelByName(s, "Room"))
	require.NotNil(t, modelByName(s, "RoomMember"))
	require.True(t, externalByName(s, "RoomGraphQLNotify"))
}
