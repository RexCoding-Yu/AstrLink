// Package seekdb runs the local seekdb server behind the knowledge base. It
// installs the pinned runtime on demand, starts it in embedded mode with
// telemetry off, and serves a database/sql pool over the server's local
// endpoint: a Unix socket, or a named pipe on Windows. The server never
// listens on TCP.
package seekdb

import (
	"fmt"
	"runtime"
)

// The runtimes are the bundle.tar.gz archives of the seekdb-go native
// modules: each holds the seekdb executable and the libraries it loads. All
// four v1.4.0 module tags point at one commit, which pins the raw download.
const (
	nativeCommit  = "f6a2a48ea9877459c4e1bf4506f80ec88a78e977"
	nativeVersion = "v1.4.0"
	// Version is the seekdb release the pinned runtimes carry.
	Version = "1.4.0.0-r1"
)

// Bundle pins one platform's runtime archive.
type Bundle struct {
	Platform   string
	SHA256     string
	Size       int64
	Executable string
}

var bundles = map[string]Bundle{
	"darwin/arm64": {
		Platform:   "darwin-arm64",
		SHA256:     "8267306ccd5e38076df790b025f01b0f403f9524bc98529573eac0e305e8c583",
		Size:       52007730,
		Executable: "seekdb.bin",
	},
	"linux/amd64": {
		Platform:   "linux-amd64",
		SHA256:     "73bae794192b643a4b836cedb1b7f6dd5b7fa5210654d57b804bdc74fe2d1cc7",
		Size:       53574813,
		Executable: "seekdb.bin",
	},
	"linux/arm64": {
		Platform:   "linux-arm64",
		SHA256:     "91b28ab4e0c4c15bab1c7972aec212c9aaee19bc0ce57e25fbff62d2763ca647",
		Size:       49231485,
		Executable: "seekdb.bin",
	},
	"windows/amd64": {
		Platform:   "windows-amd64",
		SHA256:     "3a50225198ac67f26f1777dd02cd6a5357ceb0a7ed33fa78db05a2e7a33af295",
		Size:       64919314,
		Executable: "seekdb.exe",
	},
}

// CurrentBundle returns the runtime for this build's platform. seekdb ships
// none for Intel Macs, Windows on Arm or other systems.
func CurrentBundle() (Bundle, bool) {
	bundle, ok := bundles[runtime.GOOS+"/"+runtime.GOARCH]
	return bundle, ok
}

// source is one place to fetch a bundle from. A Go module zip carries the
// archive at member; a direct download is the archive itself.
type source struct {
	url    string
	member string
}

// sources lists the GitHub raw file first, then the module zip from the Go
// module proxy and from goproxy.cn, which mainland users reach more easily.
// Every source is checked against the pinned digest.
func (bundle Bundle) sources() []source {
	module := "github.com/oceanbase/seekdb-go/native/" + bundle.Platform
	member := fmt.Sprintf("%s@%s/bundle.tar.gz", module, nativeVersion)
	return []source{
		{url: fmt.Sprintf("https://raw.githubusercontent.com/oceanbase/seekdb-go/%s/native/%s/bundle.tar.gz", nativeCommit, bundle.Platform)},
		{url: fmt.Sprintf("https://proxy.golang.org/%s/@v/%s.zip", module, nativeVersion), member: member},
		{url: fmt.Sprintf("https://goproxy.cn/%s/@v/%s.zip", module, nativeVersion), member: member},
	}
}
