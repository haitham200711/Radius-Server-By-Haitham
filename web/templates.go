package web

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
)

type Renderer struct {
	templates map[string]*template.Template
	baseDir   string
}

func NewRenderer(baseDir string) (*Renderer, error) {
	if baseDir == "" {
		baseDir = "."
	}

	r := &Renderer{
		templates: make(map[string]*template.Template),
		baseDir:   baseDir,
	}

	pages := map[string][]string{
		"auth/login":          {"templates/base.html", "templates/auth/login.html"},
		"dashboard/index":     {"templates/base.html", "templates/dashboard/index.html"},
		"subscribers/list":    {"templates/base.html", "templates/subscribers/list.html"},
		"subscribers/add":     {"templates/base.html", "templates/subscribers/add.html"},
		"subscribers/edit":    {"templates/base.html", "templates/subscribers/edit.html"},
		"cards/list":          {"templates/base.html", "templates/cards/list.html"},
		"cards/generate":      {"templates/base.html", "templates/cards/generate.html"},
		"cards/print":         {"templates/cards/print.html"},
		"card_templates/list": {"templates/base.html", "templates/card_templates/list.html"},
		"profiles/list":          {"templates/base.html", "templates/profiles/list.html"},
		"profiles/speed_control": {"templates/base.html", "templates/profiles/speed_control.html"},
		"routers/list":           {"templates/base.html", "templates/routers/list.html"},
		"resellers/list":         {"templates/base.html", "templates/resellers/list.html"},
		"sessions/list":          {"templates/base.html", "templates/sessions/list.html"},
		"sstp/index":             {"templates/base.html", "templates/sstp/index.html"},
		"reports/index":          {"templates/base.html", "templates/reports/index.html"},
		"settings/index":         {"templates/base.html", "templates/settings/index.html"},
		"whatsapp/index":         {"templates/base.html", "templates/whatsapp/index.html"},
	}

	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}

	for key, files := range pages {
		var fullPaths []string
		for _, f := range files {
			fullPaths = append(fullPaths, filepath.Join(baseDir, f))
		}
		tmpl, err := template.New(filepath.Base(fullPaths[0])).Funcs(funcMap).ParseFiles(fullPaths...)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", key, err)
		}
		r.templates[key] = tmpl
	}

	return r, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data interface{}) error {
	tmpl, ok := r.templates[name]
	if !ok {
		return fmt.Errorf("template %s not found", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if name == "cards/print" {
		return tmpl.Execute(w, data)
	}
	return tmpl.ExecuteTemplate(w, "base.html", data)
}

func StaticHandler(staticDir string) http.Handler {
	if staticDir == "" {
		staticDir = "./static"
	}
	if _, err := os.Stat(staticDir); err != nil {
		_ = os.MkdirAll(staticDir, 0755)
	}
	return http.FileServer(http.Dir(staticDir))
}
