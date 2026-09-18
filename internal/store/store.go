// Package store opens the Reprise diary database.
//
// One SQLite file holds the diary tables and the Keel stores beside them.
// This package migrates the diary tables under its own namespace, so the
// Keel stores keep their own ledgers in the same file. Deleting an episode
// cascades to its diary content.
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/nrynss/keel/sqlite"
)

// schemaNamespace is the migration ledger namespace this package owns. It
// shares the database file with the Keel stores without colliding, because
// each namespace keeps its own ledger.
const schemaNamespace = "reprise"

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the diary database. Create it with Open, because the zero value
// has no database. Store is safe for concurrent use.
type Store struct {
	db *sqlite.DB
}

// DB returns the open database the store runs on.
func (s *Store) DB() *sqlite.DB { return s.db }

// Open applies the diary schema to db and returns the store. Open never
// takes ownership of db, so the caller closes it.
func Open(ctx context.Context, db *sqlite.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("store: open: %w: database must not be nil", ErrInvalid)
	}
	schema, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := sqlite.Migrate(ctx, db, schemaNamespace, schema); err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	return &Store{db: db}, nil
}
