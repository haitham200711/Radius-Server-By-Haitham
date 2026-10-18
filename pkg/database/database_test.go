package database

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseAndCache(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "radius_db_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_radius.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// 1. Verify default seed data
	user, exists := db.Cache.GetUser("testuser")
	if !exists {
		t.Fatalf("Expected seeded user 'testuser' in cache")
	}
	if user.Password != "123456" {
		t.Errorf("Expected password '123456', got '%s'", user.Password)
	}

	nas, exists := db.Cache.GetNAS("127.0.0.1")
	if !exists {
		t.Fatalf("Expected seeded NAS '127.0.0.1' in cache")
	}
	if nas.Secret != "radius123" {
		t.Errorf("Expected NAS secret 'radius123', got '%s'", nas.Secret)
	}

	// 2. Test saving and caching new user
	exp := time.Now().Add(24 * time.Hour)
	newUser := &User{
		Username:       "haitham_vip",
		Password:       "secure999",
		ProfileID:      1,
		Status:         "active",
		ExpirationDate: &exp,
		DataLimitBytes: 10 * 1024 * 1024 * 1024,
	}

	if err := db.SaveUser(newUser); err != nil {
		t.Fatalf("SaveUser failed: %v", err)
	}

	cu, found := db.Cache.GetUser("haitham_vip")
	if !found {
		t.Fatalf("Expected 'haitham_vip' in memory cache")
	}
	if cu.RateLimit == "" {
		t.Errorf("Expected rate limit to be populated from profile")
	}

	// 3. Test Batch Generation (simulating high-volume voucher generation)
	count, err := db.BatchGenerateUsers(500, "VOUCH-", 6, 1, 5, 30)
	if err != nil {
		t.Fatalf("BatchGenerateUsers failed: %v", err)
	}
	if count != 500 {
		t.Errorf("Expected 500 created users, got %d", count)
	}

	// 4. Test Accounting records
	if err := db.RecordAcctStart("sess-1001", "127.0.0.1", "haitham_vip", "10.10.10.50", "00:11:22:33:44:55"); err != nil {
		t.Fatalf("RecordAcctStart failed: %v", err)
	}

	active, err := db.GetActiveSessions()
	if err != nil {
		t.Fatalf("GetActiveSessions failed: %v", err)
	}
	if len(active) == 0 {
		t.Fatalf("Expected at least 1 active session")
	}

	// Test Interim Update
	if err := db.RecordAcctUpdate("sess-1001", "127.0.0.1", "haitham_vip", 300, 1048576, 524288, "10.10.10.50"); err != nil {
		t.Fatalf("RecordAcctUpdate failed: %v", err)
	}

	// Test Stop
	if err := db.RecordAcctStop("sess-1001", "127.0.0.1", "haitham_vip", 600, 2097152, 1048576, "User-Request"); err != nil {
		t.Fatalf("RecordAcctStop failed: %v", err)
	}

	// Stats
	stats, err := db.GetDashboardStats()
	if err != nil {
		t.Fatalf("GetDashboardStats failed: %v", err)
	}
	if stats.TotalUsers < 500 {
		t.Errorf("Expected total users >= 500, got %d", stats.TotalUsers)
	}
}
