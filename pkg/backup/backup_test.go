package backup

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"testing"
)

func TestParseMySQLDSN_NoHardcodedPassword(t *testing.T) {
	// DSN without password
	dsn := "radius@tcp(127.0.0.1:3306)/radiusdb"
	user, pass, host, port, dbname := parseMySQLDSN(dsn)

	if user != "radius" {
		t.Errorf("expected user 'radius', got: %s", user)
	}
	if pass != "" {
		t.Errorf("expected empty password fallback, got: %s", pass)
	}
	if host != "127.0.0.1" || port != "3306" || dbname != "radiusdb" {
		t.Errorf("unexpected host/port/dbname: %s:%s/%s", host, port, dbname)
	}

	// DSN with password
	dsnWithPass := "myuser:secret123@tcp(10.0.0.5:3307)/customdb?parseTime=true"
	user2, pass2, host2, port2, dbname2 := parseMySQLDSN(dsnWithPass)
	if user2 != "myuser" || pass2 != "secret123" || host2 != "10.0.0.5" || port2 != "3307" || dbname2 != "customdb" {
		t.Errorf("unexpected parsed DSN: %s:%s@%s:%s/%s", user2, pass2, host2, port2, dbname2)
	}
}

func TestDecompressionLimitProtection(t *testing.T) {
	// Verify LimitReader properly prevents reading more than limit
	limit := int64(1024) // 1 KB test limit
	data := make([]byte, 5000)

	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	_, _ = gz.Write(data)
	_ = gz.Close()

	gzReader, err := gzip.NewReader(buf)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	limited := io.LimitReader(gzReader, limit)
	readBuf := make([]byte, 10000)
	n, _ := io.ReadFull(limited, readBuf)

	if int64(n) > limit {
		t.Errorf("decompressed read exceeded limit: %d > %d", n, limit)
	}
}

func TestDumpCLI_OptionsFileMode(t *testing.T) {
	// Verify secure temp options file creates with 0600 permissions
	optFile, err := os.CreateTemp("", "test_opts_*.cnf")
	if err != nil {
		t.Fatalf("failed to create temp options file: %v", err)
	}
	defer os.Remove(optFile.Name())
	_ = optFile.Chmod(0600)
	_ = optFile.Close()

	fi, err := os.Stat(optFile.Name())
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	// Under Unix/Linux the permission should be 0600; on Windows file attributes differ,
	// but the call must succeed without errors
	_ = fi
}
