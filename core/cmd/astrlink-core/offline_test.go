package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const (
	offlineRequestID = contract.RequestID("request_offline_show")
	offlineSecret    = "alice@example.com"
	offlinePassword  = "correct horse battery"
)

// newOfflineDataDir writes a database, its local key and one captured
// request with a shareable upstream body.
func newOfflineDataDir(t *testing.T, directory string) string {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(directory, "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: offlineRequestID, StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat,
		Audit:         contract.AuditRecordSummary{RequestBodyCaptured: true, UpstreamRequestBodyCaptured: true},
	}); err != nil {
		t.Fatal(err)
	}
	insertOfflinePart(t, store, storage.AuditDirectionUpstreamRequest, storage.AuditExposureShareable, `{"content":"mail <EMAIL_1>"}`)
	return directory
}

// captureOfflineRawBody captures the request's raw body the way Core does:
// the store seals it to the raw key, or keeps nothing without one.
func captureOfflineRawBody(t *testing.T, directory string) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(directory, "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	insertOfflinePart(t, store, storage.AuditDirectionRequest, storage.AuditExposureRaw, `{"content":"mail `+offlineSecret+`"}`)
}

func insertOfflinePart(t *testing.T, store *sqlite.Store, direction storage.AuditDirection, exposure storage.AuditExposure, plain string) {
	t.Helper()
	ctx := context.Background()
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	nonce, ciphertext, err := storage.SealAuditBlob(key, []byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAuditBlob(ctx, storage.AuditBlob{
		RequestID: offlineRequestID, Direction: direction, Exposure: exposure, MediaType: "application/json",
		Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: len(plain),
	}); err != nil {
		t.Fatal(err)
	}
}

type offlineRun struct {
	code           int
	stdout, stderr string
}

func runOffline(t *testing.T, stdin string, args ...string) offlineRun {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code, handled := runOfflineCommand(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	if !handled {
		t.Fatalf("%v was not handled as an offline command", args)
	}
	return offlineRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (run offlineRun) want(t *testing.T, code int, fragment string) {
	t.Helper()
	if run.code != code || !strings.Contains(run.stdout+run.stderr, fragment) {
		t.Fatalf("exit=%d want %d with %q\nstdout=%s\nstderr=%s", run.code, code, fragment, run.stdout, run.stderr)
	}
}

func showRequestJSON(t *testing.T, directory, stdin string, extra ...string) contract.AuditContent {
	t.Helper()
	args := append([]string{"audit", "show", string(offlineRequestID), "--data-dir", directory, "--json"}, extra...)
	run := runOffline(t, stdin, args...)
	run.want(t, 0, "")
	var content contract.AuditContent
	if err := json.Unmarshal([]byte(run.stdout), &content); err != nil {
		t.Fatalf("decode %s: %v", run.stdout, err)
	}
	return content
}

func TestOfflineCommandsLeaveServerArgumentsAlone(t *testing.T) {
	for _, args := range [][]string{nil, {"--data-dir", "x"}, {"-control-token-stdin"}} {
		if _, handled := runOfflineCommand(context.Background(), args, nil, nil, nil); handled {
			t.Fatalf("%v was taken as an offline command", args)
		}
	}
}

func TestOfflineCommandsRejectBadArguments(t *testing.T) {
	directory := t.TempDir()
	for _, testCase := range []struct {
		args     []string
		fragment string
	}{
		{[]string{"raw-password"}, "name one action"},
		{[]string{"raw-password", "rotate", "--data-dir", directory, "--password-stdin"}, "unknown action"},
		{[]string{"raw-password", "set", "--data-dir", directory}, "--password-stdin is required"},
		{[]string{"raw-password", "set", "--password-stdin"}, "--data-dir is required"},
		{[]string{"raw-password", "reset", "--data-dir", directory, "--password-stdin"}, "--yes"},
		{[]string{"raw-password", "set", "--password", offlinePassword}, "flag provided but not defined"},
		{[]string{"audit", "list", "--data-dir", directory}, "audit show REQUEST_ID"},
		{[]string{"audit", "show", "--data-dir", directory}, "audit show REQUEST_ID"},
		{[]string{"audit", "show", "not a request id", "--data-dir", directory}, "request id"},
	} {
		runOffline(t, offlinePassword+"\n", testCase.args...).want(t, 2, testCase.fragment)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		t.Fatalf("bad arguments left %d file(s) behind", len(entries))
	}
}

func TestOfflineCommandsNeverCreateADatabaseOrKey(t *testing.T) {
	empty := t.TempDir()
	runOffline(t, offlinePassword+"\n", "raw-password", "set", "--data-dir", empty, "--password-stdin").
		want(t, 1, "no AstrLink database")
	runOffline(t, "", "audit", "show", string(offlineRequestID), "--data-dir", empty).
		want(t, 1, "no AstrLink database")
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Fatalf("an offline command created %d file(s)", len(entries))
	}

	directory := newOfflineDataDir(t, t.TempDir())
	keyPath := filepath.Join(directory, localkey.FileName)
	if err := os.Rename(keyPath, keyPath+".moved"); err != nil {
		t.Fatal(err)
	}
	runOffline(t, "", "audit", "show", string(offlineRequestID), "--data-dir", directory).
		want(t, 1, "Keychain")
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an offline command created a local key")
	}
	wrongKey := filepath.Join(t.TempDir(), "other.key")
	if err := os.WriteFile(wrongKey, localkey.Encode(bytes.Repeat([]byte{7}, localkey.KeyBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	runOffline(t, offlinePassword+"\n", "raw-password", "set", "--data-dir", directory, "--kek-file", wrongKey, "--password-stdin").
		want(t, 1, "does not open this database")
	// The right key still opens everything afterwards.
	content := showRequestJSON(t, directory, "", "--kek-file", keyPath+".moved")
	if content.UpstreamRequestBody == nil || !strings.Contains(content.UpstreamRequestBody.Content, "<EMAIL_1>") {
		t.Fatalf("upstream body=%#v", content.UpstreamRequestBody)
	}
}

func TestOfflineRawPasswordSealsAndAuditShowReads(t *testing.T) {
	directory := newOfflineDataDir(t, t.TempDir())
	// Before a raw password exists, raw content is not kept.
	captureOfflineRawBody(t, directory)
	before := runOffline(t, "", "audit", "show", string(offlineRequestID), "--data-dir", directory, "--json")
	before.want(t, 0, "astrlink-core raw-password set")
	var content contract.AuditContent
	if err := json.Unmarshal([]byte(before.stdout), &content); err != nil {
		t.Fatal(err)
	}
	if content.RequestBody == nil || !content.RequestBody.Withheld || content.RequestBody.Reason != contract.AuditWithheldRawNotKept ||
		strings.Contains(before.stdout, offlineSecret) {
		t.Fatalf("request body before set=%#v", content.RequestBody)
	}
	if content.UpstreamRequestBody == nil || content.UpstreamRequestBody.Withheld {
		t.Fatalf("upstream body before set=%#v", content.UpstreamRequestBody)
	}

	runOffline(t, "short\n", "raw-password", "set", "--data-dir", directory, "--password-stdin").want(t, 1, "8 to 128")
	runOffline(t, offlinePassword, "raw-password", "set", "--data-dir", directory, "--password-stdin").
		want(t, 0, "raw password set")
	runOffline(t, offlinePassword+"\n", "raw-password", "set", "--data-dir", directory, "--password-stdin").
		want(t, 1, "already set")
	captureOfflineRawBody(t, directory)

	store, err := sqlite.Open(context.Background(), filepath.Join(directory, "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := store.GetAuditBlobsByRequest(context.Background(), offlineRequestID)
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range blobs {
		want := storage.AuditSealingAudit
		if blob.Exposure == storage.AuditExposureRaw {
			want = storage.AuditSealingRawV1
		}
		if blob.Sealing != want {
			t.Fatalf("%s part sealing=%q want %q", blob.Direction, blob.Sealing, want)
		}
	}

	runOffline(t, "", "audit", "show", string(offlineRequestID), "--data-dir", directory).
		want(t, 1, "--password-stdin")
	runOffline(t, "wrong password\n", "audit", "show", string(offlineRequestID), "--data-dir", directory, "--password-stdin").
		want(t, 1, "incorrect")
	content = showRequestJSON(t, directory, offlinePassword+"\r\n", "--password-stdin")
	if content.RequestBody == nil || !strings.Contains(content.RequestBody.Content, offlineSecret) || content.RequestBody.Exposure != contract.AuditPartExposureRaw {
		t.Fatalf("request body=%#v", content.RequestBody)
	}
	if content.UpstreamRequestBody == nil || strings.Contains(content.UpstreamRequestBody.Content, offlineSecret) {
		t.Fatalf("upstream body=%#v", content.UpstreamRequestBody)
	}
	text := runOffline(t, offlinePassword+"\n", "audit", "show", "--data-dir", directory, "--password-stdin", string(offlineRequestID))
	text.want(t, 0, "== request body (raw, application/json")
	if !strings.Contains(text.stdout, offlineSecret) {
		t.Fatalf("text output=%s", text.stdout)
	}

	runOffline(t, "wrong password\nanother phrase\n", "raw-password", "change", "--data-dir", directory, "--password-stdin").
		want(t, 1, "incorrect")
	runOffline(t, offlinePassword+"\n", "raw-password", "change", "--data-dir", directory, "--password-stdin").
		want(t, 1, "stdin ended before password line 2")
	runOffline(t, offlinePassword+"\nanother phrase\n", "raw-password", "change", "--data-dir", directory, "--password-stdin").
		want(t, 0, "raw password changed")
	content = showRequestJSON(t, directory, "another phrase\n", "--password-stdin")
	if !strings.Contains(content.RequestBody.Content, offlineSecret) {
		t.Fatalf("request body after change=%#v", content.RequestBody)
	}

	runOffline(t, "a third phrase\n", "raw-password", "reset", "--data-dir", directory, "--password-stdin", "--yes").
		want(t, 0, "discarded 1 raw part(s) from 1 request(s)")
	content = showRequestJSON(t, directory, "a third phrase\n", "--password-stdin")
	if content.RequestBody != nil || content.UpstreamRequestBody == nil {
		t.Fatalf("content after reset=%#v", content)
	}
}

// TestOfflineAuditShowReadsBesideARunningCore covers `docker exec` reads:
// audit show opens the database read-only while Core holds it.
func TestOfflineAuditShowReadsBesideARunningCore(t *testing.T) {
	directory := newOfflineDataDir(t, t.TempDir())
	runOffline(t, offlinePassword+"\n", "raw-password", "set", "--data-dir", directory, "--password-stdin").
		want(t, 0, "raw password set")
	captureOfflineRawBody(t, directory)
	path := filepath.Join(directory, "astrlink.db")
	serving, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer serving.Close()
	if err := serving.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: "request_while_serving", StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() [2][]byte {
		var files [2][]byte
		for index, name := range []string{path, path + "-wal"} {
			content, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			files[index] = content
		}
		return files
	}
	before := snapshot()
	content := showRequestJSON(t, directory, offlinePassword+"\n", "--password-stdin")
	if content.RequestBody == nil || !strings.Contains(content.RequestBody.Content, offlineSecret) {
		t.Fatalf("request body=%#v", content.RequestBody)
	}
	if after := snapshot(); !bytes.Equal(after[0], before[0]) || !bytes.Equal(after[1], before[1]) {
		t.Fatal("audit show changed the database beside a running Core")
	}
	if err := serving.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: "request_after_show", StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat,
	}); err != nil {
		t.Fatalf("Core cannot write after audit show: %v", err)
	}
}

func TestOfflineRawPasswordRefusesARunningCore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Core has no control socket on Windows")
	}
	// Unix socket paths are short; t.TempDir can exceed the limit on macOS.
	directory, err := os.MkdirTemp("", "alcli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	newOfflineDataDir(t, directory)
	listener, err := net.Listen("unix", filepath.Join(directory, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	runOffline(t, offlinePassword+"\n", "raw-password", "set", "--data-dir", directory, "--password-stdin").
		want(t, 1, "Core is running")
	// A socket left behind by a crashed Core does not block the command.
	_ = listener.Close()
	_ = os.WriteFile(filepath.Join(directory, "control.sock"), nil, 0o600)
	runOffline(t, offlinePassword+"\n", "raw-password", "set", "--data-dir", directory, "--password-stdin").
		want(t, 0, "raw password set")
}

func TestReadPasswordLines(t *testing.T) {
	values, buffer, err := readPasswordLines(iotest.OneByteReader(strings.NewReader("first é\r\nsecond")), 2)
	if err != nil || string(values[0]) != "first é" || string(values[1]) != "second" {
		t.Fatalf("values=%q err=%v", values, err)
	}
	clear(buffer)
	if strings.Trim(string(values[0])+string(values[1]), "\x00") != "" {
		t.Fatal("clearing the buffer left a password copy behind")
	}
	for _, input := range []string{"", "\n", "\r\n", "one\n", strings.Repeat("a", maxPasswordLineBytes+1) + "\n"} {
		if values, _, err := readPasswordLines(strings.NewReader(input), 1+strings.Count(input, "one")); err == nil {
			t.Fatalf("readPasswordLines(%q)=%q", input, values)
		}
	}
	if _, _, err := readPasswordLines(iotest.ErrReader(errors.New("boom")), 1); err == nil {
		t.Fatal("a read error was ignored")
	}
}
