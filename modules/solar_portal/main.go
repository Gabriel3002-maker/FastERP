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
	Name:        "solar_portal",
	Version:     "1.0.0",
	Label:       "Solar Portal",
	Description: "Portal web público para generar cotizaciones de paneles solares",
	Author:      "FastERP Solar Team",
	Icon:        "globe",
	Models: []ModelDef{
		{
			Name:  "portal_session",
			Label: "Portal Session",
			Fields: []FieldDef{
				{Name: "session_id", Type: "string", Label: "Session ID", Required: true},
				{Name: "client_email", Type: "string", Label: "Client Email"},
				{Name: "client_name", Type: "string", Label: "Client Name"},
				{Name: "current_step", Type: "string", Label: "Step (welcome|info|address|upload|review|quote)", Required: true},
				{Name: "form_data", Type: "text", Label: "Form Data (JSON)"},
				{Name: "created_at", Type: "datetime", Label: "Created"},
				{Name: "last_updated", Type: "datetime", Label: "Last Updated"},
				{Name: "expires_at", Type: "datetime", Label: "Expires"},
				{Name: "status", Type: "string", Label: "Status (active|completed|expired)"},
			},
		},
		{
			Name:  "portal_quote",
			Label: "Portal Generated Quote",
			Fields: []FieldDef{
				{Name: "quote_id", Type: "string", Label: "Quote ID", Required: true},
				{Name: "session_id", Type: "string", Label: "Session ID"},
				{Name: "client_email", Type: "string", Label: "Client Email"},
				{Name: "client_name", Type: "string", Label: "Client Name"},
				{Name: "quote_data", Type: "text", Label: "Quote JSON"},
				{Name: "pdf_url", Type: "string", Label: "PDF URL"},
				{Name: "validity_days", Type: "integer", Label: "Validity Days"},
				{Name: "expires_at", Type: "datetime", Label: "Expires"},
				{Name: "signed", Type: "boolean", Label: "Signed"},
				{Name: "signature_date", Type: "datetime", Label: "Signature Date"},
				{Name: "created_at", Type: "datetime", Label: "Created"},
			},
		},
		{
			Name:  "portal_inquiry",
			Label: "Portal Inquiry",
			Fields: []FieldDef{
				{Name: "inquiry_id", Type: "string", Label: "Inquiry ID", Required: true},
				{Name: "email", Type: "string", Label: "Email", Required: true},
				{Name: "phone", Type: "string", Label: "Phone"},
				{Name: "message", Type: "text", Label: "Message"},
				{Name: "inquiry_type", Type: "string", Label: "Type (contact|estimate|prequalify)"},
				{Name: "status", Type: "string", Label: "Status (new|contacted|converted)"},
				{Name: "created_at", Type: "datetime", Label: "Created"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Solar Portal", Icon: "globe", Route: "/portal", Seq: 50},
		{Label: "Active Sessions", Icon: "users", Route: "/portal/sessions", Seq: 51},
		{Label: "Generated Quotes", Icon: "file-text", Route: "/portal/quotes", Seq: 52},
		{Label: "Inquiries", Icon: "mail", Route: "/portal/inquiries", Seq: 53},
	},
}

// ============================================================================
// ESTRUCTURAS PARA EL PORTAL
// ============================================================================

// PortalStep1Welcome - Página de bienvenida
type PortalStep1Welcome struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	CTAText     string `json:"cta_text"`
}

// PortalStep2ClientInfo - Formulario de datos del cliente
type PortalStep2ClientInfo struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Company     string `json:"company,omitempty"`
	Address     string `json:"address"`
	ProjectType string `json:"project_type"` // residential | commercial | industrial
}

// PortalStep3Location - Ubicación del proyecto
type PortalStep3Location struct {
	Address   string  `json:"address"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	RoofType  string  `json:"roof_type"` // losa | lamina | inclinado
	RoofArea  float64 `json:"roof_area"`
	Photos    []string `json:"photos"`  // URLs de fotos
}

// PortalStep4ElectricityData - Datos eléctricos
type PortalStep4ElectricityData struct {
	Tariff           string  `json:"tariff"`        // DAC, PDBT, GDMTO, etc
	TariffRate       float64 `json:"tariff_rate"`   // $/kWh
	CurrentProvider  string  `json:"current_provider"`
	ConsumptionFile  string  `json:"consumption_file"` // URL de PDF/imagen de facturas
	EstimatedMonthly float64 `json:"estimated_monthly_consumption"`
}

// PortalQuoteReview - Cotización para revisión
type PortalQuoteReview struct {
	QuoteID              string                  `json:"quote_id"`
	ClientName           string                  `json:"client_name"`
	ClientEmail          string                  `json:"client_email"`
	SystemSize           float64                 `json:"system_size_kw"`
	AnnualProduction     float64                 `json:"annual_production_kwh"`
	AnnualSavings        float64                 `json:"annual_savings_usd"`
	ROIYears             float64                 `json:"roi_years"`
	TotalInvestment      float64                 `json:"total_investment"`
	ProductsList         []QuoteProduct          `json:"products"`
	FinancingOptions     []FinancingOption       `json:"financing_options"`
	ValidityDays         int                     `json:"validity_days"`
	ExpiresAt            string                  `json:"expires_at"`
	CreatedAt            string                  `json:"created_at"`
}

// QuoteProduct - Producto en la cotización
type QuoteProduct struct {
	Name     string  `json:"name"`
	Category string  `json:"category"` // panel | inverter | structure
	Brand    string  `json:"brand"`
	Model    string  `json:"model"`
	Quantity int     `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
	Total    float64 `json:"total"`
	OdooID   int     `json:"odoo_id"`
}

// FinancingOption - Opción de financiamiento
type FinancingOption struct {
	Name          string  `json:"name"` // Contado | Crédito 36 meses | etc
	DownPayment   float64 `json:"down_payment"`
	MonthlyPayment float64 `json:"monthly_payment"`
	TotalMonths   int     `json:"total_months"`
	TotalCost     float64 `json:"total_cost"`
	InterestRate  float64 `json:"interest_rate"`
}

// PortalResponse - Respuesta general
type PortalResponse struct {
	Status  string      `json:"status"` // success | error
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
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

func main() {}
