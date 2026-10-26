package database

import "fmt"

// ClearReportHistory removes historical report rows while preserving subscribers,
// balances, configuration, active RADIUS sessions, and active loans.
func (d *DB) ClearReportHistory() error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("failed to start report history reset: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`DELETE FROM billing_transactions`,
		`DELETE FROM reseller_transactions`,
		`DELETE FROM financial_ledger`,
		`DELETE FROM audit_logs`,
		`DELETE FROM radacct WHERE acctstoptime IS NOT NULL`,
		`DELETE FROM loan_transactions WHERE status IN ('settled', 'cancelled')`,
		`DELETE FROM financial_outbox WHERE status IN ('completed', 'failed', 'dead_letter')`,
		`DELETE FROM idempotency_keys WHERE status = 'completed' OR expires_at < NOW()`,
		`DELETE FROM whatsapp_logs`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to clear report history: %w; SQL: %s", err, stmt)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit report history reset: %w", err)
	}
	return nil
}
