package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"contractaudit/internal/config"
	"contractaudit/internal/docversion"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConcurrentIdenticalBlobUploadsAreAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("同一份材料\n"), 1000)
	digest := docversion.Hash(body).String()
	start := make(chan struct{})
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- writeBlob(dir, digest, body) }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := readBlob(dir, digest)
	if err != nil || !bytes.Equal(body, got) {
		t.Fatalf("blob corrupted: %v", err)
	}
	files, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary files leaked: %v %v", files, err)
	}
}

func TestDuplicateDatabaseErrorIsNarrowlyClassified(t *testing.T) {
	for _, tc := range []struct {
		err     error
		allowed bool
	}{
		{&pgconn.PgError{Code: "42P04"}, true},
		{fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505", ConstraintName: "pg_database_datname_index"}), true},
		{&pgconn.PgError{Code: "23505", ConstraintName: "other_unique_index"}, false},
		{&pgconn.PgError{Code: "42501"}, false},
		{errors.New("duplicate database"), false},
		{nil, false},
	} {
		if got := duplicateDatabase(tc.err); got != tc.allowed {
			t.Fatalf("%v: %v", tc.err, got)
		}
	}
}

func TestConcurrentDatabaseInitialization(t *testing.T) {
	base, err := config.Select(config.Environ{AppEnv: "test", TestDatabaseURL: os.Getenv("TEST_DATABASE_URL")})
	if err != nil {
		t.Fatal(err)
	}
	name, err := newID("ca_test_init_")
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := withDatabase(base.DSN, name)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := config.Select(config.Environ{AppEnv: "test", TestDatabaseURL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	adminDSN, err := withDatabase(base.DSN, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	quoted, err := quoteIdent(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Only the fresh random test database this test created is dropped.
		conn, err := pgx.Connect(context.Background(), adminDSN)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoted); err != nil {
			t.Error(err)
		}
	})
	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- ensureDatabase(context.Background(), sel) }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var exists bool
	if err := admin.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || !exists {
		t.Fatalf("database not initialized: %v", err)
	}
}
