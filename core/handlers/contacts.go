package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fasterp/backend/db"
)

type ContactHandler struct {
	dbConn *db.DB
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

func NewContactHandler(dbConn *db.DB) *ContactHandler {
	return &ContactHandler{dbConn: dbConn}
}

// ListContacts GET /api/contacts/list
func (ch *ContactHandler) ListContacts(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	tenantID := r.Header.Get("X-Tenant-ID")

	query := `
		SELECT id, name, email, phone, mobile, company, job_title,
		       tax_id_type, tax_id, person_type, tax_regime, accounting_obligation,
		       retention_agent, address, city, province, country, postal_code,
		       website, notes, created_at
		FROM mod_contacts_contact
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`

	rows, err := ch.dbConn.Query(ctx, query, tenantID)
	if err != nil {
		fmt.Printf("[ContactHandler] Error listing: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Error: %v", err),
		})
		return
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		err := rows.Scan(&c.ID, &c.Name, &c.Email, &c.Phone, &c.Mobile, &c.Company,
			&c.JobTitle, &c.TaxIDType, &c.TaxID, &c.PersonType, &c.TaxRegime,
			&c.AccountingObligation, &c.RetentionAgent, &c.Address, &c.City,
			&c.Province, &c.Country, &c.PostalCode, &c.Website, &c.Notes, &c.CreatedAt)
		if err == nil {
			contacts = append(contacts, c)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"contacts": contacts,
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

	query := `
		INSERT INTO mod_contacts_contact
		(tenant_id, name, email, phone, mobile, company, job_title, tax_id_type, tax_id,
		 person_type, tax_regime, accounting_obligation, retention_agent, address, city,
		 province, country, postal_code, website, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING id
	`

	var id string
	err := ch.dbConn.QueryRow(ctx, query,
		tenantID, c.Name, c.Email, c.Phone, c.Mobile, c.Company, c.JobTitle,
		c.TaxIDType, c.TaxID, c.PersonType, c.TaxRegime, c.AccountingObligation,
		c.RetentionAgent, c.Address, c.City, c.Province, c.Country, c.PostalCode,
		c.Website, c.Notes,
	).Scan(&id)

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

	query := `
		UPDATE mod_contacts_contact
		SET name = $1, email = $2, phone = $3, mobile = $4, company = $5, job_title = $6,
		    tax_id_type = $7, tax_id = $8, person_type = $9, tax_regime = $10,
		    accounting_obligation = $11, retention_agent = $12, address = $13, city = $14,
		    province = $15, country = $16, postal_code = $17, website = $18, notes = $19,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $20 AND tenant_id = $21
	`

	_, err := ch.dbConn.Exec(ctx, query,
		c.Name, c.Email, c.Phone, c.Mobile, c.Company, c.JobTitle,
		c.TaxIDType, c.TaxID, c.PersonType, c.TaxRegime, c.AccountingObligation,
		c.RetentionAgent, c.Address, c.City, c.Province, c.Country, c.PostalCode,
		c.Website, c.Notes, id, tenantID,
	)

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

	query := `DELETE FROM mod_contacts_contact WHERE id = $1 AND tenant_id = $2`

	_, err := ch.dbConn.Exec(ctx, query, id, tenantID)
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
