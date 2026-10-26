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
// 1. Subscriber Login & Session Reports (تقرير حالات تسجيل الدخول والجلسات)
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
// 2. Top Consumers Report (أعلى العملاء استهلاكاً)
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
			end := fmt.Sprintf("%04d-%02d-01 00:00:00", year, month)
			t, _ := time.Parse("2006-01-02 15:04:05", start)
			end = t.AddDate(0, 1, 0).Format("2006-01-02 15:04:05")
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
// 3. Subscriber Debts Report (تقرير ديون العملاء)
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

	// Debt status classification
	switch debtStatus {
	case "critical":
		whereClauses = append(whereClauses, "(s.debt >= 500 OR DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)) > 30)")
	case "overdue":
		whereClauses = append(whereClauses, "(s.debt < 500 AND DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)) BETWEEN 8 AND 30)")
	case "minor":
		whereClauses = append(whereClauses, "(s.debt <= 100 AND DATEDIFF(NOW(), COALESCE(s.last_renewed_at, s.created_at)) <= 7)")
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// Summary KPIs
	var summary models.SubscriberDebtsSummary
	summaryQuery := fmt.Sprintf(`SELECT 
		COUNT(*), 
		COALESCE(SUM(s.debt), 0),
		COALESCE(AVG(s.debt), 0)
	FROM subscribers s
	WHERE %s`, whereSQL)
	_ = d.QueryRow(summaryQuery, args...).Scan(
		&summary.TotalDebtorsCount,
		&summary.TotalDebtAmount,
		&summary.AverageDebt,
	)

	// Total count for pagination
	total := int(summary.TotalDebtorsCount)

	// Query last settled amount today
	todayStart := time.Now().Format("2006-01-02 00:00:00")
	var settledToday float64
	_ = d.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM financial_ledger 
		WHERE operation_type IN ('settle_debt', 'debt_settle') AND created_at >= ?`, todayStart).Scan(&settledToday)
	summary.TotalSettledToday = settledToday

	// Data Query
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
		s.last_renewed_at
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
		); err == nil {
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

			// Determine status badge
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
// 4. Reseller Balances & Financial Ledger Report (تقرير أرصدة الموزعين والمديرين)
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

	// 1. Reseller Summary Cards
	var summary models.ResellerBalancesSummary
	sumQuery := fmt.Sprintf(`SELECT 
		COALESCE(SUM(balance), 0),
		COALESCE(SUM(debt), 0),
		COALESCE(SUM(balance + credit_limit - debt), 0),
		COALESCE(SUM(CASE WHEN is_active = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN is_active = 0 THEN 1 ELSE 0 END), 0)
	FROM resellers r WHERE %s`, whereClause)
	_ = d.QueryRow(sumQuery, args...).Scan(
		&summary.TotalResellerBalances,
		&summary.TotalResellerDebt,
		&summary.TotalAvailableCap,
		&summary.ActiveResellersCount,
		&summary.InactiveResellersCount,
	)

	// Sum total deposits
	_ = d.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM financial_ledger 
		WHERE account_type = 'reseller' AND operation_type LIKE '%deposit%'`).Scan(&summary.TotalDepositsSum)

	// 2. Reseller Balance Items
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

	// 3. Reseller Ledger Statement
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
// 5. Subscriber Balances & Ledger Report (تقرير أرصدة العملاء والعمليات المالية)
// -----------------------------------------------------------------------------

func (d *DB) GetSubscriberBalancesReport(
	search, balanceState string, resellerID int64,
	limit, offset int,
) ([]models.SubscriberBalanceItem, int, *models.SubscriberBalancesSummary, []models.SubscriberLedgerItem, int, error) {
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
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern, pattern, pattern)
	}

	switch balanceState {
	case "positive":
		whereClauses = append(whereClauses, "s.balance > 0")
	case "zero":
		whereClauses = append(whereClauses, "s.balance = 0 AND s.debt = 0")
	case "debt":
		whereClauses = append(whereClauses, "s.debt > 0")
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. KPI Summary
	var summary models.SubscriberBalancesSummary
	sumQuery := fmt.Sprintf(`SELECT 
		COALESCE(SUM(s.balance), 0),
		COALESCE(SUM(s.debt), 0),
		COALESCE(SUM(s.balance - s.debt), 0),
		COALESCE(SUM(CASE WHEN s.balance > 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN s.debt > 0 THEN 1 ELSE 0 END), 0)
	FROM subscribers s
	WHERE %s`, whereSQL)
	_ = d.QueryRow(sumQuery, args...).Scan(
		&summary.TotalBalancesSum,
		&summary.TotalDebtsSum,
		&summary.NetLiquidity,
		&summary.PositiveAccounts,
		&summary.DebtorAccounts,
	)

	// 2. Count for pagination
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
		COALESCE((SELECT amount FROM financial_ledger WHERE subscriber_username = s.username AND operation_type LIKE '%%dep%%' ORDER BY id DESC LIMIT 1), 0.00) as last_dep,
		COALESCE((SELECT created_at FROM financial_ledger WHERE subscriber_username = s.username AND operation_type LIKE '%%dep%%' ORDER BY id DESC LIMIT 1), s.created_at) as last_dep_date,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE subscriber_username = s.username AND operation_type LIKE '%%dep%%'), 0.00) as tot_dep,
		COALESCE((SELECT SUM(amount) FROM financial_ledger WHERE subscriber_username = s.username AND operation_type NOT LIKE '%%dep%%'), 0.00) as tot_purchases,
		COALESCE((SELECT operation_type FROM financial_ledger WHERE subscriber_username = s.username ORDER BY id DESC LIMIT 1), 'افتتاح حساب') as last_op,
		COALESCE((SELECT created_at FROM financial_ledger WHERE subscriber_username = s.username ORDER BY id DESC LIMIT 1), s.created_at) as last_op_date
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
	LEFT JOIN billing_transactions bt ON fl.reference_id = bt.notes
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
