package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/radius"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/web"
)

func main() {
	authPort := flag.Int("auth-port", 1812, "RADIUS Authentication UDP port")
	acctPort := flag.Int("acct-port", 1813, "RADIUS Accounting UDP port")
	coaPort := flag.Int("coa-port", 3799, "RADIUS CoA / PoD UDP port")
	webAddr := flag.String("web-addr", ":8080", "HTTP Web Dashboard address")
	dbPath := flag.String("db", "./radius.db", "SQLite database file path")
	flag.Parse()

	fmt.Println("==================================================================")
	fmt.Println("  HAITHAM RADIUS SERVER - High Performance MikroTik Edition")
	fmt.Println("  Version: 1.0.0 | Engine: Go + WAL In-Memory Accelerated")
	fmt.Println("==================================================================")

	// 1. Initialize High-Performance Database & Memory Cache
	log.Printf("[INIT] Opening database with WAL mode and Memory Cache: %s", *dbPath)
	db, err := database.Open(*dbPath)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize database: %v", err)
	}
	defer db.Close()

	log.Printf("[INIT] Database initialized. Loaded %d subscribers in RAM cache for nanosecond authentication.", db.Cache.CountUsers())

	// 2. Start RADIUS Server (UDP 1812, 1813, 3799)
	radCfg := radius.ServerConfig{
		AuthPort: *authPort,
		AcctPort: *acctPort,
		CoAPort:  *coaPort,
		BindAddr: "0.0.0.0",
	}
	radServer := radius.NewServer(radCfg, db)
	if err := radServer.Start(); err != nil {
		log.Fatalf("[FATAL] Failed to start RADIUS listeners: %v", err)
	}
	defer radServer.Stop()

	// 3. Start HTTP Web Dashboard Server
	webServer := web.NewServer(*webAddr, db)
	go func() {
		if err := webServer.Start(); err != nil {
			log.Printf("[WEB] Web server error: %v", err)
		}
	}()

	fmt.Printf("\n>>> RADIUS Server is RUNNING! <<<\n")
	fmt.Printf(" - Auth Listener:       0.0.0.0:%d (UDP)\n", *authPort)
	fmt.Printf(" - Acct Listener:       0.0.0.0:%d (UDP)\n", *acctPort)
	fmt.Printf(" - CoA / PoD:           0.0.0.0:%d (UDP)\n", *coaPort)
	fmt.Printf(" - Web Dashboard:       http://localhost%s\n\n", *webAddr)

	// Wait for shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("[SHUTDOWN] Terminating gracefully...")
	_ = webServer.Stop()
	radServer.Stop()
	_ = db.Close()
	log.Println("[SHUTDOWN] RADIUS Server shut down successfully.")
}
