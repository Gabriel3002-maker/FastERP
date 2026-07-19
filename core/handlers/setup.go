package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/fasterp/backend/db"
)

type SetupHandler struct {
	dbConn *db.DB
}

func NewSetupHandler(dbConn *db.DB) *SetupHandler {
	return &SetupHandler{
		dbConn: dbConn,
	}
}

// CheckSetup verifica si el sistema ya fue configurado
func (sh *SetupHandler) CheckSetup(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// Contar tenants
	row := sh.dbConn.QueryRow(ctx, "SELECT COUNT(*) FROM tenants")
	var count int
	row.Scan(&count)

	w.Header().Set("Content-Type", "application/json")
	if count == 0 {
		// No hay tenants, mostrar setup
		json.NewEncoder(w).Encode(map[string]interface{}{
			"setup_required": true,
			"message":        "System needs configuration",
		})
	} else {
		// Ya hay tenants, sistema configurado
		json.NewEncoder(w).Encode(map[string]interface{}{
			"setup_required": false,
			"message":        "System already configured",
		})
	}
}

// ShowSetupWizard renderiza la página del wizard
func (sh *SetupHandler) ShowSetupWizard(renderer *TemplateRenderer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		// Verificar si ya hay tenants
		row := sh.dbConn.QueryRow(ctx, "SELECT COUNT(*) FROM tenants")
		var count int
		row.Scan(&count)

		if count > 0 {
			// Ya configurado, redirigir a login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Mostrar wizard
		renderer.RenderFile("setup-wizard.html", nil, w)
	}
}

type SetupRequest struct {
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
	AdminUser  string `json:"admin_user"`
	AdminEmail string `json:"admin_email"`
	Password   string `json:"password"`
	Confirm    string `json:"confirm"`
}

type SetupResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// CreateInitialSetup crea el tenant inicial y admin
func (sh *SetupHandler) CreateInitialSetup(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// Verificar si ya hay tenants
	row := sh.dbConn.QueryRow(ctx, "SELECT COUNT(*) FROM tenants")
	var count int
	row.Scan(&count)

	if count > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "System already configured",
		})
		return
	}

	var req SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "Invalid request",
		})
		return
	}

	// Validar
	if req.TenantName == "" || req.TenantSlug == "" || req.AdminUser == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "All fields are required",
		})
		return
	}

	if req.Password != req.Confirm {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "Passwords do not match",
		})
		return
	}

	if len(req.Password) < 8 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "Password must be at least 8 characters",
		})
		return
	}

	// Generar IDs
	tenantID := generateUUID()
	userID := generateUUID()

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "Error hashing password",
		})
		return
	}

	// Crear tenant
	tenantQuery := `
		INSERT INTO tenants (id, name, slug, active, created_at, updated_at)
		VALUES ($1, $2, $3, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	if _, err := sh.dbConn.Exec(ctx, tenantQuery, tenantID, req.TenantName, req.TenantSlug); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   fmt.Sprintf("Error creating tenant: %v", err),
		})
		return
	}

	// Crear admin user
	userQuery := `
		INSERT INTO users (id, tenant_id, username, email, password_hash, is_admin, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	if _, err := sh.dbConn.Exec(ctx, userQuery, userID, tenantID, req.AdminUser, req.AdminEmail, string(hashedPassword)); err != nil {
		// Intentar rollback (delete tenant)
		sh.dbConn.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   fmt.Sprintf("Error creating admin user: %v", err),
		})
		return
	}

	// Éxito
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(SetupResponse{
		Success: true,
		Message: fmt.Sprintf("Setup complete! Tenant: %s, Admin: %s", req.TenantName, req.AdminUser),
	})
}

// CreateNewTenant crea un nuevo tenant (solo para admins)
func (sh *SetupHandler) CreateNewTenant(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	var req SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "Invalid request",
		})
		return
	}

	// Validar
	if req.TenantName == "" || req.TenantSlug == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "Tenant name and slug required",
		})
		return
	}

	// Verificar que el slug sea único
	row := sh.dbConn.QueryRow(ctx, "SELECT COUNT(*) FROM tenants WHERE slug = $1", req.TenantSlug)
	var count int
	row.Scan(&count)

	if count > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   "Tenant slug already exists",
		})
		return
	}

	tenantID := generateUUID()

	query := `
		INSERT INTO tenants (id, name, slug, active, created_at, updated_at)
		VALUES ($1, $2, $3, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	if _, err := sh.dbConn.Exec(ctx, query, tenantID, req.TenantName, req.TenantSlug); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(SetupResponse{
			Success: false,
			Error:   fmt.Sprintf("Error creating tenant: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(SetupResponse{
		Success: true,
		Message: fmt.Sprintf("Tenant %s created successfully (ID: %s)", req.TenantName, tenantID),
	})
}

// Helper functions

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generateSlug(name string) string {
	// Convertir a minúsculas
	slug := strings.ToLower(name)
	// Reemplazar espacios con guiones
	slug = strings.ReplaceAll(slug, " ", "-")
	// Remover caracteres especiales
	var result strings.Builder
	for _, char := range slug {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			result.WriteRune(char)
		}
	}
	return result.String()
}
