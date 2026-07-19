package main

import (
	"encoding/json"
	"unsafe"
)

// ============================================================================
// Módulo: Integración Odoo <-> FastERP
//
// Este módulo WASM SOLO declara los datos (modelos -> tablas), menús y páginas.
// La lógica de red con Odoo (JSON-RPC) vive en el backend nativo, en el paquete
// internal/odoo, porque el sandbox WASI no tiene acceso a red.
//
// Tablas generadas (mod_<modulo>_<modelo>):
//   - mod_integracion_odoo_fasterp_odoo_connection
//   - mod_integracion_odoo_fasterp_imported_product
//   - mod_integracion_odoo_fasterp_sync_order
//   - mod_integracion_odoo_fasterp_sync_lead
//   - mod_integracion_odoo_fasterp_sync_history
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

var manifest = Manifest{
	Name:        "integracion_odoo_fasterp",
	Version:     "1.0.0",
	Label:       "Integración Odoo",
	Description: "Sincronización bidireccional de productos, pedidos, leads y stock entre Odoo y FastERP",
	Author:      "Ecuabyte",
	Icon:        "refresh-cw",
	Models: []ModelDef{
		{
			Name:  "odoo_connection",
			Label: "Conexión Odoo",
			Fields: []FieldDef{
				{Name: "name", Type: "string", Label: "Nombre de la conexión"},
				{Name: "odoo_url", Type: "string", Label: "URL de Odoo (http://localhost:8073)", Required: true},
				{Name: "odoo_database", Type: "string", Label: "Base de datos", Required: true},
				{Name: "odoo_username", Type: "string", Label: "Usuario (login)", Required: true},
				{Name: "odoo_password", Type: "text", Label: "API Key / Token (o contraseña)", Required: true},
				{Name: "uid", Type: "integer", Label: "UID (auto)"},
				{Name: "active", Type: "boolean", Label: "Activa"},
				{Name: "last_sync", Type: "datetime", Label: "Última sincronización"},
				{Name: "last_error", Type: "text", Label: "Último error"},
			},
		},
		{
			Name:  "imported_product",
			Label: "Producto importado",
			Fields: []FieldDef{
				{Name: "odoo_product_id", Type: "integer", Label: "ID Producto Odoo", Required: true},
				{Name: "odoo_tmpl_id", Type: "integer", Label: "ID Plantilla Odoo"},
				{Name: "name", Type: "string", Label: "Nombre", Required: true},
				{Name: "default_code", Type: "string", Label: "SKU / Referencia"},
				{Name: "barcode", Type: "string", Label: "Código de barras"},
				{Name: "category", Type: "string", Label: "Categoría"},
				{Name: "list_price", Type: "float", Label: "Precio de venta"},
				{Name: "standard_price", Type: "float", Label: "Costo"},
				{Name: "qty_available", Type: "float", Label: "Stock disponible"},
				{Name: "uom", Type: "string", Label: "Unidad de medida"},
				{Name: "description", Type: "text", Label: "Descripción"},
				{Name: "active", Type: "boolean", Label: "Activo"},
				{Name: "image", Type: "text", Label: "Imagen (data URI)"},
				{Name: "last_synced", Type: "datetime", Label: "Última sincronización"},
				{Name: "sync_status", Type: "string", Label: "Estado"},
			},
		},
		{
			Name:  "sync_order",
			Label: "Pedido a Odoo",
			Fields: []FieldDef{
				{Name: "partner_name", Type: "string", Label: "Cliente"},
				{Name: "partner_email", Type: "string", Label: "Email"},
				{Name: "amount_total", Type: "float", Label: "Total"},
				{Name: "lines_json", Type: "text", Label: "Líneas (JSON)"},
				{Name: "odoo_order_id", Type: "integer", Label: "ID Pedido Odoo"},
				{Name: "state", Type: "string", Label: "Estado"},
				{Name: "push_status", Type: "string", Label: "Estado de envío"},
				{Name: "error_message", Type: "text", Label: "Error"},
				{Name: "pushed_at", Type: "datetime", Label: "Enviado el"},
			},
		},
		{
			Name:  "sync_lead",
			Label: "Lead a Odoo",
			Fields: []FieldDef{
				{Name: "name", Type: "string", Label: "Título"},
				{Name: "contact_name", Type: "string", Label: "Contacto"},
				{Name: "email", Type: "string", Label: "Email"},
				{Name: "phone", Type: "string", Label: "Teléfono"},
				{Name: "description", Type: "text", Label: "Descripción"},
				{Name: "odoo_lead_id", Type: "integer", Label: "ID Lead Odoo"},
				{Name: "push_status", Type: "string", Label: "Estado de envío"},
				{Name: "error_message", Type: "text", Label: "Error"},
				{Name: "pushed_at", Type: "datetime", Label: "Enviado el"},
			},
		},
		{
			Name:  "sync_history",
			Label: "Historial de sincronización",
			Fields: []FieldDef{
				{Name: "direction", Type: "string", Label: "Dirección (pull|push)"},
				{Name: "entity", Type: "string", Label: "Entidad"},
				{Name: "status", Type: "string", Label: "Estado"},
				{Name: "records_count", Type: "integer", Label: "Registros"},
				{Name: "error_message", Type: "text", Label: "Error"},
				{Name: "duration_seconds", Type: "float", Label: "Duración (s)"},
				{Name: "sync_timestamp", Type: "datetime", Label: "Fecha"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Integración Odoo", Icon: "refresh-cw", Route: "/odoo", Seq: 120},
	},
}

var resultBuf [65536]byte

//go:wasmexport fasterp_get_manifest
func fasterp_get_manifest() uint64 {
	data, _ := json.Marshal(manifest)
	n := copy(resultBuf[:], data)
	return uint64(uintptr(unsafe.Pointer(&resultBuf[0])))<<32 | uint64(n)
}

//go:wasmexport fasterp_on_load
func fasterp_on_load() uint32 { return 0 }

//go:wasmexport fasterp_on_unload
func fasterp_on_unload() uint32 { return 0 }

func main() {}
