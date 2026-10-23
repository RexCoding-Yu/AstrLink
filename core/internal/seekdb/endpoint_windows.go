//go:build windows

package seekdb

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
)

// On Windows the server writes the name of its named pipe to run/sql.pipe;
// with TCP disabled the pipe is its only endpoint.
type endpoint struct {
	pipeFile string
}

func prepareEndpoint(dataDir string) (endpoint, error) {
	return endpoint{pipeFile: filepath.Join(dataDir, "run", "sql.pipe")}, nil
}

func (point endpoint) dial(ctx context.Context) (net.Conn, error) {
	file, err := os.Open(point.pipeFile)
	if err != nil {
		return nil, fmt.Errorf("seekdb has not published its pipe yet: %w", err)
	}
	line, _ := bufio.NewReader(file).ReadString('\n')
	_ = file.Close()
	name := strings.TrimRight(line, "\r\n")
	if name == "" || strings.ContainsAny(name, `\/`) {
		return nil, fmt.Errorf("seekdb has not published its pipe yet")
	}
	return winio.DialPipeContext(ctx, `\\.\pipe\`+name)
}

func (point endpoint) close() {}
