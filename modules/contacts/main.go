package main

import (
	"encoding/json"
	"unsafe"
)

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
	Name:        "contacts",
	Version:     "2.0.0",
	Label:       "Contactos",
	Description: "Gestión de contactos con campos contables LATAM",
	Author:      "FastERP Team",
	Icon:        "users",
	Models: []ModelDef{
		{
			Name:  "contact",
			Label: "Contacto",
			Fields: []FieldDef{
				{Name: "name", Type: "string", Label: "Nombre", Required: true},
				{Name: "email", Type: "string", Label: "Email"},
				{Name: "phone", Type: "string", Label: "Teléfono"},
				{Name: "mobile", Type: "string", Label: "Celular"},
				{Name: "company", Type: "string", Label: "Empresa"},
				{Name: "job_title", Type: "string", Label: "Cargo"},
				{Name: "tax_id_type", Type: "string", Label: "Tipo de Identificación"},
				{Name: "tax_id", Type: "string", Label: "Cédula / RUC / RFC / Pasaporte"},
				{Name: "person_type", Type: "string", Label: "Tipo de Persona"},
				{Name: "tax_regime", Type: "string", Label: "Régimen Tributario"},
				{Name: "accounting_obligation", Type: "string", Label: "Obligado a Llevar Contabilidad"},
				{Name: "retention_agent", Type: "string", Label: "Agente de Retención"},
				{Name: "address", Type: "text", Label: "Dirección"},
				{Name: "city", Type: "string", Label: "Ciudad"},
				{Name: "province", Type: "string", Label: "Provincia / Estado"},
				{Name: "country", Type: "string", Label: "País"},
				{Name: "postal_code", Type: "string", Label: "Código Postal"},
				{Name: "website", Type: "string", Label: "Sitio Web"},
				{Name: "notes", Type: "text", Label: "Notas"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Contactos", Icon: "users", Route: "/contacts", Seq: 20},
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
