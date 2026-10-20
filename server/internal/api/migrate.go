package api

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate applies any embedded migrations that haven't run yet, in filename order, in one transaction. An advisory
// lock keeps two instances starting at once from both applying them.
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rostr_migrations'))`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `create table if not exists schema_migrations (
			version    text primary key,
			applied_at timestamptz not null default now()
		)`)
		if err != nil {
			return err
		}

		for _, name := range names {
			var applied bool
			err := tx.QueryRow(ctx, `select exists (select 1 from schema_migrations where version = $1)`, name).Scan(&applied)
			if err != nil {
				return err
			}
			if applied {
				continue
			}

			sql, err := migrationFiles.ReadFile(name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `insert into schema_migrations (version) values ($1)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}
