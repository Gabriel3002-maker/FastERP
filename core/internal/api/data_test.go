package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fasterp/backend/internal/module"
)

func fields() []module.FieldDef {
	return []module.FieldDef{
		{Name: "name", Type: "string", Required: true},
		{Name: "notes", Type: "text"},
		{Name: "qty", Type: "int"},
		{Name: "price", Type: "float"},
		{Name: "active", Type: "bool"},
		{Name: "due", Type: "date"},
	}
}

func TestValidateInputAccepts(t *testing.T) {
	body := map[string]interface{}{
		"name":   "Acme",
		"notes":  "long text",
		"qty":    float64(3), // JSON numbers decode to float64
		"price":  12.5,
		"active": true,
		"due":    "2026-07-18",
	}
	if err := validateInput(fields(), body); err != nil {
		t.Fatalf("validateInput: %v", err)
	}
}

func TestValidateInputRejects(t *testing.T) {
	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"required missing", map[string]interface{}{"qty": float64(1)}},
		{"required explicitly null", map[string]interface{}{"name": nil}},
		{"string too long", map[string]interface{}{"name": stringOfLen(256)}},
		{"string given a number", map[string]interface{}{"name": float64(1)}},
		{"int given a string", map[string]interface{}{"name": "x", "qty": "3"}},
		{"float given a string", map[string]interface{}{"name": "x", "price": "1.5"}},
		{"bool given a string", map[string]interface{}{"name": "x", "active": "true"}},
		{"date given a number", map[string]interface{}{"name": "x", "due": float64(20260718)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateInput(fields(), tc.body); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestValidateInputAllowsOptionalFieldsToBeOmitted(t *testing.T) {
	if err := validateInput(fields(), map[string]interface{}{"name": "Acme"}); err != nil {
		t.Fatalf("validateInput: %v", err)
	}
}

// A 255-char string is the documented boundary and must be accepted.
func TestValidateInputStringBoundary(t *testing.T) {
	if err := validateInput(fields(), map[string]interface{}{"name": stringOfLen(255)}); err != nil {
		t.Fatalf("255 chars should be accepted: %v", err)
	}
}

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// dispatchModel must reject an unknown module before it ever touches the database,
// which is what makes the single-dispatcher design safe to register for all paths.
func TestDispatchModelUnknownModule(t *testing.T) {
	h := NewHandler(nil, testSecret, testSecret+"-refresh", gin.New())

	r := gin.New()
	r.GET("/api/:module/:model", h.dispatchModel(listHandler))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/definitely-not-a-module/thing", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", w.Code)
	}
}

// Static routes must win over the /:module/:model wildcard, otherwise registering the
// dispatcher would shadow the core API.
func TestStaticRoutesOutrankModuleDispatcher(t *testing.T) {
	r := gin.New()
	api := r.Group("/api")
	api.GET("/modules", func(c *gin.Context) { c.String(http.StatusOK, "core") })
	api.GET("/me", func(c *gin.Context) { c.String(http.StatusOK, "core") })
	api.GET("/:module/:model", func(c *gin.Context) { c.String(http.StatusOK, "dispatcher") })
	api.GET("/:module/:model/:id", func(c *gin.Context) { c.String(http.StatusOK, "dispatcher") })

	cases := []struct{ path, want string }{
		{"/api/modules", "core"},
		{"/api/me", "core"},
		{"/api/contacts/contact", "dispatcher"},
		{"/api/contacts/contact/42", "dispatcher"},
	}

	for _, tc := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Body.String() != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.path, w.Body.String(), tc.want)
		}
	}
}
