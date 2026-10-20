package config

import (
	"os"
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

	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		secret = "haitham-radius-enterprise-secret-key-2026"
	}

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
