package backups

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/backup"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/whatsapp"
	"github.com/haitham200711/Radius-Server-By-Haitham/web"
)

type Blueprint struct {
	db       *database.DB
	renderer *web.Renderer
	cfg      *config.Config
}

func New(db *database.DB, r *web.Renderer, cfg *config.Config) *Blueprint {
	return &Blueprint{
		db:       db,
		renderer: r,
		cfg:      cfg,
	}
}

func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	settings, err := bp.db.GetBackupSettings()
	if err != nil {
		settings = &models.BackupSettings{
			Enabled:        true,
			IntervalHours:  6,
			RetentionCount: 10,
			SendWhatsApp:   false,
		}
	}

	backupsList, err := bp.db.ListBackups()
	if err != nil {
		backupsList = []models.BackupRecord{}
	}

	var totalBytes int64
	for _, b := range backupsList {
		totalBytes += b.SizeBytes
	}

	waStatus := whatsapp.GetWhatsAppStatus()

	_ = bp.renderer.Render(w, "backups/index", map[string]interface{}{
		"Title":          "النسخ الاحتياطي التلقائي واليدوي",
		"ActiveMenu":     "backups",
		"CurrentUser":    middleware.GetUser(r),
		"Settings":       settings,
		"Backups":        backupsList,
		"TotalCount":     len(backupsList),
		"TotalStorage":   database.FormatBytesHuman(totalBytes),
		"WhatsAppStatus": waStatus,
	})
}

func (bp *Blueprint) SaveSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/backups", http.StatusSeeOther)
		return
	}

	enabled := r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on" || r.FormValue("enabled") == "true"
	sendWA := r.FormValue("send_whatsapp") == "1" || r.FormValue("send_whatsapp") == "on" || r.FormValue("send_whatsapp") == "true"
	phone := strings.TrimSpace(r.FormValue("whatsapp_phone"))

	interval, _ := strconv.Atoi(r.FormValue("interval_hours"))
	if interval < 1 {
		interval = 1
	}

	retention, _ := strconv.Atoi(r.FormValue("retention_count"))
	if retention < 1 {
		retention = 5
	}

	settings := &models.BackupSettings{
		Enabled:        enabled,
		IntervalHours:  interval,
		RetentionCount: retention,
		SendWhatsApp:   sendWA,
		WhatsAppPhone:  phone,
	}

	if err := bp.db.UpdateBackupSettings(settings); err != nil {
		http.Error(w, "فشل حفظ الإعدادات: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" || strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": "تم حفظ إعدادات النسخ الاحتياطي بنجاح!",
		})
		return
	}

	http.Redirect(w, r, "/backups?success=settings_saved", http.StatusSeeOther)
}

func (bp *Blueprint) CreateNowHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user := middleware.GetUser(r)
	trigger := "يدوي (لوحة التحكم)"
	if user != nil {
		trigger = fmt.Sprintf("يدوي بواسطة (%s)", user.Username)
	}

	rec, err := backup.ExecuteBackup(bp.db, bp.cfg, trigger)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "success",
		"message":       "تم إنشاء النسخة الاحتياطية بنجاح!",
		"id":            rec.ID,
		"filename":      rec.Filename,
		"size":          database.FormatBytesHuman(rec.SizeBytes),
		"created_at":    rec.CreatedAt.Format("2006-01-02 15:04:05"),
		"whatsapp_sent": rec.WhatsAppSent,
	})
}

func (bp *Blueprint) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "معرف النسخة غير صالح", http.StatusBadRequest)
		return
	}

	rec, err := bp.db.GetBackupRecord(id)
	if err != nil || rec == nil {
		http.Error(w, "لم يتم العثور على النسخة الاحتياطية", http.StatusNotFound)
		return
	}

	if _, err := os.Stat(rec.FilePath); os.IsNotExist(err) {
		http.Error(w, "ملف النسخة الاحتياطية غير موجود على القرص", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", rec.Filename))
	w.Header().Set("Content-Type", "application/gzip")
	http.ServeFile(w, r, rec.FilePath)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("id")
	if idStr == "" {
		idStr = r.URL.Query().Get("id")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "معرف النسخة غير صالح", http.StatusBadRequest)
		return
	}

	rec, err := bp.db.GetBackupRecord(id)
	if err == nil && rec != nil && rec.FilePath != "" {
		_ = os.Remove(rec.FilePath)
	}

	if err := bp.db.DeleteBackupRecord(id); err != nil {
		http.Error(w, "فشل حذف السجل من قاعدة البيانات", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "تم حذف النسخة الاحتياطية بنجاح!",
	})
}

func (bp *Blueprint) SendWhatsAppHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "معرف النسخة غير صالح", http.StatusBadRequest)
		return
	}

	rec, err := bp.db.GetBackupRecord(id)
	if err != nil || rec == nil {
		http.Error(w, "النسخة غير موجودة", http.StatusNotFound)
		return
	}

	phone := strings.TrimSpace(r.FormValue("phone"))
	if phone == "" {
		settings, _ := bp.db.GetBackupSettings()
		if settings != nil {
			phone = settings.WhatsAppPhone
		}
	}

	if phone == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "يرجى تحديد رقم هاتف لإرسال النسخة إليه",
		})
		return
	}

	fileBytes, err := os.ReadFile(rec.FilePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "تعذر قراءة ملف النسخة: " + err.Error(),
		})
		return
	}

	caption := fmt.Sprintf(`📦 *نسخة احتياطية لنظام Haitham RADIUS*
══════════════════════════
📅 *التاريخ:* %s
📁 *اسم الملف:* %s
📊 *الحجم:* %s
⚙️ *طريقة الإرسال:* إرسال يدوي مباشر
══════════════════════════
🛡️ نظام النسخ الاحتياطي يعمل بكفاءة وأمان.`,
		rec.CreatedAt.Format("2006-01-02 15:04:05"),
		rec.Filename,
		rec.SizeFormatted,
	)

	err = whatsapp.SendWhatsAppDocument(phone, fileBytes, rec.Filename, caption)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "فشل إرسال النسخة عبر الواتساب: " + err.Error(),
		})
		return
	}

	_ = bp.db.MarkBackupWhatsAppSent(rec.ID, true)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("تم إرسال النسخة بنجاح إلى رقم الواتساب: %s", phone),
	})
}

func (bp *Blueprint) RestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("id")
	targetFilePath := ""

	if idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil && id > 0 {
			rec, err := bp.db.GetBackupRecord(id)
			if err == nil && rec != nil {
				targetFilePath = rec.FilePath
			}
		}
	}

	// Or handle uploaded file
	if targetFilePath == "" {
		file, handler, err := r.FormFile("backup_file")
		if err == nil {
			defer file.Close()
			tmpPath := filepath.Join(backup.BackupDir, "uploaded-restore-"+strconv.FormatInt(time.Now().Unix(), 10)+"-"+handler.Filename)
			dst, err := os.Create(tmpPath)
			if err != nil {
				http.Error(w, "فشل إنشاء ملف مؤقت للاستعادة", http.StatusInternalServerError)
				return
			}
			_, _ = io.Copy(dst, file)
			dst.Close()
			targetFilePath = tmpPath
			defer os.Remove(tmpPath)
		}
	}

	if targetFilePath == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "يرجى تحديد نسخة موجودة أو رفع ملف نسخة احتياطية (.sql.gz)",
		})
		return
	}

	err := backup.RestoreBackup(bp.db, bp.cfg, targetFilePath)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "فشلت عملية الاستعادة: " + err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "تم استعادة قاعدة البيانات بنجاح تام!",
	})
}
