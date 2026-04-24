package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type demoStatsSnapshot struct {
	Wallets         int64     `json:"wallets"`
	Users           int64     `json:"users"`
	Transfers       int64     `json:"transfers"`
	AtmTransactions int64     `json:"atm_transactions"`
	TransfersPerSec float64   `json:"transfers_per_sec"`
	AtmPerSec       float64   `json:"atm_per_sec"`
	ObservedAt      time.Time `json:"observed_at"`
	IntervalSeconds float64   `json:"interval_seconds"`
}

var demoStatsMu sync.Mutex
var demoStatsPrev demoStatsSnapshot

func countActiveRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var n int64
	q := `SELECT COUNT(*) FROM "` + table + `" WHERE deleted_at IS NULL`
	err := db.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}

func fetchDemoStats(ctx context.Context, db *sql.DB) (demoStatsSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var out demoStatsSnapshot
	var err error
	out.Wallets, err = countActiveRows(ctx, db, "bank_wallet")
	if err != nil {
		return out, err
	}
	out.Users, err = countActiveRows(ctx, db, "bank_user")
	if err != nil {
		return out, err
	}
	out.Transfers, err = countActiveRows(ctx, db, "wallet_transfer")
	if err != nil {
		return out, err
	}
	out.AtmTransactions, err = countActiveRows(ctx, db, "atm_transaction")
	if err != nil {
		return out, err
	}
	out.ObservedAt = time.Now()

	demoStatsMu.Lock()
	prev := demoStatsPrev
	dt := out.ObservedAt.Sub(prev.ObservedAt).Seconds()
	if prev.ObservedAt.IsZero() {
		dt = 0
	}
	out.IntervalSeconds = dt
	if dt > 0.001 {
		out.TransfersPerSec = float64(out.Transfers-prev.Transfers) / dt
		out.AtmPerSec = float64(out.AtmTransactions-prev.AtmTransactions) / dt
	}
	demoStatsPrev = out
	demoStatsMu.Unlock()

	return out, nil
}

func handleDemoStats(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snap, err := fetchDemoStats(r.Context(), db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(snap)
	}
}
