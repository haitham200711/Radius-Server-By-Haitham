package config

import (
	"os"
)

type Config struct {
	WebAddr       string
	DBDriver      string // "mysql" or "sqlite"
	DBDSN         string
	SessionSecret string
	FreeRadiusDir string
}

func LoadConfig() *Config {
	webAddr := os.Getenv("WEB_ADDR")
	if webAddr == "" {
		webAddr = ":5000" // Matching default in mikrotik_cards_system
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
		WebAddr:       webAddr,
		DBDriver:      dbDriver,
		DBDSN:         dbDSN,
		SessionSecret: secret,
		FreeRadiusDir: "/etc/freeradius/3.0",
	}
}
