package plugin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"fmt"
	"hash"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
)

//go:embed prelude.js
var preludeSource string

// prelude is the script that adds the web APIs to every plugin runtime,
// compiled once.
var prelude = sync.OnceValues(func() (*moejs.Script, error) {
	return moejs.CompileScript(preludeName, preludeSource)
})

// preludeName is the prelude's name in stack traces.
const preludeName = "astrlink:prelude"

// processStart is the zero of performance.now.
var processStart = time.Now()

// maxRandomBytes is what crypto.getRandomValues fills at most.
const maxRandomBytes = 65536

// installWeb adds the timers and the web APIs of prelude.js to the runtime.
// The timers and fetch work only while an apiVersion 2 hook runs; the rest
// works anywhere.
func (bound *instance) installWeb() error {
	runtime := bound.runtime
	current := func(realm *moejs.Realm, name string) (*call, error) {
		c := bound.current
		if c == nil || c.loop == nil {
			return nil, realm.TypeError("%s works only while the host runs a hook of an apiVersion 2 plugin", name)
		}
		return c, nil
	}
	timer := func(name string, repeat bool) moejs.Value {
		return runtime.Function(name, 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c, err := current(realm, name)
			if err != nil {
				return moejs.Undefined(), err
			}
			callback := moejs.Arg(args, 0)
			if !engine.IsCallable(callback) {
				return moejs.Undefined(), realm.TypeError("%s takes a function", name)
			}
			delay, err := timerDelay(realm, moejs.Arg(args, 1))
			if err != nil {
				return moejs.Undefined(), err
			}
			var extra []moejs.Value
			if len(args) > 2 {
				extra = append(extra, args[2:]...)
			}
			id, ok := c.loop.setTimer(callback, extra, delay, repeat)
			if !ok {
				return moejs.Undefined(), realm.RangeError("a call may have at most %d timers at once", maxTimers)
			}
			return moejs.Number(float64(id)), nil
		})
	}
	clearer := func(name string) moejs.Value {
		return runtime.Function(name, 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil || c.loop == nil {
				return moejs.Undefined(), nil
			}
			number, err := realm.ToNumber(moejs.Arg(args, 0))
			if err != nil || number != number {
				return moejs.Undefined(), nil
			}
			c.loop.clearTimer(int64(number))
			return moejs.Undefined(), nil
		})
	}
	for name, value := range map[string]moejs.Value{
		"setTimeout":    timer("setTimeout", false),
		"setInterval":   timer("setInterval", true),
		"clearTimeout":  clearer("clearTimeout"),
		"clearInterval": clearer("clearInterval"),
	} {
		if err := runtime.SetGlobal(name, value); err != nil {
			return err
		}
	}
	host := map[string]any{
		"fetch": runtime.Function("fetch", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c, err := current(realm, "fetch")
			if err != nil {
				return moejs.Undefined(), err
			}
			return hostRawFetch(c, realm, args)
		}),
		"log": runtime.Function("log", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil {
				// A module's top level has no call to log for.
				return moejs.Undefined(), nil
			}
			return hostLog(c, realm, args)
		}),
		"randomBytes": runtime.Function("randomBytes", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			number, err := realm.ToNumber(moejs.Arg(args, 0))
			if err != nil || number != number || number < 0 || number > maxRandomBytes {
				return moejs.Undefined(), realm.RangeError("crypto.getRandomValues fills at most %d bytes", maxRandomBytes)
			}
			data := make([]byte, int(number))
			_, _ = rand.Read(data)
			return moejs.String(base64.StdEncoding.EncodeToString(data)), nil
		}),
		"randomUUID": runtime.Function("randomUUID", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.String(randomUUID()), nil
		}),
		"digest": runtime.Function("digest", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "algorithm")
			if err != nil {
				return moejs.Undefined(), err
			}
			newHash := hashOf(name)
			if newHash == nil {
				return moejs.Undefined(), realm.TypeError("crypto.subtle.digest supports SHA-1, SHA-256, SHA-384 and SHA-512, not %q", name)
			}
			data, err := base64Arg(realm, args, 1, "data")
			if err != nil {
				return moejs.Undefined(), err
			}
			sum := newHash()
			sum.Write(data)
			return moejs.String(base64.StdEncoding.EncodeToString(sum.Sum(nil))), nil
		}),
		"hmac": runtime.Function("hmac", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "hash")
			if err != nil {
				return moejs.Undefined(), err
			}
			newHash := hashOf(name)
			if newHash == nil {
				return moejs.Undefined(), realm.TypeError("HMAC supports SHA-1, SHA-256, SHA-384 and SHA-512, not %q", name)
			}
			key, err := base64Arg(realm, args, 1, "key")
			if err != nil {
				return moejs.Undefined(), err
			}
			data, err := base64Arg(realm, args, 2, "data")
			if err != nil {
				return moejs.Undefined(), err
			}
			mac := hmac.New(newHash, key)
			mac.Write(data)
			return moejs.String(base64.StdEncoding.EncodeToString(mac.Sum(nil))), nil
		}),
		"parseURL": runtime.Function("parseURL", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			input, err := stringArg(realm, args, 0, "url")
			if err != nil {
				return moejs.Undefined(), err
			}
			base, hasBase := "", !moejs.Arg(args, 1).IsUndefined()
			if hasBase {
				if base, err = stringArg(realm, args, 1, "base"); err != nil {
					return moejs.Undefined(), err
				}
			}
			parts, ok := parseURL(input, base, hasBase)
			if !ok {
				return moejs.Null(), nil
			}
			return realm.FromGo(parts)
		}),
		"now": runtime.Function("now", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.Number(float64(time.Since(processStart).Microseconds()) / 1000), nil
		}),
	}
	if err := runtime.SetGlobal("__astrlinkHost", host); err != nil {
		return err
	}
	script, err := prelude()
	if err != nil {
		return fmt.Errorf("compile the plugin prelude: %w", err)
	}
	if _, err := runtime.RunScript(script); err != nil {
		return fmt.Errorf("run the plugin prelude: %w", err)
	}
	return nil
}

func hashOf(name string) func() hash.Hash {
	switch strings.ToUpper(name) {
	case "SHA-1":
		return sha1.New
	case "SHA-256":
		return sha256.New
	case "SHA-384":
		return sha512.New384
	case "SHA-512":
		return sha512.New
	}
	return nil
}

func base64Arg(realm *moejs.Realm, args []moejs.Value, index int, name string) ([]byte, error) {
	text, err := stringArg(realm, args, index, name)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, realm.TypeError("the %s is not base64", name)
	}
	return data, nil
}

// randomUUID returns a version 4 UUID.
func randomUUID() string {
	var data [16]byte
	_, _ = rand.Read(data[:])
	data[6] = data[6]&0x0f | 0x40
	data[8] = data[8]&0x3f | 0x80
	text := fmt.Sprintf("%x", data[:])
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:32]
}

// specialPorts are the default ports of the schemes URL treats as special.
var specialPorts = map[string]string{"http": "80", "https": "443", "ws": "80", "wss": "443", "ftp": "21", "file": ""}

// parseURL parses input against base, when there is one, the way the URL
// constructor does, as far as net/url allows: special schemes need a host
// and get a path of at least "/", the default port goes, and the scheme
// and host are lowercase. It returns the parts URL keeps.
func parseURL(input, base string, hasBase bool) (map[string]any, bool) {
	input = cleanURLText(input)
	var parsed *url.URL
	if hasBase {
		root, err := url.Parse(cleanURLText(base))
		if err != nil || root.Scheme == "" {
			return nil, false
		}
		reference, err := url.Parse(input)
		if err != nil {
			return nil, false
		}
		parsed = root.ResolveReference(reference)
	} else {
		var err error
		if parsed, err = url.Parse(input); err != nil || parsed.Scheme == "" {
			return nil, false
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	defaultPort, special := specialPorts[scheme]
	if special && parsed.Opaque != "" {
		// "https:example.com" means https://example.com/ to a browser.
		reparsed, err := url.Parse(scheme + "://" + strings.TrimLeft(parsed.Opaque, "/") + querySuffix(parsed) + fragmentSuffix(parsed))
		if err != nil {
			return nil, false
		}
		parsed = reparsed
	}
	parts := map[string]any{
		"protocol": scheme + ":",
		"username": "",
		"password": "",
		"hostname": "",
		"port":     "",
		"pathname": "",
		"search":   querySuffix(parsed),
		"hash":     fragmentSuffix(parsed),
		"opaque":   false,
	}
	if parsed.Opaque != "" || (!special && parsed.Host == "" && !strings.HasPrefix(parsed.Path, "/")) {
		parts["opaque"] = true
		parts["pathname"] = parsed.Opaque
		if parsed.Opaque == "" {
			parts["pathname"] = parsed.EscapedPath()
		}
		return parts, true
	}
	hostname := strings.ToLower(parsed.Hostname())
	if special && scheme != "file" && hostname == "" {
		return nil, false
	}
	if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	port := parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number > 65535 {
			return nil, false
		}
		port = strconv.Itoa(number)
		if special && port == defaultPort {
			port = ""
		}
	}
	if parsed.User != nil {
		name, password, _ := strings.Cut(parsed.User.String(), ":")
		parts["username"], parts["password"] = name, password
	}
	path := removeDotSegments(parsed.EscapedPath())
	if special && path == "" {
		path = "/"
	}
	parts["hostname"] = hostname
	parts["port"] = port
	parts["pathname"] = path
	if _, _, err := net.SplitHostPort(hostname + ":0"); err != nil && !strings.HasPrefix(hostname, "[") {
		return nil, false
	}
	return parts, true
}

func querySuffix(parsed *url.URL) string {
	if parsed.RawQuery == "" {
		return ""
	}
	return "?" + encodeQuery(parsed.RawQuery)
}

// encodeQuery percent-encodes what the URL parser encodes in a query: bytes
// outside printable ASCII, the space, ", # , < and >.
func encodeQuery(query string) string {
	var builder strings.Builder
	for index := 0; index < len(query); index++ {
		char := query[index]
		if char <= ' ' || char >= 0x7f || char == '"' || char == '#' || char == '<' || char == '>' {
			fmt.Fprintf(&builder, "%%%02X", char)
			continue
		}
		builder.WriteByte(char)
	}
	return builder.String()
}

// removeDotSegments resolves the . and .. segments of a path, as RFC 3986
// does, keeping a trailing slash.
func removeDotSegments(path string) string {
	if !strings.Contains(path, ".") {
		return path
	}
	segments := strings.Split(path, "/")
	var kept []string
	for index, segment := range segments {
		last := index == len(segments)-1
		switch segment {
		case ".":
			if last {
				kept = append(kept, "")
			}
		case "..":
			if len(kept) > 1 {
				kept = kept[:len(kept)-1]
			}
			if last {
				kept = append(kept, "")
			}
		default:
			kept = append(kept, segment)
		}
	}
	resolved := strings.Join(kept, "/")
	if strings.HasPrefix(path, "/") && !strings.HasPrefix(resolved, "/") {
		resolved = "/" + resolved
	}
	return resolved
}

func fragmentSuffix(parsed *url.URL) string {
	if parsed.Fragment == "" {
		return ""
	}
	return "#" + parsed.EscapedFragment()
}

// cleanURLText removes what the URL parser ignores: leading and trailing C0
// controls and spaces, and tabs and line breaks anywhere.
func cleanURLText(text string) string {
	text = strings.TrimFunc(text, func(r rune) bool { return r <= ' ' })
	return strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(text)
}
