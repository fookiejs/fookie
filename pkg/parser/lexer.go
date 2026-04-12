package parser

import (
	"unicode"
)

// Token types for FSL
type TokenType int

const (
	TOKEN_EOF TokenType = iota
	TOKEN_NEWLINE
	TOKEN_INDENT
	TOKEN_DEDENT

	// Keywords
	TOKEN_MODEL
	TOKEN_EXTERNAL
	TOKEN_MODULE
	TOKEN_CREATE
	TOKEN_READ
	TOKEN_UPDATE
	TOKEN_DELETE
	TOKEN_USE
	TOKEN_ROLE
	TOKEN_RULE
	TOKEN_MODIFY
	TOKEN_EFFECT
	TOKEN_WHERE
	TOKEN_ORDERBY
	TOKEN_CURSOR
	TOKEN_RETURN
	TOKEN_INPUT
	TOKEN_OUTPUT
	TOKEN_FIELDS

	// Types
	TOKEN_STRING_TYPE
	TOKEN_NUMBER_TYPE
	TOKEN_BOOLEAN_TYPE
	TOKEN_ID_TYPE
	TOKEN_DATE_TYPE
	TOKEN_TIMESTAMP_TYPE
	TOKEN_JSON_TYPE

	// Literals
	TOKEN_IDENTIFIER
	TOKEN_STRING
	TOKEN_NUMBER
	TOKEN_BOOL
	TOKEN_NULL

	// Operators
	TOKEN_ASSIGN    // =
	TOKEN_EQ        // ==
	TOKEN_NE        // !=
	TOKEN_LT        // <
	TOKEN_LE        // <=
	TOKEN_GT        // >
	TOKEN_GE        // >=
	TOKEN_AND       // &&
	TOKEN_OR        // ||
	TOKEN_NOT       // !
	TOKEN_PLUS      // +
	TOKEN_MINUS     // -
	TOKEN_MULT      // *
	TOKEN_DIV       // /
	TOKEN_DOT       // .
	TOKEN_COMMA     // ,
	TOKEN_COLON     // :
	TOKEN_SEMICOLON // ;
	TOKEN_LPAREN    // (
	TOKEN_RPAREN    // )
	TOKEN_LBRACE    // {
	TOKEN_RBRACE    // }
	TOKEN_LBRACKET  // [
	TOKEN_RBRACKET  // ]

	TOKEN_COMMENT
	TOKEN_CONSTRAINT
)

type Token struct {
	Type   TokenType
	Value  string
	LineNo int
	ColNo  int
}

type Lexer struct {
	input           []rune
	position        int
	current         rune
	lineNo          int
	colNo           int
	tokens          []Token
	indentStack     []int
	pendingDeindent int
}

var keywords = map[string]TokenType{
	"model":     TOKEN_MODEL,
	"external":  TOKEN_EXTERNAL,
	"module":    TOKEN_MODULE,
	"create":    TOKEN_CREATE,
	"read":      TOKEN_READ,
	"update":    TOKEN_UPDATE,
	"delete":    TOKEN_DELETE,
	"use":       TOKEN_USE,
	"role":      TOKEN_ROLE,
	"rule":      TOKEN_RULE,
	"modify":    TOKEN_MODIFY,
	"effect":    TOKEN_EFFECT,
	"where":     TOKEN_WHERE,
	"orderBy":   TOKEN_ORDERBY,
	"cursor":    TOKEN_CURSOR,
	"return":    TOKEN_RETURN,
	"input":     TOKEN_INPUT,
	"output":    TOKEN_OUTPUT,
	"fields":    TOKEN_FIELDS,
	"string":    TOKEN_STRING_TYPE,
	"number":    TOKEN_NUMBER_TYPE,
	"boolean":   TOKEN_BOOLEAN_TYPE,
	"id":        TOKEN_ID_TYPE,
	"date":      TOKEN_DATE_TYPE,
	"timestamp": TOKEN_TIMESTAMP_TYPE,
	"json":      TOKEN_JSON_TYPE,
	"true":      TOKEN_BOOL,
	"false":     TOKEN_BOOL,
	"null":      TOKEN_NULL,
}

func NewLexer(input string) *Lexer {
	runes := []rune(input)
	lex := &Lexer{
		input:       runes,
		position:    0,
		lineNo:      1,
		colNo:       1,
		indentStack: []int{0},
	}
	lex.advance()
	return lex
}

func (l *Lexer) advance() {
	if l.position >= len(l.input) {
		l.current = 0
	} else {
		l.current = l.input[l.position]
	}
	l.position++
}

func (l *Lexer) peek() rune {
	if l.position >= len(l.input) {
		return 0
	}
	return l.input[l.position]
}

func (l *Lexer) peekN(n int) rune {
	pos := l.position + n - 1
	if pos >= len(l.input) {
		return 0
	}
	return l.input[pos]
}

func (l *Lexer) skipWhitespace() {
	for l.current == ' ' || l.current == '\t' {
		if l.current == '\t' {
			l.colNo += 4
		} else {
			l.colNo++
		}
		l.advance()
	}
}

func (l *Lexer) skipComment() {
	if l.current == '#' {
		for l.current != '\n' && l.current != 0 {
			l.advance()
		}
	}
}

func (l *Lexer) readString(quote rune) string {
	start := l.position - 1
	for l.current != quote && l.current != 0 {
		if l.current == '\\' {
			l.advance()
		}
		l.advance()
	}
	str := string(l.input[start : l.position-1])
	if l.current == quote {
		l.advance()
	}
	return str
}

func (l *Lexer) readIdentifier() string {
	start := l.position - 1
	for isIdentifierChar(l.current) {
		l.advance()
	}
	return string(l.input[start : l.position-1])
}

func (l *Lexer) readNumber() string {
	start := l.position - 1
	for unicode.IsDigit(l.current) || l.current == '.' {
		l.advance()
	}
	return string(l.input[start : l.position-1])
}

func (l *Lexer) handleIndentation() []Token {
	var tokens []Token

	if l.current == '\n' {
		l.lineNo++
		l.colNo = 1
		l.advance()

		// Count spaces
		indent := 0
		for l.current == ' ' || l.current == '\t' {
			if l.current == '\t' {
				indent += 4
			} else {
				indent++
			}
			l.advance()
		}

		// Skip blank lines
		if l.current == '\n' || l.current == '#' || l.current == 0 {
			return tokens
		}

		currentIndent := l.indentStack[len(l.indentStack)-1]
		if indent > currentIndent {
			l.indentStack = append(l.indentStack, indent)
			tokens = append(tokens, Token{Type: TOKEN_INDENT, LineNo: l.lineNo, ColNo: l.colNo})
		} else if indent < currentIndent {
			for len(l.indentStack) > 0 && l.indentStack[len(l.indentStack)-1] > indent {
				l.indentStack = l.indentStack[:len(l.indentStack)-1]
				tokens = append(tokens, Token{Type: TOKEN_DEDENT, LineNo: l.lineNo, ColNo: l.colNo})
			}
		}
	}
	return tokens
}

func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	if l.current == '#' {
		l.skipComment()
		return l.NextToken()
	}

	lineNo := l.lineNo
	colNo := l.colNo

	switch l.current {
	case 0:
		// EOF: emit remaining DEDENTs
		if len(l.indentStack) > 1 {
			l.indentStack = l.indentStack[:len(l.indentStack)-1]
			return Token{Type: TOKEN_DEDENT, LineNo: lineNo, ColNo: colNo}
		}
		return Token{Type: TOKEN_EOF, LineNo: lineNo, ColNo: colNo}

	case '\n':
		indents := l.handleIndentation()
		if len(indents) > 0 {
			return indents[0]
		}
		return Token{Type: TOKEN_NEWLINE, LineNo: lineNo, ColNo: colNo}

	case '"', '\'':
		quote := l.current
		l.advance()
		str := l.readString(quote)
		return Token{Type: TOKEN_STRING, Value: str, LineNo: lineNo, ColNo: colNo}

	case '=':
		l.advance()
		if l.current == '=' {
			l.advance()
			return Token{Type: TOKEN_EQ, Value: "==", LineNo: lineNo, ColNo: colNo}
		}
		return Token{Type: TOKEN_ASSIGN, Value: "=", LineNo: lineNo, ColNo: colNo}

	case '!':
		l.advance()
		if l.current == '=' {
			l.advance()
			return Token{Type: TOKEN_NE, Value: "!=", LineNo: lineNo, ColNo: colNo}
		}
		return Token{Type: TOKEN_NOT, Value: "!", LineNo: lineNo, ColNo: colNo}

	case '<':
		l.advance()
		if l.current == '=' {
			l.advance()
			return Token{Type: TOKEN_LE, Value: "<=", LineNo: lineNo, ColNo: colNo}
		}
		return Token{Type: TOKEN_LT, Value: "<", LineNo: lineNo, ColNo: colNo}

	case '>':
		l.advance()
		if l.current == '=' {
			l.advance()
			return Token{Type: TOKEN_GE, Value: ">=", LineNo: lineNo, ColNo: colNo}
		}
		return Token{Type: TOKEN_GT, Value: ">", LineNo: lineNo, ColNo: colNo}

	case '&':
		l.advance()
		if l.current == '&' {
			l.advance()
			return Token{Type: TOKEN_AND, Value: "&&", LineNo: lineNo, ColNo: colNo}
		}

	case '|':
		l.advance()
		if l.current == '|' {
			l.advance()
			return Token{Type: TOKEN_OR, Value: "||", LineNo: lineNo, ColNo: colNo}
		}

	case '+':
		l.advance()
		return Token{Type: TOKEN_PLUS, Value: "+", LineNo: lineNo, ColNo: colNo}

	case '-':
		l.advance()
		if l.current == '-' {
			l.advance()
			// Read constraint
			constraint := "--"
			for isIdentifierChar(l.current) || l.current == ' ' {
				if l.current != ' ' {
					constraint += string(l.current)
				}
				l.advance()
			}
			return Token{Type: TOKEN_CONSTRAINT, Value: constraint, LineNo: lineNo, ColNo: colNo}
		}
		return Token{Type: TOKEN_MINUS, Value: "-", LineNo: lineNo, ColNo: colNo}

	case '*':
		l.advance()
		return Token{Type: TOKEN_MULT, Value: "*", LineNo: lineNo, ColNo: colNo}

	case '/':
		l.advance()
		return Token{Type: TOKEN_DIV, Value: "/", LineNo: lineNo, ColNo: colNo}

	case '.':
		l.advance()
		return Token{Type: TOKEN_DOT, Value: ".", LineNo: lineNo, ColNo: colNo}

	case ',':
		l.advance()
		return Token{Type: TOKEN_COMMA, Value: ",", LineNo: lineNo, ColNo: colNo}

	case ':':
		l.advance()
		return Token{Type: TOKEN_COLON, Value: ":", LineNo: lineNo, ColNo: colNo}

	case ';':
		l.advance()
		return Token{Type: TOKEN_SEMICOLON, Value: ";", LineNo: lineNo, ColNo: colNo}

	case '(':
		l.advance()
		return Token{Type: TOKEN_LPAREN, Value: "(", LineNo: lineNo, ColNo: colNo}

	case ')':
		l.advance()
		return Token{Type: TOKEN_RPAREN, Value: ")", LineNo: lineNo, ColNo: colNo}

	case '{':
		l.advance()
		return Token{Type: TOKEN_LBRACE, Value: "{", LineNo: lineNo, ColNo: colNo}

	case '}':
		l.advance()
		return Token{Type: TOKEN_RBRACE, Value: "}", LineNo: lineNo, ColNo: colNo}

	case '[':
		l.advance()
		return Token{Type: TOKEN_LBRACKET, Value: "[", LineNo: lineNo, ColNo: colNo}

	case ']':
		l.advance()
		return Token{Type: TOKEN_RBRACKET, Value: "]", LineNo: lineNo, ColNo: colNo}

	default:
		if unicode.IsDigit(l.current) {
			num := l.readNumber()
			return Token{Type: TOKEN_NUMBER, Value: num, LineNo: lineNo, ColNo: colNo}
		}
		if isIdentifierStart(l.current) {
			ident := l.readIdentifier()
			if tt, ok := keywords[ident]; ok {
				return Token{Type: tt, Value: ident, LineNo: lineNo, ColNo: colNo}
			}
			return Token{Type: TOKEN_IDENTIFIER, Value: ident, LineNo: lineNo, ColNo: colNo}
		}
		l.advance()
		return Token{Type: TOKEN_IDENTIFIER, Value: string(l.current), LineNo: lineNo, ColNo: colNo}
	}

	l.advance()
	return Token{Type: TOKEN_IDENTIFIER, LineNo: lineNo, ColNo: colNo}
}

func isIdentifierStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isIdentifierChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// Tokenize returns all tokens in the input
func (l *Lexer) Tokenize() []Token {
	var tokens []Token
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == TOKEN_EOF {
			break
		}
	}
	return tokens
}
