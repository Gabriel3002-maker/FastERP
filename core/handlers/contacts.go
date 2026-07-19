package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fasterp/backend/db"
	"github.com/fasterp/backend/sdk"
)

type ContactHandler struct {
	dbConn *db.DB
	sdk    *sdk.ModuleSDK
}

type Contact struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Email                 string `json:"email"`
	Phone                 string `json:"phone"`
	Mobile                string `json:"mobile"`
	Company               string `json:"company"`
	JobTitle              string `json:"job_title"`
	TaxIDType             string `json:"tax_id_type"`
	TaxID                 string `json:"tax_id"`
	PersonType            string `json:"person_type"`
	TaxRegime             string `json:"tax_regime"`
	AccountingObligation  string `json:"accounting_obligation"`
	RetentionAgent        string `json:"retention_agent"`
	Address               string `json:"address"`
	City                  string `json:"city"`
	Province              string `json:"province"`
	Country               string `json:"country"`
	PostalCode            string `json:"postal_code"`
	Website               string `json:"website"`
	Notes                 string `json:"notes"`
	CreatedAt             string `json:"created_at"`
}

// Manifest del módulo contacts (embebido)
const contactsManifest = `{
	"name": "contacts",
	"models": {
		"contact": {
			"name": "contact",
			"fields": {
				"name": {"type": "string", "required": true},
				"email": {"type": "string", "required": false},
				"phone": {"type": "string", "required": false},
				"mobile": {"type": "string", "required": false},
				"company": {"type": "string", "required": false},
				"job_title": {"type": "string", "required": false},
				"tax_id_type": {"type": "string", "required": false},
				"tax_id": {"type": "string", "required": false},
				"person_type": {"type": "string", "required": false},
				"tax_regime": {"type": "string", "required": false},
				"accounting_obligation": {"type": "string", "required": false},
				"retention_agent": {"type": "string", "required": false},
				"address": {"type": "text", "required": false},
				"city": {"type": "string", "required": false},
				"province": {"type": "string", "required": false},
				"country": {"type": "string", "required": false},
				"postal_code": {"type": "string", "required": false},
				"website": {"type": "string", "required": false},
				"notes": {"type": "text", "required": false}
			}
		}
	}
}`

func NewContactHandler(dbConn *db.DB) *ContactHandler {
	// Crear SDK para el módulo contacts
	moduleSdk := sdk.NewModuleSDK("contacts", "", "", dbConn.Pool())
	moduleSdk.LoadManifest(contactsManifest)

	return &ContactHandler{
		dbConn: dbConn,
		sdk:    moduleSdk,
	}
}

// ListContacts GET /api/contacts/list
func (ch *ContactHandler) ListContacts(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantID := r.Header.Get("X-Tenant-ID")

	// Usar SDK en lugar de SQL directo
	ch.sdk.TenantID = tenantID
	results, err := ch.sdk.List(ctx, "contact")
	if err != nil {
		fmt.Printf("[ContactHandler] Error listing: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"contacts": results,
	})
}

// CreateContact POST /api/contacts
func (ch *ContactHandler) CreateContact(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantID := r.Header.Get("X-Tenant-ID")

	var c Contact
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Invalid JSON"})
		return
	}

	// Usar SDK en lugar de SQL directo
	ch.sdk.TenantID = tenantID
	id, err := ch.sdk.Create(ctx, "contact", map[string]interface{}{
		"name":                    c.Name,
		"email":                   c.Email,
		"phone":                   c.Phone,
		"mobile":                  c.Mobile,
		"company":                 c.Company,
		"job_title":               c.JobTitle,
		"tax_id_type":             c.TaxIDType,
		"tax_id":                  c.TaxID,
		"person_type":             c.PersonType,
		"tax_regime":              c.TaxRegime,
		"accounting_obligation":   c.AccountingObligation,
		"retention_agent":         c.RetentionAgent,
		"address":                 c.Address,
		"city":                    c.City,
		"province":                c.Province,
		"country":                 c.Country,
		"postal_code":             c.PostalCode,
		"website":                 c.Website,
		"notes":                   c.Notes,
	})

	if err != nil {
		fmt.Printf("[ContactHandler] Error creating: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      id,
		"message": "Contact created",
	})
}

// UpdateContact PUT /api/contacts/:id
func (ch *ContactHandler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantID := r.Header.Get("X-Tenant-ID")
	id := r.URL.Query().Get("id")
	if id == "" {
		id = r.PathValue("id") // Go 1.22+
	}

	var c Contact
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Invalid JSON"})
		return
	}

	// Usar SDK en lugar de SQL directo
	ch.sdk.TenantID = tenantID
	err := ch.sdk.Update(ctx, "contact", id, map[string]interface{}{
		"name":                    c.Name,
		"email":                   c.Email,
		"phone":                   c.Phone,
		"mobile":                  c.Mobile,
		"company":                 c.Company,
		"job_title":               c.JobTitle,
		"tax_id_type":             c.TaxIDType,
		"tax_id":                  c.TaxID,
		"person_type":             c.PersonType,
		"tax_regime":              c.TaxRegime,
		"accounting_obligation":   c.AccountingObligation,
		"retention_agent":         c.RetentionAgent,
		"address":                 c.Address,
		"city":                    c.City,
		"province":                c.Province,
		"country":                 c.Country,
		"postal_code":             c.PostalCode,
		"website":                 c.Website,
		"notes":                   c.Notes,
	})

	if err != nil {
		fmt.Printf("[ContactHandler] Error updating: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Contact updated",
	})
}

// DeleteContact DELETE /api/contacts/:id
func (ch *ContactHandler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantID := r.Header.Get("X-Tenant-ID")
	id := r.URL.Query().Get("id")
	if id == "" {
		id = r.PathValue("id") // Go 1.22+
	}

	// Usar SDK en lugar de SQL directo
	ch.sdk.TenantID = tenantID
	err := ch.sdk.Delete(ctx, "contact", id)

	if err != nil {
		fmt.Printf("[ContactHandler] Error deleting: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Contact deleted",
	})
}
