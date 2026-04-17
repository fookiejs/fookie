package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fookiejs/fookie/pkg/ast"
	fookiegql "github.com/fookiejs/fookie/pkg/graphql"
	"github.com/fookiejs/fookie/pkg/parser"
	"github.com/graphql-go/graphql"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

func parseSchema(t *testing.T, name string) *ast.Schema {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(projectRoot(), "schemas", name+".fql"))
	require.NoError(t, err)
	lexer := parser.NewLexer(string(content))
	tokens := lexer.Tokenize()
	p := parser.NewParser(tokens)
	schema, err := p.Parse()
	require.NoError(t, err)
	return schema
}

func TestGraphQL_TypeMapping(t *testing.T) {
	cases := []struct {
		fslType ast.FieldType
		gqlType graphql.Output
	}{
		{ast.TypeString, graphql.String},
		{ast.TypeNumber, graphql.Float},
		{ast.TypeBoolean, graphql.Boolean},
		{ast.TypeID, graphql.ID},
		{ast.TypeRelation, graphql.ID},
		{ast.TypeEmail, graphql.String},
		{ast.TypeURL, graphql.String},
		{ast.TypePhone, graphql.String},
		{ast.TypeUUID, graphql.String},
		{ast.TypeIBAN, graphql.String},
		{ast.TypeIPAddress, graphql.String},
		{ast.TypeColor, graphql.String},
		{ast.TypeCurrency, graphql.String},
		{ast.TypeLocale, graphql.String},
		{ast.TypeDate, graphql.String},
		{ast.TypeTimestamp, graphql.String},
		{ast.TypeJSON, graphql.String},
		{ast.TypeCoordinate, graphql.String},
	}

	for _, tc := range cases {
		t.Run(string(tc.fslType), func(t *testing.T) {
			result := fookiegql.MapFieldType(tc.fslType)
			assert.Equal(t, tc.gqlType, result)
		})
	}
}

func TestGraphQL_BuildSchema_WalletTransfer(t *testing.T) {
	schema := parseSchema(t, "wallet_transfer")
	gqlSchema, err := fookiegql.BuildSchema(schema)
	require.NoError(t, err)

	queryType := gqlSchema.QueryType()
	require.NotNil(t, queryType)

	queryFields := queryType.Fields()
	assert.Contains(t, queryFields, "users")
	assert.Contains(t, queryFields, "wallets")
	assert.Contains(t, queryFields, "transactions")

	mutationType := gqlSchema.MutationType()
	require.NotNil(t, mutationType)

	mutFields := mutationType.Fields()
	assert.Contains(t, mutFields, "createUser")
	assert.Contains(t, mutFields, "createWallet")
	assert.Contains(t, mutFields, "createTransaction")
	assert.Contains(t, mutFields, "updateUser")
	assert.Contains(t, mutFields, "updateWallet")
	assert.Contains(t, mutFields, "updateTransaction")
	assert.Contains(t, mutFields, "updateManyUsers")
	assert.Contains(t, mutFields, "updateManyWallets")

	assert.NotContains(t, mutFields, "deleteUser")
	assert.NotContains(t, mutFields, "deleteWallet")
	assert.NotContains(t, mutFields, "deleteTransaction")
}

func TestGraphQL_BuildSchema_PaymentProcessing(t *testing.T) {
	schema := parseSchema(t, "payment_processing")
	gqlSchema, err := fookiegql.BuildSchema(schema)
	require.NoError(t, err)

	mutFields := gqlSchema.MutationType().Fields()
	assert.Contains(t, mutFields, "createMerchant")
	assert.Contains(t, mutFields, "createPaymentMethod")
	assert.Contains(t, mutFields, "createPaymentRecord")
	assert.Contains(t, mutFields, "createRefund")

	assert.NotContains(t, mutFields, "updateMerchant")
}

func TestGraphQL_BuildSchema_UserOnboarding(t *testing.T) {
	schema := parseSchema(t, "user_onboarding")
	gqlSchema, err := fookiegql.BuildSchema(schema)
	require.NoError(t, err)

	mutFields := gqlSchema.MutationType().Fields()
	assert.Contains(t, mutFields, "createAccount")
	assert.Contains(t, mutFields, "createProfile")
	assert.Contains(t, mutFields, "createKycVerification")
	assert.Contains(t, mutFields, "createVerificationLog")
	assert.Contains(t, mutFields, "updateKycVerification")
}

func TestGraphQL_ExtraInputFields_Transaction(t *testing.T) {
	schema := parseSchema(t, "wallet_transfer")

	var txModel *ast.Model
	for _, m := range schema.Models {
		if m.Name == "Transaction" {
			txModel = m
			break
		}
	}
	require.NotNil(t, txModel)

	createOp := txModel.CRUD["create"]
	require.NotNil(t, createOp)

	extras := fookiegql.DetectExtraInputFields(txModel, createOp, schema)

	extraNames := map[string]bool{}
	for _, e := range extras {
		extraNames[e.Name] = true
	}

	assert.True(t, extraNames["recipientEmail"], "recipientEmail should be detected as extra input")
	assert.False(t, extraNames["token"], "token should be excluded")
	assert.False(t, extraNames["amount"], "amount is a model field, should not be extra")
	assert.False(t, extraNames["fromWalletId"], "fromWalletId is a model field")
}

func TestGraphQL_ExtraInputFields_PaymentRecord(t *testing.T) {
	schema := parseSchema(t, "payment_processing")

	var prModel *ast.Model
	for _, m := range schema.Models {
		if m.Name == "PaymentRecord" {
			prModel = m
			break
		}
	}
	require.NotNil(t, prModel)

	createOp := prModel.CRUD["create"]
	require.NotNil(t, createOp)

	extras := fookiegql.DetectExtraInputFields(prModel, createOp, schema)

	extraNames := map[string]bool{}
	for _, e := range extras {
		extraNames[e.Name] = true
	}

	assert.True(t, extraNames["apiKey"], "apiKey should be detected (used in role block)")
	assert.True(t, extraNames["webhookUrl"], "webhookUrl should be detected (used in effect block)")
	assert.False(t, extraNames["cardToken"], "cardToken is a model field")
	assert.False(t, extraNames["amount"], "amount is a model field")
}

func TestGraphQL_AggregateReadDetection(t *testing.T) {
	schema := parseSchema(t, "wallet_transfer")

	for _, model := range schema.Models {
		op, ok := model.CRUD["read"]
		if !ok {
			continue
		}
		switch model.Name {
		case "Transaction":
			gqlSchema, err := fookiegql.BuildSchema(schema)
			require.NoError(t, err)
			qf := gqlSchema.QueryType().Fields()
			txField := qf["transactions"]
			require.NotNil(t, txField)
			assert.NotEqual(t, "NonNull", txField.Type.Name())
		case "User":
			assert.Empty(t, op.Select)
		}
	}
}

func TestGraphQL_ReadWhereArg(t *testing.T) {
	schema := parseSchema(t, "wallet_transfer")
	gqlSchema, err := fookiegql.BuildSchema(schema)
	require.NoError(t, err)

	qf := gqlSchema.QueryType().Fields()
	users := qf["users"]
	require.NotNil(t, users)
	var hasWhere bool
	for _, a := range users.Args {
		if a.Name() == "where" {
			hasWhere = true
			break
		}
	}
	assert.True(t, hasWhere, "users query should accept optional where argument")
}

func TestGraphQL_Introspection(t *testing.T) {
	schema := parseSchema(t, "wallet_transfer")
	gqlSchema, err := fookiegql.BuildSchema(schema)
	require.NoError(t, err)

	result := graphql.Do(graphql.Params{
		Schema:        gqlSchema,
		RequestString: `{ __schema { queryType { name } mutationType { name } } }`,
	})
	require.Empty(t, result.Errors)

	data := result.Data.(map[string]interface{})
	schemaData := data["__schema"].(map[string]interface{})
	assert.Equal(t, "Query", schemaData["queryType"].(map[string]interface{})["name"])
	assert.Equal(t, "Mutation", schemaData["mutationType"].(map[string]interface{})["name"])
}
