package handlers

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/fookiejs/fookie/pkg/runtime"
)

const transferConcurrency = 100

func toF64(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case []byte:
		f, _ := strconv.ParseFloat(string(x), 64)
		return f
	}
	return 0
}

func runTransferBatch(ctx context.Context, _ map[string]interface{}, store runtime.Store) (map[string]interface{}, error) {
	wallets, err := store.Read(ctx, "BankWallet", map[string]interface{}{})
	if err != nil || len(wallets) < 2 {
		return map[string]interface{}{"batch_size": 0, "__nocache": true}, nil
	}

	type walletSnap struct {
		id      string
		balance float64
	}
	snaps := make([]walletSnap, 0, len(wallets))
	for _, w := range wallets {
		id, _ := w["id"].(string)
		bal := toF64(w["balance"])
		if id != "" && bal > 1 {
			snaps = append(snaps, walletSnap{id: id, balance: bal})
		}
	}
	if len(snaps) < 2 {
		return map[string]interface{}{"batch_size": 0, "__nocache": true}, nil
	}

	var created int64
	var wg sync.WaitGroup
	rng := rand.New(rand.NewSource(rand.Int63()))

	for k := 0; k < transferConcurrency; k++ {

		n := len(snaps)
		i := rng.Intn(n)
		j := rng.Intn(n - 1)
		if j >= i {
			j++
		}
		from := snaps[i]
		to := snaps[j]
		amount := 1.0 + rng.Float64()*float64(int(from.balance/20+1))
		if amount > from.balance/2 {
			amount = from.balance / 2
		}
		if amount < 1 {
			amount = 1
		}

		wg.Add(1)
		go func(fromID, toID string, amt float64) {
			defer wg.Done()
			_, cerr := store.Create(ctx, "WalletTransfer", map[string]interface{}{
				"from_wallet_id":  fromID,
				"to_wallet_id":    toID,
				"amount":          amt,
				"transfer_status": "done",
			})
			if cerr == nil {
				atomic.AddInt64(&created, 1)
			}
		}(from.id, to.id, amount)
	}
	wg.Wait()

	return map[string]interface{}{"batch_size": int(created), "__nocache": true}, nil
}

func growUserbase(ctx context.Context, _ map[string]interface{}, store runtime.Store) (map[string]interface{}, error) {

	if rand.Float64() > 0.2 {
		return map[string]interface{}{"users_added": 0, "__nocache": true}, nil
	}

	rng := rand.New(rand.NewSource(rand.Int63()))
	addr := fmt.Sprintf("WLT-NEW-%08x", rng.Uint32())
	balance := 500.0 + rng.Float64()*4500.0

	wallet, err := store.Create(ctx, "BankWallet", map[string]interface{}{
		"address": addr,
		"balance": balance,
		"world_x": 50.0 + rng.Float64()*900.0,
		"world_y": 50.0 + rng.Float64()*900.0,
	})
	if err != nil {
		return map[string]interface{}{"users_added": 0}, err
	}
	wid, _ := wallet["id"].(string)

	uid := rng.Uint32()
	_, err = store.Create(ctx, "BankUser", map[string]interface{}{
		"display_name": fmt.Sprintf("user_new_%d", uid),
		"wallet_id":    wid,
		"world_x":      50.0 + rng.Float64()*900.0,
		"world_y":      50.0 + rng.Float64()*900.0,
	})
	if err != nil {
		return map[string]interface{}{"users_added": 0}, err
	}
	return map[string]interface{}{"users_added": 1, "__nocache": true}, nil
}

func runAtmActivity(ctx context.Context, _ map[string]interface{}, store runtime.Store) (map[string]interface{}, error) {
	wallets, err := store.Read(ctx, "BankWallet", map[string]interface{}{})
	if err != nil || len(wallets) == 0 {
		return map[string]interface{}{"ops": 0, "__nocache": true}, nil
	}

	w := wallets[rand.Intn(len(wallets))]
	wid, _ := w["id"].(string)
	bal := toF64(w["balance"])

	op := "deposit"
	amount := 50.0 + rand.Float64()*200.0
	if rand.Float64() > 0.5 && bal > 100 {
		op = "withdrawal"
		if amount > bal {
			amount = bal / 2
		}
	}

	_, err = store.Create(ctx, "AtmTransaction", map[string]interface{}{
		"wallet_id":  wid,
		"op":         op,
		"amount":     amount,
		"txn_status": "done",
	})
	if err != nil {
		return map[string]interface{}{"ops": 0, "__nocache": true}, err
	}
	return map[string]interface{}{"ops": 1, "__nocache": true}, nil
}
