package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/money"
)

var (
	ErrDuplicateIdempotency = errors.New("idempotency key already processed or in progress")
	ErrIdempotencyNotFound  = errors.New("idempotency record not found")
	ErrInvalidOperation     = errors.New("invalid financial operation")
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
	LastError     string     `json:"last_error,omitempty"`
	ScheduledAt   time.Time  `json:"scheduled_at"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
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

// AcquireIdempotencyKey checks if an idempotency key exists.
// If it does not exist, it inserts it with status 'in_progress' and returns (acquired = true, responseCode = 0, responseBody = "", nil).
// If it exists and is completed, returns (acquired = false, responseCode, responseBody, nil).
// If it exists and is in_progress, returns (acquired = false, 0, "", ErrDuplicateIdempotency).
func (d *DB) AcquireIdempotencyKey(key, operationType, resourceID, requestHash string, ttl time.Duration) (bool, int, string, error) {
	if key == "" {
		return true, 0, "", nil
	}

	tx, err := d.Begin()
	if err != nil {
		return false, 0, "", err
	}
	defer tx.Rollback()

	var (
		status       string
		responseCode int
		responseBody sql.NullString
		expiresAt    time.Time
	)

	err = tx.QueryRow(`SELECT status, response_code, response_body, expires_at 
		FROM idempotency_keys WHERE idempotency_key = ? FOR UPDATE`, key).Scan(&status, &responseCode, &responseBody, &expiresAt)

	if errors.Is(err, sql.ErrNoRows) {
		// Key does not exist; insert as in_progress
		exp := time.Now().Add(ttl)
		_, err = tx.Exec(`INSERT INTO idempotency_keys (
			idempotency_key, operation_type, resource_id, request_hash, response_code, response_body, status, created_at, expires_at
		) VALUES (?, ?, ?, ?, 0, NULL, 'in_progress', NOW(), ?)`, key, operationType, resourceID, requestHash, exp)
		if err != nil {
			return false, 0, "", fmt.Errorf("failed to reserve idempotency key: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, 0, "", err
		}
		return true, 0, "", nil
	}

	if err != nil {
		return false, 0, "", err
	}

	// Key exists: check if expired
	if time.Now().After(expiresAt) {
		// Expired: reset to in_progress
		exp := time.Now().Add(ttl)
		_, err = tx.Exec(`UPDATE idempotency_keys SET status = 'in_progress', response_code = 0, response_body = NULL, expires_at = ? WHERE idempotency_key = ?`, exp, key)
		if err != nil {
			return false, 0, "", err
		}
		if err := tx.Commit(); err != nil {
			return false, 0, "", err
		}
		return true, 0, "", nil
	}

	_ = tx.Commit()

	if status == "completed" {
		return false, responseCode, responseBody.String, nil
	}

	return false, 0, "", ErrDuplicateIdempotency
}

// CompleteIdempotencyKey marks an idempotency key as completed with its response
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
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					d.processPendingOutbox(ctx)
				}
			}
		}()
	})
}

func (d *DB) processPendingOutbox(ctx context.Context) {
	rows, err := d.Query(`SELECT id, event_type, aggregate_type, aggregate_id, payload, retry_count, max_retries 
		FROM financial_outbox 
		WHERE status IN ('pending', 'processing') AND scheduled_at <= NOW() AND retry_count < max_retries 
		ORDER BY id ASC LIMIT 50`)
	if err != nil {
		return
	}
	defer rows.Close()

	type outboxItem struct {
		id            int64
		eventType     string
		aggregateType string
		aggregateID   string
		payload       string
		retryCount    int
		maxRetries    int
	}

	var items []outboxItem
	for rows.Next() {
		var it outboxItem
		if err := rows.Scan(&it.id, &it.eventType, &it.aggregateType, &it.aggregateID, &it.payload, &it.retryCount, &it.maxRetries); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, item := range items {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Mark as processing
		_, _ = d.Exec(`UPDATE financial_outbox SET status = 'processing' WHERE id = ?`, item.id)

		var procErr error
		switch item.eventType {
		case "coa_disconnect":
			// Send CoA disconnect to all NAS or specific subscriber session
			username := item.aggregateID
			_, _, procErr = d.DisconnectSubscriberSession(username)
		case "coa_rate_limit":
			// Send rate-limit change CoA
			username := item.aggregateID
			_, _, procErr = d.SendSubscriberRateLimitCoA(username, item.payload)
		default:
			procErr = nil
		}

		if procErr != nil {
			nextRetry := time.Now().Add(time.Duration(1<<uint(item.retryCount)) * 5 * time.Second)
			newRetries := item.retryCount + 1
			newStatus := "pending"
			if newRetries >= item.maxRetries {
				newStatus = "dead_letter"
			}
			_, _ = d.Exec(`UPDATE financial_outbox SET status = ?, retry_count = ?, last_error = ?, scheduled_at = ? WHERE id = ?`,
				newStatus, newRetries, procErr.Error(), nextRetry, item.id)
		} else {
			_, _ = d.Exec(`UPDATE financial_outbox SET status = 'completed', processed_at = NOW() WHERE id = ?`, item.id)
		}
	}
}
