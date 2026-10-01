package buildinfo

import "runtime/debug"

// Version is replaced with -ldflags by the desktop sidecar build, which reads
// the release-tag version stamped into apps/desktop/package.json.
var (
	Version = "dev"
	Commit  = "unknown"
)

func init() {
	if Commit != "unknown" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			Commit = setting.Value
			return
		}
	}
}
