package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/models"
)

// TranslateLedgerOperationType translates raw financial operation types to descriptive Arabic.
func TranslateLedgerOperationType(opType string) string {
	opType = strings.TrimSpace(strings.ToLower(opType))
	switch opType {
	case "deposit", "dep-sub", "dep-res":
		return "إيداع شحن رصيد"
	case "renew", "renew-sub", "renew-res":
		return "تجديد اشتراك"
	case "add_quota", "quota-sub", "quota-res":
		return "إضافة كوتة إضافية"
	case "settle_debt", "pay-debt-sub", "debt-settle":
		return "سداد وتسوية مديونية"
	case "loan_grant", "grant-loan":
		return "منح سلفة طوارئ"
	case "loan_settlement", "settle-loan":
		return "خصم تسوية سلفة"
	case "cards_generation", "card-gen-res":
		return "توليد كروت شحن"
	case "adjust_balance", "balance-adjust":
		return "تعديل رصيد إداري"
	case "adjust_debt", "debt-adjust":
		return "تعديل دين إداري"
	default:
		if strings.Contains(opType, "renew") {
			return "تجديد اشتراك"
		}
		if strings.Contains(opType, "quota") {
			return "إضافة كوتة"
		}
		if strings.Contains(opType, "debt") {
			return "حركة مديونية"
		}
		if strings.Contains(opType, "dep") {
			return "شحن رصيد"
		}
		return opType
	}
}

// GetNASList returns all configured NAS routers for reports filtering
func (d *DB) GetNASList() ([]models.NAS, error) {
	rows, err := d.Query(`SELECT id, nasname, COALESCE(shortname, nasname), COALESCE(type, 'mikrotik') FROM nas ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.NAS
	for rows.Next() {
		var n models.NAS
		if err := rows.Scan(&n.ID, &n.NASName, &n.ShortName, &n.Type); err == nil {
			list = append(list, n)
		}
	}
	return list, nil
}

// -----------------------------------------------------------------------------
// Executive Overview Dashboard (لوحة المتابعة الشاملة)
// -----------------------------------------------------------------------------

func (d *DB) GetReportsExecutiveSummary(resellerID int64) (*models.ReportsExecutiveSummary, error) {
	s := &models.ReportsExecutiveSummary{}

	whereSub := "1=1"
	var subArgs []interface{}
	if resellerID > 0 {
		whereSub = "reseller_id = ?"
		subArgs = append(subArgs, resellerID)
	}

	// 1. Online Now
	if resellerID > 0 {
		_ = d.QueryRow(`SELECT COUNT(*) FROM radacct r 
			JOIN subscribers s ON r.username = s.username COLLATE utf8mb4_unicode_ci 
			WHERE r.acctstoptime IS NULL AND s.reseller_id = ?`, resellerID).Scan(&s.OnlineNow)
	} else {
		_ = d.QueryRow("SELECT COUNT(*) FROM radacct WHERE acctstoptime IS NULL").Scan(&s.OnlineNow)
	}

	// 2. Today's Traffic (64-bit Gigaword math)
	todayStart := time.Now().Format("2006-01-02 00:00:00")
	if resellerID > 0 {
		_ = d.QueryRow(fmt.Sprintf(`SELECT COALESCE(SUM((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED) + 
			(CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)), 0)
			FROM radacct r
			JOIN subscribers s ON r.username = s.username COLLATE utf8mb4_unicode_ci
			WHERE r.acctstarttime >= ? AND s.reseller_id = ?`), todayStart, resellerID).Scan(&s.TodayTrafficBytes)
	} else {
		_ = d.QueryRow(fmt.Sprintf(`SELECT COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED) + 
			(CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0)
			FROM radacct WHERE acctstarttime >= ?`), todayStart).Scan(&s.TodayTrafficBytes)
	}
	s.TodayTrafficDisplay = FormatBytesHuman(s.TodayTrafficBytes)

	// 3. Failed Logins Today
	_ = d.QueryRow("SELECT COUNT(*) FROM radpostauth WHERE reply = 'Access-Reject' AND authdate >= ?", todayStart).Scan(&s.FailedLoginsToday)

	// 4. Quota Exhausted & Expired
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers WHERE %s AND quota_bytes > 0 AND (SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = subscribers.username COLLATE utf8mb4_unicode_ci AND acctstarttime >= subscribers.last_renewed_at) >= quota_bytes", whereSub), subArgs...).Scan(&s.QuotaExhaustedCount)
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers WHERE %s AND expiration_date IS NOT NULL AND expiration_date < NOW()", whereSub), subArgs...).Scan(&s.ExpiredCount)
	s.TotalIssuesCount = s.QuotaExhaustedCount + s.ExpiredCount

	// 5. Customer Debt & Customer Balances
	_ = d.QueryRow(fmt.Sprintf("SELECT COALESCE(SUM(debt), 0), COALESCE(SUM(balance), 0) FROM subscribers WHERE %s", whereSub), subArgs...).Scan(&s.TotalCustomerDebt, &s.TotalCustomerBalance)

	// 6. Reseller Balances
	if resellerID > 0 {
		_ = d.QueryRow("SELECT COALESCE(balance, 0) FROM resellers WHERE id = ?", resellerID).Scan(&s.TotalResellerBalance)
	} else {
		_ = d.QueryRow("SELECT COALESCE(SUM(balance), 0) FROM resellers").Scan(&s.TotalResellerBalance)
	}

	// 7. Monthly Revenue
	monthStart := time.Now().Format("2006-01-01 00:00:00")
	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM billing_transactions WHERE created_at >= ?", monthStart).Scan(&s.TotalRevenueMonth)

	// 8. Routers Status
	_ = d.QueryRow("SELECT COUNT(*) FROM nas").Scan(&s.TotalRoutersCount)
	tenMinAgo := time.Now().Add(-10 * time.Minute).Format("2006-01-02 15:04:05")
	_ = d.QueryRow(`SELECT COUNT(DISTINCT nasipaddress) FROM radacct WHERE COALESCE(acctupdatetime, acctstarttime) >= ?`, tenMinAgo).Scan(&s.ActiveRoutersCount)

	// 9. Peak Traffic Hour Today
	var peakHour int
	var peakCount int
	err := d.QueryRow("SELECT HOUR(acctstarttime) as hr, COUNT(*) as c FROM radacct WHERE acctstarttime >= ? GROUP BY hr ORDER BY c DESC LIMIT 1", todayStart).Scan(&peakHour, &peakCount)
	if err == nil {
		s.PeakTrafficHourText = fmt.Sprintf("الساعة %02d:00 (%d جلسة)", peakHour, peakCount)
	} else {
		s.PeakTrafficHourText = "غير محدد بعد"
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// 1. Authentication & Logins Report (تقرير المصادقة وتسجيل الدخول)
// -----------------------------------------------------------------------------

func (d *DB) GetAuthLogReport(
	search, replyFilter, fromDate, toDate string,
	resellerID int64,
	limit, offset int,
) ([]models.AuthLogItem, int, *models.AuthLogSummary, []map[string]interface{}, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"1=1"}
	var args []interface{}

	if resellerID > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerID)
	}

	search = strings.TrimSpace(search)
	if search != "" {
		whereClauses = append(whereClauses, "(r.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ?)")
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern, pattern)
	}

	if replyFilter == "accept" {
		whereClauses = append(whereClauses, "r.reply = 'Access-Accept'")
	} else if replyFilter == "reject" {
		whereClauses = append(whereClauses, "r.reply = 'Access-Reject'")
	}

	if fromDate != "" {
		whereClauses = append(whereClauses, "r.authdate >= ?")
		if len(fromDate) == 10 {
			fromDate += " 00:00:00"
		}
		args = append(args, fromDate)
	}
	if toDate != "" {
		whereClauses = append(whereClauses, "r.authdate <= ?")
		if len(toDate) == 10 {
			toDate += " 23:59:59"
		}
		args = append(args, toDate)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. Summary
	var summary models.AuthLogSummary
	_ = d.QueryRow(fmt.Sprintf(`SELECT 
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.reply = 'Access-Accept' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.reply = 'Access-Reject' THEN 1 ELSE 0 END), 0)
		FROM radpostauth r
		LEFT JOIN subscribers s ON r.username = s.username COLLATE utf8mb4_unicode_ci
		WHERE %s`, whereSQL), args...).Scan(&summary.TotalAttempts, &summary.AcceptedCount, &summary.RejectedCount)

	if summary.TotalAttempts > 0 {
		summary.SuccessRatePercent = (float64(summary.AcceptedCount) / float64(summary.TotalAttempts)) * 100.0
	}

	total := int(summary.TotalAttempts)

	// 2. Data Query
	query := fmt.Sprintf(`SELECT 
		r.id,
		r.username,
		COALESCE(s.full_name, r.username),
		COALESCE(s.contract_no, '-'),
		COALESCE(res.name, 'المركز الرئيسي'),
		r.reply,
		r.authdate,
		COALESCE(s.status, 'unknown'),
		COALESCE(s.expiration_date, '1970-01-01'),
		COALESCE(s.quota_bytes, 0)
	FROM radpostauth r
	LEFT JOIN subscribers s ON r.username = s.username COLLATE utf8mb4_unicode_ci
	LEFT JOIN resellers res ON s.reseller_id = res.id
	WHERE %s
	ORDER BY r.id DESC
	LIMIT %d OFFSET %d`, whereSQL, limit, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, &summary, nil, err
	}
	defer rows.Close()

	var list []models.AuthLogItem
	for rows.Next() {
		var item models.AuthLogItem
		var subStatus string
		var subExp time.Time
		var subQuota int64

		if err := rows.Scan(
			&item.ID,
			&item.Username,
			&item.FullName,
			&item.ContractNo,
			&item.ResellerName,
			&item.Reply,
			&item.AuthDate,
			&subStatus,
			&subExp,
			&subQuota,
		); err == nil {
			item.AuthDateFormatted = item.AuthDate.Format("2006-01-02 15:04:05")
			if item.Reply == "Access-Accept" {
				item.IsSuccess = true
				item.StatusBadgeClass = "bg-success text-white"
				item.ReasonText = "تم قبول الدخول بنجاح"
				item.ErrorCategory = "ناجح"
			} else {
				item.IsSuccess = false
				item.StatusBadgeClass = "bg-danger text-white"
				if subStatus == "suspended" {
					item.ReasonText = "الحساب موقوف إدارياً"
					item.ErrorCategory = "حساب موقوف"
				} else if subStatus != "unknown" && !subExp.IsZero() && subExp.Before(time.Now()) {
					item.ReasonText = "اشتراك العميل منتهي الصلاحية"
					item.ErrorCategory = "انتهاء الصلاحية"
				} else if subStatus == "unknown" {
					item.ReasonText = "مستخدم غير مسجل بالنظام"
					item.ErrorCategory = "مستخدم غير مسجل"
				} else {
					item.ReasonText = "كلمة مرور غير مطابقة أو خطأ مصادقة"
					item.ErrorCategory = "كلمة مرور خاطئة"
				}
			}
			list = append(list, item)
		}
	}

	// 3. Top Failing Clients
	topFailingRows, err := d.Query(`SELECT username, COUNT(*) as fails 
		FROM radpostauth WHERE reply = 'Access-Reject' 
		GROUP BY username ORDER BY fails DESC LIMIT 5`)
	var topFailing []map[string]interface{}
	if err == nil {
		defer topFailingRows.Close()
		for topFailingRows.Next() {
			var u string
			var count int
			if err := topFailingRows.Scan(&u, &count); err == nil {
				topFailing = append(topFailing, map[string]interface{}{
					"Username": u,
					"Failures": count,
				})
			}
		}
	}

	return list, total, &summary, topFailing, nil
}

// -----------------------------------------------------------------------------
// 2. Subscriber Login & Session Reports (تقرير حالات تسجيل الدخول والجلسات)
// -----------------------------------------------------------------------------

func (d *DB) GetSubscriberLoginSessionsReport(
	search, status, nasIP, fromDate, toDate string,
	resellerID int64, sortBy string,
	limit, offset int,
) ([]models.SubscriberLoginSession, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"1=1"}
	var args []interface{}

	if resellerID > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerID)
	}

	search = strings.TrimSpace(search)
	if search != "" {
		whereClauses = append(whereClauses, "(r.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR r.framedipaddress LIKE ? OR r.callingstationid LIKE ?)")
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}

	status = strings.TrimSpace(status)
	if status == "online" {
		whereClauses = append(whereClauses, "r.acctstoptime IS NULL")
	} else if status == "closed" {
		whereClauses = append(whereClauses, "r.acctstoptime IS NOT NULL")
	} else if status == "stuck" {
		whereClauses = append(whereClauses, "r.acctstoptime IS NULL AND (r.acctstarttime < NOW() - INTERVAL 24 HOUR OR COALESCE(r.acctupdatetime, r.acctstarttime) < NOW() - INTERVAL 2 HOUR)")
	}

	nasIP = strings.TrimSpace(nasIP)
	if nasIP != "" && nasIP != "all" {
		whereClauses = append(whereClauses, "r.nasipaddress = ?")
		args = append(args, nasIP)
	}

	fromDate = strings.TrimSpace(fromDate)
	if fromDate != "" {
		whereClauses = append(whereClauses, "r.acctstarttime >= ?")
		if len(fromDate) == 10 {
			fromDate = fromDate + " 00:00:00"
		}
		args = append(args, fromDate)
	}

	toDate = strings.TrimSpace(toDate)
	if toDate != "" {
		whereClauses = append(whereClauses, "r.acctstarttime <= ?")
		if len(toDate) == 10 {
			toDate = toDate + " 23:59:59"
		}
		args = append(args, toDate)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. Total Count
	var total int
	countQuery := fmt.Sprintf(`SELECT COUNT(*) 
		FROM radacct r 
		LEFT JOIN subscribers s ON r.username = s.username COLLATE utf8mb4_unicode_ci
		WHERE %s`, whereSQL)
	_ = d.QueryRow(countQuery, args...).Scan(&total)

	// 2. Sorting
	orderBySQL := "r.radacctid DESC"
	switch sortBy {
	case "duration_desc":
		orderBySQL = "r.acctsessiontime DESC, r.radacctid DESC"
	case "traffic_desc":
		orderBySQL = `((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED) + 
			(CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)) DESC, r.radacctid DESC`
	}

	// 3. Data Query
	dataQuery := fmt.Sprintf(`SELECT 
		r.radacctid,
		r.acctsessionid,
		r.username,
		COALESCE(s.full_name, r.username),
		COALESCE(s.contract_no, '-'),
		COALESCE(s.phone, '-'),
		COALESCE(s.profile_name, ''),
		COALESCE(res.name, 'المركز الرئيسي'),
		r.nasipaddress,
		COALESCE(n.shortname, r.nasipaddress),
		COALESCE(r.framedipaddress, ''),
		COALESCE(r.callingstationid, ''),
		r.acctstarttime,
		r.acctstoptime,
		COALESCE(r.acctsessiontime, 0),
		((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED)),
		((CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)),
		COALESCE(r.acctterminatecause, '')
	FROM radacct r
	LEFT JOIN subscribers s ON r.username = s.username COLLATE utf8mb4_unicode_ci
	LEFT JOIN resellers res ON s.reseller_id = res.id
	LEFT JOIN nas n ON r.nasipaddress = n.nasname
	WHERE %s
	ORDER BY %s
	LIMIT %d OFFSET %d`, whereSQL, orderBySQL, limit, offset)

	rows, err := d.Query(dataQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sessions []models.SubscriberLoginSession
	for rows.Next() {
		var s models.SubscriberLoginSession
		var (
			fullName, contractNo, phone, profileName sql.NullString
			resellerName                             sql.NullString
			nasName, framedIP, callingStation        sql.NullString
			termCause                                sql.NullString
			stopTime                                 sql.NullTime
		)

		if err := rows.Scan(
			&s.RadAcctID,
			&s.AcctSessionID,
			&s.UserName,
			&fullName,
			&contractNo,
			&phone,
			&profileName,
			&resellerName,
			&s.NASIPAddress,
			&nasName,
			&framedIP,
			&callingStation,
			&s.AcctStartTime,
			&stopTime,
			&s.AcctSessionTime,
			&s.AcctInputOctets,
			&s.AcctOutputOctets,
			&termCause,
		); err == nil {
			if fullName.Valid {
				s.FullName = fullName.String
			}
			if contractNo.Valid {
				s.ContractNo = contractNo.String
			}
			if phone.Valid {
				s.Phone = phone.String
			}
			if profileName.Valid {
				s.ProfileName = profileName.String
			}
			if resellerName.Valid {
				s.ResellerName = resellerName.String
			}
			if nasName.Valid {
				s.NASName = nasName.String
			}
			if framedIP.Valid {
				s.FramedIPAddress = framedIP.String
			}
			if callingStation.Valid {
				s.CallingStationID = callingStation.String
			}
			if termCause.Valid {
				s.AcctTerminateCause = termCause.String
			}

			s.StartTimeFormatted = s.AcctStartTime.Format("2006-01-02 15:04:05")

			if stopTime.Valid {
				s.AcctStopTime = &stopTime.Time
				s.StopTimeFormatted = stopTime.Time.Format("2006-01-02 15:04:05")
				s.IsOnline = false
				s.StatusBadgeClass = "bg-secondary text-white"
				s.StatusText = "منتهية"
			} else {
				s.IsOnline = true
				s.StatusBadgeClass = "bg-success text-white"
				s.StatusText = "متصل الآن"
				s.StopTimeFormatted = "قيد الاتصال"
			}

			s.DurationFormatted = FormatDurationArabic(s.AcctSessionTime)
			s.TerminateCauseText = FormatTerminateCauseArabic(s.AcctTerminateCause, s.IsOnline)

			s.TotalBytes = s.AcctInputOctets + s.AcctOutputOctets
			s.UploadFormatted = FormatBytesHuman(s.AcctInputOctets)
			s.DownloadFormatted = FormatBytesHuman(s.AcctOutputOctets)
			s.TotalBytesFormatted = FormatBytesHuman(s.TotalBytes)

			sessions = append(sessions, s)
		}
	}

	return sessions, total, nil
}

// -----------------------------------------------------------------------------
// 3. Top Consumers Report (أعلى العملاء استهلاكاً)
// -----------------------------------------------------------------------------

func (d *DB) GetTopConsumersReport(resellerID int64, year, month, limit int) ([]models.TopConsumerItem, error) {
	if limit <= 0 {
		limit = 10
	}
	whereClauses := []string{"1=1"}
	var args []interface{}

	if resellerID > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerID)
	}

	if year > 2000 {
		if month >= 1 && month <= 12 {
			start := fmt.Sprintf("%04d-%02d-01 00:00:00", year, month)
			t, _ := time.Parse("2006-01-02 15:04:05", start)
			end := t.AddDate(0, 1, 0).Format("2006-01-02 15:04:05")
			whereClauses = append(whereClauses, "r.acctstarttime >= ? AND r.acctstarttime < ?")
			args = append(args, start, end)
		} else {
			start := fmt.Sprintf("%04d-01-01 00:00:00", year)
			end := fmt.Sprintf("%04d-01-01 00:00:00", year+1)
			whereClauses = append(whereClauses, "r.acctstarttime >= ? AND r.acctstarttime < ?")
			args = append(args, start, end)
		}
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	query := fmt.Sprintf(`SELECT 
		r.username,
		COALESCE(s.full_name, r.username),
		COALESCE(s.contract_no, '-'),
		COALESCE(s.profile_name, ''),
		COALESCE(res.name, 'المركز الرئيسي'),
		COALESCE(SUM((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED)), 0) as up_bytes,
		COALESCE(SUM((CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)), 0) as down_bytes,
		COUNT(*) as sessions,
		COALESCE(SUM(r.acctsessiontime), 0) as duration
	FROM radacct r
	LEFT JOIN subscribers s ON r.username = s.username COLLATE utf8mb4_unicode_ci
	LEFT JOIN resellers res ON s.reseller_id = res.id
	WHERE %s
	GROUP BY r.username
	ORDER BY (up_bytes + down_bytes) DESC
	LIMIT %d`, whereSQL, limit)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.TopConsumerItem
	for rows.Next() {
		var item models.TopConsumerItem
		var upBytes, downBytes int64
		var duration int64
		if err := rows.Scan(
			&item.Username,
			&item.FullName,
			&item.ContractNo,
			&item.ProfileName,
			&item.ResellerName,
			&upBytes,
			&downBytes,
			&item.SessionCount,
			&duration,
		); err == nil {
			item.TotalBytes = upBytes + downBytes
			item.TotalGB = float64(item.TotalBytes) / (1024 * 1024 * 1024)
			item.UploadGB = float64(upBytes) / (1024 * 1024 * 1024)
			item.DownloadGB = float64(downBytes) / (1024 * 1024 * 1024)
			item.TotalDisplay = FormatBytesHuman(item.TotalBytes)
			list = append(list, item)
		}
	}
	return list, nil
}

// -----------------------------------------------------------------------------
// 4. Quota & Expiry Tracking Report (تقرير الكوتة والصلاحية)
// -----------------------------------------------------------------------------

func (d *DB) GetQuotaExpiryReport(
	search, category string, resellerID int64,
	limit, offset int,
) ([]models.QuotaExpiryItem, int, *models.QuotaExpirySummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"1=1"}
	var args []interface{}

	if resellerID > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerID)
	}

	search = strings.TrimSpace(search)
	if search != "" {
		whereClauses = append(whereClauses, "(s.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?)")
		patt := "%" + search + "%"
		args = append(args, patt, patt, patt, patt)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	var summary models.QuotaExpirySummary
	_ = d.QueryRow(fmt.Sprintf(`SELECT 
		COALESCE(SUM(s.quota_bytes) / 1073741824, 0),
		COALESCE(SUM(CASE WHEN s.expiration_date < NOW() THEN 1 ELSE 0 END), 0)
		FROM subscribers s WHERE %s`, whereSQL), args...).Scan(&summary.TotalAssignedQuotaGB, &summary.NearExpiryTimeCount)

	var total int
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers s WHERE %s", whereSQL), args...).Scan(&total)

	query := fmt.Sprintf(`SELECT 
		s.id,
		s.username,
		s.full_name,
		s.contract_no,
		COALESCE(s.phone, '-'),
		COALESCE(res.name, 'المركز الرئيسي'),
		COALESCE(s.profile_name, '-'),
		COALESCE(s.quota_bytes, 0),
		COALESCE((SELECT SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED) + 
			(CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED))
			FROM radacct WHERE username = s.username COLLATE utf8mb4_unicode_ci AND acctstarttime >= COALESCE(s.last_renewed_at, s.created_at)), 0) as used_bytes,
		s.expiration_date,
		COALESCE(DATEDIFF(s.expiration_date, NOW()), 0) as days_rem
	FROM subscribers s
	LEFT JOIN resellers res ON s.reseller_id = res.id
	WHERE %s
	ORDER BY s.id DESC
	LIMIT %d OFFSET %d`, whereSQL, limit, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, &summary, err
	}
	defer rows.Close()

	var list []models.QuotaExpiryItem
	for rows.Next() {
		var item models.QuotaExpiryItem
		var expDate sql.NullTime

		if err := rows.Scan(
			&item.SubscriberID,
			&item.Username,
			&item.FullName,
			&item.ContractNo,
			&item.Phone,
			&item.ResellerName,
			&item.ProfileName,
			&item.TotalQuotaBytes,
			&item.UsedBytes,
			&expDate,
			&item.DaysRemaining,
		); err == nil {
			item.TotalQuotaGB = float64(item.TotalQuotaBytes) / (1024 * 1024 * 1024)
			item.UsedGB = float64(item.UsedBytes) / (1024 * 1024 * 1024)
			summary.TotalConsumedQuotaGB += item.UsedGB

			if item.TotalQuotaBytes > 0 {
				item.RemainingBytes = item.TotalQuotaBytes - item.UsedBytes
				if item.RemainingBytes < 0 {
					item.RemainingBytes = 0
				}
				item.RemainingGB = float64(item.RemainingBytes) / (1024 * 1024 * 1024)
				item.UsagePercent = (float64(item.UsedBytes) / float64(item.TotalQuotaBytes)) * 100.0
			} else {
				item.RemainingBytes = 0
				item.RemainingGB = 0
				item.UsagePercent = 0
			}

			if expDate.Valid {
				item.ExpirationDate = expDate.Time.Format("2006-01-02 15:04")
			} else {
				item.ExpirationDate = "مفتوح / غير محدد"
			}

			// Classification
			if item.TotalQuotaBytes > 0 && item.UsedBytes >= item.TotalQuotaBytes {
				item.StatusCategory = "exhausted"
				item.StatusText = "الكوتة منتهية"
				item.StatusBadgeClass = "bg-danger text-white"
				summary.ExhaustedCount++
			} else if item.TotalQuotaBytes > 0 && item.UsagePercent >= 80.0 {
				item.StatusCategory = "near_quota"
				item.StatusText = "قريب من النفاد"
				item.StatusBadgeClass = "bg-warning-subtle text-warning-emphasis border border-warning-subtle"
				summary.NearExpiryQuotaCount++
			} else if expDate.Valid && item.DaysRemaining <= 3 && item.DaysRemaining >= 0 {
				item.StatusCategory = "near_time"
				item.StatusText = "قريب من انتهاء الوقت"
				item.StatusBadgeClass = "bg-info-subtle text-info-emphasis border border-info-subtle"
			} else {
				item.StatusCategory = "healthy"
				item.StatusText = "طبيعي"
				item.StatusBadgeClass = "bg-success-subtle text-success border border-success-subtle"
			}

			list = append(list, item)
		}
	}

	return list, total, &summary, nil
}

// -----------------------------------------------------------------------------
// 5. Service Status & Disconnects (تقرير حالة الخدمة والفصل)
// -----------------------------------------------------------------------------

func (d *DB) GetServiceStatusReport(
	search, statusFilter string, resellerID int64,
	limit, offset int,
) ([]models.ServiceStatusItem, int, *models.ServiceStatusSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"1=1"}
	var args []interface{}

	if resellerID > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerID)
	}

	search = strings.TrimSpace(search)
	if search != "" {
		whereClauses = append(whereClauses, "(s.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ?)")
		patt := "%" + search + "%"
		args = append(args, patt, patt, patt)
	}

	if statusFilter != "" {
		whereClauses = append(whereClauses, "s.status = ?")
		args = append(args, statusFilter)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	var summary models.ServiceStatusSummary
	_ = d.QueryRow(fmt.Sprintf(`SELECT 
		COALESCE(SUM(CASE WHEN s.status = 'active' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN s.status = 'expired' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN s.status = 'suspended' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN s.status = 'dormant' THEN 1 ELSE 0 END), 0)
		FROM subscribers s WHERE %s`, whereSQL), args...).Scan(&summary.ActiveCount, &summary.ExpiredCount, &summary.SuspendedCount, &summary.DormantCount)

	_ = d.QueryRow("SELECT COUNT(*) FROM financial_outbox WHERE status = 'completed'").Scan(&summary.CoASuccessTotal)
	_ = d.QueryRow("SELECT COUNT(*) FROM financial_outbox WHERE status IN ('failed', 'dead_letter')").Scan(&summary.CoAFailTotal)

	var total int
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers s WHERE %s", whereSQL), args...).Scan(&total)

	query := fmt.Sprintf(`SELECT 
		s.id,
		s.username,
		s.full_name,
		COALESCE(res.name, 'المركز الرئيسي'),
		COALESCE(s.profile_name, '-'),
		COALESCE(s.status, 'active'),
		(SELECT MAX(acctstarttime) FROM radacct WHERE username = s.username COLLATE utf8mb4_unicode_ci) as last_login,
		(SELECT acctterminatecause FROM radacct WHERE username = s.username COLLATE utf8mb4_unicode_ci AND acctstoptime IS NOT NULL ORDER BY radacctid DESC LIMIT 1) as last_term,
		(SELECT COUNT(*) FROM radacct WHERE username = s.username COLLATE utf8mb4_unicode_ci AND acctstoptime IS NULL) as is_on
	FROM subscribers s
	LEFT JOIN resellers res ON s.reseller_id = res.id
	WHERE %s
	ORDER BY s.id DESC
	LIMIT %d OFFSET %d`, whereSQL, limit, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, &summary, err
	}
	defer rows.Close()

	var list []models.ServiceStatusItem
	for rows.Next() {
		var item models.ServiceStatusItem
		var lastLogin sql.NullTime
		var lastTerm sql.NullString
		var isOn int

		if err := rows.Scan(
			&item.SubscriberID,
			&item.Username,
			&item.FullName,
			&item.ResellerName,
			&item.ProfileName,
			&item.Status,
			&lastLogin,
			&lastTerm,
			&isOn,
		); err == nil {
			item.IsOnline = (isOn > 0)
			if lastLogin.Valid {
				item.LastLoginDate = lastLogin.Time.Format("2006-01-02 15:04")
				item.DaysSinceLastLogin = int(time.Since(lastLogin.Time).Hours() / 24)
			} else {
				item.LastLoginDate = "لم يسجل دخول"
				item.DaysSinceLastLogin = 999
			}
			if lastTerm.Valid {
				item.DisconnectReason = FormatTerminateCauseArabic(lastTerm.String, false)
			} else {
				item.DisconnectReason = "-"
			}

			switch item.Status {
			case "active":
				item.StatusText = "نشط"
				item.StatusBadgeClass = "bg-success text-white"
			case "expired":
				item.StatusText = "منتهي"
				item.StatusBadgeClass = "bg-danger text-white"
			case "suspended":
				item.StatusText = "موقوف"
				item.StatusBadgeClass = "bg-warning text-dark"
			default:
				item.StatusText = item.Status
				item.StatusBadgeClass = "bg-secondary text-white"
			}

			list = append(list, item)
		}
	}

	return list, total, &summary, nil
}

// -----------------------------------------------------------------------------
// 6. Subscribers & Retention (العملاء والاشتراكات)
// -----------------------------------------------------------------------------

func (d *DB) GetSubscribersGrowthReport(resellerID int64) ([]models.SubscribersGrowthItem, []models.SubscribersGrowthItem, *models.SubscribersGrowthSummary, error) {
	summary := &models.SubscribersGrowthSummary{}

	whereClause := "1=1"
	var args []interface{}
	if resellerID > 0 {
		whereClause = "s.reseller_id = ?"
		args = append(args, resellerID)
	}

	monthStart := time.Now().Format("2006-01-01 00:00:00")
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers s WHERE %s", whereClause), args...).Scan(&summary.TotalSubscribers)
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers s WHERE %s AND s.created_at >= ?", whereClause), append(args, monthStart)...).Scan(&summary.NewThisMonth)
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers s WHERE %s AND s.last_renewed_at >= ?", whereClause), append(args, monthStart)...).Scan(&summary.RenewedThisMonth)

	var activeCount int64
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers s WHERE %s AND s.status = 'active'", whereClause), args...).Scan(&activeCount)
	if summary.TotalSubscribers > 0 {
		summary.RetentionRate = (float64(activeCount) / float64(summary.TotalSubscribers)) * 100.0
		summary.ChurnRate = 100.0 - summary.RetentionRate
	}

	// 1. By Profile
	pRows, err := d.Query(fmt.Sprintf(`SELECT 
		COALESCE(s.profile_name, 'بدون باقة'),
		COUNT(*),
		COALESCE(SUM(CASE WHEN s.status = 'active' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN s.status != 'active' THEN 1 ELSE 0 END), 0)
		FROM subscribers s
		WHERE %s
		GROUP BY s.profile_name
		ORDER BY COUNT(*) DESC`, whereClause), args...)

	var byProfile []models.SubscribersGrowthItem
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var item models.SubscribersGrowthItem
			if err := pRows.Scan(&item.GroupName, &item.Count, &item.ActiveCount, &item.ExpiredCount); err == nil {
				if summary.TotalSubscribers > 0 {
					item.Percentage = (float64(item.Count) / float64(summary.TotalSubscribers)) * 100.0
				}
				byProfile = append(byProfile, item)
			}
		}
	}

	// 2. By Reseller
	rRows, err := d.Query(`SELECT 
		COALESCE(r.name, 'المركز الرئيسي'),
		COUNT(*),
		COALESCE(SUM(CASE WHEN s.status = 'active' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN s.status != 'active' THEN 1 ELSE 0 END), 0)
		FROM subscribers s
		LEFT JOIN resellers r ON s.reseller_id = r.id
		GROUP BY s.reseller_id
		ORDER BY COUNT(*) DESC`)

	var byReseller []models.SubscribersGrowthItem
	if err == nil {
		defer rRows.Close()
		for rRows.Next() {
			var item models.SubscribersGrowthItem
			if err := rRows.Scan(&item.GroupName, &item.Count, &item.ActiveCount, &item.ExpiredCount); err == nil {
				if summary.TotalSubscribers > 0 {
					item.Percentage = (float64(item.Count) / float64(summary.TotalSubscribers)) * 100.0
				}
				byReseller = append(byReseller, item)
			}
		}
	}

	return byProfile, byReseller, summary, nil
}

// -----------------------------------------------------------------------------
// 7. Subscriber Debts Report (تقرير ديون العملاء)
// -----------------------------------------------------------------------------

func (d *DB) GetSubscriberDebtsReport(
	search, debtStatus string, resellerID int64,
	limit, offset int,
) ([]models.SubscriberDebtItem, int, *models.SubscriberDebtsSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"s.debt > 0"}
	var args []interface{}

	if resellerID > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerID)
	}

	search = strings.TrimSpace(search)
	if search != "" {
		whereClauses = append(whereClauses, "(s.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?)")
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern, pattern, pattern)
	}

	switch debtStatus {
	case "critical":
		whereClauses = append(whereClauses, "(s.debt >= 500 OR DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)) > 30)")
	case "overdue":
		whereClauses = append(whereClauses, "(s.debt < 500 AND DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)) BETWEEN 8 AND 30)")
	case "minor":
		whereClauses = append(whereClauses, "(s.debt <= 100 AND DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)) <= 7)")
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	var summary models.SubscriberDebtsSummary
	_ = d.QueryRow(fmt.Sprintf(`SELECT 
		COUNT(*), 
		COALESCE(SUM(s.debt), 0),
		COALESCE(AVG(s.debt), 0),
		COALESCE(SUM(CASE WHEN (s.debt >= 500 OR DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)) > 60) THEN 1 ELSE 0 END), 0)
	FROM subscribers s
	WHERE %s`, whereSQL), args...).Scan(
		&summary.TotalDebtorsCount,
		&summary.TotalDebtAmount,
		&summary.AverageDebt,
		&summary.CriticalDebtsCount,
	)
	summary.AverageDebtPerUser = summary.AverageDebt

	total := int(summary.TotalDebtorsCount)

	todayStart := time.Now().Format("2006-01-02 00:00:00")
	_ = d.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM financial_ledger 
		WHERE operation_type IN ('settle_debt', 'debt_settle') AND created_at >= ?`, todayStart).Scan(&summary.TotalSettledToday)

	dataQuery := fmt.Sprintf(`SELECT 
		s.id,
		s.username,
		s.full_name,
		s.contract_no,
		COALESCE(s.phone, '-'),
		s.reseller_id,
		COALESCE(res.name, 'المركز الرئيسي'),
		COALESCE(s.balance, 0.00),
		s.debt,
		COALESCE(DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)), 0) as age_days,
		COALESCE((SELECT amount FROM billing_transactions WHERE username = s.username AND type IN ('debt_payment', 'settle_debt') ORDER BY id DESC LIMIT 1), 0.00) as last_pay,
		COALESCE((SELECT created_at FROM billing_transactions WHERE username = s.username AND type IN ('debt_payment', 'settle_debt') ORDER BY id DESC LIMIT 1), s.created_at) as last_pay_date,
		s.last_renewed_at,
		COALESCE(s.loan_active, 0)
	FROM subscribers s
	LEFT JOIN resellers res ON s.reseller_id = res.id
	WHERE %s
	ORDER BY s.debt DESC, age_days DESC
	LIMIT %d OFFSET %d`, whereSQL, limit, offset)

	rows, err := d.Query(dataQuery, args...)
	if err != nil {
		return nil, 0, &summary, err
	}
	defer rows.Close()

	var list []models.SubscriberDebtItem
	for rows.Next() {
		var item models.SubscriberDebtItem
		var lastPayDate sql.NullTime
		var lastRenewDate sql.NullTime
		var loanActiveInt int

		if err := rows.Scan(
			&item.SubscriberID,
			&item.Username,
			&item.FullName,
			&item.ContractNo,
			&item.Phone,
			&item.ResellerID,
			&item.ResellerName,
			&item.Balance,
			&item.Debt,
			&item.DebtAgeDays,
			&item.LastPaymentAmount,
			&lastPayDate,
			&lastRenewDate,
			&loanActiveInt,
		); err == nil {
			item.LoanActive = (loanActiveInt == 1)
			if lastPayDate.Valid {
				item.LastPaymentDate = lastPayDate.Time.Format("2006-01-02 15:04")
			} else {
				item.LastPaymentDate = "-"
			}
			if lastRenewDate.Valid {
				item.LastRenewalDate = lastRenewDate.Time.Format("2006-01-02 15:04")
			} else {
				item.LastRenewalDate = "-"
			}

			if item.Debt <= 0 {
				item.DebtStatus = "none"
				item.DebtStatusText = "لا يوجد دين"
				item.DebtStatusBadgeClass = "bg-success-subtle text-success border border-success-subtle"
			} else if item.Debt >= 500 || item.DebtAgeDays > 30 {
				item.DebtStatus = "critical"
				item.DebtStatusText = "متعثر"
				item.DebtStatusBadgeClass = "bg-danger text-white"
			} else if item.DebtAgeDays > 7 {
				item.DebtStatus = "overdue"
				item.DebtStatusText = "متأخر"
				item.DebtStatusBadgeClass = "bg-warning-subtle text-warning-emphasis border border-warning-subtle"
			} else {
				item.DebtStatus = "minor"
				item.DebtStatusText = "دين بسيط"
				item.DebtStatusBadgeClass = "bg-info-subtle text-info-emphasis border border-info-subtle"
			}

			list = append(list, item)
		}
	}

	return list, total, &summary, nil
}

// -----------------------------------------------------------------------------
// 8. Reseller Balances & Ledger Report (أرصدة وحسابات الموزعين)
// -----------------------------------------------------------------------------

func (d *DB) GetResellerBalancesReport(
	targetResellerID int64,
	limit, offset int,
) ([]models.ResellerBalanceItem, *models.ResellerBalancesSummary, []models.ResellerLedgerItem, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClause := "1=1"
	var args []interface{}
	if targetResellerID > 0 {
		whereClause = "r.id = ?"
		args = append(args, targetResellerID)
	}

	var summary models.ResellerBalancesSummary
	sumQuery := fmt.Sprintf(`SELECT 
		COALESCE(SUM(balance), 0),
		COALESCE(SUM(debt), 0),
		COALESCE(SUM(balance + credit_limit - debt), 0),
		COALESCE(SUM(credit_limit), 0),
		COALESCE(SUM(CASE WHEN is_active = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN is_active = 0 THEN 1 ELSE 0 END), 0)
	FROM resellers r WHERE %s`, whereClause)
	_ = d.QueryRow(sumQuery, args...).Scan(
		&summary.TotalResellerBalances,
		&summary.TotalResellerDebt,
		&summary.TotalAvailableCap,
		&summary.TotalCreditLimitSum,
		&summary.ActiveResellersCount,
		&summary.InactiveResellersCount,
	)
	summary.TotalBalancesSum = summary.TotalResellerBalances
	summary.TotalDebtsSum = summary.TotalResellerDebt
	summary.NetOperationalCapacity = summary.TotalAvailableCap

	_ = d.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM financial_ledger 
		WHERE account_type = 'reseller' AND operation_type LIKE '%deposit%'`).Scan(&summary.TotalDepositsSum)

	resQuery := fmt.Sprintf(`SELECT 
		r.id,
		r.name,
		r.username,
		COALESCE(r.phone, '-'),
		COALESCE(r.balance, 0.00),
		COALESCE(r.credit_limit, 0.00),
		COALESCE(r.debt, 0.00),
		COALESCE(r.commission_percent, 0.00),
		COALESCE(r.is_active, 1),
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id) as sub_count,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE reseller_id = r.id AND operation_type LIKE '%%dep%%'), 0.00) as deposits,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE reseller_id = r.id AND operation_type LIKE '%%renew%%'), 0.00) as renewals,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE reseller_id = r.id AND operation_type LIKE '%%quota%%'), 0.00) as quotas,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE reseller_id = r.id AND operation_type LIKE '%%card%%'), 0.00) as cards,
		COALESCE((SELECT amount FROM financial_ledger WHERE reseller_id = r.id ORDER BY id DESC LIMIT 1), 0.00) as last_amount,
		COALESCE((SELECT created_at FROM financial_ledger WHERE reseller_id = r.id ORDER BY id DESC LIMIT 1), r.created_at) as last_tx
	FROM resellers r
	WHERE %s
	ORDER BY r.balance DESC, r.name ASC`, whereClause)

	resRows, err := d.Query(resQuery, args...)
	if err != nil {
		return nil, &summary, nil, 0, err
	}
	defer resRows.Close()

	var resellers []models.ResellerBalanceItem
	for resRows.Next() {
		var item models.ResellerBalanceItem
		var isActiveInt int
		var lastTxDate sql.NullTime

		if err := resRows.Scan(
			&item.ResellerID,
			&item.Name,
			&item.Username,
			&item.Phone,
			&item.Balance,
			&item.CreditLimit,
			&item.Debt,
			&item.CommissionPercent,
			&isActiveInt,
			&item.SubscribersCount,
			&item.TotalDeposits,
			&item.TotalRenewalsCost,
			&item.TotalQuotaCost,
			&item.TotalCardsCost,
			&item.LastTxAmount,
			&lastTxDate,
		); err == nil {
			item.IsActive = (isActiveInt == 1)
			item.Role = "موزع معتمد"
			item.AvailableCapacity = item.Balance + item.CreditLimit - item.Debt
			item.TotalDeductions = item.TotalRenewalsCost + item.TotalQuotaCost + item.TotalCardsCost
			if lastTxDate.Valid {
				item.LastTxDate = lastTxDate.Time.Format("2006-01-02 15:04")
			} else {
				item.LastTxDate = "-"
			}
			resellers = append(resellers, item)
		}
	}

	ledgerWhere := "fl.account_type = 'reseller'"
	var ledgerArgs []interface{}
	if targetResellerID > 0 {
		ledgerWhere += " AND fl.reseller_id = ?"
		ledgerArgs = append(ledgerArgs, targetResellerID)
	}

	var totalLedgerCount int
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM financial_ledger fl WHERE %s", ledgerWhere), ledgerArgs...).Scan(&totalLedgerCount)

	ledgerQuery := fmt.Sprintf(`SELECT 
		fl.id,
		fl.reference_id,
		COALESCE(fl.idempotency_key, '-'),
		fl.reseller_id,
		COALESCE(res.name, 'المركز الرئيسي'),
		fl.operation_type,
		fl.amount,
		fl.balance_before,
		fl.balance_after,
		fl.debt_before,
		fl.debt_after,
		COALESCE(fl.subscriber_username, '-'),
		fl.description,
		fl.created_by,
		fl.created_at
	FROM financial_ledger fl
	LEFT JOIN resellers res ON fl.reseller_id = res.id
	WHERE %s
	ORDER BY fl.id DESC
	LIMIT %d OFFSET %d`, ledgerWhere, limit, offset)

	ledgerRows, err := d.Query(ledgerQuery, ledgerArgs...)
	if err != nil {
		return resellers, &summary, nil, totalLedgerCount, err
	}
	defer ledgerRows.Close()

	var ledgerItems []models.ResellerLedgerItem
	for ledgerRows.Next() {
		var l models.ResellerLedgerItem
		if err := ledgerRows.Scan(
			&l.ID,
			&l.ReferenceID,
			&l.IdempotencyKey,
			&l.ResellerID,
			&l.ResellerName,
			&l.OperationType,
			&l.Amount,
			&l.BalanceBefore,
			&l.BalanceAfter,
			&l.DebtBefore,
			&l.DebtAfter,
			&l.SubscriberName,
			&l.Description,
			&l.CreatedBy,
			&l.CreatedAt,
		); err == nil {
			l.OperationTypeText = TranslateLedgerOperationType(l.OperationType)
			l.CreatedAtFormatted = l.CreatedAt.Format("2006-01-02 15:04:05")
			ledgerItems = append(ledgerItems, l)
		}
	}

	return resellers, &summary, ledgerItems, totalLedgerCount, nil
}

// -----------------------------------------------------------------------------
// 9. Revenue & Profitability (تقرير الإيرادات والربحية)
// -----------------------------------------------------------------------------

func (d *DB) GetRevenueProfitReport(resellerID int64) ([]models.RevenueProfitItem, []models.RevenueProfitItem, *models.RevenueProfitSummary, error) {
	summary := &models.RevenueProfitSummary{}

	todayStart := time.Now().Format("2006-01-02 00:00:00")
	monthStart := time.Now().Format("2006-01-01 00:00:00")
	yearStart := time.Now().Format("2006-01-01 00:00:00")

	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM billing_transactions WHERE created_at >= ?", todayStart).Scan(&summary.TodayRevenue)
	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM billing_transactions WHERE created_at >= ?", monthStart).Scan(&summary.MonthRevenue)
	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM billing_transactions WHERE created_at >= ?", yearStart).Scan(&summary.YearRevenue)
	_ = d.QueryRow("SELECT COALESCE(SUM(debt), 0) FROM subscribers WHERE debt > 0").Scan(&summary.TotalUncollectedDebt)

	// Profit by Profile
	pQuery := `SELECT 
		p.display_name,
		COUNT(b.id) as sales_count,
		COALESCE(SUM(b.amount), 0) as gross_rev,
		COALESCE(SUM(p.cost_price), 0) as est_cost
	FROM billing_transactions b
	JOIN subscribers s ON b.username = s.username COLLATE utf8mb4_unicode_ci
	JOIN profiles p ON s.profile_name = p.name
	GROUP BY p.name
	ORDER BY gross_rev DESC`

	pRows, err := d.Query(pQuery)
	var byProfile []models.RevenueProfitItem
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var item models.RevenueProfitItem
			if err := pRows.Scan(&item.ItemName, &item.SalesCount, &item.GrossRevenue, &item.CostPrice); err == nil {
				item.Category = "باقة اشتراك"
				item.NetProfit = item.GrossRevenue - item.CostPrice
				if item.GrossRevenue > 0 {
					item.ProfitMargin = (item.NetProfit / item.GrossRevenue) * 100.0
				}
				summary.TotalEstimatedCost += item.CostPrice
				byProfile = append(byProfile, item)
			}
		}
	}

	summary.TotalNetProfit = summary.YearRevenue - summary.TotalEstimatedCost
	if summary.YearRevenue > 0 {
		summary.ProfitMarginAvg = (summary.TotalNetProfit / summary.YearRevenue) * 100.0
	}

	// Revenue by Reseller
	rQuery := `SELECT 
		COALESCE(r.name, 'المركز الرئيسي'),
		COUNT(b.id),
		COALESCE(SUM(b.amount), 0)
	FROM billing_transactions b
	LEFT JOIN subscribers s ON b.username = s.username COLLATE utf8mb4_unicode_ci
	LEFT JOIN resellers r ON s.reseller_id = r.id
	GROUP BY s.reseller_id
	ORDER BY SUM(b.amount) DESC`

	rRows, err := d.Query(rQuery)
	var byReseller []models.RevenueProfitItem
	if err == nil {
		defer rRows.Close()
		for rRows.Next() {
			var item models.RevenueProfitItem
			if err := rRows.Scan(&item.ItemName, &item.SalesCount, &item.GrossRevenue); err == nil {
				item.Category = "موزع"
				byReseller = append(byReseller, item)
			}
		}
	}

	return byProfile, byReseller, summary, nil
}

// -----------------------------------------------------------------------------
// 10. RADIUS & NAS Health (حالة الراوترات و RADIUS)
// -----------------------------------------------------------------------------

func (d *DB) GetNASHealthReport() ([]models.NASHealthItem, *models.NASHealthSummary, error) {
	summary := &models.NASHealthSummary{}

	query := `SELECT 
		n.id,
		n.nasname,
		COALESCE(n.shortname, n.nasname),
		COALESCE(n.type, 'mikrotik'),
		COALESCE((SELECT COUNT(*) FROM radacct WHERE nasipaddress = n.nasname AND acctstoptime IS NULL), 0) as online_sessions,
		COALESCE((SELECT SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED) + 
			(CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)) 
			FROM radacct WHERE nasipaddress = n.nasname) / 1073741824, 0) as tot_gb,
		(SELECT MAX(COALESCE(acctupdatetime, acctstarttime)) FROM radacct WHERE nasipaddress = n.nasname) as last_acct,
		(SELECT MAX(authdate) FROM radpostauth) as last_auth
	FROM nas n
	ORDER BY n.id ASC`

	rows, err := d.Query(query)
	if err != nil {
		return nil, summary, err
	}
	defer rows.Close()

	var list []models.NASHealthItem
	for rows.Next() {
		var item models.NASHealthItem
		var lastAcct sql.NullTime
		var lastAuth sql.NullTime

		if err := rows.Scan(
			&item.ID,
			&item.NASName,
			&item.ShortName,
			&item.Type,
			&item.ActiveSessions,
			&item.TotalTrafficGB,
			&lastAcct,
			&lastAuth,
		); err == nil {
			summary.TotalRouters++
			summary.TotalActiveSessions += item.ActiveSessions

			if lastAcct.Valid {
				item.LastAcctUpdate = lastAcct.Time.Format("2006-01-02 15:04")
				if time.Since(lastAcct.Time) < 15*time.Minute {
					item.HealthStatus = "online"
					item.HealthText = "يعمل بكفاءة"
					item.HealthBadgeClass = "bg-success text-white"
					summary.OnlineRouters++
				} else if time.Since(lastAcct.Time) < 2*time.Hour {
					item.HealthStatus = "idle"
					item.HealthText = "خمول / هادئ"
					item.HealthBadgeClass = "bg-warning-subtle text-warning-emphasis border border-warning-subtle"
					summary.IdleRouters++
				} else {
					item.HealthStatus = "offline"
					item.HealthText = "لا يرسل تحديثات"
					item.HealthBadgeClass = "bg-danger text-white"
					summary.OfflineRouters++
				}
			} else {
				item.LastAcctUpdate = "لا توجد حزم محاسبة"
				item.HealthStatus = "offline"
				item.HealthText = "غير متصل"
				item.HealthBadgeClass = "bg-secondary text-white"
				summary.OfflineRouters++
			}

			if lastAuth.Valid {
				item.LastAuthAttempt = lastAuth.Time.Format("2006-01-02 15:04")
			} else {
				item.LastAuthAttempt = "-"
			}

			list = append(list, item)
		}
	}

	return list, summary, nil
}

// -----------------------------------------------------------------------------
// 11. Capacity & Peak Load (السعة والضغط على الشبكة)
// -----------------------------------------------------------------------------

func (d *DB) GetCapacityPeakReport(resellerID int64) ([]models.CapacityHourlyItem, *models.CapacitySummary, error) {
	summary := &models.CapacitySummary{
		BandwidthAdvisory: "السعة الحالية للشبكة مستقرة وتستوعب أحمال الذروة",
	}

	query := `SELECT 
		HOUR(acctstarttime) as hr,
		COUNT(*) as sessions,
		COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)) / 1073741824, 0) as up_gb,
		COALESCE(SUM((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)) / 1073741824, 0) as down_gb
	FROM radacct
	WHERE acctstarttime >= NOW() - INTERVAL 7 DAY
	GROUP BY hr
	ORDER BY hr ASC`

	rows, err := d.Query(query)
	if err != nil {
		return nil, summary, err
	}
	defer rows.Close()

	var list []models.CapacityHourlyItem
	var maxTraffic float64
	var totalTraffic float64

	for rows.Next() {
		var item models.CapacityHourlyItem
		if err := rows.Scan(&item.Hour, &item.SessionsStarted, &item.UploadGB, &item.DownloadGB); err == nil {
			item.HourDisplay = fmt.Sprintf("%02d:00", item.Hour)
			item.TotalTrafficGB = item.UploadGB + item.DownloadGB
			totalTraffic += item.TotalTrafficGB

			if item.TotalTrafficGB > maxTraffic {
				maxTraffic = item.TotalTrafficGB
				summary.PeakHourText = fmt.Sprintf("الساعة %02d:00 (ذروة الضغط)", item.Hour)
				summary.PeakTrafficHourGB = maxTraffic
				summary.PeakSessionsHour = item.SessionsStarted
			}
			list = append(list, item)
		}
	}

	if len(list) > 0 {
		summary.AvgHourlyTrafficGB = totalTraffic / float64(len(list))
		for i := range list {
			if maxTraffic > 0 {
				list[i].PercentOfPeak = (list[i].TotalTrafficGB / maxTraffic) * 100.0
			}
		}
	}

	return list, summary, nil
}

// -----------------------------------------------------------------------------
// 12. Audit Trail & Security (الأمان والمراجعة)
// -----------------------------------------------------------------------------

func (d *DB) GetAuditReport(
	search, actionFilter, actorFilter, fromDate, toDate string,
	limit, offset int,
) ([]models.AuditReportItem, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"1=1"}
	var args []interface{}

	search = strings.TrimSpace(search)
	if search != "" {
		whereClauses = append(whereClauses, "(reference_id LIKE ? OR actor_username LIKE ? OR target_id LIKE ? OR details LIKE ?)")
		patt := "%" + search + "%"
		args = append(args, patt, patt, patt, patt)
	}

	if actionFilter != "" {
		whereClauses = append(whereClauses, "action = ?")
		args = append(args, actionFilter)
	}

	if actorFilter != "" {
		whereClauses = append(whereClauses, "actor_username = ?")
		args = append(args, actorFilter)
	}

	if fromDate != "" {
		whereClauses = append(whereClauses, "created_at >= ?")
		if len(fromDate) == 10 {
			fromDate += " 00:00:00"
		}
		args = append(args, fromDate)
	}
	if toDate != "" {
		whereClauses = append(whereClauses, "created_at <= ?")
		if len(toDate) == 10 {
			toDate += " 23:59:59"
		}
		args = append(args, toDate)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	var total int
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM audit_logs WHERE %s", whereSQL), args...).Scan(&total)

	query := fmt.Sprintf(`SELECT 
		id,
		reference_id,
		actor_username,
		actor_role,
		action,
		target_type,
		target_id,
		COALESCE(details, ''),
		ip_address,
		created_at
	FROM audit_logs
	WHERE %s
	ORDER BY id DESC
	LIMIT %d OFFSET %d`, whereSQL, limit, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []models.AuditReportItem
	for rows.Next() {
		var item models.AuditReportItem
		if err := rows.Scan(
			&item.ID,
			&item.ReferenceID,
			&item.ActorUsername,
			&item.ActorRole,
			&item.Action,
			&item.TargetType,
			&item.TargetID,
			&item.Details,
			&item.IPAddress,
			&item.CreatedAt,
		); err == nil {
			item.CreatedAtFormatted = item.CreatedAt.Format("2006-01-02 15:04:05")
			item.ActionText = item.Action
			list = append(list, item)
		}
	}

	return list, total, nil
}

// -----------------------------------------------------------------------------
// 5. Subscriber Balances & Financial Ledger (أرصدة العملاء وكشف الحساب المالي)
// -----------------------------------------------------------------------------

func (d *DB) GetSubscriberBalancesReport(
	search, balanceState string,
	resellerID int64,
	limit, offset int,
) ([]models.SubscriberBalanceItem, int, *models.SubscriberBalancesSummary, []models.SubscriberLedgerItem, int, error) {
	var summary models.SubscriberBalancesSummary

	whereClauses := []string{"1=1"}
	var args []interface{}

	if resellerID > 0 {
		whereClauses = append(whereClauses, "s.reseller_id = ?")
		args = append(args, resellerID)
	}

	if search != "" {
		whereClauses = append(whereClauses, "(s.username LIKE ? OR s.full_name LIKE ? OR s.phone LIKE ? OR s.contract_no LIKE ?)")
		patt := "%" + search + "%"
		args = append(args, patt, patt, patt, patt)
	}

	switch balanceState {
	case "positive":
		whereClauses = append(whereClauses, "s.balance > 0")
	case "debtor":
		whereClauses = append(whereClauses, "s.debt > 0")
	case "zero":
		whereClauses = append(whereClauses, "s.balance = 0 AND s.debt = 0")
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. Balances Summary
	summaryWhere := "1=1"
	var sumArgs []interface{}
	if resellerID > 0 {
		summaryWhere = "reseller_id = ?"
		sumArgs = append(sumArgs, resellerID)
	}

	_ = d.QueryRow(fmt.Sprintf(`SELECT 
		COALESCE(SUM(balance), 0),
		COALESCE(SUM(debt), 0),
		COALESCE(SUM(balance - debt), 0),
		COUNT(CASE WHEN balance > 0 THEN 1 END),
		COUNT(CASE WHEN debt > 0 THEN 1 END)
	FROM subscribers WHERE %s`, summaryWhere), sumArgs...).Scan(
		&summary.TotalBalancesSum,
		&summary.TotalDebtsSum,
		&summary.NetLiquidity,
		&summary.PositiveAccounts,
		&summary.DebtorAccounts,
	)

	// 2. Count Total Matching Subscribers
	var total int
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM subscribers s WHERE %s", whereSQL), args...).Scan(&total)

	// 3. Subscriber Balance Items
	subQuery := fmt.Sprintf(`SELECT 
		s.id,
		s.username,
		s.full_name,
		s.contract_no,
		COALESCE(s.phone, '-'),
		s.reseller_id,
		COALESCE(res.name, 'المركز الرئيسي'),
		COALESCE(s.profile_name, '-'),
		COALESCE(s.balance, 0.00),
		COALESCE(s.debt, 0.00),
		COALESCE(s.status, 'active'),
		COALESCE(s.loan_active, 0),
		COALESCE((SELECT amount FROM financial_ledger WHERE subscriber_username = s.username COLLATE utf8mb4_unicode_ci AND operation_type LIKE '%%dep%%' ORDER BY id DESC LIMIT 1), 0.00) as last_dep,
		COALESCE((SELECT created_at FROM financial_ledger WHERE subscriber_username = s.username COLLATE utf8mb4_unicode_ci AND operation_type LIKE '%%dep%%' ORDER BY id DESC LIMIT 1), s.created_at) as last_dep_date,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE subscriber_username = s.username COLLATE utf8mb4_unicode_ci AND operation_type LIKE '%%dep%%'), 0.00) as tot_dep,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE subscriber_username = s.username COLLATE utf8mb4_unicode_ci AND operation_type NOT LIKE '%%dep%%'), 0.00) as tot_purchases,
		COALESCE((SELECT operation_type FROM financial_ledger WHERE subscriber_username = s.username COLLATE utf8mb4_unicode_ci ORDER BY id DESC LIMIT 1), 'افتتاح حساب') as last_op,
		COALESCE((SELECT created_at FROM financial_ledger WHERE subscriber_username = s.username COLLATE utf8mb4_unicode_ci ORDER BY id DESC LIMIT 1), s.created_at) as last_op_date
	FROM subscribers s
	LEFT JOIN resellers res ON s.reseller_id = res.id
	WHERE %s
	ORDER BY s.balance DESC, s.debt DESC, s.id DESC
	LIMIT %d OFFSET %d`, whereSQL, limit, offset)

	subRows, err := d.Query(subQuery, args...)
	if err != nil {
		return nil, 0, &summary, nil, 0, err
	}
	defer subRows.Close()

	var subscribers []models.SubscriberBalanceItem
	for subRows.Next() {
		var item models.SubscriberBalanceItem
		var loanActiveInt int
		var lastDepDate sql.NullTime
		var lastOpDate sql.NullTime

		if err := subRows.Scan(
			&item.SubscriberID,
			&item.Username,
			&item.FullName,
			&item.ContractNo,
			&item.Phone,
			&item.ResellerID,
			&item.ResellerName,
			&item.ProfileName,
			&item.Balance,
			&item.Debt,
			&item.Status,
			&loanActiveInt,
			&item.LastDepositAmount,
			&lastDepDate,
			&item.TotalDeposits,
			&item.TotalPurchases,
			&item.LastTxType,
			&lastOpDate,
		); err == nil {
			item.LoanActive = (loanActiveInt == 1)
			item.NetBalance = item.Balance - item.Debt
			item.LastTxType = TranslateLedgerOperationType(item.LastTxType)
			if lastDepDate.Valid {
				item.LastDepositDate = lastDepDate.Time.Format("2006-01-02 15:04")
			} else {
				item.LastDepositDate = "-"
			}
			if lastOpDate.Valid {
				item.LastTxDate = lastOpDate.Time.Format("2006-01-02 15:04")
			} else {
				item.LastTxDate = "-"
			}
			subscribers = append(subscribers, item)
		}
	}

	// 4. Subscriber Ledger Statement
	ledgerWhere := "fl.account_type = 'subscriber'"
	var ledgerArgs []interface{}
	if resellerID > 0 {
		ledgerWhere += " AND fl.reseller_id = ?"
		ledgerArgs = append(ledgerArgs, resellerID)
	}
	if search != "" {
		ledgerWhere += " AND (fl.subscriber_username LIKE ? OR fl.reference_id LIKE ?)"
		patt := "%" + search + "%"
		ledgerArgs = append(ledgerArgs, patt, patt)
	}

	var totalLedgerCount int
	_ = d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM financial_ledger fl WHERE %s", ledgerWhere), ledgerArgs...).Scan(&totalLedgerCount)

	ledgerQuery := fmt.Sprintf(`SELECT 
		fl.id,
		fl.reference_id,
		COALESCE(fl.subscriber_username, '-'),
		COALESCE(s.full_name, fl.subscriber_username),
		fl.operation_type,
		fl.amount,
		fl.balance_before,
		fl.balance_after,
		fl.debt_before,
		fl.debt_after,
		COALESCE(bt.payment_method, 'نظام داخلي'),
		fl.description,
		fl.created_by,
		fl.created_at
	FROM financial_ledger fl
	LEFT JOIN subscribers s ON fl.subscriber_username = s.username COLLATE utf8mb4_unicode_ci
	LEFT JOIN billing_transactions bt ON fl.reference_id = bt.notes COLLATE utf8mb4_unicode_ci
	WHERE %s
	ORDER BY fl.id DESC
	LIMIT %d OFFSET %d`, ledgerWhere, limit, offset)

	ledgerRows, err := d.Query(ledgerQuery, ledgerArgs...)
	if err != nil {
		return subscribers, total, &summary, nil, totalLedgerCount, err
	}
	defer ledgerRows.Close()

	var ledgerItems []models.SubscriberLedgerItem
	for ledgerRows.Next() {
		var l models.SubscriberLedgerItem
		if err := ledgerRows.Scan(
			&l.ID,
			&l.ReferenceID,
			&l.Username,
			&l.FullName,
			&l.OperationType,
			&l.Amount,
			&l.BalanceBefore,
			&l.BalanceAfter,
			&l.DebtBefore,
			&l.DebtAfter,
			&l.PaymentMethod,
			&l.Description,
			&l.CreatedBy,
			&l.CreatedAt,
		); err == nil {
			l.OperationTypeText = TranslateLedgerOperationType(l.OperationType)
			l.CreatedAtFormatted = l.CreatedAt.Format("2006-01-02 15:04:05")
			ledgerItems = append(ledgerItems, l)
		}
	}

	return subscribers, total, &summary, ledgerItems, totalLedgerCount, nil
}
