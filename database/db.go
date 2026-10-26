package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/crypto"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/mikrotik"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/money"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/radius"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/speedutil"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/whatsapp"
)

const (
	// RadacctRowBytesSQL calculates total 64-bit octets for a single radacct row including gigawords and null safety
	RadacctRowBytesSQL = `((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED) + (CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED))`

	// RadacctAliasedRowBytesSQL calculates total 64-bit octets for an aliased (r.) radacct row
	RadacctAliasedRowBytesSQL = `((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED) + (CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED))`

	// RadacctSumBytesSQL aggregates total consumed bytes across radacct sessions
	RadacctSumBytesSQL = `COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED) + (CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0)`

	// RadacctAliasedSumBytesSQL aggregates total consumed bytes across aliased (r.) radacct sessions
	RadacctAliasedSumBytesSQL = `COALESCE(SUM((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED) + (CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)), 0)`
)

type DB struct {
	*sql.DB
	Driver string
}

var (
	metricsMu         sync.RWMutex
	cachedMetrics     *models.DashboardMetrics
	cachedMetricsTime time.Time

	loginSummaryMu         sync.RWMutex
	cachedLoginSummary     *models.SubscriberLoginSummary
	cachedLoginSummaryTime time.Time

	topConsumersMu         sync.RWMutex
	cachedTopConsumers     = make(map[string][]models.TopConsumerItem)
	cachedTopConsumersTime = make(map[string]time.Time)

	totalLoginsMu         sync.RWMutex
	cachedTotalLogins     int
	cachedTotalLoginsTime time.Time
)

func Connect(cfg *config.Config) (*DB, error) {
	driver := cfg.DBDriver
	dsn := cfg.DBDSN

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(30)
	db.SetConnMaxIdleTime(5 * time.Minute)
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
			nasipaddress VARCHAR(45) NOT NULL DEFAULT '',
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
			acctinputoctets BIGINT DEFAULT 0,
			acctoutputoctets BIGINT DEFAULT 0,
			acctinputgigawords INT UNSIGNED DEFAULT 0,
			acctoutputgigawords INT UNSIGNED DEFAULT 0,
			normalized_input_bytes BIGINT UNSIGNED DEFAULT 0,
			normalized_output_bytes BIGINT UNSIGNED DEFAULT 0,
			calledstationid VARCHAR(50) NOT NULL DEFAULT '',
			callingstationid VARCHAR(50) NOT NULL DEFAULT '',
			acctterminatecause VARCHAR(32) NOT NULL DEFAULT '',
			servicetype VARCHAR(32) DEFAULT NULL,
			framedprotocol VARCHAR(32) DEFAULT NULL,
			framedipaddress VARCHAR(45) NOT NULL DEFAULT '',
			disconnect_status VARCHAR(32) DEFAULT '',
			disconnect_pending_at DATETIME NULL,
			INDEX idx_radacct_active (acctstoptime, username),
			INDEX idx_radacct_session (acctsessionid),
			INDEX idx_radacct_user (username),
			INDEX idx_radacct_disconnect (disconnect_status, disconnect_pending_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

		`CREATE TABLE IF NOT EXISTS subscriber_session_baselines (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) NOT NULL,
			acctsessionid VARCHAR(64) NOT NULL,
			baseline_input_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
			baseline_output_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_ssb_user (username),
			INDEX idx_ssb_user_sess (username, acctsessionid)
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
			balance DECIMAL(10, 2) DEFAULT 0.00,
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

	// Run versioned schema migrations
	if err := d.RunMigrations(); err != nil {
		return fmt.Errorf("financial schema migrations failed: %w", err)
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
		`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS balance DECIMAL(10, 2) DEFAULT 0.00;`,
		`ALTER TABLE subscriber_portal_settings ADD COLUMN IF NOT EXISTS show_balance TINYINT(1) DEFAULT 1;`,
		`ALTER TABLE subscriber_portal_settings ADD COLUMN IF NOT EXISTS allow_balance_renewal TINYINT(1) DEFAULT 1;`,
		`ALTER TABLE radacct MODIFY COLUMN nasipaddress VARCHAR(45) NOT NULL DEFAULT '';`,
		`ALTER TABLE radacct MODIFY COLUMN framedipaddress VARCHAR(45) NOT NULL DEFAULT '';`,
		`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS acctinputgigawords INT UNSIGNED DEFAULT 0;`,
		`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS acctoutputgigawords INT UNSIGNED DEFAULT 0;`,
		`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS normalized_input_bytes BIGINT UNSIGNED DEFAULT 0;`,
		`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS normalized_output_bytes BIGINT UNSIGNED DEFAULT 0;`,
		`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS disconnect_status VARCHAR(32) DEFAULT '';`,
		`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS disconnect_pending_at DATETIME NULL;`,
	}
	for _, m := range migrations {
		if _, err := d.Exec(m); err != nil {
			return fmt.Errorf("schema migration failed: %w\nSQL: %s", err, m)
		}
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
		{
			key:  "loan_granted",
			name: "منح سلفة فترة سماح",
			text: "مرحباً {name}، تم منحك سلفة فترة سماح بنجاح! المدة الممنوحة: {loan_hours} ساعة، والكوتة: {loan_quota} جيجا. تنتهي السلفة بتاريخ {expiry_date}. يرجى التجديد قبل انتهاء المدة.",
		},
		{
			key:  "balance_added",
			name: "شحن رصيد بالمحفظة",
			text: "مرحباً {name}، تم إيداع مبلغ {amount} ج.م في محفظتكم بنجاح. رصيدك الحالي: {balance} ج.م.",
		},
		{
			key:  "renew_from_balance",
			name: "تجديد الباقة من الرصيد",
			text: "مرحباً {name}، تم تجديد اشتراككم بنجاح على باقة ({profile}) وخصم {amount} ج.م من رصيد محفظتكم. رصيدك المتبقي: {balance} ج.م. تاريخ الانتهاء الجديد: {expiry_date}.",
		},
		{
			key:  "debt_settled_from_balance",
			name: "سداد دين من رصيد المحفظة",
			text: "مرحباً {name}، تم سداد مبلغ {amount} ج.م من مديونيتكم خصماً من رصيد المحفظة. المتبقي من الدين: {debt} ج.م. رصيدك المتبقي بالمحفظة: {balance} ج.م.",
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
		initAdminPass := strings.TrimSpace(os.Getenv("INITIAL_ADMIN_PASSWORD"))
		if initAdminPass == "" {
			initAdminPass = crypto.GenerateRandomPassword(16)
			envPath := ".env"
			if _, err := os.Stat("/opt/radius-server/.env"); err == nil {
				envPath = "/opt/radius-server/.env"
			}
			f, err := os.OpenFile(envPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err == nil {
				_, _ = f.WriteString(fmt.Sprintf("\nINITIAL_ADMIN_PASSWORD=%s\n", initAdminPass))
				_ = f.Close()
			}
			log.Printf("[SECURITY] Created initial admin with random password. Stored securely in %s", envPath)
		}
		admin := &models.Admin{
			Username: "admin",
			Role:     "admin",
			IsActive: true,
		}
		_ = admin.SetPassword(initAdminPass)
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

		_, _ = d.Exec(`INSERT INTO card_templates (name, is_default, header_title, footer_text, theme_color, html_content, card_width, card_height, cols, `+"`rows`"+`, card_gap) VALUES
			('القالب الأزرق الحديث (افتراضي)', 1, 'كارت إنترنت', 'أهلاً بكم - نتمنى لكم وقتاً ممتعاً', '#0d6efd', ?, '240px', '140px', 3, 7, '2mm'),
			('قالب كافيه فخم (ذهبي داكن)', 0, 'VIP Wi-Fi Voucher', 'Welcome to our cafe - Enjoy high speed internet', '#d4af37', ?, '240px', '140px', 3, 7, '2mm'),
			('قالب رول كاشير (حراري POS)', 0, 'إيصال شبكة واي فاي', 'يرجى الاحتفاظ بالكود لحين انتهاء الجلسة', '#212529', ?, '240px', '140px', 3, 7, '2mm'),
			('قالب ميني اقتصادي (أخضر خفيف)', 0, 'كارت إنترنت سريع', 'شكراً لزيارتكم', '#198754', ?, '240px', '140px', 3, 7, '2mm')
			ON DUPLICATE KEY UPDATE html_content = VALUES(html_content)`, defaultHtml, luxuryHtml, thermalHtml, miniHtml)
	}

	// Seed default SSTP Settings
	var sstpCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM sstp_settings").Scan(&sstpCount)
	if sstpCount == 0 {
		_, _ = d.Exec(`INSERT INTO sstp_settings (enabled, port, gateway_ip, ip_pool)
			VALUES (1, 4444, '10.10.10.1', '10.10.10.2-254')`)
	}

	// Seed default NAS with cryptographically random secrets
	var nasCount int
	_ = d.QueryRow("SELECT COUNT(*) FROM nas").Scan(&nasCount)
	if nasCount == 0 {
		mainSecret := crypto.GenerateRandomSecret(16)
		localSecret := crypto.GenerateRandomSecret(16)
		_, _ = d.Exec(`INSERT INTO nas (nasname, shortname, type, secret, description, coa_port)
			VALUES ('192.168.2.1', 'MikroTik-Main', 'mikrotik', ?, 'Default MikroTik CCR/RB', 3799),
			       ('127.0.0.1', 'Localhost', 'other', ?, 'Testing NAS', 3799);`, mainSecret, localSecret)
	}

	d.RotateLegacyDefaultSecrets()

	return nil
}

// VerifyNoDefaultCredentials ensures that no default credentials remain active
func (d *DB) VerifyNoDefaultCredentials() error {
	rows, err := d.Query("SELECT id, username, password_hash FROM admins")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var username, hash string
		if err := rows.Scan(&id, &username, &hash); err == nil {
			admin := models.Admin{PasswordHash: hash}
			if admin.CheckPassword("admin123") {
				return fmt.Errorf("CRITICAL SECURITY ERROR: Admin '%s' is still using the default password 'admin123'. System startup refused. Please set a secure password.", username)
			}
		}
	}
	return nil
}

// RotateLegacyDefaultSecrets replaces any legacy hardcoded secrets (e.g. radius123) with secure random secrets
func (d *DB) RotateLegacyDefaultSecrets() int {
	rows, err := d.Query("SELECT id, nasname FROM nas WHERE secret = 'radius123' OR secret = ''")
	if err != nil {
		return 0
	}
	defer rows.Close()

	rotated := 0
	type item struct {
		id      int64
		nasname string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.nasname); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		newSecret := crypto.GenerateRandomSecret(16)
		if _, err := d.Exec("UPDATE nas SET secret = ? WHERE id = ?", newSecret, it.id); err == nil {
			log.Printf("[SECURITY] Rotated insecure default secret on NAS '%s' to random secure secret.", it.nasname)
			rotated++
		}
	}
	return rotated
}

// --- Metrics & Dashboard ---

func (d *DB) GetMetrics() (*models.DashboardMetrics, error) {
	metricsMu.RLock()
	if cachedMetrics != nil && time.Since(cachedMetricsTime) < 3*time.Second {
		cp := *cachedMetrics
		metricsMu.RUnlock()
		return &cp, nil
	}
	metricsMu.RUnlock()

	m := &models.DashboardMetrics{}

	// 1. Consolidated Subscribers Query
	_ = d.QueryRow(`SELECT 
		COUNT(*),
		COALESCE(SUM(CASE WHEN status = 'active' AND (expiration_date IS NULL OR expiration_date >= NOW()) THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'expired' OR (expiration_date IS NOT NULL AND expiration_date < NOW()) THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'active' AND expiration_date IS NOT NULL AND expiration_date BETWEEN NOW() AND DATE_ADD(NOW(), INTERVAL 3 DAY) THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(debt), 0)
	FROM subscribers`).Scan(
		&m.TotalSubscribers,
		&m.ActiveSubscribers,
		&m.ExpiredSubscribers,
		&m.ExpiringSoonSubscribers,
		&m.TotalDebts,
	)

	// Fallback to radcheck if subscribers table is empty
	if m.TotalSubscribers == 0 {
		_ = d.QueryRow("SELECT COUNT(DISTINCT username) FROM radcheck").Scan(&m.TotalSubscribers)
	}

	// 2. Active Online Sessions (uses idx_radacct_active)
	_ = d.QueryRow("SELECT COUNT(*) FROM radacct WHERE acctstoptime IS NULL").Scan(&m.OnlineSessions)

	// Online Subscribers vs Online Cards (scans only active sessions, NOT 220k rows!)
	_ = d.QueryRow(`SELECT 
		COALESCE(SUM(CASE WHEN s.username IS NOT NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN c.username IS NOT NULL THEN 1 ELSE 0 END), 0)
	FROM radacct r
	LEFT JOIN subscribers s ON r.username = s.username
	LEFT JOIN cards c ON r.username = c.username
	WHERE r.acctstoptime IS NULL`).Scan(&m.OnlineSubscribers, &m.OnlineCards)

	if m.OnlineSubscribers == 0 && m.OnlineSessions > 0 {
		if m.OnlineCards > 0 {
			m.OnlineSubscribers = m.OnlineSessions - m.OnlineCards
			if m.OnlineSubscribers < 0 {
				m.OnlineSubscribers = 0
			}
		} else {
			m.OnlineSubscribers = m.OnlineSessions
		}
	}

	// 3. Consolidated Cards Query
	_ = d.QueryRow(`SELECT 
		COUNT(*),
		COALESCE(SUM(CASE WHEN status = 'available' THEN 1 ELSE 0 END), 0)
	FROM cards`).Scan(&m.TotalCards, &m.ActiveCards)

	// 4. Financials (Today's Revenue)
	_ = d.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM billing_transactions WHERE DATE(created_at) = CURDATE() AND payment_status = 'paid'").Scan(&m.TodayRevenue)
	if m.TodayRevenue == 0 {
		_ = d.QueryRow("SELECT COALESCE(SUM(total_price), 0) FROM batches WHERE DATE(created_at) = CURDATE()").Scan(&m.TodayRevenue)
	}

	// 5. Infrastructure & General
	_ = d.QueryRow("SELECT COUNT(*) FROM resellers WHERE is_active = 1").Scan(&m.TotalResellers)
	_ = d.QueryRow("SELECT COUNT(*) FROM nas").Scan(&m.TotalRouters)
	_ = d.QueryRow("SELECT COALESCE(SUM(total_price), 0) FROM batches").Scan(&m.TotalRevenue)

	metricsMu.Lock()
	cachedMetrics = m
	cachedMetricsTime = time.Now()
	metricsMu.Unlock()

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

	// 1. Prevent duplicate entry overwriting existing subscriber data
	var existingID int64
	err = tx.QueryRow("SELECT id FROM subscribers WHERE username = ?", sub.Username).Scan(&existingID)
	if err == nil && existingID > 0 {
		return fmt.Errorf("اسم المشترك '%s' مسجل بالفعل في النظام، لا يمكن تكراره", sub.Username)
	}

	query := `INSERT INTO subscribers (username, contract_no, full_name, phone, address, debt, balance, profile_name, static_ip, calling_mac, quota_bytes, expiration_date, status, reseller_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var expDate interface{}
	if sub.ExpirationDate.Valid {
		expDate = sub.ExpirationDate.Time
	}

	_, err = tx.Exec(query, sub.Username, sub.ContractNo, sub.FullName, sub.Phone, sub.Address, sub.Debt, sub.Balance,
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

func (d *DB) generateAdvancedBatchTx(tx *sql.Tx, opts models.GenerateBatchOptions, prof *models.Profile) (string, int, error) {
	if opts.Count <= 0 || opts.Count > 50000 {
		return "", 0, fmt.Errorf("عدد الكروت غير صحيح (يجب أن يكون بين 1 و 50000)")
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
		return "", 0, fmt.Errorf("failed to insert batch: %w", err)
	}
	batchID, err := res.LastInsertId()
	if err != nil {
		return "", 0, fmt.Errorf("failed to get batch ID: %w", err)
	}

	// 2. Prepare bulk insert statements
	stmtCard, err := tx.Prepare(`INSERT INTO cards (
		batch_id, username, password, profile_id, reseller_id, status, cost_price, price,
		card_type, nas_id, quota_bytes, time_limit_seconds, time_calc_mode,
		simultaneous_sessions, stop_on_quota, bind_mac, lock_mac, convert_mac_after_session, original_code, is_mac_converted, service_name, created_at
	) VALUES (?, ?, ?, ?, ?, 'available', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`)
	if err != nil {
		return "", 0, fmt.Errorf("failed to prepare card statement: %w", err)
	}
	defer stmtCard.Close()

	stmtRadCheck, err := tx.Prepare(`INSERT INTO radcheck (username, attribute, op, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return "", 0, fmt.Errorf("failed to prepare radcheck statement: %w", err)
	}
	defer stmtRadCheck.Close()

	stmtRadReply, err := tx.Prepare(`INSERT INTO radreply (username, attribute, op, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return "", 0, fmt.Errorf("failed to prepare radreply statement: %w", err)
	}
	defer stmtRadReply.Close()

	stmtRadGroup, err := tx.Prepare(`INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)`)
	if err != nil {
		return "", 0, fmt.Errorf("failed to prepare radusergroup statement: %w", err)
	}
	defer stmtRadGroup.Close()

	created := 0
	for i := 0; i < opts.Count; i++ {
		var u, p string
		cardInserted := false

		// Retry with collision prevention
		for attempt := 0; attempt < 5; attempt++ {
			var randPart string
			if opts.Format == "num" {
				randPart = randomNumeric(opts.CharLen)
			} else if opts.Format == "alpha" {
				randPart = randomLetters(opts.CharLen)
			} else {
				randPart = randomAlphanumeric(opts.CharLen)
			}

			if opts.AffixType == "suffix" {
				u = randPart + opts.AffixValue
			} else {
				u = opts.AffixValue + randPart
			}
			p = u

			if _, err := stmtCard.Exec(batchID, u, p, opts.ProfileID, opts.ResellerID, opts.CostPricePerCard, opts.PricePerCard,
				opts.CardType, opts.NASID, opts.QuotaBytes, opts.TimeLimitSeconds, opts.TimeCalcMode,
				opts.SimultaneousSessions, opts.StopOnQuota, opts.BindMAC, opts.LockMAC, opts.ConvertMACAfterSession, u, 0, opts.ServiceName); err == nil {
				cardInserted = true
				break
			}
		}

		if !cardInserted {
			return "", 0, fmt.Errorf("فشل توليد اسم مستخدم فريد للكارت بعد عدة محاولات (تكرار)")
		}

		// radcheck: Cleartext-Password
		if _, err := stmtRadCheck.Exec(u, "Cleartext-Password", ":=", p); err != nil {
			return "", 0, fmt.Errorf("failed to insert radcheck password for %s: %w", u, err)
		}

		// radcheck: Simultaneous-Use
		if opts.SimultaneousSessions > 0 {
			if _, err := stmtRadCheck.Exec(u, "Simultaneous-Use", ":=", strconv.Itoa(opts.SimultaneousSessions)); err != nil {
				return "", 0, fmt.Errorf("failed to insert radcheck simultaneous use for %s: %w", u, err)
			}
		}

		// radcheck: Called-Station-Id
		if opts.ServiceName != "" && opts.ServiceName != "all" {
			if _, err := stmtRadCheck.Exec(u, "Called-Station-Id", "=~", opts.ServiceName); err != nil {
				return "", 0, fmt.Errorf("failed to insert radcheck service name for %s: %w", u, err)
			}
		}

		// radcheck: Max-All-Session
		if opts.TimeCalcMode == "by_second" && opts.TimeLimitSeconds > 0 {
			if _, err := stmtRadCheck.Exec(u, "Max-All-Session", ":=", strconv.FormatInt(opts.TimeLimitSeconds, 10)); err != nil {
				return "", 0, fmt.Errorf("failed to insert radcheck max session for %s: %w", u, err)
			}
		}

		// radreply: Mikrotik-Total-Limit
		if opts.QuotaBytes > 0 && opts.StopOnQuota {
			if _, err := stmtRadReply.Exec(u, "Mikrotik-Total-Limit", ":=", strconv.FormatInt(opts.QuotaBytes, 10)); err != nil {
				return "", 0, fmt.Errorf("failed to insert radreply total limit for %s: %w", u, err)
			}
		}

		// radreply: Session-Timeout
		if opts.TimeLimitSeconds > 0 {
			if _, err := stmtRadReply.Exec(u, "Session-Timeout", ":=", strconv.FormatInt(opts.TimeLimitSeconds, 10)); err != nil {
				return "", 0, fmt.Errorf("failed to insert radreply session timeout for %s: %w", u, err)
			}
		}

		// radusergroup: link to profile
		if _, err := stmtRadGroup.Exec(u, prof.Name); err != nil {
			return "", 0, fmt.Errorf("failed to insert radusergroup for %s: %w", u, err)
		}

		created++
	}

	return batchCode, created, nil
}

func (d *DB) GenerateAdvancedBatch(opts models.GenerateBatchOptions) (string, int, error) {
	var prof models.Profile
	err := d.QueryRow("SELECT id, name, display_name, cost_price, price, quota_bytes, uptime_seconds, validity_value, validity_unit FROM profiles WHERE id = ?", opts.ProfileID).Scan(
		&prof.ID, &prof.Name, &prof.DisplayName, &prof.CostPrice, &prof.Price, &prof.QuotaBytes, &prof.UptimeSeconds, &prof.ValidityValue, &prof.ValidityUnit)
	if err != nil {
		return "", 0, fmt.Errorf("profile not found: %w", err)
	}

	tx, err := d.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	batchCode, created, err := d.generateAdvancedBatchTx(tx, opts, &prof)
	if err != nil {
		return "", 0, err
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
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id) AS subs_count,
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id AND s.debt > 0) AS debtors_count,
		(SELECT COALESCE(SUM(s.debt), 0.00) FROM subscribers s WHERE s.reseller_id = r.id AND s.debt > 0) AS subs_debt
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
			&res.SubscribersCount, &res.DebtorsCount, &res.SubscribersDebt,
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
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id) AS subs_count,
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id AND s.debt > 0) AS debtors_count,
		(SELECT COALESCE(SUM(s.debt), 0.00) FROM subscribers s WHERE s.reseller_id = r.id AND s.debt > 0) AS subs_debt
	FROM resellers r
	WHERE r.id = ?`

	var res models.Reseller
	var permsJSON string
	err := d.QueryRow(query, id).Scan(
		&res.ID, &res.Name, &res.Username, &res.Phone, &res.Balance, &res.CreditLimit,
		&res.Debt, &res.CommissionPercent, &permsJSON, &res.Notes, &res.IsActive, &res.CreatedAt,
		&res.SubscribersCount, &res.DebtorsCount, &res.SubscribersDebt,
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
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id) AS subs_count,
		(SELECT COUNT(*) FROM subscribers s WHERE s.reseller_id = r.id AND s.debt > 0) AS debtors_count,
		(SELECT COALESCE(SUM(s.debt), 0.00) FROM subscribers s WHERE s.reseller_id = r.id AND s.debt > 0) AS subs_debt
	FROM resellers r
	WHERE r.username = ?`

	var res models.Reseller
	var permsJSON string
	err := d.QueryRow(query, username).Scan(
		&res.ID, &res.Name, &res.Username, &res.Phone, &res.Balance, &res.CreditLimit,
		&res.Debt, &res.CommissionPercent, &permsJSON, &res.Notes, &res.IsActive, &res.CreatedAt,
		&res.SubscribersCount, &res.DebtorsCount, &res.SubscribersDebt,
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

func (d *DB) AdjustResellerBalance(resellerID int64, actionType string, amount float64, notes, adminUser string, idempotencyKey ...string) error {
	if err := money.ValidateFinite(amount); err != nil || amount <= 0 {
		return fmt.Errorf("المبلغ يجب أن يكون رقماً موجباً أكبر من صفر")
	}

	var idempKey string
	if len(idempotencyKey) > 0 {
		idempKey = strings.TrimSpace(idempotencyKey[0])
	}
	if idempKey == "" {
		idempKey = GenerateDeterministicKey("reseller_adjust", fmt.Sprintf("%d:%s:%.2f", resellerID, actionType, amount))
	}

	reqHash := ComputeRequestHash(map[string]interface{}{
		"reseller_id": resellerID,
		"action":      actionType,
		"amount":      amount,
	})

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Reserve idempotency key inside tx
	acquired, _, _, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "reseller_adjust", fmt.Sprintf("%d", resellerID), reqHash, 24*time.Hour)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}

	var (
		resName        string
		resBalance     float64
		resCreditLimit float64
		resDebt        float64
	)
	err = tx.QueryRow("SELECT name, balance, credit_limit, COALESCE(debt, 0.00) FROM resellers WHERE id = ? FOR UPDATE", resellerID).Scan(
		&resName, &resBalance, &resCreditLimit, &resDebt,
	)
	if err != nil {
		return fmt.Errorf("الموزع غير موجود: %w", err)
	}

	amountCents := money.ToCents(amount)
	balCents := money.ToCents(resBalance)
	debtCents := money.ToCents(resDebt)
	limitCents := money.ToCents(resCreditLimit)

	newBalCents := balCents
	newDebtCents := debtCents
	actualAmountCents := amountCents

	switch actionType {
	case "deposit":
		newBalCents = balCents + amountCents
	case "withdraw":
		if balCents < amountCents {
			return fmt.Errorf("رصيد الموزع غير كافٍ للسحب (الرصيد الحالي: %s، المطلوب: %s)", money.FormatEGP(balCents), money.FormatEGP(amountCents))
		}
		newBalCents = balCents - amountCents
	case "pay_debt":
		if debtCents <= 0 {
			return fmt.Errorf("لا توجد مديونية مسجلة على الموزع لسدادها")
		}
		if amountCents > debtCents {
			actualAmountCents = debtCents
		}
		newDebtCents = debtCents - actualAmountCents
	case "pay_debt_from_balance":
		if debtCents <= 0 {
			return fmt.Errorf("لا توجد مديونية مسجلة على الموزع لسدادها")
		}
		if amountCents > debtCents {
			actualAmountCents = debtCents
		}
		if balCents < actualAmountCents {
			return fmt.Errorf("رصيد المحفظة غير كافٍ لسداد الدين (المتاح: %s، المطلوب: %s)", money.FormatEGP(balCents), money.FormatEGP(actualAmountCents))
		}
		newBalCents = balCents - actualAmountCents
		newDebtCents = debtCents - actualAmountCents
	case "add_debt":
		if limitCents > 0 && debtCents+amountCents > limitCents {
			return fmt.Errorf("لا يمكن إضافة دين يتجاوز الحد الائتماني للموزع (%s). المديونية الحالية: %s، الحد الأقصى: %s",
				money.FormatEGP(limitCents), money.FormatEGP(debtCents), money.FormatEGP(limitCents))
		}
		newDebtCents = debtCents + amountCents
	default:
		return fmt.Errorf("نوع العملية غير صالح: %s", actionType)
	}

	newBalance := money.FromCents(newBalCents)
	newDebt := money.FromCents(newDebtCents)
	actualAmount := money.FromCents(actualAmountCents)

	updateRes, err := tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newBalance, newDebt, resellerID)
	if err != nil {
		return err
	}
	rowsAff, _ := updateRes.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("failed to update reseller balance: record not found")
	}

	if adminUser == "" {
		adminUser = "admin"
	}
	if notes == "" {
		switch actionType {
		case "deposit":
			notes = fmt.Sprintf("شحن رصيد للموزع %s بقيمة %s", resName, money.FormatEGP(actualAmountCents))
		case "withdraw":
			notes = fmt.Sprintf("خصم / سحب رصيد من الموزع %s بقيمة %s", resName, money.FormatEGP(actualAmountCents))
		case "pay_debt":
			notes = fmt.Sprintf("سداد مديونية نقداً للموزع %s بقيمة %s", resName, money.FormatEGP(actualAmountCents))
		case "pay_debt_from_balance":
			notes = fmt.Sprintf("سداد مديونية من رصيد المحفظة للموزع %s بقيمة %s", resName, money.FormatEGP(actualAmountCents))
		case "add_debt":
			notes = fmt.Sprintf("إضافة مديونية على الموزع %s بقيمة %s", resName, money.FormatEGP(actualAmountCents))
		}
	}

	refID := GenerateReferenceID("RES-ADJ")

	_, err = tx.Exec(`INSERT INTO reseller_transactions 
		(reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, notes, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resellerID, actionType, actualAmount, resBalance, newBalance, resDebt, newDebt, notes, adminUser)
	if err != nil {
		return err
	}

	frec := &FinancialRecord{
		ReferenceID:    refID,
		IdempotencyKey: idempKey,
		OperationType:  "reseller_" + actionType,
		AccountType:    "reseller",
		ResellerID:     resellerID,
		AmountCents:    actualAmountCents,
		Amount:         actualAmount,
		BalanceBefore:  resBalance,
		BalanceAfter:   newBalance,
		DebtBefore:     resDebt,
		DebtAfter:      newDebt,
		Status:         "completed",
		Description:    notes,
		CreatedBy:      adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return err
	}

	// Record audit log for admin adjustment
	auditRec := &AuditLogRecord{
		ReferenceID:   refID,
		ActorUsername: adminUser,
		ActorRole:     "admin",
		Action:        "reseller_adjust_" + actionType,
		TargetType:    "reseller",
		TargetID:      fmt.Sprintf("%d", resellerID),
		Details:       notes,
	}
	if err := d.RecordAuditTx(tx, auditRec); err != nil {
		return err
	}

	// 2. Complete idempotency key
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"success":true,"reference_id":"%s"}`, refID)); err != nil {
		return err
	}

	return tx.Commit()
}

func (d *DB) GetResellerTransactionsFiltered(resellerID int64, txType, startDate, endDate, search string, limit, offset int) ([]models.ResellerTransaction, int, map[string]float64, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	whereClause := " WHERE 1=1"
	var args []interface{}

	if resellerID > 0 {
		whereClause += " AND t.reseller_id = ?"
		args = append(args, resellerID)
	}

	if txType != "" && txType != "all" {
		whereClause += " AND t.type = ?"
		args = append(args, txType)
	}

	if startDate != "" {
		whereClause += " AND t.created_at >= ?"
		args = append(args, startDate+" 00:00:00")
	}

	if endDate != "" {
		whereClause += " AND t.created_at <= ?"
		args = append(args, endDate+" 23:59:59")
	}

	if search != "" {
		whereClause += " AND (t.notes LIKE ? OR t.subscriber_name LIKE ? OR r.name LIKE ? OR r.username LIKE ? OR t.created_by LIKE ?)"
		sArg := "%" + search + "%"
		args = append(args, sArg, sArg, sArg, sArg, sArg)
	}

	// 1. Count total
	countQuery := "SELECT COUNT(*) FROM reseller_transactions t LEFT JOIN resellers r ON t.reseller_id = r.id" + whereClause
	var totalCount int
	_ = d.QueryRow(countQuery, args...).Scan(&totalCount)

	// 2. Compute financial breakdown stats for the filtered set
	statsQuery := `SELECT 
		COALESCE(SUM(CASE WHEN t.type = 'deposit' THEN t.amount ELSE 0 END), 0.00),
		COALESCE(SUM(CASE WHEN t.type = 'renew' THEN t.amount ELSE 0 END), 0.00),
		COALESCE(SUM(CASE WHEN t.type = 'generate_cards' THEN t.amount ELSE 0 END), 0.00),
		COALESCE(SUM(CASE WHEN t.type = 'quota' THEN t.amount ELSE 0 END), 0.00),
		COALESCE(SUM(CASE WHEN t.type IN ('pay_debt', 'pay_debt_from_balance') THEN t.amount ELSE 0 END), 0.00),
		COALESCE(SUM(CASE WHEN t.type = 'withdraw' THEN t.amount ELSE 0 END), 0.00),
		COALESCE(SUM(CASE WHEN t.type = 'subscriber_deposit' THEN t.amount ELSE 0 END), 0.00),
		COALESCE(SUM(CASE WHEN t.type IN ('renew', 'generate_cards', 'quota', 'withdraw', 'subscriber_deposit') THEN t.amount ELSE 0 END), 0.00)
	FROM reseller_transactions t
	LEFT JOIN resellers r ON t.reseller_id = r.id` + whereClause

	stats := make(map[string]float64)
	var totDep, totRenew, totCards, totQuota, totDebtPaid, totWith, totSubDep, totSpent float64
	_ = d.QueryRow(statsQuery, args...).Scan(&totDep, &totRenew, &totCards, &totQuota, &totDebtPaid, &totWith, &totSubDep, &totSpent)
	stats["TotalDeposits"] = totDep
	stats["TotalRenewals"] = totRenew
	stats["TotalCards"] = totCards
	stats["TotalQuotas"] = totQuota
	stats["TotalDebtPaid"] = totDebtPaid
	stats["TotalWithdrawals"] = totWith
	stats["TotalSubscriberDeposits"] = totSubDep
	stats["TotalSpent"] = totSpent

	// If single reseller, also provide current balance and debt in stats
	if resellerID > 0 {
		var curBal, curDebt float64
		_ = d.QueryRow("SELECT COALESCE(balance, 0.00), COALESCE(debt, 0.00) FROM resellers WHERE id = ?", resellerID).Scan(&curBal, &curDebt)
		stats["CurrentBalance"] = curBal
		stats["CurrentDebt"] = curDebt
	}

	// 3. Select transactions
	query := `SELECT 
		t.id, t.reseller_id, COALESCE(r.name, ''), t.type, t.amount,
		t.balance_before, t.balance_after, t.debt_before, t.debt_after,
		t.subscriber_id, COALESCE(t.subscriber_name, ''),
		t.notes, t.created_by, t.created_at
	FROM reseller_transactions t
	LEFT JOIN resellers r ON t.reseller_id = r.id` + whereClause + " ORDER BY t.id DESC LIMIT ? OFFSET ?"

	queryArgs := append(args, limit, offset)
	rows, err := d.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, stats, err
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

	return txs, totalCount, stats, nil
}

func (d *DB) GetResellerTransactions(resellerID int64, limit int) ([]models.ResellerTransaction, error) {
	txs, _, _, err := d.GetResellerTransactionsFiltered(resellerID, "", "", "", "", limit, 0)
	return txs, err
}

func (d *DB) GetResellerFinancialSummary() (map[string]interface{}, error) {
	summary := make(map[string]interface{})

	var totalResellers, activeResellers, totalSubs int
	var totalBalance, totalDebt float64

	_ = d.QueryRow("SELECT COUNT(*), COALESCE(SUM(balance), 0.00), COALESCE(SUM(debt), 0.00) FROM resellers").Scan(&totalResellers, &totalBalance, &totalDebt)
	_ = d.QueryRow("SELECT COUNT(*) FROM resellers WHERE is_active = 1").Scan(&activeResellers)
	_ = d.QueryRow("SELECT COUNT(*) FROM subscribers WHERE reseller_id > 0").Scan(&totalSubs)

	var totalSubsDebt float64
	var totalDebtors int
	_ = d.QueryRow("SELECT COUNT(*), COALESCE(SUM(debt), 0.00) FROM subscribers WHERE reseller_id > 0 AND debt > 0").Scan(&totalDebtors, &totalSubsDebt)

	var todayTxCount int
	var todayTxAmount float64
	_ = d.QueryRow("SELECT COUNT(*), COALESCE(SUM(amount), 0.00) FROM reseller_transactions WHERE DATE(created_at) = CURDATE()").Scan(&todayTxCount, &todayTxAmount)

	summary["TotalResellers"] = totalResellers
	summary["ActiveResellers"] = activeResellers
	summary["TotalBalance"] = totalBalance
	summary["TotalDebt"] = totalDebt
	summary["TotalSubscribers"] = totalSubs
	summary["TotalSubscribersDebt"] = totalSubsDebt
	summary["TotalDebtors"] = totalDebtors
	summary["TodayTxCount"] = todayTxCount
	summary["TodayTxAmount"] = todayTxAmount

	return summary, nil
}

// GetResellersDebtSummary returns aggregated debtor subscriber stats grouped by reseller
func (d *DB) GetResellersDebtSummary() ([]models.ResellerDebtSummary, float64, int, error) {
	query := `SELECT 
		r.id, 
		r.name, 
		COALESCE(r.phone, ''), 
		COALESCE(r.balance, 0.00), 
		COALESCE(r.debt, 0.00),
		COUNT(s.id) as debtors_count,
		COALESCE(SUM(s.debt), 0.00) as total_debt
	FROM resellers r
	JOIN subscribers s ON s.reseller_id = r.id
	WHERE s.debt > 0
	GROUP BY r.id, r.name, r.phone, r.balance, r.debt
	ORDER BY total_debt DESC`

	rows, err := d.Query(query)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var list []models.ResellerDebtSummary
	var grandTotalDebt float64
	var grandTotalDebtors int

	for rows.Next() {
		var item models.ResellerDebtSummary
		if err := rows.Scan(
			&item.ResellerID, &item.ResellerName, &item.ResellerPhone,
			&item.ResellerBalance, &item.ResellerDebt,
			&item.DebtorsCount, &item.TotalDebt,
		); err == nil {
			list = append(list, item)
			grandTotalDebt += item.TotalDebt
			grandTotalDebtors += item.DebtorsCount
		}
	}
	return list, grandTotalDebt, grandTotalDebtors, nil
}

// GetResellerIndebtedSubscribers retrieves subscribers with outstanding debt filtered by reseller and search
func (d *DB) GetResellerIndebtedSubscribers(resellerID int64, search string, limit, offset int) ([]models.ResellerSubscriberDebt, int, float64, error) {
	baseWhere := "WHERE s.debt > 0"
	var args []interface{}
	var countArgs []interface{}

	if resellerID > 0 {
		baseWhere += " AND s.reseller_id = ?"
		args = append(args, resellerID)
		countArgs = append(countArgs, resellerID)
	}
	if search != "" {
		baseWhere += " AND (s.username LIKE ? OR s.full_name LIKE ? OR s.phone LIKE ? OR s.contract_no LIKE ?)"
		sArg := "%" + search + "%"
		args = append(args, sArg, sArg, sArg, sArg)
		countArgs = append(countArgs, sArg, sArg, sArg, sArg)
	}

	var totalCount int
	var totalDebt float64
	countQuery := "SELECT COUNT(*), COALESCE(SUM(s.debt), 0.00) FROM subscribers s " + baseWhere
	_ = d.QueryRow(countQuery, countArgs...).Scan(&totalCount, &totalDebt)

	query := `SELECT 
		s.id, 
		s.username, 
		s.full_name, 
		COALESCE(s.phone, ''), 
		COALESCE(s.contract_no, ''), 
		COALESCE(s.profile_name, ''), 
		COALESCE(p.display_name, s.profile_name),
		s.debt, 
		COALESCE(s.balance, 0.00), 
		COALESCE(s.reseller_id, 0), 
		COALESCE(r.name, 'بدون موزع'), 
		s.expiration_date, 
		s.last_renewed_at, 
		COALESCE(s.status, 'active')
	FROM subscribers s
	LEFT JOIN resellers r ON s.reseller_id = r.id
	LEFT JOIN profiles p ON s.profile_name = p.name ` +
		baseWhere + ` ORDER BY s.debt DESC, s.id DESC LIMIT ? OFFSET ?`

	args = append(args, limit, offset)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var list []models.ResellerSubscriberDebt
	for rows.Next() {
		var item models.ResellerSubscriberDebt
		if err := rows.Scan(
			&item.ID, &item.Username, &item.FullName, &item.Phone,
			&item.ContractNo, &item.ProfileName, &item.ProfileDisplay,
			&item.Debt, &item.Balance, &item.ResellerID, &item.ResellerName,
			&item.ExpirationDate, &item.LastRenewedAt, &item.Status,
		); err == nil {
			list = append(list, item)
		}
	}
	return list, totalCount, totalDebt, nil
}

func (d *DB) ResellerRenewSubscriber(resellerID int64, username, profileName string, isCustomerPaid bool, adminUser string, idempotencyKey ...string) error {
	var idempKey string
	if len(idempotencyKey) > 0 {
		idempKey = strings.TrimSpace(idempotencyKey[0])
	}
	if idempKey == "" {
		idempKey = GenerateDeterministicKey("reseller_renew", fmt.Sprintf("%d:%s:%s", resellerID, username, profileName), isCustomerPaid)
	}

	reqHash := ComputeRequestHash(map[string]interface{}{
		"reseller_id": resellerID,
		"username":    username,
		"profile":     profileName,
		"is_paid":     isCustomerPaid,
	})

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Reserve idempotency key inside tx
	acquired, _, _, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "reseller_renew", fmt.Sprintf("%d:%s", resellerID, username), reqHash, 24*time.Hour)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}

	var (
		resName           string
		resBalance        float64
		resCreditLimit    float64
		resDebt           float64
		commissionPercent float64
		isActive          bool
		permsJSON         sql.NullString
	)

	err = tx.QueryRow(`SELECT name, balance, credit_limit, debt, commission_percent, is_active, permissions 
		FROM resellers WHERE id = ? FOR UPDATE`, resellerID).Scan(
		&resName, &resBalance, &resCreditLimit, &resDebt, &commissionPercent, &isActive, &permsJSON,
	)
	if err != nil {
		return fmt.Errorf("الموزع غير موجود: %w", err)
	}
	if !isActive {
		return fmt.Errorf("حساب الموزع معطل حالياً")
	}

	var perms models.ResellerPermissions
	if permsJSON.Valid && permsJSON.String != "" {
		_ = json.Unmarshal([]byte(permsJSON.String), &perms)
	}
	if !perms.CanRenew {
		return fmt.Errorf("ليس لديك صلاحية تجديد باقات المشتركين")
	}
	if err := money.ValidateCommission(commissionPercent); err != nil {
		return fmt.Errorf("نسبة عمولة الموزع غير صالحة: %w", err)
	}

	var (
		subID          int64
		subResellerID  int64
		currentProf    string
		contractNo     string
		fullName       string
		loanActive     bool
		subCurrDebt    float64
		subCurrBalance float64
	)
	err = tx.QueryRow(`SELECT id, reseller_id, profile_name, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(loan_active, 0), COALESCE(debt, 0.00), COALESCE(balance, 0.00) 
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &subResellerID, &currentProf, &contractNo, &fullName, &loanActive, &subCurrDebt, &subCurrBalance,
	)
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
	err = tx.QueryRow(`SELECT id, name, display_name, cost_price, price, quota_bytes, validity_value, validity_unit FROM profiles WHERE name = ?`, profileName).Scan(
		&prof.ID, &prof.Name, &prof.DisplayName, &prof.CostPrice, &prof.Price, &prof.QuotaBytes, &prof.ValidityValue, &prof.ValidityUnit,
	)
	if err != nil {
		return fmt.Errorf("الباقة غير موجودة")
	}

	profPriceCents := money.ToCents(prof.Price)
	profCostPriceCents := money.ToCents(prof.CostPrice)
	costCents, _, err := money.CalculateResellerCost(profPriceCents, commissionPercent, profCostPriceCents)
	if err != nil {
		return err
	}

	balCents := money.ToCents(resBalance)
	limitCents := money.ToCents(resCreditLimit)
	debtCents := money.ToCents(resDebt)

	availCents := money.AvailableCapacityCents(balCents, limitCents, debtCents)
	if availCents < costCents {
		return fmt.Errorf("رصيدك غير كافٍ لتجديد الباقة. تكلفة الباقة للموزع: %s، رصيدك المتاح: %s",
			money.FormatEGP(costCents), money.FormatEGP(availCents))
	}

	newBalCents, newDebtCents, err := money.DeductResellerFunds(balCents, debtCents, limitCents, costCents)
	if err != nil {
		return err
	}

	newBalance := money.FromCents(newBalCents)
	newDebt := money.FromCents(newDebtCents)
	costAmount := money.FromCents(costCents)

	updateRes, err := tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newBalance, newDebt, resellerID)
	if err != nil {
		return fmt.Errorf("failed to deduct reseller: %w", err)
	}
	rowsAff, _ := updateRes.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("failed to update reseller balance")
	}

	newExp, finalQuota, deductedHours, deductedQuotaBytes, hadLoan := d.CalculateLoanDeductionTx(tx, username, &prof)

	subRes, err := tx.Exec(`UPDATE subscribers SET
		profile_name = ?,
		quota_bytes = ?,
		expiration_date = ?,
		status = 'active',
		loan_active = 0,
		loan_granted_at = NULL,
		loan_hours = 0,
		loan_quota_bytes = 0,
		loan_deduction_type = 'full',
		loan_initial_used_bytes = 0,
		last_renewed_at = NOW(),
		notified_expired = 0,
		notified_quota_50 = 0,
		notified_quota_75 = 0,
		notified_quota_100 = 0
		WHERE username = ?`, prof.Name, finalQuota, newExp, username)
	if err != nil {
		return fmt.Errorf("failed to update subscriber: %w", err)
	}
	rowsAff, _ = subRes.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("failed to update subscriber: record not found")
	}

	if hadLoan {
		_, err = tx.Exec(`UPDATE loan_transactions SET 
			status = 'settled', 
			settled_at = NOW(), 
			used_bytes = ?, 
			deducted_hours = ?, 
			deducted_quota_bytes = ?,
			deducted_amount = ?,
			remaining_amount = 0.00
			WHERE username = ? AND status = 'active'`,
			deductedQuotaBytes, int(deductedHours), deductedQuotaBytes, prof.Price, username)
		if err != nil {
			return fmt.Errorf("failed to settle loan transaction: %w", err)
		}
	}

	expStr := newExp.Format("02 Jan 2006 15:04:05")
	if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, expStr); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Auth-Type'", username); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM radusergroup WHERE username = ?", username); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", username, prof.Name); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute IN ('Mikrotik-Total-Limit', 'Mikrotik-Rate-Limit', 'Mikrotik-Address-List', 'Framed-Pool')", username); err != nil {
		return err
	}
	if finalQuota > 0 {
		if _, err := tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", finalQuota)); err != nil {
			return err
		}
	}

	// Capture baselines for all active sessions inside tx
	d.captureSessionBaselinesTx(tx, username)

	refID := GenerateReferenceID("RENEW-RES")
	payStatus := "paid"
	payMethod := "reseller"
	newSubDebt := subCurrDebt
	if !isCustomerPaid {
		payStatus = "unpaid"
		payMethod = "debt"
		newSubDebt = subCurrDebt + prof.Price
		subDebtRes, err := tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newSubDebt, username)
		if err != nil {
			return fmt.Errorf("failed to update subscriber debt: %w", err)
		}
		if aff, _ := subDebtRes.RowsAffected(); aff == 0 {
			return fmt.Errorf("failed to update subscriber debt: record not found")
		}

		// Record subscriber debt ledger entry
		frecSub := &FinancialRecord{
			ReferenceID:        refID + "-SUB",
			IdempotencyKey:     idempKey + "-sub",
			OperationType:      "subscriber_renew_debt",
			AccountType:        "subscriber",
			SubscriberID:       subID,
			SubscriberUsername: username,
			ResellerID:         resellerID,
			AmountCents:        profPriceCents,
			Amount:             prof.Price,
			BalanceBefore:      subCurrBalance,
			BalanceAfter:       subCurrBalance,
			DebtBefore:         subCurrDebt,
			DebtAfter:          newSubDebt,
			Status:             "completed",
			Description:        fmt.Sprintf("تجديد باقة آجل (%s) عبر الموزع %s", prof.DisplayName, resName),
			CreatedBy:          adminUser,
		}
		if err := d.RecordFinancialTx(tx, frecSub); err != nil {
			return err
		}
	}

	if adminUser == "" {
		adminUser = "admin"
	}

	desc := fmt.Sprintf("تجديد باقة: %s عبر الموزع %s", prof.DisplayName, resName)
	if hadLoan {
		desc += fmt.Sprintf(" (تسوية سلفة: خصم %.1f ساعة و %.2f جيجا)", deductedHours, float64(deductedQuotaBytes)/(1024*1024*1024))
	}

	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'renew', ?, ?, ?, ?, ?, ?)`,
		username, contractNo, fullName, desc, prof.Price, payStatus, payMethod, fmt.Sprintf("تكلفة الموزع: %s", money.FormatEGP(costCents)), adminUser)
	if err != nil {
		return fmt.Errorf("failed to record billing transaction: %w", err)
	}

	notes := fmt.Sprintf("تجديد باقة %s للمشترك %s (سعر الباقة %s - تكلفة الموزع %s)",
		prof.DisplayName, username, money.FormatEGP(profPriceCents), money.FormatEGP(costCents))
	_, err = tx.Exec(`INSERT INTO reseller_transactions (reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, subscriber_id, subscriber_name, notes, created_by)
		VALUES (?, 'renew', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resellerID, costAmount, resBalance, newBalance, resDebt, newDebt, subID, username, notes, adminUser)
	if err != nil {
		return fmt.Errorf("failed to record reseller transaction: %w", err)
	}

	frec := &FinancialRecord{
		ReferenceID:        refID,
		IdempotencyKey:     idempKey,
		OperationType:      "reseller_renew",
		AccountType:        "reseller",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        costCents,
		Amount:             costAmount,
		BalanceBefore:      resBalance,
		BalanceAfter:       newBalance,
		DebtBefore:         resDebt,
		DebtAfter:          newDebt,
		Status:             "completed",
		Description:        notes,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return err
	}

	// 2. Enqueue Outbox event inside transaction
	action := "coa"
	if currentProf != "" && currentProf != prof.Name {
		action = "disconnect"
	}
	outboxPayload := OutboxPayload{
		Username:           username,
		OperationReference: refID,
		IdempotencyKey:     idempKey,
		Trigger:            "reseller_renew",
	}
	var activeSessID, activeNAS, activeFramed string
	_ = tx.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(&activeSessID, &activeNAS, &activeFramed)
	outboxPayload.SessionID = activeSessID
	outboxPayload.NASIP = activeNAS
	outboxPayload.FramedIP = activeFramed

	if err := d.EnqueueOutboxTx(tx, action, "subscriber", username, outboxPayload); err != nil {
		return fmt.Errorf("failed to enqueue outbox event: %w", err)
	}

	// 3. Mark idempotency key as completed inside tx
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"success":true,"reference_id":"%s"}`, refID)); err != nil {
		return err
	}

	return tx.Commit()
}

// ResellerGenerateCards handles cards batch generation for a reseller with balance deduction and tracking
func (d *DB) ResellerGenerateCards(resellerID int64, opts models.GenerateBatchOptions, adminUser string, idempotencyKey ...string) (string, int, error) {
	if opts.Count <= 0 || opts.Count > 10000 {
		return "", 0, fmt.Errorf("عدد الكروت غير صحيح (يجب أن يكون بين 1 و 10000)")
	}

	var idempKey string
	if len(idempotencyKey) > 0 {
		idempKey = strings.TrimSpace(idempotencyKey[0])
	}
	if idempKey == "" {
		idempKey = GenerateDeterministicKey("reseller_cards", fmt.Sprintf("%d:%d:%d", resellerID, opts.ProfileID, opts.Count), opts.CardType)
	}

	reqHash := ComputeRequestHash(map[string]interface{}{
		"reseller_id": resellerID,
		"profile_id":  opts.ProfileID,
		"count":       opts.Count,
		"card_type":   opts.CardType,
	})

	tx, err := d.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	// 1. Reserve idempotency key inside tx
	acquired, code, body, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "reseller_cards", fmt.Sprintf("%d", resellerID), reqHash, 24*time.Hour)
	if err != nil {
		return "", 0, err
	}
	if !acquired {
		if code == 200 && body != "" {
			var resp struct {
				BatchCode string `json:"batch_code"`
				Count     int    `json:"count"`
			}
			if err := json.Unmarshal([]byte(body), &resp); err == nil {
				return resp.BatchCode, resp.Count, nil
			}
		}
		return "", 0, nil
	}

	var (
		resName           string
		resUsername       string
		resBalance        float64
		resCreditLimit    float64
		resDebt           float64
		commissionPercent float64
		isActive          bool
		permsJSON         sql.NullString
	)

	err = tx.QueryRow(`SELECT name, username, balance, credit_limit, debt, commission_percent, is_active, permissions 
		FROM resellers WHERE id = ? FOR UPDATE`, resellerID).Scan(
		&resName, &resUsername, &resBalance, &resCreditLimit, &resDebt, &commissionPercent, &isActive, &permsJSON,
	)
	if err != nil {
		return "", 0, fmt.Errorf("الموزع غير موجود: %w", err)
	}
	if !isActive {
		return "", 0, fmt.Errorf("حساب الموزع معطل حالياً")
	}

	var perms models.ResellerPermissions
	if permsJSON.Valid && permsJSON.String != "" {
		_ = json.Unmarshal([]byte(permsJSON.String), &perms)
	}
	if !perms.CanGenerateCards {
		return "", 0, fmt.Errorf("ليس لديك صلاحية توليد كروت الشحن أو الكافيهات")
	}

	if err := money.ValidateCommission(commissionPercent); err != nil {
		return "", 0, fmt.Errorf("نسبة عمولة الموزع غير صالحة: %w", err)
	}

	var prof models.Profile
	err = tx.QueryRow("SELECT id, name, display_name, cost_price, price, quota_bytes, uptime_seconds, validity_value, validity_unit FROM profiles WHERE id = ?", opts.ProfileID).Scan(
		&prof.ID, &prof.Name, &prof.DisplayName, &prof.CostPrice, &prof.Price, &prof.QuotaBytes, &prof.UptimeSeconds, &prof.ValidityValue, &prof.ValidityUnit)
	if err != nil {
		return "", 0, fmt.Errorf("الباقة غير موجودة")
	}

	// Server-side authoritative pricing in minor units (cents)
	profPriceCents := money.ToCents(prof.Price)
	profCostPriceCents := money.ToCents(prof.CostPrice)
	costPerCardCents, _, err := money.CalculateResellerCost(profPriceCents, commissionPercent, profCostPriceCents)
	if err != nil {
		return "", 0, err
	}

	expectedTotalCostCents := int64(opts.Count) * costPerCardCents
	balCents := money.ToCents(resBalance)
	limitCents := money.ToCents(resCreditLimit)
	debtCents := money.ToCents(resDebt)

	availCents := money.AvailableCapacityCents(balCents, limitCents, debtCents)
	if availCents < expectedTotalCostCents {
		return "", 0, fmt.Errorf("رصيدك غير كافٍ لتوليد الدفعة. التكلفة الإجمالية: %s، رصيدك المتاح: %s",
			money.FormatEGP(expectedTotalCostCents), money.FormatEGP(availCents))
	}

	opts.PricePerCard = money.FromCents(profPriceCents)
	opts.CostPricePerCard = money.FromCents(costPerCardCents)
	opts.ResellerID = resellerID

	// Generate batch inside same transaction!
	batchCode, created, err := d.generateAdvancedBatchTx(tx, opts, &prof)
	if err != nil {
		return "", 0, err
	}
	if created == 0 {
		return "", 0, fmt.Errorf("لم يتم إنشاء أي كروت بنجاح")
	}

	// Deduct ONLY for actual created cards
	actualCostCents := int64(created) * costPerCardCents
	newBalCents, newDebtCents, err := money.DeductResellerFunds(balCents, debtCents, limitCents, actualCostCents)
	if err != nil {
		return "", 0, err
	}

	newBalance := money.FromCents(newBalCents)
	newDebt := money.FromCents(newDebtCents)
	actualCost := money.FromCents(actualCostCents)

	updateRes, err := tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newBalance, newDebt, resellerID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to update reseller balance: %w", err)
	}
	rowsAff, _ := updateRes.RowsAffected()
	if rowsAff == 0 {
		return "", 0, fmt.Errorf("failed to deduct reseller: no rows updated")
	}

	cardTypeName := "كروت شحن"
	if opts.CardType == "cafe" {
		cardTypeName = "كروت كافيهات"
	}
	notes := fmt.Sprintf("توليد دفعة %s (%d كارت، باقة: %s، تكلفة الكارت: %s، كود الدفعة: %s)",
		cardTypeName, created, prof.DisplayName, money.FormatEGP(costPerCardCents), batchCode)

	refID := GenerateReferenceID("CARD-BATCH")

	_, err = tx.Exec(`INSERT INTO reseller_transactions (reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, subscriber_id, subscriber_name, notes, created_by)
		VALUES (?, 'generate_cards', ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		resellerID, actualCost, resBalance, newBalance, resDebt, newDebt, batchCode, notes, adminUser)
	if err != nil {
		return "", 0, fmt.Errorf("failed to insert reseller transaction: %w", err)
	}

	frec := &FinancialRecord{
		ReferenceID:    refID,
		IdempotencyKey: idempKey,
		OperationType:  "reseller_cards",
		AccountType:    "reseller",
		ResellerID:     resellerID,
		AmountCents:    actualCostCents,
		Amount:         actualCost,
		BalanceBefore:  resBalance,
		BalanceAfter:   newBalance,
		DebtBefore:     resDebt,
		DebtAfter:      newDebt,
		Status:         "completed",
		Description:    notes,
		CreatedBy:      adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return "", 0, err
	}

	bodyResp := fmt.Sprintf(`{"batch_code":"%s","count":%d}`, batchCode, created)
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, bodyResp); err != nil {
		return "", 0, err
	}

	if err := tx.Commit(); err != nil {
		return "", 0, err
	}

	return batchCode, created, nil
}

// ResellerAddQuota handles quota addition for a reseller's subscriber with balance tracking
func (d *DB) ResellerAddQuota(resellerID int64, username string, quotaGB float64, price float64, isPaid bool, adminUser string, idempotencyKey ...string) error {
	if err := money.ValidateFinite(quotaGB); err != nil || quotaGB <= 0 {
		return fmt.Errorf("كمية الكوتة المضافة يجب أن تكون رقماً موجباً")
	}
	if err := money.ValidateFinite(price); err != nil || price <= 0 {
		return fmt.Errorf("سعر الكوتة الإضافية غير صحيح ويجب أن يكون رقماً موجباً")
	}

	var idempKey string
	if len(idempotencyKey) > 0 {
		idempKey = strings.TrimSpace(idempotencyKey[0])
	}
	if idempKey == "" {
		idempKey = GenerateDeterministicKey("reseller_quota", fmt.Sprintf("%d:%s:%.2f", resellerID, username, quotaGB), price, isPaid)
	}

	reqHash := ComputeRequestHash(map[string]interface{}{
		"reseller_id": resellerID,
		"username":    username,
		"quota_gb":    quotaGB,
		"price":       price,
		"is_paid":     isPaid,
	})

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Reserve idempotency key inside tx
	acquired, _, _, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "reseller_quota", fmt.Sprintf("%d:%s", resellerID, username), reqHash, 24*time.Hour)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}

	var (
		resName           string
		resBalance        float64
		resCreditLimit    float64
		resDebt           float64
		commissionPercent float64
		isActive          bool
		permsJSON         sql.NullString
	)

	err = tx.QueryRow(`SELECT name, balance, credit_limit, debt, commission_percent, is_active, permissions 
		FROM resellers WHERE id = ? FOR UPDATE`, resellerID).Scan(
		&resName, &resBalance, &resCreditLimit, &resDebt, &commissionPercent, &isActive, &permsJSON,
	)
	if err != nil {
		return fmt.Errorf("الموزع غير موجود: %w", err)
	}
	if !isActive {
		return fmt.Errorf("حساب الموزع معطل حالياً")
	}

	var perms models.ResellerPermissions
	if permsJSON.Valid && permsJSON.String != "" {
		_ = json.Unmarshal([]byte(permsJSON.String), &perms)
	}
	if !perms.CanAddQuota {
		return fmt.Errorf("ليس لديك صلاحية إضافة كوتة للمشتركين")
	}
	if err := money.ValidateCommission(commissionPercent); err != nil {
		return fmt.Errorf("نسبة عمولة الموزع غير صالحة: %w", err)
	}

	var (
		subID          int64
		subResellerID  int64
		contractNo     string
		fullName       string
		subCurrDebt    float64
		subCurrBalance float64
	)
	err = tx.QueryRow(`SELECT id, reseller_id, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00) 
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &subResellerID, &contractNo, &fullName, &subCurrDebt, &subCurrBalance,
	)
	if err != nil {
		return fmt.Errorf("المشترك غير موجود")
	}
	if subResellerID != resellerID {
		return fmt.Errorf("هذا المشترك غير مسجل تحت حسابك كموزع")
	}

	priceCents := money.ToCents(price)
	costCents, _, err := money.CalculateResellerCost(priceCents, commissionPercent, 0)
	if err != nil {
		return err
	}

	balCents := money.ToCents(resBalance)
	limitCents := money.ToCents(resCreditLimit)
	debtCents := money.ToCents(resDebt)

	availCents := money.AvailableCapacityCents(balCents, limitCents, debtCents)
	if availCents < costCents {
		return fmt.Errorf("رصيدك غير كافٍ لإضافة الكوتة. التكلفة للموزع: %s، رصيدك المتاح: %s",
			money.FormatEGP(costCents), money.FormatEGP(availCents))
	}

	newBalCents, newDebtCents, err := money.DeductResellerFunds(balCents, debtCents, limitCents, costCents)
	if err != nil {
		return err
	}

	newBalance := money.FromCents(newBalCents)
	newDebt := money.FromCents(newDebtCents)
	costAmount := money.FromCents(costCents)

	updateRes, err := tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newBalance, newDebt, resellerID)
	if err != nil {
		return fmt.Errorf("failed to deduct reseller: %w", err)
	}
	rowsAff, _ := updateRes.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("failed to update reseller balance")
	}

	// Update subscriber quota
	addBytes, err := money.QuotaGBToBytes(quotaGB)
	if err != nil {
		return err
	}
	subRes, err := tx.Exec("UPDATE subscribers SET quota_bytes = quota_bytes + ? WHERE username = ?", addBytes, username)
	if err != nil {
		return fmt.Errorf("failed to update subscriber quota: %w", err)
	}
	rowsAff, _ = subRes.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("subscriber record not found")
	}

	refID := GenerateReferenceID("QUOTA-RES")

	// If subscriber did not pay immediately, increase subscriber debt atomically
	newSubDebt := subCurrDebt
	if !isPaid && price > 0 {
		newSubDebt = subCurrDebt + price
		subDebtRes, err := tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newSubDebt, username)
		if err != nil {
			return fmt.Errorf("failed to update subscriber debt: %w", err)
		}
		if aff, _ := subDebtRes.RowsAffected(); aff == 0 {
			return fmt.Errorf("failed to update subscriber debt: record not found")
		}

		frecSub := &FinancialRecord{
			ReferenceID:        refID + "-SUB",
			IdempotencyKey:     idempKey + "-sub",
			OperationType:      "subscriber_quota_debt",
			AccountType:        "subscriber",
			SubscriberID:       subID,
			SubscriberUsername: username,
			ResellerID:         resellerID,
			AmountCents:        priceCents,
			Amount:             price,
			BalanceBefore:      subCurrBalance,
			BalanceAfter:       subCurrBalance,
			DebtBefore:         subCurrDebt,
			DebtAfter:          newSubDebt,
			Status:             "completed",
			Description:        fmt.Sprintf("إضافة كوتة رصيد (%.1f GB) آجل عبر الموزع %s", quotaGB, resName),
			CreatedBy:          adminUser,
		}
		if err := d.RecordFinancialTx(tx, frecSub); err != nil {
			return err
		}
	}

	var currLimitStr string
	errQ := tx.QueryRow("SELECT value FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username).Scan(&currLimitStr)
	if errQ == nil {
		currLimit, _ := strconv.ParseInt(currLimitStr, 10, 64)
		newLimit := currLimit + addBytes
		if _, err := tx.Exec("UPDATE radreply SET value = ? WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", fmt.Sprintf("%d", newLimit), username); err != nil {
			return fmt.Errorf("failed to update radreply total limit: %w", err)
		}
	} else {
		var newLimit int64
		_ = tx.QueryRow("SELECT quota_bytes FROM subscribers WHERE username = ?", username).Scan(&newLimit)
		if newLimit > 0 {
			if _, err := tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", newLimit)); err != nil {
				return fmt.Errorf("failed to insert radreply total limit: %w", err)
			}
		}
	}

	payStatus := "paid"
	payMethod := "reseller"
	if !isPaid {
		payStatus = "unpaid"
		payMethod = "debt"
	}

	if adminUser == "" {
		adminUser = "admin"
	}

	desc := fmt.Sprintf("إضافة كوتة رصيد: %.1f GB عبر الموزع %s", quotaGB, resName)
	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'quota', ?, ?, ?, ?, ?, ?)`,
		username, contractNo, fullName, desc, price, payStatus, payMethod, fmt.Sprintf("تكلفة الموزع: %s", money.FormatEGP(costCents)), adminUser)
	if err != nil {
		return fmt.Errorf("failed to record billing transaction: %w", err)
	}

	notes := fmt.Sprintf("إضافة كوتة رصيد (%.1f GB) للمشترك %s (السعر %s - تكلفة الموزع %s)",
		quotaGB, username, money.FormatEGP(priceCents), money.FormatEGP(costCents))
	_, err = tx.Exec(`INSERT INTO reseller_transactions (reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, subscriber_id, subscriber_name, notes, created_by)
		VALUES (?, 'quota', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resellerID, costAmount, resBalance, newBalance, resDebt, newDebt, subID, username, notes, adminUser)
	if err != nil {
		return fmt.Errorf("failed to record reseller transaction: %w", err)
	}

	frec := &FinancialRecord{
		ReferenceID:        refID,
		IdempotencyKey:     idempKey,
		OperationType:      "reseller_quota",
		AccountType:        "reseller",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        costCents,
		Amount:             costAmount,
		BalanceBefore:      resBalance,
		BalanceAfter:       newBalance,
		DebtBefore:         resDebt,
		DebtAfter:          newDebt,
		Status:             "completed",
		Description:        notes,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return err
	}

	// 2. Enqueue Outbox CoA event inside transaction
	outboxPayload := OutboxPayload{
		Username:           username,
		OperationReference: refID,
		IdempotencyKey:     idempKey,
		Trigger:            "reseller_add_quota",
	}
	var activeSessID, activeNAS, activeFramed string
	_ = tx.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(&activeSessID, &activeNAS, &activeFramed)
	outboxPayload.SessionID = activeSessID
	outboxPayload.NASIP = activeNAS
	outboxPayload.FramedIP = activeFramed

	if err := d.EnqueueOutboxTx(tx, "coa", "subscriber", username, outboxPayload); err != nil {
		return fmt.Errorf("failed to enqueue quota outbox event: %w", err)
	}

	// 3. Mark idempotency key as completed inside tx
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"success":true,"reference_id":"%s"}`, refID)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Reset notification
	_, _ = d.Exec("UPDATE subscribers SET notified_quota_100 = 0 WHERE username = ?", username)

	return nil
}

// ResellerAddSubscriberBalance handles balance deposit from reseller's wallet into their subscriber's balance
func (d *DB) ResellerAddSubscriberBalance(resellerID int64, username string, amount float64, notes, adminUser string, settleDebtFirst bool, idempotencyKey ...string) (float64, float64, float64, error) {
	if err := money.ValidatePositive(amount); err != nil {
		return 0, 0, 0, fmt.Errorf("المبلغ يجب أن يكون أكبر من الصفر")
	}
	idempKey, err := ResolveIdempotencyKey(idempotencyKey, "reseller_sub_deposit", fmt.Sprintf("%d:%s", resellerID, username), amount, settleDebtFirst)
	if err != nil {
		return 0, 0, 0, err
	}

	tx, err := d.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	acquired, _, responseBody, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "reseller_sub_deposit", fmt.Sprintf("%d:%s", resellerID, username), ComputeRequestHash(map[string]interface{}{"reseller_id": resellerID, "username": username, "amount": amount, "settle_debt_first": settleDebtFirst}), 24*time.Hour)
	if err != nil {
		return 0, 0, 0, err
	}
	if !acquired {
		var cached struct{ Balance, Debt, Settled float64 }
		if json.Unmarshal([]byte(responseBody), &cached) == nil {
			return cached.Balance, cached.Debt, cached.Settled, nil
		}
		return 0, 0, 0, nil
	}

	var (
		resName        string
		resBalance     float64
		resCreditLimit float64
		resDebt        float64
		isActive       bool
		permsJSON      sql.NullString
	)

	err = tx.QueryRow(`SELECT name, balance, credit_limit, debt, is_active, permissions 
		FROM resellers WHERE id = ? FOR UPDATE`, resellerID).Scan(
		&resName, &resBalance, &resCreditLimit, &resDebt, &isActive, &permsJSON,
	)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("الموزع غير موجود: %w", err)
	}
	if !isActive {
		return 0, 0, 0, fmt.Errorf("حساب الموزع معطل حالياً")
	}

	var perms models.ResellerPermissions
	if permsJSON.Valid && permsJSON.String != "" {
		_ = json.Unmarshal([]byte(permsJSON.String), &perms)
	}
	if !perms.CanCollectDebt {
		return 0, 0, 0, fmt.Errorf("ليس لديك صلاحية شحن رصيد أو سداد ديون للمشتركين")
	}

	var (
		subID         int64
		subResellerID int64
		contractNo    string
		fullName      string
		subDebt       float64
		subBalance    float64
	)
	err = tx.QueryRow(`SELECT id, reseller_id, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00)
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &subResellerID, &contractNo, &fullName, &subDebt, &subBalance,
	)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("المشترك غير موجود")
	}
	if subResellerID != resellerID {
		return 0, 0, 0, fmt.Errorf("هذا المشترك غير مسجل تحت حسابك كموزع")
	}

	amountCents := money.ToCents(amount)
	balCents := money.ToCents(resBalance)
	limitCents := money.ToCents(resCreditLimit)
	debtCents := money.ToCents(resDebt)

	availCents := money.AvailableCapacityCents(balCents, limitCents, debtCents)
	if availCents < amountCents {
		return 0, 0, 0, fmt.Errorf("رصيدك بالمحفظة غير كافٍ. المبلغ المطلوب: %s، رصيدك المتاح: %s",
			money.FormatEGP(amountCents), money.FormatEGP(availCents))
	}

	newResBalCents, newResDebtCents, err := money.DeductResellerFunds(balCents, debtCents, limitCents, amountCents)
	if err != nil {
		return 0, 0, 0, err
	}

	newResBalance := money.FromCents(newResBalCents)
	newResDebt := money.FromCents(newResDebtCents)

	updateRes, err := tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newResBalance, newResDebt, resellerID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to deduct reseller: %w", err)
	}
	rowsAff, _ := updateRes.RowsAffected()
	if rowsAff == 0 {
		return 0, 0, 0, fmt.Errorf("failed to update reseller balance")
	}

	// Update subscriber balance & settle debt if requested
	subBalCents := money.ToCents(subBalance)
	subDebtCents := money.ToCents(subDebt)
	var settledCents int64 = 0
	var finalSubBalCents int64
	var finalSubDebtCents int64

	if settleDebtFirst && subDebtCents > 0 {
		if amountCents >= subDebtCents {
			settledCents = subDebtCents
			remCents := amountCents - subDebtCents
			finalSubDebtCents = 0
			finalSubBalCents = subBalCents + remCents
		} else {
			settledCents = amountCents
			finalSubDebtCents = subDebtCents - amountCents
			finalSubBalCents = subBalCents
		}
	} else {
		finalSubBalCents = subBalCents + amountCents
		finalSubDebtCents = subDebtCents
	}

	finalSubBal := money.FromCents(finalSubBalCents)
	finalSubDebt := money.FromCents(finalSubDebtCents)
	settledAmount := money.FromCents(settledCents)

	subRes, err := tx.Exec("UPDATE subscribers SET balance = ?, debt = ? WHERE username = ?", finalSubBal, finalSubDebt, username)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to update subscriber: %w", err)
	}
	rowsAff, _ = subRes.RowsAffected()
	if rowsAff == 0 {
		return 0, 0, 0, fmt.Errorf("failed to update subscriber: record not found")
	}

	if adminUser == "" {
		adminUser = "admin"
	}
	refID := GenerateReferenceID("DEP-RES")

	desc := fmt.Sprintf("شحن رصيد محفظة عبر الموزع %s بمبلغ %s", resName, money.FormatEGP(amountCents))
	if settledCents > 0 {
		desc += fmt.Sprintf(" (سداد دين: %s، متبقي رصيد: %s)", money.FormatEGP(settledCents), money.FormatEGP(amountCents-settledCents))
	}

	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'deposit', ?, ?, 'paid', 'reseller_wallet', ?, ?)`,
		username, contractNo, fullName, desc, amount, notes, adminUser)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to record billing transaction: %w", err)
	}

	txNotes := fmt.Sprintf("شحن رصيد محفظة للمشترك %s بمبلغ %s (سداد دين: %s، متبقي رصيد: %s)",
		username, money.FormatEGP(amountCents), money.FormatEGP(settledCents), money.FormatEGP(amountCents-settledCents))
	_, err = tx.Exec(`INSERT INTO reseller_transactions (reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, subscriber_id, subscriber_name, notes, created_by)
		VALUES (?, 'subscriber_deposit', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resellerID, amount, resBalance, newResBalance, resDebt, newResDebt, subID, username, txNotes, adminUser)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to record reseller transaction: %w", err)
	}

	frec := &FinancialRecord{
		ReferenceID:        refID,
		OperationType:      "reseller_sub_deposit",
		AccountType:        "reseller",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        amountCents,
		Amount:             amount,
		BalanceBefore:      resBalance,
		BalanceAfter:       newResBalance,
		DebtBefore:         resDebt,
		DebtAfter:          newResDebt,
		Status:             "completed",
		Description:        txNotes,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return 0, 0, 0, err
	}

	// Mirror the subscriber-side state change in the ledger as well.
	frecSub := &FinancialRecord{
		ReferenceID:        refID + "-SUB",
		IdempotencyKey:     idempKey + "-sub",
		OperationType:      "subscriber_balance_deposit_reseller",
		AccountType:        "subscriber",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        amountCents,
		Amount:             amount,
		BalanceBefore:      subBalance,
		BalanceAfter:       finalSubBal,
		DebtBefore:         subDebt,
		DebtAfter:          finalSubDebt,
		Status:             "completed",
		Description:        desc,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frecSub); err != nil {
		return 0, 0, 0, err
	}
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"balance":%.2f,"debt":%.2f,"settled":%.2f}`, finalSubBal, finalSubDebt, settledAmount)); err != nil {
		return 0, 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}

	return finalSubBal, finalSubDebt, settledAmount, nil
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

	// Update FreeRADIUS radgroupcheck for Simultaneous-Use
	_, _ = tx.Exec("DELETE FROM radgroupcheck WHERE groupname = ? AND attribute = 'Simultaneous-Use'", p.Name)
	sharedUsers := p.SharedUsers
	if sharedUsers <= 0 {
		sharedUsers = 1
	}
	_, _ = tx.Exec("INSERT INTO radgroupcheck (groupname, attribute, op, value) VALUES (?, 'Simultaneous-Use', ':=', ?)", p.Name, fmt.Sprintf("%d", sharedUsers))

	// Update FreeRADIUS radgroupreply for Rate-Limit, Pool, Address-List, Session-Timeout, Mikrotik-Total-Limit
	_, _ = tx.Exec("DELETE FROM radgroupreply WHERE groupname = ? AND attribute IN ('Mikrotik-Rate-Limit', 'Framed-Pool', 'Mikrotik-Address-List', 'Session-Timeout', 'Mikrotik-Total-Limit')", p.Name)
	if p.RateLimit != "" {
		_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Mikrotik-Rate-Limit', ':=', ?)", p.Name, p.RateLimit)
	}
	if p.PoolName != "" {
		_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Framed-Pool', ':=', ?)", p.Name, p.PoolName)
	}
	if p.MikrotikAddressList != "" {
		_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Mikrotik-Address-List', ':=', ?)", p.Name, p.MikrotikAddressList)
	}
	if p.UptimeSeconds > 0 {
		_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Session-Timeout', ':=', ?)", p.Name, fmt.Sprintf("%d", p.UptimeSeconds))
	}
	if p.QuotaBytes > 0 {
		_, _ = tx.Exec("INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", p.Name, fmt.Sprintf("%d", p.QuotaBytes))
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

		// 2. Partial quota deduction: traffic consumed since loan grant using baseline delta
		if loanGrantedAt.Valid {
			var currentCycleTotal int64
			_ = tx.QueryRow(fmt.Sprintf(`SELECT %s FROM radacct WHERE username = ?`, RadacctSumBytesSQL), username).Scan(&currentCycleTotal)
			usedDuringLoan := currentCycleTotal - loanInitialUsedBytes
			if usedDuringLoan < 0 {
				usedDuringLoan = 0
			}
			if loanQuotaBytes > 0 && usedDuringLoan > loanQuotaBytes {
				usedDuringLoan = loanQuotaBytes
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
		_, _, _ = d.DisconnectSubscriberSession(username)
	} else {
		_ = d.RestoreActiveSubscriberCoA(username)
	}
	return nil
}

// GrantSubscriberLoan grants a grace period loan (time and quota) to a subscriber based on profile settings
func (d *DB) GrantSubscriberLoan(username, adminUser string, idempotencyKey ...string) (*models.Subscriber, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("اسم المستخدم مطلوب")
	}

	idempKey, err := ResolveIdempotencyKey(idempotencyKey, "subscriber_loan_grant", username)
	if err != nil {
		return nil, err
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acquired, _, _, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "subscriber_loan_grant", username, ComputeRequestHash(map[string]interface{}{"username": username}), 24*time.Hour)
	if err != nil {
		return nil, err
	}
	if !acquired {
		_ = tx.Commit()
		return d.GetSubscriber(username)
	}

	var (
		subID          int64
		resellerID     int64
		profileName    string
		contractNo     string
		fullName       string
		loanActive     bool
		loanGrantedAt  sql.NullTime
		expirationDate sql.NullTime
		quotaBytes     int64
		debt           float64
		balance        float64
	)
	err = tx.QueryRow(`SELECT id, reseller_id, profile_name, COALESCE(contract_no, ''), COALESCE(full_name, ''), 
		COALESCE(loan_active, 0), loan_granted_at, expiration_date, quota_bytes, COALESCE(debt, 0.00), COALESCE(balance, 0.00)
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &resellerID, &profileName, &contractNo, &fullName,
		&loanActive, &loanGrantedAt, &expirationDate, &quotaBytes, &debt, &balance,
	)
	if err != nil {
		return nil, fmt.Errorf("المشترك غير موجود: %w", err)
	}

	if loanActive {
		grantDate := "سابقاً"
		if loanGrantedAt.Valid {
			grantDate = loanGrantedAt.Time.Format("2006-01-02 15:04")
		}
		return nil, fmt.Errorf("المشترك لديه سلفة نشطة بالفعل تم منحها بتاريخ %s ولم يتم تسديدها بعد", grantDate)
	}

	if profileName == "" {
		return nil, fmt.Errorf("المشترك ليس لديه باقة محددة")
	}

	var prof models.Profile
	err = tx.QueryRow(`SELECT id, name, display_name, quota_bytes, uptime_seconds, validity_value, validity_unit,
		COALESCE(loan_enabled, 0), COALESCE(loan_hours, 48), COALESCE(loan_quota_bytes, 0), COALESCE(loan_deduction_type, 'full')
		FROM profiles WHERE name = ?`, profileName).Scan(
		&prof.ID, &prof.Name, &prof.DisplayName, &prof.QuotaBytes, &prof.UptimeSeconds, &prof.ValidityValue, &prof.ValidityUnit,
		&prof.LoanEnabled, &prof.LoanHours, &prof.LoanQuotaBytes, &prof.LoanDeductionType,
	)
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

	var currentUsedBytes int64
	var cycleStart time.Time
	var lastRenewed sql.NullTime
	_ = tx.QueryRow("SELECT last_renewed_at, created_at FROM subscribers WHERE username = ?", username).Scan(&lastRenewed, &cycleStart)
	if lastRenewed.Valid {
		cycleStart = lastRenewed.Time
	}
	_ = tx.QueryRow(fmt.Sprintf("SELECT %s FROM radacct WHERE username = ? AND acctstarttime >= ?", RadacctSumBytesSQL), username, cycleStart).Scan(&currentUsedBytes)

	if adminUser == "self-service" {
		nowCheck := time.Now()
		isTimeExpired := expirationDate.Valid && nowCheck.After(expirationDate.Time)
		isQuotaExpired := quotaBytes > 0 && currentUsedBytes >= quotaBytes
		if !isTimeExpired && !isQuotaExpired {
			return nil, fmt.Errorf("لا يمكن طلب سلفة إلا في حال انتهاء صلاحية الاشتراك أو نفاد الكوتة بالكامل")
		}
	}

	now := time.Now()
	newExp := now.Add(time.Duration(loanHours) * time.Hour)

	// Update subscribers table
	subRes, err := tx.Exec(`UPDATE subscribers SET
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
	rowsAff, _ := subRes.RowsAffected()
	if rowsAff == 0 {
		return nil, fmt.Errorf("failed to update subscriber loan: record not found")
	}

	// Update FreeRADIUS radcheck Expiration
	expStr := newExp.Format("02 Jan 2006 15:04:05")
	if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username); err != nil {
		return nil, err
	}
	if _, err := tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, expStr); err != nil {
		return nil, err
	}

	// Update radreply Mikrotik-Total-Limit
	if _, err := tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username); err != nil {
		return nil, err
	}
	if loanQuotaBytes > 0 {
		if _, err := tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", loanQuotaBytes)); err != nil {
			return nil, err
		}
	}

	loanID := GenerateReferenceID("LOAN")
	refID := GenerateReferenceID("FIN-LOAN")

	// Insert into loan_transactions
	_, err = tx.Exec(`INSERT INTO loan_transactions (
		loan_id, reference_id, subscriber_id, username, commercial_value,
		granted_hours, granted_quota_bytes, deduction_type, initial_used_bytes, status, granted_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', NOW())`,
		loanID, refID, subID, username, prof.Price, loanHours, loanQuotaBytes, loanDeductionType, currentUsedBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to record loan transaction: %w", err)
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

	// Insert into billing_transactions
	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'loan', ?, 0.00, 'paid', 'loan', ?, ?)`,
		username, contractNo, fullName, desc, desc, adminUser)
	if err != nil {
		return nil, fmt.Errorf("failed to record billing transaction for loan: %w", err)
	}

	// Insert into financial_ledger
	frec := &FinancialRecord{
		ReferenceID:        refID,
		OperationType:      "subscriber_loan_grant",
		AccountType:        "subscriber",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        0,
		Amount:             0,
		BalanceBefore:      balance,
		BalanceAfter:       balance,
		DebtBefore:         debt,
		DebtAfter:          debt,
		Status:             "completed",
		Description:        desc,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return nil, err
	}

	// Enqueue Outbox CoA event inside transaction
	outboxPayload := OutboxPayload{
		Username:           username,
		OperationReference: refID,
		Trigger:            "subscriber_loan_grant",
	}
	var activeSessID, activeNAS, activeFramed string
	_ = tx.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(&activeSessID, &activeNAS, &activeFramed)
	outboxPayload.SessionID = activeSessID
	outboxPayload.NASIP = activeNAS
	outboxPayload.FramedIP = activeFramed

	if err := d.EnqueueOutboxTx(tx, "coa", "subscriber", username, outboxPayload); err != nil {
		return nil, fmt.Errorf("failed to enqueue loan outbox event: %w", err)
	}
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"success":true,"reference_id":"%s"}`, refID)); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	sub := &models.Subscriber{
		ID:             subID,
		Username:       username,
		ContractNo:     contractNo,
		FullName:       fullName,
		ProfileName:    profileName,
		LoanActive:     true,
		LoanGrantedAt:  sql.NullTime{Time: now, Valid: true},
		LoanHours:      loanHours,
		LoanQuotaBytes: loanQuotaBytes,
		ExpirationDate: sql.NullTime{Time: newExp, Valid: true},
		Status:         "expired",
	}

	// Trigger WhatsApp notification asynchronously
	go func() {
		_ = d.TriggerWhatsAppEvent("loan_granted", sub, map[string]string{
			"{loan_hours}":  fmt.Sprintf("%d", loanHours),
			"{loan_quota}":  fmt.Sprintf("%.2f", float64(loanQuotaBytes)/(1024*1024*1024)),
			"{expiry_date}": newExp.Format("2006-01-02 15:04"),
		})
	}()

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
	_, _, _ = d.DisconnectSubscriberSession(username)

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
	_, _, _ = d.DisconnectSubscriberSession(username)

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
	_, _, _ = d.DisconnectSubscriberSession(username)

	return nil
}

// --- Subscriber CRM Edit Methods ---

func (d *DB) GetSubscriber(username string) (*models.Subscriber, error) {
	var s models.Subscriber
	var exp sql.NullTime
	var loanGranted sql.NullTime
	err := d.QueryRow(`SELECT s.id, s.username, s.contract_no, s.full_name, s.phone, s.address, s.debt, COALESCE(s.balance, 0.00),
		s.profile_name, s.static_ip, s.calling_mac, s.quota_bytes, s.expiration_date, s.status,
		COALESCE(s.reseller_id, 0), COALESCE(r.name, ''),
		COALESCE(s.loan_active, 0), s.loan_granted_at, COALESCE(s.loan_hours, 0), COALESCE(s.loan_quota_bytes, 0),
		COALESCE(s.loan_deduction_type, 'full'), COALESCE(s.loan_initial_used_bytes, 0),
		s.created_at, s.updated_at
		FROM subscribers s
		LEFT JOIN resellers r ON s.reseller_id = r.id
		WHERE s.username = ?`, username).Scan(
		&s.ID, &s.Username, &s.ContractNo, &s.FullName, &s.Phone, &s.Address, &s.Debt, &s.Balance,
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

	// 1. CRM safety: Disallow modifying debt, balance, or quota directly from CRM.
	// Preserves existing database values for existing subscribers, or defaults to 0.00 for new subscribers.
	var (
		existingDebt    float64
		existingBalance float64
		existingQuota   int64
	)
	err = tx.QueryRow(`SELECT COALESCE(debt, 0.00), COALESCE(balance, 0.00), COALESCE(quota_bytes, 0) 
		FROM subscribers WHERE username = ? FOR UPDATE`, sub.Username).Scan(
		&existingDebt, &existingBalance, &existingQuota,
	)
	if err == nil {
		// Existing subscriber: preserve financial integrity
		sub.Debt = existingDebt
		sub.Balance = existingBalance
		sub.QuotaBytes = existingQuota
	} else if errors.Is(err, sql.ErrNoRows) {
		// New subscriber created from CRM: start with 0 balance and debt
		sub.Debt = 0.00
		sub.Balance = 0.00
	} else {
		return fmt.Errorf("error verifying subscriber CRM state: %w", err)
	}

	var expDate interface{}
	if sub.ExpirationDate.Valid {
		expDate = sub.ExpirationDate.Time
	}

	if sub.ContractNo == "" {
		sub.ContractNo = fmt.Sprintf("CNT-%d-%05d", time.Now().Year(), time.Now().UnixNano()%100000)
	}

	_, err = tx.Exec(`INSERT INTO subscribers (
		username, contract_no, full_name, phone, address, debt, balance, profile_name,
		static_ip, calling_mac, quota_bytes, expiration_date, status, reseller_id, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())
	ON DUPLICATE KEY UPDATE
		full_name = VALUES(full_name),
		phone = VALUES(phone),
		address = VALUES(address),
		profile_name = VALUES(profile_name),
		static_ip = VALUES(static_ip),
		calling_mac = VALUES(calling_mac),
		expiration_date = VALUES(expiration_date),
		status = VALUES(status),
		reseller_id = VALUES(reseller_id),
		updated_at = NOW()`,
		sub.Username, sub.ContractNo, sub.FullName, sub.Phone, sub.Address, sub.Debt, sub.Balance, sub.ProfileName,
		sub.StaticIP, sub.CallingMAC, sub.QuotaBytes, expDate, sub.Status, sub.ResellerID)
	if err != nil {
		return fmt.Errorf("failed to save subscriber CRM: %w", err)
	}

	// Update Cleartext-Password
	if password != "" {
		if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Cleartext-Password'", sub.Username); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)", sub.Username, password); err != nil {
			return err
		}
	}

	// Calling-Station-Id
	if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Calling-Station-Id'", sub.Username); err != nil {
		return err
	}
	if sub.CallingMAC != "" {
		if _, err := tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Calling-Station-Id', '==', ?)", sub.Username, sub.CallingMAC); err != nil {
			return err
		}
	}

	// Expiration
	if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", sub.Username); err != nil {
		return err
	}
	if sub.ExpirationDate.Valid {
		expStr := sub.ExpirationDate.Time.Format("02 Jan 2006 15:04:05")
		if _, err := tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", sub.Username, expStr); err != nil {
			return err
		}
	}

	// Profile group
	if _, err := tx.Exec("DELETE FROM radusergroup WHERE username = ?", sub.Username); err != nil {
		return err
	}
	if sub.ProfileName != "" {
		if _, err := tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", sub.Username, sub.ProfileName); err != nil {
			return err
		}
	}

	// Reply items
	if _, err := tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute IN ('Framed-IP-Address', 'Mikrotik-Total-Limit')", sub.Username); err != nil {
		return err
	}
	if sub.StaticIP != "" {
		if _, err := tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Framed-IP-Address', ':=', ?)", sub.Username, sub.StaticIP); err != nil {
			return err
		}
	}
	if sub.QuotaBytes > 0 {
		if _, err := tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", sub.Username, fmt.Sprintf("%d", sub.QuotaBytes)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// AdjustSubscriberFinancials adjusts balance and debt with strict audit and financial ledger logging
func (d *DB) AdjustSubscriberFinancials(username string, balanceDelta, debtDelta float64, reason, adminUser, ipAddress string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		subID      int64
		resellerID int64
		contractNo string
		fullName   string
		currBal    float64
		currDebt   float64
	)
	err = tx.QueryRow(`SELECT id, reseller_id, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(balance, 0.00), COALESCE(debt, 0.00)
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &resellerID, &contractNo, &fullName, &currBal, &currDebt,
	)
	if err != nil {
		return fmt.Errorf("المشترك غير موجود: %w", err)
	}

	balCents := money.ToCents(currBal) + money.ToCents(balanceDelta)
	debtCents := money.ToCents(currDebt) + money.ToCents(debtDelta)

	if balCents < 0 {
		return fmt.Errorf("الرصيد لا يمكن أن يكون سالباً")
	}
	if debtCents < 0 {
		debtCents = 0
	}

	newBal := money.FromCents(balCents)
	newDebt := money.FromCents(debtCents)

	updateRes, err := tx.Exec("UPDATE subscribers SET balance = ?, debt = ? WHERE username = ?", newBal, newDebt, username)
	if err != nil {
		return fmt.Errorf("failed to update subscriber financials: %w", err)
	}
	rowsAff, _ := updateRes.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("failed to update subscriber: record not found")
	}

	refID := GenerateReferenceID("ADJ-SUB")
	if adminUser == "" {
		adminUser = "admin"
	}

	desc := fmt.Sprintf("تعديل يدوي للحساب (رصيد: %+.2f، دين: %+.2f) - السبب: %s", balanceDelta, debtDelta, reason)
	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'adjustment', ?, ?, 'paid', 'admin_adjustment', ?, ?)`,
		username, contractNo, fullName, desc, balanceDelta, reason, adminUser)
	if err != nil {
		return err
	}

	frec := &FinancialRecord{
		ReferenceID:        refID,
		OperationType:      "subscriber_admin_adjust",
		AccountType:        "subscriber",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        money.ToCents(balanceDelta),
		Amount:             balanceDelta,
		BalanceBefore:      currBal,
		BalanceAfter:       newBal,
		DebtBefore:         currDebt,
		DebtAfter:          newDebt,
		Status:             "completed",
		Description:        desc,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return err
	}

	arec := &AuditLogRecord{
		ReferenceID:   refID,
		ActorUsername: adminUser,
		ActorRole:     "admin",
		Action:        "adjust_subscriber_financials",
		TargetType:    "subscriber",
		TargetID:      username,
		Details:       desc,
		IPAddress:     ipAddress,
	}
	if err := d.RecordAuditTx(tx, arec); err != nil {
		return err
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
		COALESCE(`+"`rows`"+`, 7), 
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
			cols, `+"`rows`"+`, card_gap, overlay_config, created_at
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
			cols = ?, `+"`rows`"+` = ?, card_gap = ?, overlay_config = ?
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
	if token == "" {
		return 0, "", "", fmt.Errorf("token cannot be empty")
	}

	tx, err := d.Begin()
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	// Atomic update: only marks used if it was not used and within 10 minutes lifetime
	res, err := tx.Exec(`UPDATE sstp_tokens SET used = 1, used_at = NOW() WHERE token = ? AND used = 0 AND created_at >= NOW() - INTERVAL 10 MINUTE`, token)
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to consume token: %v", err)
	}

	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		return 0, "", "", fmt.Errorf("invalid, expired, or already used token")
	}

	err = tx.QueryRow(`SELECT client_id, username, password FROM sstp_tokens WHERE token = ?`, token).Scan(&clientID, &username, &password)
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to retrieve token data: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, "", "", fmt.Errorf("failed to commit token consumption: %v", err)
	}

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

// BulkSummary holds the overall results of a bulk operation
type BulkSummary struct {
	Total      int              `json:"total"`
	Successful int              `json:"successful"`
	Failed     int              `json:"failed"`
	Items      []BulkItemResult `json:"items"`
}

func (d *DB) BulkRenewSubscribers(usernames []string, targetProfile string) (BulkSummary, error) {
	summary := BulkSummary{
		Total: len(usernames),
		Items: make([]BulkItemResult, 0, len(usernames)),
	}
	if len(usernames) == 0 {
		return summary, nil
	}

	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		err := d.RenewSubscriber(u, targetProfile)
		if err != nil {
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
		} else {
			summary.Successful++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    true,
			})
		}
	}

	if summary.Failed > 0 {
		return summary, fmt.Errorf("فشل تجديد %d من أصل %d مشترك", summary.Failed, summary.Total)
	}
	return summary, nil
}

func (d *DB) BulkAddQuota(usernames []string, quotaGB float64) (BulkSummary, error) {
	summary := BulkSummary{
		Total: len(usernames),
		Items: make([]BulkItemResult, 0, len(usernames)),
	}
	if len(usernames) == 0 || quotaGB <= 0 {
		return summary, nil
	}

	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		err := d.AddQuotaBilling(u, quotaGB, 0, true, "bulk", "إضافة كوتة جماعية", "admin")
		if err != nil {
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
		} else {
			summary.Successful++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    true,
			})
		}
	}

	if summary.Failed > 0 {
		return summary, fmt.Errorf("فشل إضافة كوتة لـ %d من أصل %d مشترك", summary.Failed, summary.Total)
	}
	return summary, nil
}

func (d *DB) BulkAddDebt(usernames []string, amount float64) (BulkSummary, error) {
	summary := BulkSummary{
		Total: len(usernames),
		Items: make([]BulkItemResult, 0, len(usernames)),
	}
	if len(usernames) == 0 {
		return summary, nil
	}
	if amount <= 0 {
		return summary, fmt.Errorf("مبلغ المديونية يجب أن يكون أكبر من الصفر")
	}

	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		refID := GenerateReferenceID("DEBT-BULK")
		tx, err := d.Begin()
		if err != nil {
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
			continue
		}

		var subID, resellerID int64
		var contractNo, fullName string
		var currDebt, currBal float64
		err = tx.QueryRow(`SELECT id, reseller_id, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00)
			FROM subscribers WHERE username = ? FOR UPDATE`, u).Scan(&subID, &resellerID, &contractNo, &fullName, &currDebt, &currBal)
		if err != nil {
			tx.Rollback()
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      "subscriber not found",
			})
			continue
		}

		newDebt := currDebt + amount
		res, err := tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newDebt, u)
		if err != nil {
			tx.Rollback()
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
			continue
		}
		rowsAff, _ := res.RowsAffected()
		if rowsAff == 0 {
			tx.Rollback()
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      "subscriber not found",
			})
			continue
		}

		desc := fmt.Sprintf("إضافة مديونية جماعية بقيمة %.2f ج.م", amount)
		_, err = tx.Exec(`INSERT INTO billing_transactions (
			username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
		) VALUES (?, ?, ?, 'debt', ?, ?, 'unpaid', 'debt', 'إضافة مديونية جماعية', 'admin')`,
			u, contractNo, fullName, desc, amount)
		if err != nil {
			tx.Rollback()
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
			continue
		}

		frec := &FinancialRecord{
			ReferenceID:        refID,
			OperationType:      "subscriber_add_debt",
			AccountType:        "subscriber",
			SubscriberID:       subID,
			SubscriberUsername: u,
			ResellerID:         resellerID,
			AmountCents:        money.ToCents(amount),
			Amount:             amount,
			BalanceBefore:      currBal,
			BalanceAfter:       currBal,
			DebtBefore:         currDebt,
			DebtAfter:          newDebt,
			Status:             "completed",
			Description:        desc,
			CreatedBy:          "admin",
		}
		if err := d.RecordFinancialTx(tx, frec); err != nil {
			tx.Rollback()
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
			continue
		}

		auditRec := &AuditLogRecord{
			ReferenceID:   refID,
			ActorUsername: "admin",
			ActorRole:     "admin",
			Action:        "bulk_add_debt",
			TargetType:    "subscriber",
			TargetID:      u,
			Details:       desc,
		}
		if err := d.RecordAuditTx(tx, auditRec); err != nil {
			tx.Rollback()
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
			continue
		}

		if err := tx.Commit(); err != nil {
			summary.Failed++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier: u,
				Success:    false,
				Error:      err.Error(),
			})
		} else {
			summary.Successful++
			summary.Items = append(summary.Items, BulkItemResult{
				Identifier:  u,
				Success:     true,
				ReferenceID: refID,
			})
		}
	}

	if summary.Failed > 0 {
		return summary, fmt.Errorf("فشل إضافة دين لـ %d من أصل %d مشترك", summary.Failed, summary.Total)
	}
	return summary, nil
}

// OwnsSubscriber returns true if the subscriber belongs to the given reseller
func (d *DB) OwnsSubscriber(resellerID int64, username string) bool {
	var count int
	_ = d.QueryRow("SELECT COUNT(*) FROM subscribers WHERE username = ? AND reseller_id = ?", username, resellerID).Scan(&count)
	return count > 0
}

// OwnsBatch returns true if the batch belongs to the given reseller
func (d *DB) OwnsBatch(resellerID int64, batchCode string) bool {
	var count int
	_ = d.QueryRow("SELECT COUNT(*) FROM batches WHERE batch_code = ? AND reseller_id = ?", batchCode, resellerID).Scan(&count)
	return count > 0
}

// captureSessionBaselinesTx records the current 64-bit traffic baselines for all active sessions of a subscriber
func (d *DB) captureSessionBaselinesTx(tx *sql.Tx, username string) {
	rowsActive, errSess := tx.Query(`SELECT acctsessionid,
		((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)),
		((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED))
		FROM radacct WHERE username = ? AND acctstoptime IS NULL`, username)
	if errSess != nil {
		return
	}
	defer rowsActive.Close()

	type sessBase struct {
		sid string
		in  uint64
		out uint64
	}
	var bases []sessBase
	for rowsActive.Next() {
		var sb sessBase
		if errScan := rowsActive.Scan(&sb.sid, &sb.in, &sb.out); errScan == nil {
			bases = append(bases, sb)
		}
	}
	for _, sb := range bases {
		_, _ = tx.Exec(`INSERT INTO subscriber_session_baselines (username, acctsessionid, baseline_input_bytes, baseline_output_bytes)
			VALUES (?, ?, ?, ?)`, username, sb.sid, sb.in, sb.out)
	}
}

// --- Billing & Financial Operations ---

func (d *DB) RenewSubscriberWithPaymentOption(username, profileName string, price float64, paymentOption string, method, notes, adminUser string, idempotencyKey ...string) error {
	var idempKey string
	if len(idempotencyKey) > 0 {
		idempKey = strings.TrimSpace(idempotencyKey[0])
	}
	if idempKey == "" {
		idempKey = GenerateDeterministicKey("subscriber_renew", username, profileName, paymentOption)
	}

	reqHash := ComputeRequestHash(map[string]interface{}{
		"username": username,
		"profile":  profileName,
		"price":    price,
		"option":   paymentOption,
	})

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Acquire idempotency key inside tx
	acquired, _, _, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "subscriber_renew", username, reqHash, 24*time.Hour)
	if err != nil {
		return err
	}
	if !acquired {
		// Key was already processed successfully; return without re-executing
		return nil
	}

	var (
		subID          int64
		resellerID     int64
		oldProfile     string
		contractNo     string
		fullName       string
		currentDebt    float64
		currentBalance float64
	)

	err = tx.QueryRow(`SELECT id, reseller_id, profile_name, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00) 
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &resellerID, &oldProfile, &contractNo, &fullName, &currentDebt, &currentBalance,
	)
	if err != nil {
		return fmt.Errorf("subscriber not found: %w", err)
	}

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

	// Always use the profile price from the database. A client supplied price is
	// only a display hint and must never change the amount charged.
	price = p.Price
	if err := money.ValidatePositive(price); err != nil {
		return fmt.Errorf("invalid profile price: %w", err)
	}

	newExp, finalQuota, deductedHours, deductedQuotaBytes, hadLoan := d.CalculateLoanDeductionTx(tx, username, &p)

	subRes, err := tx.Exec(`UPDATE subscribers SET
		profile_name = ?,
		quota_bytes = ?,
		expiration_date = ?,
		status = 'active',
		loan_active = 0,
		loan_granted_at = NULL,
		loan_hours = 0,
		loan_quota_bytes = 0,
		loan_deduction_type = 'full',
		loan_initial_used_bytes = 0,
		last_renewed_at = NOW(),
		notified_expired = 0,
		notified_quota_50 = 0,
		notified_quota_75 = 0,
		notified_quota_100 = 0
		WHERE username = ?`, p.Name, finalQuota, newExp, username)
	if err != nil {
		return fmt.Errorf("failed to update subscriber: %w", err)
	}
	rowsAff, _ := subRes.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("subscriber record not found")
	}

	if hadLoan {
		_, err = tx.Exec(`UPDATE loan_transactions SET 
			status = 'settled', 
			settled_at = NOW(), 
			used_bytes = ?, 
			deducted_hours = ?, 
			deducted_quota_bytes = ?,
			deducted_amount = ?,
			remaining_amount = 0.00
			WHERE username = ? AND status = 'active'`,
			deductedQuotaBytes, int(deductedHours), deductedQuotaBytes, price, username)
		if err != nil {
			return fmt.Errorf("failed to update loan transactions: %w", err)
		}
	}

	expStr := newExp.Format("02 Jan 2006 15:04:05")
	if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", username); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", username, expStr); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Auth-Type'", username); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM radusergroup WHERE username = ?", username); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO radusergroup (username, groupname, priority) VALUES (?, ?, 1)", username, p.Name); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM radreply WHERE username = ? AND attribute IN ('Mikrotik-Total-Limit', 'Mikrotik-Rate-Limit', 'Mikrotik-Address-List', 'Framed-Pool')", username); err != nil {
		return err
	}
	if finalQuota > 0 {
		if _, err := tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", finalQuota)); err != nil {
			return err
		}
	}

	d.captureSessionBaselinesTx(tx, username)

	if fullName == "" {
		fullName = username
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	payStatus := "paid"
	actualMethod := method
	desc := fmt.Sprintf("تجديد باقة: %s", p.DisplayName)
	if hadLoan {
		desc += fmt.Sprintf(" (تسوية سلفة: خصم %.1f ساعة و %.2f جيجا)", deductedHours, float64(deductedQuotaBytes)/(1024*1024*1024))
	}

	var paidFromBalance float64
	var addedDebt float64
	priceCents := money.ToCents(price)
	balCents := money.ToCents(currentBalance)
	debtCents := money.ToCents(currentDebt)

	switch paymentOption {
	case "balance":
		actualMethod = "balance"
		if balCents >= priceCents {
			paidFromBalance = price
			newBal := money.FromCents(balCents - priceCents)
			_, err = tx.Exec("UPDATE subscribers SET balance = ? WHERE username = ?", newBal, username)
			if err != nil {
				return err
			}
			payStatus = "paid"
			desc += " (خصم كامل من رصيد المحفظة)"
		} else if balCents > 0 {
			paidFromBalance = currentBalance
			addedDebtCents := priceCents - balCents
			addedDebt = money.FromCents(addedDebtCents)
			newDebt := money.FromCents(debtCents + addedDebtCents)
			_, err = tx.Exec("UPDATE subscribers SET balance = 0, debt = ? WHERE username = ?", newDebt, username)
			if err != nil {
				return err
			}
			payStatus = "partial"
			actualMethod = "balance+debt"
			desc += fmt.Sprintf(" (خصم جزئي من الرصيد: %.2f ج.م، ومتبقي آجل: %.2f ج.م)", paidFromBalance, addedDebt)
		} else {
			addedDebt = price
			newDebt := money.FromCents(debtCents + priceCents)
			_, err = tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newDebt, username)
			if err != nil {
				return err
			}
			payStatus = "unpaid"
			actualMethod = "debt"
			desc += " (آجل - لا يوجد رصيد)"
		}
	case "debt", "unpaid":
		addedDebt = price
		newDebt := money.FromCents(debtCents + priceCents)
		_, err = tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newDebt, username)
		if err != nil {
			return err
		}
		payStatus = "unpaid"
		if actualMethod == "" {
			actualMethod = "debt"
		}
	default: // cash / paid
		payStatus = "paid"
		if actualMethod == "" {
			actualMethod = "cash"
		}
	}

	refID := GenerateReferenceID("RENEW")
	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'renew', ?, ?, ?, ?, ?, ?)`,
		username, contractNo, fullName, desc, price, payStatus, actualMethod, notes, adminUser)
	if err != nil {
		return fmt.Errorf("failed to record billing transaction: %w", err)
	}

	frec := &FinancialRecord{
		ReferenceID:        refID,
		IdempotencyKey:     idempKey,
		OperationType:      "subscriber_renew",
		AccountType:        "subscriber",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        priceCents,
		Amount:             price,
		BalanceBefore:      currentBalance,
		BalanceAfter:       currentBalance - paidFromBalance,
		DebtBefore:         currentDebt,
		DebtAfter:          currentDebt + addedDebt,
		Status:             "completed",
		Description:        desc,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return err
	}

	// 2. Enqueue Outbox event inside transaction
	action := "coa"
	if oldProfile != "" && oldProfile != p.Name {
		action = "disconnect"
	}
	outboxPayload := OutboxPayload{
		Username:           username,
		OperationReference: refID,
		IdempotencyKey:     idempKey,
		Trigger:            "subscriber_renew",
	}
	var activeSessID, activeNAS, activeFramed string
	_ = tx.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(&activeSessID, &activeNAS, &activeFramed)
	outboxPayload.SessionID = activeSessID
	outboxPayload.NASIP = activeNAS
	outboxPayload.FramedIP = activeFramed

	if err := d.EnqueueOutboxTx(tx, action, "subscriber", username, outboxPayload); err != nil {
		return fmt.Errorf("failed to enqueue renewal outbox event: %w", err)
	}

	// 3. Mark idempotency key as completed inside tx
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"success":true,"reference_id":"%s"}`, refID)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Trigger WhatsApp notifications asynchronously
	go func() {
		sub, err := d.GetSubscriber(username)
		if err == nil && sub != nil {
			if paymentOption == "balance" && paidFromBalance > 0 {
				_ = d.TriggerWhatsAppEvent("renew_from_balance", sub, map[string]string{
					"{amount}":  fmt.Sprintf("%.2f", paidFromBalance),
					"{profile}": p.DisplayName,
				})
			} else {
				_ = d.TriggerWhatsAppEvent("renew", sub, map[string]string{
					"{amount}":  fmt.Sprintf("%.2f", price),
					"{profile}": p.DisplayName,
				})
			}
			if addedDebt > 0 {
				_ = d.TriggerWhatsAppEvent("debt_added", sub, map[string]string{
					"{amount}": fmt.Sprintf("%.2f", addedDebt),
				})
			}
		}
	}()

	return nil
}

func (d *DB) RenewSubscriberBilling(username, profileName string, price float64, isPaid bool, method, notes, adminUser string, idempotencyKey ...string) error {
	opt := "cash"
	if !isPaid {
		opt = "debt"
	}
	return d.RenewSubscriberWithPaymentOption(username, profileName, price, opt, method, notes, adminUser, idempotencyKey...)
}

// AddSubscriberBalance deposits amount to subscriber wallet.
// If settleDebtFirst is true and subscriber has debt:
// - If amount >= debt: settles debt to 0, deposits remainder to balance.
// - If amount < debt: reduces debt by amount, balance unaffected.
// Returns (newBalance, newDebt, settledDebt, error)
func (d *DB) AddSubscriberBalance(username string, amount float64, method, notes, adminUser string, settleDebtFirst bool, idempotencyKey ...string) (float64, float64, float64, error) {
	if err := money.ValidatePositive(amount); err != nil {
		return 0, 0, 0, fmt.Errorf("مبلغ الشحن يجب أن يكون أكبر من الصفر")
	}
	idempKey, err := ResolveIdempotencyKey(idempotencyKey, "subscriber_balance_deposit", username, amount, settleDebtFirst, method)
	if err != nil {
		return 0, 0, 0, err
	}

	tx, err := d.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	requestHash := ComputeRequestHash(map[string]interface{}{"username": username, "amount": amount, "method": method, "settle_debt_first": settleDebtFirst})
	acquired, _, responseBody, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "subscriber_balance_deposit", username, requestHash, 24*time.Hour)
	if err != nil {
		return 0, 0, 0, err
	}
	if !acquired {
		var cached struct{ Balance, Debt, Settled float64 }
		if json.Unmarshal([]byte(responseBody), &cached) == nil {
			return cached.Balance, cached.Debt, cached.Settled, nil
		}
		return 0, 0, 0, nil
	}

	var subID, resellerID int64
	var contractNo, fullName string
	var currDebt, currBalance float64
	err = tx.QueryRow(`SELECT id, COALESCE(reseller_id, 0), COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00) 
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(&subID, &resellerID, &contractNo, &fullName, &currDebt, &currBalance)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("المشترك غير موجود: %w", err)
	}
	if fullName == "" {
		fullName = username
	}
	if method == "" {
		method = "cash"
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	var settledDebt float64
	var newDebt, newBalance float64

	if settleDebtFirst && currDebt > 0 {
		if amount >= currDebt {
			settledDebt = currDebt
			remDeposit := amount - currDebt
			newDebt = 0
			newBalance = currBalance + remDeposit

			_, err = tx.Exec("UPDATE subscribers SET debt = 0, balance = ? WHERE username = ?", newBalance, username)
			if err != nil {
				return 0, 0, 0, err
			}

			// 1. Transaction for settling debt
			debtDesc := fmt.Sprintf("سداد كامل المديونية (%.2f ج.م) من عملية إيداع الرصيد", settledDebt)
				if _, err = tx.Exec(`INSERT INTO billing_transactions (
				username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
			) VALUES (?, ?, ?, 'payment', ?, ?, 'paid', ?, ?, ?)`,
					username, contractNo, fullName, debtDesc, settledDebt, method, notes, adminUser); err != nil {
					return 0, 0, 0, err
				}

			// 2. Transaction for remaining deposit if > 0
			if remDeposit > 0 {
				depDesc := fmt.Sprintf("إيداع رصيد بالمحفظة (المتبقي بعد تصفير الدين - الرصيد الجديد: %.2f ج.م)", newBalance)
					if _, err = tx.Exec(`INSERT INTO billing_transactions (
					username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
				) VALUES (?, ?, ?, 'deposit', ?, ?, 'paid', ?, ?, ?)`,
						username, contractNo, fullName, depDesc, remDeposit, method, notes, adminUser); err != nil {
						return 0, 0, 0, err
					}
			}
		} else { // amount < currDebt
			settledDebt = amount
			newDebt = currDebt - amount
			newBalance = currBalance

			_, err = tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newDebt, username)
			if err != nil {
				return 0, 0, 0, err
			}

			debtDesc := fmt.Sprintf("سداد جزئي للمديونية من عملية شحن الرصيد (المتبقي من الدين: %.2f ج.م)", newDebt)
				if _, err = tx.Exec(`INSERT INTO billing_transactions (
				username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
			) VALUES (?, ?, ?, 'payment', ?, ?, 'paid', ?, ?, ?)`,
					username, contractNo, fullName, debtDesc, settledDebt, method, notes, adminUser); err != nil {
					return 0, 0, 0, err
				}
		}
	} else {
		settledDebt = 0
		newDebt = currDebt
		newBalance = currBalance + amount

		_, err = tx.Exec("UPDATE subscribers SET balance = ? WHERE username = ?", newBalance, username)
		if err != nil {
			return 0, 0, 0, err
		}

		depDesc := fmt.Sprintf("شحن / إيداع رصيد بالمحفظة (الرصيد الجديد: %.2f ج.م)", newBalance)
			if _, err = tx.Exec(`INSERT INTO billing_transactions (
			username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
		) VALUES (?, ?, ?, 'deposit', ?, ?, 'paid', ?, ?, ?)`,
				username, contractNo, fullName, depDesc, amount, method, notes, adminUser); err != nil {
				return 0, 0, 0, err
			}
	}

		refID := GenerateReferenceID("DEP-SUB")
		desc := fmt.Sprintf("تغيير رصيد المشترك بمبلغ %.2f", amount)
		frec := &FinancialRecord{
			ReferenceID:        refID,
			IdempotencyKey:     idempKey,
			OperationType:      "subscriber_balance_deposit",
			AccountType:        "subscriber",
			SubscriberID:       subID,
			SubscriberUsername: username,
			ResellerID:         resellerID,
			AmountCents:        money.ToCents(amount),
			Amount:             amount,
			BalanceBefore:      currBalance,
			BalanceAfter:       newBalance,
			DebtBefore:         currDebt,
			DebtAfter:          newDebt,
			Status:             "completed",
			Description:        desc,
			CreatedBy:          adminUser,
		}
		if err := d.RecordFinancialTx(tx, frec); err != nil {
			return 0, 0, 0, err
		}
		if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"balance":%.2f,"debt":%.2f,"settled":%.2f}`, newBalance, newDebt, settledDebt)); err != nil {
			return 0, 0, 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, 0, 0, err
		}

		// Trigger notifications only after the financial transaction is durable.
		go func() {
			sub, err := d.GetSubscriber(username)
			if err == nil && sub != nil {
				if settledDebt > 0 {
					_ = d.TriggerWhatsAppEvent("debt_paid", sub, map[string]string{
						"{amount}": fmt.Sprintf("%.2f", settledDebt),
						"{debt}":   fmt.Sprintf("%.2f", newDebt),
					})
				}
				rem := amount - settledDebt
				if rem > 0 {
					_ = d.TriggerWhatsAppEvent("balance_added", sub, map[string]string{
						"{amount}":  fmt.Sprintf("%.2f", rem),
						"{balance}": fmt.Sprintf("%.2f", newBalance),
					})
				}
			}
		}()

		return newBalance, newDebt, settledDebt, nil
	}

	// SettleDebtFromBalance pays off existing subscriber debt using available wallet balance
func (d *DB) SettleDebtFromBalance(username string, amount float64, adminUser string, creditReseller bool, idempotencyKey ...string) (float64, float64, error) {
	if err := money.ValidatePositive(amount); err != nil {
		return 0, 0, fmt.Errorf("مبلغ السداد يجب أن يكون رقماً موجباً")
	}
	idempKey, err := ResolveIdempotencyKey(idempotencyKey, "subscriber_debt_balance", username, amount, creditReseller)
	if err != nil {
		return 0, 0, err
	}
	tx, err := d.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	acquired, _, responseBody, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "subscriber_debt_balance", username, ComputeRequestHash(map[string]interface{}{"username": username, "amount": amount, "credit_reseller": creditReseller}), 24*time.Hour)
	if err != nil {
		return 0, 0, err
	}
	if !acquired {
		var cached struct{ Balance, Debt float64 }
		if json.Unmarshal([]byte(responseBody), &cached) == nil {
			return cached.Balance, cached.Debt, nil
		}
		return 0, 0, nil
	}

	var contractNo, fullName string
	var currDebt, currBalance float64
	var subID, resellerID int64
	err = tx.QueryRow(`SELECT id, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00), COALESCE(reseller_id, 0) 
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(&subID, &contractNo, &fullName, &currDebt, &currBalance, &resellerID)
	if err != nil {
		return 0, 0, fmt.Errorf("المشترك غير موجود: %w", err)
	}
	if fullName == "" {
		fullName = username
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	if currBalance <= 0 {
		return currBalance, currDebt, fmt.Errorf("رصيد المحفظة صفر - لا يمكن السداد من الرصيد")
	}
	if currDebt <= 0 {
		return currBalance, currDebt, fmt.Errorf("لا توجد مديونية مسجلة على المشترك")
	}

	if amount > currBalance {
		amount = currBalance
	}
	if amount > currDebt {
		amount = currDebt
	}

	newBalance := currBalance - amount
	newDebt := currDebt - amount

	_, err = tx.Exec("UPDATE subscribers SET balance = ?, debt = ? WHERE username = ?", newBalance, newDebt, username)
	if err != nil {
		return 0, 0, err
	}

	desc := fmt.Sprintf("سداد مديونية خصماً من رصيد المحفظة (متبقي الدين: %.2f ج.م، متبقي الرصيد: %.2f ج.م)", newDebt, newBalance)

	var resellerBeforeBalance, resellerBeforeDebt, resellerAfterBalance, resellerAfterDebt float64
	var resellerName string
	// Smart settlement with reseller if applicable
	if creditReseller && resellerID > 0 {
		var resBalance, resDebt float64
		err = tx.QueryRow("SELECT name, balance, COALESCE(debt, 0.00) FROM resellers WHERE id = ? FOR UPDATE", resellerID).Scan(&resellerName, &resBalance, &resDebt)
		if err != nil {
			return 0, 0, fmt.Errorf("الموزع غير موجود: %w", err)
		}
		{
			resellerBeforeBalance, resellerBeforeDebt = resBalance, resDebt
			newResBalance := resBalance
			newResDebt := resDebt
			var noteSettlement string

			if resDebt > 0 {
				if resDebt >= amount {
					newResDebt = resDebt - amount
					noteSettlement = fmt.Sprintf("تسوية ذكية: سداد مديونية المشترك %s خصماً من محفظته بقيمة %.2f ج.م تم خصمها من مديونية الموزع", fullName, amount)
				} else {
					rem := amount - resDebt
					newResDebt = 0
					newResBalance = resBalance + rem
					noteSettlement = fmt.Sprintf("تسوية ذكية: سداد مديونية المشترك %s بقيمة %.2f ج.م تم تصفير دين الموزع وإيداع المتبقي (%.2f ج.م) بمحفظته", fullName, amount, rem)
				}
			} else {
				newResBalance = resBalance + amount
				noteSettlement = fmt.Sprintf("تسوية ذكية: سداد مديونية المشترك %s بقيمة %.2f ج.م خصماً من محفظته وتم إيداعها بمحفظة الموزع", fullName, amount)
			}

				if _, err = tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newResBalance, newResDebt, resellerID); err != nil {
					return 0, 0, err
				}
				if _, err = tx.Exec(`INSERT INTO reseller_transactions 
				(reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, subscriber_id, subscriber_name, notes, created_by)
				VALUES (?, 'subscriber_debt_settlement', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					resellerID, amount, resBalance, newResBalance, resDebt, newResDebt, subID, fullName, noteSettlement, adminUser); err != nil {
					return 0, 0, err
				}
				resellerAfterBalance, resellerAfterDebt = newResBalance, newResDebt

				desc += fmt.Sprintf(" [تسوية ذكية مع الموزع: %s]", resellerName)
		}
	}

	if _, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'payment', ?, ?, 'paid', 'balance', 'سداد من رصيد المحفظة', ?)`,
		username, contractNo, fullName, desc, amount, adminUser); err != nil {
		return 0, 0, err
	}

	refID := GenerateReferenceID("PAY-BAL")
	frecSub := &FinancialRecord{
		ReferenceID: refID, IdempotencyKey: idempKey, OperationType: "subscriber_debt_payment_balance",
		AccountType: "subscriber", SubscriberID: subID, SubscriberUsername: username, ResellerID: resellerID,
		AmountCents: money.ToCents(amount), Amount: amount, BalanceBefore: currBalance, BalanceAfter: newBalance,
		DebtBefore: currDebt, DebtAfter: newDebt, Status: "completed", Description: desc, CreatedBy: adminUser,
	}
	if err := d.RecordFinancialTx(tx, frecSub); err != nil {
		return 0, 0, err
	}
	if resellerName != "" {
		frecRes := &FinancialRecord{
			ReferenceID: refID + "-RES", IdempotencyKey: idempKey + "-res", OperationType: "subscriber_debt_settle_reseller",
			AccountType: "reseller", SubscriberID: subID, SubscriberUsername: username, ResellerID: resellerID,
			AmountCents: money.ToCents(amount), Amount: amount, BalanceBefore: resellerBeforeBalance, BalanceAfter: resellerAfterBalance,
			DebtBefore: resellerBeforeDebt, DebtAfter: resellerAfterDebt, Status: "completed", Description: desc, CreatedBy: adminUser,
		}
		if err := d.RecordFinancialTx(tx, frecRes); err != nil {
			return 0, 0, err
		}
	}
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"balance":%.2f,"debt":%.2f}`, newBalance, newDebt)); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}

	go func() {
		sub, err := d.GetSubscriber(username)
		if err == nil && sub != nil {
			_ = d.TriggerWhatsAppEvent("debt_settled_from_balance", sub, map[string]string{
				"{amount}":  fmt.Sprintf("%.2f", amount),
				"{debt}":    fmt.Sprintf("%.2f", newDebt),
				"{balance}": fmt.Sprintf("%.2f", newBalance),
			})
		}
	}()

	return newBalance, newDebt, nil
}

func (d *DB) AddQuotaBilling(username string, quotaGB float64, price float64, isPaid bool, method, notes, adminUser string, idempotencyKey ...string) error {
	if err := money.ValidateFinite(quotaGB); err != nil || quotaGB <= 0 {
		return fmt.Errorf("كمية الكوتة المضافة يجب أن تكون رقماً موجباً")
	}
	if err := money.ValidateFinite(price); err != nil || price < 0 {
		return fmt.Errorf("سعر الكوتة يجب أن يكون رقماً غير سالب")
	}

	var idempKey string
	if len(idempotencyKey) > 0 {
		idempKey = strings.TrimSpace(idempotencyKey[0])
	}
	if idempKey == "" {
		idempKey = GenerateDeterministicKey("subscriber_quota", fmt.Sprintf("%s:%.2f", username, quotaGB), price, isPaid)
	}

	reqHash := ComputeRequestHash(map[string]interface{}{
		"username": username,
		"quota_gb": quotaGB,
		"price":    price,
		"is_paid":  isPaid,
	})

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Reserve idempotency key inside tx
	acquired, _, _, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "subscriber_quota", username, reqHash, 24*time.Hour)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}

	var (
		subID       int64
		resellerID  int64
		contractNo  string
		fullName    string
		currentDebt float64
		currentBal  float64
	)
	err = tx.QueryRow(`SELECT id, reseller_id, COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00) 
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &resellerID, &contractNo, &fullName, &currentDebt, &currentBal,
	)
	if err != nil {
		return fmt.Errorf("subscriber not found: %w", err)
	}

	addBytes, err := money.QuotaGBToBytes(quotaGB)
	if err != nil {
		return err
	}
	res, err := tx.Exec("UPDATE subscribers SET quota_bytes = quota_bytes + ? WHERE username = ?", addBytes, username)
	if err != nil {
		return err
	}
	rowsAff, _ := res.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("subscriber record not found")
	}

	var currLimitStr string
	errQ := tx.QueryRow("SELECT value FROM radreply WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", username).Scan(&currLimitStr)
	if errQ == nil {
		currLimit, _ := strconv.ParseInt(currLimitStr, 10, 64)
		newLimit := currLimit + addBytes
		if _, err := tx.Exec("UPDATE radreply SET value = ? WHERE username = ? AND attribute = 'Mikrotik-Total-Limit'", fmt.Sprintf("%d", newLimit), username); err != nil {
			return fmt.Errorf("failed to update radreply total limit: %w", err)
		}
	} else {
		var newLimit int64
		_ = tx.QueryRow("SELECT quota_bytes FROM subscribers WHERE username = ?", username).Scan(&newLimit)
		if newLimit > 0 {
			if _, err := tx.Exec("INSERT INTO radreply (username, attribute, op, value) VALUES (?, 'Mikrotik-Total-Limit', ':=', ?)", username, fmt.Sprintf("%d", newLimit)); err != nil {
				return fmt.Errorf("failed to insert radreply total limit: %w", err)
			}
		}
	}

	if fullName == "" {
		fullName = username
	}

	payStatus := "paid"
	newDebt := currentDebt
	if !isPaid {
		payStatus = "unpaid"
		if price > 0 {
			priceCents := money.ToCents(price)
			debtCents := money.ToCents(currentDebt)
			newDebt = money.FromCents(debtCents + priceCents)
			_, err = tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newDebt, username)
			if err != nil {
				return err
			}
		}
	}

	if method == "" {
		method = "cash"
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	refID := GenerateReferenceID("QUOTA")
	desc := fmt.Sprintf("إضافة كوتة رصيد: %.1f GB", quotaGB)
	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'quota', ?, ?, ?, ?, ?, ?)`,
		username, contractNo, fullName, desc, price, payStatus, method, notes, adminUser)
	if err != nil {
		return err
	}

	frec := &FinancialRecord{
		ReferenceID:        refID,
		IdempotencyKey:     idempKey,
		OperationType:      "subscriber_quota_add",
		AccountType:        "subscriber",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        money.ToCents(price),
		Amount:             price,
		BalanceBefore:      currentBal,
		BalanceAfter:       currentBal,
		DebtBefore:         currentDebt,
		DebtAfter:          newDebt,
		Status:             "completed",
		Description:        desc,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frec); err != nil {
		return err
	}

	// 2. Enqueue Outbox CoA event inside transaction
	outboxPayload := OutboxPayload{
		Username:           username,
		OperationReference: refID,
		IdempotencyKey:     idempKey,
		Trigger:            "subscriber_add_quota",
	}
	var activeSessID, activeNAS, activeFramed string
	_ = tx.QueryRow(`SELECT acctsessionid, nasipaddress, framedipaddress FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1`, username).Scan(&activeSessID, &activeNAS, &activeFramed)
	outboxPayload.SessionID = activeSessID
	outboxPayload.NASIP = activeNAS
	outboxPayload.FramedIP = activeFramed

	if err := d.EnqueueOutboxTx(tx, "coa", "subscriber", username, outboxPayload); err != nil {
		return fmt.Errorf("failed to enqueue quota outbox event: %w", err)
	}

	// 3. Mark idempotency key as completed inside tx
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"success":true,"reference_id":"%s"}`, refID)); err != nil {
		return err
	}

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

func (d *DB) PaySubscriberDebt(username string, amount float64, method, notes, adminUser string, creditReseller bool, idempotencyKey ...string) (bool, string, error) {
	if err := money.ValidateFinite(amount); err != nil || amount <= 0 {
		return false, "", fmt.Errorf("المبلغ يجب أن يكون رقماً موجباً أكبر من صفر")
	}

	var idempKey string
	if len(idempotencyKey) > 0 {
		idempKey = strings.TrimSpace(idempotencyKey[0])
	}
	if idempKey == "" {
		idempKey = GenerateDeterministicKey("subscriber_pay_debt", fmt.Sprintf("%s:%.2f", username, amount), method, creditReseller)
	}

	reqHash := ComputeRequestHash(map[string]interface{}{
		"username":        username,
		"amount":          amount,
		"method":          method,
		"credit_reseller": creditReseller,
	})

	tx, err := d.Begin()
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()

	// 1. Reserve idempotency key inside tx
	acquired, _, _, err := d.AcquireIdempotencyKeyTx(tx, idempKey, "subscriber_pay_debt", username, reqHash, 24*time.Hour)
	if err != nil {
		return false, "", err
	}
	if !acquired {
		return true, "تم سداد المديونية مسبقاً بنجاح", nil
	}

	var (
		subID       int64
		resellerID  int64
		contractNo  string
		fullName    string
		currentDebt float64
		currentBal  float64
	)
	err = tx.QueryRow(`SELECT id, COALESCE(reseller_id, 0), COALESCE(contract_no, ''), COALESCE(full_name, ''), COALESCE(debt, 0.00), COALESCE(balance, 0.00) 
		FROM subscribers WHERE username = ? FOR UPDATE`, username).Scan(
		&subID, &resellerID, &contractNo, &fullName, &currentDebt, &currentBal,
	)
	if err != nil {
		return false, "", fmt.Errorf("المشترك غير موجود")
	}
	if fullName == "" {
		fullName = username
	}

	debtCents := money.ToCents(currentDebt)
	if debtCents <= 0 {
		return false, "", fmt.Errorf("لا توجد مديونية مسجلة على المشترك لسدادها")
	}

	amountCents := money.ToCents(amount)
	if amountCents > debtCents {
		amountCents = debtCents
	}
	actualAmount := money.FromCents(amountCents)
	newDebtCents := debtCents - amountCents
	newDebt := money.FromCents(newDebtCents)

	updateRes, err := tx.Exec("UPDATE subscribers SET debt = ? WHERE username = ?", newDebt, username)
	if err != nil {
		return false, "", err
	}
	rowsAff, _ := updateRes.RowsAffected()
	if rowsAff == 0 {
		return false, "", fmt.Errorf("subscriber record not found")
	}

	if method == "" {
		method = "cash"
	}
	if adminUser == "" {
		adminUser = "admin"
	}

	desc := fmt.Sprintf("سداد نقدي من المديونية (متبقي: %s)", money.FormatEGP(newDebtCents))
	resellerCredited := false
	successMsg := fmt.Sprintf("تم سداد مبلغ %s من مديونية المشترك %s بنجاح", money.FormatEGP(amountCents), fullName)

	refID := GenerateReferenceID("PAY-DEBT")

	// Smart Debt Settlement with Reseller
	if creditReseller && resellerID > 0 {
		var (
			resName    string
			resBalance float64
			resDebt    float64
		)
		err = tx.QueryRow("SELECT name, balance, COALESCE(debt, 0.00) FROM resellers WHERE id = ? FOR UPDATE", resellerID).Scan(&resName, &resBalance, &resDebt)
		if err == nil {
			resellerCredited = true
			resBalCents := money.ToCents(resBalance)
			resDebtCents := money.ToCents(resDebt)
			var newResBalCents, newResDebtCents int64
			var noteSettlement string

			if resDebtCents > 0 {
				if resDebtCents >= amountCents {
					newResDebtCents = resDebtCents - amountCents
					newResBalCents = resBalCents
					noteSettlement = fmt.Sprintf("تسوية ذكية: سداد مديونية المشترك %s (%s) بقيمة %s تم خصمها من مديونية الموزع طرف الإدارة (متبقي دين الموزع: %s)", fullName, username, money.FormatEGP(amountCents), money.FormatEGP(newResDebtCents))
					successMsg = fmt.Sprintf("تم سداد مديونية المشترك %s وتخفيض مديونية الموزع %s طرف الإدارة", money.FormatEGP(amountCents), resName)
				} else {
					remCents := amountCents - resDebtCents
					newResDebtCents = 0
					newResBalCents = resBalCents + remCents
					noteSettlement = fmt.Sprintf("تسوية ذكية: سداد مديونية المشترك %s (%s) بقيمة %s تم تصفير دين الموزع وإيداع المتبقي (%s) في محفظته", fullName, username, money.FormatEGP(amountCents), money.FormatEGP(remCents))
					successMsg = fmt.Sprintf("تم سداد مديونية المشترك %s وتصفير دين الموزع %s وإيداع المتبقي (%s) بمحفظته", money.FormatEGP(amountCents), resName, money.FormatEGP(remCents))
				}
			} else {
				newResDebtCents = resDebtCents
				newResBalCents = resBalCents + amountCents
				noteSettlement = fmt.Sprintf("تسوية ذكية: سداد مديونية المشترك %s (%s) بقيمة %s طرف الإدارة وتم إيداعها في محفظة الموزع", fullName, username, money.FormatEGP(amountCents))
				successMsg = fmt.Sprintf("تم سداد مديونية المشترك %s وإيداعها مباشرة في محفظة الموزع %s", money.FormatEGP(amountCents), resName)
			}

			newResBalance := money.FromCents(newResBalCents)
			newResDebt := money.FromCents(newResDebtCents)

			_, err = tx.Exec("UPDATE resellers SET balance = ?, debt = ? WHERE id = ?", newResBalance, newResDebt, resellerID)
			if err != nil {
				return false, "", err
			}

			_, err = tx.Exec(`INSERT INTO reseller_transactions 
				(reseller_id, type, amount, balance_before, balance_after, debt_before, debt_after, subscriber_id, subscriber_name, notes, created_by)
				VALUES (?, 'subscriber_debt_settlement', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				resellerID, actualAmount, resBalance, newResBalance, resDebt, newResDebt, subID, fullName, noteSettlement, adminUser)
			if err != nil {
				return false, "", err
			}

			frecRes := &FinancialRecord{
				ReferenceID:        refID + "-RES",
				IdempotencyKey:     idempKey + "-res",
				OperationType:      "subscriber_debt_settle_reseller",
				AccountType:        "reseller",
				SubscriberID:       subID,
				SubscriberUsername: username,
				ResellerID:         resellerID,
				AmountCents:        amountCents,
				Amount:             actualAmount,
				BalanceBefore:      resBalance,
				BalanceAfter:       newResBalance,
				DebtBefore:         resDebt,
				DebtAfter:          newResDebt,
				Status:             "completed",
				Description:        noteSettlement,
				CreatedBy:          adminUser,
			}
			if err := d.RecordFinancialTx(tx, frecRes); err != nil {
				return false, "", err
			}

			desc += fmt.Sprintf(" [تسوية ذكية مع الموزع: %s]", resName)
		}
	}

	_, err = tx.Exec(`INSERT INTO billing_transactions (
		username, contract_no, full_name, type, description, amount, payment_status, payment_method, notes, created_by
	) VALUES (?, ?, ?, 'payment', ?, ?, 'paid', ?, ?, ?)`,
		username, contractNo, fullName, desc, actualAmount, method, notes, adminUser)
	if err != nil {
		return false, "", err
	}

	frecSub := &FinancialRecord{
		ReferenceID:        refID,
		IdempotencyKey:     idempKey,
		OperationType:      "subscriber_debt_payment",
		AccountType:        "subscriber",
		SubscriberID:       subID,
		SubscriberUsername: username,
		ResellerID:         resellerID,
		AmountCents:        amountCents,
		Amount:             actualAmount,
		BalanceBefore:      currentBal,
		BalanceAfter:       currentBal,
		DebtBefore:         currentDebt,
		DebtAfter:          newDebt,
		Status:             "completed",
		Description:        desc,
		CreatedBy:          adminUser,
	}
	if err := d.RecordFinancialTx(tx, frecSub); err != nil {
		return false, "", err
	}

	// 2. Complete idempotency key inside tx
	if err := d.CompleteIdempotencyKeyTx(tx, idempKey, 200, fmt.Sprintf(`{"success":true,"reference_id":"%s"}`, refID)); err != nil {
		return false, "", err
	}

	if err := tx.Commit(); err != nil {
		return false, "", err
	}

	go func() {
		sub, err := d.GetSubscriber(username)
		if err == nil && sub != nil {
			_ = d.TriggerWhatsAppEvent("debt_paid", sub, map[string]string{
				"{amount}": money.FormatEGP(amountCents),
				"{debt}":   money.FormatEGP(newDebtCents),
			})
		}
	}()

	return resellerCredited, successMsg, nil
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
		"{name}":        sub.FullName,
		"{username}":    sub.Username,
		"{contract_no}": sub.ContractNo,
		"{phone}":       sub.Phone,
		"{profile}":     sub.ProfileName,
		"{debt}":        fmt.Sprintf("%.2f", sub.Debt),
		"{quota_total}": fmt.Sprintf("%.1f", float64(sub.QuotaBytes)/(1024*1024*1024)),
		"{date}":        time.Now().Format("2006-01-02"),
		"{time}":        time.Now().Format("15:04"),
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
				if sq.QuotaBytes <= 0 {
					continue
				}

				uc, err := d.GetSubscriberUsageCycle(sq.Username)
				if err != nil || uc == nil {
					continue
				}

				usedBytes := uc.UsedBytes
				usagePercent := uc.PercentUsed
				remainingBytes := uc.RemainingBytes

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

func (d *DB) DisconnectProfileActiveSessions(profileName string) (int, int, error) {
	rows, err := d.Query(`SELECT r.acctsessionid, r.username, r.nasipaddress, r.framedipaddress
		FROM radacct r
		JOIN radusergroup ug ON r.username = ug.username
		WHERE r.acctstoptime IS NULL AND ug.groupname = ?`, profileName)
	if err != nil {
		return 0, 0, err
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

	successCount := 0
	failCount := 0
	for _, s := range sessions {
		var secret string
		var coaPort int
		_ = d.QueryRow("SELECT secret, coa_port FROM nas WHERE nasname = ?", s.nasIP).Scan(&secret, &coaPort)
		if secret == "" {
			failCount++
			continue // Skip unregistered NAS without secret
		}
		if coaPort <= 0 {
			coaPort = 3799
		}

		var fIP net.IP
		if s.framedIP != "" {
			fIP = net.ParseIP(s.framedIP)
		}

		res, err := radius.SendDisconnectRequest(s.nasIP, coaPort, []byte(secret), s.username, s.sessionID, fIP)
		if err == nil && res != nil && res.Success {
			successCount++
			_, _ = d.Exec(`UPDATE radacct SET disconnect_status = 'pending', disconnect_pending_at = CURRENT_TIMESTAMP
				WHERE acctsessionid = ? AND acctstoptime IS NULL`, s.sessionID)
		} else {
			failCount++
		}
	}
	return successCount, failCount, nil
}

func (d *DB) DisconnectAllActiveSessions() (int, int, error) {
	rows, err := d.Query(`SELECT acctsessionid, username, nasipaddress, framedipaddress
		FROM radacct WHERE acctstoptime IS NULL`)
	if err != nil {
		return 0, 0, err
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
					continue
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

	var (
		mu           sync.Mutex
		successCount int
		failCount    int
		wg           sync.WaitGroup
	)

	for _, s := range sessions {
		wg.Add(1)
		go func(sess sess) {
			defer wg.Done()
			nasInfo, ok := nasMap[sess.nasIP]
			if !ok || nasInfo.secret == "" {
				mu.Lock()
				failCount++
				mu.Unlock()
				return
			}
			secret := nasInfo.secret
			coaPort := nasInfo.coaPort

			var fIP net.IP
			if sess.framedIP != "" {
				fIP = net.ParseIP(sess.framedIP)
			}

			res, err := radius.SendDisconnectRequest(sess.nasIP, coaPort, []byte(secret), sess.username, sess.sessionID, fIP)
			mu.Lock()
			if err == nil && res != nil && res.Success {
				successCount++
				_, _ = d.Exec(`UPDATE radacct SET disconnect_status = 'pending', disconnect_pending_at = CURRENT_TIMESTAMP
					WHERE acctsessionid = ? AND acctstoptime IS NULL`, sess.sessionID)
			} else {
				failCount++
			}
			mu.Unlock()
		}(s)
	}

	// Wait up to 5 seconds for CoA UDP packets to transmit
	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
	}

	subTrafficMu.Lock()
	subTrafficHistory = make(map[string]*subTrafficPoint)
	subTrafficMu.Unlock()

	return successCount, failCount, nil
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
	_ = d.QueryRow("SELECT COALESCE(SUM(CASE WHEN status = 'available' THEN 1 ELSE 0 END), 0), COALESCE(SUM(CASE WHEN status != 'available' THEN 1 ELSE 0 END), 0) FROM cards WHERE batch_id = ?", b.ID).Scan(&b.AvailableCount, &b.UsedCount)

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
	query += " ORDER BY b.id DESC LIMIT 2000"

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Batch
	var batchIDs []interface{}
	for rows.Next() {
		var b models.Batch
		if err := rows.Scan(&b.ID, &b.BatchCode, &b.BatchName, &b.ProfileID, &b.ProfileName, &b.Count,
			&b.CostPricePerCard, &b.PricePerCard, &b.TotalCostPrice, &b.TotalPrice, &b.CreatedAt, &b.NASID, &b.NASName); err == nil {
			b.TotalProfit = b.TotalPrice - b.TotalCostPrice
			b.AvailableCount = b.Count
			list = append(list, b)
			batchIDs = append(batchIDs, b.ID)
		}
	}

	if len(batchIDs) > 0 {
		placeholders := make([]string, len(batchIDs))
		for i := range batchIDs {
			placeholders[i] = "?"
		}
		cRows, err := d.Query(fmt.Sprintf(`SELECT batch_id,
			COALESCE(SUM(CASE WHEN status = 'available' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status != 'available' THEN 1 ELSE 0 END), 0)
			FROM cards WHERE batch_id IN (%s) GROUP BY batch_id`, strings.Join(placeholders, ",")), batchIDs...)
		if err == nil {
			defer cRows.Close()
			stats := make(map[int64][2]int)
			for cRows.Next() {
				var bid int64
				var avail, used int
				if err := cRows.Scan(&bid, &avail, &used); err == nil {
					stats[bid] = [2]int{avail, used}
				}
			}
			for i := range list {
				if s, ok := stats[list[i].ID]; ok {
					list[i].AvailableCount = s[0]
					list[i].UsedCount = s[1]
				}
			}
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
		return nil, fmt.Errorf("يرجى إدخال كود الكارت أو الماك أدرس")
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
		p.name, p.display_name, p.rate_limit, p.quota_bytes,
		COALESCE(c.original_code, ''), c.is_mac_converted, COALESCE(c.calling_mac, '')
		FROM cards c
		LEFT JOIN batches b ON c.batch_id = b.id
		LEFT JOIN nas n ON c.nas_id = n.id
		LEFT JOIN profiles p ON c.profile_id = p.id
		WHERE c.username = ? OR (c.original_code != '' AND c.original_code = ?) OR (c.calling_mac != '' AND c.calling_mac = ?)
		ORDER BY c.id DESC LIMIT 1`

	row := d.QueryRow(query, code, code, code)
	err := row.Scan(&c.ID, &c.Username, &c.Password, &c.Price, &c.Status, &c.FirstLoginAt, &c.ExpiredAt,
		&batchID, &batchCode, &nasID, &nasName,
		&profileName, &profileDisp, &rateLimit, &quotaBytes,
		&c.OriginalCode, &c.IsMACConverted, &c.CallingMAC)
	if err != nil {
		return nil, fmt.Errorf("الكارت أو الماك '%s' غير مسجل في النظام", code)
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
		tracker.QuotaDisplay = "غير محدود (Open ∞)"
	}

	// 2. Query Accounting consumption from radacct
	var sumIn, sumOut, sumTime sql.NullInt64
	var minStart sql.NullTime
	var lastMAC, lastIP sql.NullString

	_ = d.QueryRow(`SELECT 
		COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM(acctsessiontime), 0),
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

	// 6. Device MAC Multi-Card History & Tracking (سجل كروت وجلسات هذا الجهاز)
	if tracker.CallingMAC != "" {
		tracker.DeviceCallingMAC = tracker.CallingMAC

		// Fetch all cards used by this MAC address
		cardRows, err := d.Query(`SELECT c.id, COALESCE(c.original_code, c.username), c.username,
			COALESCE(b.batch_code, ''), COALESCE(p.name, 'افتراضي'), c.price, c.status,
			c.first_login_at, c.expired_at, c.is_mac_converted
			FROM cards c
			LEFT JOIN batches b ON c.batch_id = b.id
			LEFT JOIN profiles p ON c.profile_id = p.id
			WHERE (c.calling_mac != '' AND c.calling_mac = ?)
			   OR c.username = ?
			   OR (c.original_code != '' AND c.original_code = ?)
			ORDER BY c.id DESC`, tracker.CallingMAC, tracker.CallingMAC, tracker.CallingMAC)
		if err == nil {
			var deviceTotalBytes int64
			var deviceTotalUptime int64

			for cardRows.Next() {
				var item models.CafeDeviceCardItem
				var origCode, currentU, batchC, profN, st string
				var pr float64
				var firstLog, expAt sql.NullTime
				var isConverted bool
				var cid int64

				if err := cardRows.Scan(&cid, &origCode, &currentU, &batchC, &profN, &pr, &st, &firstLog, &expAt, &isConverted); err == nil {
					item.CardID = cid
					item.OriginalCode = origCode
					item.Username = currentU
					item.BatchCode = batchC
					item.ProfileName = profN
					item.Price = pr
					item.Status = st

					if firstLog.Valid {
						item.FirstLogin = firstLog.Time.Format("2006-01-02 15:04:05")
					} else {
						item.FirstLogin = "لم يبدأ"
					}

					if expAt.Valid {
						item.ExpiredAt = expAt.Time.Format("2006-01-02 15:04:05")
					} else {
						item.ExpiredAt = "—"
					}

					if item.CardID == c.ID && tracker.IsOnline {
						item.IsActive = true
						item.StatusLabel = "نشط ومتصل الآن"
						item.StatusBadge = "bg-success text-white"
					} else if st == "expired" {
						item.StatusLabel = "منتهي الكوتة/الوقت"
						item.StatusBadge = "bg-danger text-white"
					} else if st == "used" {
						item.StatusLabel = "مستهلك سابقاً"
						item.StatusBadge = "bg-secondary text-white"
					} else {
						item.StatusLabel = "متاح / جاهز"
						item.StatusBadge = "bg-info text-white"
					}

					// Calculate consumption for this specific card from radacct
					var cardIn, cardOut, cardSec sql.NullInt64
					var sessCount int
					_ = d.QueryRow(`SELECT 
						COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)), 0),
						COALESCE(SUM((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0),
						COALESCE(SUM(acctsessiontime), 0), COUNT(*)
						FROM radacct
						WHERE username = ? OR (username != '' AND username = ?)`, currentU, origCode).Scan(&cardIn, &cardOut, &cardSec, &sessCount)

					var totalB, sec int64
					if cardIn.Valid {
						totalB += cardIn.Int64
						item.BytesInDisplay = FormatBytesHuman(cardIn.Int64)
					}
					if cardOut.Valid {
						totalB += cardOut.Int64
						item.BytesOutDisplay = FormatBytesHuman(cardOut.Int64)
					}
					if cardSec.Valid {
						sec = cardSec.Int64
					}
					item.TotalBytes = totalB
					item.TotalDisplay = FormatBytesHuman(totalB)
					item.UptimeDisplay = FormatDurationHuman(sec)
					item.SessionsCount = sessCount

					deviceTotalBytes += totalB
					deviceTotalUptime += sec

					tracker.DeviceCardsHistory = append(tracker.DeviceCardsHistory, item)
				}
			}
			cardRows.Close()

			tracker.DeviceTotalCardsCount = len(tracker.DeviceCardsHistory)
			tracker.DeviceTotalTraffic = FormatBytesHuman(deviceTotalBytes)
			tracker.DeviceTotalUptime = FormatDurationHuman(deviceTotalUptime)
		}

		// Fetch detailed radacct sessions for this device MAC
		sessRows, err := d.Query(`SELECT r.acctsessionid, r.username,
			COALESCE(r.acctstarttime, '0000-00-00 00:00:00'),
			COALESCE(r.acctstoptime, ''),
			r.acctsessiontime,
			r.acctinputoctets, r.acctoutputoctets,
			COALESCE(r.framedipaddress, ''),
			COALESCE(n.shortname, r.nasipaddress),
			COALESCE(r.acctterminatecause, '')
			FROM radacct r
			LEFT JOIN nas n ON r.nasipaddress = n.nasname
			WHERE r.callingstationid = ?
			ORDER BY r.radacctid DESC
			LIMIT 50`, tracker.CallingMAC)
		if err == nil {
			for sessRows.Next() {
				var sItem models.CafeDeviceSessionItem
				var sessID, u, startT, stopT, framedIP, nasN, termCause string
				var sessSec, inB, outB int64

				if err := sessRows.Scan(&sessID, &u, &startT, &stopT, &sessSec, &inB, &outB, &framedIP, &nasN, &termCause); err == nil {
					sItem.SessionID = sessID
					sItem.CardCode = u
					sItem.StartTime = startT
					if stopT == "" || stopT == "0000-00-00 00:00:00" {
						sItem.StopTime = "متصل حالياً (Live)"
						sItem.IsLive = true
					} else {
						sItem.StopTime = stopT
					}
					sItem.Duration = FormatDurationHuman(sessSec)
					sItem.BytesInDisplay = FormatBytesHuman(inB)
					sItem.BytesOutDisplay = FormatBytesHuman(outB)
					sItem.TotalDisplay = FormatBytesHuman(inB + outB)
					sItem.FramedIP = framedIP
					sItem.NASName = nasN
					sItem.TerminateCause = termCause

					tracker.DeviceSessionsHistory = append(tracker.DeviceSessionsHistory, sItem)
				}
			}
			sessRows.Close()
		}
	}

	return tracker, nil
}

func (d *DB) DisconnectCard(code string) (bool, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return false, fmt.Errorf("كود الكارت مطلوب")
	}

	rows, err := d.Query(`SELECT acctsessionid, nasipaddress, framedipaddress
		FROM radacct 
		WHERE (username = ? OR (username != '' AND username = (SELECT original_code FROM cards WHERE username = ? OR original_code = ? LIMIT 1))) 
		  AND acctstoptime IS NULL`, code, code, code)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	type sessInfo struct {
		sessionID string
		nasIP     string
		framedIP  string
	}
	var sessions []sessInfo
	for rows.Next() {
		var s sessInfo
		var fIP sql.NullString
		if errScan := rows.Scan(&s.sessionID, &s.nasIP, &fIP); errScan == nil {
			if fIP.Valid {
				s.framedIP = fIP.String
			}
			sessions = append(sessions, s)
		}
	}

	if len(sessions) == 0 {
		return false, fmt.Errorf("الكارت غير متصل أونلاين حالياً")
	}

	successCount := 0
	for _, s := range sessions {
		var secret string
		var coaPort int
		_ = d.QueryRow("SELECT secret, coa_port FROM nas WHERE nasname = ?", s.nasIP).Scan(&secret, &coaPort)
		if secret == "" {
			continue
		}
		if coaPort <= 0 {
			coaPort = 3799
		}

		var framedIP net.IP
		if s.framedIP != "" {
			framedIP = net.ParseIP(s.framedIP)
		}

		res, err := radius.SendDisconnectRequest(s.nasIP, coaPort, []byte(secret), code, s.sessionID, framedIP)
		if err == nil && res != nil && res.Success {
			successCount++
			_, _ = d.Exec(`UPDATE radacct SET disconnect_status = 'pending', disconnect_pending_at = CURRENT_TIMESTAMP
				WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`, s.sessionID, s.nasIP)
		}
	}

	return successCount > 0, nil
}

// StartCardSyncDaemon handles real-time synchronization between active accounting sessions (radacct)
// and cards table: tracking first logins, binding & locking to client MAC address, and auto-disabling expired cards.
func (d *DB) StartCardSyncDaemon() {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			d.syncActiveCards()
		}
	}()
}

func (d *DB) syncActiveCards() {
	// 1. MAC Conversion After First Session (التحويل لماك أدرس بعد انتهاء أول جلسة عند تسجيل الخروج):
	var needConvert int
	_ = d.QueryRow("SELECT COUNT(*) FROM cards WHERE convert_mac_after_session = 1 AND is_mac_converted = 0").Scan(&needConvert)
	if needConvert > 0 {
		rowsConvert, err := d.Query(`SELECT c.id, c.username,
			(SELECT r.callingstationid 
			 FROM radacct r 
			 WHERE r.username = c.username
			   AND r.callingstationid != '' AND r.acctstoptime IS NOT NULL
			 ORDER BY r.radacctid DESC LIMIT 1) AS last_mac
			FROM cards c
			WHERE c.convert_mac_after_session = 1 
			  AND c.is_mac_converted = 0
			  AND EXISTS (
			      SELECT 1 FROM radacct r1 
			      WHERE r1.username = c.username
			        AND r1.acctstoptime IS NOT NULL
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM radacct r2 
			      WHERE r2.username = c.username
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

				// If any other card currently holds this MAC as username, revert it to its original_code
				// This completely prevents MySQL "Duplicate entry for key username" and allows multiple cards on same MAC
				_, _ = d.Exec(`UPDATE cards SET username = original_code WHERE username = ? AND id != ?`, mac, item.cardID)

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
	}

	// 2. MAC Binding & Locking: For cards with bind_mac=1 or lock_mac=1 that have not captured calling_mac yet
	var needBind int
	_ = d.QueryRow("SELECT COUNT(*) FROM cards WHERE (bind_mac = 1 OR lock_mac = 1) AND (calling_mac = '' OR calling_mac IS NULL)").Scan(&needBind)
	if needBind > 0 {
		rowsMAC, err := d.Query(`SELECT c.id, c.username, c.lock_mac, r.callingstationid
			FROM cards c
			JOIN radacct r ON r.username = c.username
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
	}

	// 3. First Login Tracking for Calendar/First-Login time calculation mode (Direct index join)
	rowsLogin, err := d.Query(`SELECT c.id, c.username, c.time_limit_seconds, c.time_calc_mode, MIN(r.acctstarttime)
		FROM cards c
		JOIN radacct r ON r.username = c.username
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
				_, _ = d.Exec("UPDATE cards SET first_login_at = ?, expired_at = ?, status = 'used' WHERE id = ?", le.firstLogin, expDate, le.cardID)
				// Set Expiration attribute in FreeRADIUS radcheck
				expStr := expDate.Format("02 Jan 2006 15:04:05")
				_, _ = d.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Expiration'", le.username)
				_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Expiration', ':=', ?)", le.username, expStr)
			} else {
				_, _ = d.Exec("UPDATE cards SET first_login_at = ?, status = 'used' WHERE id = ?", le.firstLogin, le.cardID)
			}
		}
	}

	// 4. Auto-Expiry Enforcement for Quota & Time Limits
	// Check calendar-expired cards
	rowsExp, err := d.Query(`SELECT c.id, c.username FROM cards c
		WHERE (c.status = 'available' OR c.status = 'used') AND c.expired_at IS NOT NULL AND c.expired_at < CURRENT_TIMESTAMP`)
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

	// Check quota-exhausted cards using gigaword-aware calculation
	rowsQuota, err := d.Query(fmt.Sprintf(`SELECT c.id, c.username, c.quota_bytes, %s
		FROM cards c
		JOIN radacct r ON r.username = c.username
		WHERE (c.status = 'available' OR c.status = 'used') AND c.stop_on_quota = 1 AND c.quota_bytes > 0
		GROUP BY c.id, c.username, c.quota_bytes
		HAVING %s >= c.quota_bytes`, RadacctAliasedSumBytesSQL, RadacctAliasedSumBytesSQL))
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
		sessID, nasIP, framedIP, callingMAC sql.NullString
		sessStart, sessUpdate               sql.NullTime
		sessTime, inOctets, outOctets       sql.NullInt64
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

	// 4. Accounting Cycle Consumption via UsageCycle
	uc, errUC := d.GetSubscriberUsageCycle(username)
	if errUC == nil && uc != nil {
		traffic.TotalUploadBytes = uc.UsedInputBytes
		traffic.TotalDownloadBytes = uc.UsedOutputBytes
		traffic.TotalBytes = uc.UsedBytes
		traffic.TotalBytesDisplay = FormatBytesHuman(uc.UsedBytes)

		// 5. Quota Calculations
		if traffic.QuotaBytes > 0 {
			traffic.QuotaDisplay = FormatBytesHuman(traffic.QuotaBytes)
			traffic.QuotaRemainingBytes = uc.RemainingBytes
			traffic.QuotaRemainingDisplay = FormatBytesHuman(uc.RemainingBytes)
			traffic.QuotaUsagePercent = uc.PercentUsed
		} else {
			traffic.QuotaDisplay = "غير محدود ∞"
			traffic.QuotaRemainingDisplay = "مفتوح ∞"
			traffic.QuotaUsagePercent = 0.0
		}
	} else {
		traffic.TotalBytesDisplay = "0 B"
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

// GetSubscriberUsageCycle calculates the unified and gigaword-aware accounting cycle
// consumption for a subscriber, accounting for renewal baselines and active sessions.
func (d *DB) GetSubscriberUsageCycle(username string) (*models.UsageCycle, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("اسم المشترك مطلوب")
	}

	sub, err := d.GetSubscriber(username)
	if err != nil {
		return nil, fmt.Errorf("المشترك غير موجود: %w", err)
	}

	cycleStart := sub.CreatedAt
	var renewalTS *time.Time
	var lastRenewed sql.NullTime
	_ = d.QueryRow("SELECT last_renewed_at FROM subscribers WHERE username = ?", username).Scan(&lastRenewed)
	if lastRenewed.Valid {
		cycleStart = lastRenewed.Time
		t := lastRenewed.Time
		renewalTS = &t
	}

	quotaLimit := sub.QuotaBytes
	if sub.LoanActive && sub.LoanGrantedAt.Valid {
		cycleStart = sub.LoanGrantedAt.Time
		if sub.LoanQuotaBytes > 0 {
			quotaLimit = sub.LoanQuotaBytes
		}
	}

	// 1. Calculate bytes from completed sessions that started on or after cycleStart
	var closedIn, closedOut int64
	_ = d.QueryRow(`SELECT 
		COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0)
		FROM radacct 
		WHERE username = ? 
		  AND acctstoptime IS NOT NULL 
		  AND acctstarttime >= ?`, username, cycleStart).Scan(&closedIn, &closedOut)

	// Check any completed sessions that started before cycleStart but stopped on or after cycleStart
	rowsCross, err := d.Query(`SELECT acctsessionid,
		((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)),
		((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED))
		FROM radacct
		WHERE username = ? 
		  AND acctstoptime IS NOT NULL 
		  AND acctstarttime < ? 
		  AND acctstoptime >= ?`, username, cycleStart, cycleStart)
	if err == nil {
		for rowsCross.Next() {
			var sessID string
			var totIn, totOut int64
			if errScan := rowsCross.Scan(&sessID, &totIn, &totOut); errScan == nil {
				var baseIn, baseOut uint64
				_ = d.QueryRow("SELECT baseline_input_bytes, baseline_output_bytes FROM subscriber_session_baselines WHERE username = ? AND acctsessionid = ? ORDER BY id DESC LIMIT 1", username, sessID).Scan(&baseIn, &baseOut)
				deltaIn := totIn - int64(baseIn)
				if deltaIn < 0 {
					deltaIn = 0
				}
				deltaOut := totOut - int64(baseOut)
				if deltaOut < 0 {
					deltaOut = 0
				}
				closedIn += deltaIn
				closedOut += deltaOut
			}
		}
		rowsCross.Close()
	}

	// 2. Add active (online) sessions (acctstoptime IS NULL)
	var activeIn, activeOut int64
	rowsActive, err := d.Query(`SELECT acctsessionid, acctstarttime,
		((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)),
		((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED))
		FROM radacct
		WHERE username = ? AND acctstoptime IS NULL`, username)
	if err == nil {
		for rowsActive.Next() {
			var sessID string
			var startTime time.Time
			var totIn, totOut int64
			if errScan := rowsActive.Scan(&sessID, &startTime, &totIn, &totOut); errScan == nil {
				if startTime.Before(cycleStart) {
					var baseIn, baseOut uint64
					_ = d.QueryRow("SELECT baseline_input_bytes, baseline_output_bytes FROM subscriber_session_baselines WHERE username = ? AND acctsessionid = ? ORDER BY id DESC LIMIT 1", username, sessID).Scan(&baseIn, &baseOut)
					deltaIn := totIn - int64(baseIn)
					if deltaIn < 0 {
						deltaIn = 0
					}
					deltaOut := totOut - int64(baseOut)
					if deltaOut < 0 {
						deltaOut = 0
					}
					activeIn += deltaIn
					activeOut += deltaOut
				} else {
					activeIn += totIn
					activeOut += totOut
				}
			}
		}
		rowsActive.Close()
	}

	totalIn := closedIn + activeIn
	totalOut := closedOut + activeOut
	totalUsed := totalIn + totalOut

	var remaining int64
	var percentUsed float64
	var isExhausted bool

	if quotaLimit > 0 {
		remaining = quotaLimit - totalUsed
		if remaining < 0 {
			remaining = 0
		}
		percentUsed = (float64(totalUsed) / float64(quotaLimit)) * 100.0
		if percentUsed > 100.0 {
			percentUsed = 100.0
		}
		isExhausted = totalUsed >= quotaLimit
	} else {
		remaining = 0
		percentUsed = 0.0
		isExhausted = false
	}

	return &models.UsageCycle{
		Username:         username,
		CycleStart:       cycleStart,
		RenewalTimestamp: renewalTS,
		QuotaLimitBytes:  quotaLimit,
		UsedInputBytes:   totalIn,
		UsedOutputBytes:  totalOut,
		UsedBytes:        totalUsed,
		RemainingBytes:   remaining,
		PercentUsed:      percentUsed,
		IsExhausted:      isExhausted,
	}, nil
}

// GetCardUsageCycle calculates the unified and gigaword-aware accounting cycle consumption for a hotspot/cafe card.
func (d *DB) GetCardUsageCycle(cardCode string) (*models.UsageCycle, error) {
	cardCode = strings.TrimSpace(cardCode)
	if cardCode == "" {
		return nil, fmt.Errorf("كود الكارت مطلوب")
	}

	var quotaLimit int64
	var firstLogin sql.NullTime
	var createdAt time.Time
	var origCode string
	err := d.QueryRow("SELECT quota_bytes, first_login_at, created_at, COALESCE(original_code, '') FROM cards WHERE username = ? OR original_code = ? LIMIT 1", cardCode, cardCode).Scan(&quotaLimit, &firstLogin, &createdAt, &origCode)
	if err != nil {
		return nil, fmt.Errorf("الكارت غير موجود: %w", err)
	}

	cycleStart := createdAt
	if firstLogin.Valid {
		cycleStart = firstLogin.Time
	}

	var totalIn, totalOut int64
	_ = d.QueryRow(`SELECT 
		COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0)
		FROM radacct 
		WHERE username = ? OR (username != '' AND username = ?)`, cardCode, origCode).Scan(&totalIn, &totalOut)

	totalUsed := totalIn + totalOut
	var remaining int64
	var percentUsed float64
	var isExhausted bool

	if quotaLimit > 0 {
		remaining = quotaLimit - totalUsed
		if remaining < 0 {
			remaining = 0
		}
		percentUsed = (float64(totalUsed) / float64(quotaLimit)) * 100.0
		if percentUsed > 100.0 {
			percentUsed = 100.0
		}
		isExhausted = totalUsed >= quotaLimit
	}

	return &models.UsageCycle{
		Username:        cardCode,
		CycleStart:      cycleStart,
		QuotaLimitBytes: quotaLimit,
		UsedInputBytes:  totalIn,
		UsedOutputBytes: totalOut,
		UsedBytes:       totalUsed,
		RemainingBytes:  remaining,
		PercentUsed:     percentUsed,
		IsExhausted:     isExhausted,
	}, nil
}

// ReconcileStaleSessions marks pending disconnects that never received Accounting-Stop,
// or sessions with no interim updates for extended period, as closed.
func (d *DB) ReconcileStaleSessions(maxPendingAge, maxInterimAge time.Duration) (int64, error) {
	if maxPendingAge <= 0 {
		maxPendingAge = 5 * time.Minute
	}
	if maxInterimAge <= 0 {
		maxInterimAge = 24 * time.Hour
	}

	cutoffPending := time.Now().Add(-maxPendingAge)
	cutoffInterim := time.Now().Add(-maxInterimAge)

	res1, err := d.Exec(`UPDATE radacct 
		SET acctstoptime = COALESCE(disconnect_pending_at, CURRENT_TIMESTAMP), 
		    acctterminatecause = 'Admin-Disconnect-Timeout',
		    disconnect_status = 'timeout'
		WHERE acctstoptime IS NULL 
		  AND disconnect_status = 'pending' 
		  AND disconnect_pending_at <= ?`, cutoffPending)
	if err != nil {
		return 0, err
	}
	c1, _ := res1.RowsAffected()

	res2, err := d.Exec(`UPDATE radacct 
		SET acctstoptime = COALESCE(acctupdatetime, acctstarttime, CURRENT_TIMESTAMP), 
		    acctterminatecause = 'Lost-Carrier-Stale'
		WHERE acctstoptime IS NULL 
		  AND (acctupdatetime <= ? OR (acctupdatetime IS NULL AND acctstarttime <= ?))`, cutoffInterim, cutoffInterim)
	if err != nil {
		return c1, err
	}
	c2, _ := res2.RowsAffected()

	return c1 + c2, nil
}

func (d *DB) DisconnectSubscriberSession(username string) (int, int, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, 0, fmt.Errorf("اسم المستخدم مطلوب")
	}

	rows, err := d.Query(`SELECT acctsessionid, nasipaddress, framedipaddress
		FROM radacct WHERE username = ? AND acctstoptime IS NULL`, username)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	type sessInfo struct {
		sessionID string
		nasIP     string
		framedIP  string
	}
	var sessions []sessInfo
	for rows.Next() {
		var s sessInfo
		var fIP sql.NullString
		if errScan := rows.Scan(&s.sessionID, &s.nasIP, &fIP); errScan == nil {
			if fIP.Valid {
				s.framedIP = fIP.String
			}
			sessions = append(sessions, s)
		}
	}

	if len(sessions) == 0 {
		return 0, 0, fmt.Errorf("المشترك غير متصل أونلاين حالياً")
	}

	successCount := 0
	failCount := 0

	for _, s := range sessions {
		var secret string
		var coaPort int
		_ = d.QueryRow("SELECT secret, coa_port FROM nas WHERE nasname = ?", s.nasIP).Scan(&secret, &coaPort)
		if secret == "" {
			failCount++
			continue
		}
		if coaPort <= 0 {
			coaPort = 3799
		}

		var framedIP net.IP
		if s.framedIP != "" {
			framedIP = net.ParseIP(s.framedIP)
		}

		res, err := radius.SendDisconnectRequest(s.nasIP, coaPort, []byte(secret), username, s.sessionID, framedIP)
		if err == nil && res != nil && res.Success {
			successCount++
			// Mark as pending disconnect without prematurely setting acctstoptime
			_, _ = d.Exec(`UPDATE radacct SET disconnect_status = 'pending', disconnect_pending_at = CURRENT_TIMESTAMP
				WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`, s.sessionID, s.nasIP)
		} else {
			failCount++
			log.Printf("[Disconnect] Failed to disconnect session %s on NAS %s: %v", s.sessionID, s.nasIP, err)
		}
	}

	subTrafficMu.Lock()
	delete(subTrafficHistory, username)
	subTrafficMu.Unlock()

	return successCount, failCount, nil
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
		return fmt.Errorf("الراوتر %s غير مسجل أو لا يحتوي على secret صالح", nasIP.String)
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
		return fmt.Errorf("NAS %s returned CoA-NAK for %s: %s", nasIP.String, username, res.Message)
	} else if res != nil {
		log.Printf("[CoA] Successfully applied CoA for %s on NAS %s (Rate: %s, List: %s, Pool: %s)", username, nasIP.String, opts.RateLimit, opts.AddressList, opts.PoolName)
	}
	return nil
}

// SendSubscriberRateLimitCoA sends a rate limit CoA request to the subscriber's NAS
func (d *DB) SendSubscriberRateLimitCoA(username string, rateLimit string) (int, int, error) {
	err := d.SendSubscriberCoA(username, radius.CoAOptions{
		RateLimit: rateLimit,
	})
	if err != nil {
		return 0, 1, err
	}
	return 1, 0, nil
}

// RestoreActiveSubscriberCoA restores an active subscriber's speed and normal profile settings via CoA
func (d *DB) RestoreActiveSubscriberCoA(username string) error {
	var profileName string
	err := d.QueryRow("SELECT profile_name FROM subscribers WHERE username = ?", username).Scan(&profileName)
	if err != nil {
		return err
	}

	// Remove reject lockout and fallback attributes
	_, _ = d.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Auth-Type'", username)
	_, _ = d.Exec("DELETE FROM radreply WHERE username = ? AND attribute IN ('Mikrotik-Rate-Limit', 'Mikrotik-Address-List', 'Framed-Pool')", username)

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
		_, _, _ = d.DisconnectSubscriberSession(username)
		_, _ = d.Exec("UPDATE subscribers SET status = 'expired_quota' WHERE username = ?", username)
		_, _ = d.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Auth-Type'", username)
		_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Auth-Type', ':=', 'Reject')", username)
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
		_, _, _ = d.DisconnectSubscriberSession(username)
		_, _ = d.Exec("UPDATE subscribers SET status = 'expired_time' WHERE username = ?", username)
		_, _ = d.Exec("DELETE FROM radcheck WHERE username = ? AND attribute = 'Auth-Type'", username)
		_, _ = d.Exec("INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Auth-Type', ':=', 'Reject')", username)
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
			continue // Skip unregistered NAS without secret
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
			continue // Skip unregistered NAS without secret
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
			_, _, _ = d.DisconnectSubscriberSession(u)
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

		// B. Check Quota Expiration via unified UsageCycle
		isQuotaExpired := false
		if s.quotaBytes > 0 {
			if uc, err := d.GetSubscriberUsageCycle(s.username); err == nil && uc != nil {
				isQuotaExpired = uc.IsExhausted
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

func (d *DB) GetSubscriberLoginSessions(search, status, nasIP, fromDate, toDate string, resellerID int64, limit, offset int) ([]models.SubscriberLoginSession, int, error) {
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

	// 1. Total Count (cached when unfiltered for instant response)
	var total int
	isUnfiltered := (search == "" && status == "" && nasIP == "" && fromDate == "" && toDate == "" && resellerID == 0)
	if isUnfiltered {
		totalLoginsMu.RLock()
		if cachedTotalLogins > 0 && time.Since(cachedTotalLoginsTime) < 30*time.Second {
			total = cachedTotalLogins
			totalLoginsMu.RUnlock()
		} else {
			totalLoginsMu.RUnlock()
			_ = d.QueryRow("SELECT COUNT(*) FROM radacct").Scan(&total)
			totalLoginsMu.Lock()
			cachedTotalLogins = total
			cachedTotalLoginsTime = time.Now()
			totalLoginsMu.Unlock()
		}
	} else {
		var countQuery string
		if search != "" || resellerID > 0 {
			countQuery = fmt.Sprintf("SELECT COUNT(*) FROM radacct r LEFT JOIN subscribers s ON r.username = s.username WHERE %s", whereSQL)
		} else {
			countQuery = fmt.Sprintf("SELECT COUNT(*) FROM radacct r WHERE %s", whereSQL)
		}
		_ = d.QueryRow(countQuery, args...).Scan(&total)
	}

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
		((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED)),
		((CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)),
		COALESCE(r.acctterminatecause, '')
	FROM radacct r
	LEFT JOIN subscribers s ON r.username = s.username
	LEFT JOIN nas n ON r.nasipaddress = n.nasname
	WHERE %s
	ORDER BY r.radacctid DESC
	LIMIT %d OFFSET %d`, whereSQL, limit, offset)

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
	isUnfiltered := (search == "" && status == "" && nasIP == "" && fromDate == "" && toDate == "")
	if isUnfiltered {
		loginSummaryMu.RLock()
		if cachedLoginSummary != nil && time.Since(cachedLoginSummaryTime) < 60*time.Second {
			cp := *cachedLoginSummary
			loginSummaryMu.RUnlock()
			return &cp, nil
		}
		loginSummaryMu.RUnlock()
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

	var query string
	if search != "" {
		query = fmt.Sprintf(`SELECT 
			COUNT(*),
			COUNT(DISTINCT r.username),
			COALESCE(SUM(CASE WHEN r.acctstoptime IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN r.acctstoptime IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM((CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)), 0),
			COALESCE(SUM((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED)), 0),
			COALESCE(AVG(r.acctsessiontime), 0)
		FROM radacct r
		LEFT JOIN subscribers s ON r.username = s.username
		WHERE %s`, whereSQL)
	} else {
		query = fmt.Sprintf(`SELECT 
			COUNT(*),
			COUNT(DISTINCT r.username),
			COALESCE(SUM(CASE WHEN r.acctstoptime IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN r.acctstoptime IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM((CAST(COALESCE(r.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctoutputoctets, 0) AS UNSIGNED)), 0),
			COALESCE(SUM((CAST(COALESCE(r.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(r.acctinputoctets, 0) AS UNSIGNED)), 0),
			COALESCE(AVG(r.acctsessiontime), 0)
		FROM radacct r
		WHERE %s`, whereSQL)
	}

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

	if isUnfiltered {
		loginSummaryMu.Lock()
		cachedLoginSummary = summary
		cachedLoginSummaryTime = time.Now()
		loginSummaryMu.Unlock()
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
		COALESCE(show_quota_usage, 1), COALESCE(show_debt, 1), COALESCE(show_balance, 1), COALESCE(allow_balance_renewal, 1),
		COALESCE(show_profile_details, 1),
		COALESCE(show_online_status, 1), COALESCE(allow_password_change, 0), COALESCE(allow_phone_login, 0),
		COALESCE(allow_ip_auto_login, 0), COALESCE(allow_session_disconnect, 1),
		COALESCE(company_name, 'شبكتنا لخدمات الإنترنت'), COALESCE(company_phone, ''), portal_announcement, updated_at
		FROM subscriber_portal_settings WHERE id = 1`).Scan(
		&s.ID, &s.PortalEnabled, &s.AllowLoanRequest,
		&s.ShowQuotaUsage, &s.ShowDebt, &s.ShowBalance, &s.AllowBalanceRenewal,
		&s.ShowProfileDetails,
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
				ShowBalance:            true,
				AllowBalanceRenewal:    true,
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
		id, portal_enabled, allow_loan_request, show_quota_usage, show_debt, show_balance, allow_balance_renewal,
		show_profile_details, show_online_status, allow_password_change, allow_phone_login, allow_ip_auto_login, allow_session_disconnect,
		company_name, company_phone, portal_announcement
	) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON DUPLICATE KEY UPDATE
		portal_enabled = VALUES(portal_enabled),
		allow_loan_request = VALUES(allow_loan_request),
		show_quota_usage = VALUES(show_quota_usage),
		show_debt = VALUES(show_debt),
		show_balance = VALUES(show_balance),
		allow_balance_renewal = VALUES(allow_balance_renewal),
		show_profile_details = VALUES(show_profile_details),
		show_online_status = VALUES(show_online_status),
		allow_password_change = VALUES(allow_password_change),
		allow_phone_login = VALUES(allow_phone_login),
		allow_ip_auto_login = VALUES(allow_ip_auto_login),
		allow_session_disconnect = VALUES(allow_session_disconnect),
		company_name = VALUES(company_name),
		company_phone = VALUES(company_phone),
		portal_announcement = VALUES(portal_announcement)`,
		s.PortalEnabled, s.AllowLoanRequest, s.ShowQuotaUsage, s.ShowDebt, s.ShowBalance, s.AllowBalanceRenewal,
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

// FindSubscriberForExpiredPortal finds subscriber by active framed IP, static IP, or most recent session IP
func (d *DB) FindSubscriberForExpiredPortal(ip string) (*models.Subscriber, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" || ip == "127.0.0.1" || ip == "::1" {
		return nil, sql.ErrNoRows
	}

	var username string
	// 1. Check active session in radacct
	err := d.QueryRow(`SELECT username FROM radacct 
		WHERE framedipaddress = ? AND acctstoptime IS NULL 
		ORDER BY radacctid DESC LIMIT 1`, ip).Scan(&username)
	if err == nil && username != "" {
		return d.GetSubscriber(username)
	}

	// 2. Check static IP in subscribers table
	err = d.QueryRow(`SELECT username FROM subscribers WHERE static_ip = ? LIMIT 1`, ip).Scan(&username)
	if err == nil && username != "" {
		return d.GetSubscriber(username)
	}

	// 3. Check most recent session from this IP (e.g. within past 48 hours)
	err = d.QueryRow(`SELECT username FROM radacct 
		WHERE framedipaddress = ? 
		ORDER BY radacctid DESC LIMIT 1`, ip).Scan(&username)
	if err == nil && username != "" {
		return d.GetSubscriber(username)
	}

	return nil, sql.ErrNoRows
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

	// 1. Quota & Consumption via unified UsageCycle
	if sub.QuotaBytes > 0 {
		view.QuotaGB = float64(sub.QuotaBytes) / (1024 * 1024 * 1024)
	} else if prof.QuotaBytes > 0 {
		view.QuotaGB = float64(prof.QuotaBytes) / (1024 * 1024 * 1024)
	}

	uc, err := d.GetSubscriberUsageCycle(username)
	if err == nil && uc != nil {
		view.UsedBytes = uc.UsedBytes
		view.UsedGB = float64(uc.UsedBytes) / (1024 * 1024 * 1024)
		if sub.QuotaBytes > 0 {
			view.RemainingBytes = uc.RemainingBytes
			view.RemainingGB = float64(uc.RemainingBytes) / (1024 * 1024 * 1024)
			view.UsagePercent = uc.PercentUsed
		} else {
			view.RemainingBytes = -1
			view.RemainingGB = -1
			view.UsagePercent = 0.0
		}
	} else {
		view.UsedBytes = 0
		view.UsedGB = 0
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
	_, _, _ = d.DisconnectSubscriberSession(username)
	return nil
}

// -----------------------------------------------------------------------------
// Subscriber Traffic Consumption Report (الاستهلاك الشهري واليومي)
// -----------------------------------------------------------------------------

var arabicMonthNamesFull = []string{
	"",
	"يناير (January)",
	"فبراير (February)",
	"مارس (March)",
	"أبريل (April)",
	"مايو (May)",
	"يونيو (June)",
	"يوليو (July)",
	"أغسطس (August)",
	"سبتمبر (September)",
	"أكتوبر (October)",
	"نوفمبر (November)",
	"ديسمبر (December)",
}

var arabicDayNames = map[time.Weekday]string{
	time.Sunday:    "الأحد",
	time.Monday:    "الاثنين",
	time.Tuesday:   "الثلاثاء",
	time.Wednesday: "الأربعاء",
	time.Thursday:  "الخميس",
	time.Friday:    "الجمعة",
	time.Saturday:  "السبت",
}

func (d *DB) GetSubscriberTrafficReport(username string, year int, month int) (*models.SubscriberTrafficReport, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("اسم المشترك مطلوب")
	}

	now := time.Now()
	if year < 2000 || year > now.Year()+2 {
		year = now.Year()
	}
	if month < 0 || month > 12 {
		month = 0 // 0 = Full Year Mode
	}

	monthName := "تقرير سنة كاملة (كافة شهور السنة)"
	if month >= 1 && month <= 12 {
		monthName = arabicMonthNamesFull[month]
	}

	report := &models.SubscriberTrafficReport{
		Username:          username,
		FullName:          username,
		SelectedYear:      year,
		SelectedMonth:     month,
		SelectedMonthName: monthName,
	}

	// 1. Fetch Subscriber Details
	_ = d.QueryRow(`SELECT 
		COALESCE(s.full_name, s.username),
		COALESCE(s.contract_no, ''),
		COALESCE(s.phone, ''),
		COALESCE(s.profile_name, ''),
		COALESCE(p.display_name, s.profile_name),
		COALESCE(r.name, 'المركز الرئيسي')
	FROM subscribers s
	LEFT JOIN profiles p ON s.profile_name = p.name
	LEFT JOIN resellers r ON s.reseller_id = r.id
	WHERE s.username = ?`, username).Scan(
		&report.FullName, &report.ContractNo, &report.Phone,
		&report.ProfileName, &report.ProfileDisplay, &report.ResellerName,
	)

	// 2. Online status check
	var onlineIP string
	errOnline := d.QueryRow("SELECT framedipaddress FROM radacct WHERE username = ? AND acctstoptime IS NULL ORDER BY radacctid DESC LIMIT 1", username).Scan(&onlineIP)
	if errOnline == nil {
		report.IsOnline = true
		report.OnlineIP = onlineIP
	}

	// 3. Available Years for this Subscriber (ensure comprehensive full years list)
	yearsMap := make(map[int]bool)
	currYear := now.Year()
	// Always include future year down to 2018
	for y := currYear + 1; y >= 2018; y-- {
		yearsMap[y] = true
	}
	yearsMap[year] = true

	// Also merge any other years from radacct for this subscriber
	yearRows, err := d.Query("SELECT DISTINCT YEAR(acctstarttime) as yr FROM radacct WHERE username = ? AND acctstarttime IS NOT NULL ORDER BY yr DESC", username)
	if err == nil {
		defer yearRows.Close()
		for yearRows.Next() {
			var yr int
			if err := yearRows.Scan(&yr); err == nil && yr > 2000 {
				yearsMap[yr] = true
			}
		}
	}

	var sortedYears []int
	for yr := range yearsMap {
		sortedYears = append(sortedYears, yr)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedYears)))
	report.AvailableYears = sortedYears

	// 4. Monthly Usage for Selected Year (Direct range on idx_radacct_user_start)
	type monthData struct {
		upBytes   int64
		downBytes int64
		totBytes  int64
		sessions  int
		duration  int64
	}
	monthMap := make(map[int]monthData)

	startOfYear := fmt.Sprintf("%04d-01-01 00:00:00", year)
	endOfYear := fmt.Sprintf("%04d-01-01 00:00:00", year+1)

	mRows, err := d.Query(fmt.Sprintf(`SELECT 
		MONTH(acctstarttime) as m,
		COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0),
		%s,
		COUNT(*),
		COALESCE(SUM(acctsessiontime), 0)
	FROM radacct
	WHERE username = ? AND acctstarttime >= ? AND acctstarttime < ?
	GROUP BY MONTH(acctstarttime)`, RadacctSumBytesSQL), username, startOfYear, endOfYear)

	if err == nil {
		defer mRows.Close()
		for mRows.Next() {
			var m int
			var md monthData
			if err := mRows.Scan(&m, &md.upBytes, &md.downBytes, &md.totBytes, &md.sessions, &md.duration); err == nil {
				monthMap[m] = md
			}
		}
	}

	// Find max monthly bytes for relative percentage visualization
	var maxMonthBytes int64
	var peakMonthNum int
	for m := 1; m <= 12; m++ {
		if md, ok := monthMap[m]; ok {
			report.YearTotalBytes += md.totBytes
			report.YearTotalSessions += md.sessions
			if md.totBytes > maxMonthBytes {
				maxMonthBytes = md.totBytes
				peakMonthNum = m
			}
		}
	}
	if maxMonthBytes > 0 {
		report.YearPeakMonth = arabicMonthNamesFull[peakMonthNum]
		report.YearPeakMonthGB = float64(maxMonthBytes) / (1024 * 1024 * 1024)
	}

	var yearTotalUp int64
	var yearTotalDown int64
	var yearTotalDurationSec int64

	// 5. Query daily usage for ALL months in the year in one fast query (Direct range on idx_radacct_user_start)
	type dayData struct {
		upBytes   int64
		downBytes int64
		totBytes  int64
		sessions  int
		duration  int64
	}
	yearDaysMap := make(map[int]map[int]dayData) // month -> day -> data

	dRows, err := d.Query(fmt.Sprintf(`SELECT 
		MONTH(acctstarttime) as m,
		DAY(acctstarttime) as d,
		COALESCE(SUM((CAST(COALESCE(acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctinputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM((CAST(COALESCE(acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(acctoutputoctets, 0) AS UNSIGNED)), 0),
		%s,
		COUNT(*),
		COALESCE(SUM(acctsessiontime), 0)
	FROM radacct
	WHERE username = ? AND acctstarttime >= ? AND acctstarttime < ?
	GROUP BY MONTH(acctstarttime), DAY(acctstarttime)`, RadacctSumBytesSQL), username, startOfYear, endOfYear)

	if err == nil {
		defer dRows.Close()
		for dRows.Next() {
			var m, d int
			var dd dayData
			if err := dRows.Scan(&m, &d, &dd.upBytes, &dd.downBytes, &dd.totBytes, &dd.sessions, &dd.duration); err == nil {
				if yearDaysMap[m] == nil {
					yearDaysMap[m] = make(map[int]dayData)
				}
				yearDaysMap[m][d] = dd
			}
		}
	}

	// Build 12-month array with attached daily lists
	for m := 1; m <= 12; m++ {
		md := monthMap[m]
		upGB := float64(md.upBytes) / (1024 * 1024 * 1024)
		downGB := float64(md.downBytes) / (1024 * 1024 * 1024)
		totGB := float64(md.totBytes) / (1024 * 1024 * 1024)

		yearTotalUp += md.upBytes
		yearTotalDown += md.downBytes
		yearTotalDurationSec += md.duration

		var pctPeak float64
		if maxMonthBytes > 0 {
			pctPeak = (float64(md.totBytes) / float64(maxMonthBytes)) * 100.0
		}

		var pctYear float64
		if report.YearTotalBytes > 0 {
			pctYear = (float64(md.totBytes) / float64(report.YearTotalBytes)) * 100.0
		}

		monthItem := models.SubscriberMonthlyTraffic{
			Year:            year,
			Month:           m,
			MonthName:       fmt.Sprintf("%02d", m),
			MonthNameAr:     arabicMonthNamesFull[m],
			UploadBytes:     md.upBytes,
			DownloadBytes:   md.downBytes,
			TotalBytes:      md.totBytes,
			UploadGB:        upGB,
			DownloadGB:      downGB,
			TotalGB:         totGB,
			TotalDisplay:    FormatBytesHuman(md.totBytes),
			UploadDisplay:   FormatBytesHuman(md.upBytes),
			DownloadDisplay: FormatBytesHuman(md.downBytes),
			SessionCount:    md.sessions,
			TotalTimeSec:    md.duration,
			DurationString:  FormatDurationArabic(md.duration),
			PercentOfPeak:   pctPeak,
			PercentOfYear:   pctYear,
		}

		// Calculate days of month m
		daysInMonth := time.Date(year, time.Month(m+1), 0, 0, 0, 0, 0, time.Local).Day()
		var maxDayBytesInMonth int64
		monthDaysMap := yearDaysMap[m]

		if monthDaysMap != nil {
			for d := 1; d <= daysInMonth; d++ {
				if dd, ok := monthDaysMap[d]; ok {
					if dd.totBytes > maxDayBytesInMonth {
						maxDayBytesInMonth = dd.totBytes
					}
				}
			}
		}

		for d := 1; d <= daysInMonth; d++ {
			var dd dayData
			if monthDaysMap != nil {
				dd = monthDaysMap[d]
			}
			dateObj := time.Date(year, time.Month(m), d, 0, 0, 0, 0, time.Local)
			dayNameAr := arabicDayNames[dateObj.Weekday()]
			dateStr := dateObj.Format("2006-01-02")

			dUpGB := float64(dd.upBytes) / (1024 * 1024 * 1024)
			dDownGB := float64(dd.downBytes) / (1024 * 1024 * 1024)
			dTotGB := float64(dd.totBytes) / (1024 * 1024 * 1024)

			var dPct float64
			if maxDayBytesInMonth > 0 {
				dPct = (float64(dd.totBytes) / float64(maxDayBytesInMonth)) * 100.0
			}

			dayItem := models.SubscriberDailyTraffic{
				Date:            dateStr,
				DayNum:          d,
				DayNameAr:       dayNameAr,
				UploadBytes:     dd.upBytes,
				DownloadBytes:   dd.downBytes,
				TotalBytes:      dd.totBytes,
				UploadGB:        dUpGB,
				DownloadGB:      dDownGB,
				TotalGB:         dTotGB,
				TotalDisplay:    FormatBytesHuman(dd.totBytes),
				UploadDisplay:   FormatBytesHuman(dd.upBytes),
				DownloadDisplay: FormatBytesHuman(dd.downBytes),
				SessionCount:    dd.sessions,
				TotalTimeSec:    dd.duration,
				DurationString:  FormatDurationArabic(dd.duration),
				PercentOfPeak:   dPct,
			}
			monthItem.DailyList = append(monthItem.DailyList, dayItem)
		}

		report.MonthlyList = append(report.MonthlyList, monthItem)
	}

	report.YearTotalGB = float64(report.YearTotalBytes) / (1024 * 1024 * 1024)
	report.YearTotalUploadGB = float64(yearTotalUp) / (1024 * 1024 * 1024)
	report.YearTotalDownGB = float64(yearTotalDown) / (1024 * 1024 * 1024)
	report.YearTotalDuration = FormatDurationArabic(yearTotalDurationSec)

	// If a specific month is selected (1..12), bind its daily list and stats
	if month >= 1 && month <= 12 {
		selectedMonthItem := report.MonthlyList[month-1]
		report.DailyList = selectedMonthItem.DailyList
		report.MonthTotalBytes = selectedMonthItem.TotalBytes
		report.MonthTotalGB = selectedMonthItem.TotalGB
		report.MonthTotalUpload = selectedMonthItem.UploadGB
		report.MonthTotalDown = selectedMonthItem.DownloadGB
		report.MonthSessions = selectedMonthItem.SessionCount
		report.MonthDuration = selectedMonthItem.DurationString

		daysInMonth := len(selectedMonthItem.DailyList)
		if daysInMonth > 0 {
			report.MonthDailyAvgGB = report.MonthTotalGB / float64(daysInMonth)
		}

		var maxDayBytes int64
		var peakDayNum int
		for _, d := range selectedMonthItem.DailyList {
			if d.TotalBytes > maxDayBytes {
				maxDayBytes = d.TotalBytes
				peakDayNum = d.DayNum
			}
		}
		if maxDayBytes > 0 {
			report.MonthPeakDay = fmt.Sprintf("%02d/%02d/%d", peakDayNum, month, year)
			report.MonthPeakDayGB = float64(maxDayBytes) / (1024 * 1024 * 1024)
		}
	} else {
		// Full Year Mode (month == 0):
		report.MonthTotalBytes = report.YearTotalBytes
		report.MonthTotalGB = report.YearTotalGB
		report.MonthTotalUpload = report.YearTotalUploadGB
		report.MonthTotalDown = report.YearTotalDownGB
		report.MonthSessions = report.YearTotalSessions
		report.MonthDuration = report.YearTotalDuration
		report.MonthPeakDay = report.YearPeakMonth
		report.MonthPeakDayGB = report.YearPeakMonthGB
		report.MonthDailyAvgGB = report.YearTotalGB / 365.0
	}

	return report, nil
}

func (d *DB) GetTopConsumers(year, month int, limit int) ([]models.TopConsumerItem, error) {
	if limit <= 0 {
		limit = 10
	}
	now := time.Now()
	if year < 2000 {
		year = now.Year()
	}
	if month < 1 || month > 12 {
		month = int(now.Month())
	}

	cacheKey := fmt.Sprintf("%d-%d-%d", year, month, limit)
	topConsumersMu.RLock()
	if cached, ok := cachedTopConsumers[cacheKey]; ok {
		if t, okT := cachedTopConsumersTime[cacheKey]; okT && time.Since(t) < 3*time.Minute {
			topConsumersMu.RUnlock()
			return cached, nil
		}
	}
	topConsumersMu.RUnlock()

	startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	endOfMonth := startOfMonth.AddDate(0, 1, 0)

	query := `SELECT 
		ra.username,
		COALESCE(s.full_name, ra.username),
		COALESCE(s.contract_no, ''),
		COALESCE(s.profile_name, ''),
		COALESCE(r.name, 'المركز الرئيسي'),
		COALESCE(SUM((CAST(COALESCE(ra.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(ra.acctinputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM((CAST(COALESCE(ra.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(ra.acctoutputoctets, 0) AS UNSIGNED)), 0),
		COALESCE(SUM((CAST(COALESCE(ra.acctinputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(ra.acctinputoctets, 0) AS UNSIGNED) + (CAST(COALESCE(ra.acctoutputgigawords, 0) AS UNSIGNED) * 4294967296) + CAST(COALESCE(ra.acctoutputoctets, 0) AS UNSIGNED)), 0) as tot_b,
		COUNT(*)
	FROM radacct ra
	LEFT JOIN subscribers s ON ra.username = s.username
	LEFT JOIN resellers r ON s.reseller_id = r.id
	WHERE ra.acctstarttime >= ? AND ra.acctstarttime < ?
	GROUP BY ra.username, s.full_name, s.contract_no, s.profile_name, r.name
	ORDER BY tot_b DESC
	LIMIT ?`

	rows, err := d.Query(query, startOfMonth, endOfMonth, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.TopConsumerItem
	for rows.Next() {
		var item models.TopConsumerItem
		var inB, outB, totB int64
		if err := rows.Scan(
			&item.Username, &item.FullName, &item.ContractNo,
			&item.ProfileName, &item.ResellerName,
			&inB, &outB, &totB, &item.SessionCount,
		); err == nil {
			item.TotalBytes = totB
			item.TotalGB = float64(totB) / (1024 * 1024 * 1024)
			item.TotalDisplay = FormatBytesHuman(totB)
			item.UploadGB = float64(inB) / (1024 * 1024 * 1024)
			item.DownloadGB = float64(outB) / (1024 * 1024 * 1024)
			list = append(list, item)
		}
	}

	topConsumersMu.Lock()
	cachedTopConsumers[cacheKey] = list
	cachedTopConsumersTime[cacheKey] = time.Now()
	topConsumersMu.Unlock()

	return list, nil
}

func (d *DB) SearchSubscribersForTraffic(query string, limit int) ([]models.Subscriber, error) {
	if limit <= 0 {
		limit = 15
	}
	q := "%" + query + "%"
	rows, err := d.Query(`SELECT s.id, s.username, s.full_name, COALESCE(s.contract_no, ''), COALESCE(s.phone, ''), COALESCE(s.profile_name, '')
		FROM subscribers s
		WHERE s.username LIKE ? OR s.full_name LIKE ? OR s.contract_no LIKE ? OR s.phone LIKE ?
		ORDER BY s.id DESC LIMIT ?`, q, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Subscriber
	for rows.Next() {
		var s models.Subscriber
		if err := rows.Scan(&s.ID, &s.Username, &s.FullName, &s.ContractNo, &s.Phone, &s.ProfileName); err == nil {
			list = append(list, s)
		}
	}

	return list, nil
}
