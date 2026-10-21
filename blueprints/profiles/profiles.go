package profiles

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
	profilesList, err := bp.db.GetProfiles()
	if err != nil {
		profilesList = []models.Profile{}
	}

	_ = bp.renderer.Render(w, "profiles/list", map[string]interface{}{
		"Title":       "باقات السرعات والخدمات",
		"ActiveMenu":  "profiles",
		"CurrentUser": middleware.GetUser(r),
		"Profiles":    profilesList,
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/profiles", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	rateLimit := strings.TrimSpace(r.FormValue("rate_limit"))
	poolName := strings.TrimSpace(r.FormValue("pool_name"))
	costPrice, _ := strconv.ParseFloat(r.FormValue("cost_price"), 64)
	price, _ := strconv.ParseFloat(r.FormValue("price"), 64)

	// Quota (GB to bytes)
	quotaGB, _ := strconv.ParseFloat(r.FormValue("quota_gb"), 64)
	var quotaBytes int64
	if quotaGB > 0 {
		quotaBytes = int64(quotaGB * 1024 * 1024 * 1024)
	}

	// Validity
	valVal, _ := strconv.Atoi(r.FormValue("validity_value"))
	if valVal <= 0 {
		valVal = 30
	}
	valUnit := strings.TrimSpace(r.FormValue("validity_unit"))
	if valUnit == "" {
		valUnit = "days"
	}

	// Fallback actions on quota
	onQuotaAction := strings.TrimSpace(r.FormValue("on_quota_action"))
	onQuotaProfile := strings.TrimSpace(r.FormValue("on_quota_profile"))
	onQuotaPool := strings.TrimSpace(r.FormValue("on_quota_pool"))

	// Fallback actions on time
	onTimeAction := strings.TrimSpace(r.FormValue("on_time_action"))
	onTimeProfile := strings.TrimSpace(r.FormValue("on_time_profile"))
	onTimePool := strings.TrimSpace(r.FormValue("on_time_pool"))

	// Loan (Grace Period) settings
	loanEnabled := r.FormValue("loan_enabled") == "1" || r.FormValue("loan_enabled") == "on" || r.FormValue("loan_enabled") == "true"
	loanHours, _ := strconv.Atoi(r.FormValue("loan_hours"))
	if loanHours <= 0 {
		loanHours = 48
	}
	loanQuotaGB, _ := strconv.ParseFloat(r.FormValue("loan_quota_gb"), 64)
	var loanQuotaBytes int64
	if loanQuotaGB > 0 {
		loanQuotaBytes = int64(loanQuotaGB * 1024 * 1024 * 1024)
	}
	loanDeductionType := strings.TrimSpace(r.FormValue("loan_deduction_type"))
	if loanDeductionType != "partial" {
		loanDeductionType = "full"
	}

	portalEnabled := r.FormValue("portal_enabled") == "1" || r.FormValue("portal_enabled") == "on" || r.FormValue("portal_enabled") == "true"

	if name != "" && rateLimit != "" {
		if displayName == "" {
			displayName = name
		}
		p := &models.Profile{
			Name:              name,
			DisplayName:       displayName,
			RateLimit:         rateLimit,
			PoolName:          poolName,
			QuotaBytes:        quotaBytes,
			ValidityValue:     valVal,
			ValidityUnit:      valUnit,
			CostPrice:         costPrice,
			Price:             price,
			SharedUsers:       1,
			OnQuotaAction:     onQuotaAction,
			OnQuotaProfile:    onQuotaProfile,
			OnQuotaPool:       onQuotaPool,
			OnTimeAction:      onTimeAction,
			OnTimeProfile:     onTimeProfile,
			OnTimePool:        onTimePool,
			LoanEnabled:       loanEnabled,
			LoanHours:         loanHours,
			LoanQuotaBytes:    loanQuotaBytes,
			LoanDeductionType: loanDeductionType,
			PortalEnabled:     portalEnabled,
		}
		_ = bp.db.SaveProfile(p)
	}

	http.Redirect(w, r, "/profiles", http.StatusFound)
}

func (bp *Blueprint) EditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		idStr := r.URL.Query().Get("id")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		p, err := bp.db.GetProfile(id)
		if err != nil {
			http.Error(w, "Profile not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p)
		return
	}

	if r.Method == http.MethodPost {
		idStr := r.FormValue("id")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		p, err := bp.db.GetProfile(id)
		if err != nil {
			http.Redirect(w, r, "/profiles", http.StatusFound)
			return
		}

		p.DisplayName = strings.TrimSpace(r.FormValue("display_name"))
		p.RateLimit = strings.TrimSpace(r.FormValue("rate_limit"))
		p.PoolName = strings.TrimSpace(r.FormValue("pool_name"))
		p.CostPrice, _ = strconv.ParseFloat(r.FormValue("cost_price"), 64)
		p.Price, _ = strconv.ParseFloat(r.FormValue("price"), 64)

		quotaGB, _ := strconv.ParseFloat(r.FormValue("quota_gb"), 64)
		if quotaGB > 0 {
			p.QuotaBytes = int64(quotaGB * 1024 * 1024 * 1024)
		} else {
			p.QuotaBytes = 0
		}

		valVal, _ := strconv.Atoi(r.FormValue("validity_value"))
		if valVal > 0 {
			p.ValidityValue = valVal
		}
		if vu := strings.TrimSpace(r.FormValue("validity_unit")); vu != "" {
			p.ValidityUnit = vu
		}

		p.OnQuotaAction = strings.TrimSpace(r.FormValue("on_quota_action"))
		p.OnQuotaProfile = strings.TrimSpace(r.FormValue("on_quota_profile"))
		p.OnQuotaPool = strings.TrimSpace(r.FormValue("on_quota_pool"))

		p.OnTimeAction = strings.TrimSpace(r.FormValue("on_time_action"))
		p.OnTimeProfile = strings.TrimSpace(r.FormValue("on_time_profile"))
		p.OnTimePool = strings.TrimSpace(r.FormValue("on_time_pool"))

		p.LoanEnabled = r.FormValue("loan_enabled") == "1" || r.FormValue("loan_enabled") == "on" || r.FormValue("loan_enabled") == "true"
		loanHours, _ := strconv.Atoi(r.FormValue("loan_hours"))
		if loanHours <= 0 {
			loanHours = 48
		}
		p.LoanHours = loanHours

		loanQuotaGB, _ := strconv.ParseFloat(r.FormValue("loan_quota_gb"), 64)
		if loanQuotaGB > 0 {
			p.LoanQuotaBytes = int64(loanQuotaGB * 1024 * 1024 * 1024)
		} else {
			p.LoanQuotaBytes = 0
		}

		loanDeductionType := strings.TrimSpace(r.FormValue("loan_deduction_type"))
		if loanDeductionType != "partial" {
			loanDeductionType = "full"
		}
		p.LoanDeductionType = loanDeductionType

		p.PortalEnabled = r.FormValue("portal_enabled") == "1" || r.FormValue("portal_enabled") == "on" || r.FormValue("portal_enabled") == "true"

		_ = bp.db.SaveProfile(p)
	}

	http.Redirect(w, r, "/profiles", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id > 0 {
		_ = bp.db.DeleteProfile(id)
	}
	http.Redirect(w, r, "/profiles", http.StatusFound)
}
