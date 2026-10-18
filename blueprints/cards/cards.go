package cards

import (
	"fmt"
	"net/http"
	"strconv"

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
	u := middleware.GetUser(r)

	query := `SELECT b.id, b.batch_code, b.profile_id, COALESCE(p.display_name, ''),
		b.reseller_id, COALESCE(res.name, 'المدير العام'), b.count, b.price_per_card, b.total_price, b.created_at
		FROM batches b
		LEFT JOIN profiles p ON b.profile_id = p.id
		LEFT JOIN resellers res ON b.reseller_id = res.id`

	var args []interface{}
	if u != nil && u.Role == "reseller" {
		query += " WHERE b.reseller_id = ?"
		args = append(args, u.ID)
	}
	query += " ORDER BY b.id DESC LIMIT 100"

	rows, err := bp.db.Query(query, args...)
	var batches []models.Batch
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b models.Batch
			if err := rows.Scan(&b.ID, &b.BatchCode, &b.ProfileID, &b.ProfileName,
				&b.ResellerID, &b.ResellerName, &b.Count, &b.PricePerCard, &b.TotalPrice, &b.CreatedAt); err == nil {
				batches = append(batches, b)
			}
		}
	}

	_ = bp.renderer.Render(w, "cards/list", map[string]interface{}{
		"Title":       "إدارة كروت الواي فاي والشحن",
		"ActiveMenu":  "cards",
		"CurrentUser": u,
		"Batches":     batches,
	})
}

func (bp *Blueprint) GenerateHandler(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetUser(r)

	if r.Method == http.MethodGet {
		pRows, _ := bp.db.Query("SELECT id, name, display_name, price FROM profiles ORDER BY id ASC")
		var profiles []models.Profile
		if pRows != nil {
			defer pRows.Close()
			for pRows.Next() {
				var p models.Profile
				_ = pRows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.Price)
				profiles = append(profiles, p)
			}
		}

		var resellers []models.Reseller
		if u.Role == "admin" {
			rRows, _ := bp.db.Query("SELECT id, name, username, balance FROM resellers WHERE is_active = 1 ORDER BY id ASC")
			if rRows != nil {
				defer rRows.Close()
				for rRows.Next() {
					var res models.Reseller
					_ = rRows.Scan(&res.ID, &res.Name, &res.Username, &res.Balance)
					resellers = append(resellers, res)
				}
			}
		}

		_ = bp.renderer.Render(w, "cards/generate", map[string]interface{}{
			"Title":       "توليد كروت جديدة",
			"ActiveMenu":  "cards",
			"CurrentUser": u,
			"Profiles":    profiles,
			"Resellers":   resellers,
		})
		return
	}

	count, _ := strconv.Atoi(r.FormValue("count"))
	prefix := r.FormValue("prefix")
	charLen, _ := strconv.Atoi(r.FormValue("char_len"))
	profileID, _ := strconv.ParseInt(r.FormValue("profile_id"), 10, 64)
	resellerID, _ := strconv.ParseInt(r.FormValue("reseller_id"), 10, 64)
	pricePerCard, _ := strconv.ParseFloat(r.FormValue("price_per_card"), 64)

	if u.Role == "reseller" {
		resellerID = u.ID
	}

	batchCode, _, err := bp.db.GenerateBatch(count, prefix, charLen, profileID, resellerID, pricePerCard)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error generating batch: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/cards/print?batch="+batchCode, http.StatusFound)
}

func (bp *Blueprint) PrintHandler(w http.ResponseWriter, r *http.Request) {
	batchCode := r.URL.Query().Get("batch")
	if batchCode == "" {
		http.Redirect(w, r, "/cards", http.StatusFound)
		return
	}

	rows, err := bp.db.Query(`SELECT c.id, c.username, c.password, COALESCE(p.display_name, ''), c.price
		FROM cards c
		JOIN batches b ON c.batch_id = b.id
		LEFT JOIN profiles p ON c.profile_id = p.id
		WHERE b.batch_code = ? ORDER BY c.id ASC`, batchCode)

	type PrintableCard struct {
		ID          int64
		Username    string
		Password    string
		ProfileName string
		Price       float64
	}
	var cardList []PrintableCard
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var c PrintableCard
			if err := rows.Scan(&c.ID, &c.Username, &c.Password, &c.ProfileName, &c.Price); err == nil {
				cardList = append(cardList, c)
			}
		}
	}

	_ = bp.renderer.Render(w, "cards/print", map[string]interface{}{
		"BatchCode": batchCode,
		"Cards":     cardList,
	})
}
