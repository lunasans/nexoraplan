// Package web bettet die gebaute Web-Oberfläche in die Go-Binary ein.
//
// dist/ ist der Build-Output der SvelteKit-App aus frontend/
// ("npm run build" im Ordner frontend). Vor "go build" muss er existieren.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // kann nur bei einem Tippfehler im Pfad passieren
	}
	return sub
}
