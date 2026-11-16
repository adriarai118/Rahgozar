// Package frontend embeds the user interface.
package frontend

import "embed"

// Assets holds the built UI.
//
//go:embed all:dist
var Assets embed.FS
