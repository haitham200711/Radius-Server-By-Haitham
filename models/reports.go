package models

import "time"

// ReportsExecutiveSummary holds high-level KPI dashboard metrics for executive oversight
type ReportsExecutiveSummary struct {
	OnlineNow            int64   `json:"online_now"`
	TodayTrafficBytes    int64   `json:"today_traffic_bytes"`
	TodayTrafficDisplay  string  `json:"today_traffic_display"`
	FailedLoginsToday    int64   `json:"failed_logins_today"`
	QuotaExhaustedCount  int64   `json:"quota_exhausted_count"`
	ExpiredCount         int64   `json:"expired_count"`
	TotalIssuesCount     int64   `json:"total_issues_count"`
	TotalCustomerDebt    float64 `json:"total_customer_debt"`
	TotalCustomerBalance float64 `json:"total_customer_balance"`
	TotalResellerBalance float64 `json:"total_reseller_balance"`
	TotalRevenueMonth    float64 `json:"total_revenue_month"`
	ActiveRoutersCount   int     `json:"active_routers_count"`
	TotalRoutersCount    int     `json:"total_routers_count"`
	PeakTrafficHourText  string  `json:"peak_traffic_hour_text"`
}

// AuthLogItem represents a RADIUS authentication attempt record
type AuthLogItem struct {
	ID                 int64     `json:"id"`
	Username           string    `json:"username"`
	FullName           string    `json:"full_name"`
	ContractNo         string    `json:"contract_no"`
	ResellerName       string    `json:"reseller_name"`
	Reply              string    `json:"reply"` // "Access-Accept" or "Access-Reject"
	IsSuccess          bool      `json:"is_success"`
	StatusBadgeClass   string    `json:"status_badge_class"`
	ReasonText         string    `json:"reason_text"`
	ErrorCategory      string    `json:"error_category"`
	AuthDate           time.Time `json:"authdate"`
	AuthDateFormatted  string    `json:"authdate_formatted"`
	ClientIP           string    `json:"client_ip"`
	NASName            string    `json:"nas_name"`
}

// AuthLogSummary aggregates auth statistics
type AuthLogSummary struct {
	TotalAttempts      int64   `json:"total_attempts"`
	AcceptedCount      int64   `json:"accepted_count"`
	RejectedCount      int64   `json:"rejected_count"`
	SuccessRatePercent float64 `json:"success_rate_percent"`
}

// QuotaExpiryItem represents a subscriber's current quota and validity state
type QuotaExpiryItem struct {
	SubscriberID       int64   `json:"subscriber_id"`
	Username           string  `json:"username"`
	FullName           string  `json:"full_name"`
	ContractNo         string  `json:"contract_no"`
	Phone              string  `json:"phone"`
	ResellerName       string  `json:"reseller_name"`
	ProfileName        string  `json:"profile_name"`
	TotalQuotaBytes    int64   `json:"total_quota_bytes"`
	TotalQuotaGB       float64 `json:"total_quota_gb"`
	UsedBytes          int64   `json:"used_bytes"`
	UsedGB             float64 `json:"used_gb"`
	RemainingBytes     int64   `json:"remaining_bytes"`
	RemainingGB        float64 `json:"remaining_gb"`
	UsagePercent       float64 `json:"usage_percent"`
	ExpirationDate     string  `json:"expiration_date"`
	DaysRemaining      int     `json:"days_remaining"`
	StatusCategory     string  `json:"status_category"` // "exhausted", "near_quota", "near_time", "healthy"
	StatusBadgeClass   string  `json:"status_badge_class"`
	StatusText         string  `json:"status_text"`
	LastAddonAmountGB  float64 `json:"last_addon_amount_gb"`
	LastAddonDate      string  `json:"last_addon_date"`
}

// QuotaExpirySummary aggregates quota and expiration indicators
type QuotaExpirySummary struct {
	TotalAssignedQuotaGB float64 `json:"total_assigned_quota_gb"`
	TotalConsumedQuotaGB float64 `json:"total_consumed_quota_gb"`
	ExhaustedCount       int64   `json:"exhausted_count"`
	NearExpiryQuotaCount int64   `json:"near_expiry_quota_count"`
	NearExpiryTimeCount  int64   `json:"near_expiry_time_count"`
}

// ServiceStatusItem represents subscriber service and disconnect event tracking
type ServiceStatusItem struct {
	SubscriberID       int64   `json:"subscriber_id"`
	Username           string  `json:"username"`
	FullName           string  `json:"full_name"`
	ResellerName       string  `json:"reseller_name"`
	ProfileName        string  `json:"profile_name"`
	Status             string  `json:"status"` // "active", "expired", "suspended", "dormant"
	StatusBadgeClass   string  `json:"status_badge_class"`
	StatusText         string  `json:"status_text"`
	IsOnline           bool    `json:"is_online"`
	LastLoginDate      string  `json:"last_login_date"`
	DaysSinceLastLogin int     `json:"days_since_last_login"`
	LastDisconnectDate string  `json:"last_disconnect_date"`
	DisconnectReason   string  `json:"disconnect_reason"`
	CoASuccessCount    int     `json:"coa_success_count"`
	CoAFailCount       int     `json:"coa_fail_count"`
}

// ServiceStatusSummary aggregates service status
type ServiceStatusSummary struct {
	ActiveCount        int64 `json:"active_count"`
	ExpiredCount       int64 `json:"expired_count"`
	SuspendedCount     int64 `json:"suspended_count"`
	DormantCount       int64 `json:"dormant_count"`
	CoASuccessTotal    int64 `json:"coa_success_total"`
	CoAFailTotal       int64 `json:"coa_fail_total"`
}

// SubscribersGrowthItem represents distribution by profile or reseller
type SubscribersGrowthItem struct {
	GroupName          string  `json:"group_name"`
	Count              int64   `json:"count"`
	ActiveCount        int64   `json:"active_count"`
	ExpiredCount       int64   `json:"expired_count"`
	Percentage         float64 `json:"percentage"`
	TotalRevenue       float64 `json:"total_revenue"`
}

// SubscribersGrowthSummary aggregates churn and retention metrics
type SubscribersGrowthSummary struct {
	TotalSubscribers   int64   `json:"total_subscribers"`
	NewThisMonth       int64   `json:"new_this_month"`
	RenewedThisMonth   int64   `json:"renewed_this_month"`
	RetentionRate      float64 `json:"retention_rate"`
	ChurnRate          float64 `json:"churn_rate"`
}

// RevenueProfitItem represents revenue breakdown by profile or reseller
type RevenueProfitItem struct {
	ItemName           string  `json:"item_name"`
	Category           string  `json:"category"`
	SalesCount         int64   `json:"sales_count"`
	GrossRevenue       float64 `json:"gross_revenue"`
	CostPrice          float64 `json:"cost_price"`
	NetProfit          float64 `json:"net_profit"`
	ProfitMargin       float64 `json:"profit_margin"`
}

// RevenueProfitSummary aggregates overall financial profitability
type RevenueProfitSummary struct {
	TodayRevenue       float64 `json:"today_revenue"`
	MonthRevenue       float64 `json:"month_revenue"`
	YearRevenue        float64 `json:"year_revenue"`
	TotalEstimatedCost float64 `json:"total_estimated_cost"`
	TotalNetProfit     float64 `json:"total_net_profit"`
	ProfitMarginAvg    float64 `json:"profit_margin_avg"`
	TotalUncollectedDebt float64 `json:"total_uncollected_debt"`
}

// NASHealthItem represents operational monitoring of a NAS router
type NASHealthItem struct {
	ID                 int64   `json:"id"`
	NASName            string  `json:"nasname"`
	ShortName          string  `json:"shortname"`
	Type               string  `json:"type"`
	ActiveSessions     int     `json:"active_sessions"`
	TotalTrafficGB     float64 `json:"total_traffic_gb"`
	LastAcctUpdate     string  `json:"last_acct_update"`
	LastAuthAttempt    string  `json:"last_auth_attempt"`
	HealthStatus       string  `json:"health_status"` // "online", "idle", "offline"
	HealthBadgeClass   string  `json:"health_badge_class"`
	HealthText         string  `json:"health_text"`
}

// NASHealthSummary aggregates NAS operational health
type NASHealthSummary struct {
	TotalRouters       int   `json:"total_routers"`
	OnlineRouters      int   `json:"online_routers"`
	IdleRouters        int   `json:"idle_routers"`
	OfflineRouters     int   `json:"offline_routers"`
	TotalActiveSessions int  `json:"total_active_sessions"`
}

// CapacityHourlyItem represents 24-hour network load metrics
type CapacityHourlyItem struct {
	Hour               int     `json:"hour"`
	HourDisplay        string  `json:"hour_display"`
	SessionsStarted    int     `json:"sessions_started"`
	TotalTrafficGB     float64 `json:"total_traffic_gb"`
	DownloadGB         float64 `json:"download_gb"`
	UploadGB           float64 `json:"upload_gb"`
	PercentOfPeak      float64 `json:"percent_of_peak"`
}

// CapacitySummary aggregates network capacity indicators
type CapacitySummary struct {
	PeakHourText       string  `json:"peak_hour_text"`
	PeakSessionsHour   int     `json:"peak_sessions_hour"`
	PeakTrafficHourGB  float64 `json:"peak_traffic_hour_gb"`
	AvgHourlyTrafficGB float64 `json:"avg_hourly_traffic_gb"`
	BandwidthAdvisory  string  `json:"bandwidth_advisory"`
}

// AuditReportItem represents an audit trail event
type AuditReportItem struct {
	ID                 int64     `json:"id"`
	ReferenceID        string    `json:"reference_id"`
	ActorUsername      string    `json:"actor_username"`
	ActorRole          string    `json:"actor_role"`
	Action             string    `json:"action"`
	ActionText         string    `json:"action_text"`
	TargetType         string    `json:"target_type"`
	TargetID           string    `json:"target_id"`
	Details            string    `json:"details"`
	IPAddress          string    `json:"ip_address"`
	CreatedAt          time.Time `json:"created_at"`
	CreatedAtFormatted string    `json:"created_at_formatted"`
}

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
	LoanActive           bool    `json:"loan_active"`
	LoanAmount           float64 `json:"loan_amount"`
}

// SubscriberDebtsSummary holds KPI aggregates for subscriber debts
type SubscriberDebtsSummary struct {
	TotalDebtorsCount  int64   `json:"total_debtors_count"`
	TotalDebtAmount    float64 `json:"total_debt_amount"`
	TotalSettledToday  float64 `json:"total_settled_today"`
	AverageDebt        float64 `json:"average_debt"`
	AverageDebtPerUser float64 `json:"average_debt_per_user"`
	CriticalDebtsCount int64   `json:"critical_debts_count"`
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
	TotalBalancesSum       float64 `json:"total_balances_sum"`
	TotalResellerDebt      float64 `json:"total_reseller_debt"`
	TotalDebtsSum          float64 `json:"total_debts_sum"`
	TotalAvailableCap      float64 `json:"total_available_cap"`
	NetOperationalCapacity float64 `json:"net_operational_capacity"`
	TotalCreditLimitSum    float64 `json:"total_credit_limit_sum"`
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
