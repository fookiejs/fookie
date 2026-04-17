package fookiegql

import (
	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/graphql-go/graphql"
)

type ExtraField struct {
	Name    string
	GQLType graphql.Input
}

func DetectExtraInputFields(model *ast.Model, op *ast.Operation, schema *ast.Schema) []ExtraField {
	modelFields := make(map[string]bool, len(model.Fields))
	for _, f := range model.Fields {
		modelFields[f.Name] = true
	}

	inputRefs := map[string]bool{}

	walkOpBlocks(op, inputRefs)

	for _, useName := range model.Uses {
		for _, mod := range schema.Modules {
			if mod.Name == useName {
				walkModuleBlocks(mod, inputRefs)
			}
		}
	}

	typeHints := inferTypes(schema, op, model.Uses)

	var extras []ExtraField
	for name := range inputRefs {
		if modelFields[name] || name == "token" {
			continue
		}
		gqlType := graphql.Input(graphql.String)
		if t, ok := typeHints[name]; ok {
			gqlType = t
		}
		extras = append(extras, ExtraField{Name: name, GQLType: gqlType})
	}
	return extras
}

func walkOpBlocks(op *ast.Operation, collector map[string]bool) {
	if op.Role != nil {
		walkBlock(op.Role, collector)
	}
	if op.Rule != nil {
		walkBlock(op.Rule, collector)
	}
	if op.Modify != nil {
		walkBlock(op.Modify, collector)
	}
	if op.Effect != nil {
		walkBlock(op.Effect, collector)
	}
	if op.Compensate != nil {
		walkBlock(op.Compensate, collector)
	}
}

func walkModuleBlocks(mod *ast.Module, collector map[string]bool) {
	if mod.Role != nil {
		walkBlock(mod.Role, collector)
	}
	if mod.Rule != nil {
		walkBlock(mod.Rule, collector)
	}
	if mod.Modify != nil {
		walkBlock(mod.Modify, collector)
	}
	if mod.Effect != nil {
		walkBlock(mod.Effect, collector)
	}
}

func walkBlock(block *ast.Block, collector map[string]bool) {
	for _, stmt := range block.Statements {
		switch s := stmt.(type) {
		case *ast.Assignment:
			walkExpr(s.Value, collector)
		case *ast.ModifyAssignment:
			walkExpr(s.Value, collector)
		case *ast.PredicateExpr:
			walkExpr(s.Expr, collector)
		}
	}
}

func walkExpr(expr ast.Expression, collector map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.FieldAccess:
		if e.Object == "input" && len(e.Fields) >= 1 {
			collector[e.Fields[0]] = true
		}
	case *ast.BinaryOp:
		walkExpr(e.Left, collector)
		walkExpr(e.Right, collector)
	case *ast.UnaryOp:
		walkExpr(e.Right, collector)
	case *ast.ExternalCall:
		for _, paramExpr := range e.Params {
			walkExpr(paramExpr, collector)
		}
	case *ast.BuiltinCall:
		for _, arg := range e.Args {
			walkExpr(arg, collector)
		}
	case *ast.InExpr:
		walkExpr(e.Left, collector)
		for _, v := range e.Values {
			walkExpr(v, collector)
		}
	case *ast.PredicateExpr:
		walkExpr(e.Expr, collector)
	}
}

func inferTypes(schema *ast.Schema, op *ast.Operation, uses []string) map[string]graphql.Input {
	hints := map[string]graphql.Input{}

	extInputMap := map[string]map[string]string{}
	for _, ext := range schema.Externals {
		extInputMap[ext.Name] = ext.Input
	}

	var blocks []*ast.Block
	if op.Role != nil {
		blocks = append(blocks, op.Role)
	}
	if op.Effect != nil {
		blocks = append(blocks, op.Effect)
	}
	if op.Compensate != nil {
		blocks = append(blocks, op.Compensate)
	}
	for _, useName := range uses {
		for _, mod := range schema.Modules {
			if mod.Name == useName {
				if mod.Role != nil {
					blocks = append(blocks, mod.Role)
				}
				if mod.Effect != nil {
					blocks = append(blocks, mod.Effect)
				}
			}
		}
	}

	for _, block := range blocks {
		for _, stmt := range block.Statements {
			inferFromStatement(stmt, extInputMap, hints)
		}
	}
	return hints
}

func inferFromStatement(stmt ast.Statement, extInputMap map[string]map[string]string, hints map[string]graphql.Input) {
	switch s := stmt.(type) {
	case *ast.Assignment:
		inferFromExpr(s.Value, extInputMap, hints)
	case *ast.PredicateExpr:
		inferFromExpr(s.Expr, extInputMap, hints)
	}
}

func inferFromExpr(expr ast.Expression, extInputMap map[string]map[string]string, hints map[string]graphql.Input) {
	ec, ok := expr.(*ast.ExternalCall)
	if !ok {
		return
	}
	inputDef, exists := extInputMap[ec.Name]
	if !exists {
		return
	}
	for paramName, paramExpr := range ec.Params {
		fa, ok := paramExpr.(*ast.FieldAccess)
		if !ok || fa.Object != "input" || len(fa.Fields) < 1 {
			continue
		}
		inputFieldName := fa.Fields[0]
		if typeName, ok := inputDef[paramName]; ok {
			hints[inputFieldName] = mapFieldTypeToInput(ast.FieldType(typeName))
		}
	}
}
