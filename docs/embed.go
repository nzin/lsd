// Package docs embeds the installation guide (source and PDF) into the program, so that
// `lsd get-documentation` can hand it out. install.pdf is generated from install.md
// with `make docs` and is committed.
package docs

import "embed"

//go:embed install.md install.pdf
var Files embed.FS
