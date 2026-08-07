package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"path"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Nomor sembarang tapi tetap, dipakai untuk advisory lock. Render menjalankan
// instance baru sebelum yang lama berhenti, jadi dua proses bisa mencoba
// bermigrasi bersamaan; yang kalah menunggu, lalu melihat migrasinya sudah
// tercatat dan melewatinya.
const migrationLockID = 8_150_777_301

// migrate menerapkan berkas migrations/*.sql yang belum pernah dijalankan,
// berurutan menurut nama, satu transaksi per berkas.
//
// ponytail: sengaja bukan golang-migrate. Yang dibutuhkan cuma "jalankan yang
// belum, sekali saja, berurutan" — tanpa migrasi turun, tanpa penanda dirty.
// Begitu butuh rollback otomatis atau migrasi yang tidak boleh dibungkus
// transaksi (mis. CREATE INDEX CONCURRENTLY), pindah ke golang-migrate dan
// biarkan tabel schema_migrations di bawah ini yang dibaca olehnya.
func migrate(ctx context.Context, db *pgxpool.Pool) error {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("mengunci migrasi: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID)

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return err
	}

	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, f := range files {
		version := strings.TrimSuffix(path.Base(f), ".sql")

		var done bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}

		body, err := migrationFS.ReadFile(f)
		if err != nil {
			return err
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migrasi %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrasi %s: %w", version, err)
		}
		log.Printf("migrasi %s diterapkan", version)
	}
	return nil
}
