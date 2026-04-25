package fookiegql

import (
	"strings"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/events"
	"github.com/graphql-go/graphql"
)

func BuildSchema(schema *ast.Schema, eventBus *events.Bus, roomBus *events.RoomBus) (graphql.Schema, error) {
	strF, numF, boolF, idF := buildScalarFilterInputs()
	filterByModel := map[string]*graphql.InputObject{}
	for _, model := range schema.Models {
		filterByModel[model.Name] = buildModelFilterInput(model, strF, numF, boolF, idF)
	}
	batchPayload := buildBatchPayloadType()
	cursorInput := buildCursorInputType()

	objectTypes := buildObjectTypes(schema, filterByModel, cursorInput)
	aggregateTypes := buildAggregateTypes(schema)

	queryFields := graphql.Fields{}
	mutationFields := graphql.Fields{}

	for _, model := range schema.Models {
		wt := filterByModel[model.Name]
		modelSnake := toSnake(model.Name)

		if op, ok := model.CRUD["read"]; ok {
			fieldName := "all_" + modelSnake
			if isAggregateRead(op) {
				if aggType, ok := aggregateTypes[model.Name]; ok {
					queryFields[fieldName] = &graphql.Field{
						Type: aggType,
						Args: graphql.FieldConfigArgument{
							"filter": &graphql.ArgumentConfig{Type: wt},
						},
						Resolve: resolveAggregateRead(model.Name),
					}
				}
			} else {
				if objType, ok := objectTypes[model.Name]; ok {
					queryFields[fieldName] = &graphql.Field{
						Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(objType))),
						Args: graphql.FieldConfigArgument{
							"filter": &graphql.ArgumentConfig{Type: wt},
							"cursor": &graphql.ArgumentConfig{Type: cursorInput},
						},
						Resolve: resolveRead(model.Name),
					}
				}
			}
		}

		if op, ok := model.CRUD["create"]; ok {
			bodyType := buildCreateBody(model, op, schema)
			if objType, ok := objectTypes[model.Name]; ok {
				mutationFields["create_"+modelSnake] = &graphql.Field{
					Type: objType,
					Args: graphql.FieldConfigArgument{
						"body": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(bodyType),
						},
					},
					Resolve: resolveCreate(model.Name),
				}
			}
		}

		if op, ok := model.CRUD["update"]; ok {
			bodyType := buildUpdateBody(model, op, schema)
			if objType, ok := objectTypes[model.Name]; ok {
				mutationFields["update_"+modelSnake] = &graphql.Field{
					Type: objType,
					Args: graphql.FieldConfigArgument{
						"id": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(graphql.ID),
						},
						"body": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(bodyType),
						},
					},
					Resolve: resolveUpdate(model.Name),
				}
				mutationFields["update_many_"+modelSnake] = &graphql.Field{
					Type: batchPayload,
					Args: graphql.FieldConfigArgument{
						"filter": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(wt),
						},
						"body": &graphql.ArgumentConfig{
							Type: graphql.NewNonNull(bodyType),
						},
					},
					Resolve: resolveUpdateMany(model.Name),
				}
			}
		}

		if _, ok := model.CRUD["delete"]; ok {
			mutationFields["delete_"+modelSnake] = &graphql.Field{
				Type: graphql.NewNonNull(graphql.Boolean),
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.ID),
					},
				},
				Resolve: resolveDelete(model.Name),
			}
			mutationFields["delete_many_"+modelSnake] = &graphql.Field{
				Type: batchPayload,
				Args: graphql.FieldConfigArgument{
					"filter": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(wt),
					},
				},
				Resolve: resolveDeleteMany(model.Name),
			}
			// restore_<model> automatically available for every soft-deletable model.
			mutationFields["restore_"+modelSnake] = &graphql.Field{
				Type: graphql.NewNonNull(graphql.Boolean),
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.ID),
					},
				},
				Resolve: resolveRestore(model.Name),
			}
		}

		// Aggregate operations: sum, count, avg, min, max
		for opType, op := range model.CRUD {
			if opType == "read" || opType == "create" || opType == "update" || opType == "delete" {
				continue
			}

			// Build field name: sum_{model}_{field}, count_{model}, etc.
			var fieldName string
			if opType == "count" {
				fieldName = opType + "_" + modelSnake
			} else {
				fieldName = opType + "_" + modelSnake + "_" + toSnake(op.Field)
			}

			// Create resolver based on operation type
			var resolverFunc graphql.FieldResolveFn
			switch opType {
			case "sum":
				resolverFunc = resolveSum(model.Name, op.Field)
			case "count":
				resolverFunc = resolveCount(model.Name)
			case "avg":
				resolverFunc = resolveAvg(model.Name, op.Field)
			case "min":
				resolverFunc = resolveMin(model.Name, op.Field)
			case "max":
				resolverFunc = resolveMax(model.Name, op.Field)
			case "stddev":
				resolverFunc = resolveStddev(model.Name, op.Field)
			case "variance":
				resolverFunc = resolveVariance(model.Name, op.Field)
			default:
				continue
			}

			queryFields[fieldName] = &graphql.Field{
				Type: graphql.Float,
				Args: graphql.FieldConfigArgument{
					"filter": &graphql.ArgumentConfig{Type: wt},
				},
				Resolve: resolverFunc,
			}
		}
	}

	// ── Connection types (keyset pagination) ──────────────────────────────
	pageInfoType := graphql.NewObject(graphql.ObjectConfig{
		Name: "PageInfo",
		Fields: graphql.Fields{
			"hasNextPage": &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
			"hasPrevPage": &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
			"startCursor": &graphql.Field{Type: graphql.String},
			"endCursor":   &graphql.Field{Type: graphql.String},
			"totalCount":  &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		},
	})

	connectionInput := graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "ConnectionInput",
		Fields: graphql.InputObjectConfigFieldMap{
			"first": &graphql.InputObjectFieldConfig{Type: graphql.Int, DefaultValue: 20},
			"after": &graphql.InputObjectFieldConfig{Type: graphql.String},
		},
	})

	for _, model := range schema.Models {
		if _, ok := model.CRUD["read"]; !ok {
			continue
		}
		objType, ok := objectTypes[model.Name]
		if !ok {
			continue
		}
		wt := filterByModel[model.Name]
		modelSnake := toSnake(model.Name)

		// Capture loop variable for closures
		capturedModel := model

		edgeType := graphql.NewObject(graphql.ObjectConfig{
			Name: model.Name + "Edge",
			Fields: graphql.Fields{
				"node":   &graphql.Field{Type: graphql.NewNonNull(objType)},
				"cursor": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			},
		})
		connType := graphql.NewObject(graphql.ObjectConfig{
			Name: model.Name + "Connection",
			Fields: graphql.Fields{
				"edges":      &graphql.Field{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(edgeType)))},
				"pageInfo":   &graphql.Field{Type: graphql.NewNonNull(pageInfoType)},
				"totalCount": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
			},
		})

		queryFields["list_"+modelSnake] = &graphql.Field{
			Type: graphql.NewNonNull(connType),
			Args: graphql.FieldConfigArgument{
				"filter":     &graphql.ArgumentConfig{Type: wt},
				"connection": &graphql.ArgumentConfig{Type: connectionInput},
			},
			Resolve: resolveListConnection(capturedModel.Name),
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

	attachSubscriptions(&config, eventBus, roomBus)

	return graphql.NewSchema(config)
}

func buildObjectTypes(schema *ast.Schema, filterByModel map[string]*graphql.InputObject, cursorInput *graphql.InputObject) map[string]*graphql.Object {
	types := map[string]*graphql.Object{}

	for _, model := range schema.Models {
		m := model
		types[m.Name] = graphql.NewObject(graphql.ObjectConfig{
			Name: m.Name,
			Fields: (graphql.FieldsThunk)(func() graphql.Fields {
				fields := systemFields()

				for _, f := range m.Fields {
					f := f
					if f.Type == ast.TypeRelation && f.Relation != nil {

						scalarKey := f.Name + "_id"
						relName := *f.Relation
						fields[scalarKey] = &graphql.Field{
							Type:    graphql.ID,
							Resolve: fieldResolver(scalarKey),
						}
						if relType, ok := types[relName]; ok {
							fields[f.Name] = &graphql.Field{
								Type:    relType,
								Resolve: relatedObjectResolver(scalarKey, relName),
							}
						}
					} else {
						fields[f.Name] = &graphql.Field{
							Type:    MapFieldType(f.Type),
							Resolve: modelFieldResolver(f.Name),
						}
					}
				}

				for _, other := range schema.Models {
					if other.Name == m.Name {
						continue
					}
					if _, hasRead := other.CRUD["read"]; !hasRead {
						continue
					}
					for _, of := range other.Fields {
						if of.Type == ast.TypeRelation && of.Relation != nil && *of.Relation == m.Name {
							of := of
							other := other
							childType, ok := types[other.Name]
							if !ok {
								continue
							}
							hasManyName := "all_" + toSnake(other.Name)

							if _, exists := fields[hasManyName]; exists {
								hasManyName = "all_" + toSnake(other.Name) + "_list"
							}
							wt := filterByModel[other.Name]
							argCfg := graphql.FieldConfigArgument{
								"cursor": &graphql.ArgumentConfig{Type: cursorInput},
							}
							if wt != nil {
								argCfg["filter"] = &graphql.ArgumentConfig{Type: wt}
							}
							fields[hasManyName] = &graphql.Field{
								Type:    graphql.NewList(graphql.NewNonNull(childType)),
								Args:    argCfg,
								Resolve: hasManyResolver(other.Name, of.Name+"_id"),
							}

							// Add nested aggregate fields for child's aggregates
							childOtherSnake := toSnake(other.Name)
							for aggOpType, aggOp := range other.CRUD {
								if aggOpType == "read" || aggOpType == "create" || aggOpType == "update" || aggOpType == "delete" {
									continue
								}

								// Build aggregate field name
								var aggFieldName string
								if aggOpType == "count" {
									aggFieldName = aggOpType + "_" + childOtherSnake
								} else {
									aggFieldName = aggOpType + "_" + childOtherSnake + "_" + toSnake(aggOp.Field)
								}

								// Create resolver based on operation type
								var resolverFunc graphql.FieldResolveFn
								switch aggOpType {
								case "sum":
									resolverFunc = nestedSumResolver(other.Name, aggOp.Field, of.Name+"_id")
								case "count":
									resolverFunc = nestedCountResolver(other.Name, of.Name+"_id")
								case "avg":
									resolverFunc = nestedAvgResolver(other.Name, aggOp.Field, of.Name+"_id")
								case "min":
									resolverFunc = nestedMinResolver(other.Name, aggOp.Field, of.Name+"_id")
								case "max":
									resolverFunc = nestedMaxResolver(other.Name, aggOp.Field, of.Name+"_id")
								case "stddev":
									resolverFunc = nestedStddevResolver(other.Name, aggOp.Field, of.Name+"_id")
								case "variance":
									resolverFunc = nestedVarianceResolver(other.Name, aggOp.Field, of.Name+"_id")
								default:
									continue
								}

								fields[aggFieldName] = &graphql.Field{
									Type: graphql.Float,
									Args: graphql.FieldConfigArgument{
										"filter": &graphql.ArgumentConfig{Type: wt},
									},
									Resolve: resolverFunc,
								}
							}

							break
						}
					}
				}

				return fields
			}),
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

func inputFieldName(f *ast.Field) string {
	if f.Type == ast.TypeRelation {
		return f.Name + "_id"
	}
	return f.Name
}

func buildCreateBody(model *ast.Model, op *ast.Operation, schema *ast.Schema) *graphql.InputObject {
	fields := graphql.InputObjectConfigFieldMap{}
	for _, f := range model.Fields {
		fields[inputFieldName(f)] = &graphql.InputObjectFieldConfig{
			Type: graphql.NewNonNull(mapFieldTypeToInput(f.Type)),
		}
	}
	extras := DetectExtraInputFields(model, op, schema)
	for _, extra := range extras {
		fields[extra.Name] = &graphql.InputObjectFieldConfig{
			Type: graphql.NewNonNull(extra.GQLType),
		}
	}

	fields["admin_key"] = &graphql.InputObjectFieldConfig{Type: graphql.String}
	return graphql.NewInputObject(graphql.InputObjectConfig{
		Name:   "Create" + model.Name + "Body",
		Fields: fields,
	})
}

func buildUpdateBody(model *ast.Model, op *ast.Operation, schema *ast.Schema) *graphql.InputObject {
	fields := graphql.InputObjectConfigFieldMap{}
	for _, f := range model.Fields {
		fields[inputFieldName(f)] = &graphql.InputObjectFieldConfig{
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
	fields["admin_key"] = &graphql.InputObjectFieldConfig{Type: graphql.String}
	return graphql.NewInputObject(graphql.InputObjectConfig{
		Name:   "Update" + model.Name + "Body",
		Fields: fields,
	})
}

func lcFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func buildCursorInputType() *graphql.InputObject {
	return graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "CursorInput",
		Fields: graphql.InputObjectConfigFieldMap{
			"size":  &graphql.InputObjectFieldConfig{Type: graphql.Int},
			"after": &graphql.InputObjectFieldConfig{Type: graphql.Int},
		},
	})
}
