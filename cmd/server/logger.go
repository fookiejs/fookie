package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fookiejs/fookie/pkg/runtime"
)

func printBankState(ctx context.Context, exec *runtime.Executor) {
	fmt.Println("\n" + strings.Repeat("=", 100))
	fmt.Printf("[%s] Banking snapshot\n", time.Now().Format("15:04:05"))
	fmt.Println(strings.Repeat("=", 100))

	snap, err := fetchDemoStats(ctx, exec.DB())
	if err != nil {
		fmt.Printf("stats: %v\n", err)
	} else {
		fmt.Printf("Totals — wallets: %d | users: %d | transfers: %d | atm_txn: %d\n",
			snap.Wallets, snap.Users, snap.Transfers, snap.AtmTransactions)
		if snap.IntervalSeconds > 0 {
			fmt.Printf("Rate (since last snapshot) — transfers/s: %.1f | atm/s: %.1f (interval %.2fs)\n",
				snap.TransfersPerSec, snap.AtmPerSec, snap.IntervalSeconds)
		}
	}

	wallets, _ := exec.Read(ctx, "BankWallet", map[string]interface{}{})
	users, _ := exec.Read(ctx, "BankUser", map[string]interface{}{})
	transfers, _ := exec.Read(ctx, "WalletTransfer", map[string]interface{}{
		"cursor": map[string]interface{}{"size": 5},
	})
	atm, _ := exec.Read(ctx, "AtmTransaction", map[string]interface{}{
		"cursor": map[string]interface{}{"size": 5},
	})

	var sum float64
	for _, w := range wallets {
		if b, ok := toFloat(w["balance"]); ok {
			sum += b
		}
	}
	fmt.Printf("Live read — wallets: %d | users: %d | total balance: %.2f\n", len(wallets), len(users), sum)
	fmt.Printf("Recent transfers (sample rows): %d | recent ATM (sample): %d\n", len(transfers), len(atm))
	fmt.Println(strings.Repeat("=", 100))
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
