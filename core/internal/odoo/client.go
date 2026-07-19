// Package odoo implements a minimal Odoo External API (JSON-RPC) client plus
// the sync/push service used by the integracion_odoo_fasterp module.
//
// WASM modules run in a WASI sandbox with no network access, so all Odoo
// HTTP traffic lives here in the native backend. The module only declares the
// data models (tables), menus and pages; this package does the real work and
// reads/writes the module's tenant-scoped tables.
package odoo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client talks to an Odoo server through the /jsonrpc endpoint.
type Client struct {
	URL      string // e.g. http://localhost:8073
	Database string
	Username string
	Password string
	UID      int

	http *http.Client
}

// NewClient builds a client. URL is normalised (trailing slash stripped).
func NewClient(url, database, username, password string) *Client {
	return &Client{
		URL:      strings.TrimRight(strings.TrimSpace(url), "/"),
		Database: strings.TrimSpace(database),
		Username: strings.TrimSpace(username),
		Password: password,
		http:     &http.Client{Timeout: 60 * time.Second},
	}
}

type rpcRequest struct {
	Jsonrpc string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *rpcError) Error() string {
	// data.message carries the human-readable Odoo error (e.g. AccessError).
	var d struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if len(e.Data) > 0 {
		_ = json.Unmarshal(e.Data, &d)
	}
	if d.Message != "" {
		return fmt.Sprintf("odoo: %s", strings.TrimSpace(d.Message))
	}
	return fmt.Sprintf("odoo: %s", e.Message)
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (c *Client) call(ctx context.Context, service, method string, args []interface{}) (json.RawMessage, error) {
	if c.URL == "" {
		return nil, fmt.Errorf("odoo: empty URL")
	}
	payload := rpcRequest{
		Jsonrpc: "2.0",
		Method:  "call",
		ID:      1,
		Params: map[string]interface{}{
			"service": service,
			"method":  method,
			"args":    args,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/jsonrpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("odoo: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("odoo: unexpected status %d", resp.StatusCode)
	}

	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("odoo: invalid response: %w", err)
	}
	if out.Error != nil {
		return nil, out.Error
	}
	return out.Result, nil
}

// Authenticate logs in and caches the UID. Returns an error if credentials are
// rejected (Odoo returns `false` instead of an integer uid).
func (c *Client) Authenticate(ctx context.Context) (int, error) {
	res, err := c.call(ctx, "common", "authenticate",
		[]interface{}{c.Database, c.Username, c.Password, map[string]interface{}{}})
	if err != nil {
		return 0, err
	}
	// Odoo returns `false` for bad credentials, otherwise an integer uid.
	var uid int
	if err := json.Unmarshal(res, &uid); err != nil || uid == 0 {
		return 0, fmt.Errorf("odoo: authentication failed (check database, user and password)")
	}
	c.UID = uid
	return uid, nil
}

// ensureAuth authenticates lazily if no UID is cached yet.
func (c *Client) ensureAuth(ctx context.Context) error {
	if c.UID != 0 {
		return nil
	}
	_, err := c.Authenticate(ctx)
	return err
}

// ExecuteKw calls model.method(args, kwargs) via object/execute_kw.
func (c *Client) ExecuteKw(ctx context.Context, model, method string, args []interface{}, kwargs map[string]interface{}) (json.RawMessage, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return nil, err
	}
	if kwargs == nil {
		kwargs = map[string]interface{}{}
	}
	if args == nil {
		args = []interface{}{}
	}
	return c.call(ctx, "object", "execute_kw",
		[]interface{}{c.Database, c.UID, c.Password, model, method, args, kwargs})
}

// SearchRead returns records of `model` matching `domain`, projecting `fields`.
func (c *Client) SearchRead(ctx context.Context, model string, domain []interface{}, fields []string, limit int) ([]map[string]interface{}, error) {
	if domain == nil {
		domain = []interface{}{}
	}
	kwargs := map[string]interface{}{"fields": fields}
	if limit > 0 {
		kwargs["limit"] = limit
	}
	raw, err := c.ExecuteKw(ctx, model, "search_read", []interface{}{domain}, kwargs)
	if err != nil {
		return nil, err
	}
	var records []map[string]interface{}
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("odoo: cannot decode %s records: %w", model, err)
	}
	return records, nil
}

// Create inserts a record and returns its new id.
func (c *Client) Create(ctx context.Context, model string, values map[string]interface{}) (int, error) {
	raw, err := c.ExecuteKw(ctx, model, "create", []interface{}{values}, nil)
	if err != nil {
		return 0, err
	}
	var id int
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, fmt.Errorf("odoo: cannot decode created id: %w", err)
	}
	return id, nil
}

// Write updates the given record ids with values.
func (c *Client) Write(ctx context.Context, model string, ids []int, values map[string]interface{}) (bool, error) {
	raw, err := c.ExecuteKw(ctx, model, "write", []interface{}{ids, values}, nil)
	if err != nil {
		return false, err
	}
	var ok bool
	if err := json.Unmarshal(raw, &ok); err != nil {
		return false, nil
	}
	return ok, nil
}
