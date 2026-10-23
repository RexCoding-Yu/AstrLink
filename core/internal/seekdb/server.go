package seekdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	defaultReadyTimeout = 3 * time.Minute
	probeEvery          = 200 * time.Millisecond
	exitGrace           = 10 * time.Second
	// The server's own diagnostic log stays at errors; at its defaults it
	// writes hundreds of megabytes while indexing.
	logLevel = "syslog_level=ERROR"
	// Query timeout in microseconds; vector index writes can take longer than
	// seekdb's 10 s default.
	queryTimeoutMicros = "120000000"
	accessDenied       = 1045
)

// First-start parameters, which the server then keeps. 1G and 2G are the
// smallest memory budget and log disk it starts with.
var firstStartParameters = []string{
	"memory_budget=1G",
	"log_disk_size=2G",
	"max_syslog_file_count=1",
}

// Options configures one server run.
type Options struct {
	Installation Installation
	// DataDir is the server's base directory; it is created when missing.
	DataDir string
	// Password is root's. A new directory has none until Start sets it.
	Password string
	// Database is created when missing and selected for the pool.
	Database     string
	ReadyTimeout time.Duration
}

// Server is a seekdb this process holds a clients lock on.
type Server struct {
	options   Options
	point     endpoint
	clients   *fileLock
	command   *exec.Cmd
	exited    chan struct{}
	output    *tailBuffer
	db        *sql.DB
	closeOnce sync.Once
}

// Start brings up seekdb on DataDir and returns once root can connect with
// Password. It reuses a server already running on the directory.
func Start(ctx context.Context, options Options) (*Server, error) {
	if options.ReadyTimeout <= 0 {
		options.ReadyTimeout = defaultReadyTimeout
	}
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve seekdb data directory: %w", err)
	}
	options.DataDir = dataDir
	runDir := filepath.Join(dataDir, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("prepare seekdb data directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, options.ReadyTimeout)
	defer cancel()

	point, err := prepareEndpoint(dataDir)
	if err != nil {
		return nil, err
	}
	server := &Server{options: options, point: point, exited: make(chan struct{}), output: newTailBuffer(8 << 10)}
	fail := func(err error) (*Server, error) {
		server.release()
		return nil, err
	}
	if server.clients, err = openLock(filepath.Join(runDir, clientsLockName)); err != nil {
		return fail(err)
	}
	if err := server.clients.lock(ctx, false); err != nil {
		return fail(fmt.Errorf("wait for the seekdb clients lock: %w", err))
	}
	if db, err := server.connect(ctx, options.Password); err == nil {
		return server.adopt(ctx, db)
	}

	startup, err := openLock(filepath.Join(runDir, startupLockName))
	if err != nil {
		return fail(err)
	}
	defer startup.close()
	if err := startup.lock(ctx, true); err != nil {
		return fail(fmt.Errorf("wait for the seekdb startup lock: %w", err))
	}
	// The client that held startup may have started the server meanwhile.
	if db, err := server.connect(ctx, options.Password); err == nil {
		return server.adopt(ctx, db)
	}
	firstStart := !hasEntries(filepath.Join(dataDir, "store", "sstable"))
	command := exec.Command(options.Installation.Executable, arguments(dataDir, firstStart)...)
	command.Dir = dataDir
	// seekdb reports the host to OceanBase on its first start unless told not to.
	command.Env = append(os.Environ(), "TELEMETRY_ENABLED=false")
	command.Stdout = server.output
	command.Stderr = server.output
	configureCommand(command)
	if err := command.Start(); err != nil {
		return fail(fmt.Errorf("start seekdb: %w", err))
	}
	server.command = command
	go func() {
		_ = command.Wait()
		close(server.exited)
	}()
	db, err := server.waitReady(ctx, firstStart)
	if err != nil {
		return fail(err)
	}
	return server.adopt(ctx, db)
}

func arguments(dataDir string, firstStart bool) []string {
	args := []string{
		"--base-dir=" + dataDir, "--embedded", "--nodaemon",
		"--parameter", "mysql_port_mode=disabled",
		"--parameter", logLevel,
	}
	if firstStart {
		for _, parameter := range firstStartParameters {
			args = append(args, "--parameter", parameter)
		}
	}
	return args
}

// waitReady polls until root connects. A fresh directory's root has no
// password; so does one whose first start ended before Start could set it.
func (server *Server) waitReady(ctx context.Context, firstStart bool) (*sql.DB, error) {
	candidates := []string{server.options.Password, ""}
	if firstStart {
		candidates = []string{"", server.options.Password}
	}
	for {
		for _, password := range candidates {
			db, err := server.connect(ctx, password)
			if err != nil {
				continue
			}
			if password == server.options.Password {
				return db, nil
			}
			err = setPassword(ctx, db, server.options.Password)
			_ = db.Close()
			if err != nil {
				return nil, err
			}
			return server.connect(ctx, server.options.Password)
		}
		select {
		case <-server.exited:
			return nil, fmt.Errorf("seekdb exited before it was ready%s", server.output.describe())
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("seekdb was not ready within %s%s", server.options.ReadyTimeout, server.output.describe())
			}
			return nil, ctx.Err()
		case <-time.After(probeEvery):
		}
	}
}

// adopt selects the database for the pool server.DB returns.
func (server *Server) adopt(ctx context.Context, root *sql.DB) (*Server, error) {
	if server.options.Database == "" {
		server.db = root
		return server, nil
	}
	_, err := root.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+server.options.Database+"`")
	_ = root.Close()
	if err == nil {
		server.db, err = server.open(server.options.Password, server.options.Database)
	}
	if err == nil {
		err = server.db.PingContext(ctx)
	}
	if err != nil {
		server.release()
		return nil, fmt.Errorf("prepare seekdb database: %w", err)
	}
	return server, nil
}

func (server *Server) connect(ctx context.Context, password string) (*sql.DB, error) {
	db, err := server.open(password, "")
	if err != nil {
		return nil, err
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRowContext(probe, "SELECT 1").Scan(&one); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (server *Server) open(password, database string) (*sql.DB, error) {
	config := mysql.NewConfig()
	config.User = "root"
	config.Passwd = password
	config.DBName = database
	config.Net = "seekdb"
	config.Addr = "local"
	config.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return server.point.dial(ctx)
	}
	config.Timeout = 3 * time.Second
	config.InterpolateParams = true
	config.Params = map[string]string{"ob_query_timeout": queryTimeoutMicros}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}

func setPassword(ctx context.Context, db *sql.DB, password string) error {
	if password == "" {
		return nil
	}
	if _, err := db.ExecContext(ctx, "ALTER USER root IDENTIFIED BY ?", password); err != nil {
		return fmt.Errorf("set the seekdb root password: %w", err)
	}
	return nil
}

// IsAccessDenied reports a server that refused root's password, as when the
// key the password derives from has changed.
func IsAccessDenied(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == accessDenied
}

// DB returns the pool for Options.Database.
func (server *Server) DB() *sql.DB { return server.db }

// Exited closes when the server process this Server started ends. It never
// closes for a reused server.
func (server *Server) Exited() <-chan struct{} { return server.exited }

// PID returns the server's process ID, or 0 for a reused server.
func (server *Server) PID() int {
	if server.command == nil || server.command.Process == nil {
		return 0
	}
	return server.command.Process.Pid
}

// Close releases the clients lock, which makes the server exit, and waits
// for it; a server that lingers is killed.
func (server *Server) Close() error {
	server.release()
	return nil
}

func (server *Server) release() {
	server.closeOnce.Do(func() {
		if server.db != nil {
			_ = server.db.Close()
		}
		_ = server.clients.close()
		if server.command != nil {
			select {
			case <-server.exited:
			case <-time.After(exitGrace):
				_ = server.command.Process.Kill()
				<-server.exited
			}
		}
		server.point.close()
	})
}

func hasEntries(directory string) bool {
	entries, err := os.ReadDir(directory)
	return err == nil && len(entries) > 0
}

// tailBuffer keeps the last bytes the server wrote to stdout and stderr, for
// startup failures.
type tailBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.data = append(buffer.data, data...)
	if len(buffer.data) > buffer.max {
		buffer.data = append([]byte(nil), buffer.data[len(buffer.data)-buffer.max:]...)
	}
	return len(data), nil
}

func (buffer *tailBuffer) describe() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(buffer.data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return ": " + strings.Join(lines, " / ")
}
