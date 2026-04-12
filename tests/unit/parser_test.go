package tests

import (
	"testing"

	"github.com/fookiejs/fookie/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLexerBasic(t *testing.T) {
	input := `model Transaction {
  fields {
    amount: number
    status: string
  }
}`

	lexer := parser.NewLexer(input)
	tokens := lexer.Tokenize()

	assert.Greater(t, len(tokens), 0)
	assert.Equal(t, parser.TOKEN_MODEL, tokens[0].Type)
	assert.Equal(t, "Transaction", tokens[1].Value)
}

func TestParserModel(t *testing.T) {
	input := `
external ValidateToken {
  input {
    token: string
  }
  output {
    userId: id
    valid: boolean
  }
}

model Transaction {
  fields {
    amount: number
    fromWalletId: id
  }

  create {
    role {
      principal = ValidateToken(token: input.token)
    }

    rule {
      input.amount > 0
      fromWallet.balance >= input.amount
    }

    modify {
      amount = input.amount
    }

    effect {
    }
  }
}
`

	lexer := parser.NewLexer(input)
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()

	require.NoError(t, err)
	assert.NotNil(t, schema)
	assert.Equal(t, 1, len(schema.Models))
	assert.Equal(t, "Transaction", schema.Models[0].Name)
	assert.Equal(t, 1, len(schema.Externals))
	assert.Equal(t, "ValidateToken", schema.Externals[0].Name)
}

func TestParserExternal(t *testing.T) {
	input := `
external FraudCheck {
  input {
    userId: id
    amount: number
  }
  output {
    allowed: boolean
    score: number
  }
}
`

	lexer := parser.NewLexer(input)
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()

	require.NoError(t, err)
	assert.Equal(t, 1, len(schema.Externals))
	ext := schema.Externals[0]
	assert.Equal(t, "FraudCheck", ext.Name)
	assert.Contains(t, ext.Input, "userId")
	assert.Contains(t, ext.Output, "allowed")
}

func TestParserModule(t *testing.T) {
	input := `
module AuthenticateUser {
  role {
    principal = ValidateToken(token: input.token)
  }

  rule {
    principal.userId != null
  }

  modify {
  }

  effect {
  }
}
`

	lexer := parser.NewLexer(input)
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()

	require.NoError(t, err)
	assert.Equal(t, 1, len(schema.Modules))
	assert.Equal(t, "AuthenticateUser", schema.Modules[0].Name)
}

func TestLexerWithComments(t *testing.T) {
	input := `
# This is a comment
model User {
  # Another comment
  fields {
    email: string --unique
  }
}
`

	lexer := parser.NewLexer(input)
	tokens := lexer.Tokenize()

	// Should not include comment tokens
	for _, tok := range tokens {
		assert.NotEqual(t, parser.TOKEN_COMMENT, tok.Type)
	}
}

func TestLexerIndentation(t *testing.T) {
	input := `model User {
  fields {
    name: string
  }
}`

	lexer := parser.NewLexer(input)
	tokens := lexer.Tokenize()

	indents := 0
	for _, tok := range tokens {
		if tok.Type == parser.TOKEN_INDENT {
			indents++
		}
	}
	assert.Greater(t, indents, 0)
}
