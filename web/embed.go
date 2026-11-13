// Package web embeds the built React SPA bundle into the Go binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embeddedDist embed.FS

// DistFS returns the rooted static filesystem for SPA serving.
func DistFS() (fs.FS, error) {
	return fs.Sub(embeddedDist, "dist")
}
