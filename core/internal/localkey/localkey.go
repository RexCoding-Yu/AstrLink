// Package localkey finds or creates the local key that wraps AstrLink's data
// keys (plan §5.2). The key never appears in argv or the environment: it comes
// from stdin, from a key file, or from <data-dir>/local.key, in that order.
// Core never touches the macOS keychain; the desktop reads it and passes the
// key on stdin.
package localkey

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Source says where the key came from.
type Source string

const (
	// SourceStdin is a key the desktop read from the keychain and injected.
	SourceStdin Source = "stdin"
	// SourceKeyFile is an explicitly configured key file.
	SourceKeyFile Source = "key_file"
	// SourceDataDir is the default <data-dir>/local.key.
	SourceDataDir Source = "data_dir"
)

const (
	// FileName is the default key file inside the data directory.
	FileName = "local.key"
	// EnvKeyFile names a key file path; it never holds the key itself.
	EnvKeyFile = "ASTRLINK_KEK_FILE"
	// KeyBytes is the local key size.
	KeyBytes = 32
	// maxFileBytes bounds a key file read: 64 hex digits plus line endings.
	maxFileBytes = 256
)

// ErrInvalid means key material is not 64 hex digits.
var ErrInvalid = errors.New("local key must be 64 hex digits")

// Options selects the key. The first non-empty source wins.
type Options struct {
	// StdinKey is a decoded key read from stdin. It never creates a file.
	StdinKey []byte
	// KeyFile is an explicit key path from --kek-file or ASTRLINK_KEK_FILE.
	// A missing file is created; a missing parent directory is an error.
	KeyFile string
	// DataDir holds the default local.key, created with the directory if
	// either is missing.
	DataDir string
	// Logf receives non-fatal warnings such as loose file permissions.
	Logf func(format string, args ...any)
}

// Resolve returns a copy of the local key and its source.
func Resolve(options Options) ([]byte, Source, error) {
	if len(options.StdinKey) > 0 {
		if len(options.StdinKey) != KeyBytes {
			return nil, "", fmt.Errorf("stdin local key: %w", ErrInvalid)
		}
		return append([]byte(nil), options.StdinKey...), SourceStdin, nil
	}
	if path := strings.TrimSpace(options.KeyFile); path != "" {
		key, err := loadOrCreate(path, false, options.Logf)
		if err != nil {
			return nil, "", fmt.Errorf("local key file: %w", err)
		}
		return key, SourceKeyFile, nil
	}
	if strings.TrimSpace(options.DataDir) == "" {
		return nil, "", fmt.Errorf("local key: a data directory or key file is required")
	}
	key, err := loadOrCreate(filepath.Join(options.DataDir, FileName), true, options.Logf)
	if err != nil {
		return nil, "", fmt.Errorf("local key: %w", err)
	}
	return key, SourceDataDir, nil
}

// Parse decodes 64 hex digits; surrounding whitespace, including a trailing
// newline, is ignored.
func Parse(text []byte) ([]byte, error) {
	trimmed := trimSpace(text)
	if len(trimmed) != hex.EncodedLen(KeyBytes) {
		return nil, ErrInvalid
	}
	key := make([]byte, KeyBytes)
	if _, err := hex.Decode(key, trimmed); err != nil {
		clear(key)
		return nil, ErrInvalid
	}
	return key, nil
}

// Encode returns the key file content: lowercase hex and a newline.
func Encode(key []byte) []byte {
	encoded := make([]byte, hex.EncodedLen(len(key))+1)
	hex.Encode(encoded, key)
	encoded[len(encoded)-1] = '\n'
	return encoded
}

func loadOrCreate(path string, createDirectory bool, logf func(string, ...any)) ([]byte, error) {
	key, err := load(path, logf)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	directory := filepath.Dir(path)
	if createDirectory {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create key directory: %w", err)
		}
	}
	if err := create(path); err != nil {
		return nil, err
	}
	return load(path, logf)
}

func load(path string, logf func(string, ...any)) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	// Windows has no POSIX mode bits; elsewhere a readable key file is a
	// warning, not a refusal, so a restored backup still starts.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 && logf != nil {
		logf("local key file %s is accessible to other users (mode %04o); restrict it to 0600", path, info.Mode().Perm())
	}
	content, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	defer clear(content)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if len(content) > maxFileBytes {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), ErrInvalid)
	}
	key, err := Parse(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return key, nil
}

// create writes a fresh key without replacing one another process created
// first: the key goes to a private temporary file that is then linked into
// place, so a crash never leaves a half-written key behind.
func create(path string) (err error) {
	key := make([]byte, KeyBytes)
	defer clear(key)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return fmt.Errorf("generate local key: %w", err)
	}
	content := Encode(key)
	defer clear(content)
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = temporary.Close()
		return fmt.Errorf("restrict %s: %w", filepath.Base(path), err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		// Some filesystems have no hard links; fall back to a rename, which
		// cannot lose a race only if nothing created the key meanwhile.
		if _, statErr := os.Lstat(path); statErr == nil {
			return nil
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return fmt.Errorf("install %s: %w", filepath.Base(path), err)
		}
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open key directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync key directory: %w", err)
	}
	return nil
}

func trimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && isSpace(value[start]) {
		start++
	}
	for end > start && isSpace(value[end-1]) {
		end--
	}
	return value[start:end]
}

func isSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
}
