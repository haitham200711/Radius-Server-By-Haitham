package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/auth"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/card_templates"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/cards"
	"github.com/haitham200711/Radius-Server-By-Haitham/blueprints/dashboard"
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
	log.Println("[INIT] Database connected and schema synchronized successfully.")

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
	mux.HandleFunc("/subscribers/add-quota", middleware.RequireAuth(subBP.AddQuotaHandler))
	mux.HandleFunc("/subscribers/pay-debt", middleware.RequireAuth(subBP.PayDebtHandler))
	mux.HandleFunc("/subscribers/bulk", middleware.RequireAuth(subBP.BulkHandler))
	mux.HandleFunc("/subscribers/live-traffic", middleware.RequireAuth(subBP.LiveTrafficHandler))
	mux.HandleFunc("/subscribers/disconnect-session", middleware.RequireAuth(subBP.DisconnectSessionHandler))

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
	mux.HandleFunc("/routers/delete", middleware.RequireAuth(routBP.DeleteHandler))

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
	mux.HandleFunc("/resellers/delete", middleware.RequireAdmin(resBP.DeleteHandler))

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

	// Wrap mux with Session Authentication middleware
	handler := middleware.AuthMiddleware(cfg.SessionSecret)(mux)

	// 4. Start HTTP Server
	server := &http.Server{
		Addr:    cfg.WebAddr,
		Handler: handler,
	}

	go func() {
		log.Printf("[WEB] Enterprise Control-Plane listening on http://localhost%s", cfg.WebAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Web server error: %v", err)
		}
	}()

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("[SHUTDOWN] Stopping enterprise server...")
	_ = server.Close()
	_ = db.Close()
	log.Println("[SHUTDOWN] Exited cleanly.")
}
