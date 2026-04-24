package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type IdempotencyStore struct {
	db *sql.DB
}

func NewIdempotencyStore(db *sql.DB) *IdempotencyStore {
	return &IdempotencyStore{db: db}
}

func (s *IdempotencyStore) CreateTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS idempotency_keys (
			key        TEXT PRIMARY KEY,
			status     TEXT NOT NULL DEFAULT 'processing',
			response   TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
		)
	`)
	return err
}

type IdemResult struct {
	Replayed bool
	Response []byte
}

func (s *IdempotencyStore) Begin(ctx context.Context, key string) (*IdemResult, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO idempotency_keys (key) VALUES ($1) ON CONFLICT (key) DO NOTHING`,
		key,
	)
	if err != nil {
		return nil, fmt.Errorf("idempotency begin: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("idempotency begin rows: %w", err)
	}
	if n == 1 {
		return &IdemResult{Replayed: false}, nil
	}

	var status string
	var response sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT status, response FROM idempotency_keys WHERE key = $1`,
		key,
	).Scan(&status, &response)
	if err != nil {
		return nil, fmt.Errorf("idempotency check: %w", err)
	}

	if status == "processing" {
		return nil, fmt.Errorf("idempotency conflict: request with key %q is already being processed", key)
	}

	if response.Valid && response.String != "" {
		return &IdemResult{Replayed: true, Response: []byte(response.String)}, nil
	}
	return &IdemResult{Replayed: true}, nil
}

func (s *IdempotencyStore) Commit(ctx context.Context, key string, result interface{}) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("idempotency commit marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE idempotency_keys SET status = 'done', response = $1 WHERE key = $2`,
		string(raw), key,
	)
	if err != nil {
		return fmt.Errorf("idempotency commit: %w", err)
	}
	s.db.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE expires_at < NOW()`)
	return nil
}
