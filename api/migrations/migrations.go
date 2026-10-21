// Package migrations holds the database schema migrations as numbered goose
// SQL files (00001_name.sql, 00002_name.sql, ...), embedded into whichever
// binary imports this package.
//
// Rules:
//   - Once a migration is merged, never edit or delete it. Fix mistakes with
//     a new migration. CI rejects changes to merged files.
//   - Every migration has a working Down section.
//   - Migrations run as their own step (cmd/migrate), never at API startup.
//
// See docs/database.md.
package migrations

import (
	"database/sql"
	"embed"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// NewProvider returns a goose provider that applies the embedded migrations
// to db.
func NewProvider(db *sql.DB, opts ...goose.ProviderOption) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, db, files, opts...)
}
