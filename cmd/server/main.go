package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/adv"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/auth"
	backupsBP "github.com/haitham200711/Radius-Server-By-Haitham/blueprints/backups"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/card_templates"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/cards"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/dashboard"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/portal"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/profiles"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/reports"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/resellers"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/routers"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/sessions"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/settings"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/sstp"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/subscribers"
	whatsappBP "github.com/haitham200711/Radius-Server-By-Haitham/blueprints/whatsapp"
	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	backupPkg "github.com/haitham200711/Radius-Server-By-Haitham/pkg/backup"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/cert"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/whatsapp"
	"github.com/haitham200711/Radius-Server-By-Haitham/web"
)

func main() {
	fmt.Println("==================================================================")
	fmt.Println("  HAITHAM RADIUS ENTERPRISE - FreeRADIUS v3 Core & Go Platform")
	fmt.Println("  Architecture: FreeRADIUS + MariaDB + Go Modular Blueprints")
	fmt.Println("==================================================================")

	cfg := config.LoadConfig()

	// 1. Initialize Database
	log.Printf("[INIT] Connecting to %s database...", cfg.DBDriver)
	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatalf("[FATAL] Database connection failed: %v", err)
	}
	defer db.Close()

	// CLI Flag: Reset Admin Password
	resetAdmin := flag.String("reset-admin", "", "Reset admin password (format: username:newpassword or newpassword)")
	flag.Parse()

	if *resetAdmin != "" {
		parts := strings.SplitN(*resetAdmin, ":", 2)
		targetUser := "admin"
		newPass := ""
		if len(parts) == 2 {
			targetUser = parts[0]
			newPass = parts[1]
		} else {
			newPass = parts[0]
		}

		if len(newPass) < 4 {
			log.Fatalf("[ERROR] Password must be at least 4 characters")
		}

		adminObj := &models.Admin{}
		if err := adminObj.SetPassword(newPass); err != nil {
			log.Fatalf("[ERROR] Failed to hash password: %v", err)
		}

		res, err := db.Exec("UPDATE admins SET password_hash = ? WHERE username = ?", adminObj.PasswordHash, targetUser)
		if err != nil {
			log.Fatalf("[ERROR] Failed to update password: %v", err)
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			_, err = db.Exec("INSERT INTO admins (username, password_hash, role, is_active) VALUES (?, ?, 'admin', 1)", targetUser, adminObj.PasswordHash)
			if err != nil {
				log.Fatalf("[ERROR] Failed to create admin user: %v", err)
			}
			fmt.Printf("✅ Admin user '%s' created successfully with the new password.\n", targetUser)
		} else {
			fmt.Printf("✅ Password for admin user '%s' updated successfully.\n", targetUser)
		}
		return
	}

	log.Println("[INIT] Database connected and schema synchronized successfully.")
	db.EnsureAllSSTPClientsHaveSequentialIP()

	// 2. Initialize HTML/CSS Renderer
	renderer, err := web.NewRenderer(".")
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize template renderer: %v", err)
	}

	// Initialize WhatsApp Subsystem
	whatsapp.InitWhatsApp()

	// 3. Register Blueprints
	mux := http.NewServeMux()

	// Static assets
	mux.Handle("/static/", http.StripPrefix("/static/", web.StaticHandler("./static")))
	_ = os.MkdirAll("./uploads", 0755)
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("./uploads"))))

	// Root redirect
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	// Language switcher (Bilingual AR / EN)
	mux.HandleFunc("/set-lang", func(w http.ResponseWriter, r *http.Request) {
		targetLang := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang")))
		if targetLang != "en" {
			targetLang = "ar"
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "radius_lang",
			Value:    targetLang,
			Path:     "/",
			HttpOnly: false,
			MaxAge:   365 * 24 * 3600,
			SameSite: http.SameSiteLaxMode,
		})
		ref := r.Referer()
		if ref == "" {
			ref = "/dashboard"
		}
		http.Redirect(w, r, ref, http.StatusFound)
	})
	mux.HandleFunc("/lang", func(w http.ResponseWriter, r *http.Request) {
		targetLang := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("set")))
		if targetLang == "" {
			targetLang = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang")))
		}
		if targetLang != "en" {
			targetLang = "ar"
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "radius_lang",
			Value:    targetLang,
			Path:     "/",
			HttpOnly: false,
			MaxAge:   365 * 24 * 3600,
			SameSite: http.SameSiteLaxMode,
		})
		ref := r.Referer()
		if ref == "" {
			ref = "/dashboard"
		}
		http.Redirect(w, r, ref, http.StatusFound)
	})

	// Auth Blueprint
	authBP := auth.New(db, renderer, cfg.SessionSecret)
	mux.HandleFunc("/auth/login", authBP.LoginHandler)
	mux.HandleFunc("/auth/logout", authBP.LogoutHandler)

	// Dashboard Blueprint
	dashBP := dashboard.New(db, renderer)
	mux.HandleFunc("/dashboard", middleware.RequireAuth(dashBP.IndexHandler))
	mux.HandleFunc("/dashboard/api/resources", middleware.RequireAuth(dashBP.APIResourcesHandler))


	// Subscribers Blueprint
	subBP := subscribers.New(db, renderer)
	mux.HandleFunc("/subscribers", middleware.RequireAuth(subBP.ListHandler))
	mux.HandleFunc("/subscribers/add", middleware.RequireAuth(subBP.AddHandler))
	mux.HandleFunc("/subscribers/edit", middleware.RequireAuth(subBP.EditHandler))
	mux.HandleFunc("/subscribers/delete", middleware.RequireAuth(subBP.DeleteHandler))
	mux.HandleFunc("/subscribers/renew", middleware.RequireAuth(subBP.RenewHandler))
	mux.HandleFunc("/subscribers/grant-loan", middleware.RequireAuth(subBP.GrantLoanHandler))
	mux.HandleFunc("/subscribers/settle-loan", middleware.RequireAuth(subBP.SettleLoanHandler))
	mux.HandleFunc("/subscribers/terminate", middleware.RequireAuth(subBP.TerminateHandler))
	mux.HandleFunc("/subscribers/change-profile", middleware.RequireAuth(subBP.ChangeProfileHandler))
	mux.HandleFunc("/subscribers/add-quota", middleware.RequireAuth(subBP.AddQuotaHandler))
	mux.HandleFunc("/subscribers/pay-debt", middleware.RequireAuth(subBP.PayDebtHandler))
	mux.HandleFunc("/subscribers/bulk", middleware.RequireAuth(subBP.BulkHandler))
	mux.HandleFunc("/subscribers/live-traffic", middleware.RequireAuth(subBP.LiveTrafficHandler))
	mux.HandleFunc("/subscribers/disconnect-session", middleware.RequireAuth(subBP.DisconnectSessionHandler))
	db.StartSubscriberSyncDaemon()

	// Cards Blueprint
	cardsBP := cards.New(db, renderer)
	db.StartCardSyncDaemon()
	mux.HandleFunc("/cards", middleware.RequireAuth(cardsBP.ListHandler))
	mux.HandleFunc("/cards/generate", middleware.RequireAuth(cardsBP.GenerateHandler))
	mux.HandleFunc("/cards/print", middleware.RequireAuth(cardsBP.PrintHandler))
	mux.HandleFunc("/cards/cafe", middleware.RequireAuth(cardsBP.CafePortalHandler))
	mux.HandleFunc("/cards/cafe/generate", middleware.RequireAuth(cardsBP.CafeGenerateHandler))
	mux.HandleFunc("/cards/cafe/disconnect", middleware.RequireAuth(cardsBP.CafeDisconnectHandler))

	// Card Templates Blueprint
	tmplBP := card_templates.New(db, renderer)
	mux.HandleFunc("/card-templates", middleware.RequireAuth(tmplBP.ListHandler))
	mux.HandleFunc("/card-templates/add", middleware.RequireAuth(tmplBP.AddHandler))
	mux.HandleFunc("/card-templates/edit", middleware.RequireAuth(tmplBP.EditHandler))
	mux.HandleFunc("/card-templates/update", middleware.RequireAuth(tmplBP.UpdateHandler))
	mux.HandleFunc("/card-templates/delete", middleware.RequireAuth(tmplBP.DeleteHandler))
	mux.HandleFunc("/card-templates/set-default", middleware.RequireAuth(tmplBP.SetDefaultHandler))
	mux.HandleFunc("/card-templates/set_default", middleware.RequireAuth(tmplBP.SetDefaultHandler))

	// Aliases for /templates
	mux.HandleFunc("/templates", middleware.RequireAuth(tmplBP.ListHandler))
	mux.HandleFunc("/templates/add", middleware.RequireAuth(tmplBP.AddHandler))
	mux.HandleFunc("/templates/update", middleware.RequireAuth(tmplBP.UpdateHandler))
	mux.HandleFunc("/templates/delete", middleware.RequireAuth(tmplBP.DeleteHandler))
	mux.HandleFunc("/templates/set_default", middleware.RequireAuth(tmplBP.SetDefaultHandler))

	// Profiles Blueprint
	profBP := profiles.New(db, renderer)
	profBP.StartSpeedControlDaemon()
	mux.HandleFunc("/profiles", middleware.RequireAuth(profBP.ListHandler))
	mux.HandleFunc("/profiles/add", middleware.RequireAuth(profBP.AddHandler))
	mux.HandleFunc("/profiles/edit", middleware.RequireAuth(profBP.EditHandler))
	mux.HandleFunc("/profiles/delete", middleware.RequireAuth(profBP.DeleteHandler))
	mux.HandleFunc("/profiles/speed-control", middleware.RequireAuth(profBP.SpeedControlHandler))
	mux.HandleFunc("/profiles/speed-control/apply-permanent", middleware.RequireAuth(profBP.ApplyPermanentHandler))
	mux.HandleFunc("/profiles/speed-control/apply-temporary", middleware.RequireAuth(profBP.ApplyTemporaryHandler))
	mux.HandleFunc("/profiles/speed-control/save-recurring", middleware.RequireAuth(profBP.SaveRecurringHandler))
	mux.HandleFunc("/profiles/speed-control/restore", middleware.RequireAuth(profBP.RestoreHandler))
	mux.HandleFunc("/profiles/speed-control/restore-profile", middleware.RequireAuth(profBP.RestoreProfileHandler))

	// Routers (NAS) Blueprint
	routBP := routers.New(db, renderer)
	mux.HandleFunc("/routers", middleware.RequireAuth(routBP.ListHandler))
	mux.HandleFunc("/routers/add", middleware.RequireAuth(routBP.AddHandler))
	mux.HandleFunc("/routers/edit", middleware.RequireAuth(routBP.EditHandler))
	mux.HandleFunc("/routers/delete", middleware.RequireAuth(routBP.DeleteHandler))
	mux.HandleFunc("/routers/test-api", middleware.RequireAuth(routBP.TestAPIHandler))

	// SSTP Server Blueprint
	sstpBP := sstp.New(db, renderer)
	mux.HandleFunc("/sstp", middleware.RequireAuth(sstpBP.ListHandler))
	mux.HandleFunc("/sstp/settings", middleware.RequireAuth(sstpBP.SaveSettingsHandler))
	mux.HandleFunc("/sstp/clients/add", middleware.RequireAuth(sstpBP.AddClientHandler))
	mux.HandleFunc("/sstp/clients/edit", middleware.RequireAuth(sstpBP.EditClientHandler))
	mux.HandleFunc("/sstp/clients/delete", middleware.RequireAuth(sstpBP.DeleteClientHandler))
	mux.HandleFunc("/sstp/token/generate", middleware.RequireAuth(sstpBP.GenerateTokenHandler))
	mux.HandleFunc("/sstp/script", sstpBP.ScriptHandler)
	mux.HandleFunc("/sstp/routes/add", middleware.RequireAuth(sstpBP.AddRouteHandler))
	mux.HandleFunc("/sstp/routes/edit", middleware.RequireAuth(sstpBP.EditRouteHandler))
	mux.HandleFunc("/sstp/routes/delete", middleware.RequireAuth(sstpBP.DeleteRouteHandler))
	mux.HandleFunc("/sstp/routes/apply", middleware.RequireAuth(sstpBP.ApplyRoutesHandler))
	mux.HandleFunc("/sstp/routes/hook-up", sstpBP.HookUpHandler)

	// Resellers Blueprint (Admin Only)
	resBP := resellers.New(db, renderer)
	mux.HandleFunc("/resellers", middleware.RequireAdmin(resBP.ListHandler))
	mux.HandleFunc("/resellers/add", middleware.RequireAdmin(resBP.AddHandler))
	mux.HandleFunc("/resellers/edit", middleware.RequireAdmin(resBP.EditHandler))
	mux.HandleFunc("/resellers/permissions", middleware.RequireAdmin(resBP.PermissionsHandler))
	mux.HandleFunc("/resellers/recharge", middleware.RequireAdmin(resBP.RechargeHandler))
	mux.HandleFunc("/resellers/toggle-status", middleware.RequireAdmin(resBP.ToggleStatusHandler))
	mux.HandleFunc("/resellers/delete", middleware.RequireAdmin(resBP.DeleteHandler))
	mux.HandleFunc("/resellers/details", middleware.RequireAdmin(resBP.DetailsAPIHandler))
	mux.HandleFunc("/resellers/transactions", middleware.RequireAdmin(resBP.TransactionsAPIHandler))

	// Sessions & CoA Blueprint
	sessBP := sessions.New(db, renderer)
	mux.HandleFunc("/sessions", middleware.RequireAuth(sessBP.ListHandler))
	mux.HandleFunc("/sessions/disconnect", middleware.RequireAuth(sessBP.DisconnectHandler))

	// Reports Blueprint
	repBP := reports.New(db, renderer)
	mux.HandleFunc("/reports", middleware.RequireAuth(repBP.IndexHandler))
	mux.HandleFunc("/reports/logins", middleware.RequireAuth(repBP.LoginsRedirectHandler))

	// Settings Blueprint
	settBP := settings.New(db, renderer)
	mux.HandleFunc("/settings", middleware.RequireAuth(settBP.IndexHandler))
	mux.HandleFunc("/settings/change-password", middleware.RequireAuth(settBP.ChangePasswordHandler))
	mux.HandleFunc("/settings/portal", middleware.RequireAdmin(settBP.SavePortalSettingsHandler))
	mux.HandleFunc("/settings/maintenance/reboot", middleware.RequireAdmin(settBP.RebootOSHandler))
	mux.HandleFunc("/settings/maintenance/restart-radius", middleware.RequireAdmin(settBP.RestartRadiusHandler))
	mux.HandleFunc("/settings/maintenance/disconnect-all", middleware.RequireAdmin(settBP.DisconnectAllSessionsHandler))
	mux.HandleFunc("/settings/maintenance/close-sessions", middleware.RequireAdmin(settBP.CloseAllDatabaseSessionsHandler))

	// Subscriber Self-Care Portal Blueprint
	portalBP := portal.New(db, renderer, cfg.SessionSecret)
	mux.HandleFunc("/portal/login", portalBP.LoginHandler)
	mux.HandleFunc("/portal/logout", portalBP.LogoutHandler)
	mux.HandleFunc("/portal", middleware.RequireSubscriberAuth(portalBP.IndexHandler))
	mux.HandleFunc("/portal/request-loan", middleware.RequireSubscriberAuth(portalBP.RequestLoanHandler))
	mux.HandleFunc("/portal/disconnect", middleware.RequireSubscriberAuth(portalBP.DisconnectHandler))
	mux.HandleFunc("/portal/change-password", middleware.RequireSubscriberAuth(portalBP.ChangePasswordHandler))

	// WhatsApp Blueprint
	waBP := whatsappBP.New(db, renderer)
	waBP.StartWhatsAppAutomator()
	mux.HandleFunc("/whatsapp", middleware.RequireAuth(waBP.IndexHandler))
	mux.HandleFunc("/whatsapp/start-pairing", middleware.RequireAuth(waBP.StartPairingHandler))
	mux.HandleFunc("/whatsapp/pair-phone", middleware.RequireAuth(waBP.PairPhoneHandler))
	mux.HandleFunc("/whatsapp/status", middleware.RequireAuth(waBP.StatusHandler))
	mux.HandleFunc("/whatsapp/logout", middleware.RequireAuth(waBP.LogoutHandler))
	mux.HandleFunc("/whatsapp/test", middleware.RequireAuth(waBP.TestMessageHandler))
	mux.HandleFunc("/whatsapp/settings", middleware.RequireAuth(waBP.SaveSettingsHandler))
	mux.HandleFunc("/whatsapp/templates/save", middleware.RequireAuth(waBP.SaveTemplateHandler))
	mux.HandleFunc("/whatsapp/debt-reminders/run-now", middleware.RequireAuth(waBP.RunDebtRemindersHandler))

	// Backups Blueprint (Admin Only)
	bakBP := backupsBP.New(db, renderer, cfg)
	backupPkg.StartBackupScheduler(db, cfg)
	mux.HandleFunc("/backups", middleware.RequireAdmin(bakBP.IndexHandler))
	mux.HandleFunc("/backups/settings", middleware.RequireAdmin(bakBP.SaveSettingsHandler))
	mux.HandleFunc("/backups/create-now", middleware.RequireAdmin(bakBP.CreateNowHandler))
	mux.HandleFunc("/backups/download", middleware.RequireAdmin(bakBP.DownloadHandler))
	mux.HandleFunc("/backups/delete", middleware.RequireAdmin(bakBP.DeleteHandler))
	mux.HandleFunc("/backups/send-whatsapp", middleware.RequireAdmin(bakBP.SendWhatsAppHandler))
	mux.HandleFunc("/backups/restore", middleware.RequireAdmin(bakBP.RestoreHandler))

	// ADV Migration & Restore Blueprint (Admin Only)
	advBP := adv.New(db, renderer)
	mux.HandleFunc("/adv", middleware.RequireAdmin(advBP.IndexHandler))
	mux.HandleFunc("/adv/upload", middleware.RequireAdmin(advBP.UploadHandler))
	mux.HandleFunc("/adv/analyze-server", middleware.RequireAdmin(advBP.AnalyzeServerFileHandler))
	mux.HandleFunc("/adv/import", middleware.RequireAdmin(advBP.ImportHandler))

	// Wrap mux with Session Authentication middleware
	handler := middleware.AuthMiddleware(cfg.SessionSecret)(mux)

	// Determine public TLS port string if not default 443
	targetPort := ""
	if cfg.WebAddr != ":443" && cfg.WebAddr != "443" {
		_, p, err := net.SplitHostPort(cfg.WebAddr)
		if err == nil && p != "443" {
			targetPort = ":" + p
		} else if cfg.WebAddr != "" && cfg.WebAddr != ":443" {
			targetPort = cfg.WebAddr
		}
	}

	var server *http.Server
	var httpServer *http.Server
	var legacyServer *http.Server

	// 4. Start HTTP / HTTPS Server
	if cfg.EnableHTTPS {
		certFile, keyFile, err := cert.EnsureTLSCertificate(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			log.Fatalf("[FATAL] Failed to configure TLS certificates: %v", err)
		}

		// HTTPS Main Server (:443)
		server = &http.Server{
			Addr:    cfg.WebAddr,
			Handler: handler,
		}

		go func() {
			log.Printf("[HTTPS] Enterprise Control-Plane listening on https://0.0.0.0%s", cfg.WebAddr)
			if err := server.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[FATAL] HTTPS server error: %v", err)
			}
		}()

		// HTTP Redirector (:80) with RouterOS /sstp/script bypass
		if cfg.HTTPRedirectAddr != "" {
			httpMux := http.NewServeMux()
			// RouterOS script fetch compatibility over HTTP
			httpMux.HandleFunc("/sstp/script", sstpBP.ScriptHandler)
			httpMux.HandleFunc("/sstp/routes/hook-up", sstpBP.HookUpHandler)
			// Redirect all other HTTP traffic to HTTPS
			httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				host, _, err := net.SplitHostPort(r.Host)
				if err != nil {
					host = r.Host
				}
				target := "https://" + host + targetPort + r.RequestURI
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			})

			httpServer = &http.Server{
				Addr:    cfg.HTTPRedirectAddr,
				Handler: httpMux,
			}

			go func() {
				log.Printf("[HTTP] Redirector (:80 -> :443) listening on http://0.0.0.0%s", cfg.HTTPRedirectAddr)
				if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("[WARN] HTTP redirector stopped or port occupied: %v", err)
				}
			}()
		}

		// Legacy Port 5000 Redirector (keeps old links/bookmarks alive)
		if cfg.WebAddr != ":5000" && cfg.HTTPRedirectAddr != ":5000" {
			legacyMux := http.NewServeMux()
			legacyMux.HandleFunc("/sstp/script", sstpBP.ScriptHandler)
			legacyMux.HandleFunc("/sstp/routes/hook-up", sstpBP.HookUpHandler)
			legacyMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				host, _, err := net.SplitHostPort(r.Host)
				if err != nil {
					host = r.Host
				}
				target := "https://" + host + targetPort + r.RequestURI
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			})

			legacyServer = &http.Server{
				Addr:    ":5000",
				Handler: legacyMux,
			}

			go func() {
				log.Printf("[HTTP] Legacy redirector (:5000 -> :443) listening on http://0.0.0.0:5000")
				if err := legacyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("[WARN] Port 5000 redirector stopped: %v", err)
				}
			}()
		}
	} else {
		// Plain HTTP Server
		server = &http.Server{
			Addr:    cfg.WebAddr,
			Handler: handler,
		}

		go func() {
			log.Printf("[WEB] Enterprise Control-Plane listening on http://0.0.0.0%s", cfg.WebAddr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[FATAL] Web server error: %v", err)
			}
		}()
	}

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("[SHUTDOWN] Stopping enterprise server...")
	if server != nil {
		_ = server.Close()
	}
	if httpServer != nil {
		_ = httpServer.Close()
	}
	if legacyServer != nil {
		_ = legacyServer.Close()
	}
	_ = db.Close()
	log.Println("[SHUTDOWN] Exited cleanly.")
}
