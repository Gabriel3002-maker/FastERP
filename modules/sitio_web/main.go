package main

import (
	"encoding/json"
	"unsafe"
)

// ============================================================================
// Módulo: Sitio Web (CMS tipo WordPress/Odoo Website)
//
// Permite crear páginas web editando HTML/CSS/JS a gusto, hospedadas dentro de
// la plataforma y servidas públicamente (sin login) por el backend nativo
// (internal/web) en /site y /site/:slug. La tienda pública es una página cuyo
// JS consume /api/public/products.
//
// Este módulo WASM solo declara la tabla web_page y el menú. El render público
// y el servido lo hace el backend (el sandbox WASI no sirve HTTP).
//
// Tabla generada: mod_sitio_web_web_page
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
	Name:        "sitio_web",
	Version:     "1.0.0",
	Label:       "Sitio Web",
	Description: "Constructor de páginas web (HTML/CSS/JS) hospedadas y públicas, tipo WordPress",
	Author:      "Ecuabyte",
	Icon:        "globe",
	Models: []ModelDef{
		{
			Name:  "web_page",
			Label: "Página web",
			Fields: []FieldDef{
				{Name: "slug", Type: "string", Label: "Slug (URL)", Required: true},
				{Name: "title", Type: "string", Label: "Título"},
				{Name: "html", Type: "text", Label: "HTML"},
				{Name: "css", Type: "text", Label: "CSS"},
				{Name: "js", Type: "text", Label: "JavaScript"},
				{Name: "published", Type: "boolean", Label: "Publicada"},
				{Name: "is_home", Type: "boolean", Label: "Es la página de inicio"},
				{Name: "seq", Type: "integer", Label: "Orden"},
			},
		},
		{
			Name:  "media",
			Label: "Archivo multimedia",
			Fields: []FieldDef{
				{Name: "filename", Type: "string", Label: "Nombre de archivo", Required: true},
				{Name: "url", Type: "string", Label: "URL", Required: true},
				{Name: "alt", Type: "string", Label: "Texto alternativo"},
				{Name: "mime_type", Type: "string", Label: "Tipo MIME"},
				{Name: "size_bytes", Type: "integer", Label: "Tamaño (bytes)"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Sitio Web", Icon: "globe", Route: "/sitio", Seq: 140},
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
