package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

// El motor no debe imponer dimensiones: si el manifest declara un largo o una
// precisión, el DDL tiene que respetarlos.
func TestSQLTypeUsaLasDimensionesDelManifest(t *testing.T) {
	tests := []struct {
		name     string
		fieldRaw string
		want     string
	}{
		{"string sin largo cae al default", `{"type":"string"}`, "VARCHAR(255)"},
		{"string con largo del manifest", `{"type":"string","length":60}`, "VARCHAR(60)"},
		{"alias value tambien define el largo", `{"type":"string","value":255}`, "VARCHAR(255)"},
		{"alias value con largo propio", `{"type":"string","value":40}`, "VARCHAR(40)"},
		{"decimal con precision y escala", `{"type":"decimal","precision":12,"scale":2}`, "NUMERIC(12,2)"},
		{"decimal sin dimensiones", `{"type":"decimal"}`, "NUMERIC(18,4)"},
		{"money default a 2 decimales", `{"type":"money"}`, "NUMERIC(18,2)"},
		{"text ignora el largo", `{"type":"text","length":10}`, "TEXT"},
		{"alias varchar resuelve a string", `{"type":"varchar","length":30}`, "VARCHAR(30)"},
		{"alias int resuelve a integer", `{"type":"int"}`, "INTEGER"},
		{"alias bool resuelve a boolean", `{"type":"bool"}`, "BOOLEAN"},
		{"tipo desconocido cae a TEXT", `{"type":"inventado"}`, "TEXT"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f FieldDef
			if err := json.Unmarshal([]byte(tc.fieldRaw), &f); err != nil {
				t.Fatalf("manifest inválido: %v", err)
			}
			if got := f.SQLType(); got != tc.want {
				t.Errorf("SQLType() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Nada que venga del manifest puede entrar crudo al SQL.
func TestLoadManifestRechazaIdentificadoresPeligrosos(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
	}{
		{
			"campo con inyección SQL",
			`{"name":"m","models":{"item":{"fields":{"x; DROP TABLE users--":{"type":"string"}}}}}`,
		},
		{
			"modelo con inyección SQL",
			`{"name":"m","models":{"item; DROP TABLE users":{"fields":{"x":{"type":"string"}}}}}`,
		},
		{
			"campo con mayúsculas y espacios",
			`{"name":"m","models":{"item":{"fields":{"Mi Campo":{"type":"string"}}}}}`,
		},
		{
			"campo reservado tenant_id",
			`{"name":"m","models":{"item":{"fields":{"tenant_id":{"type":"uuid"}}}}}`,
		},
		{
			"campo reservado id",
			`{"name":"m","models":{"item":{"fields":{"id":{"type":"uuid"}}}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewModuleSDK("mimodulo", "tenant", "", nil)
			if err := s.LoadManifest(tc.manifest); err == nil {
				t.Fatal("se esperaba un error, el manifest fue aceptado")
			}
		})
	}
}

func TestLoadManifestRechazaModuloInvalido(t *testing.T) {
	s := NewModuleSDK("mod; DROP TABLE users", "tenant", "", nil)
	err := s.LoadManifest(`{"name":"m","models":{"item":{"fields":{"x":{"type":"string"}}}}}`)
	if err == nil {
		t.Fatal("se esperaba rechazo del nombre de módulo")
	}
	if !strings.Contains(err.Error(), "módulo") {
		t.Errorf("el error debería mencionar el módulo: %v", err)
	}
}

// El usuario elige entre 10/20/50/100; cualquier otra cosa cae al default.
func TestNormalizeLimit(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{10, 10}, {20, 20}, {50, 50}, {100, 100},
		{0, DefaultPageSize},     // no vino en la query
		{7, DefaultPageSize},     // valor arbitrario
		{-5, DefaultPageSize},    // negativo
		{10000, DefaultPageSize}, // intento de traer toda la tabla
	}

	for _, tc := range tests {
		if got := normalizeLimit(tc.in); got != tc.want {
			t.Errorf("normalizeLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// El orden sólo puede apuntar a campos que el manifest declaró.
func TestNormalizeOrderRechazaCamposNoDeclarados(t *testing.T) {
	s := NewModuleSDK("contacts", "tenant", "", nil)
	manifest := `{"name":"contacts","models":{"contact":{"fields":{"name":{"type":"string"}}}}}`
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest válido rechazado: %v", err)
	}
	model := s.Manifest.Models["contact"]

	tests := []struct {
		orderBy, orderDir string
		wantCol, wantDir  string
	}{
		{"name", "asc", "name", "ASC"},
		{"name", "desc", "name", "DESC"},
		{"created_at", "asc", "created_at", "ASC"},
		{"updated_at", "", "updated_at", "DESC"},
		{"", "", "created_at", "DESC"},
		{"password; DROP TABLE users", "asc", "created_at", "ASC"}, // no declarado → default
		{"name", "; DROP TABLE users", "name", "DESC"},             // dirección inválida → DESC
	}

	for _, tc := range tests {
		col, dir := s.normalizeOrder(model, tc.orderBy, tc.orderDir)
		if col != tc.wantCol || dir != tc.wantDir {
			t.Errorf("normalizeOrder(%q, %q) = (%q, %q), want (%q, %q)",
				tc.orderBy, tc.orderDir, col, dir, tc.wantCol, tc.wantDir)
		}
	}
}

// tenant_id nunca debe salir en las respuestas de la API.
func TestSelectColumnsNoExponeTenantID(t *testing.T) {
	model := &ModelDef{Fields: map[string]*FieldDef{
		"name":  {Type: "string"},
		"email": {Type: "email"},
	}}

	for _, col := range selectColumns(model) {
		if col == "tenant_id" {
			t.Fatal("selectColumns expone tenant_id")
		}
	}

	got := strings.Join(selectColumns(model), ",")
	for _, want := range []string{"id", "name", "email", "created_at", "updated_at"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta la columna %q en %q", want, got)
		}
	}
}

// Los defaults del manifest se escapan antes de tocar el DDL.
func TestSQLLiteral(t *testing.T) {
	tests := []struct {
		in      any
		want    string
		wantErr bool
	}{
		{true, "true", false},
		{float64(42), "42", false},
		{float64(1.5), "1.5", false},
		{"hola", "'hola'", false},
		{"O'Brien", "'O''Brien'", false},                             // comilla escapada
		{"'; DROP TABLE users--", "'''; DROP TABLE users--'", false}, // inyección neutralizada
		{"NOW()", "NOW()", false},                                    // palabra clave permitida
		{[]string{"a"}, "", true},                                    // tipo no soportado
	}

	for _, tc := range tests {
		got, err := sqlLiteral(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("sqlLiteral(%v): se esperaba error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("sqlLiteral(%v): error inesperado: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("sqlLiteral(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// La documentación sale del manifest: si el módulo declara un modelo, aparece.
func TestBuildOpenAPIDerivaLasRutasDelManifest(t *testing.T) {
	manifests := map[string]*Manifest{
		"contacts": {
			Name: "contacts",
			Models: map[string]*ModelDef{
				"contact": {Fields: map[string]*FieldDef{
					"name":  {Type: "string", Required: true, Length: 120},
					"email": {Type: "email"},
				}},
			},
		},
	}

	spec := BuildOpenAPI(manifests, "http://localhost:7071")

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("la especificación no tiene paths")
	}
	for _, want := range []string{"/api/contacts/contact", "/api/contacts/contact/{id}"} {
		if _, exists := paths[want]; !exists {
			t.Errorf("falta la ruta %q", want)
		}
	}

	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)

	input, ok := schemas["contacts_contact_input"].(map[string]any)
	if !ok {
		t.Fatal("falta el schema de entrada")
	}
	required, _ := input["required"].([]string)
	if len(required) != 1 || required[0] != "name" {
		t.Errorf("required = %v, want [name]", required)
	}

	props := input["properties"].(map[string]any)
	nameProp := props["name"].(map[string]any)
	if nameProp["maxLength"] != 120 {
		t.Errorf("maxLength = %v, want 120 (el largo declarado en el manifest)", nameProp["maxLength"])
	}
	emailProp := props["email"].(map[string]any)
	if emailProp["format"] != "email" {
		t.Errorf("format = %v, want email", emailProp["format"])
	}
}

// El manifest manda, pero nunca a costa de truncar datos ya guardados.
func TestIsSafeWidening(t *testing.T) {
	tests := []struct {
		name    string
		current columnInfo
		desired string
		want    bool
	}{
		{"varchar mas ancho es seguro",
			columnInfo{DataType: "character varying", MaxLength: 160}, "VARCHAR(255)", true},
		{"varchar mas angosto NO es seguro",
			columnInfo{DataType: "character varying", MaxLength: 255}, "VARCHAR(160)", false},
		{"varchar a text es seguro",
			columnInfo{DataType: "character varying", MaxLength: 255}, "TEXT", true},
		{"numeric con mas escala y enteros es seguro",
			columnInfo{DataType: "numeric", Precision: 14, Scale: 2}, "NUMERIC(18,4)", true},
		{"numeric perdiendo enteros NO es seguro",
			columnInfo{DataType: "numeric", Precision: 18, Scale: 2}, "NUMERIC(10,2)", false},
		{"numeric perdiendo decimales NO es seguro",
			columnInfo{DataType: "numeric", Precision: 18, Scale: 4}, "NUMERIC(18,2)", false},
		{"cambiar de familia NO es seguro",
			columnInfo{DataType: "character varying", MaxLength: 50}, "INTEGER", false},
		{"numeric a text NO es seguro",
			columnInfo{DataType: "numeric", Precision: 10, Scale: 2}, "TEXT", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSafeWidening(tc.current, tc.desired); got != tc.want {
				t.Errorf("isSafeWidening(%s → %s) = %v, want %v",
					tc.current.SQLType(), tc.desired, got, tc.want)
			}
		})
	}
}

// El tipo actual debe reconstruirse igual que lo produce FieldDef.SQLType(),
// si no toda comparación daría falso positivo y migraría en cada arranque.
func TestColumnInfoSQLTypeCoincideConFieldDef(t *testing.T) {
	tests := []struct {
		current columnInfo
		field   FieldDef
	}{
		{columnInfo{DataType: "character varying", MaxLength: 160}, FieldDef{Type: "string", Length: 160}},
		{columnInfo{DataType: "numeric", Precision: 14, Scale: 2}, FieldDef{Type: "money", Precision: 14, Scale: 2}},
		{columnInfo{DataType: "text"}, FieldDef{Type: "text"}},
		{columnInfo{DataType: "boolean"}, FieldDef{Type: "boolean"}},
		{columnInfo{DataType: "uuid"}, FieldDef{Type: "uuid"}},
		{columnInfo{DataType: "timestamp without time zone"}, FieldDef{Type: "datetime"}},
	}

	for _, tc := range tests {
		if got, want := tc.current.SQLType(), tc.field.SQLType(); got != want {
			t.Errorf("columnInfo.SQLType() = %q, FieldDef.SQLType() = %q", got, want)
		}
	}
}

// El orden en que el módulo declara sus campos es información: define el orden
// de las columnas. Un map de Go lo perdería.
func TestFieldOrderRespetaElManifest(t *testing.T) {
	manifest := `{"name":"m","models":{"item":{"fields":{
		"zeta":{"type":"string"},
		"alfa":{"type":"string"},
		"medio":{"type":"string"}
	}}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	got := s.Manifest.Models["item"].OrderedFields()
	want := []string{"zeta", "alfa", "medio"} // declaración, no alfabético
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("OrderedFields() = %v, want %v", got, want)
	}
}

// Sin bloque "views", el motor deduce las tres vistas del propio esquema.
func TestMetaDeduceLasVistasSinConfiguracion(t *testing.T) {
	manifest := `{"name":"crm","models":{"lead":{"label":"Oportunidad","fields":{
		"title":{"type":"string","required":true,"label":"Título"},
		"contact":{"type":"string"},
		"stage":{"type":"string","options":["NUEVO","GANADO"]},
		"notes":{"type":"text"},
		"amount":{"type":"money"}
	}}}}`

	s := NewModuleSDK("crm", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	meta, err := s.Meta("lead")
	if err != nil {
		t.Fatalf("Meta() falló: %v", err)
	}

	if meta.Label != "Oportunidad" {
		t.Errorf("Label = %q, want Oportunidad", meta.Label)
	}

	// "notes" es text: no cabe en una celda, queda fuera de la tabla.
	for _, col := range meta.Views.List.Columns {
		if col == "notes" {
			t.Error("un campo text no debería ser columna de la tabla")
		}
	}

	// El identificador es el primer campo obligatorio de texto.
	if meta.Views.Card.Title != "title" {
		t.Errorf("card.title = %q, want title", meta.Views.Card.Title)
	}
	// Kanban agrupa por el campo con opciones, así el conteo por columna es real.
	if meta.Views.Kanban == nil || meta.Views.Kanban.GroupBy != "stage" {
		t.Errorf("kanban.group_by = %v, want stage", meta.Views.Kanban)
	}
	if meta.Views.Default != "list" {
		t.Errorf("default = %q, want list", meta.Views.Default)
	}
}

// Sin ningún campo agrupable no se ofrece kanban: mejor no darla que darla mal.
func TestMetaSinCamposAgrupablesNoOfreceKanban(t *testing.T) {
	manifest := `{"name":"m","models":{"item":{"fields":{
		"name":{"type":"string","required":true},
		"qty":{"type":"integer"}
	}}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	meta, _ := s.Meta("item")
	if meta.Views.Kanban != nil {
		t.Errorf("no debería ofrecer kanban sin campos con options: %+v", meta.Views.Kanban)
	}
}

// Lo que el módulo declara en "views" gana sobre la deducción.
func TestMetaRespetaLasVistasDeclaradas(t *testing.T) {
	manifest := `{"name":"m","models":{"item":{
		"views":{"default":"card","list":{"columns":["sku"]}},
		"fields":{
			"name":{"type":"string","required":true},
			"sku":{"type":"string"}
		}}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	meta, _ := s.Meta("item")
	if len(meta.Views.List.Columns) != 1 || meta.Views.List.Columns[0] != "sku" {
		t.Errorf("columns = %v, want [sku] (lo declarado gana)", meta.Views.List.Columns)
	}
	if meta.Views.Default != "card" {
		t.Errorf("default = %q, want card", meta.Views.Default)
	}
	// Lo no declarado se sigue deduciendo.
	if meta.Views.Card == nil || meta.Views.Card.Title != "name" {
		t.Errorf("card debería deducirse igual: %+v", meta.Views.Card)
	}
}

// El control HTML sale del tipo declarado: el módulo no elige inputs.
func TestInputControlDerivaDelTipo(t *testing.T) {
	tests := []struct {
		field FieldDef
		want  string
	}{
		{FieldDef{Type: "string"}, "text"},
		{FieldDef{Type: "text"}, "textarea"},
		{FieldDef{Type: "email"}, "email"},
		{FieldDef{Type: "phone"}, "tel"},
		{FieldDef{Type: "money"}, "number"},
		{FieldDef{Type: "boolean"}, "checkbox"},
		{FieldDef{Type: "date"}, "date"},
		{FieldDef{Type: "string", Options: []string{"A", "B"}}, "select"}, // options gana
	}

	for _, tc := range tests {
		if got := inputControl(&tc.field); got != tc.want {
			t.Errorf("inputControl(%s, options=%v) = %q, want %q",
				tc.field.Type, tc.field.Options, got, tc.want)
		}
	}
}

// Los ILIKE necesitan comodines; el resto de operadores pasa el valor tal cual.
func TestFilterValue(t *testing.T) {
	tests := []struct{ op, in, want string }{
		{"contains", "juan", "%juan%"},
		{"starts", "juan", "juan%"},
		{"eq", "juan", "juan"},
		{"gte", "1000", "1000"},
	}
	for _, tc := range tests {
		if got := filterValue(tc.op, tc.in); got != tc.want {
			t.Errorf("filterValue(%q, %q) = %q, want %q", tc.op, tc.in, got, tc.want)
		}
	}
}
