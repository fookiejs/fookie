package parser

import (
	"fmt"
	"strconv"

	"github.com/fookiejs/fookie/pkg/ast"
)

type Parser struct {
	tokens    []Token
	position  int
	current   Token
	errors    []string
}

func NewParser(tokens []Token) *Parser {
	p := &Parser{
		tokens: tokens,
	}
	p.advance()
	return p
}

func (p *Parser) advance() {
	if p.position < len(p.tokens) {
		p.current = p.tokens[p.position]
		p.position++
	}
}

func (p *Parser) peek() Token {
	if p.position < len(p.tokens) {
		return p.tokens[p.position]
	}
	return Token{Type: TOKEN_EOF}
}

func (p *Parser) expect(tt TokenType) error {
	if p.current.Type != tt {
		p.error(fmt.Sprintf("expected %v, got %v", tt, p.current.Type))
		return fmt.Errorf("unexpected token: %v", p.current.Type)
	}
	p.advance()
	return nil
}

func (p *Parser) error(msg string) {
	p.errors = append(p.errors, fmt.Sprintf("Line %d: %s", p.current.LineNo, msg))
}

func (p *Parser) skipNewlines() {
	for p.current.Type == TOKEN_NEWLINE || p.current.Type == TOKEN_INDENT {
		p.advance()
	}
}

// Parse is the entry point
func (p *Parser) Parse() (*ast.Schema, error) {
	schema := &ast.Schema{
		Models:    []*ast.Model{},
		Externals: []*ast.External{},
		Modules:   []*ast.Module{},
	}

	p.skipNewlines()

	for p.current.Type != TOKEN_EOF {
		p.skipNewlines()

		switch p.current.Type {
		case TOKEN_MODEL:
			p.advance()
			model, err := p.parseModel()
			if err == nil && model != nil {
				schema.Models = append(schema.Models, model)
			}
		case TOKEN_EXTERNAL:
			p.advance()
			external, err := p.parseExternal()
			if err == nil && external != nil {
				schema.Externals = append(schema.Externals, external)
			}
		case TOKEN_MODULE:
			p.advance()
			module, err := p.parseModule()
			if err == nil && module != nil {
				schema.Modules = append(schema.Modules, module)
			}
		case TOKEN_EOF:
			break
		default:
			p.error(fmt.Sprintf("unexpected token: %v", p.current.Type))
			p.advance()
		}
		p.skipNewlines()
	}

	if len(p.errors) > 0 {
		return nil, fmt.Errorf("parse errors: %v", p.errors)
	}

	return schema, nil
}

func (p *Parser) parseModel() (*ast.Model, error) {
	if p.current.Type != TOKEN_IDENTIFIER {
		p.error("expected model name")
		return nil, fmt.Errorf("expected identifier")
	}

	model := &ast.Model{
		Name:  p.current.Value,
		CRUD:  make(map[string]*ast.Operation),
	}
	p.advance()

	p.skipNewlines()
	if p.expect(TOKEN_LBRACE) != nil {
		return nil, fmt.Errorf("expected {")
	}
	p.skipNewlines()

	// Parse model content
	for p.current.Type != TOKEN_RBRACE && p.current.Type != TOKEN_EOF {
		p.skipNewlines()

		switch p.current.Type {
		case TOKEN_USE:
			p.advance()
			if p.current.Type != TOKEN_IDENTIFIER {
				p.error("expected module name after use")
				break
			}
			model.Uses = append(model.Uses, p.current.Value)
			p.advance()

		case TOKEN_FIELDS:
			p.advance()
			fields, err := p.parseFields()
			if err == nil {
				model.Fields = fields
			}

		case TOKEN_CREATE, TOKEN_READ, TOKEN_UPDATE, TOKEN_DELETE:
			opType := p.current.Value
			p.advance()
			op, err := p.parseOperation(opType)
			if err == nil && op != nil {
				model.CRUD[opType] = op
			}

		case TOKEN_RBRACE:
			break

		default:
			p.error(fmt.Sprintf("unexpected token in model: %v", p.current.Type))
			p.advance()
		}
		p.skipNewlines()
	}

	if p.expect(TOKEN_RBRACE) != nil {
		return nil, fmt.Errorf("expected }")
	}

	return model, nil
}

func (p *Parser) parseFields() ([]*ast.Field, error) {
	var fields []*ast.Field

	p.skipNewlines()
	if p.expect(TOKEN_LBRACE) != nil {
		return nil, fmt.Errorf("expected {")
	}
	p.skipNewlines()

	for p.current.Type != TOKEN_RBRACE && p.current.Type != TOKEN_EOF {
		p.skipNewlines()

		if p.current.Type == TOKEN_IDENTIFIER {
			field := &ast.Field{Name: p.current.Value}
			p.advance()

			// Parse type
			if p.current.Type == TOKEN_IDENTIFIER {
				// Could be a type or relation
				typeName := p.current.Value
				switch typeName {
				case "string":
					field.Type = ast.TypeString
				case "number":
					field.Type = ast.TypeNumber
				case "boolean":
					field.Type = ast.TypeBoolean
				case "id":
					field.Type = ast.TypeID
				case "date":
					field.Type = ast.TypeDate
				case "timestamp":
					field.Type = ast.TypeTimestamp
				case "json":
					field.Type = ast.TypeJSON
				default:
					// It's a relation
					field.Type = ast.TypeRelation
					field.Relation = &typeName
				}
				p.advance()
			}

			// Parse constraints
			for p.current.Type == TOKEN_CONSTRAINT {
				field.Constraints = append(field.Constraints, p.current.Value)
				p.advance()
			}

			fields = append(fields, field)
		}
		p.skipNewlines()
	}

	if p.expect(TOKEN_RBRACE) != nil {
		return nil, fmt.Errorf("expected }")
	}

	return fields, nil
}

func (p *Parser) parseOperation(opType string) (*ast.Operation, error) {
	op := &ast.Operation{Type: opType}

	p.skipNewlines()
	if p.expect(TOKEN_LBRACE) != nil {
		return nil, fmt.Errorf("expected {")
	}
	p.skipNewlines()

	for p.current.Type != TOKEN_RBRACE && p.current.Type != TOKEN_EOF {
		p.skipNewlines()

		switch p.current.Type {
		case TOKEN_ROLE:
			p.advance()
			block, err := p.parseBlock()
			if err == nil {
				op.Role = block
			}

		case TOKEN_RULE:
			p.advance()
			block, err := p.parseBlock()
			if err == nil {
				op.Rule = block
			}

		case TOKEN_MODIFY:
			p.advance()
			block, err := p.parseBlock()
			if err == nil {
				op.Modify = block
			}

		case TOKEN_EFFECT:
			p.advance()
			block, err := p.parseBlock()
			if err == nil {
				op.Effect = block
			}

		case TOKEN_WHERE:
			p.advance()
			where, err := p.parseWhereClause()
			if err == nil {
				op.Where = where
			}

		case TOKEN_ORDERBY:
			p.advance()
			// Parse orderBy
			p.skipNewlines()

		case TOKEN_CURSOR:
			p.advance()
			// Parse cursor
			p.skipNewlines()

		case TOKEN_RETURN:
			p.advance()
			// Parse return fields
			p.skipNewlines()

		case TOKEN_RBRACE:
			break

		default:
			p.advance()
		}
		p.skipNewlines()
	}

	if p.expect(TOKEN_RBRACE) != nil {
		return nil, fmt.Errorf("expected }")
	}

	return op, nil
}

func (p *Parser) parseBlock() (*ast.Block, error) {
	block := &ast.Block{}

	p.skipNewlines()
	if p.expect(TOKEN_LBRACE) != nil {
		return nil, fmt.Errorf("expected {")
	}
	p.skipNewlines()

	for p.current.Type != TOKEN_RBRACE && p.current.Type != TOKEN_EOF {
		p.skipNewlines()

		// Parse statement
		if p.current.Type == TOKEN_IDENTIFIER {
			// Could be assignment or field reference
			saved := p.position - 1
			ident := p.current.Value
			p.advance()

			if p.current.Type == TOKEN_ASSIGN {
				// Assignment: x = ...
				p.advance()
				expr, err := p.parseExpression()
				if err == nil && expr != nil {
					block.Statements = append(block.Statements, &ast.Assignment{
						Name:   ident,
						Value:  expr,
						LineNo: p.current.LineNo,
					})
				}
			} else if p.current.Type == TOKEN_DOT || p.current.Type == TOKEN_EQ ||
				p.current.Type == TOKEN_NE || p.current.Type == TOKEN_LT ||
				p.current.Type == TOKEN_GT || p.current.Type == TOKEN_LE ||
				p.current.Type == TOKEN_GE || p.current.Type == TOKEN_AND ||
				p.current.Type == TOKEN_OR {
				// It's a predicate expression
				p.position = saved + 1
				p.current = p.tokens[p.position-1]
				expr, err := p.parseExpression()
				if err == nil && expr != nil {
					block.Statements = append(block.Statements, &ast.PredicateExpr{
						Expr:   expr,
						LineNo: p.current.LineNo,
					})
				}
			}
		}

		p.skipNewlines()

		if p.current.Type == TOKEN_RBRACE {
			break
		}
	}

	if p.expect(TOKEN_RBRACE) != nil {
		return nil, fmt.Errorf("expected }")
	}

	return block, nil
}

func (p *Parser) parseExpression() (ast.Expression, error) {
	return p.parseBinaryOp()
}

func (p *Parser) parseBinaryOp() (ast.Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for p.isOperator() {
		op := p.current.Value
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryOp{
			Left:   left,
			Op:     op,
			Right:  right,
			LineNo: p.current.LineNo,
		}
	}

	return left, nil
}

func (p *Parser) parsePrimary() (ast.Expression, error) {
	switch p.current.Type {
	case TOKEN_IDENTIFIER:
		ident := p.current.Value
		p.advance()

		// Check for function call or field access
		if p.current.Type == TOKEN_LPAREN {
			// External call: FraudCheck({...})
			p.advance()
			params := make(map[string]ast.Expression)

			for p.current.Type != TOKEN_RPAREN && p.current.Type != TOKEN_EOF {
				if p.current.Type == TOKEN_IDENTIFIER {
					key := p.current.Value
					p.advance()
					if p.expect(TOKEN_COLON) == nil {
						val, _ := p.parseExpression()
						params[key] = val
					}
					if p.current.Type == TOKEN_COMMA {
						p.advance()
					}
				}
			}
			p.expect(TOKEN_RPAREN)

			return &ast.ExternalCall{
				Name:   ident,
				Params: params,
				LineNo: p.current.LineNo,
			}, nil
		} else if p.current.Type == TOKEN_DOT {
			// Field access: principal.userId
			fields := []string{ident}
			for p.current.Type == TOKEN_DOT {
				p.advance()
				if p.current.Type == TOKEN_IDENTIFIER {
					fields = append(fields, p.current.Value)
					p.advance()
				}
			}
			return &ast.FieldAccess{
				Object: fields[0],
				Fields: fields[1:],
				LineNo: p.current.LineNo,
			}, nil
		}

		return &ast.FieldAccess{
			Object: ident,
			Fields: []string{},
			LineNo: p.current.LineNo,
		}, nil

	case TOKEN_STRING:
		val := p.current.Value
		p.advance()
		return &ast.Literal{Value: val, LineNo: p.current.LineNo}, nil

	case TOKEN_NUMBER:
		val, _ := strconv.ParseFloat(p.current.Value, 64)
		p.advance()
		return &ast.Literal{Value: val, LineNo: p.current.LineNo}, nil

	case TOKEN_BOOL:
		val := p.current.Value == "true"
		p.advance()
		return &ast.Literal{Value: val, LineNo: p.current.LineNo}, nil

	case TOKEN_LPAREN:
		p.advance()
		expr, err := p.parseExpression()
		p.expect(TOKEN_RPAREN)
		return expr, err

	default:
		p.error(fmt.Sprintf("unexpected token: %v", p.current.Type))
		return nil, fmt.Errorf("unexpected token")
	}
}

func (p *Parser) isOperator() bool {
	switch p.current.Type {
	case TOKEN_EQ, TOKEN_NE, TOKEN_LT, TOKEN_GT, TOKEN_LE, TOKEN_GE,
		TOKEN_AND, TOKEN_OR, TOKEN_PLUS, TOKEN_MINUS, TOKEN_MULT, TOKEN_DIV:
		return true
	}
	return false
}

func (p *Parser) parseWhereClause() (*ast.WhereClause, error) {
	where := &ast.WhereClause{}

	p.skipNewlines()
	if p.expect(TOKEN_LBRACE) != nil {
		return nil, fmt.Errorf("expected {")
	}
	p.skipNewlines()

	for p.current.Type != TOKEN_RBRACE && p.current.Type != TOKEN_EOF {
		expr, err := p.parseExpression()
		if err == nil && expr != nil {
			where.Conditions = append(where.Conditions, expr)
		}
		p.skipNewlines()
	}

	if p.expect(TOKEN_RBRACE) != nil {
		return nil, fmt.Errorf("expected }")
	}

	return where, nil
}

func (p *Parser) parseExternal() (*ast.External, error) {
	if p.current.Type != TOKEN_IDENTIFIER {
		p.error("expected external name")
		return nil, fmt.Errorf("expected identifier")
	}

	ext := &ast.External{
		Name:   p.current.Value,
		Input:  make(map[string]string),
		Output: make(map[string]string),
	}
	p.advance()

	p.skipNewlines()
	if p.expect(TOKEN_LBRACE) != nil {
		return nil, fmt.Errorf("expected {")
	}
	p.skipNewlines()

	for p.current.Type != TOKEN_RBRACE && p.current.Type != TOKEN_EOF {
		p.skipNewlines()

		if p.current.Type == TOKEN_INPUT {
			p.advance()
			fields, _ := p.parseFields()
			for _, f := range fields {
				ext.Input[f.Name] = string(f.Type)
			}
		} else if p.current.Type == TOKEN_OUTPUT {
			p.advance()
			fields, _ := p.parseFields()
			for _, f := range fields {
				ext.Output[f.Name] = string(f.Type)
			}
		}
		p.skipNewlines()
	}

	if p.expect(TOKEN_RBRACE) != nil {
		return nil, fmt.Errorf("expected }")
	}

	return ext, nil
}

func (p *Parser) parseModule() (*ast.Module, error) {
	if p.current.Type != TOKEN_IDENTIFIER {
		p.error("expected module name")
		return nil, fmt.Errorf("expected identifier")
	}

	mod := &ast.Module{Name: p.current.Value}
	p.advance()

	p.skipNewlines()
	if p.expect(TOKEN_LBRACE) != nil {
		return nil, fmt.Errorf("expected {")
	}
	p.skipNewlines()

	for p.current.Type != TOKEN_RBRACE && p.current.Type != TOKEN_EOF {
		p.skipNewlines()

		switch p.current.Type {
		case TOKEN_ROLE:
			p.advance()
			block, _ := p.parseBlock()
			mod.Role = block
		case TOKEN_RULE:
			p.advance()
			block, _ := p.parseBlock()
			mod.Rule = block
		case TOKEN_MODIFY:
			p.advance()
			block, _ := p.parseBlock()
			mod.Modify = block
		case TOKEN_EFFECT:
			p.advance()
			block, _ := p.parseBlock()
			mod.Effect = block
		default:
			p.advance()
		}
		p.skipNewlines()
	}

	if p.expect(TOKEN_RBRACE) != nil {
		return nil, fmt.Errorf("expected }")
	}

	return mod, nil
}
