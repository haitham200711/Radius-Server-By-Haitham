package database

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps sql.DB with caching and high-performance methods
type DB struct {
	db        *sql.DB
	Cache     *MemoryCache
	filePath  string
	startTime time.Time
	mu        sync.Mutex
}

// Open initializes SQLite database with high-performance WAL mode
func Open(filePath string) (*DB, error) {
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)&_pragma=temp_store(MEMORY)", filePath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(1 * time.Hour)

	d := &DB{
		db:        db,
		Cache:     NewMemoryCache(),
		filePath:  filePath,
		startTime: time.Now(),
	}

	if err := d.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	if err := d.loadCache(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to populate cache: %w", err)
	}

	return d, nil
}

// Close closes database
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS nas (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			ip TEXT UNIQUE NOT NULL,
			secret TEXT NOT NULL,
			description TEXT DEFAULT '',
			coa_port INTEGER DEFAULT 3799,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			rate_limit TEXT DEFAULT '',
			pool_name TEXT DEFAULT '',
			session_timeout INTEGER DEFAULT 0,
			idle_timeout INTEGER DEFAULT 0,
			shared_users INTEGER DEFAULT 1,
			mikrotik_address_list TEXT DEFAULT '',
			price REAL DEFAULT 0.0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password TEXT NOT NULL,
			profile_id INTEGER DEFAULT 0,
			ip_address TEXT DEFAULT '',
			mac_address TEXT DEFAULT '',
			status TEXT DEFAULT 'active',
			expiration_date DATETIME,
			data_limit_bytes INTEGER DEFAULT 0,
			used_data_bytes INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (profile_id) REFERENCES profiles(id)
		);`,
		`CREATE TABLE IF NOT EXISTS radacct (
			radacctid INTEGER PRIMARY KEY AUTOINCREMENT,
			acctsessionid TEXT NOT NULL,
			acctuniqueid TEXT NOT NULL,
			username TEXT NOT NULL,
			realm TEXT DEFAULT '',
			nasipaddress TEXT NOT NULL,
			nasportid TEXT DEFAULT '',
			nasporttype TEXT DEFAULT '',
			acctstarttime DATETIME NOT NULL,
			acctupdatetime DATETIME,
			acctstoptime DATETIME,
			acctsessiontime INTEGER DEFAULT 0,
			acctauthentic TEXT DEFAULT '',
			acctinputoctets INTEGER DEFAULT 0,
			acctoutputoctets INTEGER DEFAULT 0,
			calledstationid TEXT DEFAULT '',
			callingstationid TEXT DEFAULT '',
			acctterminatecause TEXT DEFAULT '',
			framedipaddress TEXT DEFAULT ''
		);`,
		// High performance indexing
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);`,
		`CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);`,
		`CREATE INDEX IF NOT EXISTS idx_users_profile_id ON users(profile_id);`,
		`CREATE INDEX IF NOT EXISTS idx_nas_ip ON nas(ip);`,
		`CREATE INDEX IF NOT EXISTS idx_radacct_active ON radacct(acctstoptime, username);`,
		`CREATE INDEX IF NOT EXISTS idx_radacct_session ON radacct(acctsessionid, nasipaddress);`,
		`CREATE INDEX IF NOT EXISTS idx_radacct_user ON radacct(username);`,
	}

	for _, q := range queries {
		if _, err := d.db.Exec(q); err != nil {
			return fmt.Errorf("error executing schema statement: %w\nQuery: %s", err, q)
		}
	}

	// Seed default entries if empty
	var count int
	_ = d.db.QueryRow("SELECT COUNT(*) FROM profiles").Scan(&count)
	if count == 0 {
		_, _ = d.db.Exec(`INSERT INTO profiles (name, rate_limit, pool_name, session_timeout, idle_timeout, price) 
			VALUES ('Default-5M', '5M/10M', 'pppoe-pool', 86400, 3600, 150.0),
			       ('Speed-20M', '10M/20M', 'pppoe-pool', 86400, 3600, 250.0),
			       ('Hotspot-1GB', '2M/4M', 'hs-pool', 3600, 600, 10.0);`)
	}

	_ = d.db.QueryRow("SELECT COUNT(*) FROM nas").Scan(&count)
	if count == 0 {
		_, _ = d.db.Exec(`INSERT INTO nas (name, ip, secret, description, coa_port) 
			VALUES ('MikroTik-Main', '192.168.2.1', 'radius123', 'Default MikroTik Router', 3799),
			       ('Localhost-Test', '127.0.0.1', 'radius123', 'Local Test NAS', 3799);`)
	}

	_ = d.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count == 0 {
		_, _ = d.db.Exec(`INSERT INTO users (username, password, profile_id, status) 
			VALUES ('testuser', '123456', 1, 'active'),
			       ('adminuser', 'mikrotik2026', 2, 'active');`)
	}

	return nil
}

func (d *DB) loadCache() error {
	// 1. Load all NAS
	rows, err := d.db.Query("SELECT id, name, ip, secret, description, coa_port, created_at FROM nas")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var nas NAS
			if err := rows.Scan(&nas.ID, &nas.Name, &nas.IP, &nas.Secret, &nas.Description, &nas.CoAPort, &nas.CreatedAt); err == nil {
				d.Cache.SetNAS(&nas)
			}
		}
	}

	// 2. Load all Profiles
	pRows, err := d.db.Query("SELECT id, name, rate_limit, pool_name, session_timeout, idle_timeout, shared_users, mikrotik_address_list, price, created_at FROM profiles")
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var p Profile
			if err := pRows.Scan(&p.ID, &p.Name, &p.RateLimit, &p.PoolName, &p.SessionTimeout, &p.IdleTimeout, &p.SharedUsers, &p.MikrotikAddressList, &p.Price, &p.CreatedAt); err == nil {
				d.Cache.SetProfile(&p)
			}
		}
	}

	// 3. Load active Users into RAM cache for nanosecond authentication
	uRows, err := d.db.Query(`SELECT u.id, u.username, u.password, u.profile_id, u.ip_address, u.mac_address, 
		u.status, u.expiration_date, u.data_limit_bytes, u.used_data_bytes,
		COALESCE(p.rate_limit, ''), COALESCE(p.pool_name, ''), COALESCE(p.session_timeout, 0), COALESCE(p.idle_timeout, 0), COALESCE(p.mikrotik_address_list, '')
		FROM users u LEFT JOIN profiles p ON u.profile_id = p.id WHERE u.status = 'active'`)
	if err == nil {
		defer uRows.Close()
		for uRows.Next() {
			var u CachedUser
			if err := uRows.Scan(&u.ID, &u.Username, &u.Password, &u.ProfileID, &u.IPAddress, &u.MACAddress,
				&u.Status, &u.ExpirationDate, &u.DataLimitBytes, &u.UsedDataBytes,
				&u.RateLimit, &u.PoolName, &u.SessionTimeout, &u.IdleTimeout, &u.AddressList); err == nil {
				d.Cache.SetUser(&u)
			}
		}
	}

	return nil
}

// --- NAS Management ---

func (d *DB) GetAllNAS() ([]NAS, error) {
	rows, err := d.db.Query("SELECT id, name, ip, secret, description, coa_port, created_at FROM nas ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []NAS
	for rows.Next() {
		var n NAS
		if err := rows.Scan(&n.ID, &n.Name, &n.IP, &n.Secret, &n.Description, &n.CoAPort, &n.CreatedAt); err == nil {
			result = append(result, n)
		}
	}
	return result, nil
}

func (d *DB) SaveNAS(n *NAS) error {
	if n.CoAPort <= 0 {
		n.CoAPort = 3799
	}
	if n.ID == 0 {
		res, err := d.db.Exec("INSERT INTO nas (name, ip, secret, description, coa_port) VALUES (?, ?, ?, ?, ?)",
			n.Name, n.IP, n.Secret, n.Description, n.CoAPort)
		if err != nil {
			return err
		}
		n.ID, _ = res.LastInsertId()
		n.CreatedAt = time.Now()
	} else {
		_, err := d.db.Exec("UPDATE nas SET name=?, ip=?, secret=?, description=?, coa_port=? WHERE id=?",
			n.Name, n.IP, n.Secret, n.Description, n.CoAPort, n.ID)
		if err != nil {
			return err
		}
	}
	d.Cache.SetNAS(n)
	return nil
}

func (d *DB) DeleteNAS(id int64) error {
	var ip string
	_ = d.db.QueryRow("SELECT ip FROM nas WHERE id = ?", id).Scan(&ip)
	_, err := d.db.Exec("DELETE FROM nas WHERE id = ?", id)
	if err == nil && ip != "" {
		d.Cache.DeleteNAS(ip)
	}
	return err
}

// --- Profile Management ---

func (d *DB) GetAllProfiles() ([]Profile, error) {
	rows, err := d.db.Query("SELECT id, name, rate_limit, pool_name, session_timeout, idle_timeout, shared_users, mikrotik_address_list, price, created_at FROM profiles ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Profile
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.RateLimit, &p.PoolName, &p.SessionTimeout, &p.IdleTimeout, &p.SharedUsers, &p.MikrotikAddressList, &p.Price, &p.CreatedAt); err == nil {
			result = append(result, p)
		}
	}
	return result, nil
}

func (d *DB) SaveProfile(p *Profile) error {
	if p.SharedUsers <= 0 {
		p.SharedUsers = 1
	}
	if p.ID == 0 {
		res, err := d.db.Exec(`INSERT INTO profiles (name, rate_limit, pool_name, session_timeout, idle_timeout, shared_users, mikrotik_address_list, price)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			p.Name, p.RateLimit, p.PoolName, p.SessionTimeout, p.IdleTimeout, p.SharedUsers, p.MikrotikAddressList, p.Price)
		if err != nil {
			return err
		}
		p.ID, _ = res.LastInsertId()
		p.CreatedAt = time.Now()
	} else {
		_, err := d.db.Exec(`UPDATE profiles SET name=?, rate_limit=?, pool_name=?, session_timeout=?, idle_timeout=?, shared_users=?, mikrotik_address_list=?, price=? WHERE id=?`,
			p.Name, p.RateLimit, p.PoolName, p.SessionTimeout, p.IdleTimeout, p.SharedUsers, p.MikrotikAddressList, p.Price, p.ID)
		if err != nil {
			return err
		}
	}
	d.Cache.SetProfile(p)
	return nil
}

func (d *DB) DeleteProfile(id int64) error {
	_, err := d.db.Exec("DELETE FROM profiles WHERE id = ?", id)
	return err
}

// --- User Management (Optimized for 200,000+ Users) ---

type UserFilter struct {
	Search    string
	ProfileID int64
	Status    string
	Page      int
	PageSize  int
}

type UserListResult struct {
	Users      []User `json:"users"`
	TotalCount int64  `json:"total_count"`
	Page       int    `json:"page"`
	PageSize   int    `json:"page_size"`
}

func (d *DB) GetUsers(filter UserFilter) (*UserListResult, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 500 {
		filter.PageSize = 50
	}

	whereClauses := []string{"1=1"}
	var args []interface{}

	if filter.Search != "" {
		whereClauses = append(whereClauses, "(u.username LIKE ? OR u.ip_address LIKE ? OR u.mac_address LIKE ?)")
		searchTerm := "%" + filter.Search + "%"
		args = append(args, searchTerm, searchTerm, searchTerm)
	}
	if filter.ProfileID > 0 {
		whereClauses = append(whereClauses, "u.profile_id = ?")
		args = append(args, filter.ProfileID)
	}
	if filter.Status != "" {
		whereClauses = append(whereClauses, "u.status = ?")
		args = append(args, filter.Status)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM users u WHERE %s", whereSQL)
	var total int64
	if err := d.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, err
	}

	// Fetch page
	offset := (filter.Page - 1) * filter.PageSize
	query := fmt.Sprintf(`SELECT u.id, u.username, u.password, u.profile_id, COALESCE(p.name, ''), COALESCE(p.rate_limit, ''),
		u.ip_address, u.mac_address, u.status, u.expiration_date, u.data_limit_bytes, u.used_data_bytes, u.created_at, u.updated_at
		FROM users u LEFT JOIN profiles p ON u.profile_id = p.id
		WHERE %s ORDER BY u.id DESC LIMIT ? OFFSET ?`, whereSQL)

	queryArgs := append(args, filter.PageSize, offset)
	rows, err := d.db.Query(query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0, filter.PageSize)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Password, &u.ProfileID, &u.ProfileName, &u.RateLimit,
			&u.IPAddress, &u.MACAddress, &u.Status, &u.ExpirationDate, &u.DataLimitBytes, &u.UsedDataBytes,
			&u.CreatedAt, &u.UpdatedAt); err == nil {
			users = append(users, u)
		}
	}

	return &UserListResult{
		Users:      users,
		TotalCount: total,
		Page:       filter.Page,
		PageSize:   filter.PageSize,
	}, nil
}

func (d *DB) SaveUser(u *User) error {
	now := time.Now()
	if u.Status == "" {
		u.Status = "active"
	}

	if u.ID == 0 {
		res, err := d.db.Exec(`INSERT INTO users (username, password, profile_id, ip_address, mac_address, status, expiration_date, data_limit_bytes, used_data_bytes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			u.Username, u.Password, u.ProfileID, u.IPAddress, u.MACAddress, u.Status, u.ExpirationDate, u.DataLimitBytes, u.UsedDataBytes, now, now)
		if err != nil {
			return err
		}
		u.ID, _ = res.LastInsertId()
		u.CreatedAt = now
		u.UpdatedAt = now
	} else {
		_, err := d.db.Exec(`UPDATE users SET username=?, password=?, profile_id=?, ip_address=?, mac_address=?, status=?, expiration_date=?, data_limit_bytes=?, used_data_bytes=?, updated_at=? WHERE id=?`,
			u.Username, u.Password, u.ProfileID, u.IPAddress, u.MACAddress, u.Status, u.ExpirationDate, u.DataLimitBytes, u.UsedDataBytes, now, u.ID)
		if err != nil {
			return err
		}
		u.UpdatedAt = now
	}

	// Update cache
	if u.Status == "active" {
		prof, _ := d.Cache.GetProfile(u.ProfileID)
		cu := &CachedUser{
			ID:             u.ID,
			Username:       u.Username,
			Password:       u.Password,
			ProfileID:      u.ProfileID,
			IPAddress:      u.IPAddress,
			MACAddress:     u.MACAddress,
			Status:         u.Status,
			ExpirationDate: u.ExpirationDate,
			DataLimitBytes: u.DataLimitBytes,
			UsedDataBytes:  u.UsedDataBytes,
		}
		if prof != nil {
			cu.RateLimit = prof.RateLimit
			cu.PoolName = prof.PoolName
			cu.SessionTimeout = prof.SessionTimeout
			cu.IdleTimeout = prof.IdleTimeout
			cu.AddressList = prof.MikrotikAddressList
		}
		d.Cache.SetUser(cu)
	} else {
		d.Cache.DeleteUser(u.Username)
	}

	return nil
}

func (d *DB) DeleteUser(id int64) error {
	var username string
	_ = d.db.QueryRow("SELECT username FROM users WHERE id = ?", id).Scan(&username)
	_, err := d.db.Exec("DELETE FROM users WHERE id = ?", id)
	if err == nil && username != "" {
		d.Cache.DeleteUser(username)
	}
	return err
}

// BatchGenerateUsers creates thousands of voucher cards / users in high-speed transaction
func (d *DB) BatchGenerateUsers(count int, prefix string, userLen int, profileID int64, dataLimitGB int64, validityDays int) (int, error) {
	if count <= 0 {
		return 0, nil
	}
	if count > 50000 {
		count = 50000 // Safe batch chunk
	}

	tx, err := d.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO users (username, password, profile_id, status, expiration_date, data_limit_bytes, used_data_bytes, created_at, updated_at)
		VALUES (?, ?, ?, 'active', ?, ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now()
	var expDate *time.Time
	if validityDays > 0 {
		exp := now.AddDate(0, 0, validityDays)
		expDate = &exp
	}
	dataLimitBytes := dataLimitGB * 1024 * 1024 * 1024

	prof, _ := d.Cache.GetProfile(profileID)
	created := 0

	for i := 0; i < count; i++ {
		username := fmt.Sprintf("%s%s", prefix, randomString(userLen))
		password := randomString(6)

		_, err := stmt.Exec(username, password, profileID, expDate, dataLimitBytes)
		if err == nil {
			created++
			cu := &CachedUser{
				Username:       username,
				Password:       password,
				ProfileID:      profileID,
				Status:         "active",
				ExpirationDate: expDate,
				DataLimitBytes: dataLimitBytes,
			}
			if prof != nil {
				cu.RateLimit = prof.RateLimit
				cu.PoolName = prof.PoolName
				cu.SessionTimeout = prof.SessionTimeout
				cu.IdleTimeout = prof.IdleTimeout
				cu.AddressList = prof.MikrotikAddressList
			}
			d.Cache.SetUser(cu)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return created, nil
}

func randomString(n int) string {
	if n <= 0 {
		n = 6
	}
	const letters = "23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// --- RADIUS Accounting Session Handling ---

func (d *DB) RecordAcctStart(sessID, nasIP, username, framedIP, callingMAC string) error {
	now := time.Now()
	uniqueID := fmt.Sprintf("%s-%s-%s", nasIP, sessID, username)

	_, err := d.db.Exec(`INSERT INTO radacct (acctsessionid, acctuniqueid, username, nasipaddress, acctstarttime, acctupdatetime, callingstationid, framedipaddress)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessID, uniqueID, username, nasIP, now, now, callingMAC, framedIP)
	return err
}

func (d *DB) RecordAcctUpdate(sessID, nasIP, username string, sessionTime, inputOctets, outputOctets int64, framedIP string) error {
	now := time.Now()
	_, err := d.db.Exec(`UPDATE radacct SET acctupdatetime=?, acctsessiontime=?, acctinputoctets=?, acctoutputoctets=?, framedipaddress=CASE WHEN ? != '' THEN ? ELSE framedipaddress END
		WHERE acctsessionid=? AND nasipaddress=? AND acctstoptime IS NULL`,
		now, sessionTime, inputOctets, outputOctets, framedIP, framedIP, sessID, nasIP)

	// Update user's used data in cache and db
	totalBytes := inputOctets + outputOctets
	d.Cache.UpdateUserTraffic(username, totalBytes)
	return err
}

func (d *DB) RecordAcctStop(sessID, nasIP, username string, sessionTime, inputOctets, outputOctets int64, termCause string) error {
	now := time.Now()
	_, err := d.db.Exec(`UPDATE radacct SET acctstoptime=?, acctsessiontime=?, acctinputoctets=?, acctoutputoctets=?, acctterminatecause=?
		WHERE acctsessionid=? AND nasipaddress=? AND acctstoptime IS NULL`,
		now, sessionTime, inputOctets, outputOctets, termCause, sessID, nasIP)

	totalBytes := inputOctets + outputOctets
	_, _ = d.db.Exec("UPDATE users SET used_data_bytes = used_data_bytes + ? WHERE username = ?", totalBytes, username)
	return err
}

// GetActiveSessions returns current live sessions (acctstoptime IS NULL)
func (d *DB) GetActiveSessions() ([]RadAcct, error) {
	rows, err := d.db.Query(`SELECT radacctid, acctsessionid, acctuniqueid, username, nasipaddress, acctstarttime, acctupdatetime,
		acctsessiontime, acctinputoctets, acctoutputoctets, callingstationid, framedipaddress
		FROM radacct WHERE acctstoptime IS NULL ORDER BY acctstarttime DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []RadAcct
	for rows.Next() {
		var s RadAcct
		if err := rows.Scan(&s.RadAcctID, &s.AcctSessionID, &s.AcctUniqueID, &s.UserName, &s.NASIPAddress,
			&s.AcctStartTime, &s.AcctUpdateTime, &s.AcctSessionTime, &s.AcctInputOctets, &s.AcctOutputOctets,
			&s.CallingStationID, &s.FramedIPAddress); err == nil {
			sessions = append(sessions, s)
		}
	}
	return sessions, nil
}

// GetAcctLogs returns recent accounting logs
func (d *DB) GetAcctLogs(limit, offset int) ([]RadAcct, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int64
	_ = d.db.QueryRow("SELECT COUNT(*) FROM radacct").Scan(&total)

	rows, err := d.db.Query(`SELECT radacctid, acctsessionid, acctuniqueid, username, nasipaddress, acctstarttime, acctupdatetime, acctstoptime,
		acctsessiontime, acctinputoctets, acctoutputoctets, callingstationid, framedipaddress, acctterminatecause
		FROM radacct ORDER BY radacctid DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []RadAcct
	for rows.Next() {
		var r RadAcct
		if err := rows.Scan(&r.RadAcctID, &r.AcctSessionID, &r.AcctUniqueID, &r.UserName, &r.NASIPAddress,
			&r.AcctStartTime, &r.AcctUpdateTime, &r.AcctStopTime, &r.AcctSessionTime,
			&r.AcctInputOctets, &r.AcctOutputOctets, &r.CallingStationID, &r.FramedIPAddress, &r.AcctTerminateCause); err == nil {
			logs = append(logs, r)
		}
	}
	return logs, total, nil
}

// GetDashboardStats aggregates all essential system counters
func (d *DB) GetDashboardStats() (*DashboardStats, error) {
	stats := &DashboardStats{
		UptimeSeconds: int64(time.Since(d.startTime).Seconds()),
	}

	_ = d.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&stats.TotalUsers)
	_ = d.db.QueryRow("SELECT COUNT(*) FROM users WHERE status = 'active'").Scan(&stats.ActiveUsers)
	_ = d.db.QueryRow("SELECT COUNT(*) FROM users WHERE status = 'expired' OR (expiration_date IS NOT NULL AND expiration_date < CURRENT_TIMESTAMP)").Scan(&stats.ExpiredUsers)
	_ = d.db.QueryRow("SELECT COUNT(*) FROM radacct WHERE acctstoptime IS NULL").Scan(&stats.OnlineSessions)
	_ = d.db.QueryRow("SELECT COUNT(*) FROM nas").Scan(&stats.TotalNAS)
	_ = d.db.QueryRow("SELECT COUNT(*) FROM profiles").Scan(&stats.TotalProfiles)
	_ = d.db.QueryRow("SELECT COALESCE(SUM(acctinputoctets), 0), COALESCE(SUM(acctoutputoctets), 0) FROM radacct").Scan(&stats.TotalDownload, &stats.TotalUpload)

	// File size of SQLite DB
	if fi, err := os.Stat(d.filePath); err == nil {
		stats.DBSizeBytes = fi.Size()
	}

	return stats, nil
}
