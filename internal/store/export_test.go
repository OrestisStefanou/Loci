package store

import (
	"context"
	"database/sql"
)

// Test-only access to internals for store_test.

// DB returns the underlying pool.
func (s *Store) DB() *sql.DB { return s.sqlDB }

// Pragma reads a pragma's value as text.
func (s *Store) Pragma(ctx context.Context, name string) (string, error) {
	var v string
	err := s.sqlDB.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v)
	return v, err
}

// VectorBlob is vectorBlob, the binary form of a query vector.
func VectorBlob(vec []float32) []byte { return vectorBlob(vec) }
