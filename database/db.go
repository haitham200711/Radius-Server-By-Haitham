package database

import (
	"database/sql"
	"fmt"
	"math/rand"
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
			price DECIMAL(10, 2) DEFAULT 0.00,
			shared_users INT DEFAULT 1,
			mikrotik_address_list VARCHAR(64) DEFAULT '',
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
	}

	for _, q := range queries {
		if _, err := d.Exec(q); err != nil {
			return fmt.Errorf("error running query: %w\n%s", err, q)
		}
	}

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
