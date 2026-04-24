package tests

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fookiejs/fookie/pkg/ast"
	"github.com/fookiejs/fookie/pkg/events"
	fookiegql "github.com/fookiejs/fookie/pkg/graphql"
	"github.com/fookiejs/fookie/pkg/parser"
	schemamerge "github.com/fookiejs/fookie/pkg/schema"
	"github.com/graphql-go/graphql"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

func parseDemoSchema(t *testing.T) *ast.Schema {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(projectRoot(), "demo", "schema.fql"))
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

func TestGraphQL_BuildSchema_BankingDemo(t *testing.T) {
	schema := parseDemoSchema(t)
	gqlSchema, err := fookiegql.BuildSchema(schema, nil, nil)
	require.NoError(t, err)

	queryFields := gqlSchema.QueryType().Fields()

	assert.Contains(t, queryFields, "all_bank_wallet")
	assert.Contains(t, queryFields, "all_bank_user")
	assert.Contains(t, queryFields, "all_wallet_transfer")
	assert.Contains(t, queryFields, "all_atm_transaction")

	mutFields := gqlSchema.MutationType().Fields()

	assert.Contains(t, mutFields, "create_bank_wallet")
	assert.Contains(t, mutFields, "create_bank_user")
	assert.Contains(t, mutFields, "create_wallet_transfer")
	assert.Contains(t, mutFields, "create_atm_transaction")

	assert.Contains(t, mutFields, "update_bank_wallet")
	assert.Contains(t, mutFields, "update_bank_user")
	assert.Contains(t, mutFields, "update_wallet_transfer")
	assert.Contains(t, mutFields, "update_atm_transaction")

	assert.Contains(t, mutFields, "delete_bank_wallet")
	assert.Contains(t, mutFields, "delete_bank_user")
	assert.Contains(t, mutFields, "delete_wallet_transfer")
	assert.Contains(t, mutFields, "delete_atm_transaction")
}

func TestGraphQL_RelationFields_BankUser(t *testing.T) {
	schema := parseDemoSchema(t)
	gqlSchema, err := fookiegql.BuildSchema(schema, nil, nil)
	require.NoError(t, err)

	queryFields := gqlSchema.QueryType().Fields()
	userField, ok := queryFields["all_bank_user"]
	require.True(t, ok)

	userObj := unwrapObject(userField.Type)
	require.NotNil(t, userObj)

	userFields := userObj.Fields()
	assert.Contains(t, userFields, "wallet_id")
	assert.Contains(t, userFields, "wallet")
}

func TestGraphQL_FilterArg(t *testing.T) {
	schema := parseDemoSchema(t)
	gqlSchema, err := fookiegql.BuildSchema(schema, nil, nil)
	require.NoError(t, err)

	queryFields := gqlSchema.QueryType().Fields()
	for _, name := range []string{"all_bank_user", "all_bank_wallet", "all_wallet_transfer"} {
		field, ok := queryFields[name]
		require.True(t, ok, "query field %s not found", name)
		hasFilter := false
		for _, a := range field.Args {
			if a.Name() == "filter" {
				hasFilter = true
				break
			}
		}
		assert.True(t, hasFilter, "%s should accept optional filter argument", name)
	}
}

func unwrapObject(t graphql.Output) *graphql.Object {
	switch tt := t.(type) {
	case *graphql.NonNull:
		return unwrapObject(tt.OfType)
	case *graphql.List:
		return unwrapObject(tt.OfType)
	case *graphql.Object:
		return tt
	}
	return nil
}

func TestGraphQL_Introspection(t *testing.T) {
	schema := parseDemoSchema(t)
	gqlSchema, err := fookiegql.BuildSchema(schema, nil, nil)
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

func TestGraphQL_Introspection_SubscriptionWithRoomBus(t *testing.T) {
	schema := parseDemoSchema(t)
	require.NoError(t, schemamerge.MergeBuiltinRooms(schema))
	eb := events.NewBus()
	rb := events.NewRoomBus()
	gqlSchema, err := fookiegql.BuildSchema(schema, eb, rb)
	require.NoError(t, err)

	result := graphql.Do(graphql.Params{
		Schema:        gqlSchema,
		RequestString: `{ __schema { subscriptionType { name } } }`,
	})
	require.Empty(t, result.Errors)
	data := result.Data.(map[string]interface{})
	st := data["__schema"].(map[string]interface{})["subscriptionType"].(map[string]interface{})
	assert.Equal(t, "Subscription", st["name"])
}

func TestGraphQL_RoomSubscription_stream(t *testing.T) {
	schema := parseDemoSchema(t)
	require.NoError(t, schemamerge.MergeBuiltinRooms(schema))
	eb := events.NewBus()
	rb := events.NewRoomBus()
	gqlSchema, err := fookiegql.BuildSchema(schema, eb, rb)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		time.Sleep(40 * time.Millisecond)
		rb.Publish("r1", map[string]interface{}{
			"room_id": "r1",
			"method":  "updated",
			"model":   "Room",
			"payload": map[string]interface{}{
				"query": "{ all_room { id } }",
				"body":  `{"name":"Lobby"}`,
			},
		})
	}()

	ch := graphql.Subscribe(graphql.Params{
		Context:       ctx,
		Schema:        gqlSchema,
		RequestString: `subscription { room_graphql_message(room_id: "r1") { room_id method model payload { query body } } }`,
	})
	var saw bool
	for res := range ch {
		require.Empty(t, res.Errors, "%+v", res.Errors)
		if res.Data != nil {
			saw = true
			break
		}
	}
	require.True(t, saw, "expected at least one subscription payload")
}

func TestGraphQL_EntityEvents_subscription(t *testing.T) {
	schema := parseDemoSchema(t)
	eb := events.NewBus()
	gqlSchema, err := fookiegql.BuildSchema(schema, eb, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		time.Sleep(40 * time.Millisecond)
		eb.PublishCRUD("created", "WalletTransfer", "id-1", map[string]interface{}{"x": 1})
	}()

	ch := graphql.Subscribe(graphql.Params{
		Context:       ctx,
		Schema:        gqlSchema,
		RequestString: `subscription { entity_events(model: "WalletTransfer") { op model id ts } }`,
	})
	var saw bool
	for res := range ch {
		require.Empty(t, res.Errors, "%+v", res.Errors)
		if res.Data != nil {
			saw = true
			break
		}
	}
	require.True(t, saw)
}
