package odoo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fasterp/backend/internal/db"
)

// ModuleName must match the manifest name of the WASM module. It determines the
// physical table names the backend reads/writes (mod_<module>_<model>).
const ModuleName = "integracion_odoo_fasterp"

func table(model string) string { return "mod_" + ModuleName + "_" + model }

// Connection is a stored Odoo connection (one active row per tenant is used).
type Connection struct {
	ID           string
	Name         string
	URL          string
	Database     string
	Username     string
	Password     string
	UID          int
	Active       bool
}

// SyncResult summarises a pull/push operation.
type SyncResult struct {
	Status          string  `json:"status"`
	Message         string  `json:"message"`
	RecordsCount    int     `json:"records_count"`
	DurationSeconds float64 `json:"duration_seconds"`
	OdooID          int     `json:"odoo_id,omitempty"`
}

// ---------------------------------------------------------------------------
// Connection loading
// ---------------------------------------------------------------------------

// LoadConnection returns the active connection for the tenant and a ready
// *Client. It does NOT authenticate yet (that happens lazily on first call).
func LoadConnection(ctx context.Context, ex db.QueryExecutor, tenantID string) (*Client, *Connection, error) {
	q := fmt.Sprintf(`SELECT id, name, odoo_url, odoo_database, odoo_username, odoo_password, active
		FROM %s WHERE tenant_id = $1 AND active = true
		ORDER BY updated_at DESC LIMIT 1`, table("odoo_connection"))

	var c Connection
	var name, url, dbName, user, pass sql.NullString
	var active sql.NullBool
	err := ex.QueryRowContext(ctx, q, tenantID).Scan(&c.ID, &name, &url, &dbName, &user, &pass, &active)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("no hay una conexión Odoo activa configurada")
	}
	if err != nil {
		return nil, nil, err
	}
	c.Name, c.URL, c.Database, c.Username, c.Password, c.Active =
		name.String, url.String, dbName.String, user.String, pass.String, active.Bool

	if c.URL == "" || c.Database == "" || c.Username == "" {
		return nil, nil, fmt.Errorf("la conexión Odoo está incompleta (url/database/username)")
	}
	return NewClient(c.URL, c.Database, c.Username, c.Password), &c, nil
}

// TestConnection authenticates and returns the uid. It also persists uid/last_error.
func TestConnection(ctx context.Context, ex db.QueryExecutor, tenantID string) (int, error) {
	client, conn, err := LoadConnection(ctx, ex, tenantID)
	if err != nil {
		return 0, err
	}
	uid, err := client.Authenticate(ctx)
	if err != nil {
		_ = updateConnStatus(ctx, ex, tenantID, conn.ID, 0, err.Error())
		return 0, err
	}
	_ = updateConnStatus(ctx, ex, tenantID, conn.ID, uid, "")
	return uid, nil
}

func updateConnStatus(ctx context.Context, ex db.QueryExecutor, tenantID, id string, uid int, lastErr string) error {
	q := fmt.Sprintf(`UPDATE %s SET uid = $1, last_error = $2, last_sync = $3, updated_at = NOW()
		WHERE id = $4 AND tenant_id = $5`, table("odoo_connection"))
	_, err := ex.ExecContext(ctx, q, uid, lastErr, time.Now(), id, tenantID)
	return err
}

// ---------------------------------------------------------------------------
// PULL: Odoo -> FastERP (product.product)
// ---------------------------------------------------------------------------

var productFields = []string{
	"name", "default_code", "barcode", "list_price", "standard_price",
	"qty_available", "categ_id", "uom_id", "description_sale", "active",
	"product_tmpl_id", "image_128",
}

// SyncProducts pulls product.product records from Odoo and upserts them into the
// imported_product table for the tenant. limit<=0 means "all".
func SyncProducts(ctx context.Context, ex db.QueryExecutor, tenantID string, limit int) (SyncResult, error) {
	start := time.Now()
	client, conn, err := LoadConnection(ctx, ex, tenantID)
	if err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	records, err := client.SearchRead(ctx, "product.product", []interface{}{}, productFields, limit)
	if err != nil {
		_ = writeHistory(ctx, ex, tenantID, "pull", "products", "error", 0, err.Error(), time.Since(start).Seconds())
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	existing, err := existingProductIDs(ctx, ex, tenantID)
	if err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	tbl := table("imported_product")
	now := time.Now()
	count := 0
	for _, r := range records {
		odooID := integer(r, "id")
		if odooID == 0 {
			continue
		}
		vals := map[string]interface{}{
			"odoo_product_id": odooID,
			"odoo_tmpl_id":    m2oID(r, "product_tmpl_id"),
			"name":            str(r, "name"),
			"default_code":    str(r, "default_code"),
			"barcode":         str(r, "barcode"),
			"category":        m2oName(r, "categ_id"),
			"list_price":      num(r, "list_price"),
			"standard_price":  num(r, "standard_price"),
			"qty_available":   num(r, "qty_available"),
			"uom":             m2oName(r, "uom_id"),
			"description":     str(r, "description_sale"),
			"active":          boolean(r, "active"),
			"image":           imageDataURI(str(r, "image_128")),
			"last_synced":     now,
			"sync_status":     "success",
		}

		if rowID, ok := existing[odooID]; ok {
			if err := updateProduct(ctx, ex, tbl, rowID, tenantID, vals); err != nil {
				return SyncResult{Status: "error", Message: err.Error()}, err
			}
		} else {
			if err := insertProduct(ctx, ex, tbl, tenantID, vals); err != nil {
				return SyncResult{Status: "error", Message: err.Error()}, err
			}
		}
		count++
	}

	dur := time.Since(start).Seconds()
	_ = updateConnStatus(ctx, ex, tenantID, conn.ID, client.UID, "")
	_ = writeHistory(ctx, ex, tenantID, "pull", "products", "success", count, "", dur)

	return SyncResult{
		Status:          "success",
		Message:         fmt.Sprintf("%d productos sincronizados desde Odoo", count),
		RecordsCount:    count,
		DurationSeconds: dur,
	}, nil
}

func existingProductIDs(ctx context.Context, ex db.QueryExecutor, tenantID string) (map[int]string, error) {
	q := fmt.Sprintf(`SELECT id, odoo_product_id FROM %s WHERE tenant_id = $1`, table("imported_product"))
	rows, err := ex.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]string)
	for rows.Next() {
		var id string
		var odooID sql.NullInt64
		if err := rows.Scan(&id, &odooID); err != nil {
			return nil, err
		}
		out[int(odooID.Int64)] = id
	}
	return out, rows.Err()
}

func insertProduct(ctx context.Context, ex db.QueryExecutor, tbl, tenantID string, v map[string]interface{}) error {
	q := fmt.Sprintf(`INSERT INTO %s
		(tenant_id, odoo_product_id, odoo_tmpl_id, name, default_code, barcode, category,
		 list_price, standard_price, qty_available, uom, description, active, image, last_synced, sync_status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, tbl)
	_, err := ex.ExecContext(ctx, q,
		tenantID, v["odoo_product_id"], v["odoo_tmpl_id"], v["name"], v["default_code"], v["barcode"],
		v["category"], v["list_price"], v["standard_price"], v["qty_available"], v["uom"],
		v["description"], v["active"], v["image"], v["last_synced"], v["sync_status"])
	return err
}

func updateProduct(ctx context.Context, ex db.QueryExecutor, tbl, rowID, tenantID string, v map[string]interface{}) error {
	q := fmt.Sprintf(`UPDATE %s SET
		odoo_tmpl_id=$1, name=$2, default_code=$3, barcode=$4, category=$5,
		list_price=$6, standard_price=$7, qty_available=$8, uom=$9, description=$10,
		active=$11, image=$12, last_synced=$13, sync_status=$14, updated_at=NOW()
		WHERE id=$15 AND tenant_id=$16`, tbl)
	_, err := ex.ExecContext(ctx, q,
		v["odoo_tmpl_id"], v["name"], v["default_code"], v["barcode"], v["category"],
		v["list_price"], v["standard_price"], v["qty_available"], v["uom"], v["description"],
		v["active"], v["image"], v["last_synced"], v["sync_status"], rowID, tenantID)
	return err
}

// ---------------------------------------------------------------------------
// PUSH: FastERP -> Odoo
// ---------------------------------------------------------------------------

// OrderLine is one line of a pushed sale order.
type OrderLine struct {
	ProductOdooID int     `json:"product_odoo_id"`
	Quantity      float64 `json:"quantity"`
	PriceUnit     float64 `json:"price_unit"`
}

// OrderPush is the payload to create a sale.order in Odoo.
type OrderPush struct {
	PartnerName  string      `json:"partner_name"`
	PartnerEmail string      `json:"partner_email"`
	Lines        []OrderLine `json:"lines"`
}

// PushOrder creates a res.partner (if needed) and a draft sale.order in Odoo,
// then records it in sync_order.
func PushOrder(ctx context.Context, ex db.QueryExecutor, tenantID string, p OrderPush) (SyncResult, error) {
	start := time.Now()
	client, _, err := LoadConnection(ctx, ex, tenantID)
	if err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	partnerID, err := findOrCreatePartner(ctx, client, p.PartnerName, p.PartnerEmail)
	if err != nil {
		return pushFail(ctx, ex, tenantID, "order", start, err)
	}

	var orderLines []interface{}
	var total float64
	for _, l := range p.Lines {
		orderLines = append(orderLines, []interface{}{0, 0, map[string]interface{}{
			"product_id":       l.ProductOdooID,
			"product_uom_qty":  l.Quantity,
			"price_unit":       l.PriceUnit,
		}})
		total += l.Quantity * l.PriceUnit
	}

	orderID, err := client.Create(ctx, "sale.order", map[string]interface{}{
		"partner_id": partnerID,
		"order_line": orderLines,
	})
	if err != nil {
		return pushFail(ctx, ex, tenantID, "order", start, err)
	}

	lineJSON, _ := json.Marshal(p.Lines)
	q := fmt.Sprintf(`INSERT INTO %s
		(tenant_id, partner_name, partner_email, amount_total, lines_json, odoo_order_id, state, push_status, error_message, pushed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, table("sync_order"))
	if _, err := ex.ExecContext(ctx, q, tenantID, p.PartnerName, p.PartnerEmail, total,
		string(lineJSON), orderID, "draft", "success", "", time.Now()); err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	dur := time.Since(start).Seconds()
	_ = writeHistory(ctx, ex, tenantID, "push", "order", "success", 1, "", dur)
	return SyncResult{Status: "success", Message: fmt.Sprintf("Pedido creado en Odoo (ID %d)", orderID),
		RecordsCount: 1, DurationSeconds: dur, OdooID: orderID}, nil
}

// LeadPush is the payload to create a crm.lead in Odoo.
type LeadPush struct {
	Name        string `json:"name"`
	ContactName string `json:"contact_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Description string `json:"description"`
}

// PushLead creates a crm.lead in Odoo and records it in sync_lead.
func PushLead(ctx context.Context, ex db.QueryExecutor, tenantID string, p LeadPush) (SyncResult, error) {
	start := time.Now()
	client, _, err := LoadConnection(ctx, ex, tenantID)
	if err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	name := p.Name
	if name == "" {
		name = p.ContactName
	}
	leadID, err := client.Create(ctx, "crm.lead", map[string]interface{}{
		"name":         name,
		"contact_name": p.ContactName,
		"email_from":   p.Email,
		"phone":        p.Phone,
		"description":  p.Description,
		"type":         "lead",
	})
	if err != nil {
		return pushFail(ctx, ex, tenantID, "lead", start, err)
	}

	q := fmt.Sprintf(`INSERT INTO %s
		(tenant_id, name, contact_name, email, phone, description, odoo_lead_id, push_status, error_message, pushed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, table("sync_lead"))
	if _, err := ex.ExecContext(ctx, q, tenantID, name, p.ContactName, p.Email, p.Phone,
		p.Description, leadID, "success", "", time.Now()); err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	dur := time.Since(start).Seconds()
	_ = writeHistory(ctx, ex, tenantID, "push", "lead", "success", 1, "", dur)
	return SyncResult{Status: "success", Message: fmt.Sprintf("Lead creado en Odoo (ID %d)", leadID),
		RecordsCount: 1, DurationSeconds: dur, OdooID: leadID}, nil
}

// ProductPush creates or updates a product.template in Odoo.
type ProductPush struct {
	OdooTmplID    int     `json:"odoo_tmpl_id"` // 0 => create
	Name          string  `json:"name"`
	DefaultCode   string  `json:"default_code"`
	ListPrice     float64 `json:"list_price"`
	StandardPrice float64 `json:"standard_price"`
}

// PushProduct creates or updates a product.template in Odoo.
func PushProduct(ctx context.Context, ex db.QueryExecutor, tenantID string, p ProductPush) (SyncResult, error) {
	start := time.Now()
	client, _, err := LoadConnection(ctx, ex, tenantID)
	if err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	values := map[string]interface{}{
		"name":           p.Name,
		"default_code":   p.DefaultCode,
		"list_price":     p.ListPrice,
		"standard_price": p.StandardPrice,
	}

	var odooID int
	if p.OdooTmplID > 0 {
		if _, err := client.Write(ctx, "product.template", []int{p.OdooTmplID}, values); err != nil {
			return pushFail(ctx, ex, tenantID, "product", start, err)
		}
		odooID = p.OdooTmplID
	} else {
		odooID, err = client.Create(ctx, "product.template", values)
		if err != nil {
			return pushFail(ctx, ex, tenantID, "product", start, err)
		}
	}

	dur := time.Since(start).Seconds()
	_ = writeHistory(ctx, ex, tenantID, "push", "product", "success", 1, "", dur)
	return SyncResult{Status: "success", Message: fmt.Sprintf("Producto enviado a Odoo (ID %d)", odooID),
		RecordsCount: 1, DurationSeconds: dur, OdooID: odooID}, nil
}

// StockPush updates qty/price of a product in Odoo.
type StockPush struct {
	ProductOdooID int     `json:"product_odoo_id"` // product.product id
	ListPrice     float64 `json:"list_price"`
	NewQuantity   float64 `json:"new_quantity"`
	UpdateStock   bool    `json:"update_stock"`
}

// PushStock updates the price on the product and, if requested, sets a new
// on-hand quantity via stock.change.product.qty style write on the variant.
func PushStock(ctx context.Context, ex db.QueryExecutor, tenantID string, p StockPush) (SyncResult, error) {
	start := time.Now()
	client, _, err := LoadConnection(ctx, ex, tenantID)
	if err != nil {
		return SyncResult{Status: "error", Message: err.Error()}, err
	}

	if p.ListPrice > 0 {
		if _, err := client.Write(ctx, "product.product", []int{p.ProductOdooID},
			map[string]interface{}{"lst_price": p.ListPrice}); err != nil {
			return pushFail(ctx, ex, tenantID, "stock", start, err)
		}
	}

	if p.UpdateStock {
		// Create + apply an inventory quantity change for the variant.
		if _, err := client.ExecuteKw(ctx, "stock.quant", "create", []interface{}{
			map[string]interface{}{
				"product_id":    p.ProductOdooID,
				"inventory_quantity": p.NewQuantity,
			},
		}, nil); err != nil {
			return pushFail(ctx, ex, tenantID, "stock", start, err)
		}
	}

	dur := time.Since(start).Seconds()
	_ = writeHistory(ctx, ex, tenantID, "push", "stock", "success", 1, "", dur)
	return SyncResult{Status: "success", Message: "Stock/precio actualizado en Odoo",
		RecordsCount: 1, DurationSeconds: dur, OdooID: p.ProductOdooID}, nil
}

func findOrCreatePartner(ctx context.Context, client *Client, name, email string) (int, error) {
	if email != "" {
		found, err := client.SearchRead(ctx, "res.partner",
			[]interface{}{[]interface{}{"email", "=", email}}, []string{"id"}, 1)
		if err != nil {
			return 0, err
		}
		if len(found) > 0 {
			return integer(found[0], "id"), nil
		}
	}
	if name == "" {
		name = "Cliente Web"
	}
	return client.Create(ctx, "res.partner", map[string]interface{}{
		"name":  name,
		"email": email,
	})
}

// ---------------------------------------------------------------------------
// history + helpers
// ---------------------------------------------------------------------------

func pushFail(ctx context.Context, ex db.QueryExecutor, tenantID, entity string, start time.Time, cause error) (SyncResult, error) {
	dur := time.Since(start).Seconds()
	_ = writeHistory(ctx, ex, tenantID, "push", entity, "error", 0, cause.Error(), dur)
	return SyncResult{Status: "error", Message: cause.Error(), DurationSeconds: dur}, cause
}

func writeHistory(ctx context.Context, ex db.QueryExecutor, tenantID, direction, entity, status string, count int, errMsg string, dur float64) error {
	q := fmt.Sprintf(`INSERT INTO %s
		(tenant_id, direction, entity, status, records_count, error_message, duration_seconds, sync_timestamp)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, table("sync_history"))
	_, err := ex.ExecContext(ctx, q, tenantID, direction, entity, status, count, errMsg, dur, time.Now())
	return err
}

// --- Odoo value coercion helpers (Odoo returns false for empty fields) ------

func str(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func num(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

func integer(m map[string]interface{}, key string) int {
	return int(num(m, key))
}

func boolean(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// m2oName extracts the display name from a many2one [id, "name"] pair.
func m2oName(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]interface{}); ok && len(arr) == 2 {
			if s, ok := arr[1].(string); ok {
				return s
			}
		}
	}
	return ""
}

// m2oID extracts the id from a many2one [id, "name"] pair.
func m2oID(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]interface{}); ok && len(arr) >= 1 {
			if f, ok := arr[0].(float64); ok {
				return int(f)
			}
		}
	}
	return 0
}

func imageDataURI(b64 string) string {
	if b64 == "" {
		return ""
	}
	return "data:image/png;base64," + b64
}
