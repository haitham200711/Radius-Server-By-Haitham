package subscribers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

type SubscriberItem struct {
	ID             int64
	ContractNo     string
	FullName       string
	Username       string
	Password       string
	Phone          string
	Address        string
	Debt           float64
	Profile        string
	IPAddress      string
	QuotaBytes     int64
	QuotaGB        float64
	ExpirationDate string
	Status         string
}

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	query := `SELECT 
		COALESCE(s.id, 0),
		COALESCE(s.contract_no, '-'),
		COALESCE(s.full_name, rc.username),
		rc.username,
		COALESCE(rc.value, ''),
		COALESCE(s.phone, '-'),
		COALESCE(s.address, '-'),
		COALESCE(s.debt, 0.00),
		COALESCE(s.profile_name, COALESCE(rug.groupname, '')),
		COALESCE(s.static_ip, COALESCE(rr.value, '')),
		COALESCE(s.quota_bytes, 0),
		s.expiration_date,
		COALESCE(s.status, 'active')
	FROM radcheck rc
	LEFT JOIN subscribers s ON rc.username = s.username
	LEFT JOIN radusergroup rug ON rc.username = rug.username
	LEFT JOIN radreply rr ON rc.username = rr.username AND rr.attribute = 'Framed-IP-Address'
	WHERE rc.attribute = 'Cleartext-Password'`

	var args []interface{}
	if search != "" {
		query += " AND (rc.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?)"
		searchArg := "%" + search + "%"
		args = append(args, searchArg, searchArg, searchArg, searchArg)
	}
	query += " ORDER BY COALESCE(s.id, rc.id) DESC LIMIT 200"

	rows, err := bp.db.Query(query, args...)
	var list []SubscriberItem
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s SubscriberItem
			var exp sql.NullTime
			if err := rows.Scan(
				&s.ID,
				&s.ContractNo,
				&s.FullName,
				&s.Username,
				&s.Password,
				&s.Phone,
				&s.Address,
				&s.Debt,
				&s.Profile,
				&s.IPAddress,
				&s.QuotaBytes,
				&exp,
				&s.Status,
			); err == nil {
				if s.QuotaBytes > 0 {
					s.QuotaGB = float64(s.QuotaBytes) / (1024 * 1024 * 1024)
				}
				if exp.Valid {
					s.ExpirationDate = exp.Time.Format("2006-01-02 15:04")
				} else {
					s.ExpirationDate = "غير محدد"
				}
				list = append(list, s)
			}
		}
	}

	_ = bp.renderer.Render(w, "subscribers/list", map[string]interface{}{
		"Title":       "إدارة المشتركين والعقود",
		"ActiveMenu":  "subscribers",
		"CurrentUser": middleware.GetUser(r),
		"Subscribers": list,
		"SearchQuery": search,
	})
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		profiles, _ := bp.db.GetProfiles()

		_ = bp.renderer.Render(w, "subscribers/add", map[string]interface{}{
			"Title":       "إضافة مشترك جديد",
			"ActiveMenu":  "subscribers",
			"CurrentUser": middleware.GetUser(r),
			"Profiles":    profiles,
		})
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := strings.TrimSpace(r.FormValue("password"))
	fullName := strings.TrimSpace(r.FormValue("full_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	address := strings.TrimSpace(r.FormValue("address"))
	contractNo := strings.TrimSpace(r.FormValue("contract_no"))
	profile := strings.TrimSpace(r.FormValue("profile"))
	staticIP := strings.TrimSpace(r.FormValue("static_ip"))
	callingMAC := strings.TrimSpace(r.FormValue("calling_mac"))
	debtStr := strings.TrimSpace(r.FormValue("debt"))
	quotaGBStr := strings.TrimSpace(r.FormValue("quota_gb"))
	expStr := strings.TrimSpace(r.FormValue("expiration_date"))

	if username == "" || password == "" {
		http.Redirect(w, r, "/subscribers/add?error=missing_fields", http.StatusFound)
		return
	}

	if fullName == "" {
		fullName = username
	}

	debt, _ := strconv.ParseFloat(debtStr, 64)
	quotaGB, _ := strconv.ParseFloat(quotaGBStr, 64)
	var quotaBytes int64
	if quotaGB > 0 {
		quotaBytes = int64(quotaGB * 1024 * 1024 * 1024)
	}

	var expNullTime sql.NullTime
	if expStr != "" {
		// Supports YYYY-MM-DDTHH:MM or YYYY-MM-DD
		t, err := time.Parse("2006-01-02T15:04", expStr)
		if err != nil {
			t, err = time.Parse("2006-01-02", expStr)
		}
		if err == nil {
			expNullTime = sql.NullTime{Time: t, Valid: true}
		}
	}

	sub := &models.Subscriber{
		Username:       username,
		ContractNo:     contractNo,
		FullName:       fullName,
		Phone:          phone,
		Address:        address,
		Debt:           debt,
		ProfileName:    profile,
		StaticIP:       staticIP,
		CallingMAC:     callingMAC,
		QuotaBytes:     quotaBytes,
		ExpirationDate: expNullTime,
		Status:         "active",
	}

	if err := bp.db.AddSubscriberCRM(sub, password); err != nil {
		fmt.Printf("[Subscribers] Error creating subscriber: %v\n", err)
	}

	http.Redirect(w, r, "/subscribers", http.StatusFound)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	if username != "" {
		_ = bp.db.DeleteSubscriber(username)
	}
	http.Redirect(w, r, "/subscribers", http.StatusFound)
}

func (bp *Blueprint) RenewHandler(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	profile := strings.TrimSpace(r.FormValue("profile"))
	if username == "" {
		username = strings.TrimSpace(r.URL.Query().Get("username"))
	}
	if profile == "" {
		profile = strings.TrimSpace(r.URL.Query().Get("profile"))
	}

	if username != "" {
		if err := bp.db.RenewSubscriber(username, profile); err != nil {
			fmt.Printf("[Subscribers] Error renewing subscriber %s: %v\n", username, err)
		}
	}
	http.Redirect(w, r, "/subscribers", http.StatusFound)
}
