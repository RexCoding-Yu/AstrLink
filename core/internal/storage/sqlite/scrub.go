package sqlite

import (
	"context"
	"errors"
	"fmt"
)

// errScrubBusy means another connection still reads the database, so the WAL
// cannot be emptied now. The scrub stays pending for the next start.
var errScrubBusy = errors.New("database is in use by another connection")

// scrubLegacyFile rewrites the database in place so free pages and old WAL
// frames no longer hold what a test build kept as plaintext: its audit key
// and the secrets SealPlaintextSecrets just sealed. VACUUM rebuilds every
// live page, the TRUNCATE checkpoint copies them over the file and empties
// the WAL, and only then is audit_keys dropped. A scrub that fails or is
// interrupted leaves the table, so the next start runs it again.
func (store *Store) scrubLegacyFile(ctx context.Context) error {
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve scrub connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuum database: %w", err)
	}
	var busy, walFrames, checkpointed int
	if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &walFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint scrubbed database: %w", err)
	}
	if busy != 0 {
		return errScrubBusy
	}
	// pending_file_scrub is where a test build queued this same scrub.
	for _, statement := range []string{`DROP TABLE IF EXISTS pending_file_scrub`, `DROP TABLE IF EXISTS audit_keys`} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("finish file scrub: %w", err)
		}
	}
	return nil
}
