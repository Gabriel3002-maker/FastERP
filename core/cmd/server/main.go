package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	// El healthcheck de docker-compose ejecuta el mismo binario con -healthcheck:
	// así el chequeo usa el servidor real en vez de una sonda aparte que puede
	// desincronizarse de lo que el proceso hace de verdad.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	cfg := config.Load()

	// Un secreto débil no es un problema de calidad, es una puerta abierta:
	// quien tenga el valor puede firmar un access token de admin para cualquier
	// tenant. En dev se avisa y se sigue (los defaults existen para que `go run`
	// funcione); fuera de dev el servidor no arranca.
	if err := checkSecrets(cfg); err != nil {
		log.Fatalf("[FATAL] %v", err)
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
	if cfg.Seed {
		seedDefaultUsers(ctx)
	}
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

	// Write tenant ID to file for easy reference. Best-effort: in a container the
	// working directory is not writable by the unprivileged user, and failing to
	// write a convenience file must not take the server down.
	if err := os.WriteFile(".default-tenant-id", []byte(tenantID), 0644); err != nil {
		log.Printf("[WARN] Could not write .default-tenant-id (%v); use the tenants table instead", err)
	}
}

// healthcheck consulta /health al servidor local. Distingue "responde" de
// "está sano": si el proceso acepta conexiones pero la base no, un chequeo que
// solo mira el socket lo daría por bueno.
func healthcheck() error {
	port := os.Getenv("FASTERP_PORT")
	if port == "" {
		port = "7071"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/readyz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/readyz devolvió %d", resp.StatusCode)
	}
	return nil
}

// checkSecrets refuses to start with a guessable signing key. The HMAC secret is
// what stands between "read the README" and "administer any tenant on this
// instance", so a default is treated as a misconfiguration, not a warning.
func checkSecrets(cfg *config.Config) error {
	var weak []string
	if config.ValidateSecretWeak(cfg.JWTSecret) {
		weak = append(weak, "FASTERP_JWT_SECRET")
	}
	if config.ValidateSecretWeak(cfg.JWTRefreshSecret) {
		weak = append(weak, "FASTERP_JWT_REFRESH_SECRET")
	}
	if cfg.JWTSecret == cfg.JWTRefreshSecret {
		weak = append(weak, "FASTERP_JWT_REFRESH_SECRET (= FASTERP_JWT_SECRET)")
	}

	if len(weak) == 0 {
		return nil
	}

	msg := fmt.Sprintf("secretos de firma.mk debil o por defecto: %s", strings.Join(weak, ", "))
	if cfg.Dev {
		log.Printf("[WARN] %s; arranca solo porque FASTERP_DEV=true", msg)
		return nil
	}
	return fmt.Errorf("%s (genera uno con: openssl rand -hex 32. Para desarrollo, FASTERP_DEV=true)", msg)
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

		// Password read from the environment on purpose. The previous build wrote a
		// literal "admin123" that any scanner finds within seconds of exposing the
		// port. With FASTERP_SEED off, the operator creates the first account through
		// the setup wizard instead and no password is ever baked into the binary.
		initialPassword := os.Getenv("FASTERP_ADMIN_PASSWORD")
		if initialPassword == "" {
			return errors.New("no users exist and FASTERP_ADMIN_PASSWORD is unset: set it, or run with FASTERP_SEED=false and create the first account from /setup")
		}
		if len(initialPassword) < 12 {
			return fmt.Errorf("FASTERP_ADMIN_PASSWORD is too short (%d chars, need 12)", len(initialPassword))
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(initialPassword), bcrypt.DefaultCost)
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
		log.Println("[Seed] Default user created: admin / <FASTERP_ADMIN_PASSWORD>")
		log.Printf("[Seed] Tenant ID: %s (use as X-Tenant-ID header)", defaultTenantID)
		return nil
	})
	if err != nil {
		log.Fatalf("[FATAL] Failed to seed default user: %v", err)
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

// loadInstalledModules carga los módulos del directorio y aplica su esquema.
//
// El registro es del proceso, no del tenant: el esquema de un módulo son tablas
// con RLS por tenant_id, no datos. Así que no hace falta recorrer los tenants
// para instalarlos — instalar es crear las tablas una vez, y lo decide qué tenant
// tiene el módulo activo, no qué tablas existen.
func loadInstalledModules(ctx context.Context, mgr *module.ModuleManager) {
	if err := mgr.LoadAll(ctx); err != nil {
		log.Printf("[WARN] %v", err)
	}
}
