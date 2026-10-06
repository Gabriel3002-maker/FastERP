package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// meta construye la metadata de un modelo de prueba. validateInput trabaja sobre
// la metadata calculada y no sobre los campos sueltos, porque de ahí salen los
// tipos normalizados, los options y los searchable.
func meta() *module.ModelMeta {
	m := &module.Manifest{
		Name:   "test",
		Models: map[string]*module.ModelDef{"thing": {Name: "thing", Fields: fields()}},
	}
	return m.MustMeta("thing")
}

func validate(t *testing.T, body map[string]any, creating bool) error {
	t.Helper()
	mt := meta()
	return validateInput(mt, body, creating)
}

func TestValidateInputAccepts(t *testing.T) {
	body := map[string]any{
		"name":   "Acme",
		"notes":  "long text",
		"qty":    float64(3), // JSON numbers decode to float64
		"price":  12.5,
		"active": true,
		"due":    "2026-07-18",
	}
	if err := validate(t, body, true); err != nil {
		t.Fatalf("validateInput: %v", err)
	}
}

func TestValidateInputRejects(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"required missing", map[string]any{"qty": float64(1)}},
		{"required explicitly null", map[string]any{"name": nil}},
		{"string too long", map[string]any{"name": stringOfLen(256)}},
		{"string given a number", map[string]any{"name": float64(1)}},
		{"int given a string", map[string]any{"name": "x", "qty": "3"}},
		{"float given a string", map[string]any{"name": "x", "price": "1.5"}},
		{"bool given a string", map[string]any{"name": "x", "active": "true"}},
		{"date given a number", map[string]any{"name": "x", "due": float64(20260718)}},
		// Un campo que no existe en el manifest se rechaza, no se ignora: si se
		// ignorara, el registro se crearía a medias sin avisar.
		{"unknown field", map[string]any{"name": "x", "nope": 1}},
		// Un entero con parte decimal no es un entero. Sin esta comprobación
		// Postgres lo redondearía en silencio.
		{"int given a float", map[string]any{"name": "x", "qty": 1.5}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate(t, tc.body, true); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestValidateInputAllowsOptionalFieldsToBeOmitted(t *testing.T) {
	if err := validate(t, map[string]any{"name": "Acme"}, true); err != nil {
		t.Fatalf("validateInput: %v", err)
	}
}

// En una actualización nada es obligatorio: se manda lo que cambia.
func TestValidateInputOnUpdateRequiresNothing(t *testing.T) {
	if err := validate(t, map[string]any{"notes": "solo esto"}, false); err != nil {
		t.Fatalf("update should not require fields: %v", err)
	}
	// Pero un campo obligatorio ausente sigue siendo un error si se manda.
	if err := validate(t, map[string]any{}, true); err == nil {
		t.Fatal("create with no required field should fail")
	}
}

// Un enum con valores declarados solo acepta esos.
func TestValidateInputHonoursOptions(t *testing.T) {
	m := &module.Manifest{
		Name: "test",
		Models: map[string]*module.ModelDef{"thing": {Name: "thing", Fields: []module.FieldDef{
			{Name: "state", Type: "string", Options: []string{"draft", "done"}},
		}}},
	}
	mt := m.MustMeta("thing")

	if err := validateInput(mt, map[string]any{"state": "draft"}, true); err != nil {
		t.Fatalf("declared option should be accepted: %v", err)
	}
	if err := validateInput(mt, map[string]any{"state": "inventado"}, true); err == nil {
		t.Fatal("undeclared option should be rejected")
	}
}

// A 255-char string is the documented boundary and must be accepted.
func TestValidateInputStringBoundary(t *testing.T) {
	if err := validate(t, map[string]any{"name": stringOfLen(255)}, true); err != nil {
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
	r.GET("/api/:module/:model", h.dispatchModel("read", listHandler))

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

// searchModel tiene un par de campos buscables, que es lo que hace que la
// búsqueda construya un grupo con paréntesis.
func searchModel() (*module.Manifest, module.ModelRegistration) {
	m := &module.Manifest{
		Name: "crm",
		Models: map[string]*module.ModelDef{"lead": {Name: "lead", Fields: []module.FieldDef{
			{Name: "name", Type: "string"},
			{Name: "email", Type: "string"},
			{Name: "amount", Type: "float"},
			// Readonly también deja el campo fuera de la búsqueda: un campo
			// calculado no es un dato de entrada.
			{Name: "internal_id", Type: "string", Readonly: true},
		}}},
	}
	return m, module.ModelRegistration{
		Manifest:  m.Models["lead"],
		TableName: "mod_crm_lead",
	}
}

// El grupo de búsqueda tiene que quedar entre paréntesis detrás del AND del
// tenant. Sin ellos, "tenant = A OR nombre ILIKE %x%" devuelve los registros de
// otro tenant: es una fuga de datos, no un fallo de forma.
func TestApplySearchKeepsTenantFilterOutsideTheGroup(t *testing.T) {
	m, reg := searchModel()
	qb := module.NewQueryBuilder(reg)
	qb.Where("tenant_id", "=", "t-1")
	applySearch(qb, m, reg, "acme")

	_, q, args, err := qb.BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}

	// El ESCAPE va dentro del paréntesis, en cada ILIKE, y no una vez al
	// final: Postgres rechaza "(... ILIKE ?) ESCAPE '\'" con syntax error, y
	// con él la búsqueda devolvía 500 en el listado y en el export.
	want := `WHERE "tenant_id" = $1 AND ("name" ILIKE $2 ESCAPE '\' OR "email" ILIKE $3 ESCAPE '\')`
	if !strings.Contains(q, want) {
		t.Errorf("query %q\ndoes not contain\n%q", q, want)
	}
	if strings.Contains(q, `) ESCAPE`) {
		t.Errorf("query %q tiene el ESCAPE fuera del paréntesis; Postgres lo rechaza", q)
	}
	if len(args) != 3 || args[0] != "t-1" {
		t.Errorf("args = %v, want the tenant first", args)
	}
	if args[1] != "%acme%" || args[2] != "%acme%" {
		t.Errorf("args = %v, want one pattern per searched column", args)
	}
	// Un campo marcado como no buscable no puede aparecer en el grupo.
	if strings.Contains(q, "internal_id ILIKE") {
		t.Errorf("query %q searched a non-searchable field", q)
	}
	// amount es float: un ILIKE sobre un número es un error en Postgres.
	if strings.Contains(q, "amount ILIKE") {
		t.Errorf("query %q searched a numeric column", q)
	}
}

// Un % escrito por quien busca es un % de verdad, no un comodín. Sin el
// ESCAPE y el escape previo, "%" traería todas las filas.
func TestApplySearchEscapesWildcards(t *testing.T) {
	m, reg := searchModel()
	qb := module.NewQueryBuilder(reg)
	qb.Where("tenant_id", "=", "t-1")
	applySearch(qb, m, reg, "100%_off")

	_, _, args, err := qb.BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	if args[1] != `%100\%\_off%` {
		t.Errorf("pattern = %q, want the wildcards escaped", args[1])
	}
}

// Un modelo sin campos buscables no puede devolver todo el tenant. Un ILIKE
// sobre un UUID no tiene sentido, y devolverlo todo sería ignorar la búsqueda.
func TestApplySearchWithNoSearchableFieldsReturnsNothing(t *testing.T) {
	m := &module.Manifest{
		Name: "crm",
		Models: map[string]*module.ModelDef{"lead": {Name: "lead", Fields: []module.FieldDef{
			{Name: "amount", Type: "float"},
		}}},
	}
	reg := module.ModelRegistration{Manifest: m.Models["lead"], TableName: "mod_crm_lead"}

	qb := module.NewQueryBuilder(reg)
	qb.Where("tenant_id", "=", "t-1")
	applySearch(qb, m, reg, "acme")

	_, q, _, err := qb.BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	if !strings.Contains(q, `AND FALSE`) {
		t.Errorf("query %q should return no rows", q)
	}
}

// Un filtro con dos puntos en el valor no debe romperse: un UUID, una hora o una
// URL los tienen. Con un Split a secas el valor se partiría por la mitad.
func TestParseFiltersKeepsColonsInTheValue(t *testing.T) {
	m, reg := searchModel()
	_ = m

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?filter=name:eq:a:b:c", nil)

	filters, err := parseFilters(c, &reg)
	if err != nil {
		t.Fatalf("parseFilters: %v", err)
	}
	if len(filters) != 1 || filters[0].value != "a:b:c" {
		t.Errorf("filters = %+v, want the value intact", filters)
	}
}

func TestParseFiltersRejects(t *testing.T) {
	m, reg := searchModel()
	_ = m

	cases := []struct{ query, want string }{
		{"?filter=name", "campo:operador:valor"},
		{"?filter=nope:eq:x", `no existe`},
		{"?filter=name:EXISTS:x", "no permitido"},
		{"?filter=name:IN:", "al menos un valor"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/"+tc.query, nil)
			_, err := parseFilters(c, &reg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}
