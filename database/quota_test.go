package database

import (
	"math"
	"testing"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/models"
)

// CalculateRowBytesGo mimics the exact SQL formula of RadacctRowBytesSQL in Go
func CalculateRowBytesGo(inGW, inOct, outGW, outOct uint64) uint64 {
	inputTotal := (inGW * 4294967296) + inOct
	outputTotal := (outGW * 4294967296) + outOct
	return inputTotal + outputTotal
}

func TestGigawordsCalculation(t *testing.T) {
	// Test normal traffic under 4GB (gigawords = 0)
	var inGW, outGW uint64 = 0, 0
	var inOct uint64 = 1500000000  // ~1.5 GB
	var outOct uint64 = 2500000000 // ~2.5 GB

	total := CalculateRowBytesGo(inGW, inOct, outGW, outOct)
	expected := uint64(4000000000)
	if total != expected {
		t.Errorf("Expected %d bytes, got %d", expected, total)
	}

	// Test high-traffic ISP subscriber exceeding 4GB (rollover to gigawords)
	inGW = 2 // 2 * 4GB = 8589934592 bytes
	inOct = 500000000
	outGW = 3 // 3 * 4GB = 12884901888 bytes
	outOct = 1000000000

	expectedIn := (uint64(2) * 4294967296) + 500000000
	expectedOut := (uint64(3) * 4294967296) + 1000000000
	expectedTotal := expectedIn + expectedOut

	total = CalculateRowBytesGo(inGW, inOct, outGW, outOct)
	if total != expectedTotal {
		t.Errorf("Gigawords calculation failed: expected %d, got %d", expectedTotal, total)
	}
}

func TestQuotaUnlimited(t *testing.T) {
	// QuotaBytes = 0 signifies unlimited quota
	quotaLimit := int64(0)
	usedBytes := int64(50000000000) // 50 GB

	var remaining int64
	var percentUsed float64
	var isExhausted bool

	if quotaLimit > 0 {
		remaining = quotaLimit - usedBytes
		if remaining < 0 {
			remaining = 0
		}
		percentUsed = (float64(usedBytes) / float64(quotaLimit)) * 100.0
		isExhausted = usedBytes >= quotaLimit
	} else {
		remaining = 0
		percentUsed = 0.0
		isExhausted = false
	}

	if isExhausted {
		t.Errorf("Unlimited quota (0) should never be exhausted")
	}
	if percentUsed != 0.0 {
		t.Errorf("Unlimited quota percentage should be 0.0, got %f", percentUsed)
	}
	if remaining != 0 {
		t.Errorf("Remaining for unlimited quota should be 0, got %d", remaining)
	}
}

func TestQuotaExhaustionAndRemaining(t *testing.T) {
	quotaLimit := int64(10 * 1024 * 1024 * 1024) // 10 GB

	cases := []struct {
		name           string
		usedBytes      int64
		expectedRem    int64
		expectedExh    bool
		expectedPctMin float64
		expectedPctMax float64
	}{
		{
			name:           "Half consumed (5GB)",
			usedBytes:      5 * 1024 * 1024 * 1024,
			expectedRem:    5 * 1024 * 1024 * 1024,
			expectedExh:    false,
			expectedPctMin: 49.9,
			expectedPctMax: 50.1,
		},
		{
			name:           "Exactly exhausted (10GB)",
			usedBytes:      10 * 1024 * 1024 * 1024,
			expectedRem:    0,
			expectedExh:    true,
			expectedPctMin: 99.9,
			expectedPctMax: 100.0,
		},
		{
			name:           "Over consumed (12GB)",
			usedBytes:      12 * 1024 * 1024 * 1024,
			expectedRem:    0,
			expectedExh:    true,
			expectedPctMin: 100.0,
			expectedPctMax: 100.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rem := quotaLimit - tc.usedBytes
			if rem < 0 {
				rem = 0
			}
			pct := math.Min(100.0, (float64(tc.usedBytes)/float64(quotaLimit))*100.0)
			isExh := tc.usedBytes >= quotaLimit

			if rem != tc.expectedRem {
				t.Errorf("Remaining: expected %d, got %d", tc.expectedRem, rem)
			}
			if isExh != tc.expectedExh {
				t.Errorf("IsExhausted: expected %v, got %v", tc.expectedExh, isExh)
			}
			if pct < tc.expectedPctMin || pct > tc.expectedPctMax {
				t.Errorf("Percentage: expected between %f and %f, got %f", tc.expectedPctMin, tc.expectedPctMax, pct)
			}
		})
	}
}

func TestSessionBaselineDeductionAcrossRenewal(t *testing.T) {
	// Scenario: A session started at 10:00.
	// At 11:00, user consumed 4 GB (Input: 1.5 GB, Output: 2.5 GB).
	// User renews at 11:00.
	// Baseline captured: baseline_in = 1.5GB, baseline_out = 2.5GB.
	// At 12:00, session reports total consumption: Input: 2.5 GB, Output: 4.5 GB (7 GB total).
	// New cycle consumption should be:
	// delta_in = 2.5GB - 1.5GB = 1.0 GB
	// delta_out = 4.5GB - 2.5GB = 2.0 GB
	// cycle_total = 3.0 GB!

	var baseIn uint64 = 1500000000
	var baseOut uint64 = 2500000000

	var currIn uint64 = 2500000000
	var currOut uint64 = 4500000000

	deltaIn := int64(currIn) - int64(baseIn)
	if deltaIn < 0 {
		deltaIn = 0
	}
	deltaOut := int64(currOut) - int64(baseOut)
	if deltaOut < 0 {
		deltaOut = 0
	}

	expectedDeltaIn := int64(1000000000)
	expectedDeltaOut := int64(2000000000)
	expectedCycleTotal := int64(3000000000)

	if deltaIn != expectedDeltaIn {
		t.Errorf("Expected deltaIn %d, got %d", expectedDeltaIn, deltaIn)
	}
	if deltaOut != expectedDeltaOut {
		t.Errorf("Expected deltaOut %d, got %d", expectedDeltaOut, deltaOut)
	}
	if (deltaIn + deltaOut) != expectedCycleTotal {
		t.Errorf("Expected cycle total %d, got %d", expectedCycleTotal, deltaIn+deltaOut)
	}
}

func TestUsageCycleModel(t *testing.T) {
	now := time.Now()
	renewalTime := now.Add(-2 * time.Hour)

	uc := &models.UsageCycle{
		Username:         "subscriber_test_1",
		CycleStart:       renewalTime,
		RenewalTimestamp: &renewalTime,
		QuotaLimitBytes:  50 * 1024 * 1024 * 1024, // 50 GB
		UsedInputBytes:   10 * 1024 * 1024 * 1024,
		UsedOutputBytes:  20 * 1024 * 1024 * 1024,
		UsedBytes:        30 * 1024 * 1024 * 1024, // 30 GB
		RemainingBytes:   20 * 1024 * 1024 * 1024, // 20 GB
		PercentUsed:      60.0,
		IsExhausted:      false,
	}

	if uc.UsedBytes != (uc.UsedInputBytes + uc.UsedOutputBytes) {
		t.Errorf("UsedBytes mismatch: %d != %d", uc.UsedBytes, uc.UsedInputBytes+uc.UsedOutputBytes)
	}
	if uc.RemainingBytes != (uc.QuotaLimitBytes - uc.UsedBytes) {
		t.Errorf("RemainingBytes mismatch: %d != %d", uc.RemainingBytes, uc.QuotaLimitBytes-uc.UsedBytes)
	}
	if uc.IsExhausted {
		t.Errorf("Should not be exhausted at 60%% usage")
	}
}
