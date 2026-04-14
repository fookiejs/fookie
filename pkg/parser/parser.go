package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fookiejs/fookie/pkg/ast"
)

type Parser struct {
	tokens  []Token
	pos     int
	errors  []string
}

func NewParser(tokens []Token) *Parser {
	var cleaned []Token
	for _, t := range tokens {
		if t.Type != TOKEN_NEWLINE {
			cleaned = append(cleaned, t)
		}
	}
	return &Parser{tokens: cleaned, pos: 0}
}

func (p *Parser) cur() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peek(offset int) Token {
	i := p.pos + offset
	if i >= len(p.tokens) {
		return Token{Type: TOKEN_EOF}
	}
	return p.tokens[i]
}

func (p *Parser) eat() Token {
	t := p.cur()
	p.pos++
	return t
}

func (p *Parser) expect(tt TokenType) (Token, error) {
	if p.cur().Type != tt {
		return Token{}, p.errorf("expected token %d, got %q (type %d)", tt, p.cur().Value, p.cur().Type)
	}
	return p.eat(), nil
}

func (p *Parser) errorf(format string, args ...interface{}) error {
	msg := fmt.Sprintf("line %d: "+format, append([]interface{}{p.cur().LineNo}, args...)...)
	p.errors = append(p.errors, msg)
	return fmt.Errorf("%s", msg)
}

func isType(t Token) bool {
	switch t.Type {
	case TOKEN_STRING_TYPE, TOKEN_NUMBER_TYPE, TOKEN_BOOLEAN_TYPE,
		TOKEN_ID_TYPE, TOKEN_DATE_TYPE, TOKEN_TIMESTAMP_TYPE, TOKEN_JSON_TYPE,
		TOKEN_IDENTIFIER:
		return true
	}
	return false
}

func typeString(t Token) (ast.FieldType, *string) {
	switch t.Type {
	case TOKEN_STRING_TYPE:
		return ast.TypeString, nil
	case TOKEN_NUMBER_TYPE:
		return ast.TypeNumber, nil
	case TOKEN_BOOLEAN_TYPE:
		return ast.TypeBoolean, nil
	case TOKEN_ID_TYPE:
		return ast.TypeID, nil
	case TOKEN_DATE_TYPE:
		return ast.TypeDate, nil
	case TOKEN_TIMESTAMP_TYPE:
		return ast.TypeTimestamp, nil
	case TOKEN_JSON_TYPE:
		return ast.TypeJSON, nil
	case TOKEN_IDENTIFIER:
		switch t.Value {
		case "email":
			return ast.TypeEmail, nil
		case "url":
			return ast.TypeURL, nil
		case "phone":
			return ast.TypePhone, nil
		case "uuid":
			return ast.TypeUUID, nil
		case "coordinate":
			return ast.TypeCoordinate, nil
		case "color":
			return ast.TypeColor, nil
		case "currency":
			return ast.TypeCurrency, nil
		case "locale":
			return ast.TypeLocale, nil
		case "iban":
			return ast.TypeIBAN, nil
		case "ipaddress":
			return ast.TypeIPAddress, nil
		default:
			s := t.Value
			return ast.TypeRelation, &s
		}
	default:
		s := t.Value
		return ast.TypeRelation, &s
	}
}

func (p *Parser) Parse() (*ast.Schema, error) {
	schema := &ast.Schema{}

	for p.cur().Type != TOKEN_EOF {
		switch p.cur().Type {
		case TOKEN_MODEL:
			p.eat()
			m, err := p.parseModel()
			if err != nil {
				return nil, err
			}
			schema.Models = append(schema.Models, m)

		case TOKEN_EXTERNAL:
			p.eat()
			e, err := p.parseExternal()
			if err != nil {
				return nil, err
			}
			schema.Externals = append(schema.Externals, e)

		case TOKEN_MODULE:
			p.eat()
			mod, err := p.parseModule()
			if err != nil {
				return nil, err
			}
			schema.Modules = append(schema.Modules, mod)

		default:
			return nil, p.errorf("unexpected top-level token: %q", p.cur().Value)
		}
	}

	if len(p.errors) > 0 {
		return nil, fmt.Errorf("parse errors:\n%s", strings.Join(p.errors, "\n"))
	}
	return schema, nil
}

func (p *Parser) parseModel() (*ast.Model, error) {
	name, err := p.expect(TOKEN_IDENTIFIER)
	if err != nil {
		return nil, err
	}
	model := &ast.Model{
		Name: name.Value,
		CRUD: make(map[string]*ast.Operation),
	}

	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		switch p.cur().Type {
		case TOKEN_USE:
			p.eat()
			mod, err := p.expect(TOKEN_IDENTIFIER)
			if err != nil {
				return nil, err
			}
			model.Uses = append(model.Uses, mod.Value)

		case TOKEN_FIELDS:
			p.eat()
			fields, err := p.parseFields()
			if err != nil {
				return nil, err
			}
			model.Fields = fields

		case TOKEN_CREATE, TOKEN_READ, TOKEN_UPDATE, TOKEN_DELETE:
			opType := p.eat().Value
			op, err := p.parseOperation(opType)
			if err != nil {
				return nil, err
			}
			model.CRUD[opType] = op

		default:
			return nil, p.errorf("unexpected token in model %q: %q", model.Name, p.cur().Value)
		}
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return model, nil
}

func (p *Parser) parseFields() ([]*ast.Field, error) {
	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	var fields []*ast.Field
	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		if p.cur().Type != TOKEN_IDENTIFIER {
			return nil, p.errorf("expected field name, got %q", p.cur().Value)
		}
		fieldName := p.eat().Value

		if p.cur().Type == TOKEN_COLON {
			p.eat()
		}

		if !isType(p.cur()) {
			return nil, p.errorf("expected type after field name %q", fieldName)
		}
		typeTok := p.eat()
		ft, rel := typeString(typeTok)

		field := &ast.Field{
			Name:     fieldName,
			Type:     ft,
			Relation: rel,
		}

		for p.cur().Type == TOKEN_CONSTRAINT {
			field.Constraints = append(field.Constraints, p.eat().Value)
		}

		fields = append(fields, field)
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return fields, nil
}

func (p *Parser) parseOperation(opType string) (*ast.Operation, error) {
	op := &ast.Operation{Type: opType}

	if p.cur().Type == TOKEN_LPAREN {
		p.eat()
		fields, err := p.parseSelectList()
		if err != nil {
			return nil, err
		}
		op.Select = fields
		if _, err := p.expect(TOKEN_RPAREN); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		switch p.cur().Type {
		case TOKEN_ROLE:
			p.eat()
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			op.Role = b

		case TOKEN_RULE:
			p.eat()
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			op.Rule = b

		case TOKEN_MODIFY:
			p.eat()
			b, err := p.parseModifyBlock()
			if err != nil {
				return nil, err
			}
			op.Modify = b

		case TOKEN_EFFECT:
			p.eat()
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			op.Effect = b

		case TOKEN_COMPENSATE:
			p.eat()
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			op.Compensate = b

		case TOKEN_WHERE:
			p.eat()
			w, err := p.parseWhereClause()
			if err != nil {
				return nil, err
			}
			op.Where = w

		case TOKEN_ORDERBY:
			p.eat()
			obs, err := p.parseOrderBy()
			if err != nil {
				return nil, err
			}
			op.OrderBy = obs

		case TOKEN_CURSOR:
			p.eat()
			cur, err := p.parseCursor()
			if err != nil {
				return nil, err
			}
			op.Cursor = cur

		case TOKEN_LOCK:
			p.eat()
			if p.cur().Type == TOKEN_TRUE {
				p.eat()
				op.Lock = true
			}

		default:
			return nil, p.errorf("unexpected token in %q operation: %q", opType, p.cur().Value)
		}
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return op, nil
}

func (p *Parser) parseBlock() (*ast.Block, error) {
	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	block := &ast.Block{}

	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		if p.cur().Type == TOKEN_IDENTIFIER && p.peek(1).Type == TOKEN_ASSIGN {
			name := p.eat().Value
			p.eat()
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			block.Statements = append(block.Statements, &ast.Assignment{
				Name:   name,
				Value:  expr,
				LineNo: p.cur().LineNo,
			})
		} else {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			block.Statements = append(block.Statements, &ast.PredicateExpr{
				Expr:   expr,
				LineNo: p.cur().LineNo,
			})
		}
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return block, nil
}

func (p *Parser) parseModifyBlock() (*ast.Block, error) {
	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	block := &ast.Block{}

	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		if !isWordToken(p.cur()) || p.peek(1).Type != TOKEN_ASSIGN {
			return nil, p.errorf("expected `field = expr` in modify block, got %q", p.cur().Value)
		}
		fieldName := p.eat().Value
		p.eat()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		block.Statements = append(block.Statements, &ast.ModifyAssignment{
			Field:  fieldName,
			Value:  expr,
			LineNo: p.cur().LineNo,
		})
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return block, nil
}

func isWordToken(t Token) bool {
	switch t.Type {
	case TOKEN_IDENTIFIER,
		TOKEN_STRING_TYPE, TOKEN_NUMBER_TYPE, TOKEN_BOOLEAN_TYPE,
		TOKEN_ID_TYPE, TOKEN_DATE_TYPE, TOKEN_TIMESTAMP_TYPE, TOKEN_JSON_TYPE,
		TOKEN_SUM, TOKEN_COUNT, TOKEN_AVG, TOKEN_MIN, TOKEN_MAX,
		TOKEN_LOCK, TOKEN_SIZE, TOKEN_ASC, TOKEN_DESC, TOKEN_IN, TOKEN_NOT,
		TOKEN_CREATE, TOKEN_READ, TOKEN_UPDATE, TOKEN_DELETE,
		TOKEN_ROLE, TOKEN_RULE, TOKEN_MODIFY, TOKEN_EFFECT, TOKEN_COMPENSATE,
		TOKEN_WHERE, TOKEN_ORDERBY, TOKEN_CURSOR, TOKEN_RETURN,
		TOKEN_USE, TOKEN_FIELDS, TOKEN_INPUT, TOKEN_OUTPUT:
		return true
	}
	return false
}

func (p *Parser) parseWhereClause() (*ast.WhereClause, error) {
	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	w := &ast.WhereClause{}
	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		w.Conditions = append(w.Conditions, expr)
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return w, nil
}

func (p *Parser) parseOrderBy() ([]*ast.OrderBy, error) {
	var obs []*ast.OrderBy

	if p.cur().Type == TOKEN_LBRACE {
		p.eat()
		for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
			ob, err := p.parseSingleOrderBy()
			if err != nil {
				return nil, err
			}
			obs = append(obs, ob)
			if p.cur().Type == TOKEN_COMMA {
				p.eat()
			}
		}
		if _, err := p.expect(TOKEN_RBRACE); err != nil {
			return nil, err
		}
	} else {
		ob, err := p.parseSingleOrderBy()
		if err != nil {
			return nil, err
		}
		obs = append(obs, ob)
	}

	return obs, nil
}

func (p *Parser) parseSingleOrderBy() (*ast.OrderBy, error) {
	path, err := p.parseFieldPath()
	if err != nil {
		return nil, err
	}
	ob := &ast.OrderBy{Field: strings.Join(path, ".")}

	if p.cur().Type == TOKEN_DESC {
		p.eat()
		ob.Desc = true
	} else if p.cur().Type == TOKEN_ASC {
		p.eat()
	}
	return ob, nil
}

func (p *Parser) parseCursor() (*ast.Cursor, error) {
	cur := &ast.Cursor{}

	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		switch p.cur().Type {
		case TOKEN_SIZE:
			p.eat()
			n, err := p.expect(TOKEN_NUMBER)
			if err != nil {
				return nil, err
			}
			v, _ := strconv.Atoi(n.Value)
			cur.Size = v

		default:
			return nil, p.errorf("unexpected cursor option: %q", p.cur().Value)
		}
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return cur, nil
}

func (p *Parser) parseSelectList() ([]*ast.SelectField, error) {
	var fields []*ast.SelectField

	for p.cur().Type != TOKEN_RPAREN && p.cur().Type != TOKEN_EOF {
		sf, err := p.parseSelectField()
		if err != nil {
			return nil, err
		}
		fields = append(fields, sf)
		if p.cur().Type == TOKEN_COMMA {
			p.eat()
		}
	}
	return fields, nil
}

func (p *Parser) parseSelectField() (*ast.SelectField, error) {
	sf := &ast.SelectField{}

	if isWordToken(p.cur()) && p.peek(1).Type == TOKEN_COLON {
		sf.Alias = p.eat().Value
		p.eat()
	}

	if isAggregate(p.cur()) {
		agg, err := p.parseAggregate()
		if err != nil {
			return nil, err
		}
		sf.Expr = agg
	} else {
		path, err := p.parseFieldPath()
		if err != nil {
			return nil, err
		}
		sf.Expr = ast.PlainField{Path: path}
	}

	return sf, nil
}

func isAggregate(t Token) bool {
	switch t.Type {
	case TOKEN_SUM, TOKEN_COUNT, TOKEN_AVG, TOKEN_MIN, TOKEN_MAX:
		return true
	}
	return false
}

func (p *Parser) parseAggregate() (*ast.AggregateFunc, error) {
	fnTok := p.eat()

	if _, err := p.expect(TOKEN_LPAREN); err != nil {
		return nil, err
	}

	path, err := p.parseFieldPath()
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(TOKEN_RPAREN); err != nil {
		return nil, err
	}

	return &ast.AggregateFunc{
		Fn:    fnTok.Value,
		Field: path,
	}, nil
}

func (p *Parser) parseExpr() (ast.Expression, error) {
	return p.parseOr()
}

func (p *Parser) parseOr() (ast.Expression, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().Type == TOKEN_OR {
		op := p.eat().Value
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryOp{Left: left, Op: op, Right: right, LineNo: p.cur().LineNo}
	}
	return left, nil
}

func (p *Parser) parseAnd() (ast.Expression, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.cur().Type == TOKEN_AND {
		op := p.eat().Value
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryOp{Left: left, Op: op, Right: right, LineNo: p.cur().LineNo}
	}
	return left, nil
}

func (p *Parser) parseComparison() (ast.Expression, error) {
	left, err := p.parseAddSub()
	if err != nil {
		return nil, err
	}

	for isCompOp(p.cur()) {
		op := p.eat().Value
		right, err := p.parseAddSub()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryOp{Left: left, Op: op, Right: right, LineNo: p.cur().LineNo}
	}

	if p.cur().Type == TOKEN_IN {
		p.eat()
		vals, err := p.parseInList()
		if err != nil {
			return nil, err
		}
		return &ast.InExpr{Left: left, Values: vals, LineNo: p.cur().LineNo}, nil
	}

	return left, nil
}

func isCompOp(t Token) bool {
	switch t.Type {
	case TOKEN_EQ, TOKEN_NE, TOKEN_LT, TOKEN_LE, TOKEN_GT, TOKEN_GE:
		return true
	}
	return false
}

func (p *Parser) parseInList() ([]ast.Expression, error) {
	if _, err := p.expect(TOKEN_LBRACKET); err != nil {
		return nil, err
	}
	var exprs []ast.Expression
	for p.cur().Type != TOKEN_RBRACKET && p.cur().Type != TOKEN_EOF {
		e, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
		if p.cur().Type == TOKEN_COMMA {
			p.eat()
		}
	}
	if _, err := p.expect(TOKEN_RBRACKET); err != nil {
		return nil, err
	}
	return exprs, nil
}

func (p *Parser) parseAddSub() (ast.Expression, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.cur().Type == TOKEN_PLUS || p.cur().Type == TOKEN_MINUS {
		op := p.eat().Value
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryOp{Left: left, Op: op, Right: right, LineNo: p.cur().LineNo}
	}
	return left, nil
}

func (p *Parser) parseUnary() (ast.Expression, error) {
	if p.cur().Type == TOKEN_BANG || p.cur().Type == TOKEN_NOT {
		op := p.eat().Value
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryOp{Op: op, Right: right, LineNo: p.cur().LineNo}, nil
	}
	return p.parsePrimary()
}

func (p *Parser) parsePrimary() (ast.Expression, error) {
	t := p.cur()

	switch t.Type {
	case TOKEN_STRING:
		p.eat()
		return &ast.Literal{Value: t.Value, LineNo: t.LineNo}, nil

	case TOKEN_NUMBER:
		p.eat()
		f, _ := strconv.ParseFloat(t.Value, 64)
		return &ast.Literal{Value: f, LineNo: t.LineNo}, nil

	case TOKEN_TRUE:
		p.eat()
		return &ast.Literal{Value: true, LineNo: t.LineNo}, nil

	case TOKEN_FALSE:
		p.eat()
		return &ast.Literal{Value: false, LineNo: t.LineNo}, nil

	case TOKEN_NULL:
		p.eat()
		return &ast.Literal{Value: nil, LineNo: t.LineNo}, nil

	case TOKEN_LPAREN:
		p.eat()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TOKEN_RPAREN); err != nil {
			return nil, err
		}
		return e, nil

	default:
		if !isWordToken(t) {
			return nil, p.errorf("unexpected token in expression: %q", t.Value)
		}
	}

	first := p.eat()

	if p.cur().Type == TOKEN_LPAREN {
		if isBuiltinValidator(first.Value) {
			return p.parseBuiltinCall(first.Value, first.LineNo)
		}
		return p.parseCallTail(first.Value, first.LineNo)
	}

	path := []string{first.Value}
	for p.cur().Type == TOKEN_DOT {
		p.eat()
		if !isWordToken(p.cur()) {
			return nil, p.errorf("expected field name after '.', got %q", p.cur().Value)
		}
		path = append(path, p.eat().Value)
	}

	return &ast.FieldAccess{
		Object: path[0],
		Fields: path[1:],
		LineNo: first.LineNo,
	}, nil
}

func isBuiltinValidator(name string) bool {
	validators := map[string]bool{
		"isEmail": true, "isRFC5321": true, "isRFC5322": true,
		"isURL": true, "isHTTP": true, "isHTTPS": true,
		"isE164": true, "isValidPhone": true, "getPhoneCountry": true,
		"isValidUUID": true, "getUUIDVersion": true,
		"isValidCoordinate": true, "isWithinBounds": true, "getDistance": true,
		"isHexColor": true, "isRGBColor": true, "isHSLColor": true,
		"isISOCurrency": true, "isValidLocale": true, "getBCP47": true,
		"isValidIBAN": true, "getIBANCountry": true, "getIBANChecksum": true,
		"isIPv4": true, "isIPv6": true, "isPrivateIP": true, "getIPVersion": true,
	}
	return validators[name]
}

func (p *Parser) parseBuiltinCall(name string, lineNo int) (ast.Expression, error) {
	if _, err := p.expect(TOKEN_LPAREN); err != nil {
		return nil, err
	}

	var args []ast.Expression
	for p.cur().Type != TOKEN_RPAREN && p.cur().Type != TOKEN_EOF {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.cur().Type == TOKEN_COMMA {
			p.eat()
		}
	}

	if _, err := p.expect(TOKEN_RPAREN); err != nil {
		return nil, err
	}

	return &ast.BuiltinCall{Name: name, Args: args, LineNo: lineNo}, nil
}

func (p *Parser) parseCallTail(name string, lineNo int) (ast.Expression, error) {
	if _, err := p.expect(TOKEN_LPAREN); err != nil {
		return nil, err
	}

	params := make(map[string]ast.Expression)

	if p.cur().Type == TOKEN_LBRACE {
		p.eat()
		for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
			if !isWordToken(p.cur()) {
				return nil, p.errorf("expected param name in call to %q", name)
			}
			key := p.eat().Value
			if _, err := p.expect(TOKEN_COLON); err != nil {
				return nil, err
			}
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			params[key] = val
			if p.cur().Type == TOKEN_COMMA {
				p.eat()
			}
		}
		if _, err := p.expect(TOKEN_RBRACE); err != nil {
			return nil, err
		}
	} else if p.cur().Type != TOKEN_RPAREN {
		for p.cur().Type != TOKEN_RPAREN && p.cur().Type != TOKEN_EOF {
			if !isWordToken(p.cur()) {
				return nil, p.errorf("expected param name")
			}
			key := p.eat().Value
			if _, err := p.expect(TOKEN_COLON); err != nil {
				return nil, err
			}
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			params[key] = val
			if p.cur().Type == TOKEN_COMMA {
				p.eat()
			}
		}
	}

	if _, err := p.expect(TOKEN_RPAREN); err != nil {
		return nil, err
	}

	return &ast.ExternalCall{Name: name, Params: params, LineNo: lineNo}, nil
}

func (p *Parser) parseFieldPath() ([]string, error) {
	if !isWordToken(p.cur()) {
		return nil, p.errorf("expected field name, got %q", p.cur().Value)
	}
	path := []string{p.eat().Value}
	for p.cur().Type == TOKEN_DOT {
		p.eat()
		if !isWordToken(p.cur()) {
			return nil, p.errorf("expected field name after '.'")
		}
		path = append(path, p.eat().Value)
	}
	return path, nil
}

func (p *Parser) parseExternal() (*ast.External, error) {
	name, err := p.expect(TOKEN_IDENTIFIER)
	if err != nil {
		return nil, err
	}
	ext := &ast.External{
		Name:   name.Value,
		Input:  make(map[string]string),
		Output: make(map[string]string),
	}

	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		switch p.cur().Type {
		case TOKEN_INPUT:
			p.eat()
			fields, err := p.parseFields()
			if err != nil {
				return nil, err
			}
			for _, f := range fields {
				ext.Input[f.Name] = string(f.Type)
			}
		case TOKEN_OUTPUT:
			p.eat()
			fields, err := p.parseFields()
			if err != nil {
				return nil, err
			}
			for _, f := range fields {
				ext.Output[f.Name] = string(f.Type)
			}
		default:
			return nil, p.errorf("unexpected token in external %q: %q", ext.Name, p.cur().Value)
		}
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return ext, nil
}

func (p *Parser) parseModule() (*ast.Module, error) {
	name, err := p.expect(TOKEN_IDENTIFIER)
	if err != nil {
		return nil, err
	}
	mod := &ast.Module{Name: name.Value}

	if _, err := p.expect(TOKEN_LBRACE); err != nil {
		return nil, err
	}

	for p.cur().Type != TOKEN_RBRACE && p.cur().Type != TOKEN_EOF {
		switch p.cur().Type {
		case TOKEN_ROLE:
			p.eat()
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			mod.Role = b
		case TOKEN_RULE:
			p.eat()
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			mod.Rule = b
		case TOKEN_MODIFY:
			p.eat()
			b, err := p.parseModifyBlock()
			if err != nil {
				return nil, err
			}
			mod.Modify = b
		case TOKEN_EFFECT:
			p.eat()
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			mod.Effect = b
		default:
			return nil, p.errorf("unexpected token in module %q: %q", mod.Name, p.cur().Value)
		}
	}

	if _, err := p.expect(TOKEN_RBRACE); err != nil {
		return nil, err
	}
	return mod, nil
}
