package card_templates

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

func saveUploadedImage(r *http.Request, fieldName string) (string, error) {
	file, handler, err := r.FormFile(fieldName)
	if err != nil {
		return "", err
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(handler.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" && ext != ".svg" {
		return "", fmt.Errorf("امتداد الصورة غير مدعوم")
	}

	_ = os.MkdirAll("uploads", 0755)
	filename := fmt.Sprintf("card_bg_%d%s", time.Now().UnixNano(), ext)
	dstPath := filepath.Join("uploads", filename)

	dst, err := os.Create(dstPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		return "", err
	}

	return "/uploads/" + filename, nil
}

func sanitizeCardTemplateHtml(htmlContent, overlayConfig, cardHeight string) string {
	if !strings.Contains(htmlContent, "voucher-image-bg") {
		return htmlContent
	}

	// Ensure background-image points to {{.BgImage}}
	if strings.Contains(htmlContent, "background-image: url('')") || strings.Contains(htmlContent, "background-image: url(\"\")") {
		htmlContent = strings.ReplaceAll(htmlContent, "background-image: url('')", "background-image: url('{{.BgImage}}')")
		htmlContent = strings.ReplaceAll(htmlContent, "background-image: url(\"\")", "background-image: url('{{.BgImage}}')")
	}

	// Ensure {{.Code}} is present
	if !strings.Contains(htmlContent, "{{.Code}}") && overlayConfig != "" {
		var cfg struct {
			Code struct {
				X     interface{} `json:"x"`
				Y     interface{} `json:"y"`
				Size  interface{} `json:"size"`
				Color string      `json:"color"`
			} `json:"code"`
			Pass struct {
				Enabled bool        `json:"enabled"`
				X       interface{} `json:"x"`
				Y       interface{} `json:"y"`
				Size    interface{} `json:"size"`
				Color   string      `json:"color"`
			} `json:"pass"`
			Prof struct {
				Enabled bool        `json:"enabled"`
				X       interface{} `json:"x"`
				Y       interface{} `json:"y"`
				Size    interface{} `json:"size"`
				Color   string      `json:"color"`
			} `json:"prof"`
			Batch struct {
				Enabled bool        `json:"enabled"`
				X       interface{} `json:"x"`
				Y       interface{} `json:"y"`
				Size    interface{} `json:"size"`
				Color   string      `json:"color"`
			} `json:"batch"`
		}
		if json.Unmarshal([]byte(overlayConfig), &cfg) == nil {
			if cardHeight == "" {
				cardHeight = "140px"
			}
			cCodeX := fmt.Sprintf("%v", cfg.Code.X)
			cCodeY := fmt.Sprintf("%v", cfg.Code.Y)
			cCodeSize := fmt.Sprintf("%v", cfg.Code.Size)
			cCodeColor := cfg.Code.Color
			if cCodeColor == "" {
				cCodeColor = "#000000"
			}

			reconstructed := fmt.Sprintf(`<div class="card-voucher voucher-image-bg" style="width: 100%%; aspect-ratio: 16/9.5; min-height: %s; background-image: url('{{.BgImage}}'); background-size: 100%% 100%%; background-position: center; background-repeat: no-repeat; position: relative; border-radius: 8px; overflow: hidden; box-shadow: 0 1px 4px rgba(0,0,0,0.15); border: 1px dashed rgba(0,0,0,0.15);">
    <img src="{{.BgImage}}" class="voucher-bg-img" alt="" style="position: absolute; left: 0; top: 0; width: 100%%; height: 100%%; object-fit: fill; z-index: 0; pointer-events: none; border-radius: inherit; display: block;">
    <div style="position: absolute; z-index: 2; left: %s%%; top: %s%%; transform: translate(-50%%, -50%%); font-size: %spx; color: %s; font-weight: 800; font-family: monospace; white-space: nowrap; letter-spacing: 1px;">{{.Code}}</div>`, cardHeight, cCodeX, cCodeY, cCodeSize, cCodeColor)

			if cfg.Pass.Enabled {
				reconstructed += fmt.Sprintf(`
    <div style="position: absolute; z-index: 2; left: %v%%; top: %v%%; transform: translate(-50%%, -50%%); font-size: %vpx; color: %s; font-family: monospace; white-space: nowrap;">{{.Password}}</div>`, cfg.Pass.X, cfg.Pass.Y, cfg.Pass.Size, cfg.Pass.Color)
			}
			if cfg.Prof.Enabled {
				reconstructed += fmt.Sprintf(`
    <div style="position: absolute; z-index: 2; left: %v%%; top: %v%%; transform: translate(-50%%, -50%%); font-size: %vpx; color: %s; font-weight: 700; white-space: nowrap;">{{.Profile}}</div>`, cfg.Prof.X, cfg.Prof.Y, cfg.Prof.Size, cfg.Prof.Color)
			}
			if cfg.Batch.Enabled {
				reconstructed += fmt.Sprintf(`
    <div style="position: absolute; z-index: 2; left: %v%%; top: %v%%; transform: translate(-50%%, -50%%); font-size: %vpx; color: %s; font-weight: 600; white-space: nowrap;">#{{.BatchID}}</div>`, cfg.Batch.X, cfg.Batch.Y, cfg.Batch.Size, cfg.Batch.Color)
			}
			reconstructed += "\n</div>"
			return reconstructed
		}
	}

	if strings.Contains(htmlContent, "voucher-image-bg") && !strings.Contains(htmlContent, "voucher-bg-img") {
		imgTag := `<img src="{{.BgImage}}" class="voucher-bg-img" alt="" style="position: absolute; left: 0; top: 0; width: 100%; height: 100%; object-fit: fill; z-index: 0; pointer-events: none; border-radius: inherit; display: block;">`
		if idx := strings.Index(htmlContent, ">"); idx != -1 {
			htmlContent = htmlContent[:idx+1] + "\n    " + imgTag + htmlContent[idx+1:]
		}
	}
	return htmlContent
}

func (bp *Blueprint) ListHandler(w http.ResponseWriter, r *http.Request) {
	list, err := bp.db.GetCardTemplates()
	if err != nil {
		list = []models.CardTemplate{}
	}

	data := map[string]interface{}{
		"Title":          "أشكال وتصاميم كروت الهوتسبوت",
		"ActiveMenu":     "card_templates",
		"CurrentUser":    middleware.GetUser(r),
		"Templates":      list,
		"TotalTemplates": len(list),
		"Message":        r.URL.Query().Get("msg"),
		"Error":          r.URL.Query().Get("err"),
	}

	_ = bp.renderer.Render(w, "card_templates/list", data)
}

func (bp *Blueprint) AddHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/card-templates", http.StatusFound)
		return
	}

	_ = r.ParseMultipartForm(10 << 20)

	name := strings.TrimSpace(r.FormValue("name"))
	headerTitle := strings.TrimSpace(r.FormValue("header_title"))
	footerText := strings.TrimSpace(r.FormValue("footer_text"))
	themeColor := strings.TrimSpace(r.FormValue("theme_color"))
	htmlContent := r.FormValue("html_content")
	cssContent := r.FormValue("css_content")
	cardWidth := strings.TrimSpace(r.FormValue("card_width"))
	cardHeight := strings.TrimSpace(r.FormValue("card_height"))
	cols, _ := strconv.Atoi(r.FormValue("cols"))
	if cols <= 0 {
		cols = 3
	}
	rowsCount, _ := strconv.Atoi(r.FormValue("rows"))
	if rowsCount <= 0 {
		rowsCount = 7
	}
	cardGap := strings.TrimSpace(r.FormValue("card_gap"))
	if cardGap == "" {
		cardGap = "2mm"
	}
	overlayConfig := r.FormValue("overlay_config")
	isDefault := r.FormValue("is_default") == "on" || r.FormValue("is_default") == "1"

	bgImage, _ := saveUploadedImage(r, "bg_image_file")
	if bgImage == "" {
		bgImage = strings.TrimSpace(r.FormValue("bg_image_url"))
	}
	bgImage = strings.ReplaceAll(bgImage, "//", "/")

	if themeColor == "" {
		themeColor = "#0d6efd"
	}
	if headerTitle == "" {
		headerTitle = "كارت إنترنت"
	}
	if cardWidth == "" {
		cardWidth = "240px"
	}
	if cardHeight == "" {
		cardHeight = "140px"
	}

	htmlContent = sanitizeCardTemplateHtml(htmlContent, overlayConfig, cardHeight)

	t := &models.CardTemplate{
		Name:          name,
		IsDefault:     isDefault,
		HeaderTitle:   headerTitle,
		FooterText:    footerText,
		ThemeColor:    themeColor,
		HTMLContent:   htmlContent,
		CSSContent:    cssContent,
		BgImage:       bgImage,
		CardWidth:     cardWidth,
		CardHeight:    cardHeight,
		Cols:          cols,
		Rows:          rowsCount,
		CardGap:       cardGap,
		OverlayConfig: overlayConfig,
	}

	err := bp.db.SaveCardTemplate(t)
	if err != nil {
		http.Redirect(w, r, "/card-templates?err="+err.Error(), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/card-templates?msg=تم+إضافة+القالب+بنجاح!", http.StatusFound)
}

func (bp *Blueprint) UpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/card-templates", http.StatusFound)
		return
	}

	_ = r.ParseMultipartForm(10 << 20)

	idStr := r.FormValue("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	name := strings.TrimSpace(r.FormValue("name"))
	headerTitle := strings.TrimSpace(r.FormValue("header_title"))
	footerText := strings.TrimSpace(r.FormValue("footer_text"))
	themeColor := strings.TrimSpace(r.FormValue("theme_color"))
	htmlContent := r.FormValue("html_content")
	cssContent := r.FormValue("css_content")
	cardWidth := strings.TrimSpace(r.FormValue("card_width"))
	cardHeight := strings.TrimSpace(r.FormValue("card_height"))
	cols, _ := strconv.Atoi(r.FormValue("cols"))
	if cols <= 0 {
		cols = 3
	}
	rowsCount, _ := strconv.Atoi(r.FormValue("rows"))
	if rowsCount <= 0 {
		rowsCount = 7
	}
	cardGap := strings.TrimSpace(r.FormValue("card_gap"))
	if cardGap == "" {
		cardGap = "2mm"
	}
	overlayConfig := r.FormValue("overlay_config")
	isDefault := r.FormValue("is_default") == "on" || r.FormValue("is_default") == "1"

	bgImage, _ := saveUploadedImage(r, "bg_image_file")
	if bgImage == "" {
		bgImage = strings.TrimSpace(r.FormValue("bg_image_existing"))
	}
	bgImage = strings.ReplaceAll(bgImage, "//", "/")

	htmlContent = sanitizeCardTemplateHtml(htmlContent, overlayConfig, cardHeight)

	t := &models.CardTemplate{
		ID:            id,
		Name:          name,
		IsDefault:     isDefault,
		HeaderTitle:   headerTitle,
		FooterText:    footerText,
		ThemeColor:    themeColor,
		HTMLContent:   htmlContent,
		CSSContent:    cssContent,
		BgImage:       bgImage,
		CardWidth:     cardWidth,
		CardHeight:    cardHeight,
		Cols:          cols,
		Rows:          rowsCount,
		CardGap:       cardGap,
		OverlayConfig: overlayConfig,
	}

	err := bp.db.SaveCardTemplate(t)
	if err != nil {
		http.Redirect(w, r, "/card-templates?err="+err.Error(), http.StatusFound)
		return
	}

	http.Redirect(w, r, "/card-templates?msg=تم+تعديل+القالب+بنجاح!", http.StatusFound)
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
	bp.UpdateHandler(w, r)
}

func (bp *Blueprint) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.FormValue("id")
	if idStr == "" {
		idStr = r.URL.Query().Get("id")
	}
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id > 0 {
		err := bp.db.DeleteCardTemplate(id)
		if err != nil {
			http.Redirect(w, r, "/card-templates?err="+err.Error(), http.StatusFound)
			return
		}
	}
	http.Redirect(w, r, "/card-templates?msg=تم+حذف+القالب+بنجاح!", http.StatusFound)
}

func (bp *Blueprint) SetDefaultHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.FormValue("id")
	if idStr == "" {
		idStr = r.URL.Query().Get("id")
	}
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id > 0 {
		_ = bp.db.SetDefaultCardTemplate(id)
	}
	http.Redirect(w, r, "/card-templates?msg=تم+تعيين+القالب+الافتراضي+بنجاح!", http.StatusFound)
}
