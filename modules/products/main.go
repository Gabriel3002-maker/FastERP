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
	Name:        "products",
	Version:     "1.0.0",
	Label:       "Solar Products",
	Description: "Gestión de catálogo de productos solares: paneles, inversores, estructuras",
	Author:      "FastERP Solar Team",
	Icon:        "box",
	Models: []ModelDef{
		{
			Name:  "product",
			Label: "Product",
			Fields: []FieldDef{
				{Name: "name", Type: "string", Label: "Product Name", Required: true},
				{Name: "category", Type: "string", Label: "Category (panel|inverter|structure|protection)", Required: true},
				{Name: "brand", Type: "string", Label: "Brand", Required: true},
				{Name: "model", Type: "string", Label: "Model", Required: true},
				{Name: "description", Type: "text", Label: "Description"},
				{Name: "specification", Type: "string", Label: "Technical Specs (JSON)"},
				{Name: "price", Type: "decimal", Label: "Price (USD)", Required: true},
				{Name: "cost", Type: "decimal", Label: "Cost (USD)"},
				{Name: "stock", Type: "integer", Label: "Stock Quantity"},
				{Name: "sync_with_odoo", Type: "boolean", Label: "Sync with Odoo"},
				{Name: "odoo_product_id", Type: "string", Label: "Odoo Product ID"},
			},
		},
		{
			Name:  "product_price",
			Label: "Product Price History",
			Fields: []FieldDef{
				{Name: "product_id", Type: "string", Label: "Product ID", Required: true},
				{Name: "price", Type: "decimal", Label: "Price", Required: true},
				{Name: "date", Type: "date", Label: "Date", Required: true},
				{Name: "source", Type: "string", Label: "Source (manual|odoo_sync)"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Solar Products", Icon: "box", Route: "/products", Seq: 30},
		{Label: "Panels", Icon: "sun", Route: "/products?category=panel", Seq: 31},
		{Label: "Inverters", Icon: "zap", Route: "/products?category=inverter", Seq: 32},
		{Label: "Structures", Icon: "package", Route: "/products?category=structure", Seq: 33},
	},
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
	// Inicialización: se pueden agregar validaciones, logs, etc.
	// Por ahora retornamos 0 (éxito)
	return 0
}

//go:wasmexport fasterp_on_unload
func fasterp_on_unload() uint32 {
	// Limpieza: liberar recursos si es necesario
	return 0
}

// ============================================================================
// FUNCIONES DE NEGOCIO (Ejemplos de API)
// ============================================================================

// Estructura para respuesta de productos
type ProductResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Brand          string `json:"brand"`
	Model          string `json:"model"`
	Price          float64 `json:"price"`
	Stock          int    `json:"stock"`
	Available      bool   `json:"available"`
	SpecJSON       string `json:"specification,omitempty"`
}

type ListProductsRequest struct {
	Category string `json:"category,omitempty"`
	Brand    string `json:"brand,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type ListProductsResponse struct {
	Products []ProductResponse `json:"products"`
	Total    int              `json:"total"`
}

// Nota: Las funciones reales de API (list_products, get_product, etc.)
// serían manejadas por el backend Go que interpreta estos datos.
// Este módulo WASM sirve como definición de datos y validaciones.

func main() {}
