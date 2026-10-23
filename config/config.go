package config

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"strings"
)

type Config struct {
	WebAddr          string // Default ":443"
	HTTPRedirectAddr string // Default ":80"
	EnableHTTPS      bool   // Default true
	TLSCertFile      string // Default "/etc/ssl/certs/ssl-cert-snakeoil.pem"
	TLSKeyFile       string // Default "/etc/ssl/private/ssl-cert-snakeoil.key"
	DBDriver         string // "mysql" or "sqlite"
	DBDSN            string
	SessionSecret    string
	FreeRadiusDir    string
}

func getOrGenerateSessionSecret() string {
	secret := strings.TrimSpace(os.Getenv("SESSION_SECRET"))
	if secret != "" && secret != "haitham-radius-enterprise-secret-key-2026" {
		return secret
	}

	// Try reading from .env file
	envPaths := []string{"/opt/radius-server/.env", ".env"}
	for _, p := range envPaths {
		if data, err := os.ReadFile(p); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "SESSION_SECRET=") {
					val := strings.TrimPrefix(line, "SESSION_SECRET=")
					val = strings.Trim(val, `"' `)
					if val != "" && val != "haitham-radius-enterprise-secret-key-2026" {
						return val
					}
				}
			}
		}
	}

	// Generate secure random 256-bit (32 bytes) hex string
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Fatalf("Fatal: failed to generate secure session secret: %v", err)
	}
	newSecret := hex.EncodeToString(bytes)

	// Persist to .env file with 0600 permissions
	for _, p := range envPaths {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err == nil {
			_, _ = f.WriteString("\nSESSION_SECRET=" + newSecret + "\n")
			_ = f.Close()
			log.Printf("[Security] Generated and persisted new random SESSION_SECRET to %s", p)
			break
		}
	}

	return newSecret
}

func LoadConfig() *Config {
	webAddr := os.Getenv("WEB_ADDR")
	if webAddr == "" {
		webAddr = ":443"
	}

	httpRedirectAddr := os.Getenv("HTTP_REDIRECT_ADDR")
	if httpRedirectAddr == "" {
		httpRedirectAddr = ":80"
	}

	enableHTTPS := true
	if os.Getenv("ENABLE_HTTPS") == "false" || os.Getenv("ENABLE_HTTPS") == "0" {
		enableHTTPS = false
	}

	tlsCert := os.Getenv("TLS_CERT")
	if tlsCert == "" {
		tlsCert = "/etc/ssl/certs/ssl-cert-snakeoil.pem"
	}

	tlsKey := os.Getenv("TLS_KEY")
	if tlsKey == "" {
		tlsKey = "/etc/ssl/private/ssl-cert-snakeoil.key"
	}

	dbDriver := os.Getenv("DB_DRIVER")
	if dbDriver == "" {
		dbDriver = "mysql"
	}

	dbDSN := os.Getenv("DB_DSN")
	if dbDSN == "" {
		dbDSN = "radius:radiuspass@tcp(127.0.0.1:3306)/radiusdb?parseTime=true&charset=utf8mb4"
	}

	secret := getOrGenerateSessionSecret()

	return &Config{
		WebAddr:          webAddr,
		HTTPRedirectAddr: httpRedirectAddr,
		EnableHTTPS:      enableHTTPS,
		TLSCertFile:      tlsCert,
		TLSKeyFile:       tlsKey,
		DBDriver:         dbDriver,
		DBDSN:            dbDSN,
		SessionSecret:    secret,
		FreeRadiusDir:    "/etc/freeradius/3.0",
	}
}
