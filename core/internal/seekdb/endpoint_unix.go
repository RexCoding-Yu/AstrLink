//go:build !windows

package seekdb

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// A socket path must fit sockaddr_un (104 bytes on macOS, 108 on Linux), and
// a data directory under Application Support can be longer than that. The
// server binds run/sql.sock relative to its base directory, so clients reach
// it through a short private directory whose run entry links to the real one.
const maxSocketPath = 100

type endpoint struct {
	aliasDir string
	socket   string
}

func prepareEndpoint(dataDir string) (endpoint, error) {
	alias, err := os.MkdirTemp("", "astrlink-seekdb-")
	if err != nil {
		return endpoint{}, fmt.Errorf("prepare seekdb socket directory: %w", err)
	}
	runDir, err := filepath.Abs(filepath.Join(dataDir, "run"))
	if err == nil {
		err = os.Symlink(runDir, filepath.Join(alias, "run"))
	}
	if err != nil {
		_ = os.RemoveAll(alias)
		return endpoint{}, fmt.Errorf("prepare seekdb socket directory: %w", err)
	}
	socket := filepath.Join(alias, "run", "sql.sock")
	if len(socket) > maxSocketPath {
		_ = os.RemoveAll(alias)
		return endpoint{}, fmt.Errorf("the temporary directory path is too long for a seekdb socket: %s", socket)
	}
	return endpoint{aliasDir: alias, socket: socket}, nil
}

func (point endpoint) dial(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", point.socket)
}

// close removes the alias directory and its link, not the run directory.
func (point endpoint) close() {
	if point.aliasDir != "" {
		_ = os.RemoveAll(point.aliasDir)
	}
}
