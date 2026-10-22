package sqliteschema

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
)

func TestCopiesHaveCurrentSchemaAndIndependentDataWithoutStoreSecrets(t *testing.T) {
	for index := range 4 {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "copy.sqlite")
			Seed(t, path)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			runner, err := migrate.New(migrate.SQLDatabase{DB: database}, migrate.DefaultMigrations())
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.RequireCurrent(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"key_envelopes", "raw_sealing_keys", "local_access_tokens", "local_access_token_bootstrap_state"} {
				var count int
				if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("%s contains %d rows before store bootstrap", table, count)
				}
			}
			// Each copy can create the same table independently. Reopening an
			// existing fixture must keep its changes, including an active WAL.
			if _, err := database.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE fixture_marker (value INTEGER);
INSERT INTO fixture_marker VALUES (?)`, index); err != nil {
				t.Fatal(err)
			}
			Seed(t, path)
			var value int
			if err := database.QueryRow("SELECT value FROM fixture_marker").Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != index {
				t.Fatalf("fixture value = %d, want %d", value, index)
			}
		})
	}
}

func TestSeedLeavesExistingFilesUntouched(t *testing.T) {
	for _, contents := range [][]byte{nil, []byte("deliberately invalid database")} {
		path := filepath.Join(t.TempDir(), "existing.sqlite")
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		Seed(t, path)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, contents) {
			t.Fatal("seed replaced an existing fixture")
		}
	}
}
