package whatsapp

import (
	"encoding/json"
	"fmt"
	"log"
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

// IndexHandler renders the main WhatsApp management & connection page
func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	status := whatsapp.GetWhatsAppStatus()
	settings, err := bp.db.GetWhatsAppSettings()
	if err != nil {
		settings = &models.WhatsAppSettings{ID: 1, Enabled: true}
	}
	templates, _ := bp.db.GetWhatsAppTemplates()
	logs, _ := bp.db.GetWhatsAppLogs(50)

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	flashMsg := ""
	flashType := ""
	if successMsg != "" {
		flashMsg = successMsg
		flashType = "success"
	} else if errorMsg != "" {
		flashMsg = errorMsg
		flashType = "danger"
	}

	_ = bp.renderer.Render(w, r, "whatsapp/index", map[string]interface{}{
		"Title":          "ربط وإشعارات واتساب (WhatsApp)",
		"ActiveMenu":     "whatsapp",
		"CurrentUser":    user,
		"Status":         status,
		"Settings":       settings,
		"Templates":      templates,
		"Logs":           logs,
		"FlashMessage":   flashMsg,
		"FlashType":      flashType,
	})
}

// StartPairingHandler generates a fresh QR code and returns it as JSON
func (bp *Blueprint) StartPairingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	qr, err := whatsapp.StartWhatsAppPairing()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true,
		"qr": qr,
	})
}

// PairPhoneHandler requests an 8-digit linking code for phone-number pairing
func (bp *Blueprint) PairPhoneHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	phone := strings.TrimSpace(r.FormValue("phone"))
	if phone == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "يرجى إدخال رقم الهاتف مع كود الدولة (مثال: 201012345678)",
		})
		return
	}

	code, err := whatsapp.PairWhatsAppByPhone(phone)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":   true,
		"code": code,
	})
}

// StatusHandler returns current connection & pairing state as JSON for live polling
func (bp *Blueprint) StatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	status := whatsapp.GetWhatsAppStatus()
	_ = json.NewEncoder(w).Encode(status)
}

// LogoutHandler disconnects and resets the WhatsApp session
func (bp *Blueprint) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	_ = whatsapp.DisconnectWhatsApp()
	http.Redirect(w, r, "/whatsapp?success="+url.QueryEscape("تم قطع الاتصال وحذف جلسة واتساب بنجاح"), http.StatusFound)
}

// TestMessageHandler sends a test message to verify the connection
func (bp *Blueprint) TestMessageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/whatsapp", http.StatusFound)
		return
	}

	phone := strings.TrimSpace(r.FormValue("test_phone"))
	message := strings.TrimSpace(r.FormValue("test_message"))
	if message == "" {
		message = "🔔 تجربة ربط واتساب ناجحة من نظام راديوس مايكروتك الاحترافي!"
	}

	settings, _ := bp.db.GetWhatsAppSettings()
	if phone == "" && settings != nil {
		phone = settings.AdminPhone
	}

	if phone == "" {
		http.Redirect(w, r, "/whatsapp?error="+url.QueryEscape("يرجى إدخال رقم الهاتف للتجربة (مع كود الدولة مثل: 201012345678)"), http.StatusFound)
		return
	}

	status := whatsapp.GetWhatsAppStatus()
	var err error
	if status.IsLoggedIn {
		err = whatsapp.SendWhatsAppMessage(phone, message)
	} else if settings != nil && settings.APIEndpoint != "" {
		err = whatsapp.SendWhatsAppExternalFallback(settings.APIEndpoint, settings.APIToken, settings.InstanceID, phone, message)
	} else {
		err = fmt.Errorf("خدمة واتساب غير مقترنة بعد")
	}

	if err != nil {
		_ = bp.db.LogWhatsAppMessage(phone, "تجربة إرسال", "test", message, "failed", err.Error())
		http.Redirect(w, r, "/whatsapp?error="+url.QueryEscape("فشل إرسال الرسالة: "+err.Error()), http.StatusFound)
		return
	}

	_ = bp.db.LogWhatsAppMessage(phone, "تجربة إرسال", "test", message, "sent", "")
	http.Redirect(w, r, "/whatsapp?success="+url.QueryEscape("تم إرسال الرسالة التجريبية بنجاح إلى: "+phone), http.StatusFound)
}

// SaveSettingsHandler saves configuration parameters
func (bp *Blueprint) SaveSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/whatsapp", http.StatusFound)
		return
	}

	days, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("debt_reminder_days")))
	if days <= 0 {
		days = 3
	}

	minDebt, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("debt_reminder_min_debt")), 64)
	if minDebt < 0 {
		minDebt = 1.00
	}

	settings := &models.WhatsAppSettings{
		ID:                    1,
		Enabled:               r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on",
		AdminPhone:            strings.TrimSpace(r.FormValue("admin_phone")),
		APIEndpoint:           strings.TrimSpace(r.FormValue("api_endpoint")),
		APIToken:              strings.TrimSpace(r.FormValue("api_token")),
		InstanceID:            strings.TrimSpace(r.FormValue("instance_id")),
		NotifyOnNewSubscriber: r.FormValue("notify_on_new_subscriber") == "1" || r.FormValue("notify_on_new_subscriber") == "on",
		NotifyOnExpiry:        r.FormValue("notify_on_expiry") == "1" || r.FormValue("notify_on_expiry") == "on",
		NotifyOnPayment:       r.FormValue("notify_on_payment") == "1" || r.FormValue("notify_on_payment") == "on",
		DebtReminderEnabled:   r.FormValue("debt_reminder_enabled") == "1" || r.FormValue("debt_reminder_enabled") == "on",
		DebtReminderDays:      days,
		DebtReminderMinDebt:   minDebt,
	}

	if err := bp.db.SaveWhatsAppSettings(settings); err != nil {
		http.Redirect(w, r, "/whatsapp?error="+url.QueryEscape("فشل حفظ الإعدادات: "+err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/whatsapp?success="+url.QueryEscape("تم حفظ إعدادات واتساب بنجاح"), http.StatusFound)
}

// SaveTemplateHandler saves/updates a specific notification message template
func (bp *Blueprint) SaveTemplateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/whatsapp", http.StatusFound)
		return
	}

	key := strings.TrimSpace(r.FormValue("event_key"))
	if key == "" {
		http.Redirect(w, r, "/whatsapp?error="+url.QueryEscape("مفتاح القالب غير محدد"), http.StatusFound)
		return
	}

	enabled := r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on"
	text := strings.TrimSpace(r.FormValue("template_text"))

	if err := bp.db.SaveWhatsAppTemplate(key, enabled, text); err != nil {
		http.Redirect(w, r, "/whatsapp?error="+url.QueryEscape("فشل حفظ القالب: "+err.Error()), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/whatsapp?success="+url.QueryEscape("تم حفظ قالب الرسالة بنجاح"), http.StatusFound)
}

// RunDebtRemindersHandler manually triggers sending debt reminders to all indebted subscribers
func (bp *Blueprint) RunDebtRemindersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/whatsapp", http.StatusFound)
		return
	}

	count, err := bp.db.RunDebtReminders()
	if err != nil {
		http.Redirect(w, r, "/whatsapp?error="+url.QueryEscape("حدث خطأ أثناء إرسال تذكيرات الديون: "+err.Error()), http.StatusFound)
		return
	}

	if count == 0 {
		http.Redirect(w, r, "/whatsapp?success="+url.QueryEscape("لم يتم العثور على عملاء تنطبق عليهم شروط التذكير في الوقت الحالي."), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/whatsapp?success="+url.QueryEscape(fmt.Sprintf("تم إرسال رسائل تذكير المديونية بنجاح إلى (%d) عميل مدين!", count)), http.StatusFound)
}

// StartWhatsAppAutomator launches a background engine that regularly checks quotas, expirations, and debt reminders
func (bp *Blueprint) StartWhatsAppAutomator() {
	go func() {
		// Initial wait to let server boot cleanly
		time.Sleep(15 * time.Second)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// 1. Quota & Expiry Automations
			if err := bp.db.CheckQuotaAndExpiryAutomations(); err != nil {
				log.Printf("[WHATSAPP-AUTO] Quota/Expiry check error: %v\n", err)
			}

			// 2. Scheduled Debt Reminders (Respects interval days and min debt)
			if _, err := bp.db.RunDebtReminders(); err != nil {
				log.Printf("[WHATSAPP-AUTO] Debt reminder check error: %v\n", err)
			}
		}
	}()
}
