package fookiegql

import (
	"errors"
	"fmt"

	langast "github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
)

func operationKind(doc string, operationName string) (string, error) {
	astDoc, err := parser.Parse(parser.ParseParams{Source: source.NewSource(&source.Source{Name: "request", Body: []byte(doc)})})
	if err != nil {
		return "", err
	}
	var ops []*langast.OperationDefinition
	for _, d := range astDoc.Definitions {
		if o, ok := d.(*langast.OperationDefinition); ok {
			ops = append(ops, o)
		}
	}
	if len(ops) == 0 {
		return "", errors.New("no GraphQL operation")
	}
	if operationName != "" {
		for _, o := range ops {
			if o.Name != nil && o.Name.Value == operationName {
				return o.Operation, nil
			}
		}
		return "", fmt.Errorf("unknown operation name %q", operationName)
	}
	if len(ops) != 1 {
		return "", errors.New("operationName is required when the document contains multiple operations")
	}
	return ops[0].Operation, nil
}

func IsSubscriptionOperation(doc string, operationName string) (bool, error) {
	k, err := operationKind(doc, operationName)
	if err != nil {
		return false, err
	}
	return k == langast.OperationTypeSubscription, nil
}
