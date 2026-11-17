// Package web embeds the built Vue frontend (web/dist).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the frontend files rooted at dist/.
func FS() (fs.FS, error) { return fs.Sub(dist, "dist") }
