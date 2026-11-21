package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	config, err := loadServerConfig("server_config.json")
	if err != nil {
		log.Printf("startup_error err=%v", err)
		os.Exit(1)
	}
	if err := cleanupEmptyUncategorized("."); err != nil {
		log.Printf("startup_error err=%v", err)
		os.Exit(1)
	}
	if err := buildManifest("."); err != nil {
		log.Printf("startup_error err=%v", err)
		os.Exit(1)
	}
	chartCatalog, err := loadChartCatalog(".")
	if err != nil {
		log.Printf("startup_error err=%v", err)
		os.Exit(1)
	}
	collections, err := buildPublicCollections(".", chartCatalog)
	if err != nil {
		log.Printf("startup_error err=%v", err)
		os.Exit(1)
	}
	store, err := openStore(config.DBPath)
	if err != nil {
		log.Printf("startup_error err=%v", err)
		os.Exit(1)
	}
	defer store.Close()

	app := &application{
		store:         store,
		inviteCode:    config.InviteCode,
		danAdminToken: config.DanAdminToken,
		catalog:       chartCatalog,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", serveServerInfo)
	mux.HandleFunc("/api/maichart/list", serveChartList("."))
	mux.HandleFunc("/api/maichart/info", serveBatchChartInfo(".", chartCatalog))
	mux.HandleFunc("/api/maichart/changes", serveChartChanges)
	mux.HandleFunc("GET /api/collection/list", servePublicCollectionList(collections))
	mux.HandleFunc("/api/collection/", servePublicCollection(collections))
	mux.HandleFunc("POST /api/maichart/{id}/score", app.serveChartScore)
	mux.HandleFunc("GET /api/maiscore/leaderboard/chart/by-id/{id}", app.serveChartLeaderboard)
	mux.HandleFunc("POST /api/account/login", app.serveLogin)
	mux.HandleFunc("GET /api/account/profile", app.requireAccount(app.serveProfile))
	mux.HandleFunc("GET /api/account/scores", app.requireAccount(app.serveAccountScores))
	mux.HandleFunc("GET /api/account/favorite/collection/list", app.requireAccount(app.serveGameCollections(collections)))
	mux.HandleFunc("POST /api/account/logout", app.requireAccount(app.serveLogout))
	mux.HandleFunc("/register", app.serveRegistration)
	mux.HandleFunc("GET /admin/dan/login", app.serveDanAdminLogin)
	mux.HandleFunc("POST /admin/dan/login", app.serveDanAdminLogin)
	mux.HandleFunc("POST /admin/dan/logout", app.requireDanAdmin(app.serveDanAdminLogout))
	mux.HandleFunc("GET /admin/dan/app.js", serveDanAdminScript)
	mux.HandleFunc("/admin/dan", app.requireDanAdmin(app.serveDanAdmin))
	mux.HandleFunc("/api/maichart/", serveChartFile(".", chartCatalog))
	addr := ":8080"
	log.Printf("startup addr=%s", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Printf("startup_error err=%v", err)
		os.Exit(1)
	}
}
