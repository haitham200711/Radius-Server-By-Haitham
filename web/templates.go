package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		"cards/cafe_portal":   {"templates/base.html", "templates/cards/cafe_portal.html"},
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
		"backups/index":          {"templates/base.html", "templates/backups/index.html"},
		"portal/login":           {"templates/portal/login.html"},
		"portal/index":           {"templates/portal/index.html"},
	}

	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"bytesToGB": func(b int64) float64 {
			return float64(b) / (1024 * 1024 * 1024)
		},
		"formatGB": func(b int64) string {
			if b <= 0 {
				return "0"
			}
			return fmt.Sprintf("%.1f", float64(b)/(1024*1024*1024))
		},
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
		err := fmt.Errorf("template '%s' not registered in Renderer", name)
		log.Printf("[RENDER ERROR] %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if name == "cards/print" || strings.HasPrefix(name, "portal/") {
		return tmpl.Execute(w, data)
	}
	if err := tmpl.ExecuteTemplate(w, "base.html", data); err != nil {
		log.Printf("[RENDER ERROR] Template %s failed to execute: %v\n", name, err)
		http.Error(w, "Template execution error: "+err.Error(), http.StatusInternalServerError)
		return err
	}
	return nil
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
