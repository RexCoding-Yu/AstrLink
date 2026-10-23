package seekdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestServerLifecycle runs a real seekdb. It needs the pinned bundle for this
// platform: ASTRLINK_SEEKDB_BUNDLE=/path/to/bundle.tar.gz.
func TestServerLifecycle(t *testing.T) {
	archive := os.Getenv("ASTRLINK_SEEKDB_BUNDLE")
	if archive == "" {
		t.Skip("set ASTRLINK_SEEKDB_BUNDLE to the seekdb runtime archive")
	}
	bundle, ok := CurrentBundle()
	if !ok {
		t.Skip("no seekdb runtime for this platform")
	}
	ctx := context.Background()
	installation, err := (Installer{Root: t.TempDir()}).Import(ctx, bundle, archive)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	dataDir := filepath.Join(t.TempDir(), "a directory with spaces", "seekdb")
	options := Options{Installation: installation, DataDir: dataDir, Password: "Test-Password-1", Database: "astrlink"}

	started := time.Now()
	server, err := Start(ctx, options)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Logf("first start: %s", time.Since(started).Round(time.Millisecond))
	db := server.DB()
	for _, statement := range []string{
		`CREATE TABLE chunks (id BIGINT PRIMARY KEY, content TEXT NOT NULL, embedding VECTOR(3),
    FULLTEXT INDEX ft (content) WITH PARSER ik,
    VECTOR INDEX vi (embedding) WITH (DISTANCE=l2, TYPE=hnsw, LIB=vsag)) ORGANIZATION = HEAP`,
		`INSERT INTO chunks VALUES (1, '部署到 Windows 时防火墙会拦截端口', '[1,0,0]'), (2, 'macOS 打包需要签名和公证', '[0,1,0]')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	var id int
	if err := db.QueryRowContext(ctx, `SELECT id FROM chunks WHERE MATCH(content) AGAINST('防火墙') LIMIT 1`).Scan(&id); err != nil || id != 1 {
		t.Fatalf("full-text query: id=%d err=%v", id, err)
	}
	// The vector index follows writes a few hundred milliseconds behind.
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = db.QueryRowContext(ctx, `SELECT id FROM chunks ORDER BY l2_distance(embedding, '[0,0.9,0.1]') APPROXIMATE LIMIT 1`).Scan(&id)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || id != 2 {
		t.Fatalf("vector query: id=%d err=%v", id, err)
	}
	if server.PID() == 0 {
		t.Fatal("the first start reused a server")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-server.Exited():
	default:
		t.Fatal("Close returned before the server exited")
	}

	// The data and the password survive a restart.
	restarted, err := Start(ctx, options)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer restarted.Close()
	if err := restarted.DB().QueryRowContext(ctx, `SELECT id FROM chunks WHERE MATCH(content) AGAINST('公证') LIMIT 1`).Scan(&id); err != nil || id != 2 {
		t.Fatalf("query after restart: id=%d err=%v", id, err)
	}
	// Root without the password is refused.
	if _, err := restarted.connect(ctx, ""); !IsAccessDenied(err) {
		t.Fatalf("root without a password: %v", err)
	}
	// Telemetry stays local.
	data, err := os.ReadFile(filepath.Join(dataDir, "run", "telemetry.json"))
	if err == nil {
		var report struct {
			Sent bool `json:"sent"`
		}
		if json.Unmarshal(data, &report) == nil && report.Sent {
			t.Fatal("seekdb reported telemetry")
		}
	}
}
