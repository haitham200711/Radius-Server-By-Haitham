package database

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	Driver string
}

func Connect(cfg *config.Config) (*DB, error) {
	driver := cfg.DBDriver
	dsn := cfg.DBDSN

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(15)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		// If MySQL not ready or fallback to SQLite
		if driver == "mysql" {
			// Try fallback
			return nil, fmt.Errorf("failed to ping MySQL database (%s): %w", dsn, err)
		}
	}

	d := &DB{DB: db, Driver: driver}
	if err := d.InitSchema(); err != nil {
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	return d, nil
}

func (d *DB) InitSchema() error {
	queries := []string{
		// 1. Admins
		`CREATE TABLE IF NOT EXISTS admins (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(20) DEFAULT 'admin',
			is_active TINYINT(1) DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 2. Resellers (الموزعين)
		`CREATE TABLE IF NOT EXISTS resellers (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			username VARCHAR(64) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			phone VARCHAR(30) DEFAULT '',
			balance DECIMAL(12, 2) DEFAULT 0.00,
			credit_limit DECIMAL(12, 2) DEFAULT 0.00,
			notes TEXT,
			is_active TINYINT(1) DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 3. Profiles (باقات الخدمات)
		`CREATE TABLE IF NOT EXISTS profiles (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(64) UNIQUE NOT NULL,
			display_name VARCHAR(100) NOT NULL,
			rate_limit VARCHAR(64) DEFAULT '',
			pool_name VARCHAR(64) DEFAULT '',
			quota_bytes BIGINT DEFAULT 0,
			uptime_seconds INT DEFAULT 0,
			validity_value INT DEFAULT 30,
			validity_unit VARCHAR(20) DEFAULT 'days',
			price DECIMAL(10, 2) DEFAULT 0.00,
			shared_users INT DEFAULT 1,
			mikrotik_address_list VARCHAR(64) DEFAULT '',
			on_quota_action VARCHAR(20) DEFAULT 'disconnect',
			on_quota_profile VARCHAR(64) DEFAULT '',
			on_quota_pool VARCHAR(64) DEFAULT '',
			on_time_action VARCHAR(20) DEFAULT 'disconnect',
			on_time_profile VARCHAR(64) DEFAULT '',
			on_time_pool VARCHAR(64) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 4. Batches (دفعات الكروت)
		`CREATE TABLE IF NOT EXISTS batches (
			id INT AUTO_INCREMENT PRIMARY KEY,
			batch_code VARCHAR(32) UNIQUE NOT NULL,
			profile_id INT NOT NULL,
			reseller_id INT DEFAULT 0,
			count INT NOT NULL,
			price_per_card DECIMAL(10, 2) DEFAULT 0.00,
			total_price DECIMAL(12, 2) DEFAULT 0.00,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 5. Cards (كروت الشحن)
		`CREATE TABLE IF NOT EXISTS cards (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			batch_id INT NOT NULL,
			username VARCHAR(64) UNIQUE NOT NULL,
			password VARCHAR(64) NOT NULL,
			profile_id INT NOT NULL,
			reseller_id INT DEFAULT 0,
			status VARCHAR(20) DEFAULT 'available',
			price DECIMAL(10, 2) DEFAULT 0.00,
			first_login_at DATETIME NULL,
			expired_at DATETIME NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_cards_reseller (reseller_id),
			INDEX idx_cards_status (status),
			INDEX idx_cards_batch (batch_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 6. Card Templates (تصاميم الطباعة)
		`CREATE TABLE IF NOT EXISTS card_templates (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			background_image VARCHAR(255) DEFAULT '',
			width_mm DECIMAL(6, 2) DEFAULT 85.60,
			height_mm DECIMAL(6, 2) DEFAULT 54.00,
			show_qr TINYINT(1) DEFAULT 1,
			show_barcode TINYINT(1) DEFAULT 0,
			show_price TINYINT(1) DEFAULT 1,
			show_profile TINYINT(1) DEFAULT 1,
			show_expiry TINYINT(1) DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 7. FreeRADIUS Standard Tables
		`CREATE TABLE IF NOT EXISTS nas (
			id INT AUTO_INCREMENT PRIMARY KEY,
			nasname VARCHAR(128) NOT NULL,
			shortname VARCHAR(32),
			type VARCHAR(30) DEFAULT 'other',
			ports INT,
			secret VARCHAR(60) NOT NULL,
			server VARCHAR(64),
			community VARCHAR(50),
			description VARCHAR(200) DEFAULT 'RADIUS Client',
			coa_port INT DEFAULT 3799,
			INDEX idx_nas_nasname (nasname)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		`CREATE TABLE IF NOT EXISTS radcheck (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) NOT NULL DEFAULT '',
			attribute VARCHAR(64) NOT NULL DEFAULT '',
			op CHAR(2) NOT NULL DEFAULT '==',
			value VARCHAR(253) NOT NULL DEFAULT '',
			INDEX idx_radcheck_user (username(32))
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		`CREATE TABLE IF NOT EXISTS radreply (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) NOT NULL DEFAULT '',
			attribute VARCHAR(64) NOT NULL DEFAULT '',
			op CHAR(2) NOT NULL DEFAULT '=',
			value VARCHAR(253) NOT NULL DEFAULT '',
			INDEX idx_radreply_user (username(32))
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		`CREATE TABLE IF NOT EXISTS radusergroup (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) NOT NULL DEFAULT '',
			groupname VARCHAR(64) NOT NULL DEFAULT '',
			priority INT NOT NULL DEFAULT 1,
			INDEX idx_radusergroup_user (username(32))
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		`CREATE TABLE IF NOT EXISTS radgroupcheck (
			id INT AUTO_INCREMENT PRIMARY KEY,
			groupname VARCHAR(64) NOT NULL DEFAULT '',
			attribute VARCHAR(64) NOT NULL DEFAULT '',
			op CHAR(2) NOT NULL DEFAULT '==',
			value VARCHAR(253) NOT NULL DEFAULT '',
			INDEX idx_radgroupcheck_group (groupname(32))
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		`CREATE TABLE IF NOT EXISTS radgroupreply (
			id INT AUTO_INCREMENT PRIMARY KEY,
			groupname VARCHAR(64) NOT NULL DEFAULT '',
			attribute VARCHAR(64) NOT NULL DEFAULT '',
			op CHAR(2) NOT NULL DEFAULT '=',
			value VARCHAR(253) NOT NULL DEFAULT '',
			INDEX idx_radgroupreply_group (groupname(32))
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		`CREATE TABLE IF NOT EXISTS radacct (
			radacctid BIGINT AUTO_INCREMENT PRIMARY KEY,
			acctsessionid VARCHAR(64) NOT NULL DEFAULT '',
			acctuniqueid VARCHAR(32) NOT NULL DEFAULT '',
			username VARCHAR(64) NOT NULL DEFAULT '',
			realm VARCHAR(64) DEFAULT '',
			nasipaddress VARCHAR(15) NOT NULL DEFAULT '',
			nasportid VARCHAR(32) DEFAULT NULL,
			nasporttype VARCHAR(32) DEFAULT NULL,
			acctstarttime DATETIME NULL DEFAULT NULL,
			acctupdatetime DATETIME NULL DEFAULT NULL,
			acctstoptime DATETIME NULL DEFAULT NULL,
			acctinterval INT DEFAULT NULL,
			acctsessiontime INT UNSIGNED DEFAULT NULL,
			acctauthentic VARCHAR(32) DEFAULT NULL,
			connectinfo_start VARCHAR(50) DEFAULT NULL,
			connectinfo_stop VARCHAR(50) DEFAULT NULL,
			acctinputoctets BIGINT DEFAULT NULL,
			acctoutputoctets BIGINT DEFAULT NULL,
			calledstationid VARCHAR(50) NOT NULL DEFAULT '',
			callingstationid VARCHAR(50) NOT NULL DEFAULT '',
			acctterminatecause VARCHAR(32) NOT NULL DEFAULT '',
			servicetype VARCHAR(32) DEFAULT NULL,
			framedprotocol VARCHAR(32) DEFAULT NULL,
			framedipaddress VARCHAR(15) NOT NULL DEFAULT '',
			INDEX idx_radacct_active (acctstoptime, username),
			INDEX idx_radacct_session (acctsessionid),
			INDEX idx_radacct_user (username)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
		// 8. Subscribers CRM Table (العملاء والعقود)
		`CREATE TABLE IF NOT EXISTS subscribers (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) UNIQUE NOT NULL,
			contract_no VARCHAR(32) UNIQUE NOT NULL,
			full_name VARCHAR(150) NOT NULL,
			phone VARCHAR(30) DEFAULT '',
			address TEXT,
			debt DECIMAL(10, 2) DEFAULT 0.00,
			profile_name VARCHAR(64) DEFAULT '',
			static_ip VARCHAR(32) DEFAULT '',
			calling_mac VARCHAR(32) DEFAULT '',
			quota_bytes BIGINT DEFAULT 0,
			expiration_date DATETIME NULL,
			status VARCHAR(20) DEFAULT 'active',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_subs_username (username),
			INDEX idx_subs_contract (contract_no),
			INDEX idx_subs_phone (phone)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 9. SSTP VPN Settings
		`CREATE TABLE IF NOT EXISTS sstp_settings (
			id INT AUTO_INCREMENT PRIMARY KEY,
			enabled TINYINT(1) DEFAULT 1,
			port INT DEFAULT 4444,
			gateway_ip VARCHAR(32) DEFAULT '10.10.10.1',
			ip_pool VARCHAR(64) DEFAULT '10.10.10.2-254',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 10. SSTP VPN Clients / Routers
		`CREATE TABLE IF NOT EXISTS sstp_clients (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			username VARCHAR(64) UNIQUE NOT NULL,
			password VARCHAR(64) NOT NULL,
			assigned_ip VARCHAR(32) DEFAULT '',
			notes VARCHAR(255) DEFAULT '',
			enabled TINYINT(1) DEFAULT 1,
			last_seen DATETIME NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_sstp_user (username)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 11. Billing Transactions & Debt Logs (سجل العمليات المالية والديون)
		`CREATE TABLE IF NOT EXISTS billing_transactions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) NOT NULL,
			contract_no VARCHAR(32) DEFAULT '',
			full_name VARCHAR(150) DEFAULT '',
			type VARCHAR(32) NOT NULL,
			description VARCHAR(255) NOT NULL,
			amount DECIMAL(10, 2) NOT NULL DEFAULT 0.00,
			payment_status VARCHAR(20) NOT NULL DEFAULT 'paid',
			payment_method VARCHAR(50) DEFAULT 'cash',
			notes TEXT,
			created_by VARCHAR(64) DEFAULT 'admin',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_billing_user (username),
			INDEX idx_billing_status (payment_status),
			INDEX idx_billing_type (type),
			INDEX idx_billing_date (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 12. SSTP One-Time Script Tokens (روابط تثبيت المايكروتك لمرة واحدة)
		`CREATE TABLE IF NOT EXISTS sstp_tokens (
			token VARCHAR(64) PRIMARY KEY,
			client_id BIGINT DEFAULT 0,
			username VARCHAR(64) NOT NULL,
			password VARCHAR(64) NOT NULL,
			used TINYINT(1) DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			used_at DATETIME NULL,
			INDEX idx_token_used (token, used)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 13. SSTP Static Routes (توجيه الشبكات الثابت للفروع)
		`CREATE TABLE IF NOT EXISTS sstp_static_routes (
			id INT AUTO_INCREMENT PRIMARY KEY,
			destination_cidr VARCHAR(64) NOT NULL,
			gateway_ip VARCHAR(32) NOT NULL,
			client_id BIGINT DEFAULT 0,
			description VARCHAR(255) DEFAULT '',
			enabled TINYINT(1) DEFAULT 1,
			is_applied TINYINT(1) DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_route_gw (gateway_ip)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 14. WhatsApp Settings (إعدادات وإشعارات الواتساب)
		`CREATE TABLE IF NOT EXISTS whatsapp_settings (
			id INT PRIMARY KEY,
			enabled TINYINT(1) DEFAULT 1,
			admin_phone VARCHAR(32) DEFAULT '',
			api_endpoint VARCHAR(255) DEFAULT '',
			api_token VARCHAR(255) DEFAULT '',
			instance_id VARCHAR(64) DEFAULT '',
			notify_on_new_subscriber TINYINT(1) DEFAULT 1,
			notify_on_expiry TINYINT(1) DEFAULT 1,
			notify_on_payment TINYINT(1) DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 15. WhatsApp Logs (سجل رسائل الواتساب المرسلة)
		`CREATE TABLE IF NOT EXISTS whatsapp_logs (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			phone VARCHAR(32) NOT NULL,
			recipient_name VARCHAR(128) DEFAULT '',
			message_type VARCHAR(32) DEFAULT 'text',
			content TEXT,
			status VARCHAR(32) DEFAULT 'sent',
			error_message TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_wa_phone (phone),
			INDEX idx_wa_status (status),
			INDEX idx_wa_date (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
	}

	for _, q := range queries {
		if _, err := d.Exec(q); err != nil {
			return fmt.Errorf("error running query: %w\n%s", err, q)
		}
	}

	// Schema migrations for profiles and card templates
	migrations := []string{
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS validity_value INT DEFAULT 30;`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS validity_unit VARCHAR(20) DEFAULT 'days';`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS on_quota_action VARCHAR(20) DEFAULT 'disconnect';`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS on_quota_profile VARCHAR(64) DEFAULT '';`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS on_quota_pool VARCHAR(64) DEFAULT '';`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS on_time_action VARCHAR(20) DEFAULT 'disconnect';`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS on_time_profile VARCHAR(64) DEFAULT '';`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS on_time_pool VARCHAR(64) DEFAULT '';`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS is_default TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS background_color VARCHAR(30) DEFAULT '#0d6efd';`,
	}
	for _, m := range migrations {
		_, _ = d.Exec(m)
	}

	// Ensure default WhatsApp settings row exists
	_, _ = d.Exec("INSERT IGNORE INTO whatsapp_settings (id, enabled, admin_phone) VALUES (1, 1, '')")

	// Seed default Admin if not present
	var adminCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM admins").Scan(&adminCount)
	if adminCount == 0 {
		admin := &models.Admin{
			Username: "admin",
			Role:     "admin",
			IsActive: true,
		}
		_ = admin.SetPassword("admin123")
		_, _ = d.Exec("INSERT INTO admins (username, password_hash, role, is_active) VALUES (?, ?, ?, ?)",
			admin.Username, admin.PasswordHash, admin.Role, admin.IsActive)
	}

	// Seed default Profiles
	var profileCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM profiles").Scan(&profileCount)
	if profileCount == 0 {
		_, _ = d.Exec(`INSERT INTO profiles (name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds, price)
			VALUES ('5M_UNLIMITED', '5 Mega Unlimited', '2560k/5120k', 'pppoe-pool', 0, 2592000, 150.00),
			       ('10M_UNLIMITED', '10 Mega Unlimited', '5120k/10240k', 'pppoe-pool', 0, 2592000, 250.00),
			       ('HS_1GB_1D', 'Hotspot 1GB / يومي', '2M/4M', 'hs-pool', 1073741824, 86400, 5.00);`)

		// Also populate FreeRADIUS radgroupreply for these profiles
		_, _ = d.Exec(`INSERT INTO radgroupreply (groupname, attribute, op, value)
			VALUES ('5M_UNLIMITED', 'Mikrotik-Rate-Limit', ':=', '2560k/5120k'),
			       ('10M_UNLIMITED', 'Mikrotik-Rate-Limit', ':=', '5120k/10240k'),
			       ('HS_1GB_1D', 'Mikrotik-Rate-Limit', ':=', '2M/4M');`)
	}

	// Seed default Card Template
	var tmplCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM card_templates").Scan(&tmplCount)
	if tmplCount == 0 {
		_, _ = d.Exec(`INSERT INTO card_templates (name, background_color, width_mm, height_mm, show_qr, show_barcode, show_price, show_profile, show_expiry, is_default)
			VALUES ('القالب الافتراضي الأزرق', '#0d6efd', 85.60, 54.00, 1, 0, 1, 1, 1, 1),
			       ('قالب الكروت المدمجة', '#198754', 70.00, 45.00, 1, 1, 1, 1, 1, 0);`)
	}

	// Seed default SSTP Settings
	var sstpCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM sstp_settings").Scan(&sstpCount)
	if sstpCount == 0 {
		_, _ = d.Exec(`INSERT INTO sstp_settings (enabled, port, gateway_ip, ip_pool)
			VALUES (1, 4444, '10.10.10.1', '10.10.10.2-254')`)
	}

	// Seed default NAS
	var nasCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM nas").Scan(&nasCount)
	if nasCount == 0 {
		_, _ = d.Exec(`INSERT INTO nas (nasname, shortname, type, secret, description, coa_port)
			VALUES ('192.168.2.1', 'MikroTik-Main', 'mikrotik', 'radius123', 'Default MikroTik CCR/RB', 3799),
			       ('127.0.0.1', 'Localhost', 'other', 'radius123', 'Testing NAS', 3799);`)
	}

	return nil
}

// --- Metrics & Dashboard ---

func (d *DB) GetMetrics() (*models.DashboardMetrics, error) {
	m := &models.DashboardMetrics{}

	_ = d.QueryRow("SELECT COUNT(DISTINCT username) FROM radcheck").Scan(&m.TotalSubscribers)
	_ = d.QueryRow("SELECT COUNT(*) FROM cards WHERE status = 'available'").Scan(&m.ActiveCards)
	_ = d.QueryRow("SELECT COUNT(*) FROM radacct WHERE acctstoptime IS NULL").Scan(&m.OnlineSessions)
	_ = d.QueryRow("SELECT COUNT(*) FROM resellers WHERE is_active = 1").Scan(&m.TotalResellers)
	_ = d.QueryRow("SELECT COUNT(*) FROM nas").Scan(&m.TotalRouters)
	_ = d.QueryRow("SELECT COALESCE(SUM(acctinputoctets), 0), COALESCE(SUM(acctoutputoctets), 0) FROM radacct").Scan(&m.TotalDownload, &m.TotalUpload)
	_ = d.QueryRow("SELECT COALESCE(SUM(total_price), 0) FROM batches").Scan(&m.TotalRevenue)

	return m, nil
}

// --- FreeRADIUS Subscriber Management ---

func (d *DB) AddSubscriber(username, password, profileName, staticIP, callingMAC string) error {
	sub := &models.Subscriber{
		Username:    username,
		FullName:    username,
		ProfileName: profileName,
		StaticIP:    staticIP,
		CallingMAC:  callingMAC,
		Status:      "active",
	}
	return d.AddSubscriberCRM(sub, password)
}

func (d *DB) AddSubscriberCRM(sub *models.Subscriber, password string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Generate contract_no if empty
	if sub.ContractNo == "" {
		var count int
		var nextID int64
		_ = tx.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM subscribers").Scan(&nextID)
		for {
			candidate := fmt.Sprintf("CNT-%s-%05d", time.Now().Format("2006"), nextID)
			_ = tx.QueryRow("SELECT COUNT(*) FROM subscribers WHERE contract_no = ?", candidate).Scan(&count)
			if count == 0 {
				sub.ContractNo = candidate
				break
			}
			nextID++
		}
	}
	if sub.Status == "" {
		sub.Status = "active"
	}

	// Auto-fill quota and expiration from profile if not explicitly set
	if sub.ProfileName != "" && (sub.QuotaBytes == 0 || !sub.ExpirationDate.Valid) {
		var profQuota int64
		var valVal int
		var valUnit string
		err := tx.QueryRow("SELECT quota_bytes, validity_value, validity_unit FROM profiles WHERE name = ?", sub.ProfileName).Scan(&profQuota, &valVal, &valUnit)
		if err == nil {
			if sub.QuotaBytes == 0 && profQuota > 0 {
				sub.QuotaBytes = profQuota
			}
			if !sub.ExpirationDate.Valid && valVal > 0 {
				p := &models.Profile{ValidityValue: valVal, ValidityUnit: valUnit}
				sub.ExpirationDate = sql.NullTime{Time: p.CalculateExpiration(time.Now()), Valid: true}
			}
		}
	}

	// 1. Insert or update in subscribers table
	query := `INSERT INTO subscribers (username, contract_no, full_name, phone, address, debt, profile_name, static_ip, calling_mac, quota_bytes, expiration_date, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE contract_no = VALUES(contract_no), full_name = VALUES(full_name), phone = VALUES(phone),
		address = VALUES(address), debt = VALUES(debt), profile_name = VALUES(profile_name), static_ip = VALUES(static_ip),
		calling_mac = VALUES(calling_mac), quota_bytes = VALUES(quota_bytes), expiration_date = VALUES(expiration_date), status = VALUES(status)`

	var expDate interface{}
	if sub.ExpirationDate.Valid {
		expDate = sub.ExpirationDate.Time
	}

	_, err = tx.Exec(query, sub.Username, sub.ContractNo, sub.FullName, sub.Phone, sub.Address, sub.Debt,
		sub.ProfileName, sub.StaticIP, sub.CallingMAC, sub.QuotaBytes, expDate, sub.Status)
	if err != nil {
		return err
	}

	// 2. FreeRADIUS radcheck: Cleartext-Password
	if password != "" {
		_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", sub.Username)
		_, err = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)", sub.Username, password)
		if err != nil {
			return err
		}
	}

	// MAC binding check
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Calling-Station-Id'", sub.Username)
	if sub.CallingMAC != "" {
		_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Calling-Station-Id', '==', ?)", sub.Username, sub.CallingMAC)
	}

	// Expiration in FreeRADIUS (Format: "02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", sub.Username)
	if sub.ExpirationDate.Valid {
		expStr := sub.ExpirationDate.Time.Format("02 Jan 2006 15:04:05")
		_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", sub.Username, expStr)
	}

	// 3. FreeRADIUS radusergroup
	_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", sub.Username)
	if sub.ProfileName != "" {
		_, err = tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", sub.Username, sub.ProfileName)
		if err != nil {
			return err
		}
	}

	// 4. FreeRADIUS radreply
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ?", sub.Username)
	if sub.StaticIP != "" {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Framed-IP-Address', ':=', ?)", sub.Username, sub.StaticIP)
	}
	if sub.QuotaBytes > 0 {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", sub.Username, fmt.Sprintf("%d", sub.QuotaBytes))
	}

	return tx.Commit()
}

func (d *DB) DeleteSubscriber(username string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM subscribers WHERE username = ?", username)
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ?", username)
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ?", username)
	_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", username)
	_, _ = tx.Exec("DELETE FROM cards WHERE username = ?", username)

	return tx.Commit()
}

// --- Batch Generator (Ultra Fast Bulk Cards into FreeRADIUS) ---

func (d *DB) GenerateBatch(count int, prefix string, charLen int, profileID int64, resellerID int64, pricePerCard float64) (string, int, error) {
	if count <= 0 || count > 50000 {
		count = 100
	}
	if charLen < 4 {
		charLen = 6
	}

	var prof models.Profile
	err := d.QueryRow("SELECT id, name, display_name, price FROM profiles WHERE id = ?", profileID).Scan(
		&prof.ID, &prof.Name, &prof.DisplayName, &prof.Price)
	if err != nil {
		return "", 0, fmt.Errorf("profile not found: %w", err)
	}

	if pricePerCard <= 0 {
		pricePerCard = prof.Price
	}

	batchCode := fmt.Sprintf("B%d-%s", time.Now().Unix(), randomLetters(4))
	totalPrice := float64(count) * pricePerCard

	tx, err := d.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	// 1. Insert Batch record
	res, err := tx.Exec(`INSERT INTO batches (batch_code, profile_id, reseller_id, count, price_per_card, total_price, created_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, batchCode, profileID, resellerID, count, pricePerCard, totalPrice)
	if err != nil {
		return "", 0, err
	}
	batchID, _ := res.LastInsertId()

	// 2. Prepare bulk insert statements
	stmtCard, err := tx.Prepare(`INSERT INTO cards (batch_id, username, password, profile_id, reseller_id, status, price, created_at)
		VALUES (?, ?, ?, ?, ?, 'available', ?, CURRENT_TIMESTAMP)`)
	if err != nil {
		return "", 0, err
	}
	defer stmtCard.Close()

	stmtRadCheck, err := tx.Prepare(`INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)`)
	if err != nil {
		return "", 0, err
	}
	defer stmtRadCheck.Close()

	stmtRadGroup, err := tx.Prepare(`INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)`)
	if err != nil {
		return "", 0, err
	}
	defer stmtRadGroup.Close()

	created := 0
	for i := 0; i < count; i++ {
		u := fmt.Sprintf("%s%s", prefix, randomAlphanumeric(charLen))
		p := randomAlphanumeric(6)

		if _, err := stmtCard.Exec(batchID, u, p, profileID, resellerID, pricePerCard); err == nil {
			_, _ = stmtRadCheck.Exec(u, p)
			_, _ = stmtRadGroup.Exec(u, prof.Name)
			created++
		}
	}

	// If assigned to a reseller with balance deduction
	if resellerID > 0 {
		_, _ = tx.Exec("UPDATE resellers SET balance = balance - ? WHERE id = ?", totalPrice, resellerID)
	}

	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	return batchCode, created, nil
}

func randomAlphanumeric(n int) string {
	const chars = "23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func randomLetters(n int) string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// --- Profile Methods ---

func (d *DB) GetProfiles() ([]models.Profile, error) {
	rows, err := d.Query(`SELECT id, name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds,
		validity_value, validity_unit, price, shared_users, mikrotik_address_list,
		on_quota_action, on_quota_profile, on_quota_pool,
		on_time_action, on_time_profile, on_time_pool, created_at
		FROM profiles ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Profile
	for rows.Next() {
		var p models.Profile
		err := rows.Scan(
			&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds,
			&p.ValidityValue, &p.ValidityUnit, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
			&p.OnQuotaAction, &p.OnQuotaProfile, &p.OnQuotaPool,
			&p.OnTimeAction, &p.OnTimeProfile, &p.OnTimePool, &p.CreatedAt,
		)
		if err == nil {
			if p.QuotaBytes > 0 {
				p.QuotaGB = float64(p.QuotaBytes) / (1024 * 1024 * 1024)
			}
			list = append(list, p)
		}
	}
	return list, nil
}

func (d *DB) GetProfile(id int64) (*models.Profile, error) {
	var p models.Profile
	err := d.QueryRow(`SELECT id, name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds,
		validity_value, validity_unit, price, shared_users, mikrotik_address_list,
		on_quota_action, on_quota_profile, on_quota_pool,
		on_time_action, on_time_profile, on_time_pool, created_at
		FROM profiles WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds,
		&p.ValidityValue, &p.ValidityUnit, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
		&p.OnQuotaAction, &p.OnQuotaProfile, &p.OnQuotaPool,
		&p.OnTimeAction, &p.OnTimeProfile, &p.OnTimePool, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if p.QuotaBytes > 0 {
		p.QuotaGB = float64(p.QuotaBytes) / (1024 * 1024 * 1024)
	}
	return &p, nil
}

func (d *DB) GetProfileByName(name string) (*models.Profile, error) {
	var p models.Profile
	err := d.QueryRow(`SELECT id, name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds,
		validity_value, validity_unit, price, shared_users, mikrotik_address_list,
		on_quota_action, on_quota_profile, on_quota_pool,
		on_time_action, on_time_profile, on_time_pool, created_at
		FROM profiles WHERE name = ?`, name).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds,
		&p.ValidityValue, &p.ValidityUnit, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
		&p.OnQuotaAction, &p.OnQuotaProfile, &p.OnQuotaPool,
		&p.OnTimeAction, &p.OnTimeProfile, &p.OnTimePool, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if p.QuotaBytes > 0 {
		p.QuotaGB = float64(p.QuotaBytes) / (1024 * 1024 * 1024)
	}
	return &p, nil
}

func (d *DB) SaveProfile(p *models.Profile) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if p.ValidityValue <= 0 {
		p.ValidityValue = 30
	}
	if p.ValidityUnit == "" {
		p.ValidityUnit = "days"
	}
	if p.OnQuotaAction == "" {
		p.OnQuotaAction = "disconnect"
	}
	if p.OnTimeAction == "" {
		p.OnTimeAction = "disconnect"
	}

	if p.ID == 0 {
		res, err := tx.Exec(`INSERT INTO profiles (name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds,
			validity_value, validity_unit, price, shared_users, mikrotik_address_list,
			on_quota_action, on_quota_profile, on_quota_pool,
			on_time_action, on_time_profile, on_time_pool)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.Name, p.DisplayName, p.RateLimit, p.PoolName, p.QuotaBytes, p.UptimeSeconds,
			p.ValidityValue, p.ValidityUnit, p.Price, p.SharedUsers, p.MikrotikAddressList,
			p.OnQuotaAction, p.OnQuotaProfile, p.OnQuotaPool,
			p.OnTimeAction, p.OnTimeProfile, p.OnTimePool)
		if err != nil {
			return err
		}
		p.ID, _ = res.LastInsertId()
	} else {
		_, err := tx.Exec(`UPDATE profiles SET
			name = ?, display_name = ?, rate_limit = ?, pool_name = ?, quota_bytes = ?, uptime_seconds = ?,
			validity_value = ?, validity_unit = ?, price = ?, shared_users = ?, mikrotik_address_list = ?,
			on_quota_action = ?, on_quota_profile = ?, on_quota_pool = ?,
			on_time_action = ?, on_time_profile = ?, on_time_pool = ?
			WHERE id = ?`,
			p.Name, p.DisplayName, p.RateLimit, p.PoolName, p.QuotaBytes, p.UptimeSeconds,
			p.ValidityValue, p.ValidityUnit, p.Price, p.SharedUsers, p.MikrotikAddressList,
			p.OnQuotaAction, p.OnQuotaProfile, p.OnQuotaPool,
			p.OnTimeAction, p.OnTimeProfile, p.OnTimePool, p.ID)
		if err != nil {
			return err
		}
	}

	// Update FreeRADIUS radgroupreply for Rate-Limit and Pool
	_, _ = tx.Exec("DELETE FROM radgroupreply WHERE groupname = ? AND attribute IN ('Mikrotik-Rate-Limit', 'Framed-Pool')", p.Name)
	if p.RateLimit != "" {
		_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Mikrotik-Rate-Limit', ':=', ?)", p.Name, p.RateLimit)
	}
	if p.PoolName != "" {
		_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Framed-Pool', ':=', ?)", p.Name, p.PoolName)
	}

	return tx.Commit()
}

func (d *DB) DeleteProfile(id int64) error {
	var name string
	_ = d.QueryRow("SELECT name FROM profiles WHERE id = ?", id).Scan(&name)
	if name != "" {
		_, _ = d.Exec("DELETE FROM profiles WHERE id = ?", id)
		_, _ = d.Exec("DELETE FROM radgroupreply WHERE groupname = ?", name)
		_, _ = d.Exec("DELETE FROM radgroupcheck WHERE groupname = ?", name)
	}
	return nil
}

func (d *DB) RenewSubscriber(username, profileName string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// If profileName is empty, get from subscriber record
	if profileName == "" {
		_ = tx.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&profileName)
	}

	var p models.Profile
	err = tx.QueryRow(`SELECT id, name, quota_bytes, validity_value, validity_unit FROM profiles WHERE name = ?`, profileName).Scan(
		&p.ID, &p.Name, &p.QuotaBytes, &p.ValidityValue, &p.ValidityUnit,
	)
	if err != nil {
		return fmt.Errorf("profile %s not found: %w", profileName, err)
	}

	// Calculate new expiration date: from now
	newExp := p.CalculateExpiration(time.Now())

	// Update subscribers table
	_, err = tx.Exec(`UPDATE subscribers SET
		profile_name = ?,
		quota_bytes = ?,
		expiration_date = ?,
		status = 'active'
		WHERE username = ?`, p.Name, p.QuotaBytes, newExp, username)
	if err != nil {
		return err
	}

	// Update radcheck Expiration
	expStr := newExp.Format("02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username)
	_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, expStr)

	// Update radusergroup
	_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", username)
	_, _ = tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", username, p.Name)

	// Update radreply Mikrotik-Total-Limit
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username)
	if p.QuotaBytes > 0 {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", p.QuotaBytes))
	}

	return tx.Commit()
}

// --- Subscriber CRM Edit Methods ---

func (d *DB) GetSubscriber(username string) (*models.Subscriber, error) {
	var s models.Subscriber
	var exp sql.NullTime
	err := d.QueryRow(`SELECT id, username, contract_no, full_name, phone, address, debt,
		profile_name, static_ip, calling_mac, quota_bytes, expiration_date, status, created_at, updated_at
		FROM subscribers WHERE username = ?`, username).Scan(
		&s.ID, &s.Username, &s.ContractNo, &s.FullName, &s.Phone, &s.Address, &s.Debt,
		&s.ProfileName, &s.StaticIP, &s.CallingMAC, &s.QuotaBytes, &exp, &s.Status, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			var pass string
			errCheck := d.QueryRow("SELECT value FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", username).Scan(&pass)
			if errCheck != nil {
				return nil, err
			}
			s = models.Subscriber{
				Username:   username,
				Password:   pass,
				FullName:   username,
				ContractNo: fmt.Sprintf("CNT-%s", username),
				Status:     "active",
			}
			_ = d.QueryRow("SELECT groupname FROM radusergroup WHERE username = ?", username).Scan(&s.ProfileName)
			_ = d.QueryRow("SELECT value FROM radreply WHERE username = ? AND attribute = 'Framed-IP-Address'", username).Scan(&s.StaticIP)
			_ = d.QueryRow("SELECT value FROM radcheck WHERE username = ? AND attribute = 'Calling-Station-Id'", username).Scan(&s.CallingMAC)
			var quotaStr string
			if errQ := d.QueryRow("SELECT value FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username).Scan(&quotaStr); errQ == nil {
				s.QuotaBytes, _ = strconv.ParseInt(quotaStr, 10, 64)
			}
			var expStr string
			if errE := d.QueryRow("SELECT value FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username).Scan(&expStr); errE == nil {
				if t, errP := time.Parse("02 Jan 2006 15:04:05", expStr); errP == nil {
					s.ExpirationDate = sql.NullTime{Time: t, Valid: true}
				}
			}
			return &s, nil
		}
		return nil, err
	}
	s.ExpirationDate = exp
	_ = d.QueryRow("SELECT value FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", username).Scan(&s.Password)
	return &s, nil
}

func (d *DB) UpdateSubscriberCRM(sub *models.Subscriber, password string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var expDate interface{}
	if sub.ExpirationDate.Valid {
		expDate = sub.ExpirationDate.Time
	}

	if sub.ContractNo == "" {
		sub.ContractNo = fmt.Sprintf("CNT-%d-%05d", time.Now().Year(), time.Now().UnixNano()%100000)
	}

	_, err = tx.Exec(`INSERT INTO subscribers (
		username, contract_no, full_name, phone, address, debt, profile_name,
		static_ip, calling_mac, quota_bytes, expiration_date, status, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())
	ON DUPLICATE KEY UPDATE
		full_name = VALUES(full_name),
		phone = VALUES(phone),
		address = VALUES(address),
		debt = VALUES(debt),
		profile_name = VALUES(profile_name),
		static_ip = VALUES(static_ip),
		calling_mac = VALUES(calling_mac),
		quota_bytes = VALUES(quota_bytes),
		expiration_date = VALUES(expiration_date),
		status = VALUES(status),
		updated_at = NOW()`,
		sub.Username, sub.ContractNo, sub.FullName, sub.Phone, sub.Address, sub.Debt, sub.ProfileName,
		sub.StaticIP, sub.CallingMAC, sub.QuotaBytes, expDate, sub.Status)
	if err != nil {
		return err
	}

	// Update Cleartext-Password
	if password != "" {
		_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", sub.Username)
		_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)", sub.Username, password)
	}

	// Calling-Station-Id
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Calling-Station-Id'", sub.Username)
	if sub.CallingMAC != "" {
		_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Calling-Station-Id', '==', ?)", sub.Username, sub.CallingMAC)
	}

	// Expiration
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", sub.Username)
	if sub.ExpirationDate.Valid {
		expStr := sub.ExpirationDate.Time.Format("02 Jan 2006 15:04:05")
		_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", sub.Username, expStr)
	}

	// Profile group
	_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", sub.Username)
	if sub.ProfileName != "" {
		_, _ = tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", sub.Username, sub.ProfileName)
	}

	// Reply items
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute IN ('Framed-IP-Address', 'Mikrotik-Total-Limit')", sub.Username)
	if sub.StaticIP != "" {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Framed-IP-Address', ':=', ?)", sub.Username, sub.StaticIP)
	}
	if sub.QuotaBytes > 0 {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", sub.Username, fmt.Sprintf("%d", sub.QuotaBytes))
	}

	return tx.Commit()
}

// --- Card Template Methods ---

func (d *DB) GetCardTemplates() ([]models.CardTemplate, error) {
	rows, err := d.Query(`SELECT id, name, background_image, background_color, width_mm, height_mm,
		show_qr, show_barcode, show_price, show_profile, show_expiry, is_default, created_at
		FROM card_templates ORDER BY is_default DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.CardTemplate
	for rows.Next() {
		var t models.CardTemplate
		var qr, bar, pri, prof, exp, isDef int
		err := rows.Scan(
			&t.ID, &t.Name, &t.BackgroundImage, &t.BackgroundColor, &t.WidthMM, &t.HeightMM,
			&qr, &bar, &pri, &prof, &exp, &isDef, &t.CreatedAt,
		)
		if err == nil {
			t.ShowQR = (qr == 1)
			t.ShowBarcode = (bar == 1)
			t.ShowPrice = (pri == 1)
			t.ShowProfile = (prof == 1)
			t.ShowExpiry = (exp == 1)
			t.IsDefault = (isDef == 1)
			list = append(list, t)
		}
	}
	return list, nil
}

func (d *DB) GetCardTemplate(id int64) (*models.CardTemplate, error) {
	var t models.CardTemplate
	var qr, bar, pri, prof, exp, isDef int
	err := d.QueryRow(`SELECT id, name, background_image, background_color, width_mm, height_mm,
		show_qr, show_barcode, show_price, show_profile, show_expiry, is_default, created_at
		FROM card_templates WHERE id = ?`, id).Scan(
		&t.ID, &t.Name, &t.BackgroundImage, &t.BackgroundColor, &t.WidthMM, &t.HeightMM,
		&qr, &bar, &pri, &prof, &exp, &isDef, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.ShowQR = (qr == 1)
	t.ShowBarcode = (bar == 1)
	t.ShowPrice = (pri == 1)
	t.ShowProfile = (prof == 1)
	t.ShowExpiry = (exp == 1)
	t.IsDefault = (isDef == 1)
	return &t, nil
}

func (d *DB) SaveCardTemplate(t *models.CardTemplate) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	isDef := 0
	if t.IsDefault {
		isDef = 1
		_, _ = tx.Exec("UPDATE card_templates SET is_default = 0")
	}

	qr := 0
	if t.ShowQR {
		qr = 1
	}
	bar := 0
	if t.ShowBarcode {
		bar = 1
	}
	pri := 0
	if t.ShowPrice {
		pri = 1
	}
	prof := 0
	if t.ShowProfile {
		prof = 1
	}
	exp := 0
	if t.ShowExpiry {
		exp = 1
	}

	if t.ID == 0 {
		_, err = tx.Exec(`INSERT INTO card_templates (name, background_image, background_color, width_mm, height_mm,
			show_qr, show_barcode, show_price, show_profile, show_expiry, is_default)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.Name, t.BackgroundImage, t.BackgroundColor, t.WidthMM, t.HeightMM,
			qr, bar, pri, prof, exp, isDef)
		if err != nil {
			return err
		}
	} else {
		_, err = tx.Exec(`UPDATE card_templates SET
			name = ?, background_image = ?, background_color = ?, width_mm = ?, height_mm = ?,
			show_qr = ?, show_barcode = ?, show_price = ?, show_profile = ?, show_expiry = ?, is_default = ?
			WHERE id = ?`,
			t.Name, t.BackgroundImage, t.BackgroundColor, t.WidthMM, t.HeightMM,
			qr, bar, pri, prof, exp, isDef, t.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) DeleteCardTemplate(id int64) error {
	_, err := d.Exec("DELETE FROM card_templates WHERE id = ?", id)
	return err
}

// --- SSTP Server Methods ---

func (d *DB) GetSSTPSettings() (*models.SSTPSettings, error) {
	var s models.SSTPSettings
	var en int
	err := d.QueryRow("SELECT id, enabled, port, gateway_ip, ip_pool, updated_at FROM sstp_settings ORDER BY id ASC LIMIT 1").Scan(
		&s.ID, &en, &s.Port, &s.GatewayIP, &s.IPPool, &s.UpdatedAt,
	)
	if err != nil {
		return &models.SSTPSettings{
			Enabled:   true,
			Port:      4444,
			GatewayIP: "10.10.10.1",
			IPPool:    "10.10.10.2-254",
		}, nil
	}
	s.Enabled = (en == 1)
	return &s, nil
}

func (d *DB) SaveSSTPSettings(s *models.SSTPSettings) error {
	en := 0
	if s.Enabled {
		en = 1
	}
	var count int
	_ = d.QueryRow("SELECT COUNT(*) FROM sstp_settings").Scan(&count)
	if count == 0 {
		_, err := d.Exec("INSERT INTO sstp_settings (enabled, port, gateway_ip, ip_pool) VALUES (?, ?, ?, ?)",
			en, s.Port, s.GatewayIP, s.IPPool)
		return err
	}
	_, err := d.Exec("UPDATE sstp_settings SET enabled = ?, port = ?, gateway_ip = ?, ip_pool = ?",
		en, s.Port, s.GatewayIP, s.IPPool)
	return err
}

func (d *DB) GetSSTPClients() ([]models.SSTPClient, error) {
	rows, err := d.Query(`SELECT id, name, username, password, assigned_ip, notes, enabled, last_seen, created_at, updated_at
		FROM sstp_clients ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.SSTPClient
	for rows.Next() {
		var c models.SSTPClient
		var en int
		if err := rows.Scan(&c.ID, &c.Name, &c.Username, &c.Password, &c.AssignedIP, &c.Notes, &en, &c.LastSeen, &c.CreatedAt, &c.UpdatedAt); err == nil {
			c.Enabled = (en == 1)
			list = append(list, c)
		}
	}
	return list, nil
}

func (d *DB) SaveSSTPClient(c *models.SSTPClient) error {
	en := 0
	if c.Enabled {
		en = 1
	}
	if c.ID == 0 {
		_, err := d.Exec(`INSERT INTO sstp_clients (name, username, password, assigned_ip, notes, enabled)
			VALUES (?, ?, ?, ?, ?, ?)`, c.Name, c.Username, c.Password, c.AssignedIP, c.Notes, en)
		return err
	}
	_, err := d.Exec(`UPDATE sstp_clients SET name = ?, username = ?, password = ?, assigned_ip = ?, notes = ?, enabled = ?
		WHERE id = ?`, c.Name, c.Username, c.Password, c.AssignedIP, c.Notes, en, c.ID)
	return err
}

func (d *DB) DeleteSSTPClient(id int64) error {
	_, err := d.Exec("DELETE FROM sstp_clients WHERE id = ?", id)
	return err
}

func (d *DB) UpdateSSTPClient(id int64, name, assignedIP, notes string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := d.Exec(`UPDATE sstp_clients SET name = ?, assigned_ip = ?, notes = ?, enabled = ? WHERE id = ?`,
		name, assignedIP, notes, en, id)
	return err
}

func (d *DB) GetSSTPClientByID(id int64) (*models.SSTPClient, error) {
	var c models.SSTPClient
	var en int
	err := d.QueryRow(`SELECT id, name, username, password, assigned_ip, notes, enabled, last_seen, created_at, updated_at
		FROM sstp_clients WHERE id = ?`, id).Scan(&c.ID, &c.Name, &c.Username, &c.Password, &c.AssignedIP, &c.Notes, &en, &c.LastSeen, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.Enabled = (en == 1)
	return &c, nil
}

func (d *DB) CreateSSTPToken(clientID int64, username, password string) (string, error) {
	token := randomAlphanumeric(32)
	_, err := d.Exec(`INSERT INTO sstp_tokens (token, client_id, username, password, used, created_at)
		VALUES (?, ?, ?, ?, 0, NOW())`, token, clientID, username, password)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (d *DB) ConsumeSSTPToken(token string) (clientID int64, username, password string, err error) {
	var used int
	err = d.QueryRow(`SELECT client_id, username, password, used FROM sstp_tokens WHERE token = ?`, token).Scan(&clientID, &username, &password, &used)
	if err != nil {
		return 0, "", "", fmt.Errorf("token not found")
	}
	if used == 1 {
		return 0, "", "", fmt.Errorf("token already used")
	}

	_, _ = d.Exec(`UPDATE sstp_tokens SET used = 1, used_at = NOW() WHERE token = ?`, token)
	return clientID, username, password, nil
}

func (d *DB) GetSSTPStaticRoutes() ([]models.SSTPStaticRoute, error) {
	query := `SELECT r.id, r.destination_cidr, r.gateway_ip, r.client_id, COALESCE(c.name, ''), r.description,
		r.enabled, r.is_applied, r.created_at, r.updated_at
		FROM sstp_static_routes r
		LEFT JOIN sstp_clients c ON r.client_id = c.id
		ORDER BY r.id DESC`
	rows, err := d.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.SSTPStaticRoute
	for rows.Next() {
		var sr models.SSTPStaticRoute
		var en, app int
		if err := rows.Scan(&sr.ID, &sr.DestinationCIDR, &sr.GatewayIP, &sr.ClientID, &sr.ClientName, &sr.Description,
			&en, &app, &sr.CreatedAt, &sr.UpdatedAt); err == nil {
			sr.Enabled = (en == 1)
			sr.IsApplied = (app == 1)
			list = append(list, sr)
		}
	}
	return list, nil
}

func (d *DB) SaveSSTPStaticRoute(r *models.SSTPStaticRoute) error {
	en := 0
	if r.Enabled {
		en = 1
	}
	app := 0
	if r.IsApplied {
		app = 1
	}

	if r.ID == 0 {
		res, err := d.Exec(`INSERT INTO sstp_static_routes (destination_cidr, gateway_ip, client_id, description, enabled, is_applied)
			VALUES (?, ?, ?, ?, ?, ?)`, r.DestinationCIDR, r.GatewayIP, r.ClientID, r.Description, en, app)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		r.ID = id
		return nil
	}

	_, err := d.Exec(`UPDATE sstp_static_routes SET destination_cidr = ?, gateway_ip = ?, client_id = ?, description = ?, enabled = ?, is_applied = ?
		WHERE id = ?`, r.DestinationCIDR, r.GatewayIP, r.ClientID, r.Description, en, app, r.ID)
	return err
}

func (d *DB) DeleteSSTPStaticRoute(id int64) (*models.SSTPStaticRoute, error) {
	var sr models.SSTPStaticRoute
	var en, app int
	err := d.QueryRow(`SELECT id, destination_cidr, gateway_ip, client_id, description, enabled, is_applied
		FROM sstp_static_routes WHERE id = ?`, id).Scan(&sr.ID, &sr.DestinationCIDR, &sr.GatewayIP, &sr.ClientID, &sr.Description, &en, &app)
	if err != nil {
		return nil, err
	}
	sr.Enabled = (en == 1)
	sr.IsApplied = (app == 1)

	_, _ = d.Exec("DELETE FROM sstp_static_routes WHERE id = ?", id)
	return &sr, nil
}

func (d *DB) SetSSTPRouteApplied(id int64, applied bool) error {
	app := 0
	if applied {
		app = 1
	}
	_, err := d.Exec("UPDATE sstp_static_routes SET is_applied = ? WHERE id = ?", app, id)
	return err
}

func (d *DB) SyncSSTPFiles() error {
	settings, err := d.GetSSTPSettings()
	if err != nil {
		return err
	}

	// 1. Generate /etc/ppp/chap-secrets
	chap := fmt.Sprintf("gw-ip-address * \"\" %s\n", settings.GatewayIP)
	clients, err := d.GetSSTPClients()
	if err == nil {
		for _, c := range clients {
			if c.Enabled {
				ip := c.AssignedIP
				if ip == "" {
					ip = "*"
				}
				chap += fmt.Sprintf("\"%s\" * \"%s\" %s\n", c.Username, c.Password, ip)
			}
		}
	}

	_ = os.MkdirAll("/etc/ppp", 0755)
	_ = os.WriteFile("/etc/ppp/chap-secrets", []byte(chap), 0600)

	// 2. Generate /etc/accel-ppp.conf
	conf := fmt.Sprintf(`[modules]
sstp
auth_chap_md5
auth_pap
auth_mschap_v1
auth_mschap_v2
chap-secrets
ippool
log_file

[core]
thread-count=2

[cli]
verbose=1
tcp=127.0.0.1:2001

[sstp]
verbose=1
accept=ssl
bind=0.0.0.0
port=%d
ssl-pemfile=/etc/ssl/certs/ssl-cert-snakeoil.pem
ssl-keyfile=/etc/ssl/private/ssl-cert-snakeoil.key
mppe=require

[ppp]
verbose=1
min-mtu=1280
mtu=1400
mru=1400
ipv4=require
ipv6=deny
lcp-echo-interval=20
lcp-echo-failure=3

[client-ip-range]
0.0.0.0/0

[ip-pool]
gw-ip-address=%s
%s

[chap-secrets]
gw-ip-address=%s
chap-secrets=/etc/ppp/chap-secrets

[log]
log-file=/var/log/accel-ppp/accel-ppp.log
log-emerg=/var/log/accel-ppp/emerg.log
log-fail-file=/var/log/accel-ppp/auth-fail.log
copy=1
level=3
`, settings.Port, settings.GatewayIP, settings.IPPool, settings.GatewayIP)

	_ = os.WriteFile("/etc/accel-ppp.conf", []byte(conf), 0644)

	// 3. Reload or Restart accel-ppp
	if exec.Command("which", "accel-cmd").Run() == nil {
		_ = exec.Command("accel-cmd", "reload").Run()
	} else {
		_ = exec.Command("systemctl", "restart", "accel-ppp").Run()
	}

	return nil
}

// --- Bulk Subscriber Operations ---

func (d *DB) BulkDeleteSubscribers(usernames []string) error {
	if len(usernames) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		_, _ = tx.Exec("DELETE FROM subscribers WHERE username = ?", u)
		_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ?", u)
		_, _ = tx.Exec("DELETE FROM radreply WHERE username = ?", u)
		_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", u)
		_, _ = tx.Exec("DELETE FROM cards WHERE username = ?", u)
	}

	return tx.Commit()
}

func (d *DB) BulkRenewSubscribers(usernames []string, targetProfile string) error {
	if len(usernames) == 0 {
		return nil
	}
	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		_ = d.RenewSubscriber(u, targetProfile)
	}
	return nil
}

func (d *DB) BulkAddQuota(usernames []string, quotaGB float64) error {
	if len(usernames) == 0 || quotaGB <= 0 {
		return nil
	}
	addBytes := int64(quotaGB * 1024 * 1024 * 1024)
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		_, _ = tx.Exec("UPDATE subscribers SET quota_bytes = quota_bytes + ? WHERE username = ?", addBytes, u)

		var currLimitStr string
		errQ := tx.QueryRow("SELECT value FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", u).Scan(&currLimitStr)
		if errQ == nil {
			currLimit, _ := strconv.ParseInt(currLimitStr, 10, 64)
			newLimit := currLimit + addBytes
			_, _ = tx.Exec("UPDATE radreply SET value = ? WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", fmt.Sprintf("%d", newLimit), u)
		} else {
			var newLimit int64
			_ = tx.QueryRow("SELECT quota_bytes FROM subscribers WHERE username = ?", u).Scan(&newLimit)
			if newLimit > 0 {
				_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", u, fmt.Sprintf("%d", newLimit))
			}
		}
	}

	return tx.Commit()
}

func (d *DB) BulkAddDebt(usernames []string, amount float64) error {
	if len(usernames) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		_, _ = tx.Exec("UPDATE subscribers SET debt = debt + ? WHERE username = ?", amount, u)
	}

	return tx.Commit()
}

// --- Billing & Financial Operations ---

func (d *DB) RenewSubscriberBilling(username, profileName string, price float64, isPaid bool, method, notes, adminUser string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if profileName == "" {
		_ = tx.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&profileName)
	}

	var p models.Profile
	err = tx.QueryRow(`SELECT id, name, display_name, quota_bytes, validity_value, validity_unit, price FROM profiles WHERE name = ?`, profileName).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.QuotaBytes, &p.ValidityValue, &p.ValidityUnit, &p.Price,
	)
	if err != nil {
		return fmt.Errorf("profile %s not found: %w", profileName, err)
	}

	if price <= 0 {
		price = p.Price
	}

	newExp := p.CalculateExpiration(time.Now())

	_, err = tx.Exec(`UPDATE subscribers SET
		profile_name = ?,
		quota_bytes = ?,
		expiration_date = ?,
		status = 'active'
		WHERE username = ?`, p.Name, p.QuotaBytes, newExp, username)
	if err != nil {
		return err
	}

	expStr := newExp.Format("02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username)
	_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, expStr)

	_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", username)
	_, _ = tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", username, p.Name)

	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username)
	if p.QuotaBytes > 0 {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", p.QuotaBytes))
	}

	var contractNo, fullName string
	_ = tx.QueryRow("SELECT COALESCE(contract_no, ''), COALESCE(full_name, '') FROM subscribers WHERE username = ?", username).Scan(&contractNo, &fullName)
	if fullName == "" {
		fullName = username
	}

	payStatus := "paid"
	if !isPaid {
		payStatus = "unpaid"
		if price > 0 {
			_, _ = tx.Exec("UPDATE subscribers SET debt = debt + ? WHERE username = ?", price, username)
		}
	}

	if method == "" {
		method = "cash"
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	desc := fmt.Sprintf("تجديد باقة: %s", p.DisplayName)
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'renew', ?, ?, ?, ?, ?, ?)`,
		username, contractNo, fullName, desc, price, payStatus, method, notes, adminUser)

	return tx.Commit()
}

func (d *DB) AddQuotaBilling(username string, quotaGB float64, price float64, isPaid bool, method, notes, adminUser string) error {
	if quotaGB <= 0 {
		return fmt.Errorf("invalid quota")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	addBytes := int64(quotaGB * 1024 * 1024 * 1024)

	_, _ = tx.Exec("UPDATE subscribers SET quota_bytes = quota_bytes + ? WHERE username = ?", addBytes, username)

	var currLimitStr string
	errQ := tx.QueryRow("SELECT value FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username).Scan(&currLimitStr)
	if errQ == nil {
		currLimit, _ := strconv.ParseInt(currLimitStr, 10, 64)
		newLimit := currLimit + addBytes
		_, _ = tx.Exec("UPDATE radreply SET value = ? WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", fmt.Sprintf("%d", newLimit), username)
	} else {
		var newLimit int64
		_ = tx.QueryRow("SELECT quota_bytes FROM subscribers WHERE username = ?", username).Scan(&newLimit)
		if newLimit > 0 {
			_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", newLimit))
		}
	}

	var contractNo, fullName string
	_ = tx.QueryRow("SELECT COALESCE(contract_no, ''), COALESCE(full_name, '') FROM subscribers WHERE username = ?", username).Scan(&contractNo, &fullName)
	if fullName == "" {
		fullName = username
	}

	payStatus := "paid"
	if !isPaid {
		payStatus = "unpaid"
		if price > 0 {
			_, _ = tx.Exec("UPDATE subscribers SET debt = debt + ? WHERE username = ?", price, username)
		}
	}

	if method == "" {
		method = "cash"
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	desc := fmt.Sprintf("إضافة كوتة رصيد: %.1f GB", quotaGB)
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'quota', ?, ?, ?, ?, ?, ?)`,
		username, contractNo, fullName, desc, price, payStatus, method, notes, adminUser)

	return tx.Commit()
}

func (d *DB) PaySubscriberDebt(username string, amount float64, method, notes, adminUser string) error {
	if amount <= 0 {
		return fmt.Errorf("invalid amount")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var contractNo, fullName string
	var currentDebt float64
	_ = tx.QueryRow("SELECT COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00) FROM subscribers WHERE username = ?", username).Scan(&contractNo, &fullName, &currentDebt)
	if fullName == "" {
		fullName = username
	}

	newDebt := currentDebt - amount
	if newDebt < 0 {
		newDebt = 0
	}

	_, err = tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newDebt, username)
	if err != nil {
		return err
	}

	if method == "" {
		method = "cash"
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	desc := fmt.Sprintf("سداد نقدي من المديونية (متبقي: %.2f ج.م)", newDebt)
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'payment', ?, ?, 'paid', ?, ?, ?)`,
		username, contractNo, fullName, desc, amount, method, notes, adminUser)

	return tx.Commit()
}

func (d *DB) GetBillingTransactions(search, typeFilter, statusFilter string, limit, offset int) ([]models.BillingTransaction, int, error) {
	query := `SELECT id, username, contract_no, full_name, type, description, amount, payment_status, payment_method, COALESCE(notes, ''), created_by, created_at
		FROM billing_transactions WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM billing_transactions WHERE 1=1`

	var args []interface{}
	var countArgs []interface{}

	if search != "" {
		query += " AND (username LIKE ? OR full_name LIKE ? OR contract_no LIKE ? OR description LIKE ?)"
		countQuery += " AND (username LIKE ? OR full_name LIKE ? OR contract_no LIKE ? OR description LIKE ?)"
		sArg := "%" + search + "%"
		args = append(args, sArg, sArg, sArg, sArg)
		countArgs = append(countArgs, sArg, sArg, sArg, sArg)
	}

	if typeFilter != "" {
		query += " AND type = ?"
		countQuery += " AND type = ?"
		args = append(args, typeFilter)
		countArgs = append(countArgs, typeFilter)
	}

	if statusFilter != "" {
		query += " AND payment_status = ?"
		countQuery += " AND payment_status = ?"
		args = append(args, statusFilter)
		countArgs = append(countArgs, statusFilter)
	}

	var totalCount int
	_ = d.QueryRow(countQuery, countArgs...).Scan(&totalCount)

	query += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []models.BillingTransaction
	for rows.Next() {
		var t models.BillingTransaction
		if err := rows.Scan(
			&t.ID, &t.Username, &t.ContractNo, &t.FullName, &t.Type, &t.Description,
			&t.Amount, &t.PaymentStatus, &t.PaymentMethod, &t.Notes, &t.CreatedBy, &t.CreatedAt,
		); err == nil {
			list = append(list, t)
		}
	}

	return list, totalCount, nil
}

func (d *DB) GetBillingFinancialSummary() (models.BillingSummary, error) {
	var s models.BillingSummary

	_ = d.QueryRow("SELECT COUNT(*) FROM billing_transactions").Scan(&s.TotalTransactions)
	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0.00) FROM billing_transactions WHERE type IN ('renew', 'quota')").Scan(&s.TotalSales)
	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0.00) FROM billing_transactions WHERE payment_status = 'paid'").Scan(&s.TotalPaid)
	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0.00) FROM billing_transactions WHERE payment_status = 'unpaid'").Scan(&s.TotalUnpaidDebt)
	_ = d.QueryRow("SELECT COALESCE(SUM(debt), 0.00) FROM subscribers").Scan(&s.TotalCustomerDebt)

	return s, nil
}

// --- WhatsApp Settings & Logs ---

func (d *DB) GetWhatsAppSettings() (*models.WhatsAppSettings, error) {
	s := &models.WhatsAppSettings{ID: 1, Enabled: true}
	err := d.QueryRow(`SELECT id, enabled, admin_phone, api_endpoint, api_token, instance_id,
		notify_on_new_subscriber, notify_on_expiry, notify_on_payment, updated_at
		FROM whatsapp_settings WHERE id = 1`).Scan(
		&s.ID, &s.Enabled, &s.AdminPhone, &s.APIEndpoint, &s.APIToken, &s.InstanceID,
		&s.NotifyOnNewSubscriber, &s.NotifyOnExpiry, &s.NotifyOnPayment, &s.UpdatedAt,
	)
	if err != nil {
		return s, nil
	}
	return s, nil
}

func (d *DB) SaveWhatsAppSettings(s *models.WhatsAppSettings) error {
	_, err := d.Exec(`INSERT INTO whatsapp_settings (id, enabled, admin_phone, api_endpoint, api_token, instance_id,
		notify_on_new_subscriber, notify_on_expiry, notify_on_payment)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		enabled = VALUES(enabled),
		admin_phone = VALUES(admin_phone),
		api_endpoint = VALUES(api_endpoint),
		api_token = VALUES(api_token),
		instance_id = VALUES(instance_id),
		notify_on_new_subscriber = VALUES(notify_on_new_subscriber),
		notify_on_expiry = VALUES(notify_on_expiry),
		notify_on_payment = VALUES(notify_on_payment)`,
		s.Enabled, s.AdminPhone, s.APIEndpoint, s.APIToken, s.InstanceID,
		s.NotifyOnNewSubscriber, s.NotifyOnExpiry, s.NotifyOnPayment,
	)
	return err
}

func (d *DB) GetWhatsAppLogs(limit int) ([]models.WhatsAppLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.Query(`SELECT id, phone, recipient_name, message_type, content, status, error_message, created_at
		FROM whatsapp_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.WhatsAppLog
	for rows.Next() {
		var l models.WhatsAppLog
		if err := rows.Scan(&l.ID, &l.Phone, &l.RecipientName, &l.MessageType, &l.Content, &l.Status, &l.ErrorMessage, &l.CreatedAt); err == nil {
			logs = append(logs, l)
		}
	}
	return logs, nil
}

func (d *DB) LogWhatsAppMessage(phone, recipient, msgType, content, status, errMsg string) error {
	_, err := d.Exec(`INSERT INTO whatsapp_logs (phone, recipient_name, message_type, content, status, error_message)
		VALUES (?, ?, ?, ?, ?, ?)`,
		phone, recipient, msgType, content, status, errMsg,
	)
	return err
}


