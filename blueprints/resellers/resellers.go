package resellers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/money"
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
	resellers, err := bp.db.GetResellers()
	if err != nil {
		resellers = []models.Reseller{}
	}

	summary, err := bp.db.GetResellerFinancialSummary()
	if err != nil {
		summary = make(map[string]interface{})
	}

	_ = bp.renderer.Render(w, r, "resellers/list", map[string]interface{}{
		"Title":       "إدارة الموزعين والوكلاء",
		"ActiveMenu":  "resellers",
		"CurrentUser": middleware.GetUser(r),
		"Resellers":   resellers,
		"Summary":     summary,
		"Success":     r.URL.Query().Get("success"),
		"Error":       r.URL.Query().Get("error"),
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/resellers", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	username := strings.TrimSpace(r.FormValue("username"))
	password := strings.TrimSpace(r.FormValue("password"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	balance, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("balance")), 64)
	creditLimit, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("credit_limit")), 64)
	commissionPercent, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("commission_percent")), 64)
	notes := strings.TrimSpace(r.FormValue("notes"))

	if name == "" || username == "" || password == "" {
		http.Redirect(w, r, "/resellers?error=missing_fields", http.StatusFound)
		return
	}

	// RBAC permissions from form checkboxes
	perms := models.ResellerPermissions{
		CanAddSubscribers:    r.FormValue("can_add_subscribers") == "1",
		CanEditSubscribers:   r.FormValue("can_edit_subscribers") == "1",
		CanDeleteSubscribers: r.FormValue("can_delete_subscribers") == "1",
		CanRenew:             r.FormValue("can_renew") == "1",
		CanChangeProfile:     r.FormValue("can_change_profile") == "1",
		CanAddQuota:          r.FormValue("can_add_quota") == "1",
		CanViewPasswords:     r.FormValue("can_view_passwords") == "1",
		CanCollectDebt:       r.FormValue("can_collect_debt") == "1",
		CanAllowDebt:         r.FormValue("can_allow_debt") == "1",
		CanGenerateCards:     r.FormValue("can_generate_cards") == "1",
		CanDisconnect:        r.FormValue("can_disconnect") == "1",
	}

	res := &models.Reseller{
		Name:              name,
		Username:          username,
		Phone:             phone,
		Balance:           balance,
		CreditLimit:       creditLimit,
		CommissionPercent: commissionPercent,
		Permissions:       perms,
		Notes:             notes,
		IsActive:          true,
	}

	_, err := bp.db.AddReseller(res, password)
	if err != nil {
		http.Redirect(w, r, "/resellers?error="+err.Error(), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/resellers?success=added", http.StatusFound)
}

func (bp *Blueprint) EditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/resellers", http.StatusFound)
		return
	}

	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if id <= 0 {
		http.Redirect(w, r, "/resellers?error=invalid_id", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	username := strings.TrimSpace(r.FormValue("username"))
	password := strings.TrimSpace(r.FormValue("password"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	creditLimit, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("credit_limit")), 64)
	commissionPercent, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("commission_percent")), 64)
	notes := strings.TrimSpace(r.FormValue("notes"))
	isActive := r.FormValue("is_active") == "1"

	res := &models.Reseller{
		ID:                id,
		Name:              name,
		Username:          username,
		Phone:             phone,
		CreditLimit:       creditLimit,
		CommissionPercent: commissionPercent,
		Notes:             notes,
		IsActive:          isActive,
	}

	if err := bp.db.UpdateReseller(res, password); err != nil {
		http.Redirect(w, r, "/resellers?error="+err.Error(), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/resellers?success=updated", http.StatusFound)
}

func (bp *Blueprint) PermissionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/resellers", http.StatusFound)
		return
	}

	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if id <= 0 {
		http.Redirect(w, r, "/resellers?error=invalid_id", http.StatusFound)
		return
	}

	perms := models.ResellerPermissions{
		CanAddSubscribers:    r.FormValue("can_add_subscribers") == "1",
		CanEditSubscribers:   r.FormValue("can_edit_subscribers") == "1",
		CanDeleteSubscribers: r.FormValue("can_delete_subscribers") == "1",
		CanRenew:             r.FormValue("can_renew") == "1",
		CanChangeProfile:     r.FormValue("can_change_profile") == "1",
		CanAddQuota:          r.FormValue("can_add_quota") == "1",
		CanViewPasswords:     r.FormValue("can_view_passwords") == "1",
		CanCollectDebt:       r.FormValue("can_collect_debt") == "1",
		CanAllowDebt:         r.FormValue("can_allow_debt") == "1",
		CanGenerateCards:     r.FormValue("can_generate_cards") == "1",
		CanDisconnect:        r.FormValue("can_disconnect") == "1",
	}

	if err := bp.db.UpdateResellerPermissions(id, perms); err != nil {
		http.Redirect(w, r, "/resellers?error="+err.Error(), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/resellers?success=perms_updated", http.StatusFound)
}

func (bp *Blueprint) RechargeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/resellers", http.StatusFound)
		return
	}

	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	actionType := strings.TrimSpace(r.FormValue("action_type"))
	amount, errA := money.ParseFiniteFloat(r.FormValue("amount"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(r.FormValue("idempotency_key"))
	}

	adminUser := "admin"
	if u := middleware.GetUser(r); u != nil {
		adminUser = u.Username
	}

	if id <= 0 || errA != nil || amount <= 0 {
		http.Redirect(w, r, "/resellers?error=invalid_amount", http.StatusFound)
		return
	}

	if err := bp.db.AdjustResellerBalance(id, actionType, amount, notes, adminUser, idempotencyKey); err != nil {
		http.Redirect(w, r, "/resellers?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/resellers?success=balance_adjusted", http.StatusFound)
}

func (bp *Blueprint) ToggleStatusHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id > 0 {
		_ = bp.db.ToggleResellerStatus(id)
	}
	http.Redirect(w, r, "/resellers?success=status_toggled", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id > 0 {
		_ = bp.db.DeleteReseller(id)
	}
	http.Redirect(w, r, "/resellers?success=deleted", http.StatusFound)
}

func (bp *Blueprint) DetailsAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "invalid id"})
		return
	}

	res, err := bp.db.GetReseller(id)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "reseller": res})
}

func (bp *Blueprint) TransactionsAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 150
	}

	txType := strings.TrimSpace(r.URL.Query().Get("type"))
	startDate := strings.TrimSpace(r.URL.Query().Get("start_date"))
	endDate := strings.TrimSpace(r.URL.Query().Get("end_date"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.Role == "reseller" {
		id = currentUser.ID // Security: reseller restricted to their own account transactions
	}

	txs, totalCount, stats, err := bp.db.GetResellerTransactionsFiltered(id, txType, startDate, endDate, search, limit, offset)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"transactions": txs,
		"total_count":  totalCount,
		"stats":        stats,
	})
}
