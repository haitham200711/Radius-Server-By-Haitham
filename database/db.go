package database

import (
	"database/sql"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/radius"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/speedutil"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/whatsapp"
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

		// 16. WhatsApp Message Templates (قوالب رسائل وإشعارات الواتساب)
		`CREATE TABLE IF NOT EXISTS whatsapp_templates (
			id INT AUTO_INCREMENT PRIMARY KEY,
			event_key VARCHAR(64) UNIQUE NOT NULL,
			event_name VARCHAR(128) NOT NULL,
			enabled TINYINT(1) DEFAULT 1,
			template_text TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_wa_tmpl_key (event_key)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 17. Speed Control Configuration (التحكم بالسرعات ومضاعفة الباقات)
		`CREATE TABLE IF NOT EXISTS speed_control_config (
			id INT PRIMARY KEY,
			mode VARCHAR(20) DEFAULT 'off',
			boost_type VARCHAR(20) DEFAULT 'multiply',
			boost_value DECIMAL(6, 2) DEFAULT 2.00,
			temp_hours INT DEFAULT 2,
			temp_expires_at DATETIME NULL,
			recurring_enabled TINYINT(1) DEFAULT 0,
			recurring_start_time VARCHAR(10) DEFAULT '02:00',
			recurring_end_time VARCHAR(10) DEFAULT '08:00',
			disconnect_users TINYINT(1) DEFAULT 0,
			is_currently_boosted TINYINT(1) DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 18. Profile Base Speeds (سجل السرعات الأساسية للبروفايلات)
		`CREATE TABLE IF NOT EXISTS profile_base_speeds (
			profile_id INT PRIMARY KEY,
			profile_name VARCHAR(64) NOT NULL,
			base_rate_limit VARCHAR(64) NOT NULL,
			boosted_rate_limit VARCHAR(64) DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
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
		`ALTER TABLE whatsapp_settings ADD COLUMN IF NOT EXISTS debt_reminder_enabled TINYINT(1) DEFAULT 1;`,
		`ALTER TABLE whatsapp_settings ADD COLUMN IF NOT EXISTS debt_reminder_days INT DEFAULT 3;`,
		`ALTER TABLE whatsapp_settings ADD COLUMN IF NOT EXISTS debt_reminder_min_debt DECIMAL(10,2) DEFAULT 1.00;`,
		`ALTER TABLE whatsapp_settings ADD COLUMN IF NOT EXISTS debt_reminder_last_run DATETIME NULL;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS last_renewed_at DATETIME NULL;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS notified_expired TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS notified_quota_50 TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS notified_quota_75 TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS notified_quota_100 TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS last_debt_reminder_at DATETIME NULL;`,
		`ALTER TABLE nas ADD COLUMN IF NOT EXISTS phone VARCHAR(32) DEFAULT '';`,
		`ALTER TABLE nas ADD COLUMN IF NOT EXISTS is_cafe TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS cost_price DECIMAL(10, 2) DEFAULT 0.00;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS nas_id INT DEFAULT 0;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS card_type VARCHAR(20) DEFAULT 'charge';`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS batch_name VARCHAR(100) DEFAULT '';`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS cost_price_per_card DECIMAL(10, 2) DEFAULT 0.00;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS total_cost_price DECIMAL(10, 2) DEFAULT 0.00;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS quota_bytes BIGINT DEFAULT 0;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS time_limit_seconds BIGINT DEFAULT 0;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS time_calc_mode VARCHAR(20) DEFAULT 'first_login';`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS simultaneous_sessions INT DEFAULT 1;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS stop_on_quota TINYINT(1) DEFAULT 1;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS bind_mac TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS lock_mac TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS service_name VARCHAR(100) DEFAULT '';`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS affix_type VARCHAR(10) DEFAULT 'prefix';`,
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS affix_value VARCHAR(50) DEFAULT '';`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS nas_id INT DEFAULT 0;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS card_type VARCHAR(20) DEFAULT 'charge';`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS cost_price DECIMAL(10, 2) DEFAULT 0.00;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS quota_bytes BIGINT DEFAULT 0;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS time_limit_seconds BIGINT DEFAULT 0;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS time_calc_mode VARCHAR(20) DEFAULT 'first_login';`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS simultaneous_sessions INT DEFAULT 1;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS stop_on_quota TINYINT(1) DEFAULT 1;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS bind_mac TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS lock_mac TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS service_name VARCHAR(100) DEFAULT '';`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS calling_mac VARCHAR(32) DEFAULT '';`,
	}
	for _, m := range migrations {
		_, _ = d.Exec(m)
	}

	// Ensure default WhatsApp settings row exists
	_, _ = d.Exec("INSERT IGNORE INTO whatsapp_settings (id, enabled, admin_phone) VALUES (1, 1, '')")

	// Seed default WhatsApp event notification templates
	defaultTemplates := []struct {
		key  string
		name string
		text string
	}{
		{
			key:  "time_expired",
			name: "انتهاء وقت واشتراك العميل",
			text: "عزيزي المشترك {name}، نود إعلامكم بأن اشتراككم رقم ({contract_no}) قد انتهى بتاريخ {expiry_date}. يرجى سرعة التجديد لضمان استمرار الخدمة.",
		},
		{
			key:  "quota_50",
			name: "استهلاك 50% من كوتة العميل",
			text: "مرحباً {name}، لقد تم استهلاك 50% من كوتة الإنترنت الخاصة بك ({quota_used} جيجا من إجمالي {quota_total} جيجا). المتبقي: {quota_remaining} جيجا.",
		},
		{
			key:  "quota_75",
			name: "استهلاك 75% من كوتة العميل",
			text: "تنبيه: مرحباً {name}، لقد استهلكت 75% من كوتة الإنترنت. المتبقي لديك: {quota_remaining} جيجا. تاريخ الانتهاء: {expiry_date}.",
		},
		{
			key:  "quota_100",
			name: "استهلاك 100% من كوتة العميل",
			text: "تنبيه هام: مرحباً {name}، لقد تم استهلاك 100% من كوتة الإنترنت بالكامل. يرجى شحن كوتة إضافية أو تجديد الباقة لاستعادة السرعة الكاملة.",
		},
		{
			key:  "renew",
			name: "تجديد اشتراك العميل",
			text: "مرحباً {name}، تم تجديد اشتراككم بنجاح على باقة ({profile}). تاريخ الانتهاء الجديد: {expiry_date}. شكراً لثقتكم بنا.",
		},
		{
			key:  "debt_added",
			name: "تسجيل أو إضافة مديونية جديدة",
			text: "مرحباً {name}، تم تسجيل مديونية على حسابكم بمبلغ {amount} ج.م. إجمالي الدين المستحق الحالي: {debt} ج.م.",
		},
		{
			key:  "debt_paid",
			name: "سداد دفعة من الدين",
			text: "مرحباً {name}، تم استلام سداد بمبلغ {amount} ج.م بنجاح. المتبقي من الدين: {debt} ج.م. شكراً لتعاملكم الراقي معنا.",
		},
		{
			key:  "debt_reminder",
			name: "التذكير الدوري بالديون المستحقة",
			text: "عزيزي المشترك {name}، نود تذكيركم بوجود مديونية مستحقة بقيمة {debt} ج.م على اشتراككم رقم ({contract_no}). يرجى سرعة السداد لتفادي توقف الخدمة.",
		},
	}
	for _, dt := range defaultTemplates {
		_, _ = d.Exec(`INSERT IGNORE INTO whatsapp_templates (event_key, event_name, enabled, template_text) VALUES (?, ?, 1, ?)`,
			dt.key, dt.name, dt.text)
	}

	// Seed default speed control config row
	_, _ = d.Exec(`INSERT IGNORE INTO speed_control_config (id, mode, boost_type, boost_value, temp_hours, recurring_enabled, recurring_start_time, recurring_end_time, disconnect_users, is_currently_boosted)
		VALUES (1, 'off', 'multiply', 2.00, 2, 0, '02:00', '08:00', 0, 0)`)
	_ = d.SyncProfileBaseSpeeds()

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

func randomNumeric(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[rand.Intn(len(digits))]
	}
	return string(b)
}

func (d *DB) GenerateAdvancedBatch(opts models.GenerateBatchOptions) (string, int, error) {
	if opts.Count <= 0 || opts.Count > 50000 {
		opts.Count = 100
	}
	if opts.CharLen < 4 {
		opts.CharLen = 6
	}
	if opts.SimultaneousSessions <= 0 {
		opts.SimultaneousSessions = 1
	}
	if opts.TimeCalcMode == "" {
		opts.TimeCalcMode = "first_login"
	}
	if opts.AffixType == "" {
		opts.AffixType = "prefix"
	}

	var prof models.Profile
	err := d.QueryRow("SELECT id, name, display_name, cost_price, price, quota_bytes, uptime_seconds, validity_value, validity_unit FROM profiles WHERE id = ?", opts.ProfileID).Scan(
		&prof.ID, &prof.Name, &prof.DisplayName, &prof.CostPrice, &prof.Price, &prof.QuotaBytes, &prof.UptimeSeconds, &prof.ValidityValue, &prof.ValidityUnit)
	if err != nil {
		return "", 0, fmt.Errorf("profile not found: %w", err)
	}

	if opts.PricePerCard <= 0 {
		opts.PricePerCard = prof.Price
	}
	if opts.CostPricePerCard <= 0 {
		opts.CostPricePerCard = prof.CostPrice
	}
	if opts.QuotaBytes == 0 && prof.QuotaBytes > 0 {
		opts.QuotaBytes = prof.QuotaBytes
	}
	if opts.TimeLimitSeconds == 0 {
		if prof.UptimeSeconds > 0 {
			opts.TimeLimitSeconds = prof.UptimeSeconds
		} else if prof.ValidityValue > 0 {
			switch prof.ValidityUnit {
			case "minutes":
				opts.TimeLimitSeconds = int64(prof.ValidityValue * 60)
			case "hours":
				opts.TimeLimitSeconds = int64(prof.ValidityValue * 3600)
			case "days":
				opts.TimeLimitSeconds = int64(prof.ValidityValue * 86400)
			case "months":
				opts.TimeLimitSeconds = int64(prof.ValidityValue * 86400 * 30)
			}
		}
	}

	prefixLetter := "B"
	if opts.CardType == "cafe" {
		prefixLetter = "CF"
	}
	batchCode := fmt.Sprintf("%s%d-%s", prefixLetter, time.Now().Unix(), randomLetters(4))
	if opts.BatchName == "" {
		opts.BatchName = fmt.Sprintf("دفعة %s", batchCode)
	}
	totalPrice := float64(opts.Count) * opts.PricePerCard
	totalCostPrice := float64(opts.Count) * opts.CostPricePerCard

	tx, err := d.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	// 1. Insert Batch record
	res, err := tx.Exec(`INSERT INTO batches (
		batch_code, batch_name, profile_id, reseller_id, count, cost_price_per_card, price_per_card,
		total_cost_price, total_price, card_type, nas_id, quota_bytes, time_limit_seconds, time_calc_mode,
		simultaneous_sessions, stop_on_quota, bind_mac, lock_mac, service_name, affix_type, affix_value, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		batchCode, opts.BatchName, opts.ProfileID, opts.ResellerID, opts.Count, opts.CostPricePerCard, opts.PricePerCard,
		totalCostPrice, totalPrice, opts.CardType, opts.NASID, opts.QuotaBytes, opts.TimeLimitSeconds, opts.TimeCalcMode,
		opts.SimultaneousSessions, opts.StopOnQuota, opts.BindMAC, opts.LockMAC, opts.ServiceName, opts.AffixType, opts.AffixValue)
	if err != nil {
		return "", 0, err
	}
	batchID, _ := res.LastInsertId()

	// 2. Prepare bulk insert statements
	stmtCard, err := tx.Prepare(`INSERT INTO cards (
		batch_id, username, password, profile_id, reseller_id, status, cost_price, price,
		card_type, nas_id, quota_bytes, time_limit_seconds, time_calc_mode,
		simultaneous_sessions, stop_on_quota, bind_mac, lock_mac, service_name, created_at
	) VALUES (?, ?, ?, ?, ?, 'available', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`)
	if err != nil {
		return "", 0, err
	}
	defer stmtCard.Close()

	stmtRadCheck, err := tx.Prepare(`INSERT INTO radcheck (username, attribute, op, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return "", 0, err
	}
	defer stmtRadCheck.Close()

	stmtRadReply, err := tx.Prepare(`INSERT INTO radreply (username, attribute, op, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return "", 0, err
	}
	defer stmtRadReply.Close()

	stmtRadGroup, err := tx.Prepare(`INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)`)
	if err != nil {
		return "", 0, err
	}
	defer stmtRadGroup.Close()

	created := 0
	for i := 0; i < opts.Count; i++ {
		var randPart string
		if opts.Format == "num" {
			randPart = randomNumeric(opts.CharLen)
		} else if opts.Format == "alpha" {
			randPart = randomLetters(opts.CharLen)
		} else {
			randPart = randomAlphanumeric(opts.CharLen)
		}

		var u string
		if opts.AffixType == "suffix" {
			u = randPart + opts.AffixValue
		} else {
			u = opts.AffixValue + randPart
		}

		p := u // Standard hotspot login: username == password

		if _, err := stmtCard.Exec(batchID, u, p, opts.ProfileID, opts.ResellerID, opts.CostPricePerCard, opts.PricePerCard,
			opts.CardType, opts.NASID, opts.QuotaBytes, opts.TimeLimitSeconds, opts.TimeCalcMode,
			opts.SimultaneousSessions, opts.StopOnQuota, opts.BindMAC, opts.LockMAC, opts.ServiceName); err == nil {

			// radcheck: Cleartext-Password
			_, _ = stmtRadCheck.Exec(u, "Cleartext-Password", ":=", p)

			// radcheck: Simultaneous-Use
			if opts.SimultaneousSessions > 0 {
				_, _ = stmtRadCheck.Exec(u, "Simultaneous-Use", ":=", strconv.Itoa(opts.SimultaneousSessions))
			}

			// radcheck: Called-Station-Id (Service Name / Hotspot Server check)
			if opts.ServiceName != "" && opts.ServiceName != "all" {
				_, _ = stmtRadCheck.Exec(u, "Called-Station-Id", "=~", opts.ServiceName)
			}

			// radcheck: Max-All-Session (if time calc mode is by_second)
			if opts.TimeCalcMode == "by_second" && opts.TimeLimitSeconds > 0 {
				_, _ = stmtRadCheck.Exec(u, "Max-All-Session", ":=", strconv.FormatInt(opts.TimeLimitSeconds, 10))
			}

			// radreply: Mikrotik-Total-Limit (Total data limit in bytes)
			if opts.QuotaBytes > 0 && opts.StopOnQuota {
				_, _ = stmtRadReply.Exec(u, "Mikrotik-Total-Limit", ":=", strconv.FormatInt(opts.QuotaBytes, 10))
			}

			// radreply: Session-Timeout
			if opts.TimeLimitSeconds > 0 {
				_, _ = stmtRadReply.Exec(u, "Session-Timeout", ":=", strconv.FormatInt(opts.TimeLimitSeconds, 10))
			}

			// radusergroup: link to profile
			_, _ = stmtRadGroup.Exec(u, prof.Name)

			created++
		}
	}

	if opts.ResellerID > 0 {
		_, _ = tx.Exec("UPDATE resellers SET balance = balance - ? WHERE id = ?", totalPrice, opts.ResellerID)
	}

	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	return batchCode, created, nil
}

func (d *DB) GenerateBatch(count int, prefix string, charLen int, profileID int64, resellerID int64, pricePerCard float64) (string, int, error) {
	return d.GenerateAdvancedBatch(models.GenerateBatchOptions{
		Count:                count,
		AffixType:            "prefix",
		AffixValue:           prefix,
		CharLen:              charLen,
		ProfileID:            profileID,
		ResellerID:           resellerID,
		PricePerCard:         pricePerCard,
		CardType:             "charge",
		StopOnQuota:          true,
		SimultaneousSessions: 1,
	})
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
		validity_value, validity_unit, COALESCE(cost_price, 0.00), price, shared_users, mikrotik_address_list,
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
			&p.ValidityValue, &p.ValidityUnit, &p.CostPrice, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
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
		validity_value, validity_unit, COALESCE(cost_price, 0.00), price, shared_users, mikrotik_address_list,
		on_quota_action, on_quota_profile, on_quota_pool,
		on_time_action, on_time_profile, on_time_pool, created_at
		FROM profiles WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds,
		&p.ValidityValue, &p.ValidityUnit, &p.CostPrice, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
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
		validity_value, validity_unit, COALESCE(cost_price, 0.00), price, shared_users, mikrotik_address_list,
		on_quota_action, on_quota_profile, on_quota_pool,
		on_time_action, on_time_profile, on_time_pool, created_at
		FROM profiles WHERE name = ?`, name).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds,
		&p.ValidityValue, &p.ValidityUnit, &p.CostPrice, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
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
			validity_value, validity_unit, cost_price, price, shared_users, mikrotik_address_list,
			on_quota_action, on_quota_profile, on_quota_pool,
			on_time_action, on_time_profile, on_time_pool)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.Name, p.DisplayName, p.RateLimit, p.PoolName, p.QuotaBytes, p.UptimeSeconds,
			p.ValidityValue, p.ValidityUnit, p.CostPrice, p.Price, p.SharedUsers, p.MikrotikAddressList,
			p.OnQuotaAction, p.OnQuotaProfile, p.OnQuotaPool,
			p.OnTimeAction, p.OnTimeProfile, p.OnTimePool)
		if err != nil {
			return err
		}
		p.ID, _ = res.LastInsertId()
	} else {
		_, err := tx.Exec(`UPDATE profiles SET
			name = ?, display_name = ?, rate_limit = ?, pool_name = ?, quota_bytes = ?, uptime_seconds = ?,
			validity_value = ?, validity_unit = ?, cost_price = ?, price = ?, shared_users = ?, mikrotik_address_list = ?,
			on_quota_action = ?, on_quota_profile = ?, on_quota_pool = ?,
			on_time_action = ?, on_time_profile = ?, on_time_pool = ?
			WHERE id = ?`,
			p.Name, p.DisplayName, p.RateLimit, p.PoolName, p.QuotaBytes, p.UptimeSeconds,
			p.ValidityValue, p.ValidityUnit, p.CostPrice, p.Price, p.SharedUsers, p.MikrotikAddressList,
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

	if err := tx.Commit(); err != nil {
		return err
	}

	// Reset notification flags and cycle start for the new period
	_, _ = d.Exec(`UPDATE subscribers SET
		last_renewed_at = NOW(),
		notified_expired = 0,
		notified_quota_50 = 0,
		notified_quota_75 = 0,
		notified_quota_100 = 0
		WHERE username = ?`, username)

	// Trigger WhatsApp notifications asynchronously
	go func() {
		sub, err := d.GetSubscriber(username)
		if err == nil && sub != nil {
			_ = d.TriggerWhatsAppEvent("renew", sub, map[string]string{
				"{amount}":  fmt.Sprintf("%.2f", price),
				"{profile}": p.DisplayName,
			})
			if !isPaid && price > 0 {
				_ = d.TriggerWhatsAppEvent("debt_added", sub, map[string]string{
					"{amount}": fmt.Sprintf("%.2f", price),
				})
			}
		}
	}()

	return nil
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

	if err := tx.Commit(); err != nil {
		return err
	}

	// Reset quota 100 notification flag when quota is expanded
	_, _ = d.Exec(`UPDATE subscribers SET notified_quota_100 = 0 WHERE username = ?`, username)

	// Trigger debt added if unpaid
	go func() {
		sub, err := d.GetSubscriber(username)
		if err == nil && sub != nil {
			if !isPaid && price > 0 {
				_ = d.TriggerWhatsAppEvent("debt_added", sub, map[string]string{
					"{amount}": fmt.Sprintf("%.2f", price),
				})
			}
		}
	}()

	return nil
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

	if err := tx.Commit(); err != nil {
		return err
	}

	// Trigger WhatsApp notification for debt payment
	go func() {
		sub, err := d.GetSubscriber(username)
		if err == nil && sub != nil {
			_ = d.TriggerWhatsAppEvent("debt_paid", sub, map[string]string{
				"{amount}": fmt.Sprintf("%.2f", amount),
				"{debt}":   fmt.Sprintf("%.2f", newDebt),
			})
		}
	}()

	return nil
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

// --- WhatsApp Settings & Logs & Automations ---

func (d *DB) GetWhatsAppSettings() (*models.WhatsAppSettings, error) {
	s := &models.WhatsAppSettings{
		ID:                  1,
		Enabled:             true,
		DebtReminderEnabled: true,
		DebtReminderDays:    3,
		DebtReminderMinDebt: 1.00,
	}
	var lastRun sql.NullTime
	err := d.QueryRow(`SELECT id, enabled, admin_phone, api_endpoint, api_token, instance_id,
		notify_on_new_subscriber, notify_on_expiry, notify_on_payment,
		COALESCE(debt_reminder_enabled, 1), COALESCE(debt_reminder_days, 3), COALESCE(debt_reminder_min_debt, 1.00), debt_reminder_last_run, updated_at
		FROM whatsapp_settings WHERE id = 1`).Scan(
		&s.ID, &s.Enabled, &s.AdminPhone, &s.APIEndpoint, &s.APIToken, &s.InstanceID,
		&s.NotifyOnNewSubscriber, &s.NotifyOnExpiry, &s.NotifyOnPayment,
		&s.DebtReminderEnabled, &s.DebtReminderDays, &s.DebtReminderMinDebt, &lastRun, &s.UpdatedAt,
	)
	if err != nil {
		return s, nil
	}
	if lastRun.Valid {
		s.DebtReminderLastRun = lastRun.Time
	}
	return s, nil
}

func (d *DB) SaveWhatsAppSettings(s *models.WhatsAppSettings) error {
	_, err := d.Exec(`INSERT INTO whatsapp_settings (id, enabled, admin_phone, api_endpoint, api_token, instance_id,
		notify_on_new_subscriber, notify_on_expiry, notify_on_payment,
		debt_reminder_enabled, debt_reminder_days, debt_reminder_min_debt)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		enabled = VALUES(enabled),
		admin_phone = VALUES(admin_phone),
		api_endpoint = VALUES(api_endpoint),
		api_token = VALUES(api_token),
		instance_id = VALUES(instance_id),
		notify_on_new_subscriber = VALUES(notify_on_new_subscriber),
		notify_on_expiry = VALUES(notify_on_expiry),
		notify_on_payment = VALUES(notify_on_payment),
		debt_reminder_enabled = VALUES(debt_reminder_enabled),
		debt_reminder_days = VALUES(debt_reminder_days),
		debt_reminder_min_debt = VALUES(debt_reminder_min_debt)`,
		s.Enabled, s.AdminPhone, s.APIEndpoint, s.APIToken, s.InstanceID,
		s.NotifyOnNewSubscriber, s.NotifyOnExpiry, s.NotifyOnPayment,
		s.DebtReminderEnabled, s.DebtReminderDays, s.DebtReminderMinDebt,
	)
	return err
}

func (d *DB) GetWhatsAppTemplates() ([]models.WhatsAppTemplate, error) {
	rows, err := d.Query(`SELECT id, event_key, event_name, enabled, template_text, updated_at
		FROM whatsapp_templates ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.WhatsAppTemplate
	for rows.Next() {
		var t models.WhatsAppTemplate
		if err := rows.Scan(&t.ID, &t.EventKey, &t.EventName, &t.Enabled, &t.TemplateText, &t.UpdatedAt); err == nil {
			list = append(list, t)
		}
	}
	return list, nil
}

func (d *DB) GetWhatsAppTemplate(eventKey string) (*models.WhatsAppTemplate, error) {
	var t models.WhatsAppTemplate
	err := d.QueryRow(`SELECT id, event_key, event_name, enabled, template_text, updated_at
		FROM whatsapp_templates WHERE event_key = ?`, eventKey).Scan(
		&t.ID, &t.EventKey, &t.EventName, &t.Enabled, &t.TemplateText, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (d *DB) SaveWhatsAppTemplate(eventKey string, enabled bool, text string) error {
	_, err := d.Exec(`UPDATE whatsapp_templates SET enabled = ?, template_text = ? WHERE event_key = ?`,
		enabled, text, eventKey)
	return err
}

func (d *DB) TriggerWhatsAppEvent(eventKey string, sub *models.Subscriber, extraVars map[string]string) error {
	waSettings, err := d.GetWhatsAppSettings()
	if err != nil || !waSettings.Enabled {
		return nil
	}

	tmpl, err := d.GetWhatsAppTemplate(eventKey)
	if err != nil || !tmpl.Enabled || strings.TrimSpace(tmpl.TemplateText) == "" {
		return nil
	}

	if sub == nil || strings.TrimSpace(sub.Phone) == "" {
		return nil
	}

	vars := map[string]string{
		"{name}":            sub.FullName,
		"{username}":        sub.Username,
		"{contract_no}":     sub.ContractNo,
		"{phone}":           sub.Phone,
		"{profile}":         sub.ProfileName,
		"{debt}":            fmt.Sprintf("%.2f", sub.Debt),
		"{quota_total}":     fmt.Sprintf("%.1f", float64(sub.QuotaBytes)/(1024*1024*1024)),
		"{date}":            time.Now().Format("2006-01-02"),
		"{time}":            time.Now().Format("15:04"),
	}
	if sub.ExpirationDate.Valid {
		vars["{expiry_date}"] = sub.ExpirationDate.Time.Format("2006-01-02")
	} else {
		vars["{expiry_date}"] = "غير محدد"
	}

	for k, v := range extraVars {
		vars[k] = v
	}

	text := tmpl.TemplateText
	for k, v := range vars {
		text = strings.ReplaceAll(text, k, v)
	}

	var sendErr error
	status := whatsapp.GetWhatsAppStatus()
	if status.IsLoggedIn {
		sendErr = whatsapp.SendWhatsAppMessage(sub.Phone, text)
	} else if waSettings.APIEndpoint != "" {
		sendErr = whatsapp.SendWhatsAppExternalFallback(waSettings.APIEndpoint, waSettings.APIToken, waSettings.InstanceID, sub.Phone, text)
	} else {
		sendErr = fmt.Errorf("خدمة واتساب غير مقترنة")
	}

	logStatus := "sent"
	errStr := ""
	if sendErr != nil {
		logStatus = "failed"
		errStr = sendErr.Error()
	}
	_ = d.LogWhatsAppMessage(sub.Phone, sub.FullName, eventKey, text, logStatus, errStr)
	return sendErr
}

func (d *DB) RunDebtReminders() (int, error) {
	waSettings, err := d.GetWhatsAppSettings()
	if err != nil || !waSettings.Enabled || !waSettings.DebtReminderEnabled {
		return 0, nil
	}

	tmpl, err := d.GetWhatsAppTemplate("debt_reminder")
	if err != nil || !tmpl.Enabled || strings.TrimSpace(tmpl.TemplateText) == "" {
		return 0, nil
	}

	intervalDays := waSettings.DebtReminderDays
	if intervalDays <= 0 {
		intervalDays = 3
	}
	minDebt := waSettings.DebtReminderMinDebt
	if minDebt <= 0 {
		minDebt = 1.00
	}

	query := `SELECT id, username, contract_no, full_name, phone, debt, profile_name, expiration_date
		FROM subscribers
		WHERE debt >= ? AND phone != '' AND (last_debt_reminder_at IS NULL OR TIMESTAMPDIFF(DAY, last_debt_reminder_at, NOW()) >= ?)`

	rows, err := d.Query(query, minDebt, intervalDays)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type IndebtedSub struct {
		ID         int64
		Username   string
		ContractNo string
		FullName   string
		Phone      string
		Debt       float64
		Profile    string
		ExpDate    sql.NullTime
	}
	var targets []IndebtedSub
	for rows.Next() {
		var s IndebtedSub
		if err := rows.Scan(&s.ID, &s.Username, &s.ContractNo, &s.FullName, &s.Phone, &s.Debt, &s.Profile, &s.ExpDate); err == nil {
			targets = append(targets, s)
		}
	}

	count := 0
	for _, s := range targets {
		subModel := &models.Subscriber{
			ID:             s.ID,
			Username:       s.Username,
			ContractNo:     s.ContractNo,
			FullName:       s.FullName,
			Phone:          s.Phone,
			Debt:           s.Debt,
			ProfileName:    s.Profile,
			ExpirationDate: s.ExpDate,
		}
		err := d.TriggerWhatsAppEvent("debt_reminder", subModel, map[string]string{
			"{debt}": fmt.Sprintf("%.2f", s.Debt),
		})
		if err == nil {
			_, _ = d.Exec("UPDATE subscribers SET last_debt_reminder_at = NOW() WHERE id = ?", s.ID)
			count++
		}
		time.Sleep(200 * time.Millisecond)
	}

	_, _ = d.Exec("UPDATE whatsapp_settings SET debt_reminder_last_run = NOW() WHERE id = 1")
	return count, nil
}

func (d *DB) CheckQuotaAndExpiryAutomations() error {
	waSettings, err := d.GetWhatsAppSettings()
	if err != nil || !waSettings.Enabled {
		return nil
	}

	// 1. Time Expired check
	expTmpl, err := d.GetWhatsAppTemplate("time_expired")
	if err == nil && expTmpl.Enabled {
		rows, err := d.Query(`SELECT id, username, contract_no, full_name, phone, debt, profile_name, expiration_date
			FROM subscribers
			WHERE expiration_date IS NOT NULL AND expiration_date <= NOW() AND notified_expired = 0 AND phone != ''`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var s models.Subscriber
				if err := rows.Scan(&s.ID, &s.Username, &s.ContractNo, &s.FullName, &s.Phone, &s.Debt, &s.ProfileName, &s.ExpirationDate); err == nil {
					_ = d.TriggerWhatsAppEvent("time_expired", &s, nil)
					_, _ = d.Exec("UPDATE subscribers SET notified_expired = 1 WHERE id = ?", s.ID)
					time.Sleep(150 * time.Millisecond)
				}
			}
		}
	}

	// 2. Quota Usage Checks (50%, 75%, 100%)
	q50Tmpl, _ := d.GetWhatsAppTemplate("quota_50")
	q75Tmpl, _ := d.GetWhatsAppTemplate("quota_75")
	q100Tmpl, _ := d.GetWhatsAppTemplate("quota_100")

	if (q50Tmpl != nil && q50Tmpl.Enabled) || (q75Tmpl != nil && q75Tmpl.Enabled) || (q100Tmpl != nil && q100Tmpl.Enabled) {
		rows, err := d.Query(`SELECT s.id, s.username, s.contract_no, s.full_name, s.phone, s.debt, s.profile_name,
			s.quota_bytes, s.expiration_date, s.last_renewed_at, s.created_at,
			COALESCE(s.notified_quota_50, 0), COALESCE(s.notified_quota_75, 0), COALESCE(s.notified_quota_100, 0)
			FROM subscribers s
			WHERE s.quota_bytes > 0 AND s.phone != '' AND s.status = 'active'`)
		if err == nil {
			defer rows.Close()
			type SubQuotaData struct {
				ID            int64
				Username      string
				ContractNo    string
				FullName      string
				Phone         string
				Debt          float64
				Profile       string
				QuotaBytes    int64
				ExpDate       sql.NullTime
				LastRenewedAt sql.NullTime
				CreatedAt     time.Time
				NotifiedQ50   int
				NotifiedQ75   int
				NotifiedQ100  int
			}
			var subList []SubQuotaData
			for rows.Next() {
				var sq SubQuotaData
				if err := rows.Scan(&sq.ID, &sq.Username, &sq.ContractNo, &sq.FullName, &sq.Phone, &sq.Debt, &sq.Profile,
					&sq.QuotaBytes, &sq.ExpDate, &sq.LastRenewedAt, &sq.CreatedAt,
					&sq.NotifiedQ50, &sq.NotifiedQ75, &sq.NotifiedQ100); err == nil {
					subList = append(subList, sq)
				}
			}

			for _, sq := range subList {
				cycleStart := sq.CreatedAt
				if sq.LastRenewedAt.Valid {
					cycleStart = sq.LastRenewedAt.Time
				}

				var usedBytes int64
				_ = d.QueryRow(`SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0)
					FROM radacct
					WHERE username = ? AND acctstarttime >= ?`, sq.Username, cycleStart).Scan(&usedBytes)

				if sq.QuotaBytes <= 0 {
					continue
				}

				usagePercent := (float64(usedBytes) / float64(sq.QuotaBytes)) * 100.0
				remainingBytes := sq.QuotaBytes - usedBytes
				if remainingBytes < 0 {
					remainingBytes = 0
				}

				extraVars := map[string]string{
					"{quota_total}":     fmt.Sprintf("%.1f", float64(sq.QuotaBytes)/(1024*1024*1024)),
					"{quota_used}":      fmt.Sprintf("%.1f", float64(usedBytes)/(1024*1024*1024)),
					"{quota_remaining}": fmt.Sprintf("%.1f", float64(remainingBytes)/(1024*1024*1024)),
					"{quota_percent}":   fmt.Sprintf("%.0f%%", usagePercent),
				}

				subModel := &models.Subscriber{
					ID:             sq.ID,
					Username:       sq.Username,
					ContractNo:     sq.ContractNo,
					FullName:       sq.FullName,
					Phone:          sq.Phone,
					Debt:           sq.Debt,
					ProfileName:    sq.Profile,
					QuotaBytes:     sq.QuotaBytes,
					ExpirationDate: sq.ExpDate,
				}

				if usagePercent >= 100.0 && sq.NotifiedQ100 == 0 && q100Tmpl != nil && q100Tmpl.Enabled {
					_ = d.TriggerWhatsAppEvent("quota_100", subModel, extraVars)
					_, _ = d.Exec("UPDATE subscribers SET notified_quota_100 = 1, notified_quota_75 = 1, notified_quota_50 = 1 WHERE id = ?", sq.ID)
					time.Sleep(150 * time.Millisecond)
				} else if usagePercent >= 75.0 && sq.NotifiedQ75 == 0 && q75Tmpl != nil && q75Tmpl.Enabled {
					_ = d.TriggerWhatsAppEvent("quota_75", subModel, extraVars)
					_, _ = d.Exec("UPDATE subscribers SET notified_quota_75 = 1, notified_quota_50 = 1 WHERE id = ?", sq.ID)
					time.Sleep(150 * time.Millisecond)
				} else if usagePercent >= 50.0 && sq.NotifiedQ50 == 0 && q50Tmpl != nil && q50Tmpl.Enabled {
					_ = d.TriggerWhatsAppEvent("quota_50", subModel, extraVars)
					_, _ = d.Exec("UPDATE subscribers SET notified_quota_50 = 1 WHERE id = ?", sq.ID)
					time.Sleep(150 * time.Millisecond)
				}
			}
		}
	}

	return nil
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

// ============================================================================
// Speed Control (التحكم بالسرعات ومضاعفة الباقات)
// ============================================================================

func (d *DB) GetSpeedControlConfig() (*models.SpeedControlConfig, error) {
	row := d.QueryRow(`SELECT id, mode, boost_type, boost_value, temp_hours, temp_expires_at,
		recurring_enabled, recurring_start_time, recurring_end_time, disconnect_users, is_currently_boosted, updated_at
		FROM speed_control_config WHERE id = 1`)

	cfg := &models.SpeedControlConfig{}
	var tempExp sql.NullTime
	var recEnabled, disUsers, isBoosted int

	err := row.Scan(&cfg.ID, &cfg.Mode, &cfg.BoostType, &cfg.BoostValue, &cfg.TempHours, &tempExp,
		&recEnabled, &cfg.RecurringStartTime, &cfg.RecurringEndTime, &disUsers, &isBoosted, &cfg.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			cfg.ID = 1
			cfg.Mode = "off"
			cfg.BoostType = "multiply"
			cfg.BoostValue = 2.0
			cfg.TempHours = 2
			cfg.RecurringStartTime = "02:00"
			cfg.RecurringEndTime = "08:00"
			return cfg, nil
		}
		return nil, err
	}

	if tempExp.Valid {
		cfg.TempExpiresAt = &tempExp.Time
	}
	cfg.RecurringEnabled = recEnabled == 1
	cfg.DisconnectUsers = disUsers == 1
	cfg.IsCurrentlyBoosted = isBoosted == 1

	return cfg, nil
}

func (d *DB) SaveSpeedControlConfig(cfg *models.SpeedControlConfig) error {
	var tempExp interface{}
	if cfg.TempExpiresAt != nil {
		tempExp = *cfg.TempExpiresAt
	}

	recVal := 0
	if cfg.RecurringEnabled {
		recVal = 1
	}
	disVal := 0
	if cfg.DisconnectUsers {
		disVal = 1
	}
	boostedVal := 0
	if cfg.IsCurrentlyBoosted {
		boostedVal = 1
	}

	_, err := d.Exec(`UPDATE speed_control_config SET
		mode = ?, boost_type = ?, boost_value = ?, temp_hours = ?, temp_expires_at = ?,
		recurring_enabled = ?, recurring_start_time = ?, recurring_end_time = ?,
		disconnect_users = ?, is_currently_boosted = ?
		WHERE id = 1`,
		cfg.Mode, cfg.BoostType, cfg.BoostValue, cfg.TempHours, tempExp,
		recVal, cfg.RecurringStartTime, cfg.RecurringEndTime,
		disVal, boostedVal,
	)
	return err
}

func (d *DB) SyncProfileBaseSpeeds() error {
	// Sync existing profiles into profile_base_speeds if not present
	rows, err := d.Query("SELECT id, name, display_name, rate_limit FROM profiles")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var name, dispName, rate string
		if err := rows.Scan(&id, &name, &dispName, &rate); err == nil {
			_, _ = d.Exec(`INSERT IGNORE INTO profile_base_speeds (profile_id, profile_name, base_rate_limit)
				VALUES (?, ?, ?)`, id, name, rate)
		}
	}
	return nil
}

func (d *DB) GetProfileBaseSpeeds() ([]models.ProfileBaseSpeed, error) {
	_ = d.SyncProfileBaseSpeeds()

	rows, err := d.Query(`SELECT p.id, p.name, p.display_name, COALESCE(pbs.base_rate_limit, p.rate_limit),
		COALESCE(pbs.boosted_rate_limit, ''), p.rate_limit
		FROM profiles p
		LEFT JOIN profile_base_speeds pbs ON p.id = pbs.profile_id
		ORDER BY p.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.ProfileBaseSpeed
	for rows.Next() {
		var item models.ProfileBaseSpeed
		if err := rows.Scan(&item.ProfileID, &item.ProfileName, &item.DisplayName, &item.BaseRateLimit, &item.BoostedRateLimit, &item.CurrentRateLimit); err == nil {
			list = append(list, item)
		}
	}
	return list, nil
}

func (d *DB) ApplySpeedBoost(boostType string, boostVal float64, disconnectUsers bool) error {
	_ = d.SyncProfileBaseSpeeds()

	speeds, err := d.GetProfileBaseSpeeds()
	if err != nil {
		return err
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range speeds {
		base := p.BaseRateLimit
		if base == "" {
			base = p.CurrentRateLimit
		}
		boosted := speedutil.BoostRateLimit(base, boostType, boostVal)
		if boosted == "" {
			continue
		}

		// Update profile
		_, _ = tx.Exec("UPDATE profiles SET rate_limit = ? WHERE id = ?", boosted, p.ProfileID)
		// Update radgroupreply
		_, _ = tx.Exec("UPDATE radgroupreply SET value = ? WHERE groupname = ? AND attribute = 'Mikrotik-Rate-Limit'", boosted, p.ProfileName)
		// Update profile_base_speeds
		_, _ = tx.Exec("UPDATE profile_base_speeds SET boosted_rate_limit = ? WHERE profile_id = ?", boosted, p.ProfileID)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if disconnectUsers {
		go func() {
			_, _ = d.DisconnectAllActiveSessions()
		}()
	}

	return nil
}

func (d *DB) RestoreBaseSpeeds(disconnectUsers bool) error {
	_ = d.SyncProfileBaseSpeeds()

	speeds, err := d.GetProfileBaseSpeeds()
	if err != nil {
		return err
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range speeds {
		base := p.BaseRateLimit
		if base == "" {
			continue
		}

		// Restore profile
		_, _ = tx.Exec("UPDATE profiles SET rate_limit = ? WHERE id = ?", base, p.ProfileID)
		// Restore radgroupreply
		_, _ = tx.Exec("UPDATE radgroupreply SET value = ? WHERE groupname = ? AND attribute = 'Mikrotik-Rate-Limit'", base, p.ProfileName)
		// Clear boosted in tracking
		_, _ = tx.Exec("UPDATE profile_base_speeds SET boosted_rate_limit = '' WHERE profile_id = ?", p.ProfileID)
	}

	// Update config
	_, _ = tx.Exec("UPDATE speed_control_config SET is_currently_boosted = 0, mode = 'off', temp_expires_at = NULL WHERE id = 1")

	if err := tx.Commit(); err != nil {
		return err
	}

	if disconnectUsers {
		go func() {
			_, _ = d.DisconnectAllActiveSessions()
		}()
	}

	return nil
}

func (d *DB) DisconnectAllActiveSessions() (int, error) {
	rows, err := d.Query(`SELECT acctsessionid, username, nasipaddress, framedipaddress
		FROM radacct WHERE acctstoptime IS NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type sess struct {
		sessionID string
		username  string
		nasIP     string
		framedIP  string
	}
	var sessions []sess
	for rows.Next() {
		var s sess
		_ = rows.Scan(&s.sessionID, &s.username, &s.nasIP, &s.framedIP)
		sessions = append(sessions, s)
	}

	count := 0
	for _, s := range sessions {
		var secret string
		var coaPort int
		_ = d.QueryRow("SELECT secret, coa_port FROM nas WHERE nasname = ?", s.nasIP).Scan(&secret, &coaPort)
		if secret == "" {
			secret = "radius123"
		}
		if coaPort <= 0 {
			coaPort = 3799
		}

		var fIP net.IP
		if s.framedIP != "" {
			fIP = net.ParseIP(s.framedIP)
		}

		_, _ = radius.SendDisconnectRequest(s.nasIP, coaPort, []byte(secret), s.username, s.sessionID, fIP)
		_, _ = d.Exec(`UPDATE radacct SET acctstoptime = CURRENT_TIMESTAMP, acctterminatecause = 'Admin-Reset'
			WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`, s.sessionID, s.nasIP)
		count++
	}

	return count, nil
}

func parseTimeHM(hm string) int {
	parts := strings.Split(strings.TrimSpace(hm), ":")
	if len(parts) != 2 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return h*60 + m
}

func (d *DB) CheckSpeedControlSchedule() error {
	cfg, err := d.GetSpeedControlConfig()
	if err != nil || cfg == nil {
		return err
	}

	now := time.Now()

	// 1. Check Temporary Boost expiration
	if cfg.Mode == "temp" && cfg.IsCurrentlyBoosted && cfg.TempExpiresAt != nil {
		if now.After(*cfg.TempExpiresAt) {
			_ = d.RestoreBaseSpeeds(cfg.DisconnectUsers)
			cfg.IsCurrentlyBoosted = false
			cfg.Mode = "off"
			cfg.TempExpiresAt = nil
			_ = d.SaveSpeedControlConfig(cfg)
			return nil
		}
	}

	// 2. Check Daily Recurring Schedule
	if cfg.RecurringEnabled {
		curMin := now.Hour()*60 + now.Minute()
		startMin := parseTimeHM(cfg.RecurringStartTime)
		endMin := parseTimeHM(cfg.RecurringEndTime)

		var inWindow bool
		if startMin < endMin {
			inWindow = curMin >= startMin && curMin < endMin
		} else if startMin > endMin {
			// Crosses midnight (e.g. 23:00 to 07:00)
			inWindow = curMin >= startMin || curMin < endMin
		}

		if inWindow && !cfg.IsCurrentlyBoosted {
			_ = d.ApplySpeedBoost(cfg.BoostType, cfg.BoostValue, cfg.DisconnectUsers)
			cfg.IsCurrentlyBoosted = true
			cfg.Mode = "recurring"
			_ = d.SaveSpeedControlConfig(cfg)
		} else if !inWindow && cfg.IsCurrentlyBoosted && cfg.Mode == "recurring" {
			_ = d.RestoreBaseSpeeds(cfg.DisconnectUsers)
			cfg.IsCurrentlyBoosted = false
			_ = d.SaveSpeedControlConfig(cfg)
		}
	}

	return nil
}

// ============================================================================
// Cafe Hotspot Cards Management (منظومة كروت الكافيهات على الراديوس)
// ============================================================================

func FormatBytesHuman(b int64) string {
	if b <= 0 {
		return "0 MB"
	}
	const GB = 1024 * 1024 * 1024
	const MB = 1024 * 1024
	const KB = 1024

	if b >= GB {
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	} else if b >= MB {
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	} else if b >= KB {
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	}
	return fmt.Sprintf("%d B", b)
}

func FormatDurationHuman(sec int64) string {
	if sec <= 0 {
		return "0 دقيقة"
	}
	hours := sec / 3600
	mins := (sec % 3600) / 60
	secs := sec % 60
	if hours > 0 {
		return fmt.Sprintf("%d ساعة و %d دقيقة", hours, mins)
	}
	if mins > 0 {
		return fmt.Sprintf("%d دقيقة و %d ثانية", mins, secs)
	}
	return fmt.Sprintf("%d ثانية", secs)
}

func (d *DB) GetCafeRouters() ([]models.NAS, error) {
	rows, err := d.Query(`SELECT id, nasname, shortname, type, secret, description, coa_port,
		COALESCE(phone, ''), COALESCE(is_cafe, 0), created_at
		FROM nas ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.NAS
	for rows.Next() {
		var n models.NAS
		var isCafeInt int
		if err := rows.Scan(&n.ID, &n.NASName, &n.ShortName, &n.Type, &n.Secret, &n.Description,
			&n.CoAPort, &n.Phone, &isCafeInt, &n.CreatedAt); err == nil {
			n.IsCafe = isCafeInt == 1
			list = append(list, n)
		}
	}
	return list, nil
}

func (d *DB) GetActiveCafeBatch(nasID int64) (*models.Batch, error) {
	query := `SELECT b.id, b.batch_code, COALESCE(b.batch_name, ''), b.profile_id, COALESCE(p.display_name, ''),
		b.count, COALESCE(b.cost_price_per_card, 0.0), b.price_per_card, COALESCE(b.total_cost_price, 0.0), b.total_price,
		b.created_at, COALESCE(b.nas_id, 0), COALESCE(n.shortname, 'الفرع الرئيسي')
		FROM batches b
		LEFT JOIN profiles p ON b.profile_id = p.id
		LEFT JOIN nas n ON b.nas_id = n.id
		WHERE b.card_type = 'cafe'`

	var args []interface{}
	if nasID > 0 {
		query += " AND b.nas_id = ?"
		args = append(args, nasID)
	}
	query += " ORDER BY b.id DESC LIMIT 1"

	row := d.QueryRow(query, args...)
	var b models.Batch
	err := row.Scan(&b.ID, &b.BatchCode, &b.BatchName, &b.ProfileID, &b.ProfileName, &b.Count,
		&b.CostPricePerCard, &b.PricePerCard, &b.TotalCostPrice, &b.TotalPrice, &b.CreatedAt, &b.NASID, &b.NASName)
	if err != nil {
		return nil, err
	}
	b.TotalProfit = b.TotalPrice - b.TotalCostPrice

	// Compute available, used, and currently online cards
	_ = d.QueryRow("SELECT COUNT(*) FROM cards WHERE batch_id = ? AND first_login_at IS NULL", b.ID).Scan(&b.AvailableCount)
	_ = d.QueryRow("SELECT COUNT(*) FROM cards WHERE batch_id = ? AND first_login_at IS NOT NULL", b.ID).Scan(&b.UsedCount)

	_ = d.QueryRow(`SELECT COUNT(DISTINCT r.username)
		FROM radacct r
		JOIN cards c ON r.username = c.username
		WHERE c.batch_id = ? AND r.acctstoptime IS NULL`, b.ID).Scan(&b.OnlineCount)

	return &b, nil
}

func (d *DB) GetCafeBatches(nasID int64) ([]models.Batch, error) {
	query := `SELECT b.id, b.batch_code, COALESCE(b.batch_name, ''), b.profile_id, COALESCE(p.display_name, ''),
		b.count, COALESCE(b.cost_price_per_card, 0.0), b.price_per_card, COALESCE(b.total_cost_price, 0.0), b.total_price,
		b.created_at, COALESCE(b.nas_id, 0), COALESCE(n.shortname, 'الفرع الرئيسي')
		FROM batches b
		LEFT JOIN profiles p ON b.profile_id = p.id
		LEFT JOIN nas n ON b.nas_id = n.id
		WHERE b.card_type = 'cafe'`

	var args []interface{}
	if nasID > 0 {
		query += " AND b.nas_id = ?"
		args = append(args, nasID)
	}
	query += " ORDER BY b.id DESC LIMIT 50"

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Batch
	for rows.Next() {
		var b models.Batch
		if err := rows.Scan(&b.ID, &b.BatchCode, &b.BatchName, &b.ProfileID, &b.ProfileName, &b.Count,
			&b.CostPricePerCard, &b.PricePerCard, &b.TotalCostPrice, &b.TotalPrice, &b.CreatedAt, &b.NASID, &b.NASName); err == nil {
			b.TotalProfit = b.TotalPrice - b.TotalCostPrice
			_ = d.QueryRow("SELECT COUNT(*) FROM cards WHERE batch_id = ? AND first_login_at IS NULL", b.ID).Scan(&b.AvailableCount)
			_ = d.QueryRow("SELECT COUNT(*) FROM cards WHERE batch_id = ? AND first_login_at IS NOT NULL", b.ID).Scan(&b.UsedCount)
			list = append(list, b)
		}
	}
	return list, nil
}

func (d *DB) GenerateCafeBatch(count int, prefix string, charLen int, profileID int64, nasID int64, pricePerCard float64) (string, int, error) {
	return d.GenerateAdvancedBatch(models.GenerateBatchOptions{
		Count:                count,
		AffixType:            "prefix",
		AffixValue:           prefix,
		CharLen:              charLen,
		ProfileID:            profileID,
		NASID:                nasID,
		PricePerCard:         pricePerCard,
		CardType:             "cafe",
		StopOnQuota:          true,
		SimultaneousSessions: 1,
	})
}

func (d *DB) InspectCard(code string) (*models.CafeCardTracker, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("يرجى إدخال كود الكارت")
	}

	// 1. Fetch card details
	var c models.Card
	var profileName, profileDisp, rateLimit string
	var quotaBytes int64
	var batchID int64
	var batchCode string
	var nasID int64
	var nasName string

	query := `SELECT c.id, c.username, c.password, c.price, c.status, c.first_login_at, c.expired_at,
		c.batch_id, COALESCE(b.batch_code, ''), COALESCE(c.nas_id, 0), COALESCE(n.shortname, 'عام / غير مقيد'),
		p.name, p.display_name, p.rate_limit, p.quota_bytes
		FROM cards c
		LEFT JOIN batches b ON c.batch_id = b.id
		LEFT JOIN nas n ON c.nas_id = n.id
		LEFT JOIN profiles p ON c.profile_id = p.id
		WHERE c.username = ? LIMIT 1`

	row := d.QueryRow(query, code)
	err := row.Scan(&c.ID, &c.Username, &c.Password, &c.Price, &c.Status, &c.FirstLoginAt, &c.ExpiredAt,
		&batchID, &batchCode, &nasID, &nasName,
		&profileName, &profileDisp, &rateLimit, &quotaBytes)
	if err != nil {
		return nil, fmt.Errorf("الكارت '%s' غير مسجل في النظام", code)
	}

	tracker := &models.CafeCardTracker{
		CardCode:       c.Username,
		Password:       c.Password,
		BatchID:        batchID,
		BatchCode:      batchCode,
		ProfileName:    profileName,
		ProfileDisplay: profileDisp,
		RateLimit:      rateLimit,
		QuotaBytes:     quotaBytes,
		Price:          c.Price,
		NASID:          nasID,
		NASName:        nasName,
	}

	if quotaBytes > 0 {
		tracker.QuotaDisplay = FormatBytesHuman(quotaBytes)
	} else {
		tracker.QuotaDisplay = "غير محدود (Open ∞)"
	}

	// 2. Query Accounting consumption from radacct
	var sumIn, sumOut, sumTime sql.NullInt64
	var minStart sql.NullTime
	var lastMAC, lastIP sql.NullString

	_ = d.QueryRow(`SELECT SUM(acctinputoctets), SUM(acctoutputoctets), SUM(acctsessiontime),
		MIN(acctstarttime), MAX(callingstationid), MAX(framedipaddress)
		FROM radacct WHERE username = ?`, code).Scan(&sumIn, &sumOut, &sumTime, &minStart, &lastMAC, &lastIP)

	if sumIn.Valid {
		tracker.BytesIn = sumIn.Int64
	}
	if sumOut.Valid {
		tracker.BytesOut = sumOut.Int64
	}
	if sumTime.Valid {
		tracker.SessionTimeSec = sumTime.Int64
	}
	tracker.TotalBytes = tracker.BytesIn + tracker.BytesOut
	tracker.TotalBytesDisplay = FormatBytesHuman(tracker.TotalBytes)
	tracker.BytesInDisplay = FormatBytesHuman(tracker.BytesIn)
	tracker.BytesOutDisplay = FormatBytesHuman(tracker.BytesOut)
	tracker.UptimeDisplay = FormatDurationHuman(tracker.SessionTimeSec)

	if lastMAC.Valid && lastMAC.String != "" {
		tracker.CallingMAC = lastMAC.String
	}
	if lastIP.Valid && lastIP.String != "" {
		tracker.FramedIP = lastIP.String
	}

	if minStart.Valid {
		tracker.FirstLogin = minStart.Time.Format("2006-01-02 15:04:05")
	} else if c.FirstLoginAt.Valid {
		tracker.FirstLogin = c.FirstLoginAt.Time.Format("2006-01-02 15:04:05")
	}

	// 3. Check if currently online
	var activeSessionID, activeNASIP, activeFramedIP sql.NullString
	_ = d.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress
		FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, code).Scan(
		&activeSessionID, &activeNASIP, &activeFramedIP)

	if activeSessionID.Valid && activeSessionID.String != "" {
		tracker.IsOnline = true
		tracker.SessionID = activeSessionID.String
		tracker.NASIP = activeNASIP.String
		if activeFramedIP.Valid && activeFramedIP.String != "" {
			tracker.FramedIP = activeFramedIP.String
		}
	}

	// 4. Quota remaining and percentage
	if quotaBytes > 0 {
		rem := quotaBytes - tracker.TotalBytes
		if rem < 0 {
			rem = 0
		}
		tracker.RemainingBytes = rem
		tracker.RemainingDisplay = FormatBytesHuman(rem)
		tracker.UsagePercent = (float64(tracker.TotalBytes) / float64(quotaBytes)) * 100.0
		if tracker.UsagePercent > 100.0 {
			tracker.UsagePercent = 100.0
		}
	} else {
		tracker.RemainingDisplay = "مفتوح ∞"
		tracker.UsagePercent = 0.0
	}

	// 5. Diagnostics & Status
	if tracker.IsOnline {
		tracker.Status = "online"
		tracker.StatusLabel = "متصل الآن (Online)"
		tracker.StatusBadgeClass = "bg-success text-white"
		tracker.ExpireReason = "الكارت يعمل ويتصل بالشبكة حالياً بشكل ممتاز."
	} else if tracker.TotalBytes == 0 && tracker.FirstLogin == "" {
		tracker.Status = "available"
		tracker.StatusLabel = "متاح للبيع (لم يبدأ بعد)"
		tracker.StatusBadgeClass = "bg-light text-secondary border"
		tracker.ExpireReason = "الكارت جديد وجاهز للبيع، لم يسجل العميل الدخول به حتى الآن."
	} else if quotaBytes > 0 && tracker.TotalBytes >= quotaBytes {
		tracker.Status = "expired"
		tracker.StatusLabel = "منتهي الكوتة (Expired Quota)"
		tracker.StatusBadgeClass = "bg-danger text-white"
		tracker.ExpireReason = "انتهت كوتة التحميل المخصصة للكارت بالكامل وتم إيقاف الخدمة."
	} else {
		tracker.Status = "used"
		tracker.StatusLabel = "مستهلك / غير متصل"
		tracker.StatusBadgeClass = "bg-secondary text-white"
		tracker.ExpireReason = "الكارت مستخدم وغير متصل حالياً (قد تكون مدة الصلاحية أو الجلسة انتهت)."
	}

	return tracker, nil
}

func (d *DB) DisconnectCard(code string) (bool, error) {
	var sessionID, nasIP, framedIPStr sql.NullString
	err := d.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress
		FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, code).Scan(
		&sessionID, &nasIP, &framedIPStr)
	if err != nil || !sessionID.Valid {
		return false, fmt.Errorf("الكارت غير متصل أونلاين حالياً")
	}

	var secret string
	var coaPort int
	_ = d.QueryRow("SELECT secret, coa_port FROM nas WHERE nasname = ?", nasIP.String).Scan(&secret, &coaPort)
	if secret == "" {
		secret = "radius123"
	}
	if coaPort <= 0 {
		coaPort = 3799
	}

	var framedIP net.IP
	if framedIPStr.Valid && framedIPStr.String != "" {
		framedIP = net.ParseIP(framedIPStr.String)
	}

	_, _ = radius.SendDisconnectRequest(nasIP.String, coaPort, []byte(secret), code, sessionID.String, framedIP)
	_, _ = d.Exec(`UPDATE radacct SET acctstoptime = CURRENT_TIMESTAMP, acctterminatecause = 'Admin-Reset'
		WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`, sessionID.String, nasIP.String)

	return true, nil
}

// StartCardSyncDaemon handles real-time synchronization between active accounting sessions (radacct)
// and cards table: tracking first logins, binding & locking to client MAC address, and auto-disabling expired cards.
func (d *DB) StartCardSyncDaemon() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			d.syncActiveCards()
		}
	}()
}

func (d *DB) syncActiveCards() {
	// 1. MAC Binding & Locking: For cards with bind_mac=1 or lock_mac=1 that have not captured calling_mac yet
	rowsMAC, err := d.Query(`SELECT c.id, c.username, c.lock_mac, r.callingstationid
		FROM cards c
		JOIN radacct r ON c.username = r.username
		WHERE (c.bind_mac = 1 OR c.lock_mac = 1) AND (c.calling_mac = '' OR c.calling_mac IS NULL)
		  AND r.callingstationid != ''
		GROUP BY c.id, c.username, c.lock_mac, r.callingstationid`)
	if err == nil {
		type macEntry struct {
			cardID   int64
			username string
			lockMAC  bool
			mac      string
		}
		var list []macEntry
		for rowsMAC.Next() {
			var me macEntry
			if err := rowsMAC.Scan(&me.cardID, &me.username, &me.lockMAC, &me.mac); err == nil {
				list = append(list, me)
			}
		}
		rowsMAC.Close()

		for _, item := range list {
			_, _ = d.Exec("UPDATE cards SET calling_mac = ? WHERE id = ?", item.mac, item.cardID)
			if item.lockMAC {
				// Lock FreeRADIUS strictly to this MAC address
				_, _ = d.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Calling-Station-Id'", item.username)
				_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Calling-Station-Id', '==', ?)", item.username, item.mac)
			}
		}
	}

	// 2. First Login Tracking for Calendar/First-Login time calculation mode
	rowsLogin, err := d.Query(`SELECT c.id, c.username, c.time_limit_seconds, c.time_calc_mode, MIN(r.acctstarttime)
		FROM cards c
		JOIN radacct r ON c.username = r.username
		WHERE c.first_login_at IS NULL
		GROUP BY c.id, c.username, c.time_limit_seconds, c.time_calc_mode`)
	if err == nil {
		type loginEntry struct {
			cardID     int64
			username   string
			timeLimit  int64
			calcMode   string
			firstLogin time.Time
		}
		var logins []loginEntry
		for rowsLogin.Next() {
			var le loginEntry
			if err := rowsLogin.Scan(&le.cardID, &le.username, &le.timeLimit, &le.calcMode, &le.firstLogin); err == nil {
				logins = append(logins, le)
			}
		}
		rowsLogin.Close()

		for _, le := range logins {
			if le.calcMode == "first_login" && le.timeLimit > 0 {
				expDate := le.firstLogin.Add(time.Duration(le.timeLimit) * time.Second)
				_, _ = d.Exec("UPDATE cards SET first_login_at = ?, expired_at = ? WHERE id = ?", le.firstLogin, expDate, le.cardID)
				// Set Expiration attribute in FreeRADIUS radcheck
				expStr := expDate.Format("02 Jan 2006 15:04:05")
				_, _ = d.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", le.username)
				_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", le.username, expStr)
			} else {
				_, _ = d.Exec("UPDATE cards SET first_login_at = ? WHERE id = ?", le.firstLogin, le.cardID)
			}
		}
	}

	// 3. Auto-Expiry Enforcement for Quota & Time Limits
	// Check calendar-expired cards
	rowsExp, err := d.Query(`SELECT c.id, c.username FROM cards c
		WHERE c.status = 'available' AND c.expired_at IS NOT NULL AND c.expired_at < CURRENT_TIMESTAMP`)
	if err == nil {
		var expCards []string
		var expIDs []int64
		for rowsExp.Next() {
			var id int64
			var u string
			if err := rowsExp.Scan(&id, &u); err == nil {
				expCards = append(expCards, u)
				expIDs = append(expIDs, id)
			}
		}
		rowsExp.Close()

		for i, u := range expCards {
			_, _ = d.Exec("UPDATE cards SET status = 'expired' WHERE id = ?", expIDs[i])
			_, _ = d.Exec("DELETE FROM radcheck WHERE username = ?", u)
			_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Auth-Type', ':=', 'Reject')", u)
			_, _ = d.DisconnectCard(u)
		}
	}

	// Check quota-exhausted cards
	rowsQuota, err := d.Query(`SELECT c.id, c.username, c.quota_bytes, COALESCE(SUM(r.acctinputoctets + r.acctoutputoctets), 0)
		FROM cards c
		JOIN radacct r ON c.username = r.username
		WHERE c.status = 'available' AND c.stop_on_quota = 1 AND c.quota_bytes > 0
		GROUP BY c.id, c.username, c.quota_bytes
		HAVING SUM(r.acctinputoctets + r.acctoutputoctets) >= c.quota_bytes`)
	if err == nil {
		var quotaCards []string
		var quotaIDs []int64
		for rowsQuota.Next() {
			var id int64
			var u string
			var limit, used int64
			if err := rowsQuota.Scan(&id, &u, &limit, &used); err == nil {
				quotaCards = append(quotaCards, u)
				quotaIDs = append(quotaIDs, id)
			}
		}
		rowsQuota.Close()

		for i, u := range quotaCards {
			_, _ = d.Exec("UPDATE cards SET status = 'expired' WHERE id = ?", quotaIDs[i])
			_, _ = d.Exec("DELETE FROM radcheck WHERE username = ?", u)
			_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Auth-Type', ':=', 'Reject')", u)
			_, _ = d.DisconnectCard(u)
		}
	}
}





