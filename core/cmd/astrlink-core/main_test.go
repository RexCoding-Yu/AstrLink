package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
)

func TestReadStdinTokensAcceptsOneBoundedBase64URLLine(t *testing.T) {
	control := strings.Repeat("aB3_", 8)
	got, err := readStdinTokens(strings.NewReader(control+"\ntrailing pipe data"), stdinLineCount(false, false), false)
	if err != nil {
		t.Fatalf("readStdinTokens: %v", err)
	}
	if got.control != control || got.observer != "" {
		t.Fatalf("tokens = %+v", got)
	}
}

func TestReadStdinTokensRejectsMissingDelimiterLengthAndUnsafeValues(t *testing.T) {
	valid := strings.Repeat("a", 32)
	for _, value := range []string{
		"",
		valid,
		"\n",
		"short\n",
		strings.Repeat("a", 129) + "\n",
		strings.Repeat("a", 31) + "+\n",
		strings.Repeat("a", 31) + " \n",
	} {
		if tokens, err := readStdinTokens(strings.NewReader(value), stdinLineCount(false, false), false); err == nil {
			t.Fatalf("readStdinTokens(%q) = %+v", value, tokens)
		}
	}
}

func TestReadStdinTokensReadsObserverFromThirdLine(t *testing.T) {
	control := strings.Repeat("c", 43)
	observer := strings.Repeat("o", 43)
	got, err := readStdinTokens(strings.NewReader(control+"\n\n"+observer+"\nignored"), stdinLineCount(true, false), false)
	if err != nil {
		t.Fatalf("readStdinTokens: %v", err)
	}
	if got.control != control || got.observer != observer {
		t.Fatalf("tokens = %+v", got)
	}
}

func TestReadStdinTokensToleratesAbsentOrEmptyOptionalLines(t *testing.T) {
	control := strings.Repeat("c", 43)
	for _, input := range []string{
		control + "\n",
		control + "\n\n",
		control + "\n\n\n",
	} {
		got, err := readStdinTokens(strings.NewReader(input), stdinLineCount(true, false), false)
		if err != nil {
			t.Fatalf("readStdinTokens(%q): %v", input, err)
		}
		if got.control != control || got.observer != "" {
			t.Fatalf("readStdinTokens(%q) = %+v", input, got)
		}
	}
}

func TestReadStdinTokensRejectsBadObserverLines(t *testing.T) {
	control := strings.Repeat("c", 43)
	for _, input := range []string{
		control + "\n\n" + strings.Repeat("o", 43),
		control + "\n\nshort\n",
		control + "\n\n" + strings.Repeat("o", 42) + "=\n",
		control + "\n\n" + control + "\n",
		control + "\npartial",
	} {
		if tokens, err := readStdinTokens(strings.NewReader(input), stdinLineCount(true, false), false); err == nil {
			t.Fatalf("readStdinTokens(%q) = %+v", input, tokens)
		}
	}
}

func TestReadTokenLinesBoundsInput(t *testing.T) {
	if _, _, err := readTokenLines(strings.NewReader(strings.Repeat("a", 200)+"\n"), 1); err == nil {
		t.Fatal("an overlong line was accepted")
	}
	if _, _, err := readTokenLines(strings.NewReader("x\n"), maxStdinLines+1); err == nil {
		t.Fatal("an out-of-range line count was accepted")
	}
}

func TestStdinLineCountCoversEveryPromisedLine(t *testing.T) {
	for _, testCase := range []struct {
		observer, localKey bool
		want               int
	}{
		{false, false, 1},
		{false, true, 2},
		{true, false, 3},
		{true, true, 3},
	} {
		if got := stdinLineCount(testCase.observer, testCase.localKey); got != testCase.want {
			t.Fatalf("stdinLineCount(%v, %v) = %d, want %d", testCase.observer, testCase.localKey, got, testCase.want)
		}
	}
}

func TestReadStdinTokensReadsTheLocalKeyFromTheSecondLine(t *testing.T) {
	control := strings.Repeat("c", 43)
	observer := strings.Repeat("o", 43)
	key := bytes.Repeat([]byte{0xa5}, localkey.KeyBytes)
	line := strings.TrimSuffix(string(localkey.Encode(key)), "\n")
	for _, testCase := range []struct {
		input    string
		observer bool
	}{
		{control + "\n" + line + "\n", false},
		{control + "\n" + line + "\n" + observer + "\n", true},
		{control + "\n" + strings.ToUpper(line) + "\n", false},
	} {
		got, err := readStdinTokens(strings.NewReader(testCase.input+"trailing"), stdinLineCount(testCase.observer, true), true)
		if err != nil {
			t.Fatalf("readStdinTokens: %v", err)
		}
		if got.control != control || !bytes.Equal(got.localKey, key) {
			t.Fatalf("tokens = control %q, key matches %v", got.control, bytes.Equal(got.localKey, key))
		}
		if testCase.observer && got.observer != observer {
			t.Fatalf("observer = %q", got.observer)
		}
	}
}

func TestReadStdinTokensRejectsABadLocalKeyWithoutEchoingIt(t *testing.T) {
	control := strings.Repeat("c", 43)
	secret := strings.Repeat("9f", 31) + "zz"
	for _, input := range []string{
		control + "\n\n",
		control + "\n",
		control + "\n" + secret + "\n",
		control + "\n" + strings.Repeat("ab", 31) + "\n",
		control + "\n" + strings.Repeat("ab", 33) + "\n",
		control + "\n" + strings.Repeat("ab", 32),
	} {
		tokens, err := readStdinTokens(strings.NewReader(input), stdinLineCount(false, true), true)
		if err == nil {
			t.Fatalf("readStdinTokens(%q) accepted a bad key", input)
		}
		if tokens.localKey != nil || tokens.control != "" {
			t.Fatalf("a rejected read returned tokens")
		}
		if strings.Contains(err.Error(), "9f9f") || strings.Contains(err.Error(), "abab") {
			t.Fatalf("error echoes key material: %v", err)
		}
	}
	// Without --kek-stdin the reserved line is not read as a key.
	got, err := readStdinTokens(strings.NewReader(control+"\n"+secret+"\n"+strings.Repeat("o", 43)+"\n"), stdinLineCount(true, false), false)
	if err != nil || got.localKey != nil {
		t.Fatalf("reserved line handled as a key: %v", err)
	}
}

// oneByteReader fails the test if anything past the promised lines is read.
type oneByteReader struct {
	t      *testing.T
	data   []byte
	offset int
	limit  int
}

func (reader *oneByteReader) Read(buffer []byte) (int, error) {
	if reader.offset >= reader.limit {
		reader.t.Fatalf("read past the promised lines at byte %d", reader.offset)
	}
	if reader.offset >= len(reader.data) {
		return 0, io.EOF
	}
	count := copy(buffer, reader.data[reader.offset:reader.limit])
	reader.offset += count
	return count, nil
}

func TestReadTokenLinesStopsAtThePromisedLinesAndClearsOnError(t *testing.T) {
	control := strings.Repeat("c", 43)
	key := strings.Repeat("ab", 32)
	promised := control + "\n" + key + "\n"
	reader := &oneByteReader{t: t, data: []byte(promised + "never read"), limit: len(promised)}
	values, buffer, err := readTokenLines(reader, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(values[0]) != control || string(values[1]) != key {
		t.Fatalf("values = %q", values)
	}
	// Every value aliases the one buffer the caller clears.
	clear(buffer)
	if !bytes.Equal(values[1], make([]byte, len(key))) {
		t.Fatal("a value outlives the cleared buffer")
	}

	values, buffer, err = readTokenLines(strings.NewReader(control+"\n"+key), 2)
	if err == nil || values != nil || buffer != nil {
		t.Fatalf("truncated key line: values=%q err=%v", values, err)
	}
	if _, _, err := readTokenLines(errorReader{}, 1); err == nil {
		t.Fatal("a read error was accepted")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestLocalKeyFlagsNeverCarryTheKey(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	// The key reaches Core only on stdin or in a file; flags and the
	// environment name a path at most.
	for _, fragment := range []string{
		`flag.BoolVar(&localKeyStdin, "kek-stdin"`,
		`flag.StringVar(&localKeyFile, "kek-file"`,
		`localKeyFile = os.Getenv(localkey.EnvKeyFile)`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("main.go no longer has %q", fragment)
		}
	}
	for _, forbidden := range []string{`"kek"`, `"local-key"`, `ASTRLINK_KEK"`, `ASTRLINK_LOCAL_KEY`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("main.go accepts key material through %s", forbidden)
		}
	}
}

type fixedRawStatus struct {
	status controlapi.RawVaultStatus
	err    error
}

func (reader fixedRawStatus) Status(context.Context) (controlapi.RawVaultStatus, error) {
	return reader.status, reader.err
}

func TestStartWarnsOnceWithoutARawPassword(t *testing.T) {
	for _, test := range []struct {
		name   string
		reader fixedRawStatus
		want   string
	}{
		{name: "no raw key", reader: fixedRawStatus{}, want: "astrlink-core raw-password set --data-dir /data --password-stdin"},
		{name: "raw password set", reader: fixedRawStatus{status: controlapi.RawVaultStatus{Configured: true, PasswordSet: true}}},
		{name: "unreadable state", reader: fixedRawStatus{err: errors.New("database is locked")}, want: "read raw sealing state"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var lines []string
			warnWithoutRawPassword(context.Background(), test.reader, "/data", func(format string, args ...any) {
				lines = append(lines, fmt.Sprintf(format, args...))
			})
			if test.want == "" {
				if len(lines) != 0 {
					t.Fatalf("logged %q", lines)
				}
				return
			}
			if len(lines) != 1 || !strings.Contains(lines[0], test.want) {
				t.Fatalf("logged %q, want one line with %q", lines, test.want)
			}
		})
	}
}
