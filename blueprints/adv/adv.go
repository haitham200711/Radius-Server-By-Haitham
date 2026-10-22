package adv

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/web"
	"golang.org/x/crypto/bcrypt"
)

type Blueprint struct {
	db       *database.DB
	renderer *web.Renderer
}

func New(db *database.DB, renderer *web.Renderer) *Blueprint {
	return &Blueprint{
		db:       db,
		renderer: renderer,
	}
}

// ProfileData parsed from ADV profiles table
type ProfileData struct {
	ID        int
	Name      string
	Price     float64
	DownSpeed string
	UpSpeed   string
	QuotaMB   int
	RateLimit string
}

// ManagerData parsed from ADV managers table
type ManagerData struct {
	ID       int
	Username string
	FullName string
	Phone    string
	Credit   float64
	IsAdmin  int
}

// AdvRadacctSession parsed from ADV radacct table
type AdvRadacctSession struct {
	SessionID        string
	UniqueID         string
	Username         string
	Realm            string
	NasIP            string
	NasPortID        string
	NasPortType      string
	StartTime        string
	StopTime         string
	SessionSec       int64
	AcctAuthentic    string
	ConnectInfoStart string
	ConnectInfoStop  string
	InOctets         int64
	OutOctets        int64
	CalledMAC        string
	CallingMAC       string
	TermCause        string
	ServiceType      string
	FramedProto      string
	FramedIP         string
}

// SubscriberData parsed from ADV userinfo, radcheck, and radusergroup
type SubscriberData struct {
	Username       string
	FullName       string
	Phone          string
	Address        string
	OwnerID        int
	OwnerName      string
	ProfileName    string
	Password       string
	TotalUnpaid    float64
	TotalPaid      float64
	Money          float64
	InDownBytes    int64
	InUpBytes      int64
	AvQuotaMB      float64
	ExpTime        int64
	ExpDateStr     string
	InternetStatus string
	IsExpired      bool

	// Accounting and consumption
	TotalUsedBytes   int64
	TotalInBytes     int64
	TotalOutBytes    int64
	UsedDisplay      string
	DownDisplay      string
	UpDisplay        string
	QuotaBytes       int64
	QuotaDisplay     string
	RemainingBytes   int64
	RemainingDisplay string
	UsagePercent     float64
	SessionsCount    int
	LastMAC          string
	LastIP           string
	LastRenewTime    int64
	CreationTime     int64
}

// BackupAnalysis holds the parsed data from the ADV backup
type BackupAnalysis struct {
	FileName            string
	FileSize            string
	AnalyzedAt          time.Time
	Profiles            []ProfileData
	Managers            []ManagerData
	Subscribers         []SubscriberData
	HotspotCards        int
	ActiveSubsCount     int
	ExpSubsCount        int
	TotalDebt           float64
	TotalMoney          float64
	TotalSubSessions    int
	TotalTrafficBytes   int64
	TotalTrafficDisplay string
	SubscriberSessions  map[string][]AdvRadacctSession
}

var (
	cachedAnalysisMu sync.Mutex
	cachedAnalysis   *BackupAnalysis
	lastBackupPath   string
)

// GetCurrentAnalysis returns the parsed analysis from cache, or attempts to find server backup
func GetCurrentAnalysis() *BackupAnalysis {
	cachedAnalysisMu.Lock()
	analysis := cachedAnalysis
	cachedAnalysisMu.Unlock()

	// If no analysis cached, try auto-detecting server backup
	if analysis == nil {
		possiblePaths := []string{
			"/opt/radius-server/adv_backup.sql",
			"/opt/radius-server/uploads/adv/adv_uploaded.sql",
			"./uploads/adv/adv_uploaded.sql",
			"C:\\Users\\Haitham Abd-Elkader\\Downloads\\adv_db-Drwbz261022_11_59_20.sql\\adv_db-Drwbz261022_11_59_20.sql",
			"D:\\AI Files\\adv_db-iFrRe261021_17_10_21.sql",
			"./adv_backup.sql",
			"/tmp/adv_backup.sql",
		}
		for _, p := range possiblePaths {
			if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
				parsed, err := ParseAdvSQL(p)
				if err == nil && parsed != nil {
					cachedAnalysisMu.Lock()
					cachedAnalysis = parsed
					lastBackupPath = p
					analysis = parsed
					cachedAnalysisMu.Unlock()
					break
				}
			}
		}
	}
	return analysis
}

// IndexHandler renders or redirects to Settings ADV tab
func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings#adv", http.StatusFound)
}

// UploadHandler handles uploading an ADV backup .sql file
func (bp *Blueprint) UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings#adv", http.StatusFound)
		return
	}

	// Limit upload size to 250 MB
	_ = r.ParseMultipartForm(250 << 20)

	file, header, err := r.FormFile("backup_file")
	if err != nil {
		http.Redirect(w, r, "/settings?adv_error=يرجى+اختيار+ملف+SQL+صحيح#adv", http.StatusFound)
		return
	}
	defer file.Close()

	uploadDir := "./uploads/adv"
	_ = os.MkdirAll(uploadDir, 0755)
	destPath := filepath.Join(uploadDir, "adv_uploaded.sql")

	dst, err := os.Create(destPath)
	if err != nil {
		http.Redirect(w, r, "/settings?adv_error=فشل+حفظ+الملف+على+السيرفر#adv", http.StatusFound)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Redirect(w, r, "/settings?adv_error=فشل+رفع+الملف#adv", http.StatusFound)
		return
	}

	// Analyze the uploaded file
	analysis, err := ParseAdvSQL(destPath)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/settings?adv_error=فشل+تحليل+الملف:+%v#adv", err), http.StatusFound)
		return
	}
	analysis.FileName = header.Filename

	cachedAnalysisMu.Lock()
	cachedAnalysis = analysis
	lastBackupPath = destPath
	cachedAnalysisMu.Unlock()

	http.Redirect(w, r, "/settings?adv_success=تم+تحليل+ملف+الباك+أب+بنجاح#adv", http.StatusFound)
}

// AnalyzeServerFileHandler analyzes the pre-existing server backup file
func (bp *Blueprint) AnalyzeServerFileHandler(w http.ResponseWriter, r *http.Request) {
	possiblePaths := []string{
		"/opt/radius-server/adv_backup.sql",
		"/opt/radius-server/uploads/adv/adv_uploaded.sql",
		"./uploads/adv/adv_uploaded.sql",
		"C:\\Users\\Haitham Abd-Elkader\\Downloads\\adv_db-Drwbz261022_11_59_20.sql\\adv_db-Drwbz261022_11_59_20.sql",
		"D:\\AI Files\\adv_db-iFrRe261021_17_10_21.sql",
		"./adv_backup.sql",
		"/tmp/adv_backup.sql",
	}

	var foundPath string
	for _, p := range possiblePaths {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			foundPath = p
			break
		}
	}

	if foundPath == "" {
		http.Redirect(w, r, "/settings?adv_error=لم+يتم+العثور+على+ملف+باك+أب+جاهز+على+السيرفر#adv", http.StatusFound)
		return
	}

	analysis, err := ParseAdvSQL(foundPath)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/settings?adv_error=فشل+تحليل+النسخة:+%v#adv", err), http.StatusFound)
		return
	}

	cachedAnalysisMu.Lock()
	cachedAnalysis = analysis
	lastBackupPath = foundPath
	cachedAnalysisMu.Unlock()

	http.Redirect(w, r, "/settings?adv_success=تم+فحص+وتحليل+النسخة+الموجودة+على+السيرفر+بنجاح#adv", http.StatusFound)
}

// ImportHandler executes the actual database migration into our system with ultra-fast batch operations
func (bp *Blueprint) ImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings#adv", http.StatusFound)
		return
	}

	cachedAnalysisMu.Lock()
	analysis := cachedAnalysis
	cachedAnalysisMu.Unlock()

	if analysis == nil {
		http.Redirect(w, r, "/settings?adv_error=لا+توجد+بيانات+محللة+للاستيراد.+يرجى+تحليل+الملف+أولاً#adv", http.StatusFound)
		return
	}

	importProfiles := r.FormValue("import_profiles") == "1"
	importResellers := r.FormValue("import_resellers") == "1"
	importSubscribers := r.FormValue("import_subscribers") == "1"
	importAccounting := r.FormValue("import_accounting") == "1"

	defaultPassword := strings.TrimSpace(r.FormValue("default_password"))
	if defaultPassword == "" {
		defaultPassword = "0000"
	}
	resellerPassword := strings.TrimSpace(r.FormValue("reseller_password"))
	if resellerPassword == "" {
		resellerPassword = "123456"
	}

	importedProfilesCount := 0
	importedResellersCount := 0
	importedSubscribersCount := 0
	importedSessionsCount := 0

	// Speed optimization: disable integrity checks for bulk ingestion
	_, _ = bp.db.Exec("SET foreign_key_checks = 0")
	_, _ = bp.db.Exec("SET unique_checks = 0")
	defer func() {
		_, _ = bp.db.Exec("SET foreign_key_checks = 1")
		_, _ = bp.db.Exec("SET unique_checks = 1")
	}()

	tx, err := bp.db.Begin()
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/settings?adv_error=فشل+بدء+معاملة+قاعدة+البيانات:+%v#adv", err), http.StatusFound)
		return
	}
	defer tx.Rollback()

	// 1. Import Profiles
	profQuotaMap := make(map[string]int64)
	if importProfiles {
		for _, p := range analysis.Profiles {
			rateLimit := p.RateLimit
			if rateLimit == "" && p.DownSpeed != "" && p.UpSpeed != "" {
				rateLimit = formatRateLimit(p.UpSpeed, p.DownSpeed)
			}
			quotaBytes := int64(p.QuotaMB) * 1024 * 1024
			profQuotaMap[p.Name] = quotaBytes

			_, err := tx.Exec(`INSERT INTO profiles (name, display_name, rate_limit, quota_bytes, price, validity_value, validity_unit)
				VALUES (?, ?, ?, ?, ?, 1, 'months')
				ON DUPLICATE KEY UPDATE
					display_name = VALUES(display_name),
					rate_limit = VALUES(rate_limit),
					quota_bytes = VALUES(quota_bytes),
					price = VALUES(price)`,
				p.Name, p.Name, rateLimit, quotaBytes, p.Price)
			if err != nil {
				log.Printf("[ADV-IMPORT] Error inserting profile %s: %v", p.Name, err)
				continue
			}

			// Update radgroupreply for FreeRADIUS
			_, _ = tx.Exec("DELETE FROM radgroupreply WHERE groupname = ? AND attribute = 'Mikrotik-Rate-Limit'", p.Name)
			if rateLimit != "" {
				_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Mikrotik-Rate-Limit', ':=', ?)", p.Name, rateLimit)
			}
			importedProfilesCount++
		}
	} else {
		// Populate profQuotaMap from existing profiles in DB
		rows, err := tx.Query("SELECT name, quota_bytes FROM profiles")
		if err == nil {
			for rows.Next() {
				var n string
				var q int64
				if err := rows.Scan(&n, &q); err == nil {
					profQuotaMap[n] = q
				}
			}
			rows.Close()
		}
	}

	// 2. Import Resellers (Managers)
	resellerIDMap := make(map[int]int64) // ADV Manager ID -> Our Reseller ID
	if importResellers {
		passHash, _ := bcrypt.GenerateFromPassword([]byte(resellerPassword), bcrypt.DefaultCost)
		for _, m := range analysis.Managers {
			if m.IsAdmin == 1 || strings.ToLower(m.Username) == "admin" {
				continue // Skip main admin
			}

			name := m.FullName
			if name == "" {
				name = m.Username
			}

			credit := m.Credit
			if credit > 999999999999.00 {
				credit = 999999999.00
			}

			res, err := tx.Exec(`INSERT INTO resellers (name, username, password_hash, phone, balance, is_active)
				VALUES (?, ?, ?, ?, ?, 1)
				ON DUPLICATE KEY UPDATE
					name = VALUES(name),
					phone = VALUES(phone),
					balance = VALUES(balance)`,
				name, m.Username, string(passHash), m.Phone, credit)
			if err == nil {
				resID, _ := res.LastInsertId()
				if resID == 0 {
					_ = tx.QueryRow("SELECT id FROM resellers WHERE username = ?", m.Username).Scan(&resID)
				}
				resellerIDMap[m.ID] = resID
				importedResellersCount++
			} else {
				log.Printf("[ADV-IMPORT] Error inserting reseller %s: %v", m.Username, err)
			}
		}
	} else {
		// Populate resellerIDMap from existing resellers
		rows, err := tx.Query("SELECT id, username FROM resellers")
		if err == nil {
			for rows.Next() {
				var id int64
				var uname string
				if err := rows.Scan(&id, &uname); err == nil {
					for _, m := range analysis.Managers {
						if m.Username == uname {
							resellerIDMap[m.ID] = id
						}
					}
				}
			}
			rows.Close()
		}
	}

	// 3. Import Subscribers & Accounting (Batched for instant speed)
	if importSubscribers && len(analysis.Subscribers) > 0 {
		now := time.Now()

		type SubRow struct {
			Username    string
			ContractNo  string
			FullName    string
			Phone       string
			Address     string
			Debt        float64
			Balance     float64
			ProfileName string
			LastMAC     string
			LastIP      string
			QuotaBytes  int64
			ExpDate     time.Time
			Status      string
			ResellerID  int64
			CreatedAt   time.Time
			LastRenewAt time.Time
			Password    string
		}

		var subRows []SubRow
		for _, s := range analysis.Subscribers {
			pass := s.Password
			if pass == "" || pass == "(بدون باسورد)" || pass == "(فارغ / بدون باسورد)" {
				if defaultPassword == "username" {
					pass = s.Username
				} else {
					pass = defaultPassword
				}
			}

			var ourResellerID int64 = 0
			if s.OwnerID > 1 {
				ourResellerID = resellerIDMap[s.OwnerID]
			}

			var expDate time.Time
			if s.ExpTime > 0 {
				expDate = time.Unix(s.ExpTime, 0)
			} else {
				expDate = now.AddDate(0, 1, 0)
			}

			status := "active"
			if now.After(expDate) {
				status = "expired"
			}
			if s.InternetStatus == "disabled" {
				status = "disabled"
			}

			quotaBytes := profQuotaMap[s.ProfileName]

			createdAt := now
			if s.CreationTime > 0 {
				createdAt = time.Unix(s.CreationTime, 0)
			}

			var lastRenewedAt time.Time
			if s.LastRenewTime > 1000000000 {
				lastRenewedAt = time.Unix(s.LastRenewTime, 0)
			} else if s.ExpTime > 0 {
				lastRenewedAt = time.Unix(s.ExpTime, 0).AddDate(0, -1, 0)
			} else {
				lastRenewedAt = now.AddDate(0, -1, 0)
			}

			subRows = append(subRows, SubRow{
				Username:    s.Username,
				ContractNo:  s.Username,
				FullName:    s.FullName,
				Phone:       s.Phone,
				Address:     s.Address,
				Debt:        s.TotalUnpaid,
				Balance:     s.Money,
				ProfileName: s.ProfileName,
				LastMAC:     s.LastMAC,
				LastIP:      s.LastIP,
				QuotaBytes:  quotaBytes,
				ExpDate:     expDate,
				Status:      status,
				ResellerID:  ourResellerID,
				CreatedAt:   createdAt,
				LastRenewAt: lastRenewedAt,
				Password:    pass,
			})
		}

		// Insert Subscribers in batches of 100
		const subBatchSize = 100
		for i := 0; i < len(subRows); i += subBatchSize {
			end := i + subBatchSize
			if end > len(subRows) {
				end = len(subRows)
			}
			chunk := subRows[i:end]

			var valPlaceholders []string
			var valArgs []interface{}
			for _, sr := range chunk {
				valPlaceholders = append(valPlaceholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
				valArgs = append(valArgs,
					sr.Username, sr.ContractNo, sr.FullName, sr.Phone, sr.Address, sr.Debt, sr.Balance, sr.ProfileName,
					sr.LastMAC, sr.LastIP, sr.QuotaBytes, sr.ExpDate, sr.Status, sr.ResellerID,
					sr.CreatedAt, sr.LastRenewAt)
			}

			query := fmt.Sprintf(`INSERT INTO subscribers (
				username, contract_no, full_name, phone, address, debt, balance, profile_name,
				calling_mac, static_ip, quota_bytes, expiration_date, status, reseller_id,
				created_at, last_renewed_at
			) VALUES %s
			ON DUPLICATE KEY UPDATE
				full_name = VALUES(full_name),
				phone = VALUES(phone),
				debt = VALUES(debt),
				balance = VALUES(balance),
				profile_name = VALUES(profile_name),
				calling_mac = VALUES(calling_mac),
				static_ip = VALUES(static_ip),
				quota_bytes = VALUES(quota_bytes),
				expiration_date = VALUES(expiration_date),
				status = VALUES(status),
				reseller_id = VALUES(reseller_id),
				created_at = VALUES(created_at),
				last_renewed_at = VALUES(last_renewed_at)`, strings.Join(valPlaceholders, ","))

			if _, err := tx.Exec(query, valArgs...); err != nil {
				log.Printf("[ADV-IMPORT] Error batch inserting subscribers: %v", err)
			} else {
				importedSubscribersCount += len(chunk)
			}
		}

		// Batch FreeRADIUS radcheck (passwords & expiration)
		var radcheckArgs []interface{}
		var radcheckPlaceholders []string
		for _, sr := range subRows {
			radcheckPlaceholders = append(radcheckPlaceholders, "(?, 'Cleartext-Password', ':=', ?)")
			radcheckArgs = append(radcheckArgs, sr.Username, sr.Password)

			expStr := sr.ExpDate.Format("02 Jan 2006 15:04:05")
			radcheckPlaceholders = append(radcheckPlaceholders, "(?, 'Expiration', ':=', ?)")
			radcheckArgs = append(radcheckArgs, sr.Username, expStr)
		}

		const radBatchSize = 400
		for i := 0; i < len(radcheckPlaceholders); i += radBatchSize {
			end := i + radBatchSize
			if end > len(radcheckPlaceholders) {
				end = len(radcheckPlaceholders)
			}
			chunkPl := radcheckPlaceholders[i:end]
			chunkArgs := radcheckArgs[i*2 : end*2]
			q := fmt.Sprintf("INSERT INTO radcheck (username, attribute, op, value) VALUES %s ON DUPLICATE KEY UPDATE value = VALUES(value)", strings.Join(chunkPl, ","))
			if _, err := tx.Exec(q, chunkArgs...); err != nil {
				log.Printf("[ADV-IMPORT] Error batch inserting radcheck: %v", err)
			}
		}

		// Batch FreeRADIUS radusergroup
		var grpPlaceholders []string
		var grpArgs []interface{}
		for _, sr := range subRows {
			if sr.ProfileName != "" && sr.ProfileName != "غير محدد" {
				grpPlaceholders = append(grpPlaceholders, "(?, ?, 1)")
				grpArgs = append(grpArgs, sr.Username, sr.ProfileName)
			}
		}
		for i := 0; i < len(grpPlaceholders); i += radBatchSize {
			end := i + radBatchSize
			if end > len(grpPlaceholders) {
				end = len(grpPlaceholders)
			}
			chunkPl := grpPlaceholders[i:end]
			chunkArgs := grpArgs[i*2 : end*2]
			q := fmt.Sprintf("INSERT INTO radusergroup (username, groupname, priority) VALUES %s ON DUPLICATE KEY UPDATE groupname = VALUES(groupname)", strings.Join(chunkPl, ","))
			if _, err := tx.Exec(q, chunkArgs...); err != nil {
				log.Printf("[ADV-IMPORT] Error batch inserting radusergroup: %v", err)
			}
		}

		// Batch FreeRADIUS radreply (quota limit)
		var repPlaceholders []string
		var repArgs []interface{}
		for _, sr := range subRows {
			if sr.QuotaBytes > 0 {
				repPlaceholders = append(repPlaceholders, "(?, 'Mikrotik-Total-Limit', ':=', ?)")
				repArgs = append(repArgs, sr.Username, fmt.Sprintf("%d", sr.QuotaBytes))
			}
		}
		for i := 0; i < len(repPlaceholders); i += radBatchSize {
			end := i + radBatchSize
			if end > len(repPlaceholders) {
				end = len(repPlaceholders)
			}
			chunkPl := repPlaceholders[i:end]
			chunkArgs := repArgs[i*2 : end*2]
			q := fmt.Sprintf("INSERT INTO radreply (username, attribute, op, value) VALUES %s ON DUPLICATE KEY UPDATE value = VALUES(value)", strings.Join(chunkPl, ","))
			if _, err := tx.Exec(q, chunkArgs...); err != nil {
				log.Printf("[ADV-IMPORT] Error batch inserting radreply: %v", err)
			}
		}

		// Batch Billing Transactions for debt / deposit balances
		var billPl []string
		var billArgs []interface{}
		for _, sr := range subRows {
			if sr.Debt > 0 {
				billPl = append(billPl, "(?, ?, ?, 'debt', ?, ?, 'pending', 'system', 'رصيد مديونية مرحل من نظام ADV القديم', 'admin')")
				billArgs = append(billArgs, sr.Username, sr.Username, sr.FullName, fmt.Sprintf("مديونية سابقة مرحلة من ADV: %.2f ج.م", sr.Debt), sr.Debt)
			}
			if sr.Balance > 0 {
				billPl = append(billPl, "(?, ?, ?, 'deposit', ?, ?, 'paid', 'system', 'رصيد محفظة مرحل من نظام ADV القديم', 'admin')")
				billArgs = append(billArgs, sr.Username, sr.Username, sr.FullName, fmt.Sprintf("رصيد محفظة مرحل من ADV: %.2f ج.م", sr.Balance), sr.Balance)
			}
		}
		for i := 0; i < len(billPl); i += 200 {
			end := i + 200
			if end > len(billPl) {
				end = len(billPl)
			}
			chunkPl := billPl[i:end]
			chunkArgs := billArgs[i*6 : end*6]
			q := fmt.Sprintf("INSERT INTO billing_transactions (username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by) VALUES %s", strings.Join(chunkPl, ","))
			_, _ = tx.Exec(q, chunkArgs...)
		}

		// 4. Batch Radacct Accounting Sessions (if selected)
		if importAccounting {
			var allSessions []AdvRadacctSession
			for _, sr := range subRows {
				if sessList, ok := analysis.SubscriberSessions[sr.Username]; ok {
					allSessions = append(allSessions, sessList...)
				}
			}

			const acctBatchSize = 400
			for i := 0; i < len(allSessions); i += acctBatchSize {
				end := i + acctBatchSize
				if end > len(allSessions) {
					end = len(allSessions)
				}
				chunk := allSessions[i:end]

				var pl []string
				var args []interface{}
				for _, sess := range chunk {
					pl = append(pl, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
					var startTime, stopTime interface{}
					if sess.StartTime != "" && sess.StartTime != "NULL" && sess.StartTime != "0000-00-00 00:00:00" {
						startTime = sess.StartTime
					}
					if sess.StopTime != "" && sess.StopTime != "NULL" && sess.StopTime != "0000-00-00 00:00:00" {
						stopTime = sess.StopTime
					}
					args = append(args,
						sess.SessionID, sess.UniqueID, sess.Username, sess.Realm, sess.NasIP,
						sess.NasPortID, sess.NasPortType, startTime, stopTime, sess.SessionSec,
						sess.AcctAuthentic, sess.ConnectInfoStart, sess.ConnectInfoStop, sess.InOctets, sess.OutOctets,
						sess.CalledMAC, sess.CallingMAC, sess.TermCause, sess.ServiceType, sess.FramedProto, sess.FramedIP)
				}

				q := fmt.Sprintf(`INSERT IGNORE INTO radacct (
					acctsessionid, acctuniqueid, username, realm, nasipaddress,
					nasportid, nasporttype, acctstarttime, acctstoptime, acctsessiontime,
					acctauthentic, connectinfo_start, connectinfo_stop, acctinputoctets, acctoutputoctets,
					calledstationid, callingstationid, acctterminatecause, servicetype, framedprotocol, framedipaddress
				) VALUES %s`, strings.Join(pl, ","))

				if _, err := tx.Exec(q, args...); err != nil {
					log.Printf("[ADV-IMPORT] Error batch inserting radacct: %v", err)
				} else {
					importedSessionsCount += len(chunk)
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/settings?adv_error=فشل+تأكيد+البيانات+في+السيرفر:+%v#adv", err), http.StatusFound)
		return
	}

	msg := fmt.Sprintf("تم+استرداد+ونقل+البيانات+بسرعة+فائقة!+تم+استيراد+%d+مشترك،+و+%d+باقة،+و+%d+موزع،+و+%d+جلسة+استهلاك.",
		importedSubscribersCount, importedProfilesCount, importedResellersCount, importedSessionsCount)
	http.Redirect(w, r, fmt.Sprintf("/settings?adv_success=%s&subs=%d&profs=%d&resellers=%d&sessions=%d#adv", msg, importedSubscribersCount, importedProfilesCount, importedResellersCount, importedSessionsCount), http.StatusFound)
}

// formatRateLimit formats up and down speeds from ADV into MikroTik "rx/tx" rate limit
func formatRateLimit(upStr, downStr string) string {
	up, _ := strconv.Atoi(strings.TrimSpace(upStr))
	down, _ := strconv.Atoi(strings.TrimSpace(downStr))

	if up <= 0 && down <= 0 {
		return ""
	}

	upFormatted := formatSpeedUnits(up)
	downFormatted := formatSpeedUnits(down)

	return fmt.Sprintf("%s/%s", upFormatted, downFormatted)
}

func formatSpeedUnits(kbps int) string {
	if kbps <= 0 {
		return "1M"
	}
	if kbps >= 1024 && kbps%1024 == 0 {
		return fmt.Sprintf("%dM", kbps/1024)
	}
	return fmt.Sprintf("%dk", kbps)
}

// ParseAdvSQL parses an ADV / SAS4 MySQL dump file
func ParseAdvSQL(filePath string) (*BackupAnalysis, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fi, _ := file.Stat()
	fileSizeMB := fmt.Sprintf("%.2f MB", float64(fi.Size())/(1024*1024))

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 50*1024*1024)

	insertRe := regexp.MustCompile("(?i)^INSERT INTO `?([a-zA-Z0-9_]+)`?")

	profilesMap := make(map[string]ProfileData)
	managersMap := make(map[int]ManagerData)
	passwordsMap := make(map[string]string)
	usergroupMap := make(map[string]string)
	subscribers := make(map[string]*SubscriberData)
	sessionsByUser := make(map[string][]AdvRadacctSession)
	userIDToUsername := make(map[string]string)
	userinfoUpdatesDebt := make(map[string]float64)
	userinfoUpdatesPaid := make(map[string]float64)
	cardsCount := 0

	for scanner.Scan() {
		line := scanner.Text()

		m := insertRe.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		tbl := m[1]

		switch tbl {
		case "profiles":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) > 13 {
					id, _ := strconv.Atoi(r[0])
					name := r[1]
					price, _ := strconv.ParseFloat(r[2], 64)
					quota, _ := strconv.Atoi(r[9])
					downSpeed := r[12]
					upSpeed := r[13]
					profilesMap[name] = ProfileData{
						ID:        id,
						Name:      name,
						Price:     price,
						DownSpeed: downSpeed,
						UpSpeed:   upSpeed,
						QuotaMB:   quota,
						RateLimit: formatRateLimit(upSpeed, downSpeed),
					}
				}
			}

		case "managers":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) > 22 {
					id, _ := strconv.Atoi(r[0])
					uname := r[1]
					fname := r[4]
					phone := r[7]
					isAdmin, _ := strconv.Atoi(r[17])
					credit, _ := strconv.ParseFloat(r[22], 64)
					managersMap[id] = ManagerData{
						ID:       id,
						Username: uname,
						FullName: fname,
						Phone:    phone,
						Credit:   credit,
						IsAdmin:  isAdmin,
					}
				}
			}

		case "radcheck":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) >= 5 {
					uname := r[1]
					attr := r[2]
					val := r[4]
					if attr == "Cleartext-Password" && val != "" {
						passwordsMap[uname] = val
					}
					isCard := 0
					if len(r) > 6 {
						isCard, _ = strconv.Atoi(r[6])
					}
					if isCard == 1 {
						cardsCount++
					}
				}
			}

		case "radusergroup":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) >= 2 {
					uname := r[0]
					grp := r[1]
					usergroupMap[uname] = grp
				}
			}

		case "radacct":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) > 22 {
					uname := r[3]
					sec, _ := strconv.ParseInt(r[11], 10, 64)
					inOct, _ := strconv.ParseInt(r[15], 10, 64)
					outOct, _ := strconv.ParseInt(r[16], 10, 64)

					sess := AdvRadacctSession{
						SessionID:        r[1],
						UniqueID:         r[2],
						Username:         uname,
						Realm:            r[5],
						NasIP:            r[6],
						NasPortID:        r[7],
						NasPortType:      r[8],
						StartTime:        r[9],
						StopTime:         r[10],
						SessionSec:       sec,
						AcctAuthentic:    r[12],
						ConnectInfoStart: r[13],
						ConnectInfoStop:  r[14],
						InOctets:         inOct,
						OutOctets:        outOct,
						CalledMAC:        r[17],
						CallingMAC:       r[18],
						TermCause:        r[19],
						ServiceType:      r[20],
						FramedProto:      r[21],
						FramedIP:         r[22],
					}
					sessionsByUser[uname] = append(sessionsByUser[uname], sess)
				}
			}

		case "userinfo":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) >= 20 {
					userID := r[0]
					uname := r[1]
					userIDToUsername[userID] = uname
					fname := r[2]
					lname := r[3]
					phone := r[5]
					addr := r[7]
					cdate, _ := strconv.ParseInt(r[11], 10, 64)
					inDown, _ := strconv.ParseInt(r[15], 10, 64)
					inUp, _ := strconv.ParseInt(r[16], 10, 64)
					ownerID, _ := strconv.Atoi(r[19])
					avQuota, _ := strconv.ParseFloat(r[28], 64)
					money, _ := strconv.ParseFloat(r[37], 64)

					var expTime, rdate int64
					var unpaid, paid float64
					status := "enabled"

					if len(r) > 80 {
						expTime, _ = strconv.ParseInt(r[80], 10, 64)
					}
					if len(r) > 84 {
						paid, _ = strconv.ParseFloat(r[83], 64)
						unpaid, _ = strconv.ParseFloat(r[84], 64)
					}
					if len(r) > 86 {
						status = r[86]
					}
					if len(r) > 90 {
						rdate, _ = strconv.ParseInt(r[90], 10, 64)
					}

					fullName := strings.TrimSpace(fname + " " + lname)
					if fullName == "" {
						fullName = uname
					}

					subscribers[uname] = &SubscriberData{
						Username:       uname,
						FullName:       fullName,
						Phone:          phone,
						Address:        addr,
						OwnerID:        ownerID,
						Money:          money,
						InDownBytes:    inDown,
						InUpBytes:      inUp,
						AvQuotaMB:      avQuota,
						ExpTime:        expTime,
						TotalPaid:      paid,
						TotalUnpaid:    unpaid,
						InternetStatus: status,
						CreationTime:   cdate,
						LastRenewTime:  rdate,
					}
				}
			}

		case "userinfo_updates":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) >= 3 {
					uid := r[0]
					paid, _ := strconv.ParseFloat(r[1], 64)
					unpaid, _ := strconv.ParseFloat(r[2], 64)
					userinfoUpdatesDebt[uid] = unpaid
					userinfoUpdatesPaid[uid] = paid
				}
			}
		}
	}

	// Apply debts & paid invoices from userinfo_updates if present
	for uid, unpaid := range userinfoUpdatesDebt {
		if uname, ok := userIDToUsername[uid]; ok {
			if sub, exists := subscribers[uname]; exists {
				sub.TotalUnpaid = unpaid
				if p, ok := userinfoUpdatesPaid[uid]; ok {
					sub.TotalPaid = p
				}
			}
		}
	}

	now := time.Now()
	nowUnix := now.Unix()
	var activeCount, expCount int
	var totalDebt, totalMoney float64
	var totalTraffic int64
	var totalSubSessions int

	subSessionsMap := make(map[string][]AdvRadacctSession)

	// Link relations and compute traffic
	for uname, sub := range subscribers {
		if pw, ok := passwordsMap[uname]; ok {
			sub.Password = pw
		} else {
			sub.Password = "(بدون باسورد)"
		}
		if grp, ok := usergroupMap[uname]; ok {
			sub.ProfileName = grp
		} else {
			sub.ProfileName = "غير محدد"
		}
		if mgr, ok := managersMap[sub.OwnerID]; ok {
			sub.OwnerName = fmt.Sprintf("%s (%s)", mgr.FullName, mgr.Username)
		} else if sub.OwnerID == 1 {
			sub.OwnerName = "المدير العام (admin)"
		} else {
			sub.OwnerName = fmt.Sprintf("موزع #%d", sub.OwnerID)
		}

		if sub.ExpTime > 0 {
			sub.ExpDateStr = time.Unix(sub.ExpTime, 0).Format("2006-01-02")
			if sub.ExpTime < nowUnix {
				sub.IsExpired = true
				expCount++
			} else {
				sub.IsExpired = false
				activeCount++
			}
		} else {
			sub.ExpDateStr = "غير محدد"
			activeCount++
		}

		totalDebt += sub.TotalUnpaid
		totalMoney += sub.Money

		// Calculate Traffic / Sessions from parsed radacct
		userSessions := sessionsByUser[uname]
		sub.SessionsCount = len(userSessions)
		var totalIn, totalOut int64
		var lastMAC, lastIP string

		for _, s := range userSessions {
			totalIn += s.InOctets
			totalOut += s.OutOctets
			if s.CallingMAC != "" {
				lastMAC = s.CallingMAC
			}
			if s.FramedIP != "" && s.FramedIP != "0.0.0.0" {
				lastIP = s.FramedIP
			}
		}

		sub.TotalInBytes = totalIn
		sub.TotalOutBytes = totalOut
		sub.TotalUsedBytes = totalIn + totalOut
		sub.LastMAC = lastMAC
		sub.LastIP = lastIP

		// Determine quota bytes from profile
		profQuotaMB := 0
		if p, ok := profilesMap[sub.ProfileName]; ok {
			profQuotaMB = p.QuotaMB
		}
		sub.QuotaBytes = int64(profQuotaMB) * 1024 * 1024

		if sub.QuotaBytes > 0 {
			sub.QuotaDisplay = database.FormatBytesHuman(sub.QuotaBytes)
			rem := sub.QuotaBytes - sub.TotalUsedBytes
			if rem < 0 {
				rem = 0
			}
			sub.RemainingBytes = rem
			sub.RemainingDisplay = database.FormatBytesHuman(rem)
			sub.UsagePercent = (float64(sub.TotalUsedBytes) / float64(sub.QuotaBytes)) * 100.0
			if sub.UsagePercent > 100.0 {
				sub.UsagePercent = 100.0
			}
		} else {
			sub.QuotaDisplay = "غير محدود"
			sub.RemainingDisplay = "غير محدود"
		}

		sub.UsedDisplay = database.FormatBytesHuman(sub.TotalUsedBytes)
		sub.DownDisplay = database.FormatBytesHuman(sub.TotalOutBytes)
		sub.UpDisplay = database.FormatBytesHuman(sub.TotalInBytes)

		if len(userSessions) > 0 {
			subSessionsMap[uname] = userSessions
			totalSubSessions += len(userSessions)
			totalTraffic += sub.TotalUsedBytes
		}
	}

	var subList []SubscriberData
	for _, s := range subscribers {
		subList = append(subList, *s)
	}
	sort.Slice(subList, func(i, j int) bool {
		return subList[i].Username < subList[j].Username
	})

	var profList []ProfileData
	for _, p := range profilesMap {
		profList = append(profList, p)
	}
	sort.Slice(profList, func(i, j int) bool {
		return profList[i].Name < profList[j].Name
	})

	var mgrList []ManagerData
	for _, m := range managersMap {
		mgrList = append(mgrList, m)
	}
	sort.Slice(mgrList, func(i, j int) bool {
		return mgrList[i].ID < mgrList[j].ID
	})

	return &BackupAnalysis{
		FileName:            filepath.Base(filePath),
		FileSize:            fileSizeMB,
		AnalyzedAt:          time.Now(),
		Profiles:            profList,
		Managers:            mgrList,
		Subscribers:         subList,
		HotspotCards:        cardsCount,
		ActiveSubsCount:     activeCount,
		ExpSubsCount:        expCount,
		TotalDebt:           totalDebt,
		TotalMoney:          totalMoney,
		TotalSubSessions:    totalSubSessions,
		TotalTrafficBytes:   totalTraffic,
		TotalTrafficDisplay: database.FormatBytesHuman(totalTraffic),
		SubscriberSessions:  subSessionsMap,
	}, nil
}

func parseSQLValues(line string) [][]string {
	var rows [][]string
	idx := strings.Index(line, "VALUES ")
	if idx == -1 {
		idx = strings.Index(line, "values ")
	}
	if idx == -1 {
		return rows
	}
	data := line[idx+7:]

	inQuotes := false
	quoteChar := byte(0)
	inTuple := false
	var currentTuple []string
	var currentVal strings.Builder

	for i := 0; i < len(data); i++ {
		c := data[i]

		if inQuotes {
			if c == '\\' && i+1 < len(data) {
				currentVal.WriteByte(data[i+1])
				i++
				continue
			}
			if c == quoteChar {
				inQuotes = false
			} else {
				currentVal.WriteByte(c)
			}
			continue
		}

		if c == '\'' || c == '"' {
			inQuotes = true
			quoteChar = c
			continue
		}

		if c == '(' && !inTuple {
			inTuple = true
			currentTuple = nil
			currentVal.Reset()
			continue
		}

		if c == ')' && inTuple {
			currentTuple = append(currentTuple, strings.TrimSpace(currentVal.String()))
			currentVal.Reset()
			rows = append(rows, currentTuple)
			inTuple = false
			continue
		}

		if c == ',' && inTuple {
			currentTuple = append(currentTuple, strings.TrimSpace(currentVal.String()))
			currentVal.Reset()
			continue
		}

		if inTuple {
			currentVal.WriteByte(c)
		}
	}

	return rows
}
