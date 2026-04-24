package parser

import "unicode"

type TokenType int

const (
	TOKEN_EOF TokenType = iota
	TOKEN_NEWLINE

	TOKEN_MODEL
	TOKEN_EXTERNAL
	TOKEN_MODULE
	TOKEN_SEED
	TOKEN_CRON
	TOKEN_USE
	TOKEN_SETUP
	TOKEN_NOTIFY

	TOKEN_CREATE
	TOKEN_READ
	TOKEN_UPDATE
	TOKEN_DELETE

	TOKEN_ROLE
	TOKEN_RULE
	TOKEN_MODIFY
	TOKEN_EFFECT
	TOKEN_COMPENSATE

	TOKEN_FILTER
	TOKEN_ORDERBY
	TOKEN_CURSOR
	TOKEN_RETURN
	TOKEN_BODY
	TOKEN_OUTPUT
	TOKEN_FIELDS
	TOKEN_FOR

	TOKEN_SUM
	TOKEN_COUNT
	TOKEN_AVG
	TOKEN_MIN
	TOKEN_MAX
	TOKEN_STDDEV
	TOKEN_VARIANCE

	TOKEN_SIZE
	TOKEN_ASC
	TOKEN_DESC
	TOKEN_IN
	TOKEN_NOT
	TOKEN_NULL
	TOKEN_TRUE
	TOKEN_FALSE

	TOKEN_STRING_TYPE
	TOKEN_NUMBER_TYPE
	TOKEN_BOOLEAN_TYPE
	TOKEN_ID_TYPE
	TOKEN_DATE_TYPE
	TOKEN_TIMESTAMP_TYPE
	TOKEN_JSON_TYPE

	TOKEN_IDENTIFIER
	TOKEN_STRING
	TOKEN_NUMBER

	TOKEN_ASSIGN
	TOKEN_EQ
	TOKEN_NE
	TOKEN_LT
	TOKEN_LE
	TOKEN_GT
	TOKEN_GE
	TOKEN_AND
	TOKEN_OR
	TOKEN_BANG
	TOKEN_PLUS
	TOKEN_MINUS
	TOKEN_MULT
	TOKEN_DIV
	TOKEN_DOT
	TOKEN_COMMA
	TOKEN_COLON
	TOKEN_LPAREN
	TOKEN_RPAREN
	TOKEN_LBRACE
	TOKEN_RBRACE
	TOKEN_LBRACKET
	TOKEN_RBRACKET

	TOKEN_CONSTRAINT
	TOKEN_ILLEGAL
)

type Token struct {
	Type   TokenType
	Value  string
	LineNo int
	ColNo  int
}

var keywords = map[string]TokenType{
	"model":    TOKEN_MODEL,
	"external": TOKEN_EXTERNAL,
	"module":   TOKEN_MODULE,
	"seed":     TOKEN_SEED,
	"cron":     TOKEN_CRON,
	"use":      TOKEN_USE,
	"setup":    TOKEN_SETUP,
	"notify":   TOKEN_NOTIFY,

	"create": TOKEN_CREATE,
	"read":   TOKEN_READ,
	"update": TOKEN_UPDATE,
	"delete": TOKEN_DELETE,

	"role":       TOKEN_ROLE,
	"rule":       TOKEN_RULE,
	"modify":     TOKEN_MODIFY,
	"effect":     TOKEN_EFFECT,
	"compensate": TOKEN_COMPENSATE,

	"filter": TOKEN_FILTER,
	"where":  TOKEN_FILTER,
	"orderBy": TOKEN_ORDERBY,
	"cursor":  TOKEN_CURSOR,
	"return":  TOKEN_RETURN,
	"body":    TOKEN_BODY,
	"output":  TOKEN_OUTPUT,
	"fields":  TOKEN_FIELDS,
	"for":     TOKEN_FOR,

	"sum":      TOKEN_SUM,
	"count":    TOKEN_COUNT,
	"avg":      TOKEN_AVG,
	"min":      TOKEN_MIN,
	"max":      TOKEN_MAX,
	"stddev":   TOKEN_STDDEV,
	"variance": TOKEN_VARIANCE,

	"size":  TOKEN_SIZE,
	"asc":   TOKEN_ASC,
	"desc":  TOKEN_DESC,
	"in":    TOKEN_IN,
	"not":   TOKEN_NOT,
	"null":  TOKEN_NULL,
	"true":  TOKEN_TRUE,
	"false": TOKEN_FALSE,

	"string":    TOKEN_STRING_TYPE,
	"number":    TOKEN_NUMBER_TYPE,
	"boolean":   TOKEN_BOOLEAN_TYPE,
	"id":        TOKEN_ID_TYPE,
	"date":      TOKEN_DATE_TYPE,
	"timestamp": TOKEN_TIMESTAMP_TYPE,
	"json":      TOKEN_JSON_TYPE,
}

type Lexer struct {
	src  []rune
	pos  int
	line int
	col  int
}

func NewLexer(src string) *Lexer {
	return &Lexer{src: []rune(src), pos: 0, line: 1, col: 1}
}

func (l *Lexer) cur() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peek1() rune {
	if l.pos+1 >= len(l.src) {
		return 0
	}
	return l.src[l.pos+1]
}

func (l *Lexer) eat() rune {
	r := l.cur()
	l.pos++
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *Lexer) skipWhitespace() {
	for l.cur() == ' ' || l.cur() == '\t' || l.cur() == '\r' {
		l.eat()
	}
}

func (l *Lexer) readString(quote rune) string {
	l.eat()
	var buf []rune
	for l.cur() != quote && l.cur() != 0 {
		if l.cur() == '\\' {
			l.eat()
			switch l.cur() {
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			default:
				buf = append(buf, l.cur())
			}
			l.eat()
		} else {
			buf = append(buf, l.eat())
		}
	}
	if l.cur() == quote {
		l.eat()
	}
	return string(buf)
}

func (l *Lexer) readNumber() string {
	start := l.pos
	for unicode.IsDigit(l.cur()) || l.cur() == '.' {
		l.eat()
	}
	return string(l.src[start:l.pos])
}

func (l *Lexer) readIdentifier() string {
	start := l.pos
	for unicode.IsLetter(l.cur()) || unicode.IsDigit(l.cur()) || l.cur() == '_' {
		l.eat()
	}
	return string(l.src[start:l.pos])
}

func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	line, col := l.line, l.col

	if l.cur() == '#' {
		for l.cur() != '\n' && l.cur() != 0 {
			l.eat()
		}
		return Token{Type: TOKEN_ILLEGAL, Value: "line comments (#) are not supported in FQL", LineNo: line, ColNo: col}
	}
	if l.cur() == '/' && l.peek1() == '/' {
		for l.cur() != '\n' && l.cur() != 0 {
			l.eat()
		}
		return Token{Type: TOKEN_ILLEGAL, Value: "line comments (//) are not supported in FQL", LineNo: line, ColNo: col}
	}

	c := l.cur()

	switch {
	case c == 0:
		return Token{Type: TOKEN_EOF, LineNo: line, ColNo: col}

	case c == '\n':
		l.eat()
		return Token{Type: TOKEN_NEWLINE, LineNo: line, ColNo: col}

	case c == '"' || c == '\'':
		s := l.readString(c)
		return Token{Type: TOKEN_STRING, Value: s, LineNo: line, ColNo: col}

	case unicode.IsDigit(c) || (c == '-' && unicode.IsDigit(l.peek1())):
		neg := ""
		if c == '-' {
			l.eat()
			neg = "-"
		}
		return Token{Type: TOKEN_NUMBER, Value: neg + l.readNumber(), LineNo: line, ColNo: col}

	case isIdentStart(c):
		ident := l.readIdentifier()
		if tt, ok := keywords[ident]; ok {
			return Token{Type: tt, Value: ident, LineNo: line, ColNo: col}
		}
		return Token{Type: TOKEN_IDENTIFIER, Value: ident, LineNo: line, ColNo: col}

	case c == '=':
		l.eat()
		if l.cur() == '=' {
			l.eat()
			return Token{Type: TOKEN_EQ, Value: "==", LineNo: line, ColNo: col}
		}
		return Token{Type: TOKEN_ASSIGN, Value: "=", LineNo: line, ColNo: col}

	case c == '!':
		l.eat()
		if l.cur() == '=' {
			l.eat()
			return Token{Type: TOKEN_NE, Value: "!=", LineNo: line, ColNo: col}
		}
		return Token{Type: TOKEN_BANG, Value: "!", LineNo: line, ColNo: col}

	case c == '<':
		l.eat()
		if l.cur() == '=' {
			l.eat()
			return Token{Type: TOKEN_LE, Value: "<=", LineNo: line, ColNo: col}
		}
		return Token{Type: TOKEN_LT, Value: "<", LineNo: line, ColNo: col}

	case c == '>':
		l.eat()
		if l.cur() == '=' {
			l.eat()
			return Token{Type: TOKEN_GE, Value: ">=", LineNo: line, ColNo: col}
		}
		return Token{Type: TOKEN_GT, Value: ">", LineNo: line, ColNo: col}

	case c == '&' && l.peek1() == '&':
		l.eat()
		l.eat()
		return Token{Type: TOKEN_AND, Value: "&&", LineNo: line, ColNo: col}

	case c == '|' && l.peek1() == '|':
		l.eat()
		l.eat()
		return Token{Type: TOKEN_OR, Value: "||", LineNo: line, ColNo: col}

	case c == '+':
		l.eat()
		return Token{Type: TOKEN_PLUS, Value: "+", LineNo: line, ColNo: col}

	case c == '-':
		l.eat()
		if l.cur() == '-' {
			l.eat()
			l.skipWhitespace()
			word := l.readIdentifier()
			constraint := "--" + word
			pos := l.pos
			l.skipWhitespace()
			if l.cur() == 'd' {
				extra := l.readIdentifier()
				if extra == "desc" {
					constraint += " desc"
				} else {
					l.pos = pos
				}
			} else {
				l.pos = pos
			}
			return Token{Type: TOKEN_CONSTRAINT, Value: constraint, LineNo: line, ColNo: col}
		}
		return Token{Type: TOKEN_MINUS, Value: "-", LineNo: line, ColNo: col}

	case c == '*':
		l.eat()
		return Token{Type: TOKEN_MULT, Value: "*", LineNo: line, ColNo: col}

	case c == '/':
		l.eat()
		return Token{Type: TOKEN_DIV, Value: "/", LineNo: line, ColNo: col}

	case c == '.':
		l.eat()
		return Token{Type: TOKEN_DOT, Value: ".", LineNo: line, ColNo: col}

	case c == ',':
		l.eat()
		return Token{Type: TOKEN_COMMA, Value: ",", LineNo: line, ColNo: col}

	case c == ':':
		l.eat()
		return Token{Type: TOKEN_COLON, Value: ":", LineNo: line, ColNo: col}

	case c == '(':
		l.eat()
		return Token{Type: TOKEN_LPAREN, Value: "(", LineNo: line, ColNo: col}

	case c == ')':
		l.eat()
		return Token{Type: TOKEN_RPAREN, Value: ")", LineNo: line, ColNo: col}

	case c == '{':
		l.eat()
		return Token{Type: TOKEN_LBRACE, Value: "{", LineNo: line, ColNo: col}

	case c == '}':
		l.eat()
		return Token{Type: TOKEN_RBRACE, Value: "}", LineNo: line, ColNo: col}

	case c == '[':
		l.eat()
		return Token{Type: TOKEN_LBRACKET, Value: "[", LineNo: line, ColNo: col}

	case c == ']':
		l.eat()
		return Token{Type: TOKEN_RBRACKET, Value: "]", LineNo: line, ColNo: col}

	default:
		l.eat()
		return l.NextToken()
	}
}

func (l *Lexer) Tokenize() []Token {
	var out []Token
	for {
		tok := l.NextToken()
		out = append(out, tok)
		if tok.Type == TOKEN_EOF {
			break
		}
	}
	return out
}

func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}
