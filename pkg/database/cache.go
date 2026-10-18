package database

import (
	"sync"
	"time"
)

// CachedUser represents user info optimized for in-memory auth checks
type CachedUser struct {
	ID             int64
	Username       string
	Password       string
	ProfileID      int64
	RateLimit      string
	PoolName       string
	SessionTimeout int
	IdleTimeout    int
	AddressList    string
	IPAddress      string
	MACAddress     string
	Status         string
	ExpirationDate *time.Time
	DataLimitBytes int64
	UsedDataBytes  int64
}

// MemoryCache manages ultra-fast in-memory lookup for RADIUS authentication
type MemoryCache struct {
	mu          sync.RWMutex
	users       map[string]*CachedUser // key: username
	nasMap      map[string]*NAS        // key: ip
	profilesMap map[int64]*Profile     // key: profile id
}

// NewMemoryCache creates a new cache instance
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		users:       make(map[string]*CachedUser, 250000), // Pre-allocated for >200k users
		nasMap:      make(map[string]*NAS),
		profilesMap: make(map[int64]*Profile),
	}
}

// SetUser puts or updates a user in memory
func (c *MemoryCache) SetUser(u *CachedUser) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.users[u.Username] = u
}

// GetUser retrieves user by username in nanoseconds
func (c *MemoryCache) GetUser(username string) (*CachedUser, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	u, found := c.users[username]
	if !found {
		return nil, false
	}
	// Return a copy to ensure concurrency safety
	val := *u
	return &val, true
}

// DeleteUser removes a user from memory
func (c *MemoryCache) DeleteUser(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.users, username)
}

// SetNAS stores NAS client info
func (c *MemoryCache) SetNAS(nas *NAS) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nasMap[nas.IP] = nas
}

// GetNAS retrieves NAS by IP address
func (c *MemoryCache) GetNAS(ip string) (*NAS, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	nas, found := c.nasMap[ip]
	if !found {
		return nil, false
	}
	val := *nas
	return &val, true
}

// DeleteNAS removes NAS by IP
func (c *MemoryCache) DeleteNAS(ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.nasMap, ip)
}

// SetProfile stores a service profile
func (c *MemoryCache) SetProfile(p *Profile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profilesMap[p.ID] = p

	// Update all cached users with this profile
	for _, u := range c.users {
		if u.ProfileID == p.ID {
			u.RateLimit = p.RateLimit
			u.PoolName = p.PoolName
			u.SessionTimeout = p.SessionTimeout
			u.IdleTimeout = p.IdleTimeout
			u.AddressList = p.MikrotikAddressList
		}
	}
}

// GetProfile retrieves profile by ID
func (c *MemoryCache) GetProfile(id int64) (*Profile, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, found := c.profilesMap[id]
	if !found {
		return nil, false
	}
	val := *p
	return &val, true
}

// UpdateUserTraffic updates used traffic in cache
func (c *MemoryCache) UpdateUserTraffic(username string, additionalBytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u, found := c.users[username]; found {
		u.UsedDataBytes += additionalBytes
	}
}

// CountUsers returns number of users currently in cache
func (c *MemoryCache) CountUsers() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.users)
}
