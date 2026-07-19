package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fasterp/backend/db"
)

type ModuleHandler struct {
	dbConn *db.DB
}

type ModuleInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Installed   bool   `json:"installed"`
	Active      bool   `json:"active"`
	Depends     []string `json:"depends,omitempty"`
}

func NewModuleHandler(dbConn *db.DB) *ModuleHandler {
	return &ModuleHandler{
		dbConn: dbConn,
	}
}

// GetAvailableModules devuelve módulos disponibles y su estado
func (mh *ModuleHandler) GetAvailableModules(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantID := r.Header.Get("X-Tenant-ID")

	// Módulos disponibles en el sistema (hardcoded, en futuro serían escaneados del filesystem)
	available := []ModuleInfo{
		{
			ID:          "contacts",
			Name:        "contacts",
			Label:       "Contacts",
			Version:     "1.0.0",
			Author:      "FastERP",
			Description: "Gestión de contactos, clientes y proveedores",
			Icon:        "👥",
			Depends:     []string{},
		},
		{
			ID:          "tienda_web",
			Name:        "tienda_web",
			Label:       "Store",
			Version:     "1.0.0",
			Author:      "FastERP",
			Description: "E-commerce y tienda online",
			Icon:        "🛍️",
			Depends:     []string{},
		},
		{
			ID:          "sitio_web",
			Name:        "sitio_web",
			Label:       "Website",
			Version:     "1.0.0",
			Author:      "FastERP",
			Description: "CMS para crear sitios web",
			Icon:        "🌐",
			Depends:     []string{},
		},
		{
			ID:          "sites",
			Name:        "sites",
			Label:       "Sites",
			Version:     "1.0.0",
			Author:      "FastERP",
			Description: "Gestión de múltiples sitios",
			Icon:        "📍",
			Depends:     []string{},
		},
		{
			ID:          "products",
			Name:        "products",
			Label:       "Products",
			Version:     "1.0.0",
			Author:      "FastERP",
			Description: "Catálogo de productos",
			Icon:        "📦",
			Depends:     []string{},
		},
		{
			ID:          "integracion_odoo_fasterp",
			Name:        "integracion_odoo_fasterp",
			Label:       "Odoo Integration",
			Version:     "1.0.0",
			Author:      "FastERP",
			Description: "Sincronización con Odoo ERP",
			Icon:        "🔗",
			Depends:     []string{},
		},
	}

	// Obtener módulos instalados
	query := `
		SELECT name, active FROM installed_modules
		WHERE tenant_id = $1::uuid
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
	tenantID := r.Header.Get("X-Tenant-ID")
	moduleName := r.URL.Query().Get("name")

	if moduleName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Module name required",
		})
		return
	}

	// Validar que existe
	available := map[string]ModuleInfo{
		"contacts":                    {Name: "contacts", Label: "Contacts"},
		"tienda_web":                  {Name: "tienda_web", Label: "Store"},
		"sitio_web":                   {Name: "sitio_web", Label: "Website"},
		"sites":                        {Name: "sites", Label: "Sites"},
		"products":                     {Name: "products", Label: "Products"},
		"integracion_odoo_fasterp":    {Name: "integracion_odoo_fasterp", Label: "Odoo Integration"},
	}

	if _, exists := available[moduleName]; !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Module not found",
		})
		return
	}

	// Verificar si ya está instalado
	row := mh.dbConn.QueryRow(ctx, "SELECT COUNT(*) FROM installed_modules WHERE tenant_id = $1 AND name = $2", tenantID, moduleName)
	var count int
	row.Scan(&count)

	if count > 0 {
		// Ya existe, solo activar
		query := `UPDATE installed_modules SET active = true, updated_at = CURRENT_TIMESTAMP WHERE tenant_id = $1 AND name = $2`
		mh.dbConn.Exec(ctx, query, tenantID, moduleName)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Module %s activated", moduleName),
		})
		return
	}

	// Insertar nuevo
	moduleID := generateUUID()
	query := `
		INSERT INTO installed_modules (id, tenant_id, name, version, label, active, installed_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	_, err := mh.dbConn.Exec(ctx, query, moduleID, tenantID, moduleName, "1.0.0", available[moduleName].Label)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error installing module: %v", err),
		})
		return
	}

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
	tenantID := r.Header.Get("X-Tenant-ID")
	moduleName := r.URL.Query().Get("name")

	if moduleName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Module name required",
		})
		return
	}

	// Desactivar
	query := `UPDATE installed_modules SET active = false, updated_at = CURRENT_TIMESTAMP WHERE tenant_id = $1 AND name = $2`
	_, err := mh.dbConn.Exec(ctx, query, tenantID, moduleName)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error uninstalling module: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Module %s deactivated", moduleName),
	})
}
