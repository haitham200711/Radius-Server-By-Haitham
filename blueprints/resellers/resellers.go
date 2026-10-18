package resellers

import (
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
	rows, err := bp.db.Query("SELECT id, name, username, phone, balance, credit_limit, notes, is_active, created_at FROM resellers ORDER BY id DESC")
	var resellers []models.Reseller
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var res models.Reseller
			if err := rows.Scan(&res.ID, &res.Name, &res.Username, &res.Phone, &res.Balance, &res.CreditLimit, &res.Notes, &res.IsActive, &res.CreatedAt); err == nil {
				resellers = append(resellers, res)
			}
		}
	}

	_ = bp.renderer.Render(w, "resellers/list", map[string]interface{}{
		"Title":       "إدارة الموزعين والوكلاء",
		"ActiveMenu":  "resellers",
		"CurrentUser": middleware.GetUser(r),
		"Resellers":   resellers,
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		name := r.FormValue("name")
		username := r.FormValue("username")
		password := r.FormValue("password")
		phone := r.FormValue("phone")
		balance, _ := strconv.ParseFloat(r.FormValue("balance"), 64)
		creditLimit, _ := strconv.ParseFloat(r.FormValue("credit_limit"), 64)
		notes := r.FormValue("notes")

		res := &models.Reseller{
			Name:        name,
			Username:    username,
			Phone:       phone,
			Balance:     balance,
			CreditLimit: creditLimit,
			Notes:       notes,
			IsActive:    true,
		}
		_ = res.SetPassword(password)

		_, _ = bp.db.Exec(`INSERT INTO resellers (name, username, password_hash, phone, balance, credit_limit, notes, is_active)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1)`, res.Name, res.Username, res.PasswordHash, res.Phone, res.Balance, res.CreditLimit, res.Notes)
	}
	http.Redirect(w, r, "/resellers", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id != "" {
		_, _ = bp.db.Exec("DELETE FROM resellers WHERE id = ?", id)
	}
	http.Redirect(w, r, "/resellers", http.StatusFound)
}
