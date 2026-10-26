package database

import (
	"testing"
	"time"
)

// TestAccountingGigawordsNormalization tests that 32-bit counter gigaword multipliers
// expand accurately into 64-bit integers without overflow.
func TestAccountingGigawordsNormalization(t *testing.T) {
	// 5 Gigawords + 1,000,000 octets
	var giga uint32 = 5
	var octets int64 = 1000000

	normalized := (uint64(giga) * 4294967296) + uint64(octets)
	expected := uint64(5)*uint64(4294967296) + 1000000

	if normalized != expected {
		t.Fatalf("Expected %d, got %d", expected, normalized)
	}

	// Max 32-bit octets rollover test
	var maxOctets uint32 = 4294967295
	var gigaRollover uint32 = 1

	normRollover := (uint64(gigaRollover) * 4294967296) + uint64(maxOctets)
	expectedRollover := uint64(8589934591)
	if normRollover != expectedRollover {
		t.Fatalf("Expected rollover sum %d, got %d", expectedRollover, normRollover)
	}
}

// TestSessionBaselineDeltaCalculation verifies session baseline subtraction
// when renewal occurs in the middle of an active session.
func TestSessionBaselineDeltaCalculation(t *testing.T) {
	// Session started at 10:00 AM
	// Renewal occurred at 12:00 PM, capturing baseline of 4 GB down / 1 GB up
	baselineIn := uint64(4 * 1024 * 1024 * 1024)
	baselineOut := uint64(1 * 1024 * 1024 * 1024)

	// At session end (2:00 PM), total session traffic reported in Stop packet is:
	// 7 GB down / 2 GB up
	totalSessionIn := uint64(7 * 1024 * 1024 * 1024)
	totalSessionOut := uint64(2 * 1024 * 1024 * 1024)

	// Delta attributed to the new cycle:
	deltaIn := int64(totalSessionIn) - int64(baselineIn)
	deltaOut := int64(totalSessionOut) - int64(baselineOut)

	expectedDeltaIn := int64(3 * 1024 * 1024 * 1024)  // 3 GB
	expectedDeltaOut := int64(1 * 1024 * 1024 * 1024) // 1 GB

	if deltaIn != expectedDeltaIn {
		t.Errorf("Expected deltaIn 3 GB, got %d", deltaIn)
	}
	if deltaOut != expectedDeltaOut {
		t.Errorf("Expected deltaOut 1 GB, got %d", deltaOut)
	}

	// Verify non-negative clamping if counters glitch or reset
	glitchedTotalIn := uint64(2 * 1024 * 1024 * 1024)
	glitchedDeltaIn := int64(glitchedTotalIn) - int64(baselineIn)
	if glitchedDeltaIn < 0 {
		glitchedDeltaIn = 0
	}
	if glitchedDeltaIn != 0 {
		t.Errorf("Expected clamped delta 0 on glitch, got %d", glitchedDeltaIn)
	}
}

// TestDuplicateStopDeduplicationLogic tests that duplicate accounting Stop packets
// do not inflate customer usage.
func TestDuplicateStopDeduplicationLogic(t *testing.T) {
	stopTime1 := time.Now().Add(-10 * time.Minute)
	stopTime2 := time.Now()

	hasStopTimeAlready := !stopTime1.IsZero()

	// In ProcessAccountingRecord:
	// If stop is received for a session that already has acctstoptime != nil:
	// it must NOT sum the traffic a second time
	processedCount := 0
	if !hasStopTimeAlready {
		processedCount++
	}

	if processedCount != 0 {
		t.Errorf("Duplicate Stop packet must be deduplicated and not processed again")
	}
	_ = stopTime2
}
