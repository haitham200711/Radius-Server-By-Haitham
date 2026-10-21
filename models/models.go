package models

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Admin represents system administrators and staff
type Admin struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"` // "admin", "staff"
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
}

func (a *Admin) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	a.PasswordHash = string(hash)
	return nil
}

func (a *Admin) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password))
	return err == nil
}

// ResellerPermissions defines fine-grained RBAC capabilities for distributors
type ResellerPermissions struct {
	CanAddSubscribers    bool `json:"can_add_subscribers"`
	CanEditSubscribers   bool `json:"can_edit_subscribers"`
	CanDeleteSubscribers bool `json:"can_delete_subscribers"`
	CanRenew             bool `json:"can_renew"`
	CanChangeProfile     bool `json:"can_change_profile"`
	CanAddQuota          bool `json:"can_add_quota"`
	CanViewPasswords     bool `json:"can_view_passwords"`
	CanCollectDebt       bool `json:"can_collect_debt"`
	CanAllowDebt         bool `json:"can_allow_debt"`
	CanGenerateCards     bool `json:"can_generate_cards"`
	CanDisconnect        bool `json:"can_disconnect"`
}

// DefaultResellerPermissions returns standard agent permissions
func DefaultResellerPermissions() ResellerPermissions {
	return ResellerPermissions{
		CanAddSubscribers:    true,
		CanEditSubscribers:   true,
		CanDeleteSubscribers: false,
		CanRenew:             true,
		CanChangeProfile:     true,
		CanAddQuota:          true,
		CanViewPasswords:     false,
		CanCollectDebt:       true,
		CanAllowDebt:         false,
		CanGenerateCards:     false,
		CanDisconnect:        true,
	}
}

// FullResellerPermissions returns all permissions enabled
func FullResellerPermissions() ResellerPermissions {
	return ResellerPermissions{
		CanAddSubscribers:    true,
		CanEditSubscribers:   true,
		CanDeleteSubscribers: true,
		CanRenew:             true,
		CanChangeProfile:     true,
		CanAddQuota:          true,
		CanViewPasswords:     true,
		CanCollectDebt:       true,
		CanAllowDebt:         true,
		CanGenerateCards:     true,
		CanDisconnect:        true,
	}
}

func (rp ResellerPermissions) ToJSON() string {
	b, err := json.Marshal(rp)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func ParseResellerPermissions(raw string) ResellerPermissions {
	perms := DefaultResellerPermissions()
	if raw == "" {
		return perms
	}
	_ = json.Unmarshal([]byte(raw), &perms)
	return perms
}

// Reseller represents commercial dealers/agents with balance & permissions
type Reseller struct {
	ID                int64               `json:"id"`
	Name              string              `json:"name"`
	Username          string              `json:"username"`
	PasswordHash      string              `json:"-"`
	Phone             string              `json:"phone"`
	Balance           float64             `json:"balance"`
	CreditLimit       float64             `json:"credit_limit"`
	Debt              float64             `json:"debt"`
	CommissionPercent float64             `json:"commission_percent"`
	Permissions       ResellerPermissions `json:"permissions"`
	PermissionsJSON   string              `json:"permissions_json"`
	Notes             string              `json:"notes"`
	IsActive          bool                `json:"is_active"`
	SubscribersCount  int                 `json:"subscribers_count"`
	CreatedAt         time.Time           `json:"created_at"`
}

func (r *Reseller) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	r.PasswordHash = string(hash)
	return nil
}

func (r *Reseller) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(r.PasswordHash), []byte(password))
	return err == nil
}

// ResellerTransaction logs all balance and debt changes for distributors
type ResellerTransaction struct {
	ID             int64     `json:"id"`
	ResellerID     int64     `json:"reseller_id"`
	ResellerName   string    `json:"reseller_name"`
	Type           string    `json:"type"` // "deposit", "withdraw", "pay_debt", "renew", "quota", "adjustment"
	Amount         float64   `json:"amount"`
	BalanceBefore  float64   `json:"balance_before"`
	BalanceAfter   float64   `json:"balance_after"`
	DebtBefore     float64   `json:"debt_before"`
	DebtAfter      float64   `json:"debt_after"`
	SubscriberID   int64     `json:"subscriber_id"`
	SubscriberName string    `json:"subscriber_name"`
	Notes          string    `json:"notes"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// Profile represents service plans (Speed limits, Quotas, Pricing)
type Profile struct {
	ID                  int64     `json:"id"`
	Name                string    `json:"name"`
	DisplayName         string    `json:"display_name"`
	RateLimit           string    `json:"rate_limit"` // e.g. "5M/10M" or "2048k/4096k"
	PoolName            string    `json:"pool_name"`
	QuotaBytes          int64     `json:"quota_bytes"`   // in bytes (0 = unlimited)
	QuotaGB             float64   `json:"quota_gb"`      // in GB for display/forms
	ValidityValue       int       `json:"validity_value"` // e.g. 30
	ValidityUnit        string    `json:"validity_unit"`  // "minutes", "hours", "days", "months", "years"
	UptimeSeconds       int64     `json:"uptime_seconds"` // 0 = unlimited
	CostPrice           float64   `json:"cost_price"`     // سعر التكلفة
	Price               float64   `json:"price"`          // سعر الجمهور / البيع
	SharedUsers         int       `json:"shared_users"`
	MikrotikAddressList string    `json:"mikrotik_address_list"`
	OnQuotaAction       string    `json:"on_quota_action"`  // "disconnect", "profile", "pool"
	OnQuotaProfile      string    `json:"on_quota_profile"` // fallback profile
	OnQuotaPool         string    `json:"on_quota_pool"`    // fallback IP pool
	OnTimeAction        string    `json:"on_time_action"`   // "disconnect", "profile", "pool"
	OnTimeProfile       string    `json:"on_time_profile"`  // fallback profile
	OnTimePool          string    `json:"on_time_pool"`     // fallback IP pool
	LoanEnabled         bool      `json:"loan_enabled"`        // هل السلفة مسموحة لهذه الباقة؟
	LoanHours           int       `json:"loan_hours"`          // مدة السلفة بالساعات
	LoanQuotaBytes      int64     `json:"loan_quota_bytes"`    // كوتة السلفة بالبايت
	LoanQuotaGB         float64   `json:"loan_quota_gb"`       // كوتة السلفة بالجيجا
	LoanDeductionType   string    `json:"loan_deduction_type"` // "partial" (جزئي) أو "full" (كلي)
	PortalEnabled       bool      `json:"portal_enabled"`      // إتاحة صفحة المشترك لمشتركي هذه الباقة
	CreatedAt           time.Time `json:"created_at"`
}

func (p *Profile) ValidityDisplay() string {
	if p.ValidityValue <= 0 {
		return "غير محدد"
	}
	unitMap := map[string]string{
		"minutes": "دقيقة",
		"hours":   "ساعة",
		"days":    "يوم",
		"months":  "شهر",
		"years":   "سنة",
	}
	unit, ok := unitMap[p.ValidityUnit]
	if !ok {
		unit = p.ValidityUnit
	}
	return fmt.Sprintf("%d %s", p.ValidityValue, unit)
}

func (p *Profile) CalculateExpiration(from time.Time) time.Time {
	if from.IsZero() {
		from = time.Now()
	}
	switch p.ValidityUnit {
	case "minutes":
		return from.Add(time.Duration(p.ValidityValue) * time.Minute)
	case "hours":
		return from.Add(time.Duration(p.ValidityValue) * time.Hour)
	case "days":
		return from.AddDate(0, 0, p.ValidityValue)
	case "months":
		return from.AddDate(0, p.ValidityValue, 0)
	case "years":
		return from.AddDate(p.ValidityValue, 0, 0)
	default:
		return from.AddDate(0, 0, p.ValidityValue)
	}
}

// Batch represents a generated batch of voucher cards
type Batch struct {
	ID                   int64     `json:"id"`
	BatchCode            string    `json:"batch_code"`
	BatchName            string    `json:"batch_name"`
	ProfileID            int64     `json:"profile_id"`
	ProfileName          string    `json:"profile_name"`
	ResellerID           int64     `json:"reseller_id"` // 0 = admin
	ResellerName         string    `json:"reseller_name"`
	Count                int       `json:"count"`
	AvailableCount       int       `json:"available_count"`
	UsedCount            int       `json:"used_count"`
	OnlineCount          int       `json:"online_count"`
	CostPricePerCard     float64   `json:"cost_price_per_card"` // سعر التكلفة للكارت
	PricePerCard         float64   `json:"price_per_card"`      // سعر الجمهور للكارت
	TotalCostPrice       float64   `json:"total_cost_price"`    // إجمالي سعر التكلفة
	TotalPrice           float64   `json:"total_price"`         // إجمالي سعر الجمهور
	TotalProfit          float64   `json:"total_profit"`        // إجمالي الأرباح المتوقعة
	CardType             string    `json:"card_type"` // "charge" or "cafe"
	NASID                int64     `json:"nas_id"`
	NASName              string    `json:"nas_name"`
	QuotaBytes           int64     `json:"quota_bytes"`
	TimeLimitSeconds     int64     `json:"time_limit_seconds"`
	TimeCalcMode         string    `json:"time_calc_mode"` // "first_login" or "by_second"
	SimultaneousSessions    int       `json:"simultaneous_sessions"`
	StopOnQuota             bool      `json:"stop_on_quota"`
	BindMAC                 bool      `json:"bind_mac"`
	LockMAC                 bool      `json:"lock_mac"`
	ConvertMACAfterSession  bool      `json:"convert_mac_after_session"`
	ServiceName             string    `json:"service_name"`
	AffixType               string    `json:"affix_type"` // "prefix" or "suffix"
	AffixValue              string    `json:"affix_value"`
	CreatedAt               time.Time `json:"created_at"`
}

// GenerateBatchOptions contains options for bulk card batch generation
type GenerateBatchOptions struct {
	BatchName               string  `json:"batch_name"`
	Count                   int     `json:"count"`
	ProfileID               int64   `json:"profile_id"`
	ResellerID              int64   `json:"reseller_id"`
	NASID                   int64   `json:"nas_id"`
	CardType                string  `json:"card_type"` // "charge" or "cafe"
	CostPricePerCard        float64 `json:"cost_price_per_card"` // سعر التكلفة
	PricePerCard            float64 `json:"price_per_card"`      // سعر الجمهور
	AffixType               string  `json:"affix_type"` // "prefix" or "suffix"
	AffixValue              string  `json:"affix_value"`
	CharLen                 int     `json:"char_len"`
	Format                  string  `json:"format"` // "num", "alpha", "mixed"
	QuotaBytes              int64   `json:"quota_bytes"`
	TimeLimitSeconds        int64   `json:"time_limit_seconds"`
	TimeCalcMode            string  `json:"time_calc_mode"` // "first_login" or "by_second"
	SimultaneousSessions    int     `json:"simultaneous_sessions"`
	StopOnQuota             bool    `json:"stop_on_quota"`
	BindMAC                 bool    `json:"bind_mac"`
	LockMAC                 bool    `json:"lock_mac"`
	ConvertMACAfterSession  bool    `json:"convert_mac_after_session"`
	ServiceName             string  `json:"service_name"`
}

// CafeCardTracker represents real-time diagnostic inspection of a cafe card
type CafeCardTracker struct {
	CardCode          string  `json:"card_code"`
	OriginalCode      string  `json:"original_code"`
	IsMACConverted    bool    `json:"is_mac_converted"`
	Password          string  `json:"password"`
	BatchID           int64   `json:"batch_id"`
	BatchCode         string  `json:"batch_code"`
	ProfileName       string  `json:"profile_name"`
	ProfileDisplay    string  `json:"profile_display"`
	RateLimit         string  `json:"rate_limit"`
	QuotaBytes        int64   `json:"quota_bytes"`
	QuotaDisplay      string  `json:"quota_display"`
	Price             float64 `json:"price"`
	NASID             int64   `json:"nas_id"`
	NASName           string  `json:"nas_name"`
	Status            string  `json:"status"` // "available", "online", "expired", "used"
	StatusLabel       string  `json:"status_label"`
	StatusBadgeClass  string  `json:"status_badge_class"`
	IsOnline          bool    `json:"is_online"`
	SessionID         string  `json:"session_id"`
	NASIP             string  `json:"nas_ip"`
	FramedIP          string  `json:"framed_ip"`
	CallingMAC        string  `json:"calling_mac"`
	BytesIn           int64   `json:"bytes_in"`
	BytesOut          int64   `json:"bytes_out"`
	TotalBytes        int64   `json:"total_bytes"`
	TotalBytesDisplay string  `json:"total_bytes_display"`
	BytesInDisplay    string  `json:"bytes_in_display"`
	BytesOutDisplay   string  `json:"bytes_out_display"`
	RemainingBytes    int64   `json:"remaining_bytes"`
	RemainingDisplay  string  `json:"remaining_display"`
	UsagePercent      float64 `json:"usage_percent"`
	FirstLogin        string  `json:"first_login"`
	SessionTimeSec    int64   `json:"session_time_sec"`
	UptimeDisplay     string  `json:"uptime_display"`
	ExpireReason      string  `json:"expire_reason"`

	// Device MAC Multi-Card History & Tracking
	DeviceCallingMAC      string                  `json:"device_calling_mac"`
	DeviceCardsHistory    []CafeDeviceCardItem    `json:"device_cards_history"`
	DeviceSessionsHistory []CafeDeviceSessionItem `json:"device_sessions_history"`
	DeviceTotalCardsCount int                     `json:"device_total_cards_count"`
	DeviceTotalTraffic    string                  `json:"device_total_traffic"`
	DeviceTotalUptime     string                  `json:"device_total_uptime"`
}

// CafeDeviceCardItem represents an individual card previously or currently used by the device MAC
type CafeDeviceCardItem struct {
	CardID          int64   `json:"card_id"`
	OriginalCode    string  `json:"original_code"`
	Username        string  `json:"username"`
	BatchCode       string  `json:"batch_code"`
	ProfileName     string  `json:"profile_name"`
	Price           float64 `json:"price"`
	Status          string  `json:"status"`
	StatusLabel     string  `json:"status_label"`
	StatusBadge     string  `json:"status_badge"`
	FirstLogin      string  `json:"first_login"`
	ExpiredAt       string  `json:"expired_at"`
	TotalBytes      int64   `json:"total_bytes"`
	TotalDisplay    string  `json:"total_display"`
	BytesInDisplay  string  `json:"bytes_in_display"`
	BytesOutDisplay string  `json:"bytes_out_display"`
	UptimeDisplay   string  `json:"uptime_display"`
	SessionsCount   int     `json:"sessions_count"`
	IsActive        bool    `json:"is_active"`
}

// CafeDeviceSessionItem represents a detailed individual radacct session for the device MAC
type CafeDeviceSessionItem struct {
	SessionID       string `json:"session_id"`
	CardCode        string `json:"card_code"`
	StartTime       string `json:"start_time"`
	StopTime        string `json:"stop_time"`
	Duration        string `json:"duration"`
	BytesInDisplay  string `json:"bytes_in_display"`
	BytesOutDisplay string `json:"bytes_out_display"`
	TotalDisplay    string `json:"total_display"`
	FramedIP        string `json:"framed_ip"`
	NASName         string `json:"nas_name"`
	TerminateCause  string `json:"terminate_cause"`
	IsLive          bool   `json:"is_live"`
}

// Card represents an individual hotspot / voucher card
type Card struct {
	ID                   int64        `json:"id"`
	BatchID              int64        `json:"batch_id"`
	Username             string       `json:"username"`
	Password             string       `json:"password"`
	ProfileID            int64        `json:"profile_id"`
	ProfileName          string       `json:"profile_name"`
	ResellerID           int64        `json:"reseller_id"`
	Status               string       `json:"status"` // "available", "used", "expired", "locked"
	CostPrice            float64      `json:"cost_price"` // سعر التكلفة
	Price                float64      `json:"price"`      // سعر الجمهور
	CardType             string       `json:"card_type"`
	NASID                int64        `json:"nas_id"`
	QuotaBytes           int64        `json:"quota_bytes"`
	TimeLimitSeconds     int64        `json:"time_limit_seconds"`
	TimeCalcMode         string       `json:"time_calc_mode"`
	SimultaneousSessions int          `json:"simultaneous_sessions"`
	StopOnQuota             bool         `json:"stop_on_quota"`
	BindMAC                 bool         `json:"bind_mac"`
	LockMAC                 bool         `json:"lock_mac"`
	ConvertMACAfterSession  bool         `json:"convert_mac_after_session"`
	ServiceName             string       `json:"service_name"`
	CallingMAC              string       `json:"calling_mac"`
	OriginalCode            string       `json:"original_code"`
	IsMACConverted          bool         `json:"is_mac_converted"`
	FirstLoginAt            sql.NullTime `json:"first_login_at"`
	ExpiredAt            sql.NullTime `json:"expired_at"`
	CreatedAt            time.Time    `json:"created_at"`
}

// CardTemplate represents card print design templates
type CardTemplate struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	IsDefault     bool      `json:"is_default"`
	HeaderTitle   string    `json:"header_title"`
	FooterText    string    `json:"footer_text"`
	ThemeColor    string    `json:"theme_color"`
	HTMLContent   string    `json:"html_content"`
	CSSContent    string    `json:"css_content"`
	BgImage       string    `json:"bg_image"`
	CardWidth     string    `json:"card_width"`
	CardHeight    string    `json:"card_height"`
	Cols          int       `json:"cols"`
	Rows          int       `json:"rows"`
	CardGap       string    `json:"card_gap"`
	OverlayConfig string    `json:"overlay_config"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedAtStr  string    `json:"created_at_str"`

	// Legacy fields for backward compatibility
	BackgroundImage string  `json:"background_image,omitempty"`
	BackgroundColor string  `json:"background_color,omitempty"`
	WidthMM         float64 `json:"width_mm,omitempty"`
	HeightMM        float64 `json:"height_mm,omitempty"`
	ShowQR          bool    `json:"show_qr,omitempty"`
	ShowBarcode     bool    `json:"show_barcode,omitempty"`
	ShowPrice       bool    `json:"show_price,omitempty"`
	ShowProfile     bool    `json:"show_profile,omitempty"`
	ShowExpiry      bool    `json:"show_expiry,omitempty"`
}

// SSTPSettings represents the Accel-PPP SSTP VPN Server configuration
type SSTPSettings struct {
	ID        int64     `json:"id"`
	Enabled   bool      `json:"enabled"`
	Port      int       `json:"port"`
	GatewayIP string    `json:"gateway_ip"`
	IPPool    string    `json:"ip_pool"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SSTPClient represents a MikroTik router connected to the server via SSTP VPN
type SSTPClient struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	Username   string       `json:"username"`
	Password   string       `json:"password"`
	AssignedIP string       `json:"assigned_ip"`
	Notes      string       `json:"notes"`
	Enabled    bool         `json:"enabled"`
	IsOnline   bool         `json:"is_online"`
	LastSeen   sql.NullTime `json:"last_seen"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

// SSTPStaticRoute represents a static IP route in Linux kernel for remote branch subnets
type SSTPStaticRoute struct {
	ID              int64     `json:"id"`
	DestinationCIDR string    `json:"destination_cidr"`
	GatewayIP       string    `json:"gateway_ip"`
	ClientID        int64     `json:"client_id"`
	ClientName      string    `json:"client_name"`
	Description     string    `json:"description"`
	Enabled         bool      `json:"enabled"`
	IsApplied       bool      `json:"is_applied"`
	ActiveDev       string    `json:"active_dev"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// NAS represents MikroTik router clients (FreeRADIUS `nas` table)
type NAS struct {
	ID          int64     `json:"id"`
	NASName     string    `json:"nasname"` // Router IP address
	ShortName   string    `json:"shortname"`
	Type        string    `json:"type"` // "other" / "mikrotik"
	Ports       int       `json:"ports"`
	Secret      string    `json:"secret"`
	Server      string    `json:"server"`
	Community   string    `json:"community"`
	Description string    `json:"description"`
	CoAPort     int       `json:"coa_port"`
	APIPort     int       `json:"api_port"`
	APIUser     string    `json:"api_user"`
	APIPassword string    `json:"api_password"`
	Phone       string    `json:"phone"`   // Cafe owner WhatsApp phone
	IsCafe      bool      `json:"is_cafe"` // true if branch is a Cafe
	CreatedAt   time.Time `json:"created_at"`
}

// RadAcct represents FreeRADIUS live and historical accounting sessions
type RadAcct struct {
	RadAcctID          int64        `json:"radacctid"`
	AcctSessionID      string       `json:"acctsessionid"`
	AcctUniqueID       string       `json:"acctuniqueid"`
	UserName           string       `json:"username"`
	Realm              string       `json:"realm"`
	NASIPAddress       string       `json:"nasipaddress"`
	NASPortID          string       `json:"nasportid"`
	NASPortType        string       `json:"nasporttype"`
	AcctStartTime      time.Time    `json:"acctstarttime"`
	AcctUpdateTime     sql.NullTime `json:"acctupdatetime"`
	AcctStopTime       sql.NullTime `json:"acctstoptime"`
	AcctSessionTime    int64        `json:"acctsessiontime"`
	AcctAuthentic      string       `json:"acctauthentic"`
	AcctInputOctets    int64        `json:"acctinputoctets"`  // Download
	AcctOutputOctets   int64        `json:"acctoutputoctets"` // Upload
	CalledStationID    string       `json:"calledstationid"`
	CallingStationID   string       `json:"callingstationid"`
	AcctTerminateCause string       `json:"acctterminatecause"`
	FramedIPAddress    string       `json:"framedipaddress"`
}

// DashboardMetrics holds aggregated dashboard stats
type DashboardMetrics struct {
	TotalSubscribers int64   `json:"total_subscribers"`
	ActiveCards      int64   `json:"active_cards"`
	OnlineSessions   int64   `json:"online_sessions"`
	TotalResellers   int64   `json:"total_resellers"`
	TotalRouters     int64   `json:"total_routers"`
	TotalDownload    int64   `json:"total_download"`
	TotalUpload      int64   `json:"total_upload"`
	TotalRevenue     float64 `json:"total_revenue"`
}

// Subscriber represents an ISP subscriber with CRM and account details
type Subscriber struct {
	ID             int64        `json:"id"`
	Username       string       `json:"username"`
	Password       string       `json:"password,omitempty"`
	ContractNo     string       `json:"contract_no"`
	FullName       string       `json:"full_name"`
	Phone          string       `json:"phone"`
	Address        string       `json:"address"`
	Debt           float64      `json:"debt"`
	ProfileName    string       `json:"profile_name"`
	StaticIP       string       `json:"static_ip"`
	CallingMAC     string       `json:"calling_mac"`
	QuotaBytes     int64        `json:"quota_bytes"` // in bytes (0 = unlimited)
	ExpirationDate sql.NullTime `json:"expiration_date"`
	Status         string       `json:"status"` // "active", "disabled", "expired"
	ResellerID           int64        `json:"reseller_id"`
	ResellerName         string       `json:"reseller_name,omitempty"`
	LoanActive           bool         `json:"loan_active"`
	LoanGrantedAt        sql.NullTime `json:"loan_granted_at"`
	LoanHours            int          `json:"loan_hours"`
	LoanQuotaBytes       int64        `json:"loan_quota_bytes"`
	LoanDeductionType    string       `json:"loan_deduction_type"`
	LoanInitialUsedBytes int64        `json:"loan_initial_used_bytes"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// BillingTransaction represents a financial or balance operation (renew, quota, payment, debt)
type BillingTransaction struct {
	ID            int64     `json:"id"`
	Username      string    `json:"username"`
	ContractNo    string    `json:"contract_no"`
	FullName      string    `json:"full_name"`
	Type          string    `json:"type"`           // "renew", "quota", "payment", "adjustment"
	Description   string    `json:"description"`
	Amount        float64   `json:"amount"`
	PaymentStatus string    `json:"payment_status"` // "paid", "unpaid"
	PaymentMethod string    `json:"payment_method"` // "cash", "vodafone_cash", "instapay", "bank"
	Notes         string    `json:"notes"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

// BillingSummary represents financial metrics for reporting
type BillingSummary struct {
	TotalTransactions int64   `json:"total_transactions"`
	TotalSales        float64 `json:"total_sales"`
	TotalPaid         float64 `json:"total_paid"`
	TotalUnpaidDebt   float64 `json:"total_unpaid_debt"`
	TotalCustomerDebt float64 `json:"total_customer_debt"`
}

// WhatsAppSettings holds configuration for WhatsApp notifications and gateways
type WhatsAppSettings struct {
	ID                    int64     `json:"id"`
	Enabled               bool      `json:"enabled"`
	AdminPhone            string    `json:"admin_phone"`
	APIEndpoint           string    `json:"api_endpoint"`
	APIToken              string    `json:"api_token"`
	InstanceID            string    `json:"instance_id"`
	NotifyOnNewSubscriber bool      `json:"notify_on_new_subscriber"`
	NotifyOnExpiry        bool      `json:"notify_on_expiry"`
	NotifyOnPayment       bool      `json:"notify_on_payment"`
	DebtReminderEnabled   bool      `json:"debt_reminder_enabled"`
	DebtReminderDays      int       `json:"debt_reminder_days"`
	DebtReminderMinDebt   float64   `json:"debt_reminder_min_debt"`
	DebtReminderLastRun   time.Time `json:"debt_reminder_last_run"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// WhatsAppTemplate holds customizable notification messages for events
type WhatsAppTemplate struct {
	ID           int64     `json:"id"`
	EventKey     string    `json:"event_key"`
	EventName    string    `json:"event_name"`
	Enabled      bool      `json:"enabled"`
	TemplateText string    `json:"template_text"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// WhatsAppLog holds sent/received message logs
type WhatsAppLog struct {
	ID            int64     `json:"id"`
	Phone         string    `json:"phone"`
	RecipientName string    `json:"recipient_name"`
	MessageType   string    `json:"message_type"`
	Content       string    `json:"content"`
	Status        string    `json:"status"` // "sent", "failed"
	ErrorMessage  string    `json:"error_message"`
	CreatedAt     time.Time `json:"created_at"`
}

// SpeedControlConfig holds system-wide speed boosting and scheduling configuration
type SpeedControlConfig struct {
	ID                 int64      `json:"id"`
	Mode               string     `json:"mode"`               // "off", "permanent", "temp", "recurring"
	BoostType          string     `json:"boost_type"`         // "multiply", "add_fixed"
	BoostValue         float64    `json:"boost_value"`        // e.g. 2.0 (for 2x) or 5 (for +5M)
	TempHours          int        `json:"temp_hours"`         // temporary boost duration in hours
	TempExpiresAt      *time.Time `json:"temp_expires_at"`    // expiration timestamp
	RecurringEnabled   bool       `json:"recurring_enabled"`  // daily happy hours enabled
	RecurringStartTime string     `json:"recurring_start_time"` // e.g. "02:00"
	RecurringEndTime   string     `json:"recurring_end_time"`   // e.g. "08:00"
	DisconnectUsers    bool       `json:"disconnect_users"`   // whether to kick active sessions to apply speed immediately
	IsCurrentlyBoosted bool       `json:"is_currently_boosted"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// ProfileBaseSpeed tracks original rate limits before boosting
type ProfileBaseSpeed struct {
	ProfileID        int64     `json:"profile_id"`
	ProfileName      string    `json:"profile_name"`
	DisplayName      string    `json:"display_name"`
	BaseRateLimit    string    `json:"base_rate_limit"`
	BoostedRateLimit string    `json:"boosted_rate_limit"`
	CurrentRateLimit string    `json:"current_rate_limit"`
	IsBoosted        bool      `json:"is_boosted"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SubscriberLiveTraffic represents real-time telemetry, bandwidth speeds, and session statistics for a subscriber
type SubscriberLiveTraffic struct {
	Username               string  `json:"username"`
	FullName               string  `json:"full_name"`
	ContractNo             string  `json:"contract_no"`
	Phone                  string  `json:"phone"`
	Address                string  `json:"address"`
	ProfileName            string  `json:"profile_name"`
	RateLimit              string  `json:"rate_limit"`
	MaxDownloadBps         int64   `json:"max_download_bps"`
	MaxUploadBps           int64   `json:"max_upload_bps"`
	MaxDownloadDisplay     string  `json:"max_download_display"`
	MaxUploadDisplay       string  `json:"max_upload_display"`
	Status                 string  `json:"status"` // "online", "offline"
	IsOnline               bool    `json:"is_online"`
	StatusBadgeClass       string  `json:"status_badge_class"`
	StatusText             string  `json:"status_text"`
	SessionID              string  `json:"session_id"`
	NASIP                  string  `json:"nas_ip"`
	NASName                string  `json:"nas_name"`
	FramedIP               string  `json:"framed_ip"`
	CallingMAC             string  `json:"calling_mac"`
	StartTime              string  `json:"start_time"`
	Uptime                 string  `json:"uptime"`
	SessionTimeSec         int64   `json:"session_time_sec"`
	// Real-time calculated speeds
	CurrentDownloadBps     int64   `json:"current_download_bps"`
	CurrentUploadBps       int64   `json:"current_upload_bps"`
	DownloadSpeedDisplay   string  `json:"download_speed_display"`
	UploadSpeedDisplay     string  `json:"upload_speed_display"`
	DownloadPercent        float64 `json:"download_percent"`
	UploadPercent          float64 `json:"upload_percent"`
	// Current session bytes
	SessionDownloadBytes   int64   `json:"session_download_bytes"`
	SessionUploadBytes     int64   `json:"session_upload_bytes"`
	SessionTotalBytes      int64   `json:"session_total_bytes"`
	SessionDownloadDisplay string  `json:"session_download_display"`
	SessionUploadDisplay   string  `json:"session_upload_display"`
	SessionTotalDisplay    string  `json:"session_total_display"`
	// All-time / monthly totals
	TotalDownloadBytes     int64   `json:"total_download_bytes"`
	TotalUploadBytes       int64   `json:"total_upload_bytes"`
	TotalBytes             int64   `json:"total_bytes"`
	TotalBytesDisplay      string  `json:"total_bytes_display"`
	// Quota & Expiry
	QuotaBytes             int64   `json:"quota_bytes"`
	QuotaDisplay           string  `json:"quota_display"`
	QuotaRemainingBytes    int64   `json:"quota_remaining_bytes"`
	QuotaRemainingDisplay  string  `json:"quota_remaining_display"`
	QuotaUsagePercent      float64 `json:"quota_usage_percent"`
	ExpirationDate         string  `json:"expiration_date"`
	LastUpdate             string  `json:"last_update"`
	DataSource             string  `json:"data_source"`
	IsAPI                  bool    `json:"is_api"`
}

// SubscriberTrafficOption provides lightweight subscriber list items for search/dropdown
type SubscriberTrafficOption struct {
	Username   string `json:"username"`
	FullName   string `json:"full_name"`
	ContractNo string `json:"contract_no"`
	Profile    string `json:"profile"`
	IsOnline   bool   `json:"is_online"`
	LiveIP     string `json:"live_ip"`
}

// SubscriberLoginSession represents detailed login/session records for reporting
type SubscriberLoginSession struct {
	RadAcctID          int64      `json:"radacctid"`
	AcctSessionID      string     `json:"acctsessionid"`
	UserName           string     `json:"username"`
	FullName           string     `json:"full_name"`
	ContractNo         string     `json:"contract_no"`
	Phone              string     `json:"phone"`
	ProfileName        string     `json:"profile_name"`
	NASIPAddress       string     `json:"nasipaddress"`
	NASName            string     `json:"nas_name"`
	FramedIPAddress    string     `json:"framedipaddress"`
	CallingStationID   string     `json:"callingstationid"` // Client MAC address
	AcctStartTime      time.Time  `json:"acctstarttime"`
	StartTimeFormatted string     `json:"start_time_formatted"`
	AcctStopTime       *time.Time `json:"acctstoptime"`
	StopTimeFormatted  string     `json:"stop_time_formatted"`
	AcctSessionTime    int64      `json:"acctsessiontime"` // duration in seconds
	DurationFormatted  string     `json:"duration_formatted"`
	AcctInputOctets    int64      `json:"acctinputoctets"`  // Upload bytes
	AcctOutputOctets   int64      `json:"acctoutputoctets"` // Download bytes
	TotalBytes         int64      `json:"total_bytes"`
	UploadFormatted    string     `json:"upload_formatted"`
	DownloadFormatted  string     `json:"download_formatted"`
	TotalBytesFormatted string    `json:"total_bytes_formatted"`
	IsOnline           bool       `json:"is_online"`
	StatusBadgeClass   string     `json:"status_badge_class"`
	StatusText         string     `json:"status_text"`
	AcctTerminateCause string     `json:"acctterminatecause"`
	TerminateCauseText string     `json:"terminate_cause_text"`
}

// SubscriberLoginSummary aggregates statistics for login session reports
type SubscriberLoginSummary struct {
	TotalSessions        int64  `json:"total_sessions"`
	OnlineSessions       int64  `json:"online_sessions"`
	ClosedSessions       int64  `json:"closed_sessions"`
	UniqueUsersCount     int64  `json:"unique_users_count"`
	TotalDownloadBytes   int64  `json:"total_download_bytes"`
	TotalUploadBytes     int64  `json:"total_upload_bytes"`
	TotalTrafficBytes    int64  `json:"total_traffic_bytes"`
	TotalDownloadDisplay string `json:"total_download_display"`
	TotalUploadDisplay   string `json:"total_upload_display"`
	TotalTrafficDisplay  string `json:"total_traffic_display"`
	AvgDurationSeconds   int64  `json:"avg_duration_seconds"`
	AvgDurationDisplay   string `json:"avg_duration_display"`
}

// BackupSettings represents automatic backup configuration
type BackupSettings struct {
	ID             int        `json:"id"`
	Enabled        bool       `json:"enabled"`
	IntervalHours  int        `json:"interval_hours"`
	RetentionCount int        `json:"retention_count"`
	SendWhatsApp   bool       `json:"send_whatsapp"`
	WhatsAppPhone  string     `json:"whatsapp_phone"`
	LastBackupAt   *time.Time `json:"last_backup_at"`
	LastStatus     string     `json:"last_status"`
	LastError      string     `json:"last_error"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// BackupRecord represents an individual backup file record
type BackupRecord struct {
	ID                 int64      `json:"id"`
	Filename           string     `json:"filename"`
	FilePath           string     `json:"filepath"`
	SizeBytes          int64      `json:"size_bytes"`
	SizeFormatted      string     `json:"size_formatted"`
	CreatedAt          time.Time  `json:"created_at"`
	CreatedAtFormatted string     `json:"created_at_formatted"`
	Status             string     `json:"status"`
	WhatsAppSent       bool       `json:"whatsapp_sent"`
	ErrorMessage       string     `json:"error_message"`
}

// SubscriberPortalSettings represents settings and permissions for the subscriber self-care portal
type SubscriberPortalSettings struct {
	ID                     int       `json:"id"`
	PortalEnabled          bool      `json:"portal_enabled"`
	AllowLoanRequest       bool      `json:"allow_loan_request"`
	ShowQuotaUsage         bool      `json:"show_quota_usage"`
	ShowDebt               bool      `json:"show_debt"`
	ShowProfileDetails     bool      `json:"show_profile_details"`
	ShowOnlineStatus       bool      `json:"show_online_status"`
	AllowPasswordChange    bool      `json:"allow_password_change"`
	AllowPhoneLogin        bool      `json:"allow_phone_login"`
	AllowIPAutoLogin       bool      `json:"allow_ip_auto_login"` // الدخول التلقائي بعنوان الآيبي (Auto Login by IP)
	AllowSessionDisconnect bool      `json:"allow_session_disconnect"`
	CompanyName            string    `json:"company_name"`
	CompanyPhone           string    `json:"company_phone"`
	PortalAnnouncement     string    `json:"portal_announcement"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// SubscriberPortalView contains all aggregated details needed to render the subscriber self-care portal
type SubscriberPortalView struct {
	Subscriber           Subscriber               `json:"subscriber"`
	Profile              Profile                  `json:"profile"`
	Settings             SubscriberPortalSettings `json:"settings"`
	IsOnline             bool                     `json:"is_online"`
	OnlineIP             string                   `json:"online_ip"`
	OnlineMAC            string                   `json:"online_mac"`
	OnlineDuration       string                   `json:"online_duration"`
	NasIP                string                   `json:"nas_ip"`
	QuotaGB              float64                  `json:"quota_gb"`
	UsedBytes            int64                    `json:"used_bytes"`
	UsedGB               float64                  `json:"used_gb"`
	RemainingBytes       int64                    `json:"remaining_bytes"`
	RemainingGB          float64                  `json:"remaining_gb"`
	UsagePercent         float64                  `json:"usage_percent"`
	ExpirationDisplay    string                   `json:"expiration_display"`
	RemainingDaysDisplay string                   `json:"remaining_days_display"`
	IsTimeExpired        bool                     `json:"is_time_expired"`
	IsQuotaExpired       bool                     `json:"is_quota_expired"`
	CanRequestLoan       bool                     `json:"can_request_loan"`
	LoanActive           bool                     `json:"loan_active"`
	LoanHours            int                      `json:"loan_hours"`
	LoanQuotaGB          float64                  `json:"loan_quota_gb"`
	LoanDeductionType    string                   `json:"loan_deduction_type"`
	LoanGrantedAt        string                   `json:"loan_granted_at"`
}



