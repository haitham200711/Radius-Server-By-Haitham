package models

import (
	"database/sql"
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

// Reseller represents commercial dealers/agents
type Reseller struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Phone        string    `json:"phone"`
	Balance      float64   `json:"balance"`
	CreditLimit  float64   `json:"credit_limit"`
	Notes        string    `json:"notes"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
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
	Price               float64   `json:"price"`
	SharedUsers         int       `json:"shared_users"`
	MikrotikAddressList string    `json:"mikrotik_address_list"`
	OnQuotaAction       string    `json:"on_quota_action"`  // "disconnect", "profile", "pool"
	OnQuotaProfile      string    `json:"on_quota_profile"` // fallback profile
	OnQuotaPool         string    `json:"on_quota_pool"`    // fallback IP pool
	OnTimeAction        string    `json:"on_time_action"`   // "disconnect", "profile", "pool"
	OnTimeProfile       string    `json:"on_time_profile"`  // fallback profile
	OnTimePool          string    `json:"on_time_pool"`     // fallback IP pool
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
	ID           int64     `json:"id"`
	BatchCode    string    `json:"batch_code"`
	ProfileID    int64     `json:"profile_id"`
	ProfileName  string    `json:"profile_name"`
	ResellerID   int64     `json:"reseller_id"` // 0 = admin
	ResellerName string    `json:"reseller_name"`
	Count        int       `json:"count"`
	PricePerCard float64   `json:"price_per_card"`
	TotalPrice   float64   `json:"total_price"`
	CreatedAt    time.Time `json:"created_at"`
}

// Card represents an individual hotspot / voucher card
type Card struct {
	ID           int64        `json:"id"`
	BatchID      int64        `json:"batch_id"`
	Username     string       `json:"username"`
	Password     string       `json:"password"`
	ProfileID    int64        `json:"profile_id"`
	ProfileName  string       `json:"profile_name"`
	ResellerID   int64        `json:"reseller_id"`
	Status       string       `json:"status"` // "available", "used", "expired", "locked"
	Price        float64      `json:"price"`
	FirstLoginAt sql.NullTime `json:"first_login_at"`
	ExpiredAt    sql.NullTime `json:"expired_at"`
	CreatedAt    time.Time    `json:"created_at"`
}

// CardTemplate represents card print design templates
type CardTemplate struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	BackgroundImage string    `json:"background_image"`
	BackgroundColor string    `json:"background_color"`
	WidthMM         float64   `json:"width_mm"`
	HeightMM        float64   `json:"height_mm"`
	ShowQR          bool      `json:"show_qr"`
	ShowBarcode     bool      `json:"show_barcode"`
	ShowPrice       bool      `json:"show_price"`
	ShowProfile     bool      `json:"show_profile"`
	ShowExpiry      bool      `json:"show_expiry"`
	IsDefault       bool      `json:"is_default"`
	CreatedAt       time.Time `json:"created_at"`
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
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

