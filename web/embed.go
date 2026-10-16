// Package web embeds Blackbox's static assets and HTML templates.
package web

import "embed"

// FS holds the embedded templates and static assets.
//
//go:embed templates static
var FS embed.FS
