package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"

	"github.com/fasterp/backend/db"
)

type ModuleHandler struct {
	dbConn *db.DB
}

type ModuleInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Version     string   `json:"version"`
	Author      string   `json:"author"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Installed   bool     `json:"installed"`
	Active      bool     `json:"active"`
	Depends     []string `json:"depends,omitempty"`
}

func NewModuleHandler(dbConn *db.DB) *ModuleHandler {
	return &ModuleHandler{
		dbConn: dbConn,
	}
}

// loadModulesFromFilesystem lee los manifiestos de módulos del filesystem
func (mh *ModuleHandler) loadModulesFromFilesystem() []ModuleInfo {
	var modules []ModuleInfo
	modulesDir := "../modules"

	// Leer directorio de módulos
	entries, err := ioutil.ReadDir(modulesDir)
	if err != nil {
		fmt.Printf("[DEBUG] Error reading modules directory: %v\n", err)
		return modules
	}

	// Para cada directorio, buscar manifest.json
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(modulesDir, entry.Name(), "manifest.json")
		data, err := ioutil.ReadFile(manifestPath)
		if err != nil {
			fmt.Printf("[DEBUG] No manifest found for %s: %v\n", entry.Name(), err)
			continue
		}

		// Parsear manifest.json
		var manifest map[string]interface{}
		if err := json.Unmarshal(data, &manifest); err != nil {
			fmt.Printf("[DEBUG] Error parsing manifest for %s: %v\n", entry.Name(), err)
			continue
		}

		// Construir ModuleInfo desde el manifest
		module := ModuleInfo{
			ID:   entry.Name(),
			Name: entry.Name(),
		}

		// Extraer campos del manifest
		if label, ok := manifest["label"].(string); ok {
			module.Label = label
		} else {
			module.Label = entry.Name()
		}

		if version, ok := manifest["version"].(string); ok {
			module.Version = version
		} else {
			module.Version = "1.0.0"
		}

		if author, ok := manifest["author"].(string); ok {
			module.Author = author
		}

		if description, ok := manifest["description"].(string); ok {
			module.Description = description
		}

		if icon, ok := manifest["icon"].(string); ok {
			module.Icon = icon
		} else {
			module.Icon = "📦"
		}

		// Extraer dependencias si existen
		if depends, ok := manifest["depends"].([]interface{}); ok {
			for _, dep := range depends {
				if depStr, ok := dep.(string); ok {
					module.Depends = append(module.Depends, depStr)
				}
			}
		}

		modules = append(modules, module)
	}

	return modules
}

// GetAvailableModules devuelve módulos disponibles y su estado
func (mh *ModuleHandler) GetAvailableModules(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantSlug := r.Header.Get("X-Tenant-ID")

	// Resolver slug a UUID si es necesario
	tenantID, err := mh.resolveTenantID(ctx, tenantSlug)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Invalid tenant: %v", err),
		})
		return
	}

	// Leer módulos desde el filesystem
	available := mh.loadModulesFromFilesystem()

	// Obtener módulos instalados
	query := `
		SELECT name, active FROM installed_modules
		WHERE tenant_id = $1
	`

	rows, err := mh.dbConn.Query(ctx, query, tenantID)

	installed := make(map[string]bool)
	active := make(map[string]bool)

	// Si hay error en la query, continuamos sin los datos de installed
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var isActive bool
			if err := rows.Scan(&name, &isActive); err == nil {
				installed[name] = true
				active[name] = isActive
			}
		}
	}

	// Marcar estado en disponibles
	for i := range available {
		available[i].Installed = installed[available[i].ID]
		available[i].Active = active[available[i].ID]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"modules": available,
		"total":   len(available),
	})
}

// InstallModule instala/activa un módulo
func (mh *ModuleHandler) InstallModule(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantSlug := r.Header.Get("X-Tenant-ID")

	// Leer JSON body
	var payload struct {
		ModuleName string `json:"module_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid JSON",
		})
		return
	}

	moduleName := payload.ModuleName

	// Resolver slug a UUID si es necesario
	tenantID, err := mh.resolveTenantID(ctx, tenantSlug)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Invalid tenant: %v", err),
		})
		return
	}

	fmt.Printf("[InstallModule] Starting - tenantID: %s (slug: %s), moduleName: %s\n", tenantID, tenantSlug, moduleName)

	if moduleName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Module name required",
		})
		return
	}

	// Cargar módulos disponibles y validar que existe
	availableModules := mh.loadModulesFromFilesystem()
	fmt.Printf("[InstallModule] Loaded %d available modules\n", len(availableModules))
	var module ModuleInfo
	var found bool

	for _, m := range availableModules {
		if m.Name == moduleName {
			module = m
			found = true
			break
		}
	}

	if !found {
		fmt.Printf("[InstallModule] Module %s not found\n", moduleName)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Module not found",
		})
		return
	}
	fmt.Printf("[InstallModule] Module found: %s v%s\n", module.Label, module.Version)

	// Verificar si ya está instalado
	fmt.Printf("[InstallModule] Checking if module already installed...\n")
	row := mh.dbConn.QueryRow(ctx, "SELECT COUNT(*) FROM installed_modules WHERE tenant_id = $1 AND name = $2", tenantID, moduleName)
	var count int
	if scanErr := row.Scan(&count); scanErr != nil {
		fmt.Printf("[InstallModule] Error checking installation: %v\n", scanErr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Database error: %v", scanErr),
		})
		return
	}

	if count > 0 {
		fmt.Printf("[InstallModule] Module already installed, activating...\n")
		// Ya existe, solo activar
		query := `UPDATE installed_modules SET active = true, updated_at = CURRENT_TIMESTAMP WHERE tenant_id = $1 AND name = $2`
		if _, activateErr := mh.dbConn.Exec(ctx, query, tenantID, moduleName); activateErr != nil {
			fmt.Printf("[InstallModule] Error activating module: %v\n", activateErr)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Module %s activated", moduleName),
		})
		return
	}

	// Insertar nuevo
	fmt.Printf("[InstallModule] Inserting new module...\n")
	moduleID := generateUUID()
	query := `
		INSERT INTO installed_modules (id, tenant_id, name, version, label, active, installed_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	fmt.Printf("[InstallModule] SQL Params - ID: %s, TenantID: %s, Name: %s, Version: %s, Label: %s\n",
		moduleID, tenantID, moduleName, module.Version, module.Label)

	if _, insertErr := mh.dbConn.Exec(ctx, query, moduleID, tenantID, moduleName, module.Version, module.Label); insertErr != nil {
		fmt.Printf("[InstallModule] Error installing module: %v\n", insertErr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error installing module: %v", insertErr),
		})
		return
	}
	fmt.Printf("[InstallModule] Module installed successfully\n")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Module %s installed successfully", moduleName),
	})
}

// UninstallModule desactiva/desinstala un módulo
func (mh *ModuleHandler) UninstallModule(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantSlug := r.Header.Get("X-Tenant-ID")

	// Leer JSON body
	var payload struct {
		ModuleName string `json:"module_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid JSON",
		})
		return
	}

	moduleName := payload.ModuleName

	// Resolver slug a UUID si es necesario
	tenantID, err := mh.resolveTenantID(ctx, tenantSlug)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Invalid tenant: %v", err),
		})
		return
	}

	if moduleName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Module name required",
		})
		return
	}

	// Eliminar completamente
	fmt.Printf("[UninstallModule] Deleting module %s for tenant %s\n", moduleName, tenantID)
	query := `DELETE FROM installed_modules WHERE tenant_id = $1 AND name = $2`
	if _, execErr := mh.dbConn.Exec(ctx, query, tenantID, moduleName); execErr != nil {
		fmt.Printf("[UninstallModule] Error: %v\n", execErr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error uninstalling module: %v", execErr),
		})
		return
	}

	fmt.Printf("[UninstallModule] Module deleted successfully\n")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Module %s uninstalled successfully", moduleName),
	})
}

// resolveTenantID convierte un slug o UUID a UUID real
func (mh *ModuleHandler) resolveTenantID(ctx context.Context, tenantSlugOrID string) (string, error) {
	// Intenta primero como UUID directo
	row := mh.dbConn.QueryRow(ctx, "SELECT id FROM tenants WHERE id = $1 LIMIT 1", tenantSlugOrID)
	var tenantID string
	if err := row.Scan(&tenantID); err == nil {
		return tenantID, nil
	}

	// Si no es UUID, intenta como slug
	row = mh.dbConn.QueryRow(ctx, "SELECT id FROM tenants WHERE slug = $1 LIMIT 1", tenantSlugOrID)
	if err := row.Scan(&tenantID); err != nil {
		return "", fmt.Errorf("tenant not found: %s", tenantSlugOrID)
	}

	return tenantID, nil
}
