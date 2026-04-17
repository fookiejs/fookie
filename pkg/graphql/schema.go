package fookiegql

import (
	"strings"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/compiler"
	"github.com/graphql-go/graphql"
)

func BuildSchema(schema *ast.Schema) (graphql.Schema, error) {
	strF, numF, boolF, idF := buildScalarFilterInputs()
	whereByModel := map[string]*graphql.InputObject{}
	for _, model := range schema.Models {
		whereByModel[model.Name] = buildModelWhereInput(model, strF, numF, boolF, idF)
	}
	batchPayload := buildBatchPayloadType()

	objectTypes := buildObjectTypes(schema)
	aggregateTypes := buildAggregateTypes(schema)

	queryFields := graphql.Fields{}
	mutationFields := graphql.Fields{}

	for _, model := range schema.Models {
		wt := whereByModel[model.Name]
		if op, ok := model.CRUD["read"]; ok {
			fieldName := lcFirst(model.Name) + "s"
			if isAggregateRead(op) {
				if aggType, ok := aggregateTypes[model.Name]; ok {
					queryFields[fieldName] = &graphql.Field{
						Type: aggType,
						Args: graphql.FieldConfigArgument{
							"where": &graphql.ArgumentConfig{Type: wt},
						},
						Resolve: resolveAggregateRead(model.Name),
					}
				}
			} else {
				if objType, ok := objectTypes[model.Name]; ok {
					queryFields[fieldName] = &graphql.Field{
						Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(objType))),
						Args: graphql.FieldConfigArgument{
							"where": &graphql.ArgumentConfig{Type: wt},
						},
						Resolve: resolveRead(model.Name),
					}
				}
			}
		}

		if op, ok := model.CRUD["create"]; ok {
			inputType := buildCreateInput(model, op, schema)
			if objType, ok := objectTypes[model.Name]; ok {
				mutationFields["create"+model.Name] = &graphql.Field{
					Type: objType,
					Args: graphql.FieldConfigArgument{
						"input": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(inputType),
						},
					},
					Resolve: resolveCreate(model.Name),
				}
			}
		}

		if op, ok := model.CRUD["update"]; ok {
			inputType := buildUpdateInput(model, op, schema)
			if objType, ok := objectTypes[model.Name]; ok {
				mutationFields["update"+model.Name] = &graphql.Field{
					Type: objType,
					Args: graphql.FieldConfigArgument{
						"id": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(graphql.ID),
						},
						"input": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(inputType),
						},
					},
					Resolve: resolveUpdate(model.Name),
				}
				mutationFields["updateMany"+model.Name+"s"] = &graphql.Field{
					Type: batchPayload,
					Args: graphql.FieldConfigArgument{
						"where": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(wt),
						},
						"input": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(inputType),
						},
					},
					Resolve: resolveUpdateMany(model.Name),
				}
			}
		}

		if _, ok := model.CRUD["delete"]; ok {
			mutationFields["delete"+model.Name] = &graphql.Field{
				Type: graphql.NewNonNull(graphql.Boolean),
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.ID),
					},
				},
				Resolve: resolveDelete(model.Name),
			}
			mutationFields["deleteMany"+model.Name+"s"] = &graphql.Field{
				Type: batchPayload,
				Args: graphql.FieldConfigArgument{
					"where": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(wt),
					},
				},
				Resolve: resolveDeleteMany(model.Name),
			}
		}
	}

	config := graphql.SchemaConfig{}
	if len(queryFields) > 0 {
		config.Query = graphql.NewObject(graphql.ObjectConfig{
			Name:   "Query",
			Fields: queryFields,
		})
	}
	if len(mutationFields) > 0 {
		config.Mutation = graphql.NewObject(graphql.ObjectConfig{
			Name:   "Mutation",
			Fields: mutationFields,
		})
	}

	if config.Query == nil {
		config.Query = graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"_empty": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return nil, nil
					},
				},
			},
		})
	}

	return graphql.NewSchema(config)
}

func buildObjectTypes(schema *ast.Schema) map[string]*graphql.Object {
	types := map[string]*graphql.Object{}
	for _, model := range schema.Models {
		fields := systemFields()
		for _, f := range model.Fields {
			snakeKey := compiler.SnakeCase(f.Name)
			fields[f.Name] = &graphql.Field{
				Type:    MapFieldType(f.Type),
				Resolve: modelFieldResolver(f.Name),
			}
			_ = snakeKey
		}
		types[model.Name] = graphql.NewObject(graphql.ObjectConfig{
			Name:   model.Name,
			Fields: fields,
		})
	}
	return types
}

func buildAggregateTypes(schema *ast.Schema) map[string]*graphql.Object {
	types := map[string]*graphql.Object{}
	for _, model := range schema.Models {
		op, ok := model.CRUD["read"]
		if !ok || !isAggregateRead(op) {
			continue
		}
		fields := graphql.Fields{}
		for _, sf := range op.Select {
			alias := sf.Alias
			if alias == "" {
				if af, ok := sf.Expr.(*ast.AggregateFunc); ok {
					alias = af.Fn + strings.Title(strings.Join(af.Field, ""))
				}
			}
			fields[alias] = &graphql.Field{
				Type:    graphql.Float,
				Resolve: fieldResolver(alias),
			}
		}
		types[model.Name] = graphql.NewObject(graphql.ObjectConfig{
			Name:   model.Name + "Aggregate",
			Fields: fields,
		})
	}
	return types
}

func isAggregateRead(op *ast.Operation) bool {
	for _, sf := range op.Select {
		if _, ok := sf.Expr.(*ast.AggregateFunc); ok {
			return true
		}
	}
	return false
}

func buildCreateInput(model *ast.Model, op *ast.Operation, schema *ast.Schema) *graphql.InputObject {
	fields := graphql.InputObjectConfigFieldMap{}
	for _, f := range model.Fields {
		fields[f.Name] = &graphql.InputObjectFieldConfig{
			Type: graphql.NewNonNull(mapFieldTypeToInput(f.Type)),
		}
	}
	extras := DetectExtraInputFields(model, op, schema)
	for _, extra := range extras {
		fields[extra.Name] = &graphql.InputObjectFieldConfig{
			Type: graphql.NewNonNull(extra.GQLType),
		}
	}
	return graphql.NewInputObject(graphql.InputObjectConfig{
		Name:   "Create" + model.Name + "Input",
		Fields: fields,
	})
}

func buildUpdateInput(model *ast.Model, op *ast.Operation, schema *ast.Schema) *graphql.InputObject {
	fields := graphql.InputObjectConfigFieldMap{}
	for _, f := range model.Fields {
		fields[f.Name] = &graphql.InputObjectFieldConfig{
			Type: mapFieldTypeToInput(f.Type),
		}
	}
	fields["status"] = &graphql.InputObjectFieldConfig{Type: graphql.String}
	extras := DetectExtraInputFields(model, op, schema)
	for _, extra := range extras {
		fields[extra.Name] = &graphql.InputObjectFieldConfig{
			Type: extra.GQLType,
		}
	}
	return graphql.NewInputObject(graphql.InputObjectConfig{
		Name:   "Update" + model.Name + "Input",
		Fields: fields,
	})
}

func lcFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
