package schema

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fookiejs/fookie/pkg/parser"
)

func TestMergeBuiltinRooms_ParseAndMerge(t *testing.T) {
	lex := parser.NewLexer(`model User { fields { name: string } read {} }`)
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
