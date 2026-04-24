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

func TestCreateBankWallet(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	w, err := exec.Create(ctx, "BankWallet", sys(map[string]interface{}{
		"address": "WLT-test-1", "balance": 10000.0, "world_x": 100.0, "world_y": 60.0,
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, w["id"])
}

func TestCreateBankUserWithWallet(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	w, err := exec.Create(ctx, "BankWallet", sys(map[string]interface{}{
		"address": "WLT-test-2", "balance": 5000.0, "world_x": 200.0, "world_y": 55.0,
	}))
	require.NoError(t, err)

	u, err := exec.Create(ctx, "BankUser", sys(map[string]interface{}{
		"display_name": "Alice",
		"wallet_id":    w["id"],
		"world_x":      220.0,
		"world_y":      200.0,
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, u["id"])
}

func TestCreateWalletTransfer(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	a, _ := exec.Create(ctx, "BankWallet", sys(map[string]interface{}{
		"address": "WLT-a", "balance": 100.0, "world_x": 50.0, "world_y": 52.0,
	}))
	b, _ := exec.Create(ctx, "BankWallet", sys(map[string]interface{}{
		"address": "WLT-b", "balance": 50.0, "world_x": 300.0, "world_y": 52.0,
	}))

	tr, err := exec.Create(ctx, "WalletTransfer", sys(map[string]interface{}{
		"from_wallet_id":  a["id"],
		"to_wallet_id":    b["id"],
		"amount":          25.0,
		"transfer_status": "completed",
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, tr["id"])
}

func TestCreateAtmTransaction(t *testing.T) {
	exec, _, cleanup := setupDB(t)
	defer cleanup()
	ctx := context.Background()

	w, _ := exec.Create(ctx, "BankWallet", sys(map[string]interface{}{
		"address": "WLT-atm", "balance": 200.0, "world_x": 400.0, "world_y": 52.0,
	}))

	tx, err := exec.Create(ctx, "AtmTransaction", sys(map[string]interface{}{
		"wallet_id":  w["id"],
		"op":         "withdraw",
		"amount":     20.0,
		"txn_status": "completed",
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, tx["id"])
}
