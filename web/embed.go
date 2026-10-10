// Package web embeds the static frontend (HTML/CSS/JS, no build step)
// served by the API for every non-/api path.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// Static returns the embedded UI files rooted at the static/ directory,
// ready for http.FileServer(http.FS(web.Static())).
func Static() fs.FS {
	// fs.Sub cannot fail here: "static" is embedded above.
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
