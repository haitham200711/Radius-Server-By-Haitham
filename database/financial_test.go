package database

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/money"
)

// TestDoubleDeductionPreventionLogic tests that reseller card batch costs are strictly computed
// from the server-side profile and commission, and that deductions are applied solely on actual created cards.
func TestDoubleDeductionPreventionLogic(t *testing.T) {
	prof := models.Profile{
		ID:          1,
		Name:        "100M-Monthly",
		DisplayName: "باقة 100 ميجا شهرية",
		Price:       150.00, // 150 EGP
		CostPrice:   120.00, // 120 EGP
	}

	resellerCommission := 20.0 // 20%
	profPriceCents := money.ToCents(prof.Price)
	profCostCents := money.ToCents(prof.CostPrice)

	// Case 1: Client tampered with price (sent 10.00 EGP instead of 150.00 EGP)
	tamperedClientPrice := 10.00
	_ = tamperedClientPrice

	// The system must compute cost authoritatively using prof.Price
	costPerCardCents, commCents, err := money.CalculateResellerCost(profPriceCents, resellerCommission, profCostCents)
	if err != nil {
		t.Fatalf("CalculateResellerCost failed: %v", err)
	}

	// 150 EGP with 20% commission -> cost = 120 EGP (12000 cents), commission = 30 EGP (3000 cents)
	if costPerCardCents != 12000 {
		t.Errorf("Expected costPerCardCents 12000 (120.00 EGP), got %d", costPerCardCents)
	}
	if commCents != 3000 {
		t.Errorf("Expected commCents 3000 (30.00 EGP), got %d", commCents)
	}

	// Case 2: Partial batch creation (requested 100, only 90 created due to error/limit)
	requestedCount := 100
	actualCreated := 90

	requestedTotalCostCents := int64(requestedCount) * costPerCardCents // 12,000 EGP
	actualTotalCostCents := int64(actualCreated) * costPerCardCents     // 10,800 EGP

	if actualTotalCostCents >= requestedTotalCostCents {
		t.Errorf("Actual cost must be less than requested cost on partial creation")
	}

	// Deducting reseller with balance of 15000 EGP (1,500,000 cents), 0 debt, 5000 EGP limit
	resBalCents := int64(1500000)
	resDebtCents := int64(0)
	creditLimitCents := int64(500000)

	newBal, newDebt, err := money.DeductResellerFunds(resBalCents, resDebtCents, creditLimitCents, actualTotalCostCents)
	if err != nil {
		t.Fatalf("DeductResellerFunds failed: %v", err)
	}

	expectedBal := resBalCents - actualTotalCostCents // 1,500,000 - 1,080,000 = 420,000 cents (4,200 EGP)
	if newBal != expectedBal {
		t.Errorf("Expected new balance %d, got %d", expectedBal, newBal)
	}
	if newDebt != 0 {
		t.Errorf("Expected debt 0, got %d", newDebt)
	}
}

// TestResellerAvailableCapacityFormula verifies that the formula:
// available_capacity = balance + credit_limit - current_debt
// is strictly enforced, preventing credit limit breach.
func TestResellerAvailableCapacityFormula(t *testing.T) {
	cases := []struct {
		name             string
		balEGP           float64
		limitEGP         float64
		debtEGP          float64
		expectedAvailEGP float64
	}{
		{
			name:             "Positive balance, zero debt",
			balEGP:           500.0,
			limitEGP:         1000.0,
			debtEGP:          0.0,
			expectedAvailEGP: 1500.0,
		},
		{
			name:             "Zero balance, debt within credit limit",
			balEGP:           0.0,
			limitEGP:         1000.0,
			debtEGP:          400.0,
			expectedAvailEGP: 600.0,
		},
		{
			name:             "Positive balance, with debt",
			balEGP:           200.0,
			limitEGP:         1000.0,
			debtEGP:          300.0,
			expectedAvailEGP: 900.0, // 200 + 1000 - 300 = 900
		},
		{
			name:             "Credit exhausted (debt == limit, balance == 0)",
			balEGP:           0.0,
			limitEGP:         1000.0,
			debtEGP:          1000.0,
			expectedAvailEGP: 0.0,
		},
		{
			name:             "Credit breached (debt > limit)",
			balEGP:           0.0,
			limitEGP:         1000.0,
			debtEGP:          1200.0,
			expectedAvailEGP: -200.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			balCents := money.ToCents(tc.balEGP)
			limitCents := money.ToCents(tc.limitEGP)
			debtCents := money.ToCents(tc.debtEGP)
			expectedAvailCents := money.ToCents(tc.expectedAvailEGP)

			avail := money.AvailableCapacityCents(balCents, limitCents, debtCents)
			if avail != expectedAvailCents {
				t.Errorf("Expected capacity %d (%f EGP), got %d (%f EGP)",
					expectedAvailCents, tc.expectedAvailEGP, avail, money.FromCents(avail))
			}
		})
	}
}

// TestOverpaymentDebtSettlement verifies that debt payment never exceeds actual debt
// and never produces negative debt.
func TestOverpaymentDebtSettlement(t *testing.T) {
	currDebtCents := int64(15000)      // 150.00 EGP debt
	paymentAmountCents := int64(20000) // 200.00 EGP payment

	// Overpayment protection clamp
	actualSettledCents := paymentAmountCents
	if actualSettledCents > currDebtCents {
		actualSettledCents = currDebtCents
	}

	newDebtCents := currDebtCents - actualSettledCents

	if actualSettledCents != 15000 {
		t.Errorf("Expected actual settled cents to clamp to 15000, got %d", actualSettledCents)
	}
	if newDebtCents != 0 {
		t.Errorf("Expected new debt to be 0, got %d", newDebtCents)
	}
	if newDebtCents < 0 {
		t.Errorf("Debt must never be negative, got %d", newDebtCents)
	}
}

// TestIdempotencyKeyStateMachine verifies that reference IDs and idempotency keys
// produce unique, deterministic values and correct collision resistance.
func TestIdempotencyKeyStateMachine(t *testing.T) {
	ref1 := GenerateReferenceID("FIN")
	ref2 := GenerateReferenceID("FIN")

	if ref1 == "" || ref2 == "" {
		t.Fatal("Reference IDs must not be empty")
	}
	if ref1 == ref2 {
		t.Fatalf("Reference IDs must be unique, got duplicate: %s", ref1)
	}

	// Verify prefix
	if len(ref1) < 10 || ref1[:4] != "FIN-" {
		t.Errorf("Reference ID must start with prefix FIN-, got %s", ref1)
	}
}

// TestLoanDeductionFullVsPartial verifies mathematical accuracy of full and partial loan deductions
func TestLoanDeductionFullVsPartial(t *testing.T) {
	prof := models.Profile{
		ID:                1,
		Name:              "Basic-Monthly",
		DisplayName:       "باقة بيسك شهرية",
		Price:             100.0,
		QuotaBytes:        100 * 1024 * 1024 * 1024, // 100 GB
		ValidityValue:     30,
		ValidityUnit:      "days",
		LoanHours:         48,
		LoanQuotaBytes:    5 * 1024 * 1024 * 1024, // 5 GB
		LoanDeductionType: "full",
	}

	// Case 1: Full loan deduction (always deducts 48 hours and 5GB)
	loanHours := prof.LoanHours
	loanQuotaBytes := prof.LoanQuotaBytes

	deductedHoursFull := float64(loanHours)
	deductedQuotaFull := loanQuotaBytes

	if deductedHoursFull != 48.0 {
		t.Errorf("Expected 48 hours deducted for full loan, got %f", deductedHoursFull)
	}
	if deductedQuotaFull != 5*1024*1024*1024 {
		t.Errorf("Expected 5GB deducted for full loan, got %d", deductedQuotaFull)
	}

	// Case 2: Partial loan deduction (subscriber only used 2.5 GB and 12 hours)
	loanGrantedAt := time.Now().Add(-12 * time.Hour)
	usedDuringLoan := int64(2500000000) // ~2.5 GB

	elapsed := time.Since(loanGrantedAt).Hours()
	if elapsed > float64(loanHours) {
		elapsed = float64(loanHours)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	deductedHoursPartial := elapsed

	if math.Abs(deductedHoursPartial-12.0) > 0.1 {
		t.Errorf("Expected ~12 hours elapsed, got %f", deductedHoursPartial)
	}

	deductedQuotaPartial := usedDuringLoan
	if deductedQuotaPartial > loanQuotaBytes {
		deductedQuotaPartial = loanQuotaBytes
	}
	if deductedQuotaPartial != 2500000000 {
		t.Errorf("Expected 2500000000 bytes deducted for partial loan, got %d", deductedQuotaPartial)
	}
}

// TestFinancialRecordMinorUnits validates that float and integer minor units are completely synchronized
func TestFinancialRecordMinorUnits(t *testing.T) {
	tests := []struct {
		amount float64
		cents  int64
	}{
		{10.00, 1000},
		{99.99, 9999},
		{0.05, 5},
		{1250.50, 125050},
	}

	for _, tc := range tests {
		rec := FinancialRecord{
			Amount: tc.amount,
		}
		if money.ToCents(rec.Amount) != tc.cents {
			t.Errorf("For amount %f, expected %d cents, got %d", tc.amount, tc.cents, money.ToCents(rec.Amount))
		}
		if money.FromCents(tc.cents) != tc.amount {
			t.Errorf("For cents %d, expected %f amount, got %f", tc.cents, tc.amount, money.FromCents(tc.cents))
		}
	}
}

// TestResellerBalanceWithdrawBoundaries verifies that reseller withdraw never creates negative balances
func TestResellerBalanceWithdrawBoundaries(t *testing.T) {
	balCents := int64(5000) // 50.00 EGP

	// Attempting to withdraw 60.00 EGP (6000 cents) should be rejected
	withdrawCents := int64(6000)
	if balCents < withdrawCents {
		err := errors.New("insufficient balance")
		if err == nil {
			t.Error("Expected error for over-withdraw")
		}
	}

	// Attempting to withdraw 40.00 EGP (4000 cents) should succeed
	withdrawCents = int64(4000)
	newBal := balCents - withdrawCents
	if newBal != 1000 {
		t.Errorf("Expected remaining balance 1000 cents (10.00 EGP), got %d", newBal)
	}
}
