package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/fasterp/backend/internal/api"
	"github.com/fasterp/backend/internal/config"
	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/models"
	"github.com/fasterp/backend/internal/module"
	"github.com/google/uuid"
)

func main() {
	cfg := config.Load()

	if cfg.JWTSecret == "change-me-in-production" || len(cfg.JWTSecret) < 16 {
		log.Println("[WARN] JWT_SECRET is weak or default. Set a strong FASTERP_JWT_SECRET in production.")
	}
	if cfg.JWTRefreshSecret == cfg.JWTSecret {
		log.Println("[WARN] FASTERP_JWT_REFRESH_SECRET matches FASTERP_JWT_SECRET. Use distinct secrets.")
	}

	ctx := context.Background()

	if err := db.Connect(db.ConnConfig{
		DatabaseURL:  cfg.DatabaseURL,
		MaxOpenConns: cfg.MaxOpenConns,
		MaxIdleConns: cfg.MaxIdleConns,
	}); err != nil {
		log.Fatalf("[FATAL] Database connection failed: %v", err)
	}
	defer db.Close()

	if err := models.AutoMigrate(); err != nil {
		log.Fatalf("[FATAL] Migration failed: %v", err)
	}
	db.CheckRLSEnforcement()

	seedDefaultTenant()
	seedDefaultUsers(ctx)
	seedDefaultModules(ctx, cfg.ModulesDir)

	modManager := module.NewManager(cfg.ModulesDir, cfg.UploadDir)
	loadInstalledModules(ctx, modManager)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           api.SetupRouter(cfg, modManager),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("[FastERP] Server listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("[FastERP] Shutting down, draining in-flight requests...")
	shutdownCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[WARN] Graceful shutdown timed out: %v", err)
	}
	log.Println("[FastERP] Stopped")
}

func seedDefaultTenant() {
	var count int
	db.DB.QueryRow("SELECT COUNT(*) FROM tenants").Scan(&count)
	if count > 0 {
		return
	}

	tenantID := uuid.New().String()
	_, err := db.DB.Exec(
		`INSERT INTO tenants (id, name, slug, active)
		 VALUES ($1, 'Default Tenant', 'default', true)`,
		tenantID,
	)
	if err != nil {
		log.Printf("[WARN] Failed to seed default tenant: %v", err)
		return
	}
	log.Printf("[Seed] Default tenant created: %s (slug: default)", tenantID)

	// Write tenant ID to file for easy reference
	os.WriteFile(".default-tenant-id", []byte(tenantID), 0644)
}

func seedDefaultUsers(ctx context.Context) {
	defaultTenantID := getDefaultTenantID()
	if defaultTenantID == "" {
		return
	}

	err := db.WithTenant(ctx, defaultTenantID, func(x db.QueryExecutor) error {
		var count int
		if err := x.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return nil
		}

		hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}

		_, err = x.ExecContext(ctx,
			`INSERT INTO users (id, tenant_id, username, email, password_hash, is_admin, active)
			 VALUES ($1, $2, 'admin', 'admin@fasterp.local', $3, true, true)`,
			uuid.New().String(), defaultTenantID, string(hash),
		)
		if err != nil {
			return err
		}
		log.Println("[Seed] Default user created: admin / admin123")
		log.Printf("[Seed] Tenant ID: %s (use as X-Tenant-ID header)", defaultTenantID)
		return nil
	})
	if err != nil {
		log.Printf("[WARN] Failed to seed default user: %v", err)
	}
}

func seedDefaultModules(ctx context.Context, modulesDir string) {
	defaultTenantID := getDefaultTenantID()
	if defaultTenantID == "" {
		return
	}

	conn, err := db.AcquireConn(ctx, defaultTenantID)
	if err != nil {
		log.Printf("[WARN] Failed to seed modules: %v", err)
		return
	}
	defer conn.Close()

	var count int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM installed_modules").Scan(&count); err != nil {
		log.Printf("[WARN] Failed to seed modules: %v", err)
		return
	}
	if count > 0 {
		return
	}

	presets := []struct {
		name, version, label, description, author string
	}{
		{"hello", "1.0.0", "Hello", "Hello World example module", "FastERP Team"},
		{"contacts", "1.0.0", "Contacts", "Simple contact management", "FastERP Team"},
		{"products", "1.0.0", "Solar Products", "Gestión de catálogo de productos solares", "FastERP Solar Team"},
		{"sites", "1.0.0", "Solar Sites", "Gestión de sitios de proyecto", "FastERP Solar Team"},
		{"tienda_web", "2.0.1", "Tienda Web", "Tienda web: ficha de producto, precio y galería de imágenes", "Ecuabyte"},
		{"sitio_web", "1.0.0", "Sitio Web", "CMS para construir y publicar el sitio público", "Ecuabyte"},
	}

	for _, p := range presets {
		// Try subdirectory first (new structure): modules/hello/module.wasm
		modPath := filepath.Join(modulesDir, p.name, "module.wasm")
		if _, err := os.Stat(modPath); os.IsNotExist(err) {
			// Fallback to flat structure: modules/hello.wasm
			modPath = filepath.Join(modulesDir, p.name+".wasm")
			if _, err := os.Stat(modPath); os.IsNotExist(err) {
				// Try .so plugin (legacy)
				modPath = filepath.Join(modulesDir, p.name+".so")
				if _, err := os.Stat(modPath); os.IsNotExist(err) {
					log.Printf("[Seed] Module %s not found, skipping", p.name)
					continue
				}
			}
		}

		_, err := conn.ExecContext(ctx,
			`INSERT INTO installed_modules (id, tenant_id, name, version, label, description, author, active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, true)`,
			uuid.New().String(), defaultTenantID,
			p.name, p.version, p.label, p.description, p.author,
		)
		if err != nil {
			log.Printf("[Seed] Failed to seed module %s: %v", p.name, err)
		} else {
			log.Printf("[Seed] Module seeded: %s (active)", p.name)
		}
	}
}

func getDefaultTenantID() string {
	var id string
	err := db.DB.QueryRow("SELECT id FROM tenants WHERE slug = 'default' AND active = true").Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// loadInstalledModules loads every module that is active for at least one tenant.
// The WASM registry is keyed by module name and shared across tenants, so this walks
// the tenant list rather than reading installed_modules across tenants — which RLS
// (correctly) no longer allows on a tenant-scoped connection.
func loadInstalledModules(ctx context.Context, mgr *module.ModuleManager) {
	tenantRows, err := db.DB.QueryContext(ctx, "SELECT id FROM tenants WHERE active = true")
	if err != nil {
		log.Printf("[WARN] Failed to query tenants: %v", err)
		return
	}
	defer tenantRows.Close()

	var tenantIDs []string
	for tenantRows.Next() {
		var id string
		if err := tenantRows.Scan(&id); err != nil {
			log.Printf("[WARN] Failed to scan tenant: %v", err)
			continue
		}
		tenantIDs = append(tenantIDs, id)
	}
	if err := tenantRows.Err(); err != nil {
		log.Printf("[WARN] Failed to read tenants: %v", err)
		return
	}

	loaded := make(map[string]bool)
	for _, tenantID := range tenantIDs {
		err := db.WithTenant(ctx, tenantID, func(x db.QueryExecutor) error {
			rows, err := x.QueryContext(ctx, "SELECT name FROM installed_modules WHERE active = true")
			if err != nil {
				return err
			}
			defer rows.Close()

			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					return err
				}
				if loaded[name] {
					continue
				}
				loaded[name] = true
				if err := mgr.LoadPlugin(name); err != nil {
					log.Printf("[WARN] Failed to load module %s: %v", name, err)
				}
			}
			return rows.Err()
		})
		if err != nil {
			log.Printf("[WARN] Failed to load modules for tenant %s: %v", tenantID, err)
		}
	}
}
