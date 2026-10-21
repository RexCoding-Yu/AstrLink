// Package sqliteschema prepares isolated test databases without repeating the
// full migration history for every business-logic test. It contains no store
// keys or tokens; sqlite.Open still bootstraps those separately for each copy.
package sqliteschema

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
	_ "modernc.org/sqlite"
)

// Seed creates a database with the current schema, or leaves an existing file
// alone. Tests of fresh initialization and migration must not use this helper.
func Seed(t testing.TB, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := schemaImage()
	if err != nil {
		t.Fatalf("prepare test schema: %v", err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// Keep only immutable bytes after closing the database, never a shared live
// connection or WAL. Build from the real migrations, not a second schema list.
var schemaImage = sync.OnceValues(func() ([]byte, error) {
	directory, err := os.MkdirTemp("", "astrlink-test-schema-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "schema.sqlite")
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, migrate.DefaultMigrations())
	if err != nil {
		return nil, err
	}
	if err := runner.Up(context.Background()); err != nil {
		return nil, err
	}
	if err := database.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})
