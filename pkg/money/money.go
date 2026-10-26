package money

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	ErrInvalidCommission = errors.New("commission percent must be between 0 and 100")
	ErrNegativeAmount    = errors.New("amount cannot be negative")
	ErrInsufficientFunds = errors.New("insufficient balance and credit limit")
	ErrNonFinite         = errors.New("number must be a finite valid number (not NaN or Infinity)")
	ErrNonPositive       = errors.New("value must be greater than zero")
	ErrQuotaTooLarge     = errors.New("quota exceeds the supported maximum")
)

const bytesPerGB = 1024 * 1024 * 1024

// ValidateFinite checks that a float64 is not NaN or Infinity
func ValidateFinite(val float64) error {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return ErrNonFinite
	}
	return nil
}

// ValidatePositive checks that a float64 is finite and strictly positive (> 0)
func ValidatePositive(val float64) error {
	if err := ValidateFinite(val); err != nil {
		return err
	}
	if val <= 0 {
		return ErrNonPositive
	}
	return nil
}

// ValidateNonNegative checks that a float64 is finite and non-negative (>= 0)
func ValidateNonNegative(val float64) error {
	if err := ValidateFinite(val); err != nil {
		return err
	}
	if val < 0 {
		return ErrNegativeAmount
	}
	return nil
}

// ParseFiniteFloat parses a string into float64 and validates that it is finite
func ParseFiniteFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric format: %w", err)
	}
	if err := ValidateFinite(val); err != nil {
		return 0, err
	}
	return val, nil
}

// QuotaGBToBytes converts a quota to bytes without allowing float overflow.
func QuotaGBToBytes(gb float64) (int64, error) {
	if err := ValidatePositive(gb); err != nil {
		return 0, err
	}
	if gb > float64(math.MaxInt64)/bytesPerGB {
		return 0, ErrQuotaTooLarge
	}
	bytes := math.Round(gb * bytesPerGB)
	if bytes <= 0 || bytes > float64(math.MaxInt64) {
		return 0, ErrQuotaTooLarge
	}
	return int64(bytes), nil
}

// ToCents converts a float64 amount (e.g. 10.50 EGP) to integer minor units / cents (1050)
// using standard midpoint rounding to avoid IEEE-754 precision drift.
func ToCents(amount float64) int64 {
	if amount >= 0 {
		return int64(math.Round(amount * 100.0))
	}
	return -int64(math.Round(-amount * 100.0))
}

// FromCents converts integer minor units / cents (1050) to float64 (10.50) for display and API boundaries.
func FromCents(cents int64) float64 {
	return float64(cents) / 100.0
}

// FormatEGP formats cents as currency string with 2 decimal places (e.g. "10.50 ج.م")
func FormatEGP(cents int64) string {
	return fmt.Sprintf("%.2f ج.م", FromCents(cents))
}

// ValidateCommission checks that commission percent is between 0 and 100 inclusive.
func ValidateCommission(commissionPercent float64) error {
	if commissionPercent < 0 || commissionPercent > 100 || math.IsNaN(commissionPercent) || math.IsInf(commissionPercent, 0) {
		return ErrInvalidCommission
	}
	return nil
}

// CalculateResellerCost computes the authoritative reseller cost and commission in cents.
// commissionPercent must be between 0 and 100.
// Returns (costCents, commissionCents, error).
func CalculateResellerCost(priceCents int64, commissionPercent float64, fallbackCostPriceCents int64) (int64, int64, error) {
	if priceCents < 0 {
		return 0, 0, ErrNegativeAmount
	}
	if err := ValidateCommission(commissionPercent); err != nil {
		return 0, 0, err
	}

	if commissionPercent > 0 {
		// Basis points (1% = 100 bps, 100% = 10000 bps)
		bps := int64(math.Round(commissionPercent * 100.0))
		commissionCents := (priceCents * bps) / 10000
		costCents := priceCents - commissionCents
		if costCents < 0 {
			costCents = 0
		}
		return costCents, commissionCents, nil
	}

	if fallbackCostPriceCents > 0 && fallbackCostPriceCents <= priceCents {
		costCents := fallbackCostPriceCents
		commissionCents := priceCents - costCents
		return costCents, commissionCents, nil
	}

	return priceCents, 0, nil
}

// AvailableCapacityCents computes the authoritative credit capacity for a reseller:
// available_capacity = balance + credit_limit - current_debt
func AvailableCapacityCents(balanceCents, creditLimitCents, debtCents int64) int64 {
	if creditLimitCents < 0 {
		creditLimitCents = 0
	}
	if debtCents < 0 {
		debtCents = 0
	}
	capacity := balanceCents + creditLimitCents - debtCents
	return capacity
}

// DeductResellerFunds performs the standard two-tier balance and debt deduction:
// 1. Deduct as much as possible from balance.
// 2. Any remainder is added to debt (up to credit limit).
// Returns (newBalanceCents, newDebtCents, error).
func DeductResellerFunds(balanceCents, debtCents, creditLimitCents, costCents int64) (int64, int64, error) {
	if costCents < 0 {
		return balanceCents, debtCents, ErrNegativeAmount
	}
	if costCents == 0 {
		return balanceCents, debtCents, nil
	}

	avail := AvailableCapacityCents(balanceCents, creditLimitCents, debtCents)
	if avail < costCents {
		return balanceCents, debtCents, fmt.Errorf("%w (المطلوب: %s، المتاح: %s)", ErrInsufficientFunds, FormatEGP(costCents), FormatEGP(avail))
	}

	var newBal, newDebt int64
	if balanceCents >= costCents {
		newBal = balanceCents - costCents
		newDebt = debtCents
	} else {
		paidFromBal := balanceCents
		if paidFromBal < 0 {
			paidFromBal = 0
		}
		deficit := costCents - paidFromBal
		newBal = 0
		newDebt = debtCents + deficit
	}

	if newDebt > creditLimitCents {
		return balanceCents, debtCents, fmt.Errorf("تجاوز الحد الائتماني للموزع (%s > %s)", FormatEGP(newDebt), FormatEGP(creditLimitCents))
	}
	if newDebt < 0 {
		newDebt = 0
	}
	if newBal < 0 {
		newBal = 0
	}

	return newBal, newDebt, nil
}
