// Package store is the only package that talks to the database. It uses
// database/sql over the Turso Database driver (tursogo) with hand-written SQL.
// Engine-specific features (full-text, vectors, sync) stay inside this package
// so that swapping to the go-libsql fallback (design.md section 2) only
// touches here.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	turso "turso.tech/database/tursogo"
)

// busyTimeout is how long a write waits for another write to finish, in ms.
const busyTimeout = 5000

// connPragmas run on every new pooled connection: these settings are
// per connection, and foreign keys are off by default.
var connPragmas = []string{"PRAGMA foreign_keys = ON"}

//go:embed migrations/*.sql
var migrationsFS embed.FS

// querier is satisfied by *sql.DB and *sql.Tx, so every query method is
// written once on queries and is available both outside and inside a transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type queries struct{ db querier }

// Store is an open Loci database.
type Store struct {
	queries
	sqlDB *sql.DB
}

// Tx is a transaction. It has every query method Store has.
type Tx struct {
	queries
}

// Open opens (creating if needed) the database at path and applies pending
// migrations. Only one process may open a database: Turso's default mode
// locks the file, and in Loci that process is `loci serve` (design.md
// section 3). Within the process, the *Store is safe for concurrent use.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	inner, err := turso.NewConnector(path, turso.WithBusyTimeout(busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db := sql.OpenDB(connector{inner})
	// journal_mode is stored in the database file, so once is enough.
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	s := &Store{queries: queries{db: db}, sqlDB: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// connector applies connPragmas to every connection the pool opens.
type connector struct{ inner driver.Connector }

func (c connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("turso connection does not support ExecContext")
	}
	for _, p := range connPragmas {
		if _, err := execer.ExecContext(ctx, p, nil); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	return conn, nil
}

func (c connector) Driver() driver.Driver { return c.inner.Driver() }

// Close closes the database.
func (s *Store) Close() error { return s.sqlDB.Close() }

// WithTx runs fn in a transaction, committing if fn returns nil and rolling
// back otherwise.
func (s *Store) WithTx(ctx context.Context, fn func(*Tx) error) error {
	sqlTx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(&Tx{queries: queries{db: sqlTx}}); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.sqlDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// apply runs one migration if it has not been applied.
func (s *Store) apply(ctx context.Context, m migration) error {
	applied, err := s.isApplied(ctx, m.version)
	if err != nil || applied {
		return err
	}
	return s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.db.ExecContext(ctx, m.sql); err != nil {
			return err
		}
		_, err := tx.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			m.version, formatTime(time.Now()))
		return err
	})
}

func (s *Store) isApplied(ctx context.Context, version int) (bool, error) {
	var n int
	err := s.sqlDB.QueryRowContext(ctx,
		`SELECT count(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n)
	return n > 0, err
}

func loadMigrations() ([]migration, error) {
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]migration, 0, len(names))
	for _, name := range names {
		base := filepath.Base(name)
		prefix, _, ok := strings.Cut(base, "_")
		version, convErr := strconv.Atoi(prefix)
		if !ok || convErr != nil {
			return nil, fmt.Errorf("migration %s: name must be NNNN_description.sql", base)
		}
		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: base, sql: string(body)})
	}
	return out, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

func nullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
