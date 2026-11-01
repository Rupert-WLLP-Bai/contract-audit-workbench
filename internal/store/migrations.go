package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Version 1 is the frozen, previously unversioned core schema. Versions 2–7
// preserve its dependent material, subject, legal and function migrations.
// Later schema changes must be new numbered files; never edit an applied file.
// The running application accepts only this explicitly reviewed version range.
const (
	schemaMinCompatibleVersion = 21
	schemaMaxCompatibleVersion = 21
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationFileName = regexp.MustCompile(`^([0-9]{4})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

type schemaMigration struct {
	version  int
	name     string
	checksum string
	sql      string
}

// migrationCatalog is the complete, ordered history, not a hash of a mutable
// current-schema projection. Checksums cover each file's exact source bytes.
func migrationCatalog() ([]schemaMigration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema migration catalog: %w", err)
	}
	catalog := make([]schemaMigration, 0, len(entries))
	for _, entry := range entries {
		match := migrationFileName.FindStringSubmatch(entry.Name())
		if entry.IsDir() || match == nil {
			return nil, fmt.Errorf("invalid schema migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil || version != len(catalog)+1 {
			return nil, fmt.Errorf("schema migration versions must be consecutive at %q", entry.Name())
		}
		content, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read schema migration %q: %w", entry.Name(), err)
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			return nil, fmt.Errorf("schema migration %q is empty", entry.Name())
		}
		digest := sha256.Sum256(content)
		catalog = append(catalog, schemaMigration{
			version: version, name: match[2], checksum: hex.EncodeToString(digest[:]), sql: string(content),
		})
	}
	if schemaMinCompatibleVersion < 1 || schemaMinCompatibleVersion > schemaMaxCompatibleVersion ||
		schemaMaxCompatibleVersion != len(catalog) {
		return nil, errors.New("schema compatibility range does not match the migration catalog")
	}
	return catalog, nil
}

type migrationQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// appliedMigrationVersion verifies the whole applied prefix, including names,
// checksums, missing versions and versions unknown to this binary.
func appliedMigrationVersion(ctx context.Context, q migrationQueryer, catalog []schemaMigration) (int, error) {
	rows, err := q.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, fmt.Errorf("read schema migration ledger: %w", err)
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return 0, fmt.Errorf("read schema migration ledger row: %w", err)
		}
		if applied >= len(catalog) {
			return 0, fmt.Errorf("database schema version %d is unknown to this application", version)
		}
		expected := catalog[applied]
		if version != expected.version {
			return 0, fmt.Errorf("schema migration ledger has version %d where version %d is required", version, expected.version)
		}
		if name != expected.name || checksum != expected.checksum {
			return 0, fmt.Errorf("schema migration %d (%s) does not match its recorded name or checksum", version, expected.name)
		}
		applied++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read schema migration ledger: %w", err)
	}
	return applied, nil
}

// checkSchemaCompatibility is used by application identities; they need only
// SELECT on the ledger and can never create it or run a migration at startup.
func checkSchemaCompatibility(ctx context.Context, pool *pgxpool.Pool) error {
	catalog, err := migrationCatalog()
	if err != nil {
		return err
	}
	version, err := appliedMigrationVersion(ctx, pool, catalog)
	if err != nil {
		return fmt.Errorf("schema compatibility check failed; run the migration job with the matching release: %w", err)
	}
	if version < schemaMinCompatibleVersion || version > schemaMaxCompatibleVersion {
		return fmt.Errorf("database schema version %d is outside application compatibility range [%d,%d]", version, schemaMinCompatibleVersion, schemaMaxCompatibleVersion)
	}
	return nil
}

const createMigrationLedgerSQL = `CREATE TABLE schema_migrations (
	version bigint PRIMARY KEY CHECK (version > 0),
	name text NOT NULL UNIQUE,
	checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
	applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`

// migrate holds the same schema-scoped advisory lock as schema-wide writers
// and service-role provisioning, on one dedicated session for the entire run.
// Each numbered step and its ledger record commit in one transaction.
func migrate(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	catalog, err := migrationCatalog()
	if err != nil {
		return err
	}
	if _, err := quoteIdent(schema); err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, schema); err != nil {
		// A cancelled lock request has an uncertain server-side outcome; do not
		// return that session to the pool with a possibly held advisory lock.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Hijack().Close(cleanupCtx)
		return fmt.Errorf("acquire schema migration lock: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanupCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, schema).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Hijack().Close(cleanupCtx)
			return
		}
		conn.Release()
	}()

	// Serialize creation too: two migrators may target the same new test or
	// application schema. Public is created by PostgreSQL and needs no CREATE.
	if schema != "public" {
		if _, err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+pgx.Identifier{schema}.Sanitize()); err != nil {
			return fmt.Errorf("create migration schema: %w", err)
		}
	}
	var activeSchema *string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&activeSchema); err != nil || activeSchema == nil || *activeSchema != schema {
		return fmt.Errorf("migration connection cannot select schema %q", schema)
	}
	var exists bool
	ledger := pgx.Identifier{schema, "schema_migrations"}.Sanitize()
	if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, ledger).Scan(&exists); err != nil {
		return fmt.Errorf("inspect schema migration ledger: %w", err)
	}
	applied := 0
	if exists {
		applied, err = appliedMigrationVersion(ctx, conn, catalog)
		if err != nil {
			return err
		}
	}
	for _, migration := range catalog[applied:] {
		if err := applyMigration(ctx, conn, schema, migration, !exists); err != nil {
			return fmt.Errorf("apply schema migration %04d_%s: %w", migration.version, migration.name, err)
		}
		exists = true
	}
	return nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, schema string, migration schemaMigration, createLedger bool) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()
	if createLedger {
		if _, err := tx.Exec(ctx, createMigrationLedgerSQL); err != nil {
			return fmt.Errorf("create schema migration ledger: %w", err)
		}
	}
	if err := executeMigrationSQL(ctx, tx, schema, migration.sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)`,
		migration.version, migration.name, migration.checksum); err != nil {
		return fmt.Errorf("record schema migration: %w", err)
	}
	return tx.Commit(ctx)
}

// Schema identifiers and schema-specific advisory keys are the only dynamic
// parts in the frozen budget and experience SQL templates.
func executeMigrationSQL(ctx context.Context, tx pgx.Tx, schema, source string) error {
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	key := strings.ReplaceAll(schema, "'", "''")
	sql := strings.NewReplacer(
		"{{SCHEMA}}", quotedSchema,
		"{{LOCK_KEY}}", key+":model-budget:global",
		"{{QUEUE_LOCK_KEY}}", key+":execution-queue",
	).Replace(source)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("execute migration SQL: %w", err)
	}
	return nil
}
