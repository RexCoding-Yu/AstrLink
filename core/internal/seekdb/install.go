package seekdb

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	manifestName = "astrlink-runtime.json"
	// A runtime unpacks to about 150 MB on Linux and macOS and 200 MB on
	// Windows; the caps only stop a hostile archive.
	maxUnpackedBytes = 1 << 30
	maxArchiveFiles  = 64
	downloadTimeout  = 30 * time.Minute
)

// ErrUnsupported reports a platform seekdb ships no runtime for.
var ErrUnsupported = errors.New("seekdb has no runtime for this platform")

// Installation is an unpacked runtime ready to start.
type Installation struct {
	Directory  string
	Executable string
	Bundle     Bundle
}

// Progress reports bytes fetched so far against the expected total; total
// is 0 while a module zip of unknown size downloads.
type Progress func(done, total int64)

// Installer downloads, verifies and unpacks runtimes under Root, one
// directory per archive digest.
type Installer struct {
	Root   string
	Client *http.Client
}

type runtimeManifest struct {
	Version       int            `json:"version"`
	BundleSHA256  string         `json:"bundle_sha256"`
	SeekdbVersion string         `json:"seekdb_version"`
	Executable    string         `json:"executable"`
	Files         []manifestFile `json:"files"`
}

type manifestFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (installer Installer) directory(bundle Bundle) string {
	return filepath.Join(installer.Root, bundle.SHA256)
}

// Installed reports the unpacked runtime for bundle when every file its
// manifest lists is present at its recorded size.
func (installer Installer) Installed(bundle Bundle) (Installation, bool) {
	directory := installer.directory(bundle)
	data, err := os.ReadFile(filepath.Join(directory, manifestName))
	if err != nil {
		return Installation{}, false
	}
	var manifest runtimeManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 ||
		manifest.BundleSHA256 != bundle.SHA256 || manifest.Executable != bundle.Executable {
		return Installation{}, false
	}
	for _, file := range manifest.Files {
		info, err := os.Stat(filepath.Join(directory, file.Name))
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return Installation{}, false
		}
	}
	return Installation{
		Directory:  directory,
		Executable: filepath.Join(directory, bundle.Executable),
		Bundle:     bundle,
	}, true
}

// Install fetches bundle from the first source that serves the pinned
// archive, then unpacks it. A partial direct download resumes.
func (installer Installer) Install(ctx context.Context, bundle Bundle, progress Progress) (Installation, error) {
	if installation, ok := installer.Installed(bundle); ok {
		return installation, nil
	}
	staging, err := installer.staging()
	if err != nil {
		return Installation{}, err
	}
	archive := filepath.Join(staging, bundle.SHA256+".tar.gz")
	var failures []string
	for _, source := range bundle.sources() {
		if err := ctx.Err(); err != nil {
			return Installation{}, err
		}
		if err := installer.fetch(ctx, bundle, source, archive, progress); err != nil {
			if ctx.Err() != nil {
				return Installation{}, ctx.Err()
			}
			failures = append(failures, fmt.Sprintf("%s: %v", source.url, err))
			continue
		}
		installation, err := installer.unpack(bundle, archive)
		if err == nil {
			_ = os.Remove(archive)
		}
		return installation, err
	}
	return Installation{}, fmt.Errorf("download seekdb runtime: %s", strings.Join(failures, "; "))
}

// Import unpacks a runtime the user fetched themselves: the bundle.tar.gz
// or the Go module zip that carries it.
func (installer Installer) Import(ctx context.Context, bundle Bundle, path string) (Installation, error) {
	staging, err := installer.staging()
	if err != nil {
		return Installation{}, err
	}
	archive := filepath.Join(staging, bundle.SHA256+".tar.gz")
	member := bundle.sources()[1].member
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		err = extractMember(path, member, archive)
	} else {
		err = copyFile(path, archive)
	}
	if err == nil {
		err = verifyArchive(archive, bundle)
	}
	if err != nil {
		_ = os.Remove(archive)
		return Installation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Installation{}, err
	}
	installation, err := installer.unpack(bundle, archive)
	if err == nil {
		_ = os.Remove(archive)
	}
	return installation, err
}

// Remove deletes the unpacked runtime for bundle and any staged download.
func (installer Installer) Remove(bundle Bundle) error {
	if err := os.RemoveAll(installer.directory(bundle)); err != nil {
		return fmt.Errorf("remove seekdb runtime: %w", err)
	}
	return os.RemoveAll(filepath.Join(installer.Root, "staging"))
}

func (installer Installer) staging() (string, error) {
	staging := filepath.Join(installer.Root, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return "", fmt.Errorf("prepare seekdb runtime staging: %w", err)
	}
	return staging, nil
}

func (installer Installer) client() *http.Client {
	if installer.Client != nil {
		return installer.Client
	}
	return &http.Client{Timeout: downloadTimeout}
}

func (installer Installer) fetch(ctx context.Context, bundle Bundle, source source, archive string, progress Progress) error {
	if source.member == "" {
		if err := installer.download(ctx, source.url, archive+".part", bundle.Size, progress); err != nil {
			return err
		}
		if err := verifyArchive(archive+".part", bundle); err != nil {
			_ = os.Remove(archive + ".part")
			return err
		}
		return os.Rename(archive+".part", archive)
	}
	zipPath := archive + ".zip.part"
	_ = os.Remove(zipPath)
	defer os.Remove(zipPath)
	if err := installer.download(ctx, source.url, zipPath, 0, progress); err != nil {
		return err
	}
	if err := extractMember(zipPath, source.member, archive); err != nil {
		return err
	}
	if err := verifyArchive(archive, bundle); err != nil {
		_ = os.Remove(archive)
		return err
	}
	return nil
}

// download writes url to path, resuming from the bytes already there when
// size is known and the server honours the range.
func (installer Installer) download(ctx context.Context, url, path string, size int64, progress Progress) error {
	var offset int64
	if size > 0 {
		if info, err := os.Stat(path); err == nil && info.Size() < size {
			offset = info.Size()
		} else if err == nil {
			_ = os.Remove(path)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := installer.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case offset > 0 && response.StatusCode == http.StatusPartialContent:
		flags |= os.O_APPEND
	case response.StatusCode == http.StatusOK:
		offset = 0
		flags |= os.O_TRUNC
	default:
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	total := size
	if total == 0 && response.ContentLength > 0 {
		total = response.ContentLength
	}
	limit := int64(maxUnpackedBytes)
	if size > 0 {
		limit = size - offset
	}
	writer := &progressWriter{file: file, done: offset, total: total, report: progress}
	written, copyErr := io.Copy(writer, io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		return fmt.Errorf("download is larger than expected")
	}
	return nil
}

type progressWriter struct {
	file        *os.File
	done, total int64
	report      Progress
	last        time.Time
}

func (writer *progressWriter) Write(data []byte) (int, error) {
	count, err := writer.file.Write(data)
	writer.done += int64(count)
	if writer.report != nil && time.Since(writer.last) > 200*time.Millisecond {
		writer.last = time.Now()
		writer.report(writer.done, writer.total)
	}
	return count, err
}

func verifyArchive(path string, bundle Bundle) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if size != bundle.Size || hex.EncodeToString(hash.Sum(nil)) != bundle.SHA256 {
		return fmt.Errorf("the archive does not match seekdb %s for %s", Version, bundle.Platform)
	}
	return nil
}

func extractMember(zipPath, member, destination string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open module zip: %w", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != member {
			continue
		}
		if file.UncompressedSize64 > maxUnpackedBytes {
			return fmt.Errorf("module zip member is too large")
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		defer source.Close()
		target, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(target, io.LimitReader(source, maxUnpackedBytes))
		if closeErr := target.Close(); copyErr == nil {
			copyErr = closeErr
		}
		return copyErr
	}
	return fmt.Errorf("module zip has no %s", member)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, io.LimitReader(input, maxUnpackedBytes))
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

// unpack extracts a verified archive into a temporary directory beside the
// final one and renames it into place, so a half-written runtime is never
// mistaken for an installed one.
func (installer Installer) unpack(bundle Bundle, archive string) (Installation, error) {
	final := installer.directory(bundle)
	temporary := final + ".tmp"
	_ = os.RemoveAll(temporary)
	if err := os.MkdirAll(temporary, 0o700); err != nil {
		return Installation{}, fmt.Errorf("prepare seekdb runtime: %w", err)
	}
	files, err := extractTarGz(archive, temporary)
	if err == nil {
		err = writeManifest(temporary, bundle, files)
	}
	if err == nil {
		_ = os.RemoveAll(final)
		err = os.Rename(temporary, final)
	}
	if err != nil {
		_ = os.RemoveAll(temporary)
		return Installation{}, fmt.Errorf("unpack seekdb runtime: %w", err)
	}
	installation, ok := installer.Installed(bundle)
	if !ok {
		return Installation{}, fmt.Errorf("unpack seekdb runtime: the unpacked files do not match")
	}
	return installation, nil
}

func extractTarGz(archive, directory string) ([]manifestFile, error) {
	file, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var files []manifestFile
	var unpacked int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unexpected archive entry %q", header.Name)
		}
		name := header.Name
		if name == "" || name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("unexpected archive path %q", header.Name)
		}
		unpacked += header.Size
		if len(files) >= maxArchiveFiles || header.Size < 0 || unpacked > maxUnpackedBytes {
			return nil, fmt.Errorf("the archive is larger than a seekdb runtime")
		}
		mode := os.FileMode(0o644)
		if header.Mode&0o111 != 0 {
			mode = 0o755
		}
		target, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
		if err != nil {
			return nil, err
		}
		written, copyErr := io.Copy(target, io.LimitReader(reader, header.Size))
		if closeErr := target.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return nil, copyErr
		}
		if written != header.Size {
			return nil, fmt.Errorf("archive entry %q is truncated", name)
		}
		files = append(files, manifestFile{Name: name, Size: written})
	}
}

func writeManifest(directory string, bundle Bundle, files []manifestFile) error {
	found := false
	for _, file := range files {
		found = found || file.Name == bundle.Executable
	}
	if !found {
		return fmt.Errorf("the archive has no %s", bundle.Executable)
	}
	data, err := json.MarshalIndent(runtimeManifest{
		Version:       1,
		BundleSHA256:  bundle.SHA256,
		SeekdbVersion: Version,
		Executable:    bundle.Executable,
		Files:         files,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, manifestName), data, 0o600)
}
