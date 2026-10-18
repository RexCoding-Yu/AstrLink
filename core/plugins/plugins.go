// Package plugins holds the plugins built into Core.
package plugins

import "embed"

// Builtin holds each builtin plugin as <key>/plugin.ts. A plugin of the same
// key in the plugin directory replaces it.
//
//go:embed */plugin.ts
var Builtin embed.FS
