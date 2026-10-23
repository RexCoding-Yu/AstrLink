package seekdb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) Bundle {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: typeflag}
		if typeflag != tar.TypeReg {
			header.Size = 0
			header.Linkname = "target"
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(buffer.Bytes())
	return Bundle{Platform: "test", SHA256: hex.EncodeToString(digest[:]), Size: int64(buffer.Len()), Executable: "seekdb.bin"}
}

func TestImportUnpacksAndRecognizesARuntime(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	bundle := writeTarGz(t, archive, []tarEntry{
		{name: "seekdb.bin", body: "#!/bin/sh\n", mode: 0o755},
		{name: "libz.so.1", body: "library", mode: 0o644},
	})
	installer := Installer{Root: root}
	if _, ok := installer.Installed(bundle); ok {
		t.Fatal("an empty root reports an installed runtime")
	}
	installation, err := installer.Import(context.Background(), bundle, archive)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if installation.Executable != filepath.Join(root, bundle.SHA256, "seekdb.bin") {
		t.Fatalf("executable = %q", installation.Executable)
	}
	info, err := os.Stat(installation.Executable)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("executable mode = %v, %v", info, err)
	}
	if _, ok := installer.Installed(bundle); !ok {
		t.Fatal("Installed does not recognize the imported runtime")
	}
	// A file that no longer matches the manifest invalidates the runtime.
	if err := os.WriteFile(filepath.Join(installation.Directory, "libz.so.1"), []byte("changed!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := installer.Installed(bundle); ok {
		t.Fatal("Installed accepts a runtime whose files changed size")
	}
}

func TestImportRejectsAnArchiveThatDoesNotMatchThePin(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	bundle := writeTarGz(t, archive, []tarEntry{{name: "seekdb.bin", body: "x", mode: 0o755}})
	bundle.SHA256 = strings.Repeat("0", 64)
	if _, err := (Installer{Root: t.TempDir()}).Import(context.Background(), bundle, archive); err == nil {
		t.Fatal("Import accepted an archive with the wrong digest")
	}
}

func TestUnpackRejectsUnsafeEntries(t *testing.T) {
	cases := map[string][]tarEntry{
		"parent path":   {{name: "../seekdb.bin", body: "x", mode: 0o755}},
		"nested path":   {{name: "bin/seekdb.bin", body: "x", mode: 0o755}},
		"symlink":       {{name: "seekdb.bin", typeflag: tar.TypeSymlink}},
		"no executable": {{name: "libz.so.1", body: "x", mode: 0o644}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
			bundle := writeTarGz(t, archive, entries)
			if _, err := (Installer{Root: root}).Import(context.Background(), bundle, archive); err == nil {
				t.Fatal("Import accepted an unsafe archive")
			}
			if _, err := os.Stat(filepath.Join(root, bundle.SHA256)); !os.IsNotExist(err) {
				t.Fatalf("a rejected archive left a runtime directory: %v", err)
			}
		})
	}
}

func TestCurrentBundleMatchesTheSupportedPlatforms(t *testing.T) {
	for key, bundle := range bundles {
		if len(bundle.SHA256) != 64 || bundle.Size <= 0 || bundle.Executable == "" {
			t.Fatalf("%s: incomplete pin %+v", key, bundle)
		}
		sources := bundle.sources()
		if len(sources) != 3 || sources[0].member != "" || sources[1].member == "" {
			t.Fatalf("%s: sources = %+v", key, sources)
		}
	}
}

func TestClientsLockExcludesTheServersExclusiveLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), clientsLockName)
	client, err := openLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if err := client.lock(context.Background(), false); err != nil {
		t.Fatalf("shared lock: %v", err)
	}
	server, err := openLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	if acquired, err := server.tryLock(true); err != nil || acquired {
		t.Fatalf("exclusive lock while a client holds the file: acquired=%v err=%v", acquired, err)
	}
	if err := client.close(); err != nil {
		t.Fatal(err)
	}
	if acquired, err := server.tryLock(true); err != nil || !acquired {
		t.Fatalf("exclusive lock after the client let go: acquired=%v err=%v", acquired, err)
	}
}
