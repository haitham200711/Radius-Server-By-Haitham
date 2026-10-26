package money

import (
	"errors"
	"math"
	"testing"
)

func TestToCentsAndFromCents(t *testing.T) {
	tests := []struct {
		val   float64
		cents int64
	}{
		{0.0, 0},
		{10.50, 1050},
		{10.504, 1050},
		{10.506, 1051},
		{100.99, 10099},
		{-5.25, -525},
		{-5.256, -526},
	}

	for _, tc := range tests {
		gotCents := ToCents(tc.val)
		if gotCents != tc.cents {
			t.Errorf("ToCents(%f) = %d, expected %d", tc.val, gotCents, tc.cents)
		}
		if tc.val >= 0 && math.Abs(tc.val-FromCents(gotCents)) > 0.01 {
			t.Errorf("FromCents(%d) = %f, expected around %f", gotCents, FromCents(gotCents), tc.val)
		}
	}
}

func TestValidateCommission(t *testing.T) {
	if err := ValidateCommission(0); err != nil {
		t.Errorf("ValidateCommission(0) unexpected err: %v", err)
	}
	if err := ValidateCommission(50); err != nil {
		t.Errorf("ValidateCommission(50) unexpected err: %v", err)
	}
	if err := ValidateCommission(100); err != nil {
		t.Errorf("ValidateCommission(100) unexpected err: %v", err)
	}
	if err := ValidateCommission(-0.01); !errors.Is(err, ErrInvalidCommission) {
		t.Errorf("ValidateCommission(-0.01) expected ErrInvalidCommission, got %v", err)
	}
	if err := ValidateCommission(100.01); !errors.Is(err, ErrInvalidCommission) {
		t.Errorf("ValidateCommission(100.01) expected ErrInvalidCommission, got %v", err)
	}
	if err := ValidateCommission(math.NaN()); !errors.Is(err, ErrInvalidCommission) {
		t.Errorf("ValidateCommission(NaN) expected ErrInvalidCommission, got %v", err)
	}
}

func TestCalculateResellerCost(t *testing.T) {
	// 100 EGP = 10000 cents, 15% commission -> cost = 8500 cents (85 EGP), commission = 1500 cents (15 EGP)
	cost, comm, err := CalculateResellerCost(10000, 15.0, 0)
	if err != nil {
		t.Fatalf("CalculateResellerCost failed: %v", err)
	}
	if cost != 8500 || comm != 1500 {
		t.Errorf("Expected cost 8500, comm 1500; got cost %d, comm %d", cost, comm)
	}

	// 0% commission with fallbackCostPriceCents = 9000
	cost, comm, err = CalculateResellerCost(10000, 0, 9000)
	if err != nil {
		t.Fatalf("CalculateResellerCost failed: %v", err)
	}
	if cost != 9000 || comm != 1000 {
		t.Errorf("Expected cost 9000, comm 1000; got cost %d, comm %d", cost, comm)
	}

	// 0% commission with no fallback -> cost = price, comm = 0
	cost, comm, err = CalculateResellerCost(10000, 0, 0)
	if err != nil {
		t.Fatalf("CalculateResellerCost failed: %v", err)
	}
	if cost != 10000 || comm != 0 {
		t.Errorf("Expected cost 10000, comm 0; got cost %d, comm %d", cost, comm)
	}

	// Invalid commission
	_, _, err = CalculateResellerCost(10000, 105.0, 0)
	if !errors.Is(err, ErrInvalidCommission) {
		t.Errorf("Expected ErrInvalidCommission, got %v", err)
	}

	// Negative price
	_, _, err = CalculateResellerCost(-100, 10.0, 0)
	if !errors.Is(err, ErrNegativeAmount) {
		t.Errorf("Expected ErrNegativeAmount, got %v", err)
	}
}

func TestAvailableCapacityCents(t *testing.T) {
	// Balance: 50, Limit: 100, Debt: 20 -> capacity = 50 + 100 - 20 = 130
	cap := AvailableCapacityCents(5000, 10000, 2000)
	if cap != 13000 {
		t.Errorf("Expected 13000, got %d", cap)
	}

	// Balance: 0, Limit: 100, Debt: 100 -> capacity = 0
	cap = AvailableCapacityCents(0, 10000, 10000)
	if cap != 0 {
		t.Errorf("Expected 0, got %d", cap)
	}

	// Balance: 0, Limit: 100, Debt: 120 -> capacity = -20 (over credit limit)
	cap = AvailableCapacityCents(0, 10000, 12000)
	if cap != -2000 {
		t.Errorf("Expected -2000, got %d", cap)
	}
}

func TestDeductResellerFunds(t *testing.T) {
	// 1. Balance covers cost completely
	// Bal: 100 EGP, Debt: 0, Limit: 50 EGP, Cost: 40 EGP
	bal, debt, err := DeductResellerFunds(10000, 0, 5000, 4000)
	if err != nil {
		t.Fatalf("DeductResellerFunds failed: %v", err)
	}
	if bal != 6000 || debt != 0 {
		t.Errorf("Expected bal 6000, debt 0; got bal %d, debt %d", bal, debt)
	}

	// 2. Balance partially covers cost, debt increases within credit limit
	// Bal: 30 EGP, Debt: 10 EGP, Limit: 50 EGP, Cost: 50 EGP
	// Capacity: 30 + 50 - 10 = 70 EGP. Cost is 50 EGP (feasible).
	// Paid from bal: 30 EGP -> bal = 0. Deficit: 20 EGP -> debt becomes 10 + 20 = 30 EGP <= 50 EGP limit.
	bal, debt, err = DeductResellerFunds(3000, 1000, 5000, 5000)
	if err != nil {
		t.Fatalf("DeductResellerFunds failed: %v", err)
	}
	if bal != 0 || debt != 3000 {
		t.Errorf("Expected bal 0, debt 3000; got bal %d, debt %d", bal, debt)
	}

	// 3. Exceeds capacity
	// Bal: 10 EGP, Debt: 40 EGP, Limit: 50 EGP (capacity = 10 + 50 - 40 = 20 EGP). Cost: 30 EGP -> fails!
	_, _, err = DeductResellerFunds(1000, 4000, 5000, 3000)
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("Expected ErrInsufficientFunds, got %v", err)
	}

	// 4. Zero cost
	bal, debt, err = DeductResellerFunds(1000, 500, 2000, 0)
	if err != nil || bal != 1000 || debt != 500 {
		t.Errorf("Zero cost should return unchanged, got bal %d, debt %d, err %v", bal, debt, err)
	}

	// 5. Negative cost
	_, _, err = DeductResellerFunds(1000, 500, 2000, -100)
	if !errors.Is(err, ErrNegativeAmount) {
		t.Errorf("Expected ErrNegativeAmount, got %v", err)
	}
}
