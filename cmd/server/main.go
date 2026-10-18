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
	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/middleware"
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

	// 3. Register Blueprints
	mux := http.NewServeMux()

	// Static assets
	mux.Handle("/static/", http.StripPrefix("/static/", web.StaticHandler("./static")))

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

	// Subscribers Blueprint
	subBP := subscribers.New(db, renderer)
	mux.HandleFunc("/subscribers", middleware.RequireAuth(subBP.ListHandler))
	mux.HandleFunc("/subscribers/add", middleware.RequireAuth(subBP.AddHandler))
	mux.HandleFunc("/subscribers/edit", middleware.RequireAuth(subBP.EditHandler))
	mux.HandleFunc("/subscribers/delete", middleware.RequireAuth(subBP.DeleteHandler))
	mux.HandleFunc("/subscribers/renew", middleware.RequireAuth(subBP.RenewHandler))

	// Cards Blueprint
	cardsBP := cards.New(db, renderer)
	mux.HandleFunc("/cards", middleware.RequireAuth(cardsBP.ListHandler))
	mux.HandleFunc("/cards/generate", middleware.RequireAuth(cardsBP.GenerateHandler))
	mux.HandleFunc("/cards/print", middleware.RequireAuth(cardsBP.PrintHandler))

	// Card Templates Blueprint
	tmplBP := card_templates.New(db, renderer)
	mux.HandleFunc("/card-templates", middleware.RequireAuth(tmplBP.ListHandler))
	mux.HandleFunc("/card-templates/add", middleware.RequireAuth(tmplBP.AddHandler))
	mux.HandleFunc("/card-templates/edit", middleware.RequireAuth(tmplBP.EditHandler))
	mux.HandleFunc("/card-templates/delete", middleware.RequireAuth(tmplBP.DeleteHandler))

	// Profiles Blueprint
	profBP := profiles.New(db, renderer)
	mux.HandleFunc("/profiles", middleware.RequireAuth(profBP.ListHandler))
	mux.HandleFunc("/profiles/add", middleware.RequireAuth(profBP.AddHandler))
	mux.HandleFunc("/profiles/edit", middleware.RequireAuth(profBP.EditHandler))
	mux.HandleFunc("/profiles/delete", middleware.RequireAuth(profBP.DeleteHandler))

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
	mux.HandleFunc("/sstp/clients/delete", middleware.RequireAuth(sstpBP.DeleteClientHandler))

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

	// Settings Blueprint
	settBP := settings.New(db, renderer)
	mux.HandleFunc("/settings", middleware.RequireAuth(settBP.IndexHandler))

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
