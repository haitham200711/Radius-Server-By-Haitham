package database

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/mikrotik"
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
			debt DECIMAL(12, 2) DEFAULT 0.00,
			commission_percent DECIMAL(5, 2) DEFAULT 0.00,
			permissions LONGTEXT,
			notes TEXT,
			is_active TINYINT(1) DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 3. Profiles (ط¨ط§ظ‚ط§طھ ط§ظ„ط®ط¯ظ…ط§طھ)
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

		// 4. Batches (ط¯ظپط¹ط§طھ ط§ظ„ظƒط±ظˆطھ)
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

		// 5. Cards (ظƒط±ظˆطھ ط§ظ„ط´ط­ظ†)
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

		// 6. Card Templates (طھطµط§ظ…ظٹظ… ط§ظ„ط·ط¨ط§ط¹ط©)
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
			api_port INT DEFAULT 8728,
			api_user VARCHAR(64) DEFAULT 'admin',
			api_password VARCHAR(128) DEFAULT '',
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
		// 8. Subscribers CRM Table (ط§ظ„ط¹ظ…ظ„ط§ط، ظˆط§ظ„ط¹ظ‚ظˆط¯)
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

		// 11. Billing Transactions & Debt Logs (ط³ط¬ظ„ ط§ظ„ط¹ظ…ظ„ظٹط§طھ ط§ظ„ظ…ط§ظ„ظٹط© ظˆط§ظ„ط¯ظٹظˆظ†)
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

		// 12. SSTP One-Time Script Tokens (ط±ظˆط§ط¨ط· طھط«ط¨ظٹطھ ط§ظ„ظ…ط§ظٹظƒط±ظˆطھظƒ ظ„ظ…ط±ط© ظˆط§ط­ط¯ط©)
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

		// 13. SSTP Static Routes (طھظˆط¬ظٹظ‡ ط§ظ„ط´ط¨ظƒط§طھ ط§ظ„ط«ط§ط¨طھ ظ„ظ„ظپط±ظˆط¹)
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

		// 14. WhatsApp Settings (ط¥ط¹ط¯ط§ط¯ط§طھ ظˆط¥ط´ط¹ط§ط±ط§طھ ط§ظ„ظˆط§طھط³ط§ط¨)
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

		// 15. WhatsApp Logs (ط³ط¬ظ„ ط±ط³ط§ط¦ظ„ ط§ظ„ظˆط§طھط³ط§ط¨ ط§ظ„ظ…ط±ط³ظ„ط©)
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

		// 16. WhatsApp Message Templates (ظ‚ظˆط§ظ„ط¨ ط±ط³ط§ط¦ظ„ ظˆط¥ط´ط¹ط§ط±ط§طھ ط§ظ„ظˆط§طھط³ط§ط¨)
		`CREATE TABLE IF NOT EXISTS whatsapp_templates (
			id INT AUTO_INCREMENT PRIMARY KEY,
			event_key VARCHAR(64) UNIQUE NOT NULL,
			event_name VARCHAR(128) NOT NULL,
			enabled TINYINT(1) DEFAULT 1,
			template_text TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_wa_tmpl_key (event_key)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 17. Speed Control Configuration (ط§ظ„طھط­ظƒظ… ط¨ط§ظ„ط³ط±ط¹ط§طھ ظˆظ…ط¶ط§ط¹ظپط© ط§ظ„ط¨ط§ظ‚ط§طھ)
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

		// 18. Profile Base Speeds (سجل السرعات الأساسية للباقات)
		`CREATE TABLE IF NOT EXISTS profile_base_speeds (
			profile_id INT PRIMARY KEY,
			profile_name VARCHAR(64) NOT NULL,
			base_rate_limit VARCHAR(64) NOT NULL,
			boosted_rate_limit VARCHAR(64) DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 19. Reseller Transactions (سجل حركات ومعاملات الموزعين والوكلاء)
		`CREATE TABLE IF NOT EXISTS reseller_transactions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			reseller_id INT NOT NULL,
			type VARCHAR(32) NOT NULL,
			amount DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
			balance_before DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
			balance_after DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
			debt_before DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
			debt_after DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
			subscriber_id INT DEFAULT 0,
			subscriber_name VARCHAR(100) DEFAULT '',
			notes TEXT,
			created_by VARCHAR(64) DEFAULT 'admin',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_res_tx_reseller (reseller_id),
			INDEX idx_res_tx_type (type),
			INDEX idx_res_tx_date (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 20. Backup Settings (إعدادات النسخ الاحتياطي التلقائي)
		`CREATE TABLE IF NOT EXISTS backup_settings (
			id INT PRIMARY KEY DEFAULT 1,
			enabled TINYINT(1) DEFAULT 1,
			interval_hours INT DEFAULT 6,
			retention_count INT DEFAULT 10,
			send_whatsapp TINYINT(1) DEFAULT 0,
			whatsapp_phone VARCHAR(50) DEFAULT '',
			last_backup_at DATETIME NULL,
			last_status VARCHAR(50) DEFAULT 'ready',
			last_error TEXT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 21. Backups (سجل ملفات النسخ الاحتياطي)
		`CREATE TABLE IF NOT EXISTS backups (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			filename VARCHAR(255) NOT NULL,
			filepath VARCHAR(500) NOT NULL,
			size_bytes BIGINT DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status VARCHAR(50) DEFAULT 'success',
			whatsapp_sent TINYINT(1) DEFAULT 0,
			error_message TEXT NULL,
			INDEX idx_backups_created (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		// 18. Subscriber Self-Care Portal Settings & Permissions
		`CREATE TABLE IF NOT EXISTS subscriber_portal_settings (
			id INT PRIMARY KEY DEFAULT 1,
			portal_enabled TINYINT(1) DEFAULT 1,
			allow_loan_request TINYINT(1) DEFAULT 1,
			show_quota_usage TINYINT(1) DEFAULT 1,
			show_debt TINYINT(1) DEFAULT 1,
			show_profile_details TINYINT(1) DEFAULT 1,
			show_online_status TINYINT(1) DEFAULT 1,
			allow_password_change TINYINT(1) DEFAULT 0,
			allow_session_disconnect TINYINT(1) DEFAULT 1,
			company_name VARCHAR(150) DEFAULT 'شبكتنا لخدمات الإنترنت',
			company_phone VARCHAR(50) DEFAULT '',
			portal_announcement TEXT,
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
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS header_title VARCHAR(150) DEFAULT 'ظƒط§ط±طھ ط¥ظ†طھط±ظ†طھ';`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS footer_text VARCHAR(255) DEFAULT 'ظ†طھظ…ظ†ظ‰ ظ„ظƒظ… ظˆظ‚طھط§ظ‹ ظ…ظ…طھط¹ط§ظ‹';`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS theme_color VARCHAR(30) DEFAULT '#0d6efd';`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS html_content MEDIUMTEXT;`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS css_content TEXT;`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS bg_image TEXT;`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS card_width VARCHAR(20) DEFAULT '240px';`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS card_height VARCHAR(20) DEFAULT '140px';`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS cols INT DEFAULT 3;`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS ` + "`rows`" + ` INT DEFAULT 7;`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS card_gap VARCHAR(20) DEFAULT '2mm';`,
		`ALTER TABLE card_templates ADD COLUMN IF NOT EXISTS overlay_config LONGTEXT;`,
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
		`ALTER TABLE nas ADD COLUMN IF NOT EXISTS coa_port INT DEFAULT 3799;`,
		`ALTER TABLE nas ADD COLUMN IF NOT EXISTS api_port INT DEFAULT 8728;`,
		`ALTER TABLE nas ADD COLUMN IF NOT EXISTS api_user VARCHAR(64) DEFAULT 'admin';`,
		`ALTER TABLE nas ADD COLUMN IF NOT EXISTS api_password VARCHAR(128) DEFAULT '';`,
		`ALTER TABLE nas ADD COLUMN IF NOT EXISTS created_at DATETIME DEFAULT CURRENT_TIMESTAMP;`,
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
		`ALTER TABLE batches ADD COLUMN IF NOT EXISTS convert_mac_after_session TINYINT(1) DEFAULT 0;`,
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
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS convert_mac_after_session TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS original_code VARCHAR(64) DEFAULT '';`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS is_mac_converted TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS service_name VARCHAR(100) DEFAULT '';`,
		`ALTER TABLE cards ADD COLUMN IF NOT EXISTS calling_mac VARCHAR(32) DEFAULT '';`,
		`UPDATE cards SET original_code = username WHERE original_code = '' OR original_code IS NULL;`,
		`ALTER TABLE resellers ADD COLUMN IF NOT EXISTS debt DECIMAL(12, 2) DEFAULT 0.00;`,
		`ALTER TABLE resellers ADD COLUMN IF NOT EXISTS commission_percent DECIMAL(5, 2) DEFAULT 0.00;`,
		`ALTER TABLE resellers ADD COLUMN IF NOT EXISTS permissions LONGTEXT;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS reseller_id INT DEFAULT 0;`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS loan_enabled TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS loan_hours INT DEFAULT 48;`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS loan_quota_bytes BIGINT DEFAULT 0;`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS loan_deduction_type VARCHAR(20) DEFAULT 'full';`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_active TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_granted_at DATETIME NULL;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_hours INT DEFAULT 0;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_quota_bytes BIGINT DEFAULT 0;`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_deduction_type VARCHAR(20) DEFAULT 'full';`,
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_initial_used_bytes BIGINT DEFAULT 0;`,
		`ALTER TABLE subscriber_portal_settings ADD COLUMN IF NOT EXISTS allow_phone_login TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE subscriber_portal_settings ADD COLUMN IF NOT EXISTS allow_ip_auto_login TINYINT(1) DEFAULT 0;`,
		`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS portal_enabled TINYINT(1) DEFAULT 1;`,
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
			name: "ط§ظ†طھظ‡ط§ط، ظˆظ‚طھ ظˆط§ط´طھط±ط§ظƒ ط§ظ„ط¹ظ…ظٹظ„",
			text: "ط¹ط²ظٹط²ظٹ ط§ظ„ظ…ط´طھط±ظƒ {name}طŒ ظ†ظˆط¯ ط¥ط¹ظ„ط§ظ…ظƒظ… ط¨ط£ظ† ط§ط´طھط±ط§ظƒظƒظ… ط±ظ‚ظ… ({contract_no}) ظ‚ط¯ ط§ظ†طھظ‡ظ‰ ط¨طھط§ط±ظٹط® {expiry_date}. ظٹط±ط¬ظ‰ ط³ط±ط¹ط© ط§ظ„طھط¬ط¯ظٹط¯ ظ„ط¶ظ…ط§ظ† ط§ط³طھظ…ط±ط§ط± ط§ظ„ط®ط¯ظ…ط©.",
		},
		{
			key:  "quota_50",
			name: "ط§ط³طھظ‡ظ„ط§ظƒ 50% ظ…ظ† ظƒظˆطھط© ط§ظ„ط¹ظ…ظٹظ„",
			text: "ظ…ط±ط­ط¨ط§ظ‹ {name}طŒ ظ„ظ‚ط¯ طھظ… ط§ط³طھظ‡ظ„ط§ظƒ 50% ظ…ظ† ظƒظˆطھط© ط§ظ„ط¥ظ†طھط±ظ†طھ ط§ظ„ط®ط§طµط© ط¨ظƒ ({quota_used} ط¬ظٹط¬ط§ ظ…ظ† ط¥ط¬ظ…ط§ظ„ظٹ {quota_total} ط¬ظٹط¬ط§). ط§ظ„ظ…طھط¨ظ‚ظٹ: {quota_remaining} ط¬ظٹط¬ط§.",
		},
		{
			key:  "quota_75",
			name: "ط§ط³طھظ‡ظ„ط§ظƒ 75% ظ…ظ† ظƒظˆطھط© ط§ظ„ط¹ظ…ظٹظ„",
			text: "طھظ†ط¨ظٹظ‡: ظ…ط±ط­ط¨ط§ظ‹ {name}طŒ ظ„ظ‚ط¯ ط§ط³طھظ‡ظ„ظƒطھ 75% ظ…ظ† ظƒظˆطھط© ط§ظ„ط¥ظ†طھط±ظ†طھ. ط§ظ„ظ…طھط¨ظ‚ظٹ ظ„ط¯ظٹظƒ: {quota_remaining} ط¬ظٹط¬ط§. طھط§ط±ظٹط® ط§ظ„ط§ظ†طھظ‡ط§ط،: {expiry_date}.",
		},
		{
			key:  "quota_100",
			name: "ط§ط³طھظ‡ظ„ط§ظƒ 100% ظ…ظ† ظƒظˆطھط© ط§ظ„ط¹ظ…ظٹظ„",
			text: "طھظ†ط¨ظٹظ‡ ظ‡ط§ظ…: ظ…ط±ط­ط¨ط§ظ‹ {name}طŒ ظ„ظ‚ط¯ طھظ… ط§ط³طھظ‡ظ„ط§ظƒ 100% ظ…ظ† ظƒظˆطھط© ط§ظ„ط¥ظ†طھط±ظ†طھ ط¨ط§ظ„ظƒط§ظ…ظ„. ظٹط±ط¬ظ‰ ط´ط­ظ† ظƒظˆطھط© ط¥ط¶ط§ظپظٹط© ط£ظˆ طھط¬ط¯ظٹط¯ ط§ظ„ط¨ط§ظ‚ط© ظ„ط§ط³طھط¹ط§ط¯ط© ط§ظ„ط³ط±ط¹ط© ط§ظ„ظƒط§ظ…ظ„ط©.",
		},
		{
			key:  "renew",
			name: "طھط¬ط¯ظٹط¯ ط§ط´طھط±ط§ظƒ ط§ظ„ط¹ظ…ظٹظ„",
			text: "ظ…ط±ط­ط¨ط§ظ‹ {name}طŒ طھظ… طھط¬ط¯ظٹط¯ ط§ط´طھط±ط§ظƒظƒظ… ط¨ظ†ط¬ط§ط­ ط¹ظ„ظ‰ ط¨ط§ظ‚ط© ({profile}). طھط§ط±ظٹط® ط§ظ„ط§ظ†طھظ‡ط§ط، ط§ظ„ط¬ط¯ظٹط¯: {expiry_date}. ط´ظƒط±ط§ظ‹ ظ„ط«ظ‚طھظƒظ… ط¨ظ†ط§.",
		},
		{
			key:  "debt_added",
			name: "طھط³ط¬ظٹظ„ ط£ظˆ ط¥ط¶ط§ظپط© ظ…ط¯ظٹظˆظ†ظٹط© ط¬ط¯ظٹط¯ط©",
			text: "ظ…ط±ط­ط¨ط§ظ‹ {name}طŒ طھظ… طھط³ط¬ظٹظ„ ظ…ط¯ظٹظˆظ†ظٹط© ط¹ظ„ظ‰ ط­ط³ط§ط¨ظƒظ… ط¨ظ…ط¨ظ„ط؛ {amount} ط¬.ظ…. ط¥ط¬ظ…ط§ظ„ظٹ ط§ظ„ط¯ظٹظ† ط§ظ„ظ…ط³طھط­ظ‚ ط§ظ„ط­ط§ظ„ظٹ: {debt} ط¬.ظ….",
		},
		{
			key:  "debt_paid",
			name: "ط³ط¯ط§ط¯ ط¯ظپط¹ط© ظ…ظ† ط§ظ„ط¯ظٹظ†",
			text: "ظ…ط±ط­ط¨ط§ظ‹ {name}طŒ طھظ… ط§ط³طھظ„ط§ظ… ط³ط¯ط§ط¯ ط¨ظ…ط¨ظ„ط؛ {amount} ط¬.ظ… ط¨ظ†ط¬ط§ط­. ط§ظ„ظ…طھط¨ظ‚ظٹ ظ…ظ† ط§ظ„ط¯ظٹظ†: {debt} ط¬.ظ…. ط´ظƒط±ط§ظ‹ ظ„طھط¹ط§ظ…ظ„ظƒظ… ط§ظ„ط±ط§ظ‚ظٹ ظ…ط¹ظ†ط§.",
		},
		{
			key:  "debt_reminder",
			name: "التذكير الدوري بالديون المستحقة",
			text: "عزيزي المشترك {name}، نود تذكيركم بوجود مديونية مستحقة بقيمة {debt} ج.م على اشتراككم رقم ({contract_no}). يرجى سرعة السداد لتفادي توقف الخدمة.",
		},
		{
			key:  "loan_granted",
			name: "منح سلفة فترة سماح",
			text: "مرحباً {name}، تم منحك سلفة فترة سماح بنجاح! المدة الممنوحة: {loan_hours} ساعة، والكوتة: {loan_quota} جيجا. تنتهي السلفة بتاريخ {expiry_date}. يرجى التجديد قبل انتهاء المدة.",
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

	// Seed default Backup Settings
	_, _ = d.Exec(`INSERT IGNORE INTO backup_settings (id, enabled, interval_hours, retention_count, send_whatsapp, whatsapp_phone, last_status)
		VALUES (1, 1, 6, 10, 0, '', 'ready')`)

	// Seed default Subscriber Portal Settings
	_, _ = d.Exec(`INSERT IGNORE INTO subscriber_portal_settings (
		id, portal_enabled, allow_loan_request, show_quota_usage, show_debt,
		show_profile_details, show_online_status, allow_password_change, allow_session_disconnect,
		company_name, company_phone, portal_announcement
	) VALUES (1, 1, 1, 1, 1, 1, 1, 0, 1, 'شبكتنا لخدمات الإنترنت', '', '')`)

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
			       ('HS_1GB_1D', 'Hotspot 1GB / ظٹظˆظ…ظٹ', '2M/4M', 'hs-pool', 1073741824, 86400, 5.00);`)

		// Also populate FreeRADIUS radgroupreply for these profiles
		_, _ = d.Exec(`INSERT INTO radgroupreply (groupname, attribute, op, value)
			VALUES ('5M_UNLIMITED', 'Mikrotik-Rate-Limit', ':=', '2560k/5120k'),
			       ('10M_UNLIMITED', 'Mikrotik-Rate-Limit', ':=', '5120k/10240k'),
			       ('HS_1GB_1D', 'Mikrotik-Rate-Limit', ':=', '2M/4M');`)
	}

	// Seed default Card Template
	var tmplCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM card_templates WHERE html_content IS NOT NULL AND html_content != ''").Scan(&tmplCount)
	if tmplCount == 0 {
		defaultHtml := `<div class="card-voucher voucher-modern" style="border: 2px dashed #0d6efd; border-radius: 10px; padding: 12px; background: #fff; text-align: center; position: relative;">
    <div style="font-weight: 700; font-size: 0.95rem; color: #1e3c72; border-bottom: 1px solid #e9ecef; padding-bottom: 6px; margin-bottom: 8px; display: flex; justify-content: space-between; align-items: center;">
        <span><i class="bi bi-wifi text-primary"></i> {{.HeaderTitle}}</span>
        <span class="badge bg-light text-secondary border font-monospace">{{.Profile}}</span>
    </div>
    <div style="font-size: 0.8rem; color: #6c757d; margin-bottom: 2px;">ظƒظˆط¯ طھط³ط¬ظٹظ„ ط§ظ„ط¯ط®ظˆظ„:</div>
    <div style="font-size: 1.25rem; font-weight: 800; letter-spacing: 2px; color: #212529; background: #f8f9fa; padding: 6px; border-radius: 6px; margin: 6px 0; font-family: monospace; border: 1px solid #dee2e6;">{{.Code}}</div>
    <div style="font-size: 0.75rem; color: #6c757d; display: flex; justify-content: space-between; margin-top: 6px;">
        <span>ط§ظ„ظپط±ط¹: {{.RouterName}}</span>
        <span class="font-monospace text-primary">#{{.BatchID}}</span>
    </div>
    <div style="font-size: 0.7rem; color: #adb5bd; margin-top: 4px;">{{.FooterText}}</div>
</div>`

		luxuryHtml := `<div class="card-voucher voucher-luxury" style="border: 2px solid #d4af37; border-radius: 12px; padding: 12px; background: linear-gradient(135deg, #1c1c1c 0%, #2c2518 100%); color: #fff; text-align: center; position: relative;">
    <div style="font-weight: 700; font-size: 0.95rem; color: #ffd700; border-bottom: 1px solid #4a3e20; padding-bottom: 6px; margin-bottom: 8px; display: flex; justify-content: space-between; align-items: center;">
        <span>âک• {{.HeaderTitle}}</span>
        <span style="background: #d4af37; color: #000; font-weight: bold; border-radius: 4px; padding: 2px 6px; font-size: 0.75rem;">{{.Profile}}</span>
    </div>
    <div style="font-size: 0.8rem; color: #cccccc; margin-bottom: 2px;">ط±ظ…ط² ط§ظ„ط¯ط®ظˆظ„ ط§ظ„ط³ط±ظٹط¹:</div>
    <div style="font-size: 1.25rem; font-weight: 800; letter-spacing: 2px; color: #ffd700; background: rgba(0,0,0,0.4); padding: 6px; border-radius: 6px; margin: 6px 0; font-family: monospace; border: 1px solid #d4af37;">{{.Code}}</div>
    <div style="font-size: 0.75rem; color: #aaa; display: flex; justify-content: space-between; margin-top: 6px;">
        <span>{{.RouterName}}</span>
        <span style="color: #ffd700;">#{{.BatchID}}</span>
    </div>
    <div style="font-size: 0.7rem; color: #888; margin-top: 4px;">{{.FooterText}}</div>
</div>`

		thermalHtml := `<div class="card-voucher voucher-thermal" style="border: 1px dashed #000; border-radius: 4px; padding: 10px; background: #fff; color: #000; text-align: center; font-family: monospace;">
    <div style="font-weight: 900; font-size: 1rem; border-bottom: 1px dashed #000; padding-bottom: 4px; margin-bottom: 6px;">
        *** {{.HeaderTitle}} ***
    </div>
    <div style="font-size: 0.85rem; margin: 3px 0;">ط§ظ„ط³ط±ط¹ط©: <strong>{{.Profile}}</strong></div>
    <div style="font-size: 1.35rem; font-weight: 900; letter-spacing: 3px; border: 2px solid #000; padding: 6px; margin: 6px 0; background: #fafafa;">{{.Code}}</div>
    <div style="font-size: 0.75rem; border-top: 1px dashed #000; padding-top: 4px; margin-top: 6px;">
        ظپط±ط¹: {{.RouterName}} | ط¯ظپط¹ط© #{{.BatchID}}
    </div>
    <div style="font-size: 0.7rem; margin-top: 2px;">{{.FooterText}}</div>
</div>`

		miniHtml := `<div class="card-voucher voucher-mini" style="border: 1px solid #198754; border-radius: 8px; padding: 8px; background: #f0fff4; text-align: center;">
    <div style="font-weight: 700; font-size: 0.85rem; color: #198754; display: flex; justify-content: space-between;">
        <span>ًں“¶ {{.HeaderTitle}}</span>
        <span style="background: #198754; color: #fff; border-radius: 4px; padding: 1px 4px; font-size: 0.7rem;">{{.Profile}}</span>
    </div>
    <div style="font-size: 1.15rem; font-weight: 800; letter-spacing: 1.5px; color: #145a32; background: #fff; padding: 4px; border-radius: 4px; margin: 4px 0; font-family: monospace; border: 1px solid #a3e4d7;">{{.Code}}</div>
    <div style="font-size: 0.7rem; color: #555; display: flex; justify-content: space-between;">
        <span>{{.RouterName}}</span>
        <span class="text-success fw-bold">#{{.BatchID}}</span>
    </div>
</div>`

		_, _ = d.Exec(`INSERT INTO card_templates (name, is_default, header_title, footer_text, theme_color, html_content, card_width, card_height, cols, ` + "`rows`" + `, card_gap) VALUES
			('ط§ظ„ظ‚ط§ظ„ط¨ ط§ظ„ط£ط²ط±ظ‚ ط§ظ„ط­ط¯ظٹط« (ط§ظپطھط±ط§ط¶ظٹ)', 1, 'ظƒط§ط±طھ ط¥ظ†طھط±ظ†طھ', 'ط£ظ‡ظ„ط§ظ‹ ط¨ظƒظ… - ظ†طھظ…ظ†ظ‰ ظ„ظƒظ… ظˆظ‚طھط§ظ‹ ظ…ظ…طھط¹ط§ظ‹', '#0d6efd', ?, '240px', '140px', 3, 7, '2mm'),
			('ظ‚ط§ظ„ط¨ ظƒط§ظپظٹظ‡ ظپط®ظ… (ط°ظ‡ط¨ظٹ ط¯ط§ظƒظ†)', 0, 'VIP Wi-Fi Voucher', 'Welcome to our cafe - Enjoy high speed internet', '#d4af37', ?, '240px', '140px', 3, 7, '2mm'),
			('ظ‚ط§ظ„ط¨ ط±ظˆظ„ ظƒط§ط´ظٹط± (ط­ط±ط§ط±ظٹ POS)', 0, 'ط¥ظٹطµط§ظ„ ط´ط¨ظƒط© ظˆط§ظٹ ظپط§ظٹ', 'ظٹط±ط¬ظ‰ ط§ظ„ط§ط­طھظپط§ط¸ ط¨ط§ظ„ظƒظˆط¯ ظ„ط­ظٹظ† ط§ظ†طھظ‡ط§ط، ط§ظ„ط¬ظ„ط³ط©', '#212529', ?, '240px', '140px', 3, 7, '2mm'),
			('ظ‚ط§ظ„ط¨ ظ…ظٹظ†ظٹ ط§ظ‚طھطµط§ط¯ظٹ (ط£ط®ط¶ط± ط®ظپظٹظپ)', 0, 'ظƒط§ط±طھ ط¥ظ†طھط±ظ†طھ ط³ط±ظٹط¹', 'ط´ظƒط±ط§ظ‹ ظ„ط²ظٹط§ط±طھظƒظ…', '#198754', ?, '240px', '140px', 3, 7, '2mm')
			ON DUPLICATE KEY UPDATE html_content = VALUES(html_content)`, defaultHtml, luxuryHtml, thermalHtml, miniHtml)
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
	query := `INSERT INTO subscribers (username, contract_no, full_name, phone, address, debt, profile_name, static_ip, calling_mac, quota_bytes, expiration_date, status, reseller_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE contract_no = VALUES(contract_no), full_name = VALUES(full_name), phone = VALUES(phone),
		address = VALUES(address), debt = VALUES(debt), profile_name = VALUES(profile_name), static_ip = VALUES(static_ip),
		calling_mac = VALUES(calling_mac), quota_bytes = VALUES(quota_bytes), expiration_date = VALUES(expiration_date), status = VALUES(status), reseller_id = VALUES(reseller_id)`

	var expDate interface{}
	if sub.ExpirationDate.Valid {
		expDate = sub.ExpirationDate.Time
	}

	_, err = tx.Exec(query, sub.Username, sub.ContractNo, sub.FullName, sub.Phone, sub.Address, sub.Debt,
		sub.ProfileName, sub.StaticIP, sub.CallingMAC, sub.QuotaBytes, expDate, sub.Status, sub.ResellerID)
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
		opts.BatchName = fmt.Sprintf("ط¯ظپط¹ط© %s", batchCode)
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
		simultaneous_sessions, stop_on_quota, bind_mac, lock_mac, convert_mac_after_session, service_name, affix_type, affix_value, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		batchCode, opts.BatchName, opts.ProfileID, opts.ResellerID, opts.Count, opts.CostPricePerCard, opts.PricePerCard,
		totalCostPrice, totalPrice, opts.CardType, opts.NASID, opts.QuotaBytes, opts.TimeLimitSeconds, opts.TimeCalcMode,
		opts.SimultaneousSessions, opts.StopOnQuota, opts.BindMAC, opts.LockMAC, opts.ConvertMACAfterSession, opts.ServiceName, opts.AffixType, opts.AffixValue)
	if err != nil {
		return "", 0, err
	}
	batchID, _ := res.LastInsertId()

	// 2. Prepare bulk insert statements
	stmtCard, err := tx.Prepare(`INSERT INTO cards (
		batch_id, username, password, profile_id, reseller_id, status, cost_price, price,
		card_type, nas_id, quota_bytes, time_limit_seconds, time_calc_mode,
		simultaneous_sessions, stop_on_quota, bind_mac, lock_mac, convert_mac_after_session, original_code, is_mac_converted, service_name, created_at
	) VALUES (?, ?, ?, ?, ?, 'available', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`)
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
			opts.SimultaneousSessions, opts.StopOnQuota, opts.BindMAC, opts.LockMAC, opts.ConvertMACAfterSession, u, 0, opts.ServiceName); err == nil {

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

// --- Reseller Management & RBAC Methods ---

func (d *DB) GetResellers() ([]models.Reseller, error) {
	query := `SELECT 
		r.id, r.name, r.username, r.phone, r.balance, r.credit_limit, 
		COALESCE(r.debt, 0.00), COALESCE(r.commission_percent, 0.00), 
		COALESCE(r.permissions, ''), r.notes, r.is_active, r.created_at,
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id) AS subs_count
	FROM resellers r
	ORDER BY r.id DESC`

	rows, err := d.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resellers []models.Reseller
	for rows.Next() {
		var res models.Reseller
		var permsJSON string
		if err := rows.Scan(
			&res.ID, &res.Name, &res.Username, &res.Phone, &res.Balance, &res.CreditLimit,
			&res.Debt, &res.CommissionPercent, &permsJSON, &res.Notes, &res.IsActive, &res.CreatedAt,
			&res.SubscribersCount,
		); err == nil {
			res.PermissionsJSON = permsJSON
			res.Permissions = models.ParseResellerPermissions(permsJSON)
			resellers = append(resellers, res)
		}
	}
	return resellers, nil
}

func (d *DB) GetReseller(id int64) (*models.Reseller, error) {
	query := `SELECT 
		r.id, r.name, r.username, r.phone, r.balance, r.credit_limit, 
		COALESCE(r.debt, 0.00), COALESCE(r.commission_percent, 0.00), 
		COALESCE(r.permissions, ''), r.notes, r.is_active, r.created_at,
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id) AS subs_count
	FROM resellers r
	WHERE r.id = ?`

	var res models.Reseller
	var permsJSON string
	err := d.QueryRow(query, id).Scan(
		&res.ID, &res.Name, &res.Username, &res.Phone, &res.Balance, &res.CreditLimit,
		&res.Debt, &res.CommissionPercent, &permsJSON, &res.Notes, &res.IsActive, &res.CreatedAt,
		&res.SubscribersCount,
	)
	if err != nil {
		return nil, err
	}
	res.PermissionsJSON = permsJSON
	res.Permissions = models.ParseResellerPermissions(permsJSON)
	return &res, nil
}

func (d *DB) GetResellerByUsername(username string) (*models.Reseller, error) {
	query := `SELECT 
		r.id, r.name, r.username, r.phone, r.balance, r.credit_limit, 
		COALESCE(r.debt, 0.00), COALESCE(r.commission_percent, 0.00), 
		COALESCE(r.permissions, ''), r.notes, r.is_active, r.created_at,
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id) AS subs_count
	FROM resellers r
	WHERE r.username = ?`

	var res models.Reseller
	var permsJSON string
	err := d.QueryRow(query, username).Scan(
		&res.ID, &res.Name, &res.Username, &res.Phone, &res.Balance, &res.CreditLimit,
		&res.Debt, &res.CommissionPercent, &permsJSON, &res.Notes, &res.IsActive, &res.CreatedAt,
		&res.SubscribersCount,
	)
	if err != nil {
		return nil, err
	}
	res.PermissionsJSON = permsJSON
	res.Permissions = models.ParseResellerPermissions(permsJSON)
	return &res, nil
}

func (d *DB) AddReseller(res *models.Reseller, password string) (int64, error) {
	if err := res.SetPassword(password); err != nil {
		return 0, err
	}
	permsJSON := res.Permissions.ToJSON()
	res.PermissionsJSON = permsJSON

	result, err := d.Exec(`INSERT INTO resellers 
		(name, username, password_hash, phone, balance, credit_limit, debt, commission_percent, permissions, notes, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		res.Name, res.Username, res.PasswordHash, res.Phone, res.Balance, res.CreditLimit,
		res.Debt, res.CommissionPercent, permsJSON, res.Notes, res.IsActive)
	if err != nil {
		return 0, err
	}

	id, _ := result.LastInsertId()
	res.ID = id

	if res.Balance > 0 {
		_, _ = d.Exec(`INSERT INTO reseller_transactions (reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, notes, created_by)
			VALUES (?, 'deposit', ?, 0, ?, 0, ?, 'رصيد افتتاحي عند إنشاء الحساب', 'admin')`,
			id, res.Balance, res.Balance, res.Debt)
	}

	return id, nil
}

func (d *DB) UpdateReseller(res *models.Reseller, password string) error {
	if password != "" {
		if err := res.SetPassword(password); err != nil {
			return err
		}
		_, err := d.Exec(`UPDATE resellers SET 
			name = ?, username = ?, password_hash = ?, phone = ?, credit_limit = ?, 
			commission_percent = ?, notes = ?, is_active = ?
			WHERE id = ?`,
			res.Name, res.Username, res.PasswordHash, res.Phone, res.CreditLimit,
			res.CommissionPercent, res.Notes, res.IsActive, res.ID)
		return err
	}

	_, err := d.Exec(`UPDATE resellers SET 
		name = ?, username = ?, phone = ?, credit_limit = ?, 
		commission_percent = ?, notes = ?, is_active = ?
		WHERE id = ?`,
		res.Name, res.Username, res.Phone, res.CreditLimit,
		res.CommissionPercent, res.Notes, res.IsActive, res.ID)
	return err
}

func (d *DB) UpdateResellerPermissions(resellerID int64, perms models.ResellerPermissions) error {
	permsJSON := perms.ToJSON()
	_, err := d.Exec("UPDATE resellers SET permissions = ? WHERE id = ?", permsJSON, resellerID)
	return err
}

func (d *DB) ToggleResellerStatus(id int64) error {
	_, err := d.Exec("UPDATE resellers SET is_active = NOT is_active WHERE id = ?", id)
	return err
}

func (d *DB) DeleteReseller(id int64) error {
	_, _ = d.Exec("UPDATE subscribers SET reseller_id = 0 WHERE reseller_id = ?", id)
	_, err := d.Exec("DELETE FROM resellers WHERE id = ?", id)
	return err
}

func (d *DB) AdjustResellerBalance(resellerID int64, actionType string, amount float64, notes, adminUser string) error {
	if amount <= 0 {
		return fmt.Errorf("المبلغ يجب أن يكون أكبر من صفر")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var balance, debt float64
	var resName string
	err = tx.QueryRow("SELECT name, balance, COALESCE(debt, 0.00) FROM resellers WHERE id = ? FOR UPDATE", resellerID).Scan(&resName, &balance, &debt)
	if err != nil {
		return fmt.Errorf("الموزع غير موجود: %w", err)
	}

	newBalance := balance
	newDebt := debt

	switch actionType {
	case "deposit":
		newBalance = balance + amount
	case "withdraw":
		newBalance = balance - amount
	case "pay_debt":
		newDebt = debt - amount
		if newDebt < 0 {
			newDebt = 0
		}
	case "pay_debt_from_balance":
		newBalance = balance - amount
		newDebt = debt - amount
		if newDebt < 0 {
			newDebt = 0
		}
	case "add_debt":
		newDebt = debt + amount
	default:
		return fmt.Errorf("نوع العملية غير صالح: %s", actionType)
	}

	_, err = tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newBalance, newDebt, resellerID)
	if err != nil {
		return err
	}

	if adminUser == "" {
		adminUser = "admin"
	}
	if notes == "" {
		switch actionType {
		case "deposit":
			notes = fmt.Sprintf("شحن رصيد للموزع %s بقيمة %.2f ج.م", resName, amount)
		case "withdraw":
			notes = fmt.Sprintf("خصم / سحب رصيد من الموزع %s بقيمة %.2f ج.م", resName, amount)
		case "pay_debt":
			notes = fmt.Sprintf("سداد مديونية نقداً للموزع %s بقيمة %.2f ج.م", resName, amount)
		case "pay_debt_from_balance":
			notes = fmt.Sprintf("سداد مديونية من رصيد المحفظة للموزع %s بقيمة %.2f ج.م", resName, amount)
		case "add_debt":
			notes = fmt.Sprintf("إضافة مديونية على الموزع %s بقيمة %.2f ج.م", resName, amount)
		}
	}

	_, err = tx.Exec(`INSERT INTO reseller_transactions 
		(reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, notes, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resellerID, actionType, amount, balance, newBalance, debt, newDebt, notes, adminUser)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (d *DB) GetResellerTransactions(resellerID int64, limit int) ([]models.ResellerTransaction, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `SELECT 
		t.id, t.reseller_id, COALESCE(r.name, ''), t.type, t.amount,
		t.balance_before, t.balance_after, t.debt_before, t.debt_after,
		t.subscriber_id, COALESCE(t.subscriber_name, ''),
		t.notes, t.created_by, t.created_at
	FROM reseller_transactions t
	LEFT JOIN resellers r ON t.reseller_id = r.id`

	var args []interface{}
	if resellerID > 0 {
		query += " WHERE t.reseller_id = ?"
		args = append(args, resellerID)
	}
	query += " ORDER BY t.id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txs []models.ResellerTransaction
	for rows.Next() {
		var t models.ResellerTransaction
		if err := rows.Scan(
			&t.ID, &t.ResellerID, &t.ResellerName, &t.Type, &t.Amount,
			&t.BalanceBefore, &t.BalanceAfter, &t.DebtBefore, &t.DebtAfter,
			&t.SubscriberID, &t.SubscriberName, &t.Notes, &t.CreatedBy, &t.CreatedAt,
		); err == nil {
			txs = append(txs, t)
		}
	}
	return txs, nil
}

func (d *DB) GetResellerFinancialSummary() (map[string]interface{}, error) {
	summary := make(map[string]interface{})

	var totalResellers, activeResellers, totalSubs int
	var totalBalance, totalDebt float64

	_ = d.QueryRow("SELECT COUNT(*), COALESCE(SUM(balance), 0.00), COALESCE(SUM(debt), 0.00) FROM resellers").Scan(&totalResellers, &totalBalance, &totalDebt)
	_ = d.QueryRow("SELECT COUNT(*) FROM resellers WHERE is_active = 1").Scan(&activeResellers)
	_ = d.QueryRow("SELECT COUNT(*) FROM subscribers WHERE reseller_id > 0").Scan(&totalSubs)

	var todayTxCount int
	var todayTxAmount float64
	_ = d.QueryRow("SELECT COUNT(*), COALESCE(SUM(amount), 0.00) FROM reseller_transactions WHERE DATE(created_at) = CURDATE()").Scan(&todayTxCount, &todayTxAmount)

	summary["TotalResellers"] = totalResellers
	summary["ActiveResellers"] = activeResellers
	summary["TotalBalance"] = totalBalance
	summary["TotalDebt"] = totalDebt
	summary["TotalSubscribers"] = totalSubs
	summary["TodayTxCount"] = todayTxCount
	summary["TodayTxAmount"] = todayTxAmount

	return summary, nil
}

func (d *DB) ResellerRenewSubscriber(resellerID int64, username, profileName string, isCustomerPaid bool, adminUser string) error {
	res, err := d.GetReseller(resellerID)
	if err != nil {
		return fmt.Errorf("الموزع غير موجود: %w", err)
	}
	if !res.IsActive {
		return fmt.Errorf("حساب الموزع معطل حالياً")
	}
	if !res.Permissions.CanRenew {
		return fmt.Errorf("ليس لديك صلاحية تجديد باقات المشتركين")
	}

	var subResellerID int64
	var subID int64
	var currentProf string
	var loanActive bool
	err = d.QueryRow("SELECT id, reseller_id, profile_name, COALESCE(loan_active, 0) FROM subscribers WHERE username = ?", username).Scan(&subID, &subResellerID, &currentProf, &loanActive)
	if err != nil {
		return fmt.Errorf("المشترك غير موجود")
	}
	if subResellerID != resellerID {
		return fmt.Errorf("هذا المشترك غير مسجل تحت حسابك كموزع")
	}

	if profileName == "" {
		profileName = currentProf
	}

	if loanActive && profileName != currentProf {
		return fmt.Errorf("لا يمكن تغيير باقة المشترك لوجود سلفة نشطة غير مسددة. يجب سداد السلفة أو تجديد نفس الباقة أولاً")
	}

	var prof models.Profile
	err = d.QueryRow(`SELECT id, name, display_name, cost_price, price, quota_bytes, validity_value, validity_unit FROM profiles WHERE name = ?`, profileName).Scan(
		&prof.ID, &prof.Name, &prof.DisplayName, &prof.CostPrice, &prof.Price, &prof.QuotaBytes, &prof.ValidityValue, &prof.ValidityUnit,
	)
	if err != nil {
		return fmt.Errorf("الباقة غير موجودة")
	}

	cost := prof.Price
	if res.CommissionPercent > 0 {
		cost = prof.Price * (1.0 - (res.CommissionPercent / 100.0))
	} else if prof.CostPrice > 0 {
		cost = prof.CostPrice
	}

	availablePower := res.Balance + res.CreditLimit
	if availablePower < cost {
		return fmt.Errorf("رصيدك غير كافٍ لتجديد الباقة. تكلفة الباقة: %.2f ج.م، رصيدك المتاح: %.2f ج.م", cost, availablePower)
	}

	err = d.RenewSubscriberBilling(username, profileName, prof.Price, isCustomerPaid, "reseller", fmt.Sprintf("تجديد عبر الموزع %s", res.Name), adminUser)
	if err != nil {
		return err
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	newBalance := res.Balance - cost
	_, err = tx.Exec("UPDATE resellers SET balance = ? WHERE id = ?", newBalance, resellerID)
	if err != nil {
		return err
	}

	notes := fmt.Sprintf("تجديد باقة %s للمشترك %s (سعر الجمهور %.2f - خصم الموزع)", prof.DisplayName, username, prof.Price)
	_, _ = tx.Exec(`INSERT INTO reseller_transactions (reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, subscriber_id, subscriber_name, notes, created_by)
		VALUES (?, 'renew', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resellerID, cost, res.Balance, newBalance, res.Debt, res.Debt, subID, username, notes, adminUser)

	return tx.Commit()
}

// --- Profile Methods ---

func (d *DB) GetProfiles() ([]models.Profile, error) {
	rows, err := d.Query(`SELECT id, name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds,
		validity_value, validity_unit, COALESCE(cost_price, 0.00), price, shared_users, mikrotik_address_list,
		on_quota_action, on_quota_profile, on_quota_pool,
		on_time_action, on_time_profile, on_time_pool,
		COALESCE(loan_enabled, 0), COALESCE(loan_hours, 48), COALESCE(loan_quota_bytes, 0), COALESCE(loan_deduction_type, 'full'),
		COALESCE(portal_enabled, 1),
		created_at
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
			&p.OnTimeAction, &p.OnTimeProfile, &p.OnTimePool,
			&p.LoanEnabled, &p.LoanHours, &p.LoanQuotaBytes, &p.LoanDeductionType,
			&p.PortalEnabled,
			&p.CreatedAt,
		)
		if err == nil {
			if p.QuotaBytes > 0 {
				p.QuotaGB = float64(p.QuotaBytes) / (1024 * 1024 * 1024)
			}
			if p.LoanQuotaBytes > 0 {
				p.LoanQuotaGB = float64(p.LoanQuotaBytes) / (1024 * 1024 * 1024)
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
		on_time_action, on_time_profile, on_time_pool,
		COALESCE(loan_enabled, 0), COALESCE(loan_hours, 48), COALESCE(loan_quota_bytes, 0), COALESCE(loan_deduction_type, 'full'),
		COALESCE(portal_enabled, 1),
		created_at
		FROM profiles WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds,
		&p.ValidityValue, &p.ValidityUnit, &p.CostPrice, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
		&p.OnQuotaAction, &p.OnQuotaProfile, &p.OnQuotaPool,
		&p.OnTimeAction, &p.OnTimeProfile, &p.OnTimePool,
		&p.LoanEnabled, &p.LoanHours, &p.LoanQuotaBytes, &p.LoanDeductionType,
		&p.PortalEnabled,
		&p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if p.QuotaBytes > 0 {
		p.QuotaGB = float64(p.QuotaBytes) / (1024 * 1024 * 1024)
	}
	if p.LoanQuotaBytes > 0 {
		p.LoanQuotaGB = float64(p.LoanQuotaBytes) / (1024 * 1024 * 1024)
	}
	return &p, nil
}

func (d *DB) GetProfileByName(name string) (*models.Profile, error) {
	var p models.Profile
	err := d.QueryRow(`SELECT id, name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds,
		validity_value, validity_unit, COALESCE(cost_price, 0.00), price, shared_users, mikrotik_address_list,
		on_quota_action, on_quota_profile, on_quota_pool,
		on_time_action, on_time_profile, on_time_pool,
		COALESCE(loan_enabled, 0), COALESCE(loan_hours, 48), COALESCE(loan_quota_bytes, 0), COALESCE(loan_deduction_type, 'full'),
		COALESCE(portal_enabled, 1),
		created_at
		FROM profiles WHERE name = ?`, name).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.RateLimit, &p.PoolName, &p.QuotaBytes, &p.UptimeSeconds,
		&p.ValidityValue, &p.ValidityUnit, &p.CostPrice, &p.Price, &p.SharedUsers, &p.MikrotikAddressList,
		&p.OnQuotaAction, &p.OnQuotaProfile, &p.OnQuotaPool,
		&p.OnTimeAction, &p.OnTimeProfile, &p.OnTimePool,
		&p.LoanEnabled, &p.LoanHours, &p.LoanQuotaBytes, &p.LoanDeductionType,
		&p.PortalEnabled,
		&p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if p.QuotaBytes > 0 {
		p.QuotaGB = float64(p.QuotaBytes) / (1024 * 1024 * 1024)
	}
	if p.LoanQuotaBytes > 0 {
		p.LoanQuotaGB = float64(p.LoanQuotaBytes) / (1024 * 1024 * 1024)
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
	if p.LoanHours <= 0 {
		p.LoanHours = 48
	}
	if p.LoanDeductionType == "" {
		p.LoanDeductionType = "full"
	}

	if p.ID == 0 {
		res, err := tx.Exec(`INSERT INTO profiles (name, display_name, rate_limit, pool_name, quota_bytes, uptime_seconds,
			validity_value, validity_unit, cost_price, price, shared_users, mikrotik_address_list,
			on_quota_action, on_quota_profile, on_quota_pool,
			on_time_action, on_time_profile, on_time_pool,
			loan_enabled, loan_hours, loan_quota_bytes, loan_deduction_type, portal_enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.Name, p.DisplayName, p.RateLimit, p.PoolName, p.QuotaBytes, p.UptimeSeconds,
			p.ValidityValue, p.ValidityUnit, p.CostPrice, p.Price, p.SharedUsers, p.MikrotikAddressList,
			p.OnQuotaAction, p.OnQuotaProfile, p.OnQuotaPool,
			p.OnTimeAction, p.OnTimeProfile, p.OnTimePool,
			p.LoanEnabled, p.LoanHours, p.LoanQuotaBytes, p.LoanDeductionType, p.PortalEnabled)
		if err != nil {
			return err
		}
		p.ID, _ = res.LastInsertId()
	} else {
		_, err := tx.Exec(`UPDATE profiles SET
			name = ?, display_name = ?, rate_limit = ?, pool_name = ?, quota_bytes = ?, uptime_seconds = ?,
			validity_value = ?, validity_unit = ?, cost_price = ?, price = ?, shared_users = ?, mikrotik_address_list = ?,
			on_quota_action = ?, on_quota_profile = ?, on_quota_pool = ?,
			on_time_action = ?, on_time_profile = ?, on_time_pool = ?,
			loan_enabled = ?, loan_hours = ?, loan_quota_bytes = ?, loan_deduction_type = ?, portal_enabled = ?
			WHERE id = ?`,
			p.Name, p.DisplayName, p.RateLimit, p.PoolName, p.QuotaBytes, p.UptimeSeconds,
			p.ValidityValue, p.ValidityUnit, p.CostPrice, p.Price, p.SharedUsers, p.MikrotikAddressList,
			p.OnQuotaAction, p.OnQuotaProfile, p.OnQuotaPool,
			p.OnTimeAction, p.OnTimeProfile, p.OnTimePool,
			p.LoanEnabled, p.LoanHours, p.LoanQuotaBytes, p.LoanDeductionType, p.PortalEnabled, p.ID)
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

	if err := tx.Commit(); err != nil {
		return err
	}

	// Apply new rate limit via CoA dynamically to all active subscribers on this profile without kicking them!
	if p.RateLimit != "" {
		go func() {
			_, _ = d.ApplyCoARateLimitToProfileActiveSessions(p.Name, p.RateLimit)
		}()
	}

	return nil
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

// CalculateLoanDeductionTx calculates time and quota to deduct when renewing an active loan
func (d *DB) CalculateLoanDeductionTx(tx *sql.Tx, username string, p *models.Profile) (time.Time, int64, float64, int64, bool) {
	var (
		loanActive           bool
		loanGrantedAt        sql.NullTime
		loanHours            int
		loanQuotaBytes       int64
		loanDeductionType    string
		loanInitialUsedBytes int64
	)

	_ = tx.QueryRow(`SELECT COALESCE(loan_active, 0), loan_granted_at, COALESCE(loan_hours, 0), 
		COALESCE(loan_quota_bytes, 0), COALESCE(loan_deduction_type, 'full'), COALESCE(loan_initial_used_bytes, 0)
		FROM subscribers WHERE username = ?`, username).Scan(
		&loanActive, &loanGrantedAt, &loanHours, &loanQuotaBytes, &loanDeductionType, &loanInitialUsedBytes)

	newExp := p.CalculateExpiration(time.Now())
	finalQuota := p.QuotaBytes

	if !loanActive {
		return newExp, finalQuota, 0, 0, false
	}

	var deductedHours float64 = 0
	var deductedQuotaBytes int64 = 0

	if loanDeductionType == "partial" {
		// 1. Partial time deduction: only elapsed time since loan grant
		if loanGrantedAt.Valid {
			elapsed := time.Since(loanGrantedAt.Time).Hours()
			if elapsed > float64(loanHours) {
				elapsed = float64(loanHours)
			}
			if elapsed < 0 {
				elapsed = 0
			}
			deductedHours = elapsed
		} else {
			deductedHours = float64(loanHours)
		}

		// 2. Partial quota deduction: only traffic consumed since loan grant
		if loanGrantedAt.Valid {
			var usedDuringLoan int64
			_ = tx.QueryRow(`SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0)
				FROM radacct WHERE username = ? AND acctstarttime >= ?`, username, loanGrantedAt.Time).Scan(&usedDuringLoan)
			if usedDuringLoan > loanQuotaBytes {
				usedDuringLoan = loanQuotaBytes
			}
			if usedDuringLoan < 0 {
				usedDuringLoan = 0
			}
			deductedQuotaBytes = usedDuringLoan
		} else {
			deductedQuotaBytes = loanQuotaBytes
		}
	} else {
		// Full deduction: deduct the entire loan hours and quota regardless of usage
		deductedHours = float64(loanHours)
		deductedQuotaBytes = loanQuotaBytes
	}

	// Apply time deduction
	newExp = newExp.Add(-time.Duration(deductedHours * float64(time.Hour)))

	// Apply quota deduction
	if finalQuota > 0 {
		finalQuota = finalQuota - deductedQuotaBytes
		if finalQuota < 0 {
			finalQuota = 0
		}
	}

	return newExp, finalQuota, deductedHours, deductedQuotaBytes, true
}

func (d *DB) RenewSubscriber(username, profileName string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// If profileName is empty, get from subscriber record
	var oldProfile string
	_ = tx.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&oldProfile)
	if profileName == "" {
		profileName = oldProfile
	}

	var p models.Profile
	err = tx.QueryRow(`SELECT id, name, quota_bytes, validity_value, validity_unit FROM profiles WHERE name = ?`, profileName).Scan(
		&p.ID, &p.Name, &p.QuotaBytes, &p.ValidityValue, &p.ValidityUnit,
	)
	if err != nil {
		return fmt.Errorf("profile %s not found: %w", profileName, err)
	}

	// Calculate new expiration and quota taking into account any active loan
	newExp, finalQuota, _, _, _ := d.CalculateLoanDeductionTx(tx, username, &p)

	// Update subscribers table and reset loan flags
	_, err = tx.Exec(`UPDATE subscribers SET
		profile_name = ?,
		quota_bytes = ?,
		expiration_date = ?,
		status = 'active',
		loan_active = 0,
		loan_granted_at = NULL,
		loan_hours = 0,
		loan_quota_bytes = 0,
		loan_deduction_type = 'full',
		loan_initial_used_bytes = 0
		WHERE username = ?`, p.Name, finalQuota, newExp, username)
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
	if finalQuota > 0 {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", finalQuota))
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if oldProfile != "" && oldProfile != p.Name {
		_ = d.DisconnectSubscriberSession(username)
	} else {
		_ = d.RestoreActiveSubscriberCoA(username)
	}
	return nil
}

// GrantSubscriberLoan grants a grace period loan (time and quota) to a subscriber based on profile settings
func (d *DB) GrantSubscriberLoan(username, adminUser string) (*models.Subscriber, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("اسم المستخدم مطلوب")
	}

	sub, err := d.GetSubscriber(username)
	if err != nil {
		return nil, fmt.Errorf("المشترك غير موجود: %w", err)
	}

	if sub.LoanActive {
		grantDate := "سابقاً"
		if sub.LoanGrantedAt.Valid {
			grantDate = sub.LoanGrantedAt.Time.Format("2006-01-02 15:04")
		}
		return nil, fmt.Errorf("المشترك لديه سلفة نشطة بالفعل تم منحها بتاريخ %s ولم يتم تسديدها بعد", grantDate)
	}

	if sub.ProfileName == "" {
		return nil, fmt.Errorf("المشترك ليس لديه باقة محددة")
	}

	prof, err := d.GetProfileByName(sub.ProfileName)
	if err != nil {
		return nil, fmt.Errorf("باقة المشترك غير موجودة: %w", err)
	}

	if !prof.LoanEnabled {
		return nil, fmt.Errorf("نظام السلفة غير مفعل لهذه الباقة (%s)", prof.DisplayName)
	}

	loanHours := prof.LoanHours
	if loanHours <= 0 {
		loanHours = 48
	}
	loanQuotaBytes := prof.LoanQuotaBytes
	loanDeductionType := prof.LoanDeductionType
	if loanDeductionType == "" {
		loanDeductionType = "full"
	}

	// Calculate initial used bytes from radacct so far as baseline for partial quota
	var currentUsedBytes int64
	_ = d.QueryRow("SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0) FROM radacct WHERE username = ?", username).Scan(&currentUsedBytes)

	if adminUser == "self-service" {
		nowCheck := time.Now()
		isTimeExpired := sub.ExpirationDate.Valid && nowCheck.After(sub.ExpirationDate.Time)
		isQuotaExpired := sub.QuotaBytes > 0 && currentUsedBytes >= sub.QuotaBytes
		if !isTimeExpired && !isQuotaExpired {
			return nil, fmt.Errorf("لا يمكن طلب سلفة إلا في حال انتهاء صلاحية الاشتراك أو نفاد الكوتة بالكامل")
		}
	}

	now := time.Now()
	newExp := now.Add(time.Duration(loanHours) * time.Hour)

	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Update subscribers table
	_, err = tx.Exec(`UPDATE subscribers SET
		loan_active = 1,
		loan_granted_at = ?,
		loan_hours = ?,
		loan_quota_bytes = ?,
		loan_deduction_type = ?,
		loan_initial_used_bytes = ?,
		expiration_date = ?,
		status = 'expired',
		updated_at = NOW()
		WHERE username = ?`,
		now, loanHours, loanQuotaBytes, loanDeductionType, currentUsedBytes, newExp, username)
	if err != nil {
		return nil, err
	}

	// Update FreeRADIUS radcheck Expiration
	expStr := newExp.Format("02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username)
	_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, expStr)

	// Update radreply Mikrotik-Total-Limit
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username)
	if loanQuotaBytes > 0 {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", loanQuotaBytes))
	}

	deductionText := "سداد كلي كامل"
	if loanDeductionType == "partial" {
		deductionText = "سداد جزئي حسب الاستهلاك"
	}
	desc := fmt.Sprintf("منح سلفة فترة سماح (%d ساعة - %.2f جيجا - %s)",
		loanHours, float64(loanQuotaBytes)/(1024*1024*1024), deductionText)
	if adminUser == "" {
		adminUser = "admin"
	}
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'loan', ?, 0.00, 'paid', 'loan', ?, ?)`,
		sub.Username, sub.ContractNo, sub.FullName, desc, desc, adminUser)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Restore active session dynamically via CoA without disconnecting
	_ = d.RestoreActiveSubscriberCoA(username)

	// Trigger WhatsApp notification asynchronously
	go func() {
		_ = d.TriggerWhatsAppEvent("loan_granted", sub, map[string]string{
			"{loan_hours}":  fmt.Sprintf("%d", loanHours),
			"{loan_quota}":  fmt.Sprintf("%.2f", float64(loanQuotaBytes)/(1024*1024*1024)),
			"{expiry_date}": newExp.Format("2006-01-02 15:04"),
		})
	}()

	sub.LoanActive = true
	sub.LoanGrantedAt = sql.NullTime{Time: now, Valid: true}
	sub.LoanHours = loanHours
	sub.LoanQuotaBytes = loanQuotaBytes
	sub.LoanDeductionType = loanDeductionType
	sub.ExpirationDate = sql.NullTime{Time: newExp, Valid: true}
	sub.Status = "expired"

	return sub, nil
}

// CloseSubscriberLoan terminates an active loan without full renewal, clearing loan status and cutting temporary access
func (d *DB) CloseSubscriberLoan(username, adminUser string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("اسم المستخدم مطلوب")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var contractNo, fullName string
	_ = tx.QueryRow("SELECT COALESCE(contract_no, ''), COALESCE(full_name, '') FROM subscribers WHERE username = ?", username).Scan(&contractNo, &fullName)
	if fullName == "" {
		fullName = username
	}

	// 1. Reset loan fields and set status to expired
	pastExp := time.Now().Add(-1 * time.Minute)
	_, err = tx.Exec(`UPDATE subscribers SET
		loan_active = 0,
		loan_granted_at = NULL,
		loan_hours = 0,
		loan_quota_bytes = 0,
		loan_deduction_type = 'full',
		loan_initial_used_bytes = 0,
		expiration_date = ?,
		status = 'expired',
		updated_at = NOW()
		WHERE username = ?`, pastExp, username)
	if err != nil {
		return err
	}

	// 2. Set FreeRADIUS radcheck Expiration to past so they cannot reconnect
	pastExpStr := pastExp.Format("02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username)
	_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, pastExpStr)

	// 3. Clear Mikrotik-Total-Limit
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username)

	// Log billing transaction note
	if adminUser == "" {
		adminUser = "admin"
	}
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'adjustment', 'إنهاء وإغلاق سلفة فترة السماح', 0.00, 'paid', 'cash', 'إلغاء السلفة يدوياً من لوحة التحكم', ?)`,
		username, contractNo, fullName, adminUser)

	if err := tx.Commit(); err != nil {
		return err
	}

	// 4. Disconnect user session via CoA
	_ = d.DisconnectSubscriberSession(username)

	return nil
}

// TerminateSubscription ends the subscriber's current subscription, zeroes quota, sets status to expired, and disconnects session
func (d *DB) TerminateSubscription(username, adminUser string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("اسم المستخدم مطلوب")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var contractNo, fullName string
	_ = tx.QueryRow("SELECT COALESCE(contract_no, ''), COALESCE(full_name, '') FROM subscribers WHERE username = ?", username).Scan(&contractNo, &fullName)
	if fullName == "" {
		fullName = username
	}

	pastExp := time.Now().Add(-1 * time.Minute)
	// Reset expiration date, clear quota bytes to 0, reset loan, set status expired
	_, err = tx.Exec(`UPDATE subscribers SET
		expiration_date = ?,
		quota_bytes = 0,
		status = 'expired',
		loan_active = 0,
		loan_granted_at = NULL,
		loan_hours = 0,
		loan_quota_bytes = 0,
		loan_deduction_type = 'full',
		loan_initial_used_bytes = 0,
		last_renewed_at = NOW(),
		updated_at = NOW()
		WHERE username = ?`, pastExp, username)
	if err != nil {
		return err
	}

	// Update FreeRADIUS radcheck Expiration to past
	pastExpStr := pastExp.Format("02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username)
	_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, pastExpStr)

	// Remove or zero out Mikrotik-Total-Limit in radreply
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username)

	if adminUser == "" {
		adminUser = "admin"
	}
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'adjustment', 'إنهاء اشتراك العميل وتصفير الكوتة', 0.00, 'paid', 'cash', 'تم إنهاء الاشتراك يدوياً من لوحة التحكم', ?)`,
		username, contractNo, fullName, adminUser)

	if err := tx.Commit(); err != nil {
		return err
	}

	// Disconnect session via CoA immediately
	_ = d.DisconnectSubscriberSession(username)

	return nil
}

// ChangeSubscriberProfile switches a subscriber to a new profile, terminates their current subscription, zeroes their quota, and disconnects session
func (d *DB) ChangeSubscriberProfile(username, newProfileName, adminUser string, isAdmin bool) error {
	username = strings.TrimSpace(username)
	newProfileName = strings.TrimSpace(newProfileName)
	if username == "" || newProfileName == "" {
		return fmt.Errorf("اسم المشترك واسم البروفايل مطلوبان")
	}

	// Verify profile exists
	prof, err := d.GetProfileByName(newProfileName)
	if err != nil || prof == nil {
		return fmt.Errorf("البروفايل المطلوب غير موجود: %s", newProfileName)
	}

	sub, err := d.GetSubscriber(username)
	if err != nil || sub == nil {
		return fmt.Errorf("المشترك غير موجود: %s", username)
	}

	// If subscriber is on active loan, only admin can change profile
	if sub.LoanActive && !isAdmin {
		return fmt.Errorf("لا يمكن تغيير باقة المشترك أثناء فترة السلفة النشطة إلا بواسطة مدير النظام (Admin)")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	pastExp := time.Now().Add(-1 * time.Minute)

	// Update subscriber: change profile, end subscription, zero out quota, set status to expired, clear loan
	_, err = tx.Exec(`UPDATE subscribers SET
		profile_name = ?,
		expiration_date = ?,
		quota_bytes = 0,
		status = 'expired',
		loan_active = 0,
		loan_granted_at = NULL,
		loan_hours = 0,
		loan_quota_bytes = 0,
		loan_deduction_type = 'full',
		loan_initial_used_bytes = 0,
		last_renewed_at = NOW(),
		updated_at = NOW()
		WHERE username = ?`, newProfileName, pastExp, username)
	if err != nil {
		return err
	}

	// Update FreeRADIUS radusergroup
	_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", username)
	_, _ = tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", username, newProfileName)

	// Update FreeRADIUS radcheck Expiration to past so they cannot reconnect without renewing
	pastExpStr := pastExp.Format("02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username)
	_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, pastExpStr)

	// Clear Mikrotik-Total-Limit
	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username)

	if adminUser == "" {
		adminUser = "admin"
	}
	desc := fmt.Sprintf("تحويل الباقة إلى (%s) مع إنهاء الاشتراك وتصفير الكوتة", prof.DisplayName)
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'adjustment', ?, 0.00, 'paid', 'cash', 'تحويل بروفايل من لوحة التحكم', ?)`,
		username, sub.ContractNo, sub.FullName, desc, adminUser)

	if err := tx.Commit(); err != nil {
		return err
	}

	// Disconnect session via CoA immediately
	_ = d.DisconnectSubscriberSession(username)

	return nil
}

// --- Subscriber CRM Edit Methods ---

func (d *DB) GetSubscriber(username string) (*models.Subscriber, error) {
	var s models.Subscriber
	var exp sql.NullTime
	var loanGranted sql.NullTime
	err := d.QueryRow(`SELECT s.id, s.username, s.contract_no, s.full_name, s.phone, s.address, s.debt,
		s.profile_name, s.static_ip, s.calling_mac, s.quota_bytes, s.expiration_date, s.status,
		COALESCE(s.reseller_id, 0), COALESCE(r.name, ''),
		COALESCE(s.loan_active, 0), s.loan_granted_at, COALESCE(s.loan_hours, 0), COALESCE(s.loan_quota_bytes, 0),
		COALESCE(s.loan_deduction_type, 'full'), COALESCE(s.loan_initial_used_bytes, 0),
		s.created_at, s.updated_at
		FROM subscribers s
		LEFT JOIN resellers r ON s.reseller_id = r.id
		WHERE s.username = ?`, username).Scan(
		&s.ID, &s.Username, &s.ContractNo, &s.FullName, &s.Phone, &s.Address, &s.Debt,
		&s.ProfileName, &s.StaticIP, &s.CallingMAC, &s.QuotaBytes, &exp, &s.Status,
		&s.ResellerID, &s.ResellerName,
		&s.LoanActive, &loanGranted, &s.LoanHours, &s.LoanQuotaBytes,
		&s.LoanDeductionType, &s.LoanInitialUsedBytes,
		&s.CreatedAt, &s.UpdatedAt,
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
	s.LoanGrantedAt = loanGranted
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
		static_ip, calling_mac, quota_bytes, expiration_date, status, reseller_id, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())
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
		reseller_id = VALUES(reseller_id),
		updated_at = NOW()`,
		sub.Username, sub.ContractNo, sub.FullName, sub.Phone, sub.Address, sub.Debt, sub.ProfileName,
		sub.StaticIP, sub.CallingMAC, sub.QuotaBytes, expDate, sub.Status, sub.ResellerID)
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
	rows, err := d.Query(`SELECT id, name, is_default, 
		COALESCE(header_title, 'ظƒط§ط±طھ ط¥ظ†طھط±ظ†طھ'), 
		COALESCE(footer_text, 'ظ†طھظ…ظ†ظ‰ ظ„ظƒظ… ظˆظ‚طھط§ظ‹ ظ…ظ…طھط¹ط§ظ‹'), 
		COALESCE(theme_color, '#0d6efd'), 
		COALESCE(html_content, ''), 
		COALESCE(css_content, ''), 
		COALESCE(bg_image, ''), 
		COALESCE(card_width, '240px'), 
		COALESCE(card_height, '140px'), 
		COALESCE(cols, 3), 
		COALESCE(` + "`rows`" + `, 7), 
		COALESCE(card_gap, '2mm'), 
		COALESCE(overlay_config, ''), 
		created_at
		FROM card_templates ORDER BY is_default DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.CardTemplate
	for rows.Next() {
		var t models.CardTemplate
		var isDef int
		err := rows.Scan(
			&t.ID, &t.Name, &isDef, &t.HeaderTitle, &t.FooterText, &t.ThemeColor,
			&t.HTMLContent, &t.CSSContent, &t.BgImage, &t.CardWidth, &t.CardHeight,
			&t.Cols, &t.Rows, &t.CardGap, &t.OverlayConfig, &t.CreatedAt,
		)
		if err == nil {
			t.IsDefault = (isDef == 1)
			t.CreatedAtStr = t.CreatedAt.Format("2006-01-02 15:04")
			list = append(list, t)
		}
	}
	return list, nil
}

func (d *DB) GetCardTemplate(id int64) (*models.CardTemplate, error) {
	var t models.CardTemplate
	var isDef int
	err := d.QueryRow(`SELECT id, name, is_default, 
		COALESCE(header_title, 'ظƒط§ط±طھ ط¥ظ†طھط±ظ†طھ'), 
		COALESCE(footer_text, 'ظ†طھظ…ظ†ظ‰ ظ„ظƒظ… ظˆظ‚طھط§ظ‹ ظ…ظ…طھط¹ط§ظ‹'), 
		COALESCE(theme_color, '#0d6efd'), 
		COALESCE(html_content, ''), 
		COALESCE(css_content, ''), 
		COALESCE(bg_image, ''), 
		COALESCE(card_width, '240px'), 
		COALESCE(card_height, '140px'), 
		COALESCE(cols, 3), 
		COALESCE(` + "`rows`" + `, 7), 
		COALESCE(card_gap, '2mm'), 
		COALESCE(overlay_config, ''), 
		created_at
		FROM card_templates WHERE id = ?`, id).Scan(
		&t.ID, &t.Name, &isDef, &t.HeaderTitle, &t.FooterText, &t.ThemeColor,
		&t.HTMLContent, &t.CSSContent, &t.BgImage, &t.CardWidth, &t.CardHeight,
		&t.Cols, &t.Rows, &t.CardGap, &t.OverlayConfig, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.IsDefault = (isDef == 1)
	t.CreatedAtStr = t.CreatedAt.Format("2006-01-02 15:04")
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

	if t.Cols <= 0 {
		t.Cols = 3
	}
	if t.Rows <= 0 {
		t.Rows = 7
	}
	if t.CardGap == "" {
		t.CardGap = "2mm"
	}
	if t.CardWidth == "" {
		t.CardWidth = "240px"
	}
	if t.CardHeight == "" {
		t.CardHeight = "140px"
	}
	if t.ThemeColor == "" {
		t.ThemeColor = "#0d6efd"
	}
	if t.HeaderTitle == "" {
		t.HeaderTitle = "ظƒط§ط±طھ ط¥ظ†طھط±ظ†طھ"
	}

	if t.ID == 0 {
		res, err := tx.Exec(`INSERT INTO card_templates (
			name, is_default, header_title, footer_text, theme_color,
			html_content, css_content, bg_image, card_width, card_height,
			cols, ` + "`rows`" + `, card_gap, overlay_config, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			t.Name, isDef, t.HeaderTitle, t.FooterText, t.ThemeColor,
			t.HTMLContent, t.CSSContent, t.BgImage, t.CardWidth, t.CardHeight,
			t.Cols, t.Rows, t.CardGap, t.OverlayConfig)
		if err != nil {
			return err
		}
		t.ID, _ = res.LastInsertId()
	} else {
		_, err = tx.Exec(`UPDATE card_templates SET
			name = ?, is_default = ?, header_title = ?, footer_text = ?, theme_color = ?,
			html_content = ?, css_content = ?, bg_image = ?, card_width = ?, card_height = ?,
			cols = ?, ` + "`rows`" + ` = ?, card_gap = ?, overlay_config = ?
			WHERE id = ?`,
			t.Name, isDef, t.HeaderTitle, t.FooterText, t.ThemeColor,
			t.HTMLContent, t.CSSContent, t.BgImage, t.CardWidth, t.CardHeight,
			t.Cols, t.Rows, t.CardGap, t.OverlayConfig, t.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) SetDefaultCardTemplate(id int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("UPDATE card_templates SET is_default = 0")
	_, err = tx.Exec("UPDATE card_templates SET is_default = 1 WHERE id = ?", id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) DeleteCardTemplate(id int64) error {
	var total int
	_ = d.QueryRow("SELECT COUNT(*) FROM card_templates").Scan(&total)
	if total <= 1 {
		return fmt.Errorf("ظ„ط§ ظٹظ…ظƒظ† ط­ط°ظپ ط§ظ„ظ‚ط§ظ„ط¨ ط§ظ„ظˆط­ظٹط¯ ط§ظ„ظ…طھط¨ظ‚ظٹ ظپظٹ ط§ظ„ظ†ط¸ط§ظ…")
	}

	var isDef int
	_ = d.QueryRow("SELECT is_default FROM card_templates WHERE id = ?", id).Scan(&isDef)

	_, err := d.Exec("DELETE FROM card_templates WHERE id = ?", id)
	if err != nil {
		return err
	}

	if isDef == 1 {
		_, _ = d.Exec("UPDATE card_templates SET is_default = 1 WHERE id = (SELECT id FROM (SELECT id FROM card_templates LIMIT 1) as t)")
	}
	return nil
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

// GetNextSequentialSSTPIP calculates the next available static IP in sequence for SSTP clients.
func (d *DB) GetNextSequentialSSTPIP() string {
	settings, err := d.GetSSTPSettings()
	gw := "10.171.171.1"
	if err == nil && strings.TrimSpace(settings.GatewayIP) != "" {
		gw = strings.TrimSpace(settings.GatewayIP)
	}

	parts := strings.Split(gw, ".")
	if len(parts) != 4 {
		return "10.171.171.2"
	}
	prefix := fmt.Sprintf("%s.%s.%s.", parts[0], parts[1], parts[2])
	gwOctet, _ := strconv.Atoi(parts[3])

	// Collect all currently assigned IPs
	used := make(map[string]bool)
	used[gw] = true // Gateway itself is occupied

	rows, qErr := d.Query("SELECT assigned_ip FROM sstp_clients WHERE assigned_ip != '' AND assigned_ip IS NOT NULL")
	if qErr == nil {
		defer rows.Close()
		for rows.Next() {
			var ip sql.NullString
			if err := rows.Scan(&ip); err == nil && ip.Valid && ip.String != "" {
				used[strings.TrimSpace(ip.String)] = true
			}
		}
	}

	// Search sequentially from 2 up to 254 (skipping the gateway)
	for i := 2; i <= 254; i++ {
		if i == gwOctet {
			continue
		}
		candidate := fmt.Sprintf("%s%d", prefix, i)
		if !used[candidate] {
			return candidate
		}
	}

	return fmt.Sprintf("%s250", prefix)
}

func (d *DB) EnsureAllSSTPClientsHaveSequentialIP() {
	rows, err := d.Query("SELECT id, username, assigned_ip FROM sstp_clients ORDER BY id ASC")
	if err != nil {
		return
	}
	defer rows.Close()

	var toUpdate []int64
	for rows.Next() {
		var id int64
		var u, ip sql.NullString
		if err := rows.Scan(&id, &u, &ip); err == nil {
			if !ip.Valid || strings.TrimSpace(ip.String) == "" {
				toUpdate = append(toUpdate, id)
			}
		}
	}

	if len(toUpdate) > 0 {
		for _, id := range toUpdate {
			nextIP := d.GetNextSequentialSSTPIP()
			_, _ = d.Exec("UPDATE sstp_clients SET assigned_ip = ? WHERE id = ?", nextIP, id)
		}
		_ = d.SyncSSTPFiles()
	}
}

func (d *DB) SaveSSTPClient(c *models.SSTPClient) error {
	en := 0
	if c.Enabled {
		en = 1
	}
	c.AssignedIP = strings.TrimSpace(c.AssignedIP)
	if c.AssignedIP == "" {
		c.AssignedIP = d.GetNextSequentialSSTPIP()
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
	assignedIP = strings.TrimSpace(assignedIP)
	if assignedIP == "" {
		assignedIP = d.GetNextSequentialSSTPIP()
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

	var oldProfile string
	_ = tx.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&oldProfile)
	if profileName == "" {
		profileName = oldProfile
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

	newExp, finalQuota, deductedHours, deductedQuotaBytes, hadLoan := d.CalculateLoanDeductionTx(tx, username, &p)

	_, err = tx.Exec(`UPDATE subscribers SET
		profile_name = ?,
		quota_bytes = ?,
		expiration_date = ?,
		status = 'active',
		loan_active = 0,
		loan_granted_at = NULL,
		loan_hours = 0,
		loan_quota_bytes = 0,
		loan_deduction_type = 'full',
		loan_initial_used_bytes = 0
		WHERE username = ?`, p.Name, finalQuota, newExp, username)
	if err != nil {
		return err
	}

	expStr := newExp.Format("02 Jan 2006 15:04:05")
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username)
	_, _ = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, expStr)

	_, _ = tx.Exec("DELETE FROM radusergroup WHERE username = ?", username)
	_, _ = tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", username, p.Name)

	_, _ = tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username)
	if finalQuota > 0 {
		_, _ = tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", finalQuota))
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
	if hadLoan {
		desc += fmt.Sprintf(" (تسوية سلفة: خصم %.1f ساعة و %.2f جيجا)", deductedHours, float64(deductedQuotaBytes)/(1024*1024*1024))
	}
	_, _ = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'renew', ?, ?, ?, ?, ?, ?)`,
		username, contractNo, fullName, desc, price, payStatus, method, notes, adminUser)

	if err := tx.Commit(); err != nil {
		return err
	}

	if oldProfile != "" && oldProfile != p.Name {
		_ = d.DisconnectSubscriberSession(username)
	} else {
		_ = d.RestoreActiveSubscriberCoA(username)
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

	desc := fmt.Sprintf("ط¥ط¶ط§ظپط© ظƒظˆطھط© ط±طµظٹط¯: %.1f GB", quotaGB)
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

	desc := fmt.Sprintf("ط³ط¯ط§ط¯ ظ†ظ‚ط¯ظٹ ظ…ظ† ط§ظ„ظ…ط¯ظٹظˆظ†ظٹط© (ظ…طھط¨ظ‚ظٹ: %.2f ط¬.ظ…)", newDebt)
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
		vars["{expiry_date}"] = "ط؛ظٹط± ظ…ط­ط¯ط¯"
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
		sendErr = fmt.Errorf("ط®ط¯ظ…ط© ظˆط§طھط³ط§ط¨ ط؛ظٹط± ظ…ظ‚طھط±ظ†ط©")
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
// Speed Control (ط§ظ„طھط­ظƒظ… ط¨ط§ظ„ط³ط±ط¹ط§طھ ظˆظ…ط¶ط§ط¹ظپط© ط§ظ„ط¨ط§ظ‚ط§طھ)
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
			item.IsBoosted = (item.BoostedRateLimit != "" && item.CurrentRateLimit == item.BoostedRateLimit) ||
				(item.BaseRateLimit != "" && item.CurrentRateLimit != item.BaseRateLimit)
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

	// Apply CoA rate limit dynamically to all active subscribers without kicking them!
	go func() {
		_, _ = d.ApplyCoARateLimitToAllActiveSessions()
	}()

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

	// Apply CoA base rate limit dynamically to all active subscribers without kicking them!
	go func() {
		_, _ = d.ApplyCoARateLimitToAllActiveSessions()
	}()

	return nil
}

func (d *DB) RestoreSingleProfileBaseSpeed(profileID int64, disconnectUsers bool) error {
	_ = d.SyncProfileBaseSpeeds()

	var baseRate, profName string
	err := d.QueryRow(`SELECT COALESCE(pbs.base_rate_limit, p.rate_limit), p.name 
		FROM profiles p
		LEFT JOIN profile_base_speeds pbs ON pbs.profile_id = p.id
		WHERE p.id = ?`, profileID).Scan(&baseRate, &profName)
	if err != nil {
		return err
	}

	if baseRate == "" {
		return fmt.Errorf("السرعة الأصلية غير متوفرة")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Restore profile rate_limit
	_, _ = tx.Exec("UPDATE profiles SET rate_limit = ? WHERE id = ?", baseRate, profileID)

	// 2. Restore radgroupreply Mikrotik-Rate-Limit
	_, _ = tx.Exec("UPDATE radgroupreply SET value = ? WHERE groupname = ? AND attribute = 'Mikrotik-Rate-Limit'", baseRate, profName)

	// 3. Clear boosted rate limit in profile_base_speeds
	_, _ = tx.Exec("UPDATE profile_base_speeds SET boosted_rate_limit = '' WHERE profile_id = ?", profileID)

	// 4. Check if any profiles are still boosted; if none, turn off boosted status in config
	var remainingBoosted int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM profile_base_speeds WHERE boosted_rate_limit != '' AND boosted_rate_limit IS NOT NULL`).Scan(&remainingBoosted)
	if remainingBoosted == 0 {
		_, _ = tx.Exec("UPDATE speed_control_config SET is_currently_boosted = 0, mode = 'off', temp_expires_at = NULL WHERE id = 1")
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Apply CoA base rate limit dynamically to active subscribers of this profile without kicking them!
	go func() {
		_, _ = d.ApplyCoARateLimitToProfileActiveSessions(profName, baseRate)
	}()

	return nil
}

func (d *DB) DisconnectProfileActiveSessions(profileName string) (int, error) {
	rows, err := d.Query(`SELECT r.acctsessionid, r.username, r.nasipaddress, r.framedipaddress
		FROM radacct r
		JOIN radusergroup ug ON r.username = ug.username
		WHERE r.acctstoptime IS NULL AND ug.groupname = ?`, profileName)
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
		count++
	}
	return count, nil
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

	nasMap := make(map[string]struct {
		secret  string
		coaPort int
	})
	nasRows, _ := d.Query("SELECT nasname, secret, coa_port FROM nas")
	if nasRows != nil {
		for nasRows.Next() {
			var nName, sec string
			var cp int
			if err := nasRows.Scan(&nName, &sec, &cp); err == nil {
				if sec == "" {
					sec = "radius123"
				}
				if cp <= 0 {
					cp = 3799
				}
				nasMap[nName] = struct {
					secret  string
					coaPort int
				}{sec, cp}
			}
		}
		nasRows.Close()
	}

	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(sess sess) {
			defer wg.Done()
			nasInfo, ok := nasMap[sess.nasIP]
			secret := "radius123"
			coaPort := 3799
			if ok {
				secret = nasInfo.secret
				coaPort = nasInfo.coaPort
			}

			var fIP net.IP
			if sess.framedIP != "" {
				fIP = net.ParseIP(sess.framedIP)
			}

			_, _ = radius.SendDisconnectRequest(sess.nasIP, coaPort, []byte(secret), sess.username, sess.sessionID, fIP)
		}(s)
	}

	// Update all active sessions in radacct to terminated
	_, _ = d.Exec(`UPDATE radacct SET acctstoptime = CURRENT_TIMESTAMP, acctterminatecause = 'Admin-Reset' WHERE acctstoptime IS NULL`)

	subTrafficMu.Lock()
	subTrafficHistory = make(map[string]*subTrafficPoint)
	subTrafficMu.Unlock()

	// Wait up to 3 seconds for CoA UDP packets to transmit
	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()
	select {
	case <-c:
	case <-time.After(3 * time.Second):
	}

	return len(sessions), nil
}

// CloseAllDatabaseSessions marks all active sessions as closed in radacct WITHOUT kicking out users from NAS routers.
// This is used to resolve 'User Already Logged In' errors caused by stale/ghost accounting sessions in the database.
func (d *DB) CloseAllDatabaseSessions() (int64, error) {
	res, err := d.Exec(`UPDATE radacct SET acctstoptime = CURRENT_TIMESTAMP, acctterminatecause = 'Admin-Cleared-Stale' WHERE acctstoptime IS NULL`)
	if err != nil {
		return 0, err
	}

	subTrafficMu.Lock()
	subTrafficHistory = make(map[string]*subTrafficPoint)
	subTrafficMu.Unlock()

	rows, _ := res.RowsAffected()
	return rows, nil
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
// Cafe Hotspot Cards Management (ظ…ظ†ط¸ظˆظ…ط© ظƒط±ظˆطھ ط§ظ„ظƒط§ظپظٹظ‡ط§طھ ط¹ظ„ظ‰ ط§ظ„ط±ط§ط¯ظٹظˆط³)
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
		return "0 ط¯ظ‚ظٹظ‚ط©"
	}
	hours := sec / 3600
	mins := (sec % 3600) / 60
	secs := sec % 60
	if hours > 0 {
		return fmt.Sprintf("%d ط³ط§ط¹ط© ظˆ %d ط¯ظ‚ظٹظ‚ط©", hours, mins)
	}
	if mins > 0 {
		return fmt.Sprintf("%d ط¯ظ‚ظٹظ‚ط© ظˆ %d ط«ط§ظ†ظٹط©", mins, secs)
	}
	return fmt.Sprintf("%d ط«ط§ظ†ظٹط©", secs)
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
		b.created_at, COALESCE(b.nas_id, 0), COALESCE(n.shortname, 'ط§ظ„ظپط±ط¹ ط§ظ„ط±ط¦ظٹط³ظٹ')
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
		JOIN cards c ON (r.username = c.username OR (c.original_code != '' AND r.username = c.original_code))
		WHERE c.batch_id = ? AND r.acctstoptime IS NULL`, b.ID).Scan(&b.OnlineCount)

	return &b, nil
}

func (d *DB) GetCafeBatches(nasID int64) ([]models.Batch, error) {
	query := `SELECT b.id, b.batch_code, COALESCE(b.batch_name, ''), b.profile_id, COALESCE(p.display_name, ''),
		b.count, COALESCE(b.cost_price_per_card, 0.0), b.price_per_card, COALESCE(b.total_cost_price, 0.0), b.total_price,
		b.created_at, COALESCE(b.nas_id, 0), COALESCE(n.shortname, 'ط§ظ„ظپط±ط¹ ط§ظ„ط±ط¦ظٹط³ظٹ')
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
		return nil, fmt.Errorf("ظٹط±ط¬ظ‰ ط¥ط¯ط®ط§ظ„ ظƒظˆط¯ ط§ظ„ظƒط§ط±طھ")
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
		c.batch_id, COALESCE(b.batch_code, ''), COALESCE(c.nas_id, 0), COALESCE(n.shortname, 'ط¹ط§ظ… / ط؛ظٹط± ظ…ظ‚ظٹط¯'),
		p.name, p.display_name, p.rate_limit, p.quota_bytes,
		COALESCE(c.original_code, ''), c.is_mac_converted, COALESCE(c.calling_mac, '')
		FROM cards c
		LEFT JOIN batches b ON c.batch_id = b.id
		LEFT JOIN nas n ON c.nas_id = n.id
		LEFT JOIN profiles p ON c.profile_id = p.id
		WHERE c.username = ? OR (c.original_code != '' AND c.original_code = ?) LIMIT 1`

	row := d.QueryRow(query, code, code)
	err := row.Scan(&c.ID, &c.Username, &c.Password, &c.Price, &c.Status, &c.FirstLoginAt, &c.ExpiredAt,
		&batchID, &batchCode, &nasID, &nasName,
		&profileName, &profileDisp, &rateLimit, &quotaBytes,
		&c.OriginalCode, &c.IsMACConverted, &c.CallingMAC)
	if err != nil {
		return nil, fmt.Errorf("ط§ظ„ظƒط§ط±طھ '%s' ط؛ظٹط± ظ…ط³ط¬ظ„ ظپظٹ ط§ظ„ظ†ط¸ط§ظ…", code)
	}

	tracker := &models.CafeCardTracker{
		CardCode:       c.Username,
		OriginalCode:   c.OriginalCode,
		IsMACConverted: c.IsMACConverted,
		CallingMAC:     c.CallingMAC,
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
		tracker.QuotaDisplay = "ط؛ظٹط± ظ…ط­ط¯ظˆط¯ (Open âˆ‍)"
	}

	// 2. Query Accounting consumption from radacct
	var sumIn, sumOut, sumTime sql.NullInt64
	var minStart sql.NullTime
	var lastMAC, lastIP sql.NullString

	_ = d.QueryRow(`SELECT SUM(acctinputoctets), SUM(acctoutputoctets), SUM(acctsessiontime),
		MIN(acctstarttime), MAX(callingstationid), MAX(framedipaddress)
		FROM radacct 
		WHERE username = ? OR (username != '' AND username = ?)`, c.Username, c.OriginalCode).Scan(&sumIn, &sumOut, &sumTime, &minStart, &lastMAC, &lastIP)

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
		tracker.RemainingDisplay = "ظ…ظپطھظˆط­ âˆ‍"
		tracker.UsagePercent = 0.0
	}

	// 5. Diagnostics & Status
	if tracker.IsOnline {
		tracker.Status = "online"
		tracker.StatusLabel = "ظ…طھطµظ„ ط§ظ„ط¢ظ† (Online)"
		tracker.StatusBadgeClass = "bg-success text-white"
		tracker.ExpireReason = "ط§ظ„ظƒط§ط±طھ ظٹط¹ظ…ظ„ ظˆظٹطھطµظ„ ط¨ط§ظ„ط´ط¨ظƒط© ط­ط§ظ„ظٹط§ظ‹ ط¨ط´ظƒظ„ ظ…ظ…طھط§ط²."
	} else if tracker.TotalBytes == 0 && tracker.FirstLogin == "" {
		tracker.Status = "available"
		tracker.StatusLabel = "ظ…طھط§ط­ ظ„ظ„ط¨ظٹط¹ (ظ„ظ… ظٹط¨ط¯ط£ ط¨ط¹ط¯)"
		tracker.StatusBadgeClass = "bg-light text-secondary border"
		tracker.ExpireReason = "ط§ظ„ظƒط§ط±طھ ط¬ط¯ظٹط¯ ظˆط¬ط§ظ‡ط² ظ„ظ„ط¨ظٹط¹طŒ ظ„ظ… ظٹط³ط¬ظ„ ط§ظ„ط¹ظ…ظٹظ„ ط§ظ„ط¯ط®ظˆظ„ ط¨ظ‡ ط­طھظ‰ ط§ظ„ط¢ظ†."
	} else if quotaBytes > 0 && tracker.TotalBytes >= quotaBytes {
		tracker.Status = "expired"
		tracker.StatusLabel = "ظ…ظ†طھظ‡ظٹ ط§ظ„ظƒظˆطھط© (Expired Quota)"
		tracker.StatusBadgeClass = "bg-danger text-white"
		tracker.ExpireReason = "ط§ظ†طھظ‡طھ ظƒظˆطھط© ط§ظ„طھط­ظ…ظٹظ„ ط§ظ„ظ…ط®طµطµط© ظ„ظ„ظƒط§ط±طھ ط¨ط§ظ„ظƒط§ظ…ظ„ ظˆطھظ… ط¥ظٹظ‚ط§ظپ ط§ظ„ط®ط¯ظ…ط©."
	} else {
		tracker.Status = "used"
		tracker.StatusLabel = "ظ…ط³طھظ‡ظ„ظƒ / ط؛ظٹط± ظ…طھطµظ„"
		tracker.StatusBadgeClass = "bg-secondary text-white"
		tracker.ExpireReason = "ط§ظ„ظƒط§ط±طھ ظ…ط³طھط®ط¯ظ… ظˆط؛ظٹط± ظ…طھطµظ„ ط­ط§ظ„ظٹط§ظ‹ (ظ‚ط¯ طھظƒظˆظ† ظ…ط¯ط© ط§ظ„طµظ„ط§ط­ظٹط© ط£ظˆ ط§ظ„ط¬ظ„ط³ط© ط§ظ†طھظ‡طھ)."
	}

	return tracker, nil
}

func (d *DB) DisconnectCard(code string) (bool, error) {
	var sessionID, nasIP, framedIPStr sql.NullString
	err := d.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress
		FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, code).Scan(
		&sessionID, &nasIP, &framedIPStr)
	if err != nil || !sessionID.Valid {
		return false, fmt.Errorf("ط§ظ„ظƒط§ط±طھ ط؛ظٹط± ظ…طھطµظ„ ط£ظˆظ†ظ„ط§ظٹظ† ط­ط§ظ„ظٹط§ظ‹")
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
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			d.syncActiveCards()
		}
	}()
}

func (d *DB) syncActiveCards() {
	// 1. MAC Conversion After First Session (ط§ظ„طھط­ظˆظٹظ„ ظ„ظ…ط§ظƒ ط£ط¯ط±ط³ ط¨ط¹ط¯ ط§ظ†طھظ‡ط§ط، ط£ظˆظ„ ط¬ظ„ط³ط© ط¹ظ†ط¯ طھط³ط¬ظٹظ„ ط§ظ„ط®ط±ظˆط¬):
	// For cards with convert_mac_after_session=1 and is_mac_converted=0,
	// when the customer finished at least one session in radacct (acctstoptime IS NOT NULL)
	// and currently has NO active session running (acctstoptime IS NULL).
	rowsConvert, err := d.Query(`SELECT c.id, c.username,
		(SELECT r.callingstationid 
		 FROM radacct r 
		 WHERE (r.username = c.username OR (c.original_code != '' AND r.username = c.original_code))
		   AND r.callingstationid != '' AND r.acctstoptime IS NOT NULL
		 ORDER BY r.radacctid DESC LIMIT 1) AS last_mac
		FROM cards c
		WHERE c.convert_mac_after_session = 1 
		  AND c.is_mac_converted = 0
		  AND EXISTS (
		      SELECT 1 FROM radacct r1 
		      WHERE (r1.username = c.username OR (c.original_code != '' AND r1.username = c.original_code))
		        AND r1.acctstoptime IS NOT NULL
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM radacct r2 
		      WHERE (r2.username = c.username OR (c.original_code != '' AND r2.username = c.original_code))
		        AND r2.acctstoptime IS NULL
		  )`)
	if err == nil {
		type convertEntry struct {
			cardID   int64
			username string
			mac      string
		}
		var toConvert []convertEntry
		for rowsConvert.Next() {
			var ce convertEntry
			var sqlMac sql.NullString
			if err := rowsConvert.Scan(&ce.cardID, &ce.username, &sqlMac); err == nil && sqlMac.Valid && sqlMac.String != "" {
				ce.mac = strings.TrimSpace(sqlMac.String)
				toConvert = append(toConvert, ce)
			}
		}
		rowsConvert.Close()

		for _, item := range toConvert {
			mac := item.mac
			oldUser := item.username

			// Update cards table: set username to mac, update password and calling_mac, mark converted
			_, _ = d.Exec(`UPDATE cards SET username = ?, password = ?, calling_mac = ?, is_mac_converted = 1 WHERE id = ?`, mac, mac, mac, item.cardID)

			// Recreate FreeRADIUS radcheck authentication for the MAC
			_, _ = d.Exec("DELETE FROM radcheck WHERE username = ?", oldUser)
			_, _ = d.Exec("DELETE FROM radcheck WHERE username = ?", mac)
			_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)", mac, mac)
			_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Calling-Station-Id', '==', ?)", mac, mac)
			_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Auth-Type', ':=', 'Accept')", mac)

			// Migrate radreply and radusergroup to the MAC username
			_, _ = d.Exec("UPDATE radreply SET username = ? WHERE username = ?", mac, oldUser)
			_, _ = d.Exec("DELETE FROM radusergroup WHERE username = ?", mac)
			_, _ = d.Exec("UPDATE radusergroup SET username = ? WHERE username = ?", mac, oldUser)
		}
	}

	// 2. MAC Binding & Locking: For cards with bind_mac=1 or lock_mac=1 that have not captured calling_mac yet
	rowsMAC, err := d.Query(`SELECT c.id, c.username, c.lock_mac, r.callingstationid
		FROM cards c
		JOIN radacct r ON (r.username = c.username OR (c.original_code != '' AND r.username = c.original_code))
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

	// 3. First Login Tracking for Calendar/First-Login time calculation mode
	rowsLogin, err := d.Query(`SELECT c.id, c.username, c.time_limit_seconds, c.time_calc_mode, MIN(r.acctstarttime)
		FROM cards c
		JOIN radacct r ON (r.username = c.username OR (c.original_code != '' AND r.username = c.original_code))
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

	// 4. Auto-Expiry Enforcement for Quota & Time Limits
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
		JOIN radacct r ON (r.username = c.username OR (c.original_code != '' AND r.username = c.original_code))
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

// --- Live Traffic Telemetry & Monitoring for Subscribers ---

type subTrafficPoint struct {
	timestamp time.Time
	inOctets  int64
	outOctets int64
	downBps   int64
	upBps     int64
}

var (
	subTrafficMu      sync.Mutex
	subTrafficHistory = make(map[string]*subTrafficPoint)
)

func parseRateLimitBps(rateStr string) (uploadBps int64, downloadBps int64) {
	rateStr = strings.TrimSpace(rateStr)
	if rateStr == "" {
		return 0, 0
	}
	parts := strings.Split(rateStr, "/")
	parseSingle := func(s string) int64 {
		s = strings.TrimSpace(strings.ToUpper(s))
		if s == "" {
			return 0
		}
		var mult int64 = 1
		if strings.HasSuffix(s, "G") || strings.HasSuffix(s, "GBPS") {
			mult = 1000 * 1000 * 1000
			s = strings.TrimSuffix(strings.TrimSuffix(s, "GBPS"), "G")
		} else if strings.HasSuffix(s, "M") || strings.HasSuffix(s, "MBPS") {
			mult = 1000 * 1000
			s = strings.TrimSuffix(strings.TrimSuffix(s, "MBPS"), "M")
		} else if strings.HasSuffix(s, "K") || strings.HasSuffix(s, "KBPS") {
			mult = 1000
			s = strings.TrimSuffix(strings.TrimSuffix(s, "KBPS"), "K")
		}
		val, _ := strconv.ParseFloat(s, 64)
		return int64(val * float64(mult))
	}

	if len(parts) >= 2 {
		uploadBps = parseSingle(parts[0])
		downloadBps = parseSingle(parts[1])
	} else {
		downloadBps = parseSingle(parts[0])
		uploadBps = downloadBps / 2
	}
	return uploadBps, downloadBps
}

func FormatSpeedBps(bps int64) string {
	if bps <= 0 {
		return "0.00 Kbps"
	}
	if bps >= 1000*1000*1000 {
		return fmt.Sprintf("%.2f Gbps", float64(bps)/(1000.0*1000.0*1000.0))
	}
	if bps >= 1000*1000 {
		return fmt.Sprintf("%.2f Mbps", float64(bps)/(1000.0*1000.0))
	}
	if bps >= 1000 {
		return fmt.Sprintf("%.1f Kbps", float64(bps)/1000.0)
	}
	return fmt.Sprintf("%d bps", bps)
}

func (d *DB) GetSubscriberLiveTraffic(username string) (*models.SubscriberLiveTraffic, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("اسم المستخدم مطلوب")
	}

	traffic := &models.SubscriberLiveTraffic{
		Username:   username,
		LastUpdate: time.Now().Format("15:04:05"),
	}

	// 1. Fetch Subscriber Details
	var (
		fullName, contractNo, phone, address, profileName, staticIP sql.NullString
		quotaBytes                                                  sql.NullInt64
		expDate                                                     sql.NullTime
		status                                                      sql.NullString
	)

	_ = d.QueryRow(`SELECT full_name, contract_no, phone, address, profile_name, static_ip, quota_bytes, expiration_date, status
		FROM subscribers WHERE username = ?`, username).Scan(
		&fullName, &contractNo, &phone, &address, &profileName, &staticIP, &quotaBytes, &expDate, &status)

	if fullName.Valid && fullName.String != "" {
		traffic.FullName = fullName.String
	} else {
		traffic.FullName = username
	}
	if contractNo.Valid {
		traffic.ContractNo = contractNo.String
	} else {
		traffic.ContractNo = "-"
	}
	if phone.Valid {
		traffic.Phone = phone.String
	}
	if address.Valid {
		traffic.Address = address.String
	}
	if profileName.Valid {
		traffic.ProfileName = profileName.String
	}
	if quotaBytes.Valid {
		traffic.QuotaBytes = quotaBytes.Int64
	}
	if expDate.Valid {
		traffic.ExpirationDate = expDate.Time.Format("2006-01-02 15:04")
	} else {
		traffic.ExpirationDate = "غير محدد"
	}

	// 2. Fetch Profile Rate Limit
	if traffic.ProfileName != "" {
		var rateLimit sql.NullString
		_ = d.QueryRow("SELECT rate_limit FROM profiles WHERE name = ?", traffic.ProfileName).Scan(&rateLimit)
		if rateLimit.Valid && rateLimit.String != "" {
			traffic.RateLimit = rateLimit.String
		}
	}
	if traffic.RateLimit == "" {
		// Fallback check radgroupreply
		var grpRate sql.NullString
		_ = d.QueryRow(`SELECT rgr.value FROM radusergroup rug
			JOIN radgroupreply rgr ON rug.groupname = rgr.groupname
			WHERE rug.username = ? AND rgr.attribute = 'Mikrotik-Rate-Limit' LIMIT 1`, username).Scan(&grpRate)
		if grpRate.Valid {
			traffic.RateLimit = grpRate.String
		}
	}
	if traffic.RateLimit == "" {
		traffic.RateLimit = "غير محدد"
	}

	upMax, downMax := parseRateLimitBps(traffic.RateLimit)
	traffic.MaxUploadBps = upMax
	traffic.MaxDownloadBps = downMax
	if downMax > 0 {
		traffic.MaxDownloadDisplay = FormatSpeedBps(downMax)
	} else {
		traffic.MaxDownloadDisplay = "غير محدود"
	}
	if upMax > 0 {
		traffic.MaxUploadDisplay = FormatSpeedBps(upMax)
	} else {
		traffic.MaxUploadDisplay = "غير محدود"
	}

	// 3. Check Live Session in radacct
	var (
		sessID, nasIP, framedIP, callingMAC  sql.NullString
		sessStart, sessUpdate                sql.NullTime
		sessTime, inOctets, outOctets        sql.NullInt64
	)

	_ = d.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress, callingstationid,
		acctstarttime, acctupdatetime, acctsessiontime, acctinputoctets, acctoutputoctets
		FROM radacct
		WHERE username = ? AND acctstoptime IS NULL
		ORDER BY radacctid DESC LIMIT 1`, username).Scan(
		&sessID, &nasIP, &framedIP, &callingMAC, &sessStart, &sessUpdate, &sessTime, &inOctets, &outOctets)

	now := time.Now()

	if sessID.Valid && sessID.String != "" {
		traffic.IsOnline = true
		traffic.Status = "online"
		traffic.StatusText = "متصل الآن (Online)"
		traffic.StatusBadgeClass = "bg-success text-white"
		traffic.SessionID = sessID.String
		traffic.NASIP = nasIP.String
		traffic.FramedIP = framedIP.String
		traffic.CallingMAC = callingMAC.String
		if sessStart.Valid {
			traffic.StartTime = sessStart.Time.Format("2006-01-02 15:04:05")
		}
		if sessTime.Valid {
			traffic.SessionTimeSec = sessTime.Int64
			traffic.Uptime = FormatDurationHuman(sessTime.Int64)
		}
		if inOctets.Valid {
			traffic.SessionUploadBytes = inOctets.Int64
		}
		if outOctets.Valid {
			traffic.SessionDownloadBytes = outOctets.Int64
		}
		traffic.SessionTotalBytes = traffic.SessionUploadBytes + traffic.SessionDownloadBytes
		traffic.SessionUploadDisplay = FormatBytesHuman(traffic.SessionUploadBytes)
		traffic.SessionDownloadDisplay = FormatBytesHuman(traffic.SessionDownloadBytes)
		traffic.SessionTotalDisplay = FormatBytesHuman(traffic.SessionTotalBytes)

		// Lookup NAS Router Name
		if traffic.NASIP != "" {
			var shortName, desc sql.NullString
			_ = d.QueryRow("SELECT shortname, description FROM nas WHERE nasname = ? LIMIT 1", traffic.NASIP).Scan(&shortName, &desc)
			if shortName.Valid && shortName.String != "" {
				traffic.NASName = fmt.Sprintf("%s (%s)", shortName.String, traffic.NASIP)
			} else {
				traffic.NASName = traffic.NASIP
			}
		}

		// 3.1 Try Live MikroTik RouterOS API measurement first
		var (
			targetIP         string
			apiPort          sql.NullInt64
			apiUser, apiPass sql.NullString
			apiSuccess       bool
		)

		targetIP = traffic.NASIP
		if targetIP == "" {
			targetIP = "10.171.171.15"
		}

		_ = d.QueryRow("SELECT nasname, api_port, api_user, api_password FROM nas WHERE nasname = ? LIMIT 1", targetIP).Scan(&targetIP, &apiPort, &apiUser, &apiPass)
		if !apiPort.Valid || apiPort.Int64 <= 0 {
			// Fallback to first configured mikrotik router in nas table
			_ = d.QueryRow("SELECT nasname, api_port, api_user, api_password FROM nas WHERE type = 'mikrotik' ORDER BY id ASC LIMIT 1").Scan(&targetIP, &apiPort, &apiUser, &apiPass)
		}

		portNum := 8728
		if apiPort.Valid && apiPort.Int64 > 0 {
			portNum = int(apiPort.Int64)
		}
		uName := "admin"
		if apiUser.Valid && strings.TrimSpace(apiUser.String) != "" {
			uName = strings.TrimSpace(apiUser.String)
		}
		pwd := ""
		if apiPass.Valid {
			pwd = apiPass.String
		}

		if targetIP != "" && portNum > 0 {
			addr := fmt.Sprintf("%s:%d", targetIP, portNum)
			mkClient, mkErr := mikrotik.GetPooledClient(addr, uName, pwd, 2500*time.Millisecond)
			if mkErr == nil {
				rxBps, txBps, trErr := mkClient.GetInterfaceTraffic(username)
				if trErr == nil {
					apiSuccess = true
					traffic.IsAPI = true
					traffic.DataSource = "MikroTik RouterOS API (لايف فوري)"
					traffic.CurrentDownloadBps = txBps
					traffic.CurrentUploadBps = rxBps
				} else {
					// Invalidate pool client if connection was lost or broken
					mikrotik.InvalidatePoolClient(addr, uName, pwd)
				}
			}
		}

		if !apiSuccess {
			traffic.IsAPI = false
			traffic.DataSource = "RADIUS Accounting"

			// Calculate Real-Time Bandwidth Speed (Bps) from RADIUS Accounting
			subTrafficMu.Lock()
			prev, exists := subTrafficHistory[username]
			if !exists {
				initDown := int64(0)
				initUp := int64(0)
				if traffic.SessionTimeSec > 5 {
					initDown = (traffic.SessionDownloadBytes * 8) / traffic.SessionTimeSec
					initUp = (traffic.SessionUploadBytes * 8) / traffic.SessionTimeSec
					if downMax > 0 && initDown > downMax {
						initDown = downMax
					}
					if upMax > 0 && initUp > upMax {
						initUp = upMax
					}
				}
				subTrafficHistory[username] = &subTrafficPoint{
					timestamp: now,
					inOctets:  traffic.SessionUploadBytes,
					outOctets: traffic.SessionDownloadBytes,
					downBps:   initDown,
					upBps:     initUp,
				}
				traffic.CurrentDownloadBps = initDown
				traffic.CurrentUploadBps = initUp
			} else {
				diffSec := now.Sub(prev.timestamp).Seconds()
				deltaIn := traffic.SessionUploadBytes - prev.inOctets
				deltaOut := traffic.SessionDownloadBytes - prev.outOctets

				if diffSec >= 0.8 {
					if deltaIn > 0 || deltaOut > 0 {
						newUp := int64(float64(deltaIn*8) / diffSec)
						newDown := int64(float64(deltaOut*8) / diffSec)
						if downMax > 0 && newDown > downMax*2 {
							newDown = downMax
						}
						if upMax > 0 && newUp > upMax*2 {
							newUp = upMax
						}
						prev.upBps = newUp
						prev.downBps = newDown
					} else if diffSec > 12 {
						// Soft decay
						prev.downBps = prev.downBps * 85 / 100
						prev.upBps = prev.upBps * 85 / 100
					}
					prev.timestamp = now
					prev.inOctets = traffic.SessionUploadBytes
					prev.outOctets = traffic.SessionDownloadBytes
				}
				traffic.CurrentDownloadBps = prev.downBps
				traffic.CurrentUploadBps = prev.upBps
			}
			subTrafficMu.Unlock()
		}

		traffic.DownloadSpeedDisplay = FormatSpeedBps(traffic.CurrentDownloadBps)
		traffic.UploadSpeedDisplay = FormatSpeedBps(traffic.CurrentUploadBps)

		if downMax > 0 {
			traffic.DownloadPercent = (float64(traffic.CurrentDownloadBps) / float64(downMax)) * 100.0
			if traffic.DownloadPercent > 100.0 {
				traffic.DownloadPercent = 100.0
			}
		}
		if upMax > 0 {
			traffic.UploadPercent = (float64(traffic.CurrentUploadBps) / float64(upMax)) * 100.0
			if traffic.UploadPercent > 100.0 {
				traffic.UploadPercent = 100.0
			}
		}
	} else {
		traffic.IsOnline = false
		traffic.Status = "offline"
		traffic.StatusText = "غير متصل (Offline)"
		traffic.StatusBadgeClass = "bg-secondary text-white"
		traffic.DownloadSpeedDisplay = "0.00 Kbps"
		traffic.UploadSpeedDisplay = "0.00 Kbps"
		traffic.DownloadPercent = 0
		traffic.UploadPercent = 0
		traffic.SessionUploadDisplay = "0 MB"
		traffic.SessionDownloadDisplay = "0 MB"
		traffic.SessionTotalDisplay = "0 MB"
		traffic.Uptime = "غير متصل"

		// Get last known session info
		var lastIP, lastMAC sql.NullString
		var lastStop sql.NullTime
		_ = d.QueryRow(`SELECT framedipaddress, callingstationid, acctstoptime
			FROM radacct WHERE username = ? ORDER BY radacctid DESC LIMIT 1`, username).Scan(&lastIP, &lastMAC, &lastStop)
		if lastIP.Valid {
			traffic.FramedIP = lastIP.String
		}
		if lastMAC.Valid {
			traffic.CallingMAC = lastMAC.String
		}
		if lastStop.Valid {
			traffic.StartTime = "آخر اتصال: " + lastStop.Time.Format("2006-01-02 15:04:05")
		}
	}

	// 4. All-time / Monthly Traffic Aggregation
	var sumIn, sumOut sql.NullInt64
	_ = d.QueryRow("SELECT SUM(acctinputoctets), SUM(acctoutputoctets) FROM radacct WHERE username = ?", username).Scan(&sumIn, &sumOut)
	if sumIn.Valid {
		traffic.TotalUploadBytes = sumIn.Int64
	}
	if sumOut.Valid {
		traffic.TotalDownloadBytes = sumOut.Int64
	}
	traffic.TotalBytes = traffic.TotalUploadBytes + traffic.TotalDownloadBytes
	traffic.TotalBytesDisplay = FormatBytesHuman(traffic.TotalBytes)

	// 5. Quota Calculations
	if traffic.QuotaBytes > 0 {
		traffic.QuotaDisplay = FormatBytesHuman(traffic.QuotaBytes)
		rem := traffic.QuotaBytes - traffic.TotalBytes
		if rem < 0 {
			rem = 0
		}
		traffic.QuotaRemainingBytes = rem
		traffic.QuotaRemainingDisplay = FormatBytesHuman(rem)
		traffic.QuotaUsagePercent = (float64(traffic.TotalBytes) / float64(traffic.QuotaBytes)) * 100.0
		if traffic.QuotaUsagePercent > 100.0 {
			traffic.QuotaUsagePercent = 100.0
		}
	} else {
		traffic.QuotaDisplay = "غير محدود ∞"
		traffic.QuotaRemainingDisplay = "مفتوح ∞"
		traffic.QuotaUsagePercent = 0.0
	}

	return traffic, nil
}

func (d *DB) GetAllSubscribersForLiveMonitor() ([]models.SubscriberTrafficOption, error) {
	// 1. Fetch active users map from radacct
	onlineMap := make(map[string]string)
	actRows, err := d.Query("SELECT username, framedipaddress FROM radacct WHERE acctstoptime IS NULL")
	if err == nil {
		defer actRows.Close()
		for actRows.Next() {
			var u, ip string
			if err := actRows.Scan(&u, &ip); err == nil && u != "" {
				onlineMap[u] = ip
			}
		}
	}

	// 2. Fetch all subscribers
	rows, err := d.Query(`SELECT rc.username, COALESCE(s.full_name, rc.username), COALESCE(s.contract_no, '-'), COALESCE(s.profile_name, '')
		FROM radcheck rc
		LEFT JOIN subscribers s ON rc.username = s.username
		WHERE rc.attribute = 'Cleartext-Password'
		GROUP BY rc.username
		ORDER BY rc.username ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var onlineList []models.SubscriberTrafficOption
	var offlineList []models.SubscriberTrafficOption

	for rows.Next() {
		var opt models.SubscriberTrafficOption
		if err := rows.Scan(&opt.Username, &opt.FullName, &opt.ContractNo, &opt.Profile); err == nil {
			if ip, online := onlineMap[opt.Username]; online {
				opt.IsOnline = true
				opt.LiveIP = ip
				onlineList = append(onlineList, opt)
			} else {
				opt.IsOnline = false
				offlineList = append(offlineList, opt)
			}
		}
	}

	// Combine with online users first
	result := append(onlineList, offlineList...)
	return result, nil
}

func (d *DB) DisconnectSubscriberSession(username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("اسم المستخدم مطلوب")
	}

	var sessionID, nasIP, framedIPStr sql.NullString
	err := d.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress
		FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(
		&sessionID, &nasIP, &framedIPStr)
	if err != nil || !sessionID.Valid {
		return fmt.Errorf("المشترك غير متصل أونلاين حالياً")
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

	_, _ = radius.SendDisconnectRequest(nasIP.String, coaPort, []byte(secret), username, sessionID.String, framedIP)

	_, _ = d.Exec(`UPDATE radacct SET acctstoptime = CURRENT_TIMESTAMP, acctterminatecause = 'Admin-Reset'
		WHERE username = ? AND acctstoptime IS NULL`, username)

	subTrafficMu.Lock()
	delete(subTrafficHistory, username)
	subTrafficMu.Unlock()

	return nil
}

// SendSubscriberCoA sends a dynamic CoA-Request (Code 43) to the subscriber's NAS
// without disconnecting the active session.
func (d *DB) SendSubscriberCoA(username string, opts radius.CoAOptions) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("اسم المستخدم مطلوب")
	}

	var sessionID, nasIP, framedIPStr sql.NullString
	err := d.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress
		FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(
		&sessionID, &nasIP, &framedIPStr)
	if err != nil || !sessionID.Valid || !nasIP.Valid || nasIP.String == "" {
		return nil // Not currently online, nothing to update via CoA
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

	if framedIPStr.Valid && framedIPStr.String != "" {
		opts.FramedIP = net.ParseIP(framedIPStr.String)
	}

	res, err := radius.SendCoARequest(nasIP.String, coaPort, []byte(secret), username, sessionID.String, opts)
	if err != nil {
		log.Printf("[CoA] Failed to send CoA-Request for %s to NAS %s:%d: %v", username, nasIP.String, coaPort, err)
		return err
	}
	if res != nil && !res.Success {
		log.Printf("[CoA] NAS %s returned NAK for %s: %s", nasIP.String, username, res.Message)
	} else if res != nil {
		log.Printf("[CoA] Successfully applied CoA for %s on NAS %s (Rate: %s, List: %s, Pool: %s)", username, nasIP.String, opts.RateLimit, opts.AddressList, opts.PoolName)
	}
	return nil
}

// RestoreActiveSubscriberCoA restores an active subscriber's speed and normal profile settings via CoA
func (d *DB) RestoreActiveSubscriberCoA(username string) error {
	var profileName string
	err := d.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&profileName)
	if err != nil {
		return err
	}

	prof, _ := d.GetProfileByName(profileName)
	if prof == nil {
		return nil
	}

	return d.SendSubscriberCoA(username, radius.CoAOptions{
		RateLimit:   prof.RateLimit,
		AddressList: prof.MikrotikAddressList,
		PoolName:    prof.PoolName,
	})
}

// ApplySubscriberQuotaExpiration transitions a quota-exhausted subscriber dynamically via CoA
// without kicking them out, unless the profile explicitly specifies 'disconnect'.
func (d *DB) ApplySubscriberQuotaExpiration(username string) error {
	var profileName string
	err := d.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&profileName)
	if err != nil {
		return err
	}

	prof, _ := d.GetProfileByName(profileName)
	if prof == nil {
		prof = &models.Profile{
			OnQuotaAction: "profile",
		}
	}

	if prof.OnQuotaAction == "disconnect" {
		_ = d.DisconnectSubscriberSession(username)
		_, _ = d.Exec("UPDATE subscribers SET status = 'expired_quota' WHERE username = ?", username)
		return nil
	}

	fallbackRate := "256k/512k"
	fallbackAddrList := "EXPIRED_QUOTA"
	fallbackPool := prof.OnQuotaPool

	if prof.OnQuotaAction == "profile" && prof.OnQuotaProfile != "" {
		if fp, err := d.GetProfileByName(prof.OnQuotaProfile); err == nil && fp != nil {
			if fp.RateLimit != "" {
				fallbackRate = fp.RateLimit
			}
			if fp.MikrotikAddressList != "" {
				fallbackAddrList = fp.MikrotikAddressList
			}
			if fp.PoolName != "" {
				fallbackPool = fp.PoolName
			}
		}
	} else if prof.OnQuotaAction == "pool" && prof.OnQuotaPool != "" {
		fallbackAddrList = prof.OnQuotaPool
		fallbackPool = prof.OnQuotaPool
	} else if prof.MikrotikAddressList != "" {
		fallbackAddrList = prof.MikrotikAddressList
	}

	_ = d.SendSubscriberCoA(username, radius.CoAOptions{
		RateLimit:   fallbackRate,
		AddressList: fallbackAddrList,
		PoolName:    fallbackPool,
	})

	_, _ = d.Exec("UPDATE subscribers SET status = 'expired_quota' WHERE username = ?", username)

	_, _ = d.Exec("DELETE FROM radreply WHERE username = ? AND attribute IN ('Mikrotik-Rate-Limit', 'Mikrotik-Address-List', 'Framed-Pool')", username)
	_, _ = d.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Rate-Limit', ':=', ?)", username, fallbackRate)
	if fallbackAddrList != "" {
		_, _ = d.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Address-List', ':=', ?)", username, fallbackAddrList)
	}
	if fallbackPool != "" {
		_, _ = d.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Framed-Pool', ':=', ?)", username, fallbackPool)
	}

	return nil
}

// ApplySubscriberTimeExpiration transitions a time-expired subscriber dynamically via CoA
// without kicking them out, unless the profile explicitly specifies 'disconnect'.
func (d *DB) ApplySubscriberTimeExpiration(username string) error {
	var profileName string
	err := d.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&profileName)
	if err != nil {
		return err
	}

	prof, _ := d.GetProfileByName(profileName)
	if prof == nil {
		prof = &models.Profile{
			OnTimeAction: "profile",
		}
	}

	if prof.OnTimeAction == "disconnect" {
		_ = d.DisconnectSubscriberSession(username)
		_, _ = d.Exec("UPDATE subscribers SET status = 'expired_time' WHERE username = ?", username)
		return nil
	}

	fallbackRate := "256k/512k"
	fallbackAddrList := "EXPIRED_TIME"
	fallbackPool := prof.OnTimePool

	if prof.OnTimeAction == "profile" && prof.OnTimeProfile != "" {
		if fp, err := d.GetProfileByName(prof.OnTimeProfile); err == nil && fp != nil {
			if fp.RateLimit != "" {
				fallbackRate = fp.RateLimit
			}
			if fp.MikrotikAddressList != "" {
				fallbackAddrList = fp.MikrotikAddressList
			}
			if fp.PoolName != "" {
				fallbackPool = fp.PoolName
			}
		}
	} else if prof.OnTimeAction == "pool" && prof.OnTimePool != "" {
		fallbackAddrList = prof.OnTimePool
		fallbackPool = prof.OnTimePool
	} else if prof.MikrotikAddressList != "" {
		fallbackAddrList = prof.MikrotikAddressList
	}

	_ = d.SendSubscriberCoA(username, radius.CoAOptions{
		RateLimit:   fallbackRate,
		AddressList: fallbackAddrList,
		PoolName:    fallbackPool,
	})

	_, _ = d.Exec("UPDATE subscribers SET status = 'expired_time' WHERE username = ?", username)

	_, _ = d.Exec("DELETE FROM radreply WHERE username = ? AND attribute IN ('Mikrotik-Rate-Limit', 'Mikrotik-Address-List', 'Framed-Pool')", username)
	_, _ = d.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Rate-Limit', ':=', ?)", username, fallbackRate)
	if fallbackAddrList != "" {
		_, _ = d.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Address-List', ':=', ?)", username, fallbackAddrList)
	}
	if fallbackPool != "" {
		_, _ = d.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Framed-Pool', ':=', ?)", username, fallbackPool)
	}

	return nil
}

// ApplyCoARateLimitToProfileActiveSessions sends CoA rate limits to all active sessions on a profile
func (d *DB) ApplyCoARateLimitToProfileActiveSessions(profileName string, rateLimit string) (int, error) {
	if rateLimit == "" {
		return 0, nil
	}

	rows, err := d.Query(`SELECT r.acctsessionid, r.username, r.nasipaddress, r.framedipaddress
		FROM radacct r
		JOIN radusergroup ug ON r.username = ug.username
		WHERE r.acctstoptime IS NULL AND ug.groupname = ?`, profileName)
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
	rows.Close()

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

		_, _ = radius.SendCoARequest(s.nasIP, coaPort, []byte(secret), s.username, s.sessionID, radius.CoAOptions{
			RateLimit: rateLimit,
			FramedIP:  fIP,
		})
		count++
	}
	return count, nil
}

// ApplyCoARateLimitToAllActiveSessions updates rate limits on all active sessions dynamically via CoA
func (d *DB) ApplyCoARateLimitToAllActiveSessions() (int, error) {
	rows, err := d.Query(`SELECT r.acctsessionid, r.username, r.nasipaddress, r.framedipaddress,
		COALESCE(p.rate_limit, '')
		FROM radacct r
		JOIN radusergroup ug ON r.username = ug.username
		JOIN profiles p ON ug.groupname = p.name
		WHERE r.acctstoptime IS NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type sess struct {
		sessionID string
		username  string
		nasIP     string
		framedIP  string
		rateLimit string
	}
	var sessions []sess
	for rows.Next() {
		var s sess
		_ = rows.Scan(&s.sessionID, &s.username, &s.nasIP, &s.framedIP, &s.rateLimit)
		sessions = append(sessions, s)
	}
	rows.Close()

	count := 0
	for _, s := range sessions {
		if s.rateLimit == "" {
			continue
		}
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

		_, _ = radius.SendCoARequest(s.nasIP, coaPort, []byte(secret), s.username, s.sessionID, radius.CoAOptions{
			RateLimit: s.rateLimit,
			FramedIP:  fIP,
		})
		count++
	}
	return count, nil
}

// StartSubscriberSyncDaemon runs background real-time synchronization for subscribers:
// checking quota consumption and expiration dates, and applying CoA rate-limit and address-list
// changes dynamically on active sessions without disconnecting them.
func (d *DB) StartSubscriberSyncDaemon() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			d.syncActiveSubscribers()
		}
	}()
}

func (d *DB) syncActiveSubscribers() {
	// 1. Disconnect active sessions of accounts that have been disabled, frozen, or suspended
	rowsDisabled, err := d.Query(`SELECT DISTINCT r.username
		FROM radacct r
		JOIN subscribers s ON r.username = s.username
		WHERE r.acctstoptime IS NULL 
		  AND (s.status = 'disabled' OR s.status = 'suspended' OR s.status = 'frozen')`)
	if err == nil {
		var toKick []string
		for rowsDisabled.Next() {
			var u string
			if err := rowsDisabled.Scan(&u); err == nil {
				toKick = append(toKick, u)
			}
		}
		rowsDisabled.Close()
		for _, u := range toKick {
			_ = d.DisconnectSubscriberSession(u)
		}
	}

	// 2. Check active online subscribers who are not disabled/frozen
	rows, err := d.Query(`SELECT DISTINCT r.username, s.status, s.quota_bytes, s.expiration_date, s.created_at, s.last_renewed_at, s.profile_name
		FROM radacct r
		JOIN subscribers s ON r.username = s.username
		WHERE r.acctstoptime IS NULL 
		  AND s.status NOT IN ('disabled', 'suspended', 'frozen')`)
	if err != nil {
		return
	}
	defer rows.Close()

	type onlineSub struct {
		username      string
		status        string
		quotaBytes    int64
		expDate       sql.NullTime
		createdAt     time.Time
		lastRenewedAt sql.NullTime
		profileName   string
	}

	var subs []onlineSub
	for rows.Next() {
		var s onlineSub
		if err := rows.Scan(&s.username, &s.status, &s.quotaBytes, &s.expDate, &s.createdAt, &s.lastRenewedAt, &s.profileName); err == nil {
			subs = append(subs, s)
		}
	}
	rows.Close()

	now := time.Now()

	for _, s := range subs {
		// A. Check Time Expiration
		isTimeExpired := s.expDate.Valid && now.After(s.expDate.Time)

		// B. Check Quota Expiration
		isQuotaExpired := false
		if s.quotaBytes > 0 {
			cycleStart := s.createdAt
			if s.lastRenewedAt.Valid {
				cycleStart = s.lastRenewedAt.Time
			}
			var usedBytes int64
			_ = d.QueryRow(`SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0)
				FROM radacct WHERE username = ? AND acctstarttime >= ?`, s.username, cycleStart).Scan(&usedBytes)
			if usedBytes >= s.quotaBytes {
				isQuotaExpired = true
			}
		}

		if isQuotaExpired {
			// If not already expired_quota, apply CoA quota expiration
			if s.status != "expired_quota" && s.status != "expired" {
				_ = d.ApplySubscriberQuotaExpiration(s.username)
			}
		} else if isTimeExpired {
			// If not already expired_time, apply CoA time expiration
			if s.status != "expired_time" && s.status != "expired" {
				_ = d.ApplySubscriberTimeExpiration(s.username)
			}
		} else if s.status == "expired_quota" || s.status == "expired_time" {
			// If neither expired but status was marked expired, restore them!
			_ = d.RestoreActiveSubscriberCoA(s.username)
			_, _ = d.Exec("UPDATE subscribers SET status = 'active' WHERE username = ?", s.username)
		}
	}
}

// --- Comprehensive Subscriber Login Sessions Reporting ---

func FormatTerminateCauseArabic(cause string, isOnline bool) string {
	if isOnline {
		return "متصل أونلاين الآن"
	}
	cause = strings.TrimSpace(cause)
	if cause == "" {
		return "انتهت الجلسة"
	}
	switch strings.ToLower(cause) {
	case "user-request":
		return "تسجيل خروج (طلب العميل)"
	case "lost-carrier":
		return "انقطاع إشارة الخط / الشبكة"
	case "lost-service":
		return "فقدان الخدمة"
	case "idle-timeout":
		return "انتهاء مهلة الخمول (Idle)"
	case "session-timeout":
		return "انتهاء وقت الجلسة المسموح"
	case "admin-reset":
		return "قطع إداري من النظام (CoA Reset)"
	case "admin-reboot":
		return "إعادة تشغيل النظام"
	case "port-error":
		return "خطأ في منفذ الاتصال"
	case "nas-error":
		return "خطأ في راوتر المايكروتك"
	case "nas-request":
		return "طلب إنهاء من المايكروتك"
	case "nas-reboot":
		return "إعادة تشغيل راوتر المايكروتك"
	default:
		return cause
	}
}

func FormatDurationArabic(sec int64) string {
	if sec <= 0 {
		return "أقل من دقيقة"
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		if m > 0 {
			return fmt.Sprintf("%d س و %d د", h, m)
		}
		return fmt.Sprintf("%d ساعة", h)
	}
	if m > 0 {
		if s > 0 {
			return fmt.Sprintf("%d د و %d ث", m, s)
		}
		return fmt.Sprintf("%d دقيقة", m)
	}
	return fmt.Sprintf("%d ثانية", s)
}

func (d *DB) GetSubscriberLoginSessions(search, status, nasIP, fromDate, toDate string, limit, offset int) ([]models.SubscriberLoginSession, int, error) {
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
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM radacct r LEFT JOIN subscribers s ON r.username = s.username WHERE %s", whereSQL)
	_ = d.QueryRow(countQuery, args...).Scan(&total)

	// 2. Data Query
	dataQuery := fmt.Sprintf(`SELECT 
		r.radacctid,
		r.acctsessionid,
		r.username,
		COALESCE(s.full_name, r.username),
		COALESCE(s.contract_no, '-'),
		COALESCE(s.phone, '-'),
		COALESCE(s.profile_name, ''),
		r.nasipaddress,
		COALESCE(n.shortname, r.nasipaddress),
		COALESCE(r.framedipaddress, ''),
		COALESCE(r.callingstationid, ''),
		r.acctstarttime,
		r.acctstoptime,
		COALESCE(r.acctsessiontime, 0),
		COALESCE(r.acctinputoctets, 0),
		COALESCE(r.acctoutputoctets, 0),
		COALESCE(r.acctterminatecause, '')
	FROM radacct r
	LEFT JOIN subscribers s ON r.username = s.username
	LEFT JOIN nas n ON r.nasipaddress = n.nasname
	WHERE %s
	ORDER BY r.radacctid DESC
	LIMIT ? OFFSET ?`, whereSQL)

	queryArgs := append(args, limit, offset)
	rows, err := d.Query(dataQuery, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sessions []models.SubscriberLoginSession
	for rows.Next() {
		var s models.SubscriberLoginSession
		var (
			fullName, contractNo, phone, profileName sql.NullString
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
				s.StatusText = "متصل الآن (Online)"
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

func (d *DB) GetSubscriberLoginSummary(search, status, nasIP, fromDate, toDate string) (*models.SubscriberLoginSummary, error) {
	whereClauses := []string{"1=1"}
	var args []interface{}

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

	query := fmt.Sprintf(`SELECT 
		COUNT(*),
		COUNT(DISTINCT r.username),
		COALESCE(SUM(CASE WHEN r.acctstoptime IS NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.acctstoptime IS NOT NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(r.acctoutputoctets), 0),
		COALESCE(SUM(r.acctinputoctets), 0),
		COALESCE(AVG(r.acctsessiontime), 0)
	FROM radacct r
	LEFT JOIN subscribers s ON r.username = s.username
	WHERE %s`, whereSQL)

	var (
		totalSessions, uniqueUsers, onlineCount, closedCount int64
		totalDown, totalUp                                   int64
		avgSec                                               float64
	)

	_ = d.QueryRow(query, args...).Scan(&totalSessions, &uniqueUsers, &onlineCount, &closedCount, &totalDown, &totalUp, &avgSec)

	totalTraffic := totalDown + totalUp

	summary := &models.SubscriberLoginSummary{
		TotalSessions:        totalSessions,
		OnlineSessions:       onlineCount,
		ClosedSessions:       closedCount,
		UniqueUsersCount:     uniqueUsers,
		TotalDownloadBytes:   totalDown,
		TotalUploadBytes:     totalUp,
		TotalTrafficBytes:    totalTraffic,
		TotalDownloadDisplay: FormatBytesHuman(totalDown),
		TotalUploadDisplay:   FormatBytesHuman(totalUp),
		TotalTrafficDisplay:  FormatBytesHuman(totalTraffic),
		AvgDurationSeconds:   int64(avgSec),
		AvgDurationDisplay:   FormatDurationArabic(int64(avgSec)),
	}

	return summary, nil
}

// -----------------------------------------------------------------------------
// Backup System Helpers
// -----------------------------------------------------------------------------

func (d *DB) GetBackupSettings() (*models.BackupSettings, error) {
	var s models.BackupSettings
	var lastBackup sql.NullTime
	err := d.QueryRow(`SELECT id, enabled, interval_hours, retention_count, send_whatsapp, whatsapp_phone, last_backup_at, last_status, COALESCE(last_error, ''), updated_at 
		FROM backup_settings WHERE id = 1`).Scan(
		&s.ID, &s.Enabled, &s.IntervalHours, &s.RetentionCount, &s.SendWhatsApp, &s.WhatsAppPhone, &lastBackup, &s.LastStatus, &s.LastError, &s.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			_, _ = d.Exec(`INSERT INTO backup_settings (id, enabled, interval_hours, retention_count, send_whatsapp, whatsapp_phone, last_status)
				VALUES (1, 1, 6, 10, 0, '', 'ready')`)
			return &models.BackupSettings{
				ID:             1,
				Enabled:        true,
				IntervalHours:  6,
				RetentionCount: 10,
				SendWhatsApp:   false,
				WhatsAppPhone:  "",
				LastStatus:     "ready",
			}, nil
		}
		return nil, err
	}
	if lastBackup.Valid {
		s.LastBackupAt = &lastBackup.Time
	}
	return &s, nil
}

func (d *DB) UpdateBackupSettings(s *models.BackupSettings) error {
	_, err := d.Exec(`UPDATE backup_settings SET 
		enabled = ?, interval_hours = ?, retention_count = ?, send_whatsapp = ?, whatsapp_phone = ? 
		WHERE id = 1`,
		s.Enabled, s.IntervalHours, s.RetentionCount, s.SendWhatsApp, s.WhatsAppPhone)
	return err
}

func (d *DB) UpdateBackupRunStatus(lastStatus string, lastError string) error {
	now := time.Now()
	_, err := d.Exec(`UPDATE backup_settings SET 
		last_backup_at = ?, last_status = ?, last_error = ? 
		WHERE id = 1`,
		now, lastStatus, lastError)
	return err
}

func (d *DB) ListBackups() ([]models.BackupRecord, error) {
	rows, err := d.Query(`SELECT id, filename, filepath, size_bytes, created_at, status, whatsapp_sent, COALESCE(error_message, '') 
		FROM backups ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.BackupRecord
	for rows.Next() {
		var b models.BackupRecord
		if err := rows.Scan(&b.ID, &b.Filename, &b.FilePath, &b.SizeBytes, &b.CreatedAt, &b.Status, &b.WhatsAppSent, &b.ErrorMessage); err != nil {
			continue
		}
		b.SizeFormatted = FormatBytesHuman(b.SizeBytes)
		b.CreatedAtFormatted = b.CreatedAt.Format("2006-01-02 15:04:05")
		list = append(list, b)
	}
	return list, nil
}

func (d *DB) CreateBackupRecord(b *models.BackupRecord) (int64, error) {
	res, err := d.Exec(`INSERT INTO backups (filename, filepath, size_bytes, created_at, status, whatsapp_sent, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.Filename, b.FilePath, b.SizeBytes, b.CreatedAt, b.Status, b.WhatsAppSent, b.ErrorMessage)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) DeleteBackupRecord(id int64) error {
	_, err := d.Exec(`DELETE FROM backups WHERE id = ?`, id)
	return err
}

func (d *DB) GetBackupRecord(id int64) (*models.BackupRecord, error) {
	var b models.BackupRecord
	err := d.QueryRow(`SELECT id, filename, filepath, size_bytes, created_at, status, whatsapp_sent, COALESCE(error_message, '') 
		FROM backups WHERE id = ?`, id).Scan(
		&b.ID, &b.Filename, &b.FilePath, &b.SizeBytes, &b.CreatedAt, &b.Status, &b.WhatsAppSent, &b.ErrorMessage)
	if err != nil {
		return nil, err
	}
	b.SizeFormatted = FormatBytesHuman(b.SizeBytes)
	b.CreatedAtFormatted = b.CreatedAt.Format("2006-01-02 15:04:05")
	return &b, nil
}

func (d *DB) MarkBackupWhatsAppSent(id int64, sent bool) error {
	_, err := d.Exec(`UPDATE backups SET whatsapp_sent = ? WHERE id = ?`, sent, id)
	return err
}

// --- Subscriber Self-Care Portal Methods ---

func (d *DB) GetSubscriberPortalSettings() (*models.SubscriberPortalSettings, error) {
	var s models.SubscriberPortalSettings
	var announcement sql.NullString
	err := d.QueryRow(`SELECT id, COALESCE(portal_enabled, 1), COALESCE(allow_loan_request, 1),
		COALESCE(show_quota_usage, 1), COALESCE(show_debt, 1), COALESCE(show_profile_details, 1),
		COALESCE(show_online_status, 1), COALESCE(allow_password_change, 0), COALESCE(allow_phone_login, 0),
		COALESCE(allow_ip_auto_login, 0), COALESCE(allow_session_disconnect, 1),
		COALESCE(company_name, 'شبكتنا لخدمات الإنترنت'), COALESCE(company_phone, ''), portal_announcement, updated_at
		FROM subscriber_portal_settings WHERE id = 1`).Scan(
		&s.ID, &s.PortalEnabled, &s.AllowLoanRequest,
		&s.ShowQuotaUsage, &s.ShowDebt, &s.ShowProfileDetails,
		&s.ShowOnlineStatus, &s.AllowPasswordChange, &s.AllowPhoneLogin,
		&s.AllowIPAutoLogin, &s.AllowSessionDisconnect,
		&s.CompanyName, &s.CompanyPhone, &announcement, &s.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return &models.SubscriberPortalSettings{
				ID:                     1,
				PortalEnabled:          true,
				AllowLoanRequest:       true,
				ShowQuotaUsage:         true,
				ShowDebt:               true,
				ShowProfileDetails:     true,
				ShowOnlineStatus:       true,
				AllowPasswordChange:    false,
				AllowPhoneLogin:        false,
				AllowIPAutoLogin:       false,
				AllowSessionDisconnect: true,
				CompanyName:            "شبكتنا لخدمات الإنترنت",
			}, nil
		}
		return nil, err
	}
	if announcement.Valid {
		s.PortalAnnouncement = announcement.String
	}
	return &s, nil
}

func (d *DB) SaveSubscriberPortalSettings(s *models.SubscriberPortalSettings) error {
	_, err := d.Exec(`INSERT INTO subscriber_portal_settings (
		id, portal_enabled, allow_loan_request, show_quota_usage, show_debt,
		show_profile_details, show_online_status, allow_password_change, allow_phone_login, allow_ip_auto_login, allow_session_disconnect,
		company_name, company_phone, portal_announcement
	) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON DUPLICATE KEY UPDATE
		portal_enabled = VALUES(portal_enabled),
		allow_loan_request = VALUES(allow_loan_request),
		show_quota_usage = VALUES(show_quota_usage),
		show_debt = VALUES(show_debt),
		show_profile_details = VALUES(show_profile_details),
		show_online_status = VALUES(show_online_status),
		allow_password_change = VALUES(allow_password_change),
		allow_phone_login = VALUES(allow_phone_login),
		allow_ip_auto_login = VALUES(allow_ip_auto_login),
		allow_session_disconnect = VALUES(allow_session_disconnect),
		company_name = VALUES(company_name),
		company_phone = VALUES(company_phone),
		portal_announcement = VALUES(portal_announcement)`,
		s.PortalEnabled, s.AllowLoanRequest, s.ShowQuotaUsage, s.ShowDebt,
		s.ShowProfileDetails, s.ShowOnlineStatus, s.AllowPasswordChange, s.AllowPhoneLogin, s.AllowIPAutoLogin, s.AllowSessionDisconnect,
		s.CompanyName, s.CompanyPhone, s.PortalAnnouncement)
	return err
}

// FindActiveSubscriberByIP finds an active online subscriber associated with the given IP in radacct
func (d *DB) FindActiveSubscriberByIP(ip string) (*models.Subscriber, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" || ip == "127.0.0.1" || ip == "::1" {
		return nil, sql.ErrNoRows
	}

	var username string
	err := d.QueryRow(`SELECT username FROM radacct 
		WHERE framedipaddress = ? AND acctstoptime IS NULL 
		ORDER BY radacctid DESC LIMIT 1`, ip).Scan(&username)
	if err != nil {
		return nil, err
	}

	return d.GetSubscriber(username)
}

func (d *DB) GetSubscriberPortalData(username string) (*models.SubscriberPortalView, error) {
	username = strings.TrimSpace(username)
	sub, err := d.GetSubscriber(username)
	if err != nil {
		return nil, fmt.Errorf("المشترك غير موجود: %w", err)
	}

	settings, err := d.GetSubscriberPortalSettings()
	if err != nil {
		settings = &models.SubscriberPortalSettings{
			PortalEnabled:          true,
			AllowLoanRequest:       true,
			ShowQuotaUsage:         true,
			ShowDebt:               true,
			ShowProfileDetails:     true,
			ShowOnlineStatus:       true,
			AllowPasswordChange:    false,
			AllowSessionDisconnect: true,
			CompanyName:            "شبكتنا لخدمات الإنترنت",
		}
	}

	var prof *models.Profile
	if sub.ProfileName != "" {
		prof, _ = d.GetProfileByName(sub.ProfileName)
	}
	if prof == nil {
		prof = &models.Profile{
			Name:        sub.ProfileName,
			DisplayName: sub.ProfileName,
		}
	}

	view := &models.SubscriberPortalView{
		Subscriber: *sub,
		Profile:    *prof,
		Settings:   *settings,
	}

	// 1. Quota & Consumption
	if sub.QuotaBytes > 0 {
		view.QuotaGB = float64(sub.QuotaBytes) / (1024 * 1024 * 1024)
	} else if prof.QuotaBytes > 0 {
		view.QuotaGB = float64(prof.QuotaBytes) / (1024 * 1024 * 1024)
	}

	// Calculate traffic used since cycle start (LastRenewedAt or CreatedAt)
	cycleStart := sub.CreatedAt
	var lastRenewed sql.NullTime
	_ = d.QueryRow("SELECT last_renewed_at FROM subscribers WHERE username = ?", username).Scan(&lastRenewed)
	if lastRenewed.Valid {
		cycleStart = lastRenewed.Time
	}

	var usedBytes int64
	_ = d.QueryRow(`SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0)
		FROM radacct WHERE username = ? AND acctstarttime >= ?`, username, cycleStart).Scan(&usedBytes)
	view.UsedBytes = usedBytes
	view.UsedGB = float64(usedBytes) / (1024 * 1024 * 1024)

	if sub.QuotaBytes > 0 {
		rem := sub.QuotaBytes - usedBytes
		if rem < 0 {
			rem = 0
		}
		view.RemainingBytes = rem
		view.RemainingGB = float64(rem) / (1024 * 1024 * 1024)
		view.UsagePercent = (float64(usedBytes) / float64(sub.QuotaBytes)) * 100.0
		if view.UsagePercent > 100.0 {
			view.UsagePercent = 100.0
		}
	} else {
		view.RemainingBytes = -1
		view.RemainingGB = -1
		view.UsagePercent = 0.0
	}

	// 2. Expiration & Remaining Days
	if sub.ExpirationDate.Valid {
		expTime := sub.ExpirationDate.Time
		view.ExpirationDisplay = expTime.Format("2006-01-02 15:04")
		now := time.Now()
		if expTime.After(now) {
			diff := expTime.Sub(now)
			days := int(diff.Hours() / 24)
			hours := int(diff.Hours()) % 24
			if days > 0 {
				view.RemainingDaysDisplay = fmt.Sprintf("باقي %d يوم و %d ساعة", days, hours)
			} else {
				mins := int(diff.Minutes()) % 60
				view.RemainingDaysDisplay = fmt.Sprintf("باقي %d ساعة و %d دقيقة", hours, mins)
			}
		} else {
			diff := now.Sub(expTime)
			days := int(diff.Hours() / 24)
			if days > 0 {
				view.RemainingDaysDisplay = fmt.Sprintf("منتهي الصلاحية منذ %d يوم", days)
			} else {
				view.RemainingDaysDisplay = "منتهي الصلاحية اليوم"
			}
		}
	} else {
		view.ExpirationDisplay = "غير محدد (مفتوح)"
		view.RemainingDaysDisplay = "صلاحية غير محدودة"
	}

	// 3. Online Session Status from radacct
	var (
		liveIP, liveMAC, nasIP sql.NullString
		acctStart              sql.NullTime
	)
	errAcct := d.QueryRow(`SELECT framedipaddress, callingstationid, nasipaddress, acctstarttime
		FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(
		&liveIP, &liveMAC, &nasIP, &acctStart)

	if errAcct == nil && liveIP.Valid && liveIP.String != "" {
		view.IsOnline = true
		view.OnlineIP = liveIP.String
		if liveMAC.Valid {
			view.OnlineMAC = liveMAC.String
		}
		if nasIP.Valid {
			view.NasIP = nasIP.String
		}
		if acctStart.Valid {
			dur := time.Since(acctStart.Time)
			hours := int(dur.Hours())
			mins := int(dur.Minutes()) % 60
			if hours > 24 {
				days := hours / 24
				hours = hours % 24
				view.OnlineDuration = fmt.Sprintf("%d يوم و %d ساعة", days, hours)
			} else {
				view.OnlineDuration = fmt.Sprintf("%d ساعة و %d دقيقة", hours, mins)
			}
		}
	}

	// 4. Loan Capabilities
	nowCheck := time.Now()
	isTimeExpired := sub.ExpirationDate.Valid && nowCheck.After(sub.ExpirationDate.Time)
	isQuotaExpired := sub.QuotaBytes > 0 && view.RemainingBytes <= 0
	view.IsTimeExpired = isTimeExpired
	view.IsQuotaExpired = isQuotaExpired

	view.LoanActive = sub.LoanActive
	if sub.LoanActive {
		view.LoanHours = sub.LoanHours
		if sub.LoanQuotaBytes > 0 {
			view.LoanQuotaGB = float64(sub.LoanQuotaBytes) / (1024 * 1024 * 1024)
		}
		view.LoanDeductionType = sub.LoanDeductionType
		if sub.LoanGrantedAt.Valid {
			view.LoanGrantedAt = sub.LoanGrantedAt.Time.Format("2006-01-02 15:04")
		}
		view.CanRequestLoan = false
	} else {
		view.LoanHours = prof.LoanHours
		view.LoanQuotaGB = prof.LoanQuotaGB
		view.LoanDeductionType = prof.LoanDeductionType
		// Loan is only eligible / visible if subscriber is expired in time or quota
		isExpired := isTimeExpired || isQuotaExpired
		view.CanRequestLoan = isExpired && settings.AllowLoanRequest && prof.LoanEnabled
	}

	return view, nil
}

func (d *DB) UpdateSubscriberPassword(username, newPass string) error {
	username = strings.TrimSpace(username)
	newPass = strings.TrimSpace(newPass)
	if username == "" || newPass == "" {
		return fmt.Errorf("اسم المستخدم وكلمة المرور مطلوبان")
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update Cleartext-Password in FreeRADIUS radcheck
	_, _ = tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", username)
	_, err = tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)", username, newPass)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Disconnect session via CoA so subscriber reconnects with new password
	_ = d.DisconnectSubscriberSession(username)
	return nil
}





