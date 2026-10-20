package cards

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/whatsapp"
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

	cardType := r.URL.Query().Get("type")
	if cardType == "cafe" {
		bp.CafePortalHandler(w, r)
		return
	}

	activeMenu := "cards"
	pageTitle := "كروت الشحن"

	query := `SELECT b.id, b.batch_code, b.profile_id, COALESCE(p.display_name, ''),
		b.reseller_id, COALESCE(res.name, 'المدير العام'), b.count, b.price_per_card, b.total_price, b.created_at
		FROM batches b
		LEFT JOIN profiles p ON b.profile_id = p.id
		LEFT JOIN resellers res ON b.reseller_id = res.id
		WHERE b.card_type != 'cafe' OR b.card_type IS NULL`

	var args []interface{}
	if u != nil && u.Role == "reseller" {
		query += " AND b.reseller_id = ?"
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
		"Title":       pageTitle,
		"ActiveMenu":  activeMenu,
		"CardType":    cardType,
		"CurrentUser": u,
		"Batches":     batches,
	})
}

func (bp *Blueprint) GenerateHandler(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetUser(r)

	cardType := r.URL.Query().Get("type")
	if cardType == "" {
		cardType = r.FormValue("card_type")
	}
	activeMenu := "cards"
	pageTitle := "توليد كروت شحن جديدة"
	if cardType == "cafe" {
		activeMenu = "cafe_cards"
		pageTitle = "توليد كروت كافيهات جديدة"
	}

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
			"Title":       pageTitle,
			"ActiveMenu":  activeMenu,
			"CardType":    cardType,
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

	redir := "/cards/print?batch=" + batchCode
	if cardType == "cafe" {
		redir += "&type=cafe"
	}
	http.Redirect(w, r, redir, http.StatusFound)
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

// CafePortalHandler displays the complete Cafe Hotspot Management Dashboard
func (bp *Blueprint) CafePortalHandler(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetUser(r)

	// 1. Fetch all Cafe Routers (NAS)
	routers, _ := bp.db.GetCafeRouters()

	// 2. Determine Selected Router ID
	routerIDStr := r.URL.Query().Get("router_id")
	var selectedRouterID int64
	if routerIDStr != "" {
		selectedRouterID, _ = strconv.ParseInt(routerIDStr, 10, 64)
	} else if len(routers) > 0 {
		selectedRouterID = routers[0].ID
	}

	var selectedRouter *models.NAS
	for _, rtr := range routers {
		if rtr.ID == selectedRouterID {
			selectedRouter = &rtr
			break
		}
	}

	// 3. Active Batch for selected router
	activeBatch, _ := bp.db.GetActiveCafeBatch(selectedRouterID)
	var availablePercent, usedPercent float64
	if activeBatch != nil && activeBatch.Count > 0 {
		availablePercent = (float64(activeBatch.AvailableCount) / float64(activeBatch.Count)) * 100.0
		usedPercent = (float64(activeBatch.UsedCount) / float64(activeBatch.Count)) * 100.0
	}

	// 4. Search and Inspect Card
	searchCode := strings.TrimSpace(r.URL.Query().Get("code"))
	var trackedCard *models.CafeCardTracker
	var searchError string
	if searchCode != "" {
		t, err := bp.db.InspectCard(searchCode)
		if err != nil {
			searchError = err.Error()
		} else {
			trackedCard = t
		}
	}

	// 5. Batches for this cafe
	batches, _ := bp.db.GetCafeBatches(selectedRouterID)

	// 6. Profiles list for modal generator
	pRows, _ := bp.db.Query("SELECT id, name, display_name, price FROM profiles ORDER BY id ASC")
	var profilesList []models.Profile
	if pRows != nil {
		defer pRows.Close()
		for pRows.Next() {
			var p models.Profile
			_ = pRows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.Price)
			profilesList = append(profilesList, p)
		}
	}

	_ = bp.renderer.Render(w, "cards/cafe_portal", map[string]interface{}{
		"Title":            "منظومة كروت الكافيهات على الراديوس",
		"ActiveMenu":       "cafe_cards",
		"CurrentUser":      u,
		"Routers":          routers,
		"SelectedRouter":   selectedRouter,
		"SelectedRouterID": selectedRouterID,
		"ActiveBatch":      activeBatch,
		"AvailablePercent": availablePercent,
		"UsedPercent":      usedPercent,
		"SearchCode":       searchCode,
		"TrackedCard":      trackedCard,
		"SearchError":      searchError,
		"Batches":          batches,
		"Profiles":         profilesList,
		"Message":          r.URL.Query().Get("msg"),
		"Error":            r.URL.Query().Get("err"),
	})
}

// CafeGenerateHandler creates a batch of cards assigned to a specific cafe router
func (bp *Blueprint) CafeGenerateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/cards/cafe", http.StatusFound)
		return
	}

	count, _ := strconv.Atoi(r.FormValue("count"))
	prefix := strings.TrimSpace(r.FormValue("prefix"))
	if prefix == "" {
		prefix = "CF-"
	}
	charLen, _ := strconv.Atoi(r.FormValue("char_len"))
	profileID, _ := strconv.ParseInt(r.FormValue("profile_id"), 10, 64)
	nasID, _ := strconv.ParseInt(r.FormValue("nas_id"), 10, 64)
	pricePerCard, _ := strconv.ParseFloat(r.FormValue("price_per_card"), 64)
	sendWhatsApp := r.FormValue("send_whatsapp") == "1"
	cafePhone := strings.TrimSpace(r.FormValue("cafe_phone"))

	batchCode, created, err := bp.db.GenerateCafeBatch(count, prefix, charLen, profileID, nasID, pricePerCard)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/cards/cafe?router_id=%d&err=%s", nasID, url.QueryEscape(err.Error())), http.StatusFound)
		return
	}

	// If phone was provided, save to NAS
	if cafePhone != "" && nasID > 0 {
		_, _ = bp.db.Exec("UPDATE nas SET phone = ?, is_cafe = 1 WHERE id = ?", cafePhone, nasID)
	}

	// Send WhatsApp notification if requested
	if sendWhatsApp && cafePhone != "" {
		go func() {
			var nasName string
			_ = bp.db.QueryRow("SELECT shortname FROM nas WHERE id = ?", nasID).Scan(&nasName)
			var profName string
			_ = bp.db.QueryRow("SELECT display_name FROM profiles WHERE id = ?", profileID).Scan(&profName)

			msg := fmt.Sprintf("☕ *كروت شبكة الواي فاي - كافيه %s*\n"+
				"━━━━━━━━━━━━━━━━━\n"+
				"🎫 *عدد الكروت المنشأة:* %d كارت\n"+
				"⚡ *الباقة والسرعة:* %s\n"+
				"📦 *كود الدفعة:* #%s\n"+
				"⏰ *وقت التوليد:* %s\n"+
				"━━━━━━━━━━━━━━━━━\n"+
				"نظام إدارة الراديوس والكافيهات",
				nasName, created, profName, batchCode, time.Now().Format("2006-01-02 15:04"))

			status := whatsapp.GetWhatsAppStatus()
			if status.IsConnected && status.IsLoggedIn {
				err := whatsapp.SendWhatsAppMessage(cafePhone, msg)
				if err == nil {
					_ = bp.db.LogWhatsAppMessage(cafePhone, nasName, "cafe_batch", msg, "sent", "")
				} else {
					_ = bp.db.LogWhatsAppMessage(cafePhone, nasName, "cafe_batch", msg, "failed", err.Error())
				}
			} else {
				settings, _ := bp.db.GetWhatsAppSettings()
				if settings != nil && settings.APIEndpoint != "" {
					err := whatsapp.SendWhatsAppExternalFallback(settings.APIEndpoint, settings.APIToken, settings.InstanceID, cafePhone, msg)
					if err == nil {
						_ = bp.db.LogWhatsAppMessage(cafePhone, nasName, "cafe_batch", msg, "sent", "")
					} else {
						_ = bp.db.LogWhatsAppMessage(cafePhone, nasName, "cafe_batch", msg, "failed", err.Error())
					}
				}
			}
		}()
	}

	http.Redirect(w, r, fmt.Sprintf("/cards/cafe?router_id=%d&msg=batch_created&batch=%s", nasID, batchCode), http.StatusFound)
}

// CafeDisconnectHandler terminates the active live session for an inspected card
func (bp *Blueprint) CafeDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	routerID := r.URL.Query().Get("router_id")

	ok, err := bp.db.DisconnectCard(code)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/cards/cafe?router_id=%s&code=%s&err=%s", routerID, code, url.QueryEscape(err.Error())), http.StatusFound)
		return
	}

	if ok {
		http.Redirect(w, r, fmt.Sprintf("/cards/cafe?router_id=%s&code=%s&msg=disconnected", routerID, code), http.StatusFound)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/cards/cafe?router_id=%s&code=%s", routerID, code), http.StatusFound)
}
