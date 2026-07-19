package main

import (
	"encoding/json"
	"unsafe"
)

// ============================================================================
// Módulo: Tienda Web (mini-tienda tipo Amazon)
//
// Enriquece los productos sincronizados desde Odoo (imported_product) con datos
// propios de la tienda web: nombre para mostrar, descripción, precio de tienda,
// publicado, y una galería de imágenes tipo Amazon.
//
// La subida/servido de imágenes y el "merge" con los productos de Odoo lo hace
// el backend nativo (internal/store), porque el sandbox WASI no maneja archivos
// ni red. Este módulo solo declara las tablas y el menú.
//
// Tablas generadas:
//   - mod_tienda_web_store_product   (overrides por producto)
//   - mod_tienda_web_store_image      (galería de imágenes)
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
	Name:        "tienda_web",
	Version:     "1.0.0",
	Label:       "Tienda Web",
	Description: "Mini-tienda para gestionar productos sincronizados: nombre, descripción y galería de imágenes tipo Amazon",
	Author:      "Ecuabyte",
	Icon:        "store",
	Models: []ModelDef{
		{
			Name:  "store_product",
			Label: "Producto de tienda",
			Fields: []FieldDef{
				{Name: "odoo_product_id", Type: "integer", Label: "ID Producto Odoo", Required: true},
				{Name: "name", Type: "string", Label: "Nombre para mostrar"},
				{Name: "description", Type: "text", Label: "Descripción"},
				{Name: "price", Type: "float", Label: "Precio de tienda"},
				{Name: "published", Type: "boolean", Label: "Publicado"},
				{Name: "featured", Type: "boolean", Label: "Destacado"},
			},
		},
		{
			Name:  "store_image",
			Label: "Imagen de producto",
			Fields: []FieldDef{
				{Name: "odoo_product_id", Type: "integer", Label: "ID Producto Odoo", Required: true},
				{Name: "url", Type: "string", Label: "URL", Required: true},
				{Name: "position", Type: "integer", Label: "Orden"},
				{Name: "alt", Type: "string", Label: "Texto alternativo"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Tienda Web", Icon: "store", Route: "/tienda", Seq: 130},
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
