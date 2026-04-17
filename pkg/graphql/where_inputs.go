package fookiegql

import (
	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/graphql-go/graphql"
)

func buildScalarFilterInputs() (str, num, boolF, id *graphql.InputObject) {
	str = graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "StringFilter",
		Fields: graphql.InputObjectConfigFieldMap{
			"eq":         &graphql.InputObjectFieldConfig{Type: graphql.String},
			"neq":        &graphql.InputObjectFieldConfig{Type: graphql.String},
			"contains":   &graphql.InputObjectFieldConfig{Type: graphql.String},
			"startsWith": &graphql.InputObjectFieldConfig{Type: graphql.String},
			"endsWith":   &graphql.InputObjectFieldConfig{Type: graphql.String},
			"in":         &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.String))},
			"notIn":      &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.String))},
		},
	})
	num = graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "FloatFilter",
		Fields: graphql.InputObjectConfigFieldMap{
			"eq":    &graphql.InputObjectFieldConfig{Type: graphql.Float},
			"neq":   &graphql.InputObjectFieldConfig{Type: graphql.Float},
			"gt":    &graphql.InputObjectFieldConfig{Type: graphql.Float},
			"gte":   &graphql.InputObjectFieldConfig{Type: graphql.Float},
			"lt":    &graphql.InputObjectFieldConfig{Type: graphql.Float},
			"lte":   &graphql.InputObjectFieldConfig{Type: graphql.Float},
			"in":    &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.Float))},
			"notIn": &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.Float))},
		},
	})
	boolF = graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "BooleanFilter",
		Fields: graphql.InputObjectConfigFieldMap{
			"eq": &graphql.InputObjectFieldConfig{Type: graphql.Boolean},
		},
	})
	id = graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "IDFilter",
		Fields: graphql.InputObjectConfigFieldMap{
			"eq":    &graphql.InputObjectFieldConfig{Type: graphql.ID},
			"neq":   &graphql.InputObjectFieldConfig{Type: graphql.ID},
			"in":    &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.ID))},
			"notIn": &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.ID))},
		},
	})
	return str, num, boolF, id
}

func filterInputForField(ft ast.FieldType, str, num, boolF, id *graphql.InputObject) graphql.Input {
	switch ft {
	case ast.TypeNumber:
		return num
	case ast.TypeBoolean:
		return boolF
	case ast.TypeID, ast.TypeRelation, ast.TypeUUID:
		return id
	default:
		return str
	}
}

func buildModelWhereInput(model *ast.Model, str, num, boolF, id *graphql.InputObject) *graphql.InputObject {
	var t *graphql.InputObject
	t = graphql.NewInputObject(graphql.InputObjectConfig{
		Name: model.Name + "WhereInput",
		Fields: (graphql.InputObjectConfigFieldMapThunk)(func() graphql.InputObjectConfigFieldMap {
			fm := graphql.InputObjectConfigFieldMap{
				"AND": &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(t))},
				"OR":  &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(t))},
				"NOT": &graphql.InputObjectFieldConfig{Type: t},
				"id":  &graphql.InputObjectFieldConfig{Type: id},
				"status":    &graphql.InputObjectFieldConfig{Type: str},
				"createdAt": &graphql.InputObjectFieldConfig{Type: str},
				"updatedAt": &graphql.InputObjectFieldConfig{Type: str},
			}
			for _, f := range model.Fields {
				fm[f.Name] = &graphql.InputObjectFieldConfig{
					Type: filterInputForField(f.Type, str, num, boolF, id),
				}
			}
			return fm
		}),
	})
	return t
}

func buildBatchPayloadType() *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name: "BatchPayload",
		Fields: graphql.Fields{
			"count": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		},
	})
}
