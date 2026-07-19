package main

import (
	"encoding/json"
	"unsafe"
)

// ============================================================================
// TIPOS DE DATOS
// ============================================================================

type FieldDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

type ModelDef struct {
	Name   string     `json:"name"`
	Label  string     `json:"label"`
	Fields []FieldDef `json:"fields"`
}

type MenuDef struct {
	Label string `json:"label"`
	Icon  string `json:"icon"`
	Route string `json:"route"`
	Seq   int    `json:"seq"`
}

type Manifest struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Label       string     `json:"label"`
	Description string     `json:"description,omitempty"`
	Author      string     `json:"author,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	Models      []ModelDef `json:"models"`
	Menus       []MenuDef  `json:"menus"`
}

// ============================================================================
// MANIFEST DE MÓDULO
// ============================================================================

var manifest = Manifest{
	Name:        "odoo_sync",
	Version:     "1.0.0",
	Label:       "Odoo Sync",
	Description: "Sincronización bidireccional con Odoo: productos, precios, leads, quotations",
	Author:      "FastERP Solar Team",
	Icon:        "refresh-cw",
	Models: []ModelDef{
		{
			Name:  "odoo_config",
			Label: "Odoo Configuration",
			Fields: []FieldDef{
				{Name: "odoo_url", Type: "string", Label: "Odoo URL (https://odoo.example.com)", Required: true},
				{Name: "odoo_db", Type: "string", Label: "Database Name", Required: true},
				{Name: "odoo_username", Type: "string", Label: "Username", Required: true},
				{Name: "odoo_password", Type: "string", Label: "Password", Required: true},
				{Name: "api_key", Type: "string", Label: "API Key (if using token auth)"},
				{Name: "last_sync", Type: "datetime", Label: "Last Sync Time"},
				{Name: "sync_enabled", Type: "boolean", Label: "Enable Auto-sync"},
				{Name: "sync_interval_minutes", Type: "integer", Label: "Sync Interval (minutes)"},
			},
		},
		{
			Name:  "odoo_product_map",
			Label: "Odoo Product Mapping",
			Fields: []FieldDef{
				{Name: "odoo_product_id", Type: "integer", Label: "Odoo Product ID", Required: true},
				{Name: "local_product_id", Type: "string", Label: "Local Product ID", Required: true},
				{Name: "odoo_name", Type: "string", Label: "Odoo Product Name"},
				{Name: "odoo_sku", Type: "string", Label: "SKU"},
				{Name: "last_synced", Type: "datetime", Label: "Last Sync"},
				{Name: "sync_status", Type: "string", Label: "Status (active|inactive|error)"},
			},
		},
		{
			Name:  "sync_log",
			Label: "Sync Log",
			Fields: []FieldDef{
				{Name: "sync_type", Type: "string", Label: "Type (products|prices|inventory|leads|quotes)", Required: true},
				{Name: "status", Type: "string", Label: "Status (success|error)", Required: true},
				{Name: "records_synced", Type: "integer", Label: "Records Synced"},
				{Name: "error_message", Type: "text", Label: "Error Message"},
				{Name: "sync_timestamp", Type: "datetime", Label: "Sync Time"},
				{Name: "duration_seconds", Type: "decimal", Label: "Duration (seconds)"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Odoo Sync", Icon: "refresh-cw", Route: "/odoo-sync", Seq: 100},
		{Label: "Configuration", Icon: "settings", Route: "/odoo-sync/config", Seq: 101},
		{Label: "Product Mapping", Icon: "link", Route: "/odoo-sync/mapping", Seq: 102},
		{Label: "Sync Logs", Icon: "activity", Route: "/odoo-sync/logs", Seq: 103},
		{Label: "Manual Sync", Icon: "play", Route: "/odoo-sync/manual", Seq: 104},
	},
}

// ============================================================================
// ESTRUCTURAS DE DATOS PARA ODOO
// ============================================================================

// OdooProduct - Estructura que viene de Odoo
type OdooProduct struct {
	ID               int     `json:"id"`
	Name             string  `json:"name"`
	SKU              string  `json:"default_code"`
	ListPrice        float64 `json:"list_price"`
	StandardPrice    float64 `json:"standard_price"`
	Category         string  `json:"categ_id"`
	Quantity         float64 `json:"qty_available"`
	Description      string  `json:"description"`
	Active           bool    `json:"active"`
	DefaultCode      string  `json:"default_code"`
}

// SyncResponse - Respuesta de sincronización
type SyncResponse struct {
	Status           string `json:"status"`                    // success | error
	Message          string `json:"message"`
	RecordsSynced    int    `json:"records_synced"`
	RecordsFailed    int    `json:"records_failed"`
	DurationSeconds  float64 `json:"duration_seconds"`
	NextSyncTime     string `json:"next_sync_time"`
	Products         []OdooProduct `json:"products,omitempty"`
}

// ProductsListResponse - Respuesta para listar productos
type ProductsListResponse struct {
	Products []struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		Brand    string  `json:"brand"`
		Category string  `json:"category"`
		Price    float64 `json:"price"`
		Stock    int     `json:"stock"`
		OdooID   int     `json:"odoo_id"`
	} `json:"products"`
	Total int `json:"total"`
}

// ============================================================================
// BUFFER PARA RESPUESTAS
// ============================================================================

var resultBuf [65536]byte

// ============================================================================
// FUNCIONES EXPORTADAS WASM
// ============================================================================

//go:wasmexport fasterp_get_manifest
func fasterp_get_manifest() uint64 {
	data, _ := json.Marshal(manifest)
	n := copy(resultBuf[:], data)
	return uint64(uintptr(unsafe.Pointer(&resultBuf[0])))<<32 | uint64(n)
}

//go:wasmexport fasterp_on_load
func fasterp_on_load() uint32 {
	// Al cargar el módulo, se puede iniciar sincronización automática si está habilitada
	return 0
}

//go:wasmexport fasterp_on_unload
func fasterp_on_unload() uint32 {
	return 0
}

// ============================================================================
// FUNCIONES DE API (Ejemplos - Backend las implementa)
// ============================================================================

// Nota: Las funciones reales serían:
// - sync_products_from_odoo()       // Trae productos desde Odoo
// - sync_prices_from_odoo()         // Actualiza precios
// - sync_inventory_from_odoo()      // Actualiza stock
// - push_quotation_to_odoo()        // Crea quote en Odoo
// - create_lead_in_odoo()           // Crea lead en Odoo
// - webhook_handler()               // Escucha cambios en Odoo

type SyncRequest struct {
	SyncType string `json:"sync_type"` // products | prices | inventory | all
	Force    bool   `json:"force"`     // Ignorar timestamp del último sync
}

type WebhookEvent struct {
	EventType string `json:"event_type"` // product.updated | price.changed | etc
	Data      string `json:"data"`
	Timestamp string `json:"timestamp"`
}

func main() {}
