package main

import (
	"context"
	"log"
	"net/http"

	"github.com/fasterp/backend/config"
	"github.com/fasterp/backend/db"
	"github.com/fasterp/backend/handlers"
	"github.com/fasterp/backend/middleware"
)

func main() {
	// Cargar config
	cfg := config.Load()
	log.Printf("[FastERP] Starting Core (port=%s:%s)", cfg.Server.Host, cfg.Server.Port)

	// Conectar a BD
	dbConn, err := db.Connect(cfg.Database.URL)
	if err != nil {
		log.Fatalf("[FATAL] Database connection failed: %v", err)
	}
	defer dbConn.Close()

	// Ejecutar migrations
	if err := db.RunMigrations(dbConn); err != nil {
		log.Fatalf("[FATAL] Migrations failed: %v", err)
	}

	// Seed datos por defecto
	if err := db.SeedDefaultData(dbConn); err != nil {
		log.Fatalf("[FATAL] Seed failed: %v", err)
	}

	// Crear logger de auditoría
	auditLogger := db.NewAuditLogger(dbConn)

	// Crear renderer de templates
	renderer := handlers.NewTemplateRenderer("./templates")
	authHandler := handlers.NewAuthHandler(dbConn)
	auditHandler := handlers.NewAuditHandler(auditLogger)
	setupHandler := handlers.NewSetupHandler(dbConn)
	moduleHandler := handlers.NewModuleHandler(dbConn)

	// Configurar router
	mux := http.NewServeMux()

	// Middleware global
	var handler http.Handler = mux
	handler = middleware.LoggingMiddleware(handler)

	// Rutas públicas
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			// Verificar si necesita setup
			ctx := context.Background()
			row := dbConn.QueryRow(ctx, "SELECT COUNT(*) FROM tenants")
			var count int
			row.Scan(&count)

			if count == 0 {
				// No hay tenants, mostrar wizard
				setupHandler.ShowSetupWizard(renderer)(w, r)
			} else {
				// Ya configurado, mostrar login
				renderer.RenderFile("login.html", nil, w)
			}
		}
	})

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			renderer.RenderFile("login.html", nil, w)
		}
	})

	// Setup Wizard
	mux.HandleFunc("/setup", setupHandler.ShowSetupWizard(renderer))

	// API Setup
	mux.HandleFunc("/api/setup/check", setupHandler.CheckSetup)
	mux.HandleFunc("/api/setup/initial", setupHandler.CreateInitialSetup)
	mux.HandleFunc("/api/setup/tenant", setupHandler.CreateNewTenant)

	// API Auth (con BD real)
	mux.HandleFunc("/api/auth/login", authHandler.Login)
	mux.HandleFunc("/api/auth/logout", authHandler.Logout)

	// API Audit
	mux.HandleFunc("/api/audit/entity", auditHandler.GetEntityAudit)
	mux.HandleFunc("/api/audit/user", auditHandler.GetUserAudit)
	mux.HandleFunc("/api/audit/stats", auditHandler.GetAuditStats)
	mux.HandleFunc("/api/audit/export", auditHandler.ExportAudit)

	// API Modules
	mux.HandleFunc("/api/modules/available", moduleHandler.GetAvailableModules)
	mux.HandleFunc("/api/modules/install", moduleHandler.InstallModule)
	mux.HandleFunc("/api/modules/uninstall", moduleHandler.UninstallModule)

	// Admin routes (protegidas - TODO)
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		renderer.RenderWithLayout("layout.html", "dashboard-content.html", map[string]interface{}{
			"Title":       "Dashboard",
			"CurrentPage": "dashboard",
		}, w)
	})

	// Audit dashboard
	mux.HandleFunc("/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		renderer.RenderWithLayout("layout.html", "audit-content.html", map[string]interface{}{
			"Title":       "Auditoría",
			"CurrentPage": "audit",
		}, w)
	})

	// Module Store
	mux.HandleFunc("/admin/modules", func(w http.ResponseWriter, r *http.Request) {
		renderer.RenderWithLayout("layout.html", "modules-content.html", map[string]interface{}{
			"Title":       "Module Store",
			"CurrentPage": "modules",
		}, w)
	})

	// Estáticos
	fs := http.FileServer(http.Dir("./static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","database":"connected"}`))
	})

	// Iniciar servidor
	addr := cfg.Server.Host + ":" + cfg.Server.Port
	log.Printf("[FastERP] Listening on %s", addr)
	log.Printf("[FastERP] Open http://localhost:%s", cfg.Server.Port)
	log.Println("[FastERP] Credenciales: admin/admin123")

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("[FATAL] Server failed: %v", err)
	}
}
