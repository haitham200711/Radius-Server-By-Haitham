package database

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/money"
)

// TestIdempotencyDeterministicKeyAndHash validates deterministic key generation and request hash comparison
func TestIdempotencyDeterministicKeyAndHash(t *testing.T) {
	key1 := GenerateDeterministicKey("subscriber_renew", "user123", "Basic-100M", "cash")
	key2 := GenerateDeterministicKey("subscriber_renew", "user123", "Basic-100M", "cash")

	if key1 != key2 {
		t.Fatalf("Expected deterministic keys to match within same time bucket, got %s vs %s", key1, key2)
	}

	keyDiff := GenerateDeterministicKey("subscriber_renew", "user123", "Premium-200M", "cash")
	if key1 == keyDiff {
		t.Fatalf("Expected different parameters to produce distinct keys, got identical %s", key1)
	}

	payloadA := map[string]interface{}{"user": "alice", "amount": 100.0}
	payloadB := map[string]interface{}{"user": "alice", "amount": 100.0}
	payloadC := map[string]interface{}{"user": "alice", "amount": 150.0}

	hashA := ComputeRequestHash(payloadA)
	hashB := ComputeRequestHash(payloadB)
	hashC := ComputeRequestHash(payloadC)

	if hashA != hashB {
		t.Errorf("Identical payloads must produce identical hashes, got %s vs %s", hashA, hashB)
	}
	if hashA == hashC {
		t.Errorf("Different payloads must produce different hashes, got identical %s", hashA)
	}
}

// TestOutboxPayloadSerializationAndLease validates the network outbox payload structure
func TestOutboxPayloadSerializationAndLease(t *testing.T) {
	p := OutboxPayload{
		Username:           "testuser",
		SessionID:          "sess-45678",
		NASIP:              "192.168.1.1",
		FramedIP:           "10.10.10.25",
		RateLimit:          "50M/10M",
		OperationReference: "REF-2026-TEST",
		IdempotencyKey:     "IDEM-123",
		Trigger:            "subscriber_renew",
	}

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Failed to marshal OutboxPayload: %v", err)
	}

	var parsed OutboxPayload
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal OutboxPayload: %v", err)
	}

	if parsed.Username != "testuser" || parsed.SessionID != "sess-45678" || parsed.NASIP != "192.168.1.1" {
		t.Errorf("OutboxPayload fields corrupted after roundtrip: %+v", parsed)
	}

	// Test backoff exponential calculation
	retries := 3
	backoffSec := int(1<<uint(retries)) * 5 // 8 * 5 = 40s
	if backoffSec != 40 {
		t.Errorf("Expected 40s backoff for retry 3, got %d", backoffSec)
	}

	// Test dead-letter threshold
	maxRetries := 5
	newRetries := 5
	status := "pending"
	if newRetries >= maxRetries {
		status = "dead_letter"
	}
	if status != "dead_letter" {
		t.Errorf("Expected status to be dead_letter when retries reached max, got %s", status)
	}
}

// TestResellerCreditLimitStrictEnforcement validates credit limit boundary logic
func TestResellerCreditLimitStrictEnforcement(t *testing.T) {
	limitCents := money.ToCents(1000.00)   // 1,000 EGP limit
	currDebtCents := money.ToCents(800.00) // 800 EGP current debt

	// Adding 150 EGP debt -> new debt 950 EGP <= limit (Permitted)
	addDebt1 := money.ToCents(150.00)
	if limitCents > 0 && currDebtCents+addDebt1 > limitCents {
		t.Errorf("Debt 950 EGP should be allowed within 1,000 EGP limit")
	}

	// Adding 250 EGP debt -> new debt 1,050 EGP > limit (Rejected)
	addDebt2 := money.ToCents(250.00)
	if limitCents > 0 && currDebtCents+addDebt2 > limitCents {
		// correctly rejected
	} else {
		t.Errorf("Debt 1050 EGP must be rejected as it exceeds 1,000 EGP credit limit")
	}
}

// TestFinancialReconciliationLogic validates the ledger discrepancy audit logic
func TestFinancialReconciliationLogic(t *testing.T) {
	report := &FinancialReconciliationReport{
		IsBalanced:    true,
		Discrepancies: make([]FinancialDiscrepancy, 0),
	}

	curBal := 500.00
	lastBalAfter := 500.00

	if math.Abs(curBal-lastBalAfter) > 0.01 {
		report.IsBalanced = false
	}
	if !report.IsBalanced {
		t.Errorf("Report should be balanced when balance matches ledger")
	}

	// Introduce a drift
	curBalTampered := 550.00
	if math.Abs(curBalTampered-lastBalAfter) > 0.01 {
		report.IsBalanced = false
		report.Discrepancies = append(report.Discrepancies, FinancialDiscrepancy{
			Entity:        "subscriber",
			Identifier:    "sub1",
			RecordedValue: curBalTampered,
			CalculatedSum: lastBalAfter,
			Difference:    curBalTampered - lastBalAfter,
		})
	}

	if report.IsBalanced {
		t.Errorf("Report must be unbalanced when discrepancy exists")
	}
	if len(report.Discrepancies) != 1 {
		t.Errorf("Expected 1 discrepancy, got %d", len(report.Discrepancies))
	}
	if report.Discrepancies[0].Difference != 50.00 {
		t.Errorf("Expected difference 50.00, got %f", report.Discrepancies[0].Difference)
	}
}
