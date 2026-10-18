package card_templates

import (
	"net/http"

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
	rows, err := bp.db.Query("SELECT id, name, background_image, width_mm, height_mm, show_qr, show_barcode, show_price, show_profile, show_expiry, created_at FROM card_templates ORDER BY id ASC")
	var list []models.CardTemplate
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t models.CardTemplate
			if err := rows.Scan(&t.ID, &t.Name, &t.BackgroundImage, &t.WidthMM, &t.HeightMM, &t.ShowQR, &t.ShowBarcode, &t.ShowPrice, &t.ShowProfile, &t.ShowExpiry, &t.CreatedAt); err == nil {
				list = append(list, t)
			}
		}
	}

	_ = bp.renderer.Render(w, "card_templates/list", map[string]interface{}{
		"Title":       "تصاميم وأشكال الكروت",
		"ActiveMenu":  "card_templates",
		"CurrentUser": middleware.GetUser(r),
		"Templates":   list,
	})
}
