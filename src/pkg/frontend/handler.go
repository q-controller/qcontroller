package frontend

import (
	"embed"
	"net/http"
)

//go:embed generated/*
var webFS embed.FS

func Handler(basepath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := "generated/" + r.URL.Path[len(basepath):]
		if _, err := webFS.Open(path); err != nil {
			http.ServeFileFS(w, r, webFS, "generated/index.html")
			return
		}
		http.ServeFileFS(w, r, webFS, path)
	}
}
