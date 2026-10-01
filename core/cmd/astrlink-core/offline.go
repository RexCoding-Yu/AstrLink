package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

// Offline commands open the data directory directly, without a control
// token. Passwords come only from stdin so they never reach argv.

// errUsage marks a command line mistake; the command exits with status 2.
var errUsage = errors.New("usage")

// errUsageShown is a usage error the flag package has already printed.
var errUsageShown = fmt.Errorf("%w shown", errUsage)

// maxPasswordLineBytes bounds one stdin password line: 128 characters of
// up to four UTF-8 bytes each, a CR and the newline.
const maxPasswordLineBytes = 128*4 + 2

// runOfflineCommand runs the offline command named by args[0]. handled is
// false when args do not name one, so main starts the server as before.
func runOfflineCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (code int, handled bool) {
	if len(args) == 0 {
		return 0, false
	}
	var err error
	switch args[0] {
	case "raw-password":
		err = runRawPassword(ctx, args[1:], stdin, stdout, stderr)
	case "audit":
		err = runAudit(ctx, args[1:], stdin, stdout, stderr)
	default:
		return 0, false
	}
	switch {
	case err == nil:
		return 0, true
	case errors.Is(err, errUsage), errors.Is(err, flag.ErrHelp):
		if !errors.Is(err, flag.ErrHelp) && !errors.Is(err, errUsageShown) {
			fmt.Fprintf(stderr, "astrlink-core %s: %v\n", args[0], err)
		}
		return 2, true
	default:
		fmt.Fprintf(stderr, "astrlink-core %s: %v\n", args[0], err)
		return 1, true
	}
}

type offlineFlags struct {
	dataDir       string
	keyFile       string
	passwordStdin bool
}

func newOfflineFlagSet(name string, stderr io.Writer, options *offlineFlags) *flag.FlagSet {
	flags := flag.NewFlagSet("astrlink-core "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.dataDir, "data-dir", "", "AstrLink data directory")
	flags.StringVar(&options.keyFile, "kek-file", "", "local key file path; defaults to $"+localkey.EnvKeyFile+", then <data-dir>/"+localkey.FileName)
	flags.BoolVar(&options.passwordStdin, "password-stdin", false, "read the raw password from stdin")
	return flags
}

// parseInterspersed accepts flags before and after positional arguments.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, errUsageShown
		}
		if flags.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, flags.Arg(0))
		args = flags.Args()[1:]
	}
}

func runRawPassword(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var options offlineFlags
	confirmed := false
	flags := newOfflineFlagSet("raw-password", stderr, &options)
	flags.BoolVar(&confirmed, "yes", false, "confirm that reset discards every captured raw part")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: astrlink-core raw-password set|change|reset --data-dir DIR --password-stdin [--kek-file FILE] [--yes]")
		fmt.Fprintln(stderr, "  set:    stdin line 1 is the new password")
		fmt.Fprintln(stderr, "  change: stdin line 1 is the current password, line 2 the new one")
		fmt.Fprintln(stderr, "  reset:  stdin line 1 is the new password; captured raw parts are discarded (needs --yes)")
		flags.PrintDefaults()
	}
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		flags.Usage()
		return fmt.Errorf("%w: name one action: set, change, or reset", errUsage)
	}
	action := controlapi.RawPasswordAction(positional[0])
	lines := 1
	switch action {
	case controlapi.RawPasswordSet:
	case controlapi.RawPasswordChange:
		lines = 2
	case controlapi.RawPasswordReset:
		if !confirmed {
			return fmt.Errorf("%w: reset discards every captured raw part; pass --yes to confirm", errUsage)
		}
	default:
		return fmt.Errorf("%w: unknown action %q; use set, change, or reset", errUsage, positional[0])
	}
	if !options.passwordStdin {
		return fmt.Errorf("%w: --password-stdin is required; the password is never taken from arguments", errUsage)
	}
	if err := refuseRunningCore(options.dataDir); err != nil {
		return err
	}
	passwords, buffer, err := readPasswordLines(stdin, lines)
	if err != nil {
		return err
	}
	defer clear(buffer)
	store, err := openOfflineStore(ctx, options, stderr)
	if err != nil {
		return err
	}
	defer store.Close()
	vault := controlapi.NewRawVault(store, controlapi.RawVaultOptions{Logf: log.New(stderr, "astrlink-core: ", 0).Printf})
	var proof controlapi.RawProof
	newPassword := passwords[0]
	if action == controlapi.RawPasswordChange {
		proof.Password, newPassword = passwords[0], passwords[1]
	}
	outcome, err := vault.ChangePassword(ctx, action, newPassword, proof)
	if err != nil {
		return describeRawError(err)
	}
	switch action {
	case controlapi.RawPasswordSet:
		fmt.Fprintln(stdout, "raw password set")
	case controlapi.RawPasswordChange:
		fmt.Fprintln(stdout, "raw password changed")
	case controlapi.RawPasswordReset:
		fmt.Fprintf(stdout, "raw password reset; discarded %d raw part(s) from %d request(s)\n",
			outcome.Reset.DeletedParts, outcome.Reset.AffectedRecords)
	}
	return nil
}

func runAudit(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var options offlineFlags
	asJSON := false
	flags := newOfflineFlagSet("audit show", stderr, &options)
	flags.BoolVar(&asJSON, "json", false, "print the audit content as JSON")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: astrlink-core audit show REQUEST_ID --data-dir DIR [--password-stdin] [--json] [--kek-file FILE]")
		flags.PrintDefaults()
	}
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 || positional[0] != "show" {
		flags.Usage()
		return fmt.Errorf("%w: expected: audit show REQUEST_ID", errUsage)
	}
	id := contract.RequestID(positional[1])
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: request id: %v", errUsage, err)
	}
	var password, buffer []byte
	if options.passwordStdin {
		passwords, lineBuffer, err := readPasswordLines(stdin, 1)
		if err != nil {
			return err
		}
		password, buffer = passwords[0], lineBuffer
	}
	defer clear(buffer)
	// Read-only, so it is safe beside a serving Core (docker exec).
	store, err := openOfflineStore(ctx, options, stderr, sqlite.WithReadOnly())
	if err != nil {
		return err
	}
	defer store.Close()
	vault := controlapi.NewRawVault(store, controlapi.RawVaultOptions{Logf: log.New(stderr, "astrlink-core: ", 0).Printf, ReadOnly: true})
	status, err := vault.Status(ctx)
	if err != nil {
		return fmt.Errorf("read raw sealing state: %w", err)
	}
	var content contract.AuditContent
	switch {
	case status.PasswordSet && password == nil:
		return errors.New("captured raw content is sealed; pass the raw password with --password-stdin")
	case status.PasswordSet:
		err = vault.WithProof(ctx, controlapi.RawProof{Password: password}, func(opener controlapi.RawKeyOpener) error {
			var readErr error
			content, readErr = controlapi.ReadFullAudit(ctx, store, opener, id)
			return readErr
		})
	default:
		fmt.Fprintln(stderr, "astrlink-core audit show: no raw password is set; raw content is not kept until one is set with `astrlink-core raw-password set`")
		content, err = controlapi.ReadFullAudit(ctx, store, nil, id)
	}
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("request %s was not found", id)
		}
		return describeRawError(err)
	}
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(content)
	}
	writeAuditText(stdout, content)
	return nil
}

func describeRawError(err error) error {
	switch {
	case errors.Is(err, controlapi.ErrRawPasswordInvalid):
		return errors.New("the raw password is incorrect")
	case errors.Is(err, controlapi.ErrRawProofRequired):
		return errors.New("this raw key has no password envelope; set a raw password in the AstrLink app first")
	case errors.Is(err, controlapi.ErrRawNotConfigured):
		return errors.New("no raw password is set; use raw-password set")
	case errors.Is(err, controlapi.ErrRawPasswordAlreadySet):
		return errors.New("a raw password is already set; use raw-password change")
	case errors.Is(err, controlapi.ErrRawPasswordNotSet):
		return errors.New("no raw password is set; use raw-password set")
	case errors.Is(err, controlapi.ErrRawPasswordRequired), errors.Is(err, controlapi.ErrRawPasswordPolicy):
		return errors.New("the new raw password must contain 8 to 128 characters")
	}
	return err
}

func writeAuditText(output io.Writer, content contract.AuditContent) {
	fmt.Fprintf(output, "request %s\n", content.RequestID)
	if meta := content.HTTPMeta; meta != nil {
		fmt.Fprintf(output, "%s %s %s\n", meta.Method, meta.URL, meta.HTTPVersion)
	}
	if len(content.PrivacyFindings) > 0 {
		fmt.Fprintln(output, "privacy findings:")
		for _, finding := range content.PrivacyFindings {
			fmt.Fprintf(output, "  %s at %s (%d)\n", finding.Kind, finding.JSONPath, finding.Count)
		}
	}
	for _, section := range []struct {
		title string
		part  *contract.AuditContentPart
	}{
		{"request body", content.RequestBody},
		{"response", content.ResponseContent},
		{"upstream request body", content.UpstreamRequestBody},
		{"upstream response", content.UpstreamResponseContent},
	} {
		if section.part == nil {
			continue
		}
		part := section.part
		if part.Withheld {
			fmt.Fprintf(output, "\n== %s: withheld (%s) ==\n", section.title, part.Reason)
			continue
		}
		details := []string{string(part.Exposure)}
		if part.MediaType != "" {
			details = append(details, part.MediaType)
		}
		details = append(details, fmt.Sprintf("%d bytes captured", part.CapturedBytes))
		if part.Truncated {
			details = append(details, "truncated")
		}
		fmt.Fprintf(output, "\n== %s (%s) ==\n%s\n", section.title, strings.Join(details, ", "), part.Content)
	}
}

// openOfflineStore opens an existing database with its existing local key.
// It never creates either, and a key that does not open the data keys fails
// instead of setting them aside.
func openOfflineStore(ctx context.Context, options offlineFlags, stderr io.Writer, extra ...sqlite.Option) (*sqlite.Store, error) {
	if strings.TrimSpace(options.dataDir) == "" {
		return nil, fmt.Errorf("%w: --data-dir is required", errUsage)
	}
	path := filepath.Join(options.dataDir, "astrlink.db")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no AstrLink database at %s: %w", path, err)
	}
	keyFile := options.keyFile
	if keyFile == "" {
		keyFile = os.Getenv(localkey.EnvKeyFile)
	}
	keyPath := keyFile
	if keyPath == "" {
		keyPath = filepath.Join(options.dataDir, localkey.FileName)
	}
	if _, err := os.Stat(keyPath); err != nil {
		return nil, fmt.Errorf("no local key file at %s; on macOS the desktop app keeps this key in the Keychain, so use the app instead: %w", keyPath, err)
	}
	logger := log.New(stderr, "astrlink-core: ", 0)
	key, _, err := localkey.Resolve(localkey.Options{KeyFile: keyFile, DataDir: options.dataDir, Logf: logger.Printf})
	if err != nil {
		return nil, fmt.Errorf("load local key: %w", err)
	}
	defer clear(key)
	store, err := sqlite.Open(ctx, path, append([]sqlite.Option{
		sqlite.WithLocalKey(key), sqlite.WithLogger(logger.Printf), sqlite.WithExistingDatabase(),
	}, extra...)...)
	if errors.Is(err, sqlite.ErrLocalKeyMismatch) {
		return nil, fmt.Errorf("the local key at %s does not open this database; nothing was changed", keyPath)
	}
	if errors.Is(err, sqlite.ErrNeedsCoreStart) {
		return nil, fmt.Errorf("start AstrLink Core on this data directory once, then try again; nothing was changed: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return store, nil
}

// refuseRunningCore stops a writing command while a Core serves the data
// directory: that Core caches the raw key and would keep sealing to the old
// one. Core listens on <data-dir>/control.sock on Unix.
func refuseRunningCore(dataDir string) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("%w: --data-dir is required", errUsage)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	connection, err := net.DialTimeout("unix", filepath.Join(dataDir, "control.sock"), time.Second)
	if err != nil {
		return nil
	}
	_ = connection.Close()
	return errors.New("AstrLink Core is running on this data directory; stop it first (for Docker, run the command in a stopped container's volume)")
}

// readPasswordLines reads count passwords, one per line, into one buffer the
// caller clears. The last line may end at end of input instead of a newline.
func readPasswordLines(input io.Reader, count int) (values [][]byte, buffer []byte, err error) {
	if input == nil {
		return nil, nil, errors.New("stdin is unavailable")
	}
	buffer = make([]byte, 0, count*maxPasswordLineBytes)
	defer func() {
		if err != nil {
			clear(buffer)
			buffer, values = nil, nil
		}
	}()
	values = make([][]byte, count)
	var next [1]byte
	defer clear(next[:])
	for index := range values {
		start := len(buffer)
		complete := false
		for !complete {
			read, readErr := input.Read(next[:])
			if read == 1 {
				switch {
				case next[0] == '\n':
					complete = true
					continue
				case len(buffer)-start == maxPasswordLineBytes:
					return nil, nil, fmt.Errorf("password line %d is too long", index+1)
				}
				buffer = append(buffer, next[0])
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) && index == count-1 && len(buffer) > start {
					break
				}
				if errors.Is(readErr, io.EOF) {
					return nil, nil, fmt.Errorf("stdin ended before password line %d", index+1)
				}
				return nil, nil, fmt.Errorf("read password line %d: %w", index+1, readErr)
			}
		}
		end := len(buffer)
		if end > start && buffer[end-1] == '\r' {
			end--
		}
		if end == start {
			return nil, nil, fmt.Errorf("password line %d is empty", index+1)
		}
		values[index] = buffer[start:end:end]
	}
	return values, buffer, nil
}
