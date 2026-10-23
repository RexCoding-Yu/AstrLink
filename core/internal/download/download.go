// Package download fetches large pinned files: a download resumes from the
// bytes already on disk when the size is known and the server honours the
// range, and every file is checked against its pinned digest before use.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// MaxBytes caps a download whose size is not pinned.
const MaxBytes = 2 << 30

// ErrMismatch reports a file whose size or digest differs from its pin.
var ErrMismatch = errors.New("the file does not match its pinned digest")

// Progress reports bytes on disk so far against the expected total; total is
// 0 while a file of unknown size downloads.
type Progress func(done, total int64)

// File writes url to path. With size > 0 it resumes a shorter file at path
// and fails on a longer response; with size 0 it starts afresh.
func File(ctx context.Context, client *http.Client, url, path string, size int64, progress Progress) error {
	var offset int64
	if info, err := os.Stat(path); err == nil {
		if size > 0 && info.Size() < size {
			offset = info.Size()
		} else {
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
	response, err := client.Do(request)
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
	limit := int64(MaxBytes)
	if size > 0 {
		limit = size - offset
	}
	writer := &progressWriter{file: file, done: offset, total: total, report: progress}
	written, copyErr := io.Copy(writer, io.LimitReader(response.Body, limit+1))
	if progress != nil {
		progress(writer.done, total)
	}
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		return fmt.Errorf("the download is larger than expected")
	}
	return nil
}

// Verify checks path against a pinned size and SHA-256.
func Verify(path string, size int64, digest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if written != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return ErrMismatch
	}
	return nil
}

// Client returns client, or a default one sized for large files.
func Client(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 60 * time.Minute}
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
