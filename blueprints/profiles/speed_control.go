package profiles

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
)

// StartSpeedControlDaemon starts a background ticker that checks temporary boost expirations
// and recurring daily happy hours windows every 30 seconds.
func (bp *Blueprint) StartSpeedControlDaemon() {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for range ticker.C {
			if err := bp.db.CheckSpeedControlSchedule(); err != nil {
				log.Printf("[SpeedControlDaemon] Check error: %v", err)
			}
		}
	}()
	log.Println("[SpeedControlDaemon] Background speed scheduler daemon started (30s interval)")
}

// SpeedControlHandler displays the Speed Control Dashboard
func (bp *Blueprint) SpeedControlHandler(w http.ResponseWriter, r *http.Request) {
	cfg, err := bp.db.GetSpeedControlConfig()
	if err != nil || cfg == nil {
		cfg = &models.SpeedControlConfig{
			Mode:               "off",
			BoostType:          "multiply",
			BoostValue:         2.0,
			TempHours:          2,
			RecurringStartTime: "02:00",
			RecurringEndTime:   "08:00",
		}
	}

	speeds, err := bp.db.GetProfileBaseSpeeds()
	if err != nil {
		speeds = []models.ProfileBaseSpeed{}
	}

	// Calculate remaining time for temp boost if active
	var remainingTimeStr string
	if cfg.Mode == "temp" && cfg.IsCurrentlyBoosted && cfg.TempExpiresAt != nil {
		diff := time.Until(*cfg.TempExpiresAt)
		if diff > 0 {
			hours := int(diff.Hours())
			mins := int(diff.Minutes()) % 60
			secs := int(diff.Seconds()) % 60
			if hours > 0 {
				remainingTimeStr = strconv.Itoa(hours) + " ساعة و " + strconv.Itoa(mins) + " دقيقة"
			} else {
				remainingTimeStr = strconv.Itoa(mins) + " دقيقة و " + strconv.Itoa(secs) + " ثانية"
			}
		}
	}

	_ = bp.renderer.Render(w, "profiles/speed_control", map[string]interface{}{
		"Title":            "التحكم بالسرعات ومضاعفة الباقات",
		"ActiveMenu":       "speed_control",
		"CurrentUser":      middleware.GetUser(r),
		"Config":           cfg,
		"Speeds":           speeds,
		"RemainingTimeStr": remainingTimeStr,
		"ProfilesCount":    len(speeds),
	})
}

// ApplyPermanentHandler boosts speeds across all profiles permanently
func (bp *Blueprint) ApplyPermanentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/profiles/speed-control", http.StatusFound)
		return
	}

	boostType := strings.TrimSpace(r.FormValue("boost_type"))
	if boostType == "" {
		boostType = "multiply"
	}

	var boostVal float64
	if boostType == "add_fixed" {
		boostVal, _ = strconv.ParseFloat(r.FormValue("boost_add_mbps"), 64)
		if boostVal <= 0 {
			boostVal = 5.0
		}
	} else {
		boostVal, _ = strconv.ParseFloat(r.FormValue("boost_multiplier"), 64)
		if boostVal <= 0 {
			boostVal = 2.0
		}
	}

	disconnectUsers := r.FormValue("disconnect_users") == "1"

	// Apply boost
	if err := bp.db.ApplySpeedBoost(boostType, boostVal, disconnectUsers); err != nil {
		log.Printf("[SpeedControl] Permanent boost error: %v", err)
	}

	// In permanent mode, the new speeds become the new base speeds
	_ = bp.db.SyncProfileBaseSpeeds()
	_, _ = bp.db.Exec(`UPDATE profile_base_speeds pbs
		JOIN profiles p ON pbs.profile_id = p.id
		SET pbs.base_rate_limit = p.rate_limit, pbs.boosted_rate_limit = ''`)

	// Save config
	cfg, _ := bp.db.GetSpeedControlConfig()
	if cfg != nil {
		cfg.Mode = "off"
		cfg.BoostType = boostType
		cfg.BoostValue = boostVal
		cfg.IsCurrentlyBoosted = false
		cfg.TempExpiresAt = nil
		_ = bp.db.SaveSpeedControlConfig(cfg)
	}

	http.Redirect(w, r, "/profiles/speed-control?msg=permanent_applied", http.StatusFound)
}

// ApplyTemporaryHandler boosts speeds for a specified duration in hours
func (bp *Blueprint) ApplyTemporaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/profiles/speed-control", http.StatusFound)
		return
	}

	boostType := strings.TrimSpace(r.FormValue("boost_type"))
	if boostType == "" {
		boostType = "multiply"
	}

	var boostVal float64
	if boostType == "add_fixed" {
		boostVal, _ = strconv.ParseFloat(r.FormValue("boost_add_mbps"), 64)
		if boostVal <= 0 {
			boostVal = 5.0
		}
	} else {
		boostVal, _ = strconv.ParseFloat(r.FormValue("boost_multiplier"), 64)
		if boostVal <= 0 {
			boostVal = 2.0
		}
	}

	hours, _ := strconv.Atoi(r.FormValue("temp_hours"))
	if hours <= 0 {
		hours = 2
	}

	disconnectUsers := r.FormValue("disconnect_users") == "1"

	// Apply boost
	if err := bp.db.ApplySpeedBoost(boostType, boostVal, disconnectUsers); err != nil {
		log.Printf("[SpeedControl] Temp boost error: %v", err)
	}

	exp := time.Now().Add(time.Duration(hours) * time.Hour)
	cfg, _ := bp.db.GetSpeedControlConfig()
	if cfg != nil {
		cfg.Mode = "temp"
		cfg.BoostType = boostType
		cfg.BoostValue = boostVal
		cfg.TempHours = hours
		cfg.TempExpiresAt = &exp
		cfg.DisconnectUsers = disconnectUsers
		cfg.IsCurrentlyBoosted = true
		_ = bp.db.SaveSpeedControlConfig(cfg)
	}

	http.Redirect(w, r, "/profiles/speed-control?msg=temp_applied", http.StatusFound)
}

// SaveRecurringHandler configures the daily recurring Happy Hours boost
func (bp *Blueprint) SaveRecurringHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/profiles/speed-control", http.StatusFound)
		return
	}

	enabled := r.FormValue("recurring_enabled") == "1"
	startTime := strings.TrimSpace(r.FormValue("recurring_start_time"))
	if startTime == "" {
		startTime = "02:00"
	}
	endTime := strings.TrimSpace(r.FormValue("recurring_end_time"))
	if endTime == "" {
		endTime = "08:00"
	}

	boostType := strings.TrimSpace(r.FormValue("boost_type"))
	if boostType == "" {
		boostType = "multiply"
	}

	var boostVal float64
	if boostType == "add_fixed" {
		boostVal, _ = strconv.ParseFloat(r.FormValue("boost_add_mbps"), 64)
		if boostVal <= 0 {
			boostVal = 5.0
		}
	} else {
		boostVal, _ = strconv.ParseFloat(r.FormValue("boost_multiplier"), 64)
		if boostVal <= 0 {
			boostVal = 2.0
		}
	}

	disconnectUsers := r.FormValue("disconnect_users") == "1"

	cfg, _ := bp.db.GetSpeedControlConfig()
	if cfg != nil {
		cfg.RecurringEnabled = enabled
		cfg.RecurringStartTime = startTime
		cfg.RecurringEndTime = endTime
		cfg.BoostType = boostType
		cfg.BoostValue = boostVal
		cfg.DisconnectUsers = disconnectUsers
		_ = bp.db.SaveSpeedControlConfig(cfg)
	}

	// Trigger immediate check to align current time with the new schedule
	_ = bp.db.CheckSpeedControlSchedule()

	http.Redirect(w, r, "/profiles/speed-control?msg=recurring_saved", http.StatusFound)
}

// RestoreHandler restores the normal base speeds immediately
func (bp *Blueprint) RestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/profiles/speed-control", http.StatusFound)
		return
	}

	disconnectUsers := r.FormValue("disconnect_users") == "1"
	_ = bp.db.RestoreBaseSpeeds(disconnectUsers)

	http.Redirect(w, r, "/profiles/speed-control?msg=restored", http.StatusFound)
}
