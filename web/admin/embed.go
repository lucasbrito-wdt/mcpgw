// Package adminweb embeds the static export of the admin and portal web app (web/admin/out).
// A fresh clone only has out/.keep; the Go handler then serves the "UI não compilada" page.
package adminweb

import (
	"embed"
	"io/fs"
)

//go:embed all:out
var out embed.FS

// FS is the export root: admin/index.html, portal/index.html, _next/static/...
var FS = mustSub(out, "out")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
