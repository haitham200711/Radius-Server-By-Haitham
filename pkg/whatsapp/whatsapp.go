package whatsapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

type WhatsAppStatus struct {
	IsConnected bool   `json:"is_connected"`
	IsLoggedIn  bool   `json:"is_logged_in"`
	PhoneNumber string `json:"phone_number"`
	HasQR       bool   `json:"has_qr"`
	QRCodeImage string `json:"qr_code_image"`
}

var (
	waClient       *whatsmeow.Client
	waContainer    *sqlstore.Container
	waLogger       waLog.Logger
	initOnce       sync.Once
	pairingMu      sync.Mutex
	qrStateMu      sync.RWMutex
	currentQRImage string
	isConnecting   bool
	qrSessionSeq   uint64
	qrCancelFunc   context.CancelFunc
	lastQRStart    time.Time
	sessionDBPath  = "whatsapp_session.db"
)

// SetSessionDBPath allows customizing the SQLite database path before Init
func SetSessionDBPath(path string) {
	if path != "" {
		sessionDBPath = path
	}
}

// InitWhatsApp initializes the whatsmeow client using SQLite storage
func InitWhatsApp() {
	initOnce.Do(func() {
		ctx := context.Background()
		waLogger = waLog.Stdout("WhatsApp", "INFO", true)

		// Set standard Chrome Web companion identity so QR clientType matches
		waStore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
		waStore.SetOSInfo("Linux", [3]uint32{6, 8, 0})

		dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_foreign_keys=on", sessionDBPath)
		container, err := sqlstore.New(ctx, "sqlite", dsn, waLogger)
		if err != nil {
			log.Printf("[WHATSAPP] DB Init Error: %v\n", err)
			return
		}
		waContainer = container

		deviceStore, err := container.GetFirstDevice(ctx)
		if err != nil {
			log.Printf("[WHATSAPP] GetFirstDevice Error: %v\n", err)
			return
		}
		if deviceStore == nil {
			deviceStore = container.NewDevice()
		}

		waClient = whatsmeow.NewClient(deviceStore, waLogger)
		waClient.AddEventHandler(handleWhatsAppEvent)

		// Auto-reconnect if already logged in from previous session
		if deviceStore.ID != nil {
			go func() {
				time.Sleep(1 * time.Second)
				log.Printf("[WHATSAPP] Auto-reconnecting saved session for user %s...\n", deviceStore.ID.User)
				if err := waClient.Connect(); err != nil {
					log.Printf("[WHATSAPP] Auto-Reconnect Error: %v\n", err)
				} else {
					log.Printf("[WHATSAPP] Successfully reconnected as %s\n", deviceStore.ID.User)
				}
			}()
		}
	})
}

func handleWhatsAppEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		log.Println("[WHATSAPP] Connected to WhatsApp servers")
		qrStateMu.Lock()
		currentQRImage = ""
		isConnecting = false
		qrStateMu.Unlock()
	case *events.PairSuccess:
		log.Printf("[WHATSAPP] PairSuccess for JID=%s LID=%s Platform=%s\n", v.ID, v.LID, v.Platform)
	case *events.PairError:
		log.Printf("[WHATSAPP] PairError for JID=%s Error=%v\n", v.ID, v.Error)
	case *events.QRScannedWithoutMultidevice:
		log.Println("[WHATSAPP] QRScannedWithoutMultidevice")
	case *events.ClientOutdated:
		log.Println("[WHATSAPP] ClientOutdated")
	case *events.ConnectFailure:
		log.Printf("[WHATSAPP] ConnectFailure reason=%v message=%s\n", v.Reason, v.Message)
	case *events.LoggedOut:
		log.Printf("[WHATSAPP] Logged out (reason: %v). Resetting session.\n", v.Reason)
		qrStateMu.Lock()
		currentQRImage = ""
		isConnecting = false
		qrStateMu.Unlock()
	}
}

// StartWhatsAppPairing safely requests a QR code from WhatsApp servers without blocking or deadlocks
func StartWhatsAppPairing() (string, error) {
	pairingMu.Lock()
	defer pairingMu.Unlock()

	if waClient == nil {
		InitWhatsApp()
		if waClient == nil {
			return "", errors.New("محرك واتساب غير مهيأ")
		}
	}

	// If already logged in, do not re-pair
	if waClient.Store != nil && waClient.Store.ID != nil {
		return "", errors.New("واتساب متصل ومقترن بالفعل برقم: " + waClient.Store.ID.User)
	}

	// Only reuse an existing QR if actively connecting and requested very recently
	qrStateMu.RLock()
	existingQR := currentQRImage
	connecting := isConnecting
	recentStart := time.Since(lastQRStart) < 8*time.Second
	qrStateMu.RUnlock()

	if existingQR != "" && connecting && recentStart && waClient.IsConnected() {
		return existingQR, nil
	}

	// Cancel previous QR context and reconnect cleanly
	if qrCancelFunc != nil {
		qrCancelFunc()
		qrCancelFunc = nil
	}
	if waClient.IsConnected() {
		waClient.Disconnect()
		time.Sleep(300 * time.Millisecond)
	}

	// Recreate fresh device store keys if needed
	if waContainer != nil && (waClient.Store == nil || waClient.Store.ID == nil) {
		newDevice := waContainer.NewDevice()
		waClient = whatsmeow.NewClient(newDevice, waLogger)
		waClient.AddEventHandler(handleWhatsAppEvent)
	}

	// 180s context to allow full rotation through all QR cycles
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	qrCancelFunc = cancel

	qrStateMu.Lock()
	qrSessionSeq++
	mySessionID := qrSessionSeq
	currentQRImage = ""
	isConnecting = true
	lastQRStart = time.Now()
	qrStateMu.Unlock()

	qrChan, err := waClient.GetQRChannel(ctx)
	if err != nil {
		cancel()
		qrStateMu.Lock()
		if qrSessionSeq == mySessionID {
			isConnecting = false
		}
		qrStateMu.Unlock()
		return "", fmt.Errorf("فشل بدء قناة QR: %v", err)
	}

	go func(sessionID uint64, sessionCancel context.CancelFunc) {
		defer func() {
			sessionCancel()
			qrStateMu.Lock()
			if qrSessionSeq == sessionID {
				currentQRImage = ""
				isConnecting = false
			}
			qrStateMu.Unlock()
		}()
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				pngBytes, err := qrcode.Encode(evt.Code, qrcode.Medium, 256)
				if err == nil {
					b64 := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
					qrStateMu.Lock()
					if qrSessionSeq == sessionID {
						currentQRImage = b64
						isConnecting = true
					}
					qrStateMu.Unlock()
					log.Println("[WHATSAPP] QR code generated/rotated and ready for scan.")
				}
			case "success", "login":
				log.Println("[WHATSAPP] QR Login Successful!")
				return
			case "timeout":
				log.Println("[WHATSAPP] QR Code timed out after full rotation.")
				return
			case "error":
				log.Printf("[WHATSAPP] QR Pairing Error: %v\n", evt.Error)
				return
			default:
				log.Printf("[WHATSAPP] QR Event: %s (err=%v)\n", evt.Event, evt.Error)
			}
		}
	}(mySessionID, cancel)

	err = waClient.Connect()
	if err != nil {
		cancel()
		qrStateMu.Lock()
		if qrSessionSeq == mySessionID {
			isConnecting = false
		}
		qrStateMu.Unlock()
		return "", fmt.Errorf("فشل الاتصال بسيرفر واتساب: %v", err)
	}

	// Wait up to 5 seconds for initial QR image to arrive
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		qrStateMu.RLock()
		img := currentQRImage
		qrStateMu.RUnlock()
		if img != "" {
			return img, nil
		}
	}

	return "", errors.New("انتهت مهلة انتظار توليد الكود من سيرفر واتساب، يرجى المحاولة مرة أخرى")
}

// PairWhatsAppByPhone generates an 8-character linking code for pairing via phone number
func PairWhatsAppByPhone(phone string) (string, error) {
	cleanPhone := FormatWhatsAppPhone(phone)
	if len(cleanPhone) < 10 {
		return "", errors.New("يرجى إدخال رقم هاتف صحيح مع كود الدولة (مثال: 201012345678)")
	}

	qrStateMu.RLock()
	hasActiveQR := currentQRImage != "" && isConnecting
	qrStateMu.RUnlock()

	if waClient == nil || !waClient.IsConnected() || !hasActiveQR {
		if _, err := StartWhatsAppPairing(); err != nil {
			return "", err
		}
	}

	pairingMu.Lock()
	defer pairingMu.Unlock()

	if waClient.Store != nil && waClient.Store.ID != nil {
		return "", errors.New("واتساب متصل ومقترن بالفعل برقم: " + waClient.Store.ID.User)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	code, err := waClient.PairPhone(ctx, cleanPhone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		log.Printf("[WHATSAPP] PairPhone error for %s: %v\n", cleanPhone, err)
		return "", fmt.Errorf("تعذر استخراج كود الربط الهاتفي: %v", err)
	}
	log.Printf("[WHATSAPP] PairPhone code generated for %s: %s\n", cleanPhone, code)
	return code, nil
}

// GetWhatsAppStatus returns current connection and login info
func GetWhatsAppStatus() WhatsAppStatus {
	qrStateMu.RLock()
	qr := currentQRImage
	connecting := isConnecting
	qrStateMu.RUnlock()

	isConn := false
	isLogged := false
	phoneNum := ""

	if waClient != nil {
		isConn = waClient.IsConnected()
		if waClient.Store != nil && waClient.Store.ID != nil {
			isLogged = true
			phoneNum = waClient.Store.ID.User
		}
	}

	hasValidQR := qr != "" && connecting && isConn && !isLogged

	return WhatsAppStatus{
		IsConnected: isConn,
		IsLoggedIn:  isLogged,
		PhoneNumber: phoneNum,
		HasQR:       hasValidQR,
		QRCodeImage: func() string {
			if hasValidQR {
				return qr
			}
			return ""
		}(),
	}
}

// DisconnectWhatsApp logs out and resets store safely
func DisconnectWhatsApp() error {
	pairingMu.Lock()
	defer pairingMu.Unlock()

	if qrCancelFunc != nil {
		qrCancelFunc()
		qrCancelFunc = nil
	}

	qrStateMu.Lock()
	qrSessionSeq++
	currentQRImage = ""
	isConnecting = false
	qrStateMu.Unlock()

	if waClient != nil {
		if waClient.Store != nil && waClient.Store.ID != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = waClient.Logout(ctx)
			cancel()
		}
		waClient.Disconnect()

		if waContainer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if waClient.Store != nil && waClient.Store.ID != nil {
				_ = waContainer.DeleteDevice(ctx, waClient.Store)
			}
			newDevice := waContainer.NewDevice()
			waClient = whatsmeow.NewClient(newDevice, waLogger)
			waClient.AddEventHandler(handleWhatsAppEvent)
			cancel()
		}
	}
	return nil
}

// FormatWhatsAppPhone normalizes phone numbers (e.g., converts local Egyptian numbers 01xxxxxxxxx to 201xxxxxxxxx)
func FormatWhatsAppPhone(phone string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)

	if strings.HasPrefix(clean, "00") {
		clean = strings.TrimPrefix(clean, "00")
	}
	if strings.HasPrefix(clean, "01") && len(clean) == 11 {
		clean = "2" + clean
	}
	return clean
}

// SendWhatsAppMessage sends a message via internal whatsmeow client
func SendWhatsAppMessage(phone string, message string) error {
	cleanPhone := FormatWhatsAppPhone(phone)
	if cleanPhone == "" {
		return errors.New("رقم الهاتف غير صالح")
	}

	if waClient == nil || !waClient.IsConnected() || waClient.Store == nil || waClient.Store.ID == nil {
		return errors.New("خدمة واتساب غير مقترنة بعد. يرجى مسح كود QR أو ربط رقم الهاتف أولاً.")
	}

	jid := types.NewJID(cleanPhone, types.DefaultUserServer)
	msg := &waProto.Message{
		Conversation: proto.String(message),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := waClient.SendMessage(ctx, jid, msg)
	if err != nil {
		log.Printf("[WHATSAPP] SendMessage error to %s: %v\n", cleanPhone, err)
		return fmt.Errorf("فشل إرسال رسالة واتساب: %v", err)
	}

	log.Printf("[WHATSAPP] Message sent successfully to %s\n", cleanPhone)
	return nil
}

// SendWhatsAppDocument sends a document (e.g. PDF) via internal whatsmeow
func SendWhatsAppDocument(phone string, fileBytes []byte, filename string, caption string) error {
	cleanPhone := FormatWhatsAppPhone(phone)
	if cleanPhone == "" {
		return errors.New("رقم الهاتف غير صالح")
	}
	if len(fileBytes) == 0 {
		return errors.New("محتوى الملف فارغ")
	}

	if waClient == nil || !waClient.IsConnected() || waClient.Store == nil || waClient.Store.ID == nil {
		return errors.New("خدمة واتساب غير مقترنة بعد. يرجى ربط الحساب أولاً.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	uploaded, err := waClient.Upload(ctx, fileBytes, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("فشل رفع الملف إلى خوادم واتساب: %v", err)
	}

	jid := types.NewJID(cleanPhone, types.DefaultUserServer)
	fileLen := uint64(len(fileBytes))

	mimeType := "application/octet-stream"
	lowerName := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lowerName, ".pdf"):
		mimeType = "application/pdf"
	case strings.HasSuffix(lowerName, ".zip"):
		mimeType = "application/zip"
	case strings.HasSuffix(lowerName, ".gz"), strings.HasSuffix(lowerName, ".tar.gz"):
		mimeType = "application/gzip"
	case strings.HasSuffix(lowerName, ".sql"):
		mimeType = "application/sql"
	}

	docMsg := &waProto.DocumentMessage{
		URL:           proto.String(uploaded.URL),
		DirectPath:    proto.String(uploaded.DirectPath),
		MediaKey:      uploaded.MediaKey,
		Mimetype:      proto.String(mimeType),
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(fileLen),
		FileName:      proto.String(filename),
		Title:         proto.String(filename),
	}
	if caption != "" {
		docMsg.Caption = proto.String(caption)
	}

	msg := &waProto.Message{
		DocumentMessage: docMsg,
	}

	_, err = waClient.SendMessage(ctx, jid, msg)
	if err != nil {
		return fmt.Errorf("فشل إرسال المستند: %v", err)
	}

	log.Printf("[WHATSAPP] Document %s sent to %s\n", filename, cleanPhone)
	return nil
}

// SendWhatsAppExternalFallback sends via an optional external HTTP Gateway if configured
func SendWhatsAppExternalFallback(apiEndpoint, apiToken, instanceID, phone, message string) error {
	cleanPhone := FormatWhatsAppPhone(phone)
	if cleanPhone == "" || apiEndpoint == "" {
		return errors.New("بيانات البوابة الخارجية غير مكتملة")
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	payload := map[string]interface{}{
		"token":    apiToken,
		"to":       cleanPhone,
		"number":   cleanPhone,
		"body":     message,
		"text":     message,
		"message":  message,
		"instance": instanceID,
	}
	jsonBody, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", apiEndpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
		req.Header.Set("apikey", apiToken)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("الخارجية API استجابت بكود %d: %s", resp.StatusCode, string(body))
}
