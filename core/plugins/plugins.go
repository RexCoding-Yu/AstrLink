// Package plugins holds the plugins built into Core.
package plugins

import "embed"

// Builtin holds each builtin plugin as <key>/plugin.ts, with its icon as
// <key>/icon.png. A plugin of the same key in the plugin directory replaces
// it.
//
//go:embed */plugin.ts */icon.png
var Builtin embed.FS
