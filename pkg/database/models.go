package database

import "time"

// NAS represents a Network Access Server (e.g. MikroTik Router)
type NAS struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	IP          string    `json:"ip"`
	Secret      string    `json:"secret"`
	Description string    `json:"description"`
	CoAPort     int       `json:"coa_port"`
	CreatedAt   time.Time `json:"created_at"`
}

// Profile represents a service package / plan
type Profile struct {
	ID                  int64     `json:"id"`
	Name                string    `json:"name"`
	RateLimit           string    `json:"rate_limit"` // e.g. "5M/10M" or "2048k/4096k"
	PoolName            string    `json:"pool_name"`  // IP pool on MikroTik
	SessionTimeout      int       `json:"session_timeout"`
	IdleTimeout         int       `json:"idle_timeout"`
	SharedUsers         int       `json:"shared_users"`
	MikrotikAddressList string    `json:"mikrotik_address_list"`
	Price               float64   `json:"price"`
	CreatedAt           time.Time `json:"created_at"`
}

// User represents an authentication subscriber (PPPoE / Hotspot)
type User struct {
	ID             int64      `json:"id"`
	Username       string     `json:"username"`
	Password       string     `json:"password"`
	ProfileID      int64      `json:"profile_id"`
	ProfileName    string     `json:"profile_name,omitempty"`
	RateLimit      string     `json:"rate_limit,omitempty"`
	IPAddress      string     `json:"ip_address"`  // Static IP if assigned
	MACAddress     string     `json:"mac_address"` // Bound MAC if set
	Status         string     `json:"status"`      // "active", "disabled", "expired"
	ExpirationDate *time.Time `json:"expiration_date"`
	DataLimitBytes int64      `json:"data_limit_bytes"` // 0 = unlimited
	UsedDataBytes  int64      `json:"used_data_bytes"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// RadAcct represents an accounting session record (RFC 2866)
type RadAcct struct {
	RadAcctID          int64      `json:"radacctid"`
	AcctSessionID      string     `json:"acctsessionid"`
	AcctUniqueID       string     `json:"acctuniqueid"`
	UserName           string     `json:"username"`
	Realm              string     `json:"realm"`
	NASIPAddress       string     `json:"nasipaddress"`
	NASPortID          string     `json:"nasportid"`
	NASPortType        string     `json:"nasporttype"`
	AcctStartTime      time.Time  `json:"acctstarttime"`
	AcctUpdateTime     *time.Time `json:"acctupdatetime"`
	AcctStopTime       *time.Time `json:"acctstoptime"`
	AcctSessionTime    int64      `json:"acctsessiontime"`
	AcctInputOctets    int64      `json:"acctinputoctets"`  // Download
	AcctOutputOctets   int64      `json:"acctoutputoctets"` // Upload
	CalledStationID    string     `json:"calledstationid"`
	CallingStationID   string     `json:"callingstationid"` // Client MAC
	AcctTerminateCause string     `json:"acctterminatecause"`
	FramedIPAddress    string     `json:"framedipaddress"`
}

// DashboardStats holds summary metrics
type DashboardStats struct {
	TotalUsers     int64   `json:"total_users"`
	ActiveUsers    int64   `json:"active_users"`
	ExpiredUsers   int64   `json:"expired_users"`
	OnlineSessions int64   `json:"online_sessions"`
	TotalNAS       int64   `json:"total_nas"`
	TotalProfiles  int64   `json:"total_profiles"`
	TotalDownload  int64   `json:"total_download"`
	TotalUpload    int64   `json:"total_upload"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
	DBSizeBytes    int64   `json:"db_size_bytes"`
}
