package database

import (
	"database/sql"
	"fmt"
	"math"
)

// FinancialDiscrepancy represents an inconsistency found during reconciliation
type FinancialDiscrepancy struct {
	Entity         string  `json:"entity"`         // 'subscriber' or 'reseller' or 'billing'
	Identifier     string  `json:"identifier"`     // username or reseller ID
	RecordedValue  float64 `json:"recorded_value"` // current value in table
	CalculatedSum  float64 `json:"calculated_sum"` // sum of movements in financial_ledger
	Difference     float64 `json:"difference"`
	DiscrepancyMsg string  `json:"discrepancy_msg"`
}

// FinancialReconciliationReport summarizes the findings of a financial ledger reconciliation run
type FinancialReconciliationReport struct {
	SubscribersChecked int                    `json:"subscribers_checked"`
	ResellersChecked   int                    `json:"resellers_checked"`
	LedgerTotalEntries int64                  `json:"ledger_total_entries"`
	TotalLedgerCents   int64                  `json:"total_ledger_cents"`
	IsBalanced         bool                   `json:"is_balanced"`
	Discrepancies      []FinancialDiscrepancy `json:"discrepancies,omitempty"`
}

// ReconcileFinancials executes a full audit comparing current balances and debts against the ledger
func (d *DB) ReconcileFinancials() (*FinancialReconciliationReport, error) {
	report := &FinancialReconciliationReport{
		IsBalanced:    true,
		Discrepancies: make([]FinancialDiscrepancy, 0),
	}

	// 1. Total ledger entries and sum
	err := d.QueryRow(`SELECT COUNT(*), COALESCE(SUM(amount_cents), 0) FROM financial_ledger`).Scan(
		&report.LedgerTotalEntries, &report.TotalLedgerCents,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate financial ledger: %w", err)
	}

	// 2. Check Resellers: compare balance and debt against latest ledger entry or movement
	resRows, err := d.Query(`SELECT id, name, COALESCE(balance, 0.00), COALESCE(debt, 0.00) FROM resellers`)
	if err != nil {
		return nil, fmt.Errorf("failed to query resellers for reconciliation: %w", err)
	}
	defer resRows.Close()

	for resRows.Next() {
		report.ResellersChecked++
		var resID int64
		var name string
		var curBal, curDebt float64
		if err := resRows.Scan(&resID, &name, &curBal, &curDebt); err != nil {
			continue
		}

		// Check if latest ledger entry for this reseller matches current balance/debt
		var lastBalAfter, lastDebtAfter sql.NullFloat64
		err := d.QueryRow(`SELECT balance_after, debt_after 
			FROM financial_ledger 
			WHERE account_type = 'reseller' AND reseller_id = ? 
			ORDER BY id DESC LIMIT 1`, resID).Scan(&lastBalAfter, &lastDebtAfter)

		if err == nil && lastBalAfter.Valid && lastDebtAfter.Valid {
			balDiff := math.Abs(curBal - lastBalAfter.Float64)
			debtDiff := math.Abs(curDebt - lastDebtAfter.Float64)
			if balDiff > 0.01 {
				report.IsBalanced = false
				report.Discrepancies = append(report.Discrepancies, FinancialDiscrepancy{
					Entity:         "reseller",
					Identifier:     fmt.Sprintf("%s (ID: %d)", name, resID),
					RecordedValue:  curBal,
					CalculatedSum:  lastBalAfter.Float64,
					Difference:     curBal - lastBalAfter.Float64,
					DiscrepancyMsg: fmt.Sprintf("رصيد الموزع المسجل (%.2f) يختلف عن آخر رصيد في سجل الأستاذ (%.2f)", curBal, lastBalAfter.Float64),
				})
			}
			if debtDiff > 0.01 {
				report.IsBalanced = false
				report.Discrepancies = append(report.Discrepancies, FinancialDiscrepancy{
					Entity:         "reseller",
					Identifier:     fmt.Sprintf("%s (ID: %d)", name, resID),
					RecordedValue:  curDebt,
					CalculatedSum:  lastDebtAfter.Float64,
					Difference:     curDebt - lastDebtAfter.Float64,
					DiscrepancyMsg: fmt.Sprintf("دين الموزع المسجل (%.2f) يختلف عن آخر دين في سجل الأستاذ (%.2f)", curDebt, lastDebtAfter.Float64),
				})
			}
		}
	}
	resRows.Close()

	// 3. Check Subscribers: compare latest ledger entry balance/debt against current subscriber balance/debt
	subRows, err := d.Query(`SELECT id, username, COALESCE(balance, 0.00), COALESCE(debt, 0.00) FROM subscribers`)
	if err != nil {
		return nil, fmt.Errorf("failed to query subscribers for reconciliation: %w", err)
	}
	defer subRows.Close()

	for subRows.Next() {
		report.SubscribersChecked++
		var subID int64
		var username string
		var curBal, curDebt float64
		if err := subRows.Scan(&subID, &username, &curBal, &curDebt); err != nil {
			continue
		}

		var lastBalAfter, lastDebtAfter sql.NullFloat64
		err := d.QueryRow(`SELECT balance_after, debt_after 
			FROM financial_ledger 
			WHERE account_type = 'subscriber' AND subscriber_username = ? 
			ORDER BY id DESC LIMIT 1`, username).Scan(&lastBalAfter, &lastDebtAfter)

		if err == nil && lastBalAfter.Valid && lastDebtAfter.Valid {
			balDiff := math.Abs(curBal - lastBalAfter.Float64)
			debtDiff := math.Abs(curDebt - lastDebtAfter.Float64)
			if balDiff > 0.01 {
				report.IsBalanced = false
				report.Discrepancies = append(report.Discrepancies, FinancialDiscrepancy{
					Entity:         "subscriber",
					Identifier:     username,
					RecordedValue:  curBal,
					CalculatedSum:  lastBalAfter.Float64,
					Difference:     curBal - lastBalAfter.Float64,
					DiscrepancyMsg: fmt.Sprintf("رصيد المشترك المسجل (%.2f) يختلف عن آخر رصيد في سجل الأستاذ (%.2f)", curBal, lastBalAfter.Float64),
				})
			}
			if debtDiff > 0.01 {
				report.IsBalanced = false
				report.Discrepancies = append(report.Discrepancies, FinancialDiscrepancy{
					Entity:         "subscriber",
					Identifier:     username,
					RecordedValue:  curDebt,
					CalculatedSum:  lastDebtAfter.Float64,
					Difference:     curDebt - lastDebtAfter.Float64,
					DiscrepancyMsg: fmt.Sprintf("دين المشترك المسجل (%.2f) يختلف عن آخر دين في سجل الأستاذ (%.2f)", curDebt, lastDebtAfter.Float64),
				})
			}
		}
	}
	subRows.Close()

	return report, nil
}
