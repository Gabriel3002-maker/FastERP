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
	Name:        "sites",
	Version:     "1.0.0",
	Label:       "Solar Sites",
	Description: "Gestión de sitios de proyecto: cliente, ubicación, consumo histórico",
	Author:      "FastERP Solar Team",
	Icon:        "map-pin",
	Models: []ModelDef{
		{
			Name:  "site",
			Label: "Project Site",
			Fields: []FieldDef{
				{Name: "client_name", Type: "string", Label: "Client Name", Required: true},
				{Name: "client_email", Type: "string", Label: "Client Email", Required: true},
				{Name: "client_phone", Type: "string", Label: "Client Phone"},
				{Name: "client_company", Type: "string", Label: "Company Name"},
				{Name: "project_address", Type: "text", Label: "Project Address", Required: true},
				{Name: "latitude", Type: "decimal", Label: "Latitude"},
				{Name: "longitude", Type: "decimal", Label: "Longitude"},
				{Name: "electricity_tariff", Type: "string", Label: "Tariff (DAC|PDBT|GDMTO|etc)", Required: true},
				{Name: "tariff_rate", Type: "decimal", Label: "Tariff Rate ($/kWh)"},
				{Name: "current_provider", Type: "string", Label: "Current Provider"},
				{Name: "roof_type", Type: "string", Label: "Roof Type (losa|lamina|inclinado)"},
				{Name: "roof_area", Type: "decimal", Label: "Roof Area (m²)"},
				{Name: "roof_condition", Type: "string", Label: "Condition (nueva|buena|regular|deficiente)"},
				{Name: "technical_notes", Type: "text", Label: "Technical Notes"},
				{Name: "visit_scheduled", Type: "datetime", Label: "Visit Scheduled"},
				{Name: "visit_completed", Type: "boolean", Label: "Visit Completed"},
				{Name: "status", Type: "string", Label: "Status (lead|quoted|installed)"},
			},
		},
		{
			Name:  "site_consumption",
			Label: "Site Consumption History",
			Fields: []FieldDef{
				{Name: "site_id", Type: "string", Label: "Site ID", Required: true},
				{Name: "period_month", Type: "date", Label: "Period Month", Required: true},
				{Name: "consumption_kwh", Type: "decimal", Label: "Consumption (kWh)", Required: true},
				{Name: "invoice_amount", Type: "decimal", Label: "Invoice Amount (USD)"},
				{Name: "source", Type: "string", Label: "Source (manual|import|api)"},
			},
		},
		{
			Name:  "site_photo",
			Label: "Site Photo",
			Fields: []FieldDef{
				{Name: "site_id", Type: "string", Label: "Site ID", Required: true},
				{Name: "photo_url", Type: "string", Label: "Photo URL"},
				{Name: "photo_type", Type: "string", Label: "Type (roof|overview|details)"},
				{Name: "upload_date", Type: "datetime", Label: "Upload Date"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Solar Sites", Icon: "map-pin", Route: "/sites", Seq: 40},
		{Label: "New Project", Icon: "plus-circle", Route: "/sites/new", Seq: 41},
		{Label: "Site Map", Icon: "map", Route: "/sites/map", Seq: 42},
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
	return 0
}

//go:wasmexport fasterp_on_unload
func fasterp_on_unload() uint32 {
	return 0
}

// ============================================================================
// ESTRUCTURAS DE API (Ejemplos)
// ============================================================================

type SiteResponse struct {
	ID              string  `json:"id"`
	ClientName      string  `json:"client_name"`
	ClientEmail     string  `json:"client_email"`
	ProjectAddress  string  `json:"project_address"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Tariff          string  `json:"electricity_tariff"`
	TariffRate      float64 `json:"tariff_rate"`
	RoofType        string  `json:"roof_type"`
	RoofArea        float64 `json:"roof_area"`
	Status          string  `json:"status"`
}

type ConsumptionEntry struct {
	Month       string  `json:"month"`
	ConsumKWh   float64 `json:"consumption_kwh"`
	InvoiceAmt  float64 `json:"invoice_amount"`
}

type SiteWithConsumption struct {
	Site        SiteResponse      `json:"site"`
	Consumption []ConsumptionEntry `json:"consumption_history"`
	Average     float64           `json:"average_monthly_kwh"`
}

func main() {}
