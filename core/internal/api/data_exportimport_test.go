package api

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fasterp/backend/internal/exportimport"
	"github.com/fasterp/backend/internal/module"
	"github.com/gin-gonic/gin"
)

type mockQueryExec struct{}

func (m *mockQueryExec) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("mock query context error")
}

func (m *mockQueryExec) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return &sql.Row{}
}

func (m *mockQueryExec) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return nil, nil
}

func TestExportImportCSVFormatParsing(t *testing.T) {
	if exportimport.ParseFormat("xlsx") != exportimport.FormatXLSX {
		t.Errorf("expected XLSX format")
	}
	if exportimport.ParseFormat("excel") != exportimport.FormatXLSX {
		t.Errorf("expected XLSX format")
	}
	if exportimport.ParseFormat("csv") != exportimport.FormatCSV {
		t.Errorf("expected CSV format")
	}
	if exportimport.ParseFormat("anything") != exportimport.FormatCSV {
		t.Errorf("expected default CSV format")
	}
}

func TestExportHandlerRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	m := &module.Manifest{
		Name: "contacts",
		Models: map[string]*module.ModelDef{
			"contact": {
				Name: "contact",
				Fields: []module.FieldDef{
					{Name: "name", Type: "string"},
				},
			},
		},
	}
	_ = m.Validate()

	modInst := &module.ModuleInstance{
		Manifest: m,
		Models: []module.ModelRegistration{
			{
				Manifest:  m.Models["contact"],
				TableName: "mod_contacts_contact",
			},
		},
	}
	module.Global.Register(modInst)
	defer module.Global.Unregister("contacts")

	h := NewHandler(nil, "secret", "refresh", r)

	apiGroup := r.Group("/api")
	apiGroup.Use(func(c *gin.Context) {
		c.Set("tenant_id", "test-tenant")
		c.Set("db_conn", &mockQueryExec{})
		c.Set("bypass_installed_check", true)
	})
	h.RegisterModuleDataRoutes(apiGroup)

	req := httptest.NewRequest("GET", "/api/contacts/contact/export?format=csv", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError && w.Code != http.StatusOK {
		t.Errorf("expected 500 or 200, got %d", w.Code)
	}
}

func TestImportHandlerMultipartRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	m := &module.Manifest{
		Name: "contacts",
		Models: map[string]*module.ModelDef{
			"contact": {
				Name: "contact",
				Fields: []module.FieldDef{
					{Name: "name", Type: "string", Required: true},
				},
			},
		},
	}
	_ = m.Validate()

	modInst := &module.ModuleInstance{
		Manifest: m,
		Models: []module.ModelRegistration{
			{
				Manifest:  m.Models["contact"],
				TableName: "mod_contacts_contact",
			},
		},
	}
	module.Global.Register(modInst)
	defer module.Global.Unregister("contacts")

	h := NewHandler(nil, "secret", "refresh", r)

	apiGroup := r.Group("/api")
	apiGroup.Use(func(c *gin.Context) {
		c.Set("tenant_id", "test-tenant")
		c.Set("db_conn", &mockQueryExec{})
		c.Set("bypass_installed_check", true)
	})
	h.RegisterModuleDataRoutes(apiGroup)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "contacts.csv")
	part.Write([]byte("Name\nJuan Perez\n"))
	writer.Close()

	req := httptest.NewRequest("POST", "/api/contacts/contact/import?dry_run=true", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError && w.Code != http.StatusOK {
		t.Errorf("expected 500 or 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

// Un id de la selección tiene que ser un UUID. Si el parser lo aceptara tal
// cual, el valor iría como texto y un "id OR 1=1" acabaría en el WHERE.
func TestParseIDListRejectsNoUUID(t *testing.T) {
	for _, bad := range []string{
		"abc",
		"1 OR 1=1",
		"'; DROP TABLE mod_contacts_contact; --",
		"$(rm -rf /)",
	} {
		if _, err := parseIDList(bad); err == nil {
			t.Errorf("parseIDList(%q) debería fallar: un id tiene que ser un UUID", bad)
		}
	}
}

func TestParseIDListAceptaUUIDValidos(t *testing.T) {
	ids := []string{
		"17912345-6700-1000-8000-000000000001",
		"c9e7a883-ee4f-460f-945f-6668099fb00c",
	}

	got, err := parseIDList(ids[0] + "," + ids[1])
	if err != nil {
		t.Fatalf("parseIDList falló con UUIDs válidos: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("se esperaban 2 ids, se obtuvieron %d", len(got))
	}

	// Espacios y comillas sueltas, que es como los manda el query string.
	got, err = parseIDList(" " + ids[0] + " , , " + ids[1] + " ")
	if err != nil {
		t.Fatalf("parseIDList no tolera espacios ni huecos: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("se esperaban 2 ids ignorando el hueco, se obtuvieron %d", len(got))
	}
}

// Sin ids válidos la selección está vacía: es un error, no un export de todo.
// Exportar todo es lo que pasa cuando NO se manda ?ids.
func TestParseIDListVaciaEsError(t *testing.T) {
	for _, vacia := range []string{"", "   ", ",,,", " , "} {
		if _, err := parseIDList(vacia); err == nil {
			t.Errorf("parseIDList(%q) debería fallar: no hay nada que exportar", vacia)
		}
	}
}

// La lista llega en la URL: sin tope, 100k ids son 100k parámetros.
func TestParseIDListAcotaElTamano(t *testing.T) {
	many := make([]string, maxExportIDs+1)
	for i := range many {
		many[i] = "c9e7a883-ee4f-460f-945f-6668099fb00c"
	}

	if _, err := parseIDList(strings.Join(many, ",")); err == nil {
		t.Errorf("parseIDList debería rechazar más de %d ids", maxExportIDs)
	}
}
