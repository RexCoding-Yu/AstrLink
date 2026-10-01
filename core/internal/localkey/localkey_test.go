package localkey

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeKeyFile(t *testing.T, path string, key []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, Encode(key), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func keyOf(fill byte) []byte { return bytes.Repeat([]byte{fill}, KeyBytes) }

func TestResolvePrefersStdinThenKeyFileThenDataDir(t *testing.T) {
	dataDir := t.TempDir()
	writeKeyFile(t, filepath.Join(dataDir, FileName), keyOf(3), 0o600)
	keyFile := filepath.Join(t.TempDir(), "kek")
	writeKeyFile(t, keyFile, keyOf(2), 0o600)

	cases := []struct {
		name    string
		options Options
		want    []byte
		source  Source
	}{
		{"stdin", Options{StdinKey: keyOf(1), KeyFile: keyFile, DataDir: dataDir}, keyOf(1), SourceStdin},
		{"key file", Options{KeyFile: keyFile, DataDir: dataDir}, keyOf(2), SourceKeyFile},
		{"data dir", Options{DataDir: dataDir}, keyOf(3), SourceDataDir},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			key, source, err := Resolve(test.options)
			if err != nil {
				t.Fatal(err)
			}
			if source != test.source || !bytes.Equal(key, test.want) {
				t.Fatalf("Resolve() source = %s, key matches = %v", source, bytes.Equal(key, test.want))
			}
		})
	}
}

func TestResolveCopiesTheStdinKeyAndNeverWritesAFile(t *testing.T) {
	dataDir := t.TempDir()
	stdinKey := keyOf(7)
	key, source, err := Resolve(Options{StdinKey: stdinKey, DataDir: dataDir})
	if err != nil || source != SourceStdin {
		t.Fatalf("Resolve() = %s, %v", source, err)
	}
	clear(stdinKey)
	if !bytes.Equal(key, keyOf(7)) {
		t.Fatal("Resolve() shares the caller's stdin buffer")
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("stdin key created %d file(s)", len(entries))
	}
	if _, _, err := Resolve(Options{StdinKey: keyOf(7)[:16], DataDir: dataDir}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short stdin key error = %v", err)
	}
}

func TestResolveGeneratesAPrivateDataDirKeyOnce(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "nested", "data")
	first, source, err := Resolve(Options{DataDir: dataDir})
	if err != nil || source != SourceDataDir {
		t.Fatalf("Resolve() = %s, %v", source, err)
	}
	path := filepath.Join(dataDir, FileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 65 || content[64] != '\n' || strings.ToLower(string(content)) != string(content) {
		t.Fatalf("key file layout is %d bytes", len(content))
	}
	if runtime.GOOS != "windows" {
		for candidate, want := range map[string]os.FileMode{path: 0o600, dataDir: 0o700} {
			info, err := os.Stat(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != want {
				t.Fatalf("%s mode = %04o, want %04o", filepath.Base(candidate), info.Mode().Perm(), want)
			}
		}
	}
	second, _, err := Resolve(Options{DataDir: dataDir})
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("second Resolve() changed the key: %v", err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("data dir keeps %d entries, want only %s", len(entries), FileName)
	}
}

func TestResolveCreatesAMissingExplicitKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "astrlink_key")
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	key, source, err := Resolve(Options{KeyFile: path, DataDir: t.TempDir()})
	if err != nil || source != SourceKeyFile || len(key) != KeyBytes {
		t.Fatalf("Resolve() = %s, %v", source, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("explicit key file was not created: %v", err)
	}
}

func TestResolveFailsForAnExplicitKeyFileItCannotCreate(t *testing.T) {
	missingParent := filepath.Join(t.TempDir(), "absent", "astrlink_key")
	dataDir := t.TempDir()
	if _, _, err := Resolve(Options{KeyFile: missingParent, DataDir: dataDir}); err == nil {
		t.Fatal("Resolve() created a key under a missing directory")
	}
	if _, err := os.Stat(filepath.Dir(missingParent)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Resolve() created the explicit key directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an explicit key file failure fell back to the data dir")
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	readOnly := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	if _, _, err := Resolve(Options{KeyFile: filepath.Join(readOnly, "astrlink_key")}); err == nil {
		t.Fatal("Resolve() succeeded in a read-only directory")
	}
}

func TestResolveRejectsInvalidKeyFiles(t *testing.T) {
	for name, content := range map[string]string{
		"short":     strings.Repeat("a", 63) + "\n",
		"long":      strings.Repeat("a", 66) + "\n",
		"not hex":   strings.Repeat("g", 64) + "\n",
		"empty":     "",
		"two lines": strings.Repeat("a", 64) + "\n" + strings.Repeat("b", 64) + "\n",
		"oversized": strings.Repeat(" ", maxFileBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, FileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Resolve(Options{DataDir: dataDir})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Resolve() error = %v, want ErrInvalid", err)
			}
			if got, _ := os.ReadFile(path); string(got) != content {
				t.Fatal("Resolve() replaced an invalid key file")
			}
		})
	}
}

func TestResolveAcceptsSurroundingWhitespaceAndUppercase(t *testing.T) {
	dataDir := t.TempDir()
	content := "  " + strings.ToUpper(strings.Repeat("ab", 32)) + "\r\n\n"
	if err := os.WriteFile(filepath.Join(dataDir, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	key, _, err := Resolve(Options{DataDir: dataDir})
	if err != nil || !bytes.Equal(key, bytes.Repeat([]byte{0xab}, KeyBytes)) {
		t.Fatalf("Resolve() = %v", err)
	}
}

func TestResolveWarnsAboutLoosePermissionsWithoutRefusing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits")
	}
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, FileName)
	writeKeyFile(t, path, keyOf(9), 0o644)
	var warnings []string
	key, _, err := Resolve(Options{DataDir: dataDir, Logf: func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}})
	if err != nil || !bytes.Equal(key, keyOf(9)) {
		t.Fatalf("Resolve() = %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "0644") {
		t.Fatalf("warnings = %q", warnings)
	}
	if strings.Contains(warnings[0], string(Encode(keyOf(9))[:64])) {
		t.Fatal("warning leaks the key")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o644 {
		t.Fatal("Resolve() changed the file mode")
	}
}

func TestResolveRequiresSomeSource(t *testing.T) {
	if _, _, err := Resolve(Options{}); err == nil {
		t.Fatal("Resolve() without a source succeeded")
	}
}

func TestResolveRejectsADirectoryAsKeyFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Resolve(Options{DataDir: dataDir}); err == nil {
		t.Fatal("Resolve() accepted a directory")
	}
}
