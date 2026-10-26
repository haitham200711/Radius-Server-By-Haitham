package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/money"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/radius"
)

var (
	ErrDuplicateIdempotency   = errors.New("idempotency key already processed or in progress")
	ErrIdempotencyKeyConflict = errors.New("idempotency key already used with different request parameters")
	ErrIdempotencyNotFound    = errors.New("idempotency record not found")
	ErrInvalidOperation       = errors.New("invalid financial operation")
)

// FinancialRecord represents an immutable entry in the financial ledger
type FinancialRecord struct {
	ID                 int64                  `json:"id"`
	ReferenceID        string                 `json:"reference_id"`
	IdempotencyKey     string                 `json:"idempotency_key,omitempty"`
	OperationType      string                 `json:"operation_type"` // e.g. 'reseller_renew', 'reseller_quota', 'reseller_cards', 'subscriber_renew', 'subscriber_debt_pay', etc.
	AccountType        string                 `json:"account_type"`   // 'reseller', 'subscriber', 'system'
	SubscriberID       int64                  `json:"subscriber_id"`
	SubscriberUsername string                 `json:"subscriber_username"`
	ResellerID         int64                  `json:"reseller_id"`
	AmountCents        int64                  `json:"amount_cents"`
	Amount             float64                `json:"amount"`
	BalanceBefore      float64                `json:"balance_before"`
	BalanceAfter       float64                `json:"balance_after"`
	DebtBefore         float64                `json:"debt_before"`
	DebtAfter          float64                `json:"debt_after"`
	Status             string                 `json:"status"` // 'completed', 'pending', 'reversed'
	Description        string                 `json:"description"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
	CreatedBy          string                 `json:"created_by"`
	CreatedAt          time.Time              `json:"created_at"`
}

// AuditLogRecord represents an entry in the system audit log
type AuditLogRecord struct {
	ID            int64     `json:"id"`
	ReferenceID   string    `json:"reference_id"`
	ActorID       int64     `json:"actor_id"`
	ActorUsername string    `json:"actor_username"`
	ActorRole     string    `json:"actor_role"`
	Action        string    `json:"action"`
	TargetType    string    `json:"target_type"`
	TargetID      string    `json:"target_id"`
	Details       string    `json:"details"`
	IPAddress     string    `json:"ip_address"`
	CreatedAt     time.Time `json:"created_at"`
}

// LoanRecord represents a loan tracking entry in loan_transactions
type LoanRecord struct {
	ID                 int64      `json:"id"`
	LoanID             string     `json:"loan_id"`
	ReferenceID        string     `json:"reference_id"`
	SubscriberID       int64      `json:"subscriber_id"`
	Username           string     `json:"username"`
	CommercialValue    float64    `json:"commercial_value"`
	GrantedHours       int        `json:"granted_hours"`
	GrantedQuotaBytes  int64      `json:"granted_quota_bytes"`
	DeductionType      string     `json:"deduction_type"`
	InitialUsedBytes   int64      `json:"initial_used_bytes"`
	UsedBytes          int64      `json:"used_bytes"`
	DeductedHours      int        `json:"deducted_hours"`
	DeductedQuotaBytes int64      `json:"deducted_quota_bytes"`
	DeductedAmount     float64    `json:"deducted_amount"`
	RemainingAmount    float64    `json:"remaining_amount"`
	Status             string     `json:"status"` // 'active', 'settled', 'cancelled'
	GrantedAt          time.Time  `json:"granted_at"`
	SettledAt          *time.Time `json:"settled_at,omitempty"`
}

// OutboxRecord represents an event enqueued in financial_outbox
type OutboxRecord struct {
	ID            int64      `json:"id"`
	EventType     string     `json:"event_type"`     // 'coa_disconnect', 'coa_rate_limit', 'whatsapp_notice'
	AggregateType string     `json:"aggregate_type"` // 'subscriber', 'reseller', 'nas'
	AggregateID   string     `json:"aggregate_id"`
	Payload       string     `json:"payload"`
	Status        string     `json:"status"` // 'pending', 'processing', 'completed', 'failed', 'dead_letter'
	RetryCount    int        `json:"retry_count"`
	MaxRetries    int        `json:"max_retries"`
	LeaseToken    string     `json:"lease_token,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	ScheduledAt   time.Time  `json:"scheduled_at"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// OutboxPayload specifies the structured network payload for asynchronous RADIUS operations
type OutboxPayload struct {
	Username           string `json:"username"`
	SessionID          string `json:"session_id,omitempty"`
	NASIP              string `json:"nas_ip,omitempty"`
	FramedIP           string `json:"framed_ip,omitempty"`
	RateLimit          string `json:"rate_limit,omitempty"`
	AddressList        string `json:"address_list,omitempty"`
	OperationReference string `json:"operation_reference,omitempty"`
	IdempotencyKey     string `json:"idempotency_key,omitempty"`
	Trigger            string `json:"trigger,omitempty"`
}

// BulkItemResult holds the status of an individual item in a bulk operation
type BulkItemResult struct {
	Identifier  string `json:"identifier"`
	Success     bool   `json:"success"`
	ReferenceID string `json:"reference_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// GenerateReferenceID generates a cryptographically random, collision-resistant reference ID
func GenerateReferenceID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	if prefix == "" {
		prefix = "REF"
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().Unix(), hex.EncodeToString(b))
}

// GenerateDeterministicKey creates a deterministic idempotency key for requests lacking an explicit header.
// It uses a 10-minute time bucket to enable safe retries within the same flow while avoiding indefinite locks.
func GenerateDeterministicKey(operationType, identifier string, params ...interface{}) string {
	h := sha256.New()
	h.Write([]byte(operationType))
	h.Write([]byte(":"))
	h.Write([]byte(identifier))
	for _, p := range params {
		h.Write([]byte(":"))
		h.Write([]byte(fmt.Sprintf("%v", p)))
	}
	bucket := time.Now().Unix() / 600
	h.Write([]byte(fmt.Sprintf(":%d", bucket)))
	return fmt.Sprintf("IDEM-%s-%s", operationType, hex.EncodeToString(h.Sum(nil))[:24])
}

// ResolveIdempotencyKey returns a bounded client key or a deterministic fallback.
// Explicit keys are limited to the database column size so oversized headers
// cannot make an otherwise valid financial transaction fail at commit time.
func ResolveIdempotencyKey(keys []string, operationType, identifier string, params ...interface{}) (string, error) {
	key := ""
	if len(keys) > 0 {
		key = strings.TrimSpace(keys[0])
	}
	if key == "" {
		key = GenerateDeterministicKey(operationType, identifier, params...)
	}
	if len(key) > 128 {
		return "", fmt.Errorf("idempotency key exceeds 128 characters")
	}
	return key, nil
}

// ComputeRequestHash computes a SHA256 digest of request parameters to detect payload tampering/mismatches on retry.
func ComputeRequestHash(payload interface{}) string {
	if payload == nil {
		return ""
	}
	var data []byte
	switch p := payload.(type) {
	case string:
		data = []byte(p)
	case []byte:
		data = p
	default:
		data, _ = json.Marshal(p)
	}
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

// RecordFinancialTx inserts a ledger entry within an ongoing database transaction
func (d *DB) RecordFinancialTx(tx *sql.Tx, rec *FinancialRecord) error {
	if rec.ReferenceID == "" {
		rec.ReferenceID = GenerateReferenceID("FIN")
	}
	if rec.AmountCents == 0 && rec.Amount != 0 {
		rec.AmountCents = money.ToCents(rec.Amount)
	}
	if rec.Amount == 0 && rec.AmountCents != 0 {
		rec.Amount = money.FromCents(rec.AmountCents)
	}
	if rec.Status == "" {
		rec.Status = "completed"
	}
	if rec.CreatedBy == "" {
		rec.CreatedBy = "system"
	}

	var metadataStr sql.NullString
	if len(rec.Metadata) > 0 {
		b, err := json.Marshal(rec.Metadata)
		if err == nil {
			metadataStr = sql.NullString{String: string(b), Valid: true}
		}
	}

	var idempKey sql.NullString
	if rec.IdempotencyKey != "" {
		idempKey = sql.NullString{String: rec.IdempotencyKey, Valid: true}
	}

	res, err := tx.Exec(`INSERT INTO financial_ledger (
		reference_id, idempotency_key, operation_type, account_type,
		subscriber_id, subscriber_username, reseller_id,
		amount_cents, amount, balance_before, balance_after, debt_before, debt_after,
		status, description, metadata, created_by, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())`,
		rec.ReferenceID, idempKey, rec.OperationType, rec.AccountType,
		rec.SubscriberID, rec.SubscriberUsername, rec.ResellerID,
		rec.AmountCents, rec.Amount, rec.BalanceBefore, rec.BalanceAfter, rec.DebtBefore, rec.DebtAfter,
		rec.Status, rec.Description, metadataStr, rec.CreatedBy)
	if err != nil {
		return fmt.Errorf("failed to write financial ledger: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		rec.ID = id
	}
	return nil
}

// RecordAuditTx writes an audit log entry within an ongoing transaction
func (d *DB) RecordAuditTx(tx *sql.Tx, rec *AuditLogRecord) error {
	if rec.ReferenceID == "" {
		rec.ReferenceID = GenerateReferenceID("AUD")
	}
	if rec.ActorUsername == "" {
		rec.ActorUsername = "system"
	}

	res, err := tx.Exec(`INSERT INTO audit_logs (
		reference_id, actor_id, actor_username, actor_role, action, target_type, target_id, details, ip_address, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())`,
		rec.ReferenceID, rec.ActorID, rec.ActorUsername, rec.ActorRole, rec.Action, rec.TargetType, rec.TargetID, rec.Details, rec.IPAddress)
	if err != nil {
		return fmt.Errorf("failed to write audit log: %w", err)
	}
	id, err := res.LastInsertId()
	if err == nil {
		rec.ID = id
	}
	return nil
}

// RecordAudit writes an audit log entry in its own transaction
func (d *DB) RecordAudit(rec *AuditLogRecord) error {
	if rec.ReferenceID == "" {
		rec.ReferenceID = GenerateReferenceID("AUD")
	}
	if rec.ActorUsername == "" {
		rec.ActorUsername = "system"
	}

	_, err := d.Exec(`INSERT INTO audit_logs (
		reference_id, actor_id, actor_username, actor_role, action, target_type, target_id, details, ip_address, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())`,
		rec.ReferenceID, rec.ActorID, rec.ActorUsername, rec.ActorRole, rec.Action, rec.TargetType, rec.TargetID, rec.Details, rec.IPAddress)
	return err
}

// EnqueueOutboxTx enqueues an asynchronous side-effect (e.g. CoA, Disconnect, notification) into financial_outbox
func (d *DB) EnqueueOutboxTx(tx *sql.Tx, eventType, aggregateType, aggregateID string, payload interface{}) error {
	var payloadStr string
	switch p := payload.(type) {
	case string:
		payloadStr = p
	case []byte:
		payloadStr = string(p)
	default:
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal outbox payload: %w", err)
		}
		payloadStr = string(b)
	}

	_, err := tx.Exec(`INSERT INTO financial_outbox (
		event_type, aggregate_type, aggregate_id, payload, status, retry_count, max_retries, scheduled_at, created_at
	) VALUES (?, ?, ?, ?, 'pending', 0, 5, NOW(), NOW())`,
		eventType, aggregateType, aggregateID, payloadStr)
	if err != nil {
		return fmt.Errorf("failed to enqueue financial outbox event: %w", err)
	}
	return nil
}

// AcquireIdempotencyKeyTx checks and reserves an idempotency key inside an ongoing transaction.
// If key does not exist: inserts with status 'in_progress' and returns (acquired=true, 0, "", nil).
// If key exists and is completed: returns (acquired=false, responseCode, responseBody, nil).
// If key exists and is in_progress: returns (acquired=false, 0, "", ErrDuplicateIdempotency).
// If key exists with different request_hash: returns (acquired=false, 0, "", ErrIdempotencyKeyConflict).
func (d *DB) AcquireIdempotencyKeyTx(tx *sql.Tx, key, operationType, resourceID, requestHash string, ttl time.Duration) (bool, int, string, error) {
	if key == "" {
		return true, 0, "", nil
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	var (
		status       string
		storedHash   sql.NullString
		responseCode int
		responseBody sql.NullString
		expiresAt    time.Time
	)

	err := tx.QueryRow(`SELECT status, request_hash, response_code, response_body, expires_at 
		FROM idempotency_keys WHERE idempotency_key = ? FOR UPDATE`, key).Scan(
		&status, &storedHash, &responseCode, &responseBody, &expiresAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		// Key does not exist; insert as in_progress within this transaction
		exp := time.Now().Add(ttl)
		_, err = tx.Exec(`INSERT INTO idempotency_keys (
			idempotency_key, operation_type, resource_id, request_hash, response_code, response_body, status, created_at, expires_at
		) VALUES (?, ?, ?, ?, 0, NULL, 'in_progress', NOW(), ?)`, key, operationType, resourceID, requestHash, exp)
		if err != nil {
			return false, 0, "", fmt.Errorf("failed to reserve idempotency key: %w", err)
		}
		return true, 0, "", nil
	}

	if err != nil {
		return false, 0, "", err
	}

	// Key already exists: verify request hash to detect mismatched payloads
	if requestHash != "" && storedHash.Valid && storedHash.String != "" && storedHash.String != requestHash {
		return false, 0, "", ErrIdempotencyKeyConflict
	}

	// If completed: return cached response
	if status == "completed" {
		return false, responseCode, responseBody.String, nil
	}

	// If in_progress but expired: take over lease
	if time.Now().After(expiresAt) {
		exp := time.Now().Add(ttl)
		res, err := tx.Exec(`UPDATE idempotency_keys SET status = 'in_progress', request_hash = ?, response_code = 0, response_body = NULL, expires_at = ? 
			WHERE idempotency_key = ?`, requestHash, exp, key)
		if err != nil {
			return false, 0, "", err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			return true, 0, "", nil
		}
	}

	return false, 0, "", ErrDuplicateIdempotency
}

// CompleteIdempotencyKeyTx marks an idempotency key as completed within the transaction
func (d *DB) CompleteIdempotencyKeyTx(tx *sql.Tx, key string, responseCode int, responseBody string) error {
	if key == "" {
		return nil
	}
	res, err := tx.Exec(`UPDATE idempotency_keys SET status = 'completed', response_code = ?, response_body = ? WHERE idempotency_key = ?`,
		responseCode, responseBody, key)
	if err != nil {
		return fmt.Errorf("failed to complete idempotency key: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("idempotency key %s not found to complete", key)
	}
	return nil
}

// FailIdempotencyKeyTx removes an idempotency key within the transaction on non-fatal retryable failure
func (d *DB) FailIdempotencyKeyTx(tx *sql.Tx, key string) error {
	if key == "" {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM idempotency_keys WHERE idempotency_key = ? AND status = 'in_progress'`, key)
	return err
}

// AcquireIdempotencyKey checks if an idempotency key exists in standalone mode
func (d *DB) AcquireIdempotencyKey(key, operationType, resourceID, requestHash string, ttl time.Duration) (bool, int, string, error) {
	if key == "" {
		return true, 0, "", nil
	}

	tx, err := d.Begin()
	if err != nil {
		return false, 0, "", err
	}
	defer tx.Rollback()

	acquired, code, body, err := d.AcquireIdempotencyKeyTx(tx, key, operationType, resourceID, requestHash, ttl)
	if err != nil {
		return false, 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, "", err
	}
	return acquired, code, body, nil
}

// CompleteIdempotencyKey marks an idempotency key as completed in standalone mode
func (d *DB) CompleteIdempotencyKey(key string, responseCode int, responseBody string) error {
	if key == "" {
		return nil
	}
	_, err := d.Exec(`UPDATE idempotency_keys SET status = 'completed', response_code = ?, response_body = ? WHERE idempotency_key = ?`,
		responseCode, responseBody, key)
	return err
}

// FailIdempotencyKey removes or marks an idempotency key as failed so it can be retried
func (d *DB) FailIdempotencyKey(key string) error {
	if key == "" {
		return nil
	}
	_, err := d.Exec(`DELETE FROM idempotency_keys WHERE idempotency_key = ? AND status = 'in_progress'`, key)
	return err
}

// Background Outbox Worker
var outboxWorkerOnce sync.Once

// StartOutboxWorker starts a background worker that polls financial_outbox and processes pending events
func (d *DB) StartOutboxWorker(ctx context.Context) {
	outboxWorkerOnce.Do(func() {
		go func() {
			workerID := fmt.Sprintf("worker-%d", time.Now().UnixNano())
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_, _, _ = d.ProcessOutboxBatch(ctx, 50, workerID)
				}
			}
		}()
	})
}

// ProcessOutboxBatch processes up to batchSize items from financial_outbox using atomic leases.
// It ensures multiple concurrent workers never process the same event twice.
func (d *DB) ProcessOutboxBatch(ctx context.Context, batchSize int, workerID string) (processed int, failed int, err error) {
	if batchSize <= 0 {
		batchSize = 50
	}
	if workerID == "" {
		workerID = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}

	rows, err := d.Query(`SELECT id, event_type, aggregate_type, aggregate_id, payload, retry_count, max_retries 
		FROM financial_outbox 
		WHERE (status = 'pending' AND scheduled_at <= NOW() AND retry_count < max_retries)
		   OR (status = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at < NOW() AND retry_count < max_retries)
		ORDER BY id ASC LIMIT ?`, batchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query pending outbox items: %w", err)
	}
	defer rows.Close()

	type outboxCandidate struct {
		id            int64
		eventType     string
		aggregateType string
		aggregateID   string
		payload       string
		retryCount    int
		maxRetries    int
	}

	var candidates []outboxCandidate
	for rows.Next() {
		var c outboxCandidate
		if err := rows.Scan(&c.id, &c.eventType, &c.aggregateType, &c.aggregateID, &c.payload, &c.retryCount, &c.maxRetries); err == nil {
			candidates = append(candidates, c)
		}
	}
	rows.Close()

	for _, item := range candidates {
		select {
		case <-ctx.Done():
			return processed, failed, ctx.Err()
		default:
		}

		// ATOMIC CLAIM: acquire exclusive 30-second lease
		leaseExp := time.Now().Add(30 * time.Second)
		res, err := d.Exec(`UPDATE financial_outbox 
			SET status = 'processing', lease_token = ?, lease_expires_at = ? 
			WHERE id = ? AND (
				status = 'pending' OR (status = 'processing' AND lease_expires_at < NOW())
			)`, workerID, leaseExp, item.id)
		if err != nil {
			log.Printf("[OUTBOX] Error attempting lease on item %d: %v", item.id, err)
			continue
		}
		rowsAff, _ := res.RowsAffected()
		if rowsAff == 0 {
			// Another worker claimed this item concurrently, skip safely
			continue
		}

		// Parse payload
		var p OutboxPayload
		if err := json.Unmarshal([]byte(item.payload), &p); err != nil {
			p.Username = item.aggregateID
		}

		var procErr error
		switch item.eventType {
		case "coa_disconnect", "disconnect":
			// If session ID and NAS IP are present, send targeted disconnect
			if p.NASIP != "" && p.SessionID != "" {
				resCoA, errCoA := d.DisconnectTargetedSession(p.Username, p.NASIP, p.SessionID, p.FramedIP)
				if errCoA != nil {
					procErr = errCoA
				} else if resCoA != nil && !resCoA.Success {
					// Residual session context removed is an acceptable success
					if resCoA.ErrorCause != radius.ErrorCauseSessionContextNotFound && resCoA.ErrorCause != radius.ErrorCauseResidualSessionContextRemoved {
						procErr = fmt.Errorf("RADIUS Disconnect NAK code %d cause %d: %s", resCoA.Code, resCoA.ErrorCause, resCoA.Message)
					}
				}
			} else {
				// Broadcast / multi-session disconnect
				_, _, errDis := d.DisconnectSubscriberSession(p.Username)
				if errDis != nil && !strings.Contains(errDis.Error(), "غير متصل") {
					procErr = errDis
				}
			}

		case "coa_rate_limit", "coa":
			if p.RateLimit != "" {
				_, _, errRate := d.SendSubscriberRateLimitCoA(p.Username, p.RateLimit)
				if errRate != nil && !strings.Contains(errRate.Error(), "غير متصل") {
					procErr = errRate
				}
			} else {
				errRest := d.RestoreActiveSubscriberCoA(p.Username)
				if errRest != nil && !strings.Contains(errRest.Error(), "غير متصل") {
					procErr = errRest
				}
			}

		default:
			procErr = nil
		}

		if procErr != nil {
			failed++
			newRetries := item.retryCount + 1
			newStatus := "pending"
			if newRetries >= item.maxRetries {
				newStatus = "dead_letter"
			}
			backoffSec := int(1<<uint(newRetries)) * 5
			if backoffSec > 3600 {
				backoffSec = 3600
			}
			nextRetry := time.Now().Add(time.Duration(backoffSec) * time.Second)

			_, updateErr := d.Exec(`UPDATE financial_outbox 
				SET status = ?, retry_count = ?, last_error = ?, scheduled_at = ?, lease_token = NULL, lease_expires_at = NULL 
					WHERE id = ? AND lease_token = ?`, newStatus, newRetries, procErr.Error(), nextRetry, item.id, workerID)
			if updateErr != nil {
				log.Printf("[OUTBOX] Failed to update error status on item %d: %v", item.id, updateErr)
			}
		} else {
			processed++
			_, updateErr := d.Exec(`UPDATE financial_outbox 
				SET status = 'completed', processed_at = NOW(), lease_token = NULL, lease_expires_at = NULL 
					WHERE id = ? AND lease_token = ?`, item.id, workerID)
			if updateErr != nil {
				log.Printf("[OUTBOX] Failed to update completed status on item %d: %v", item.id, updateErr)
			}
		}
	}

	return processed, failed, nil
}

// DisconnectTargetedSession sends an RFC 5176 Disconnect-Request targeted to a specific NAS IP and session ID
func (d *DB) DisconnectTargetedSession(username, nasIP, sessionID, framedIPStr string) (*radius.CoAResult, error) {
	var secret string
	var coaPort int
	err := d.QueryRow("SELECT secret, coa_port FROM nas WHERE nasname = ?", nasIP).Scan(&secret, &coaPort)
	if err != nil {
		return nil, fmt.Errorf("nas %s not found: %w", nasIP, err)
	}
	if coaPort <= 0 {
		coaPort = 3799
	}

	var framedIP net.IP
	if framedIPStr != "" {
		framedIP = net.ParseIP(framedIPStr)
	}

	res, err := radius.SendDisconnectRequest(nasIP, coaPort, []byte(secret), username, sessionID, framedIP)
	if err == nil && res != nil && res.Success {
		_, _ = d.Exec(`UPDATE radacct SET disconnect_status = 'pending', disconnect_pending_at = CURRENT_TIMESTAMP
			WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`, sessionID, nasIP)
	}
	return res, err
}
