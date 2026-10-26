package database

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// Migration represents a versioned database migration
type Migration struct {
	Version     string
	Description string
	SQL         []string
	Func        func(tx *sql.Tx) error
}

// GetRegisteredMigrations returns the ordered list of all schema migrations
func GetRegisteredMigrations() []Migration {
	return []Migration{
		{
			Version:     "20261026_01_financial_ledger_and_outbox",
			Description: "Create financial ledger, idempotency keys, financial outbox, loan transactions, and audit logs",
			SQL: []string{
				`CREATE TABLE IF NOT EXISTS financial_ledger (
					id BIGINT AUTO_INCREMENT PRIMARY KEY,
					reference_id VARCHAR(64) UNIQUE NOT NULL,
					idempotency_key VARCHAR(128) UNIQUE NULL,
					operation_type VARCHAR(64) NOT NULL,
					account_type VARCHAR(32) NOT NULL,
					subscriber_id INT DEFAULT 0,
					subscriber_username VARCHAR(64) DEFAULT '',
					reseller_id INT DEFAULT 0,
					amount_cents BIGINT NOT NULL DEFAULT 0,
					amount DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
					balance_before DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
					balance_after DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
					debt_before DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
					debt_after DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
					status VARCHAR(32) NOT NULL DEFAULT 'completed',
					description VARCHAR(255) NOT NULL DEFAULT '',
					metadata TEXT NULL,
					created_by VARCHAR(64) NOT NULL DEFAULT 'system',
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					INDEX idx_fledger_ref (reference_id),
					INDEX idx_fledger_reseller (reseller_id),
					INDEX idx_fledger_sub (subscriber_id),
					INDEX idx_fledger_user (subscriber_username),
					INDEX idx_fledger_op (operation_type),
					INDEX idx_fledger_date (created_at)
				) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
				`CREATE TABLE IF NOT EXISTS idempotency_keys (
					id BIGINT AUTO_INCREMENT PRIMARY KEY,
					idempotency_key VARCHAR(128) UNIQUE NOT NULL,
					operation_type VARCHAR(64) NOT NULL,
					resource_id VARCHAR(64) DEFAULT '',
					request_hash VARCHAR(64) DEFAULT '',
					response_code INT DEFAULT 200,
					response_body MEDIUMTEXT,
					status VARCHAR(32) NOT NULL DEFAULT 'in_progress',
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					expires_at DATETIME NOT NULL,
					INDEX idx_idemp_key (idempotency_key),
					INDEX idx_idemp_exp (expires_at)
				) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
				`CREATE TABLE IF NOT EXISTS financial_outbox (
					id BIGINT AUTO_INCREMENT PRIMARY KEY,
					event_type VARCHAR(64) NOT NULL,
					aggregate_type VARCHAR(64) NOT NULL,
					aggregate_id VARCHAR(64) NOT NULL,
					payload MEDIUMTEXT NOT NULL,
					status VARCHAR(32) NOT NULL DEFAULT 'pending',
					retry_count INT NOT NULL DEFAULT 0,
					max_retries INT NOT NULL DEFAULT 5,
					lease_token VARCHAR(64) NULL,
					lease_expires_at DATETIME NULL,
					last_error TEXT NULL,
					scheduled_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					processed_at DATETIME NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					INDEX idx_outbox_sched (status, scheduled_at),
					INDEX idx_outbox_lease (status, lease_expires_at)
				) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
				`CREATE TABLE IF NOT EXISTS loan_transactions (
					id BIGINT AUTO_INCREMENT PRIMARY KEY,
					loan_id VARCHAR(64) UNIQUE NOT NULL,
					reference_id VARCHAR(64) NOT NULL DEFAULT '',
					subscriber_id INT NOT NULL,
					username VARCHAR(64) NOT NULL,
					commercial_value DECIMAL(10, 2) NOT NULL DEFAULT 0.00,
					granted_hours INT NOT NULL DEFAULT 0,
					granted_quota_bytes BIGINT NOT NULL DEFAULT 0,
					deduction_type VARCHAR(20) NOT NULL DEFAULT 'full',
					initial_used_bytes BIGINT NOT NULL DEFAULT 0,
					used_bytes BIGINT NOT NULL DEFAULT 0,
					deducted_hours INT NOT NULL DEFAULT 0,
					deducted_quota_bytes BIGINT NOT NULL DEFAULT 0,
					deducted_amount DECIMAL(10, 2) NOT NULL DEFAULT 0.00,
					remaining_amount DECIMAL(10, 2) NOT NULL DEFAULT 0.00,
					status VARCHAR(32) NOT NULL DEFAULT 'active',
					granted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					settled_at DATETIME NULL,
					INDEX idx_loan_sub (subscriber_id),
					INDEX idx_loan_user (username),
					INDEX idx_loan_status (status)
				) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
				`CREATE TABLE IF NOT EXISTS audit_logs (
					id BIGINT AUTO_INCREMENT PRIMARY KEY,
					reference_id VARCHAR(64) NOT NULL DEFAULT '',
					actor_id INT NOT NULL DEFAULT 0,
					actor_username VARCHAR(64) NOT NULL DEFAULT '',
					actor_role VARCHAR(32) NOT NULL DEFAULT '',
					action VARCHAR(64) NOT NULL,
					target_type VARCHAR(64) NOT NULL,
					target_id VARCHAR(64) NOT NULL,
					details TEXT,
					ip_address VARCHAR(45) NOT NULL DEFAULT '',
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					INDEX idx_audit_ref (reference_id),
					INDEX idx_audit_actor (actor_username),
					INDEX idx_audit_target (target_type, target_id),
					INDEX idx_audit_date (created_at)
				) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
			},
		},
		{
			Version:     "20261026_02_outbox_lease_and_dedup",
			Description: "Add lease fields and unique indexes for outbox concurrency and idempotency",
			SQL: []string{
				`ALTER TABLE financial_outbox ADD COLUMN IF NOT EXISTS lease_token VARCHAR(64) NULL;`,
				`ALTER TABLE financial_outbox ADD COLUMN IF NOT EXISTS lease_expires_at DATETIME NULL;`,
				`ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS request_hash VARCHAR(64) DEFAULT '';`,
				`ALTER TABLE loan_transactions ADD COLUMN IF NOT EXISTS used_bytes BIGINT NOT NULL DEFAULT 0;`,
				`ALTER TABLE loan_transactions ADD COLUMN IF NOT EXISTS deducted_amount DECIMAL(10, 2) NOT NULL DEFAULT 0.00;`,
				`ALTER TABLE loan_transactions ADD COLUMN IF NOT EXISTS remaining_amount DECIMAL(10, 2) NOT NULL DEFAULT 0.00;`,
			},
		},
		{
			Version:     "20261026_03_radacct_integrity_and_dedup",
			Description: "Ensure radacct gigawords, normalization, and session deduplication indexes",
			SQL: []string{
				`ALTER TABLE radacct MODIFY COLUMN nasipaddress VARCHAR(45) NOT NULL DEFAULT '';`,
				`ALTER TABLE radacct MODIFY COLUMN framedipaddress VARCHAR(45) NOT NULL DEFAULT '';`,
				`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS acctinputgigawords INT UNSIGNED DEFAULT 0;`,
				`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS acctoutputgigawords INT UNSIGNED DEFAULT 0;`,
				`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS normalized_input_bytes BIGINT UNSIGNED DEFAULT 0;`,
				`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS normalized_output_bytes BIGINT UNSIGNED DEFAULT 0;`,
				`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS disconnect_status VARCHAR(32) DEFAULT '';`,
				`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS disconnect_pending_at DATETIME NULL;`,
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
			},
		},
		{
			Version:     "20261026_04_subscriber_financial_fields",
			Description: "Ensure debt, balance, and loan fields exist on subscribers and resellers with proper precision",
			SQL: []string{
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS debt DECIMAL(10, 2) DEFAULT 0.00;`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS balance DECIMAL(10, 2) DEFAULT 0.00;`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS last_renewed_at DATETIME NULL;`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_active TINYINT(1) DEFAULT 0;`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_granted_at DATETIME NULL;`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_hours INT DEFAULT 0;`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_quota_bytes BIGINT DEFAULT 0;`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_deduction_type VARCHAR(20) DEFAULT 'full';`,
				`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS loan_initial_used_bytes BIGINT DEFAULT 0;`,
				`ALTER TABLE resellers ADD COLUMN IF NOT EXISTS debt DECIMAL(12, 2) DEFAULT 0.00;`,
				`ALTER TABLE resellers ADD COLUMN IF NOT EXISTS credit_limit DECIMAL(12, 2) DEFAULT 0.00;`,
				`ALTER TABLE resellers ADD COLUMN IF NOT EXISTS commission_percent DECIMAL(5, 2) DEFAULT 0.00;`,
				`ALTER TABLE profiles ADD COLUMN IF NOT EXISTS cost_price DECIMAL(10, 2) DEFAULT 0.00;`,
			},
		},
			{
				Version:     "20261026_05_radacct_active_dedup",
				Description: "Deduplicate active accounting sessions and enforce one active row per session and NAS",
				SQL: []string{
					`ALTER TABLE financial_ledger MODIFY COLUMN idempotency_key VARCHAR(128) NULL;`,
					// Keep the newest active row when old imports contain duplicate starts.
					`DELETE r1 FROM radacct r1 JOIN radacct r2
					 WHERE r1.acctstoptime IS NULL AND r2.acctstoptime IS NULL
					   AND r1.acctsessionid = r2.acctsessionid
					   AND r1.nasipaddress = r2.nasipaddress
					   AND r1.radacctid < r2.radacctid;`,
					`ALTER TABLE radacct ADD COLUMN IF NOT EXISTS active_session_key VARCHAR(160)
					 GENERATED ALWAYS AS (CASE WHEN acctstoptime IS NULL
					 THEN CONCAT(acctsessionid, '|', nasipaddress, '|ACTIVE')
					 ELSE CONCAT(acctsessionid, '|', nasipaddress, '|', radacctid) END) STORED;`,
					`ALTER TABLE radacct ADD UNIQUE KEY IF NOT EXISTS uq_radacct_active_session (active_session_key);`,
				},
			},
	}
}

// RunMigrations executes all unapplied schema migrations in strict chronological order.
// If any migration fails, it aborts immediately and returns a descriptive error.
func (d *DB) RunMigrations() error {
	// 1. Ensure schema_migrations table exists
	_, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(128) PRIMARY KEY,
		description VARCHAR(255) NOT NULL DEFAULT '',
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	migrations := GetRegisteredMigrations()

	for _, m := range migrations {
		var existingVersion string
		err := d.QueryRow("SELECT version FROM schema_migrations WHERE version = ?", m.Version).Scan(&existingVersion)
		if err == nil {
			// Migration already applied
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("failed to check migration status for %s: %w", m.Version, err)
		}

		// Apply migration inside a dedicated transaction
		log.Printf("[MIGRATION] Applying %s: %s...", m.Version, m.Description)
		start := time.Now()

		tx, err := d.Begin()
		if err != nil {
			return fmt.Errorf("failed to start transaction for migration %s: %w", m.Version, err)
		}

		for _, stmt := range m.SQL {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s failed executing statement: %w\nSQL: %s", m.Version, err, stmt)
			}
		}

		if m.Func != nil {
			if err := m.Func(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s failed executing migration function: %w", m.Version, err)
			}
		}

		// Record migration as applied
		_, err = tx.Exec("INSERT INTO schema_migrations (version, description, applied_at) VALUES (?, ?, NOW())",
			m.Version, m.Description)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %s in schema_migrations: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %s: %w", m.Version, err)
		}

		log.Printf("[MIGRATION] Successfully applied %s in %v", m.Version, time.Since(start))
	}

	return nil
}
