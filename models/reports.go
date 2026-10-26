package models

import "time"

// SubscriberDebtItem represents a subscriber debt summary row
type SubscriberDebtItem struct {
	SubscriberID         int64   `json:"subscriber_id"`
	Username             string  `json:"username"`
	FullName             string  `json:"full_name"`
	ContractNo           string  `json:"contract_no"`
	Phone                string  `json:"phone"`
	ResellerID           int64   `json:"reseller_id"`
	ResellerName         string  `json:"reseller_name"`
	Balance              float64 `json:"balance"`
	Debt                 float64 `json:"debt"`
	DebtAgeDays          int     `json:"debt_age_days"`
	DebtStatus           string  `json:"debt_status"` // "none", "minor", "overdue", "critical"
	DebtStatusBadgeClass string  `json:"debt_status_badge_class"`
	DebtStatusText       string  `json:"debt_status_text"`
	LastPaymentAmount    float64 `json:"last_payment_amount"`
	LastPaymentDate      string  `json:"last_payment_date"`
	LastRenewalDate      string  `json:"last_renewal_date"`
}

// SubscriberDebtsSummary holds KPI aggregates for subscriber debts
type SubscriberDebtsSummary struct {
	TotalDebtorsCount int64   `json:"total_debtors_count"`
	TotalDebtAmount   float64 `json:"total_debt_amount"`
	TotalSettledToday float64 `json:"total_settled_today"`
	AverageDebt       float64 `json:"average_debt"`
}

// ResellerBalanceItem represents a financial summary row for a reseller
type ResellerBalanceItem struct {
	ResellerID        int64   `json:"reseller_id"`
	Name              string  `json:"name"`
	Username          string  `json:"username"`
	Phone             string  `json:"phone"`
	Role              string  `json:"role"`
	Balance           float64 `json:"balance"`
	CreditLimit       float64 `json:"credit_limit"`
	Debt              float64 `json:"debt"`
	AvailableCapacity float64 `json:"available_capacity"` // balance + credit_limit - debt
	CommissionPercent float64 `json:"commission_percent"`
	TotalDeposits     float64 `json:"total_deposits"`
	TotalDeductions   float64 `json:"total_deductions"`
	TotalRenewalsCost float64 `json:"total_renewals_cost"`
	TotalQuotaCost    float64 `json:"total_quota_cost"`
	TotalCardsCost    float64 `json:"total_cards_cost"`
	LastTxAmount      float64 `json:"last_tx_amount"`
	LastTxDate        string  `json:"last_tx_date"`
	IsActive          bool    `json:"is_active"`
	SubscribersCount  int     `json:"subscribers_count"`
}

// ResellerBalancesSummary holds KPI aggregates for resellers
type ResellerBalancesSummary struct {
	TotalResellerBalances  float64 `json:"total_reseller_balances"`
	TotalResellerDebt      float64 `json:"total_reseller_debt"`
	TotalAvailableCap      float64 `json:"total_available_cap"`
	TotalDepositsSum       float64 `json:"total_deposits_sum"`
	ActiveResellersCount   int     `json:"active_resellers_count"`
	InactiveResellersCount int     `json:"inactive_resellers_count"`
}

// ResellerLedgerItem represents an individual financial statement line
type ResellerLedgerItem struct {
	ID                 int64     `json:"id"`
	ReferenceID        string    `json:"reference_id"`
	IdempotencyKey     string    `json:"idempotency_key"`
	ResellerID         int64     `json:"reseller_id"`
	ResellerName       string    `json:"reseller_name"`
	OperationType      string    `json:"operation_type"`
	OperationTypeText  string    `json:"operation_type_text"`
	Amount             float64   `json:"amount"`
	BalanceBefore      float64   `json:"balance_before"`
	BalanceAfter       float64   `json:"balance_after"`
	DebtBefore         float64   `json:"debt_before"`
	DebtAfter          float64   `json:"debt_after"`
	SubscriberName     string    `json:"subscriber_name"`
	Description        string    `json:"description"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	CreatedAtFormatted string    `json:"created_at_formatted"`
}

// SubscriberBalanceItem represents a subscriber's current balance & debt standing
type SubscriberBalanceItem struct {
	SubscriberID      int64   `json:"subscriber_id"`
	Username          string  `json:"username"`
	FullName          string  `json:"full_name"`
	ContractNo        string  `json:"contract_no"`
	Phone             string  `json:"phone"`
	ResellerID        int64   `json:"reseller_id"`
	ResellerName      string  `json:"reseller_name"`
	ProfileName       string  `json:"profile_name"`
	Balance           float64 `json:"balance"`
	Debt              float64 `json:"debt"`
	NetBalance        float64 `json:"net_balance"` // balance - debt
	Status            string  `json:"status"`
	LoanActive        bool    `json:"loan_active"`
	LastDepositAmount float64 `json:"last_deposit_amount"`
	LastDepositDate   string  `json:"last_deposit_date"`
	TotalDeposits     float64 `json:"total_deposits"`
	TotalPurchases    float64 `json:"total_purchases"`
	LastTxDate        string  `json:"last_tx_date"`
	LastTxType        string  `json:"last_tx_type"`
}

// SubscriberBalancesSummary holds KPI aggregates for subscriber balances
type SubscriberBalancesSummary struct {
	TotalBalancesSum float64 `json:"total_balances_sum"`
	TotalDebtsSum    float64 `json:"total_debts_sum"`
	NetLiquidity     float64 `json:"net_liquidity"`
	PositiveAccounts int64   `json:"positive_accounts"`
	DebtorAccounts   int64   `json:"debtor_accounts"`
}

// SubscriberLedgerItem represents an individual subscriber ledger statement line
type SubscriberLedgerItem struct {
	ID                 int64     `json:"id"`
	ReferenceID        string    `json:"reference_id"`
	Username           string    `json:"username"`
	FullName           string    `json:"full_name"`
	OperationType      string    `json:"operation_type"`
	OperationTypeText  string    `json:"operation_type_text"`
	Amount             float64   `json:"amount"`
	BalanceBefore      float64   `json:"balance_before"`
	BalanceAfter       float64   `json:"balance_after"`
	DebtBefore         float64   `json:"debt_before"`
	DebtAfter          float64   `json:"debt_after"`
	PaymentMethod      string    `json:"payment_method"`
	Description        string    `json:"description"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	CreatedAtFormatted string    `json:"created_at_formatted"`
}
