package handlers

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/fookiejs/fookie/pkg/runtime"
)

const (
	targetWalletCount = 10
	balanceMin        = 1000.0
	balanceMax        = 10000.0
)

func wipeBanking(ctx context.Context, exec *runtime.Executor) error {
	db := exec.DB()
	tables := []string{"wallet_transfer", "atm_transaction", "bank_user", "bank_wallet"}
	for _, t := range tables {
		db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM "%s"`, t))
	}
	return nil
}

func BootstrapBank(ctx context.Context, exec *runtime.Executor) (walletsAdded, usersAdded int, err error) {
	if err := wipeBanking(ctx, exec); err != nil {
		return 0, 0, err
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	names := []string{"Alice", "Bob", "Charlie", "Diana", "Eve", "Frank"}

	for i := 0; i < targetWalletCount; i++ {
		addr := fmt.Sprintf("WLT-%06d-%08x", i, rng.Uint32())
		bal := balanceMin + rng.Float64()*(balanceMax-balanceMin)

		wallet, err := exec.Create(ctx, "BankWallet", runtime.WithSystemInput(map[string]interface{}{
			"address": addr,
			"balance": bal,
			"world_x": 100.0 + rng.Float64()*800.0,
			"world_y": 100.0 + rng.Float64()*800.0,
		}))
		if err != nil {
			continue
		}
		wid, _ := wallet["id"].(string)
		walletsAdded++

		name := names[i%len(names)]
		if i >= len(names) {
			name = fmt.Sprintf("%s #%d", name, i+1)
		}
		_, err = exec.Create(ctx, "BankUser", runtime.WithSystemInput(map[string]interface{}{
			"display_name": name,
			"wallet_id":    wid,
			"world_x":      100.0 + rng.Float64()*800.0,
			"world_y":      100.0 + rng.Float64()*800.0,
		}))
		if err == nil {
			usersAdded++
		}
	}

	return walletsAdded, usersAdded, nil
}
