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
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
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
}

// BackupAnalysis holds the parsed data from the ADV backup
type BackupAnalysis struct {
	FileName        string
	FileSize        string
	AnalyzedAt      time.Time
	Profiles        []ProfileData
	Managers        []ManagerData
	Subscribers     []SubscriberData
	HotspotCards    int
	ActiveSubsCount int
	ExpSubsCount    int
	TotalDebt       float64
}

var (
	cachedAnalysisMu sync.Mutex
	cachedAnalysis   *BackupAnalysis
	lastBackupPath   string
)

// IndexHandler renders the ADV Migration Dashboard
func (bp *Blueprint) IndexHandler(w http.ResponseWriter, r *http.Request) {
	cachedAnalysisMu.Lock()
	analysis := cachedAnalysis
	cachedAnalysisMu.Unlock()

	// If no analysis cached, try auto-detecting server backup
	if analysis == nil {
		possiblePaths := []string{
			"/opt/radius-server/adv_backup.sql",
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

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	data := map[string]interface{}{
		"Title":       "استرداد بيانات ADV / SAS4",
		"ActiveMenu":  "adv",
		"CurrentUser": middleware.GetUser(r),
		"Analysis":    analysis,
		"SuccessMsg":  successMsg,
		"ErrorMsg":    errorMsg,
		"ImportedSubs": r.URL.Query().Get("subs"),
		"ImportedProfs": r.URL.Query().Get("profs"),
		"ImportedResellers": r.URL.Query().Get("resellers"),
	}

	_ = bp.renderer.Render(w, "adv/index", data)
}

// UploadHandler handles uploading an ADV backup .sql file
func (bp *Blueprint) UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/adv", http.StatusFound)
		return
	}

	// Limit upload size to 250 MB
	_ = r.ParseMultipartForm(250 << 20)

	file, header, err := r.FormFile("backup_file")
	if err != nil {
		http.Redirect(w, r, "/adv?error=يرجى+اختيار+ملف+SQL+صحيح", http.StatusFound)
		return
	}
	defer file.Close()

	uploadDir := "./uploads/adv"
	_ = os.MkdirAll(uploadDir, 0755)
	destPath := filepath.Join(uploadDir, "adv_uploaded.sql")

	dst, err := os.Create(destPath)
	if err != nil {
		http.Redirect(w, r, "/adv?error=فشل+حفظ+الملف+على+السيرفر", http.StatusFound)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Redirect(w, r, "/adv?error=فشل+رفع+الملف", http.StatusFound)
		return
	}

	// Analyze the uploaded file
	analysis, err := ParseAdvSQL(destPath)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/adv?error=فشل+تحليل+الملف:+%v", err), http.StatusFound)
		return
	}
	analysis.FileName = header.Filename

	cachedAnalysisMu.Lock()
	cachedAnalysis = analysis
	lastBackupPath = destPath
	cachedAnalysisMu.Unlock()

	http.Redirect(w, r, "/adv?success=تم+تحليل+ملف+الباك+أب+بنجاح", http.StatusFound)
}

// AnalyzeServerFileHandler analyzes the pre-existing server backup file
func (bp *Blueprint) AnalyzeServerFileHandler(w http.ResponseWriter, r *http.Request) {
	possiblePaths := []string{
		"/opt/radius-server/adv_backup.sql",
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
		http.Redirect(w, r, "/adv?error=لم+يتم+العثور+على+ملف+باك+أب+جاهز+على+السيرفر", http.StatusFound)
		return
	}

	analysis, err := ParseAdvSQL(foundPath)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/adv?error=فشل+تحليل+النسخة:+%v", err), http.StatusFound)
		return
	}

	cachedAnalysisMu.Lock()
	cachedAnalysis = analysis
	lastBackupPath = foundPath
	cachedAnalysisMu.Unlock()

	http.Redirect(w, r, "/adv?success=تم+فحص+وتحليل+النسخة+الموجودة+على+السيرفر+بنجاح", http.StatusFound)
}

// ImportHandler executes the actual database migration into our system
func (bp *Blueprint) ImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/adv", http.StatusFound)
		return
	}

	cachedAnalysisMu.Lock()
	analysis := cachedAnalysis
	cachedAnalysisMu.Unlock()

	if analysis == nil {
		http.Redirect(w, r, "/adv?error=لا+توجد+بيانات+محللة+للاستيراد.+يرجى+تحليل+الملف+أولاً", http.StatusFound)
		return
	}

	importProfiles := r.FormValue("import_profiles") == "1"
	importResellers := r.FormValue("import_resellers") == "1"
	importSubscribers := r.FormValue("import_subscribers") == "1"
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

	tx, err := bp.db.Begin()
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/adv?error=فشل+بدء+معاملة+قاعدة+البيانات:+%v", err), http.StatusFound)
		return
	}
	defer tx.Rollback()

	// 1. Import Profiles
	if importProfiles {
		for _, p := range analysis.Profiles {
			rateLimit := p.RateLimit
			if rateLimit == "" && p.DownSpeed != "" && p.UpSpeed != "" {
				rateLimit = formatRateLimit(p.UpSpeed, p.DownSpeed)
			}
			quotaBytes := int64(p.QuotaMB) * 1024 * 1024

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

			res, err := tx.Exec(`INSERT INTO resellers (name, username, password_hash, phone, balance, is_active)
				VALUES (?, ?, ?, ?, ?, 1)
				ON DUPLICATE KEY UPDATE
					name = VALUES(name),
					phone = VALUES(phone),
					balance = VALUES(balance)`,
				name, m.Username, string(passHash), m.Phone, m.Credit)
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
	}

	// 3. Import Subscribers
	if importSubscribers {
		now := time.Now()
		for _, s := range analysis.Subscribers {
			// Password resolution
			pass := s.Password
			if pass == "" || pass == "(بدون باسورد)" || pass == "(فارغ / بدون باسورد)" {
				if defaultPassword == "username" {
					pass = s.Username
				} else {
					pass = defaultPassword
				}
			}

			// Reseller mapping
			var ourResellerID int64 = 0
			if s.OwnerID > 1 {
				ourResellerID = resellerIDMap[s.OwnerID]
			}

			// Expiration date
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

			// Quota
			var quotaBytes int64 = 0
			_ = tx.QueryRow("SELECT quota_bytes FROM profiles WHERE name = ?", s.ProfileName).Scan(&quotaBytes)

			_, err := tx.Exec(`INSERT INTO subscribers (
				username, contract_no, full_name, phone, address, debt, profile_name, quota_bytes, expiration_date, status, reseller_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				full_name = VALUES(full_name),
				phone = VALUES(phone),
				debt = VALUES(debt),
				profile_name = VALUES(profile_name),
				quota_bytes = VALUES(quota_bytes),
				expiration_date = VALUES(expiration_date),
				status = VALUES(status),
				reseller_id = VALUES(reseller_id)`,
				s.Username, s.Username, s.FullName, s.Phone, s.Address, s.TotalUnpaid, s.ProfileName, quotaBytes, expDate, status, ourResellerID)
			if err != nil {
				log.Printf("[ADV-IMPORT] Error inserting subscriber %s: %v", s.Username, err)
				continue
			}

			// Update FreeRADIUS radcheck, radusergroup, radreply
			expStr := expDate.Format("02 Jan 2006 15:04:05")
			_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ?", s.Username)
			_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)", s.Username, pass)
			_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", s.Username, expStr)

			_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", s.Username)
			if s.ProfileName != "" && s.ProfileName != "غير محدد" {
				_, _ = tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", s.Username, s.ProfileName)
			}

			_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", s.Username)
			if quotaBytes > 0 {
				_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", s.Username, fmt.Sprintf("%d", quotaBytes))
			}

			importedSubscribersCount++
		}
	}

	if err := tx.Commit(); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/adv?error=فشل+تأكيد+البيانات+في+السيرفر:+%v", err), http.StatusFound)
		return
	}

	msg := fmt.Sprintf("تم+استرداد+ونقل+البيانات+بنجاح!+تم+استيراد+%d+مشترك،+و+%d+باقة،+و+%d+موزع.",
		importedSubscribersCount, importedProfilesCount, importedResellersCount)
	http.Redirect(w, r, fmt.Sprintf("/adv?success=%s&subs=%d&profs=%d&resellers=%d", msg, importedSubscribersCount, importedProfilesCount, importedResellersCount), http.StatusFound)
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

		case "userinfo":
			rows := parseSQLValues(line)
			for _, r := range rows {
				if len(r) >= 20 {
					uname := r[1]
					fname := r[2]
					lname := r[3]
					phone := r[5]
					addr := r[7]
					inDown, _ := strconv.ParseInt(r[15], 10, 64)
					inUp, _ := strconv.ParseInt(r[16], 10, 64)
					ownerID, _ := strconv.Atoi(r[19])
					avQuota, _ := strconv.ParseFloat(r[28], 64)
					money, _ := strconv.ParseFloat(r[37], 64)

					var expTime int64
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
					}
				}
			}
		}
	}

	now := time.Now()
	nowUnix := now.Unix()
	var activeCount, expCount int
	var totalDebt float64

	// Link relations
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
		FileName:        filepath.Base(filePath),
		FileSize:        fileSizeMB,
		AnalyzedAt:      time.Now(),
		Profiles:        profList,
		Managers:        mgrList,
		Subscribers:     subList,
		HotspotCards:    cardsCount,
		ActiveSubsCount: activeCount,
		ExpSubsCount:    expCount,
		TotalDebt:       totalDebt,
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
