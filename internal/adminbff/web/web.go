package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

// Assets are embedded so the console has no CDN or Node runtime dependency.
// Versions and SHA-256 values are recorded in docs/plan/47-a5-admin-console.md.
//
//go:embed assets/*
var Assets embed.FS

// PageData is deliberately small: templates receive no downstream payloads or
// credentials. Operators use htmx requests to the BFF for live data.
type PageData struct {
	Title     string
	Page      string
	CSRFToken string
	Role      string
	IsMaker   bool
	IsChecker bool
}

func Render(w http.ResponseWriter, page string, data PageData) error {
	tmpl, err := template.ParseFS(templates, "templates/layout.html", "templates/"+page+".html")
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tmpl.ExecuteTemplate(w, "layout", data)
}

//go:embed templates/*
var templates embed.FS

func AssetHandler() http.Handler {
	assets, err := fs.Sub(Assets, "assets")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(assets))
}
