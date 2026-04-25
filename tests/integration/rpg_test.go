package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fookieruntime "github.com/fookiejs/fookie/pkg/runtime"
)

func sys(m map[string]interface{}) map[string]interface{} {
	return fookieruntime.WithSystemInput(m)
}

func TestCreateUser(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	u, err := exec.Create(ctx, "User", sys(map[string]interface{}{
		"email": "alice@example.com", "name": "Alice",
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, u["id"])
}

func TestCreateVillageWithOwner(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	u, err := exec.Create(ctx, "User", sys(map[string]interface{}{
		"email": "builder@example.com", "name": "Builder",
	}))
	require.NoError(t, err)

	v, err := exec.Create(ctx, "Village", sys(map[string]interface{}{
		"owner_id": u["id"],
		"name":     "Hearthhome",
		"food":     25.0,
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, v["id"])
}

func TestUpdateVillage(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := exec.Create(ctx, "User", sys(map[string]interface{}{
		"email": "farmer@example.com", "name": "Farmer",
	}))
	v, _ := exec.Create(ctx, "Village", sys(map[string]interface{}{
		"owner_id": u["id"], "name": "Sprout", "food": 10.0,
	}))
	updated, err := exec.Update(ctx, "Village", v["id"].(string), sys(map[string]interface{}{"food": 40.0}))
	require.NoError(t, err)
	assert.Equal(t, 40.0, updated["food"])
}

func TestDeleteVillage(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := exec.Create(ctx, "User", sys(map[string]interface{}{
		"email": "chief@example.com", "name": "Chief",
	}))
	v, _ := exec.Create(ctx, "Village", sys(map[string]interface{}{
		"owner_id": u["id"], "name": "Ashfall", "food": 5.0,
	}))
	err := exec.Delete(ctx, "Village", v["id"].(string), sys(map[string]interface{}{}))
	require.NoError(t, err)
}
