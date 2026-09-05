// Package web carries the built single-page app so the server ships as one
// binary with nothing to install alongside it.
//
// dist is produced by `npm run build` in this directory. It is committed as a
// build artifact because `go build` cannot run npm, and a source-only checkout
// would otherwise produce a binary that serves nothing.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built app rooted at dist, or false when the directory holds
// only its placeholder — which is what a checkout looks like before the
// frontend has been built.
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
