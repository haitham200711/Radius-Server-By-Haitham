package card_templates

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/web"
)

type Blueprint struct {
	db       *database.DB
	renderer *web.Renderer
}

func New(db *database.DB, r *web.Renderer) *Blueprint {
	return &Blueprint{db: db, renderer: r}
}

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	list, err := bp.db.GetCardTemplates()
	if err != nil {
		list = []models.CardTemplate{}
	}

	_ = bp.renderer.Render(w, "card_templates/list", map[string]interface{}{
		"Title":       "تصاميم وأشكال الكروت",
		"ActiveMenu":  "card_templates",
		"CurrentUser": middleware.GetUser(r),
		"Templates":   list,
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/card-templates", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	bgColor := strings.TrimSpace(r.FormValue("background_color"))
	if bgColor == "" {
		bgColor = "#0d6efd"
	}
	widthMM, _ := strconv.ParseFloat(r.FormValue("width_mm"), 64)
	if widthMM <= 0 {
		widthMM = 85.60
	}
	heightMM, _ := strconv.ParseFloat(r.FormValue("height_mm"), 64)
	if heightMM <= 0 {
		heightMM = 54.00
	}

	showQR := r.FormValue("show_qr") == "1" || r.FormValue("show_qr") == "on"
	showBarcode := r.FormValue("show_barcode") == "1" || r.FormValue("show_barcode") == "on"
	showPrice := r.FormValue("show_price") == "1" || r.FormValue("show_price") == "on"
	showProfile := r.FormValue("show_profile") == "1" || r.FormValue("show_profile") == "on"
	showExpiry := r.FormValue("show_expiry") == "1" || r.FormValue("show_expiry") == "on"
	isDefault := r.FormValue("is_default") == "1" || r.FormValue("is_default") == "on"

	if name != "" {
		t := &models.CardTemplate{
			Name:            name,
			BackgroundColor: bgColor,
			WidthMM:         widthMM,
			HeightMM:        heightMM,
			ShowQR:          showQR,
			ShowBarcode:     showBarcode,
			ShowPrice:       showPrice,
			ShowProfile:     showProfile,
			ShowExpiry:      showExpiry,
			IsDefault:       isDefault,
		}
		_ = bp.db.SaveCardTemplate(t)
	}

	http.Redirect(w, r, "/card-templates", http.StatusFound)
}

func (bp *Blueprint) EditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		idStr := r.URL.Query().Get("id")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		t, err := bp.db.GetCardTemplate(id)
		if err != nil {
			http.Error(w, "Template not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(t)
		return
	}

	if r.Method == http.MethodPost {
		idStr := r.FormValue("id")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		t, err := bp.db.GetCardTemplate(id)
		if err != nil {
			http.Redirect(w, r, "/card-templates", http.StatusFound)
			return
		}

		t.Name = strings.TrimSpace(r.FormValue("name"))
		if bg := strings.TrimSpace(r.FormValue("background_color")); bg != "" {
			t.BackgroundColor = bg
		}
		widthMM, _ := strconv.ParseFloat(r.FormValue("width_mm"), 64)
		if widthMM > 0 {
			t.WidthMM = widthMM
		}
		heightMM, _ := strconv.ParseFloat(r.FormValue("height_mm"), 64)
		if heightMM > 0 {
			t.HeightMM = heightMM
		}

		t.ShowQR = r.FormValue("show_qr") == "1" || r.FormValue("show_qr") == "on"
		t.ShowBarcode = r.FormValue("show_barcode") == "1" || r.FormValue("show_barcode") == "on"
		t.ShowPrice = r.FormValue("show_price") == "1" || r.FormValue("show_price") == "on"
		t.ShowProfile = r.FormValue("show_profile") == "1" || r.FormValue("show_profile") == "on"
		t.ShowExpiry = r.FormValue("show_expiry") == "1" || r.FormValue("show_expiry") == "on"
		t.IsDefault = r.FormValue("is_default") == "1" || r.FormValue("is_default") == "on"

		_ = bp.db.SaveCardTemplate(t)
	}

	http.Redirect(w, r, "/card-templates", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id > 0 {
		_ = bp.db.DeleteCardTemplate(id)
	}
	http.Redirect(w, r, "/card-templates", http.StatusFound)
}
