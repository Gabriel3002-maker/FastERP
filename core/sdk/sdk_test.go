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

func TestLoadManifestRechazaRelacionesSinRelatedModel(t *testing.T) {
	s := NewModuleSDK("contacts", "tenant", "", nil)
	manifest := `{
		"name":"contacts",
		"models":{
			"contact":{
				"fields":{
					"company_id":{"type":"many2one"}
				}
			}
		}
	}`
	if err := s.LoadManifest(manifest); err == nil {
		t.Fatal("se esperaba error por falta de related_model")
	}
}

func TestLoadManifestAceptaRelacionesConRelatedModel(t *testing.T) {
	s := NewModuleSDK("contacts", "tenant", "", nil)
	manifest := `{
		"name":"contacts",
		"models":{
			"contact":{
				"fields":{
					"company_id":{"type":"many2one","related_model":"company"}
				}
			}
		}
	}`
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifiesto válido rechazado: %v", err)
	}
	field := s.Manifest.Models["contact"].Fields["company_id"]
	if field.RelatedModule != "contacts" || field.RelatedModel != "company" {
		t.Fatalf("relación no inicializada correctamente: %#v", field)
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
		def     *FieldDef
		in      any
		want    string
		wantErr bool
	}{
		{nil, true, "true", false},
		{nil, float64(42), "42", false},
		{nil, float64(1.5), "1.5", false},
		{nil, "hola", "'hola'", false},
		{nil, "O'Brien", "'O''Brien'", false},                             // comilla escapada
		{nil, "'; DROP TABLE users--", "'''; DROP TABLE users--'", false}, // inyección neutralizada
		{nil, "NOW()", "NOW()", false},                                    // palabra clave permitida
		{nil, []string{"a"}, "", true},                                    // tipo no soportado

		// El tipo del campo decide la serialización del default.
		{&FieldDef{Type: "json"}, map[string]any{"a": 1}, `'{"a":1}'`, false},
		{&FieldDef{Type: "one2many"}, []string{"x"}, `'["x"]'`, false},
		{&FieldDef{Type: "many2many"}, []string{"x"}, `'["x"]'`, false},
		{&FieldDef{Type: "json"}, map[string]any{"a": "O'Brien"}, `'{"a":"O''Brien"}'`, false},

		{&FieldDef{Type: "uuid[]"}, nil, "ARRAY[]::UUID[]", false},
		{&FieldDef{Type: "uuid[]"}, []string{}, "ARRAY[]::UUID[]", false},
		{
			&FieldDef{Type: "uuid[]"},
			[]string{"6f1c1a3e-0000-4000-8000-000000000001"},
			"ARRAY['6f1c1a3e-0000-4000-8000-000000000001'::uuid]::UUID[]",
			false,
		},
		{&FieldDef{Type: "uuid[]"}, []string{"no-es-uuid"}, "", true},
	}

	for _, tc := range tests {
		got, err := sqlLiteral(tc.def, tc.in)
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

// El driver entrega uuid[] como bytes con la literal de Postgres: si no se
// convierte, la API devolvería el texto "{a,b}" en vez de un arreglo.
func TestParsePostgresUUIDArray(t *testing.T) {
	a := "6f1c1a3e-0000-4000-8000-000000000001"
	b := "6f1c1a3e-0000-4000-8000-000000000002"

	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"{}", nil, false},
		{"", nil, false},
		{"{ }", nil, false},
		{"NULL", nil, false},
		{"{" + a + "}", []string{a}, false},
		{"{" + a + "," + b + "}", []string{a, b}, false},
		{"{ " + a + " , " + b + " }", []string{a, b}, false}, // espacios del driver
		{"{\"x\",\"y\"}", nil, true},                         // elementos no-UUID entrecomillados
		{"{" + a + ",NULL}", []string{a}, false},             // NULL = sin valor
		{"{roto", nil, true},
		{"no-es-array", nil, true},
		{"{" + a + ",no-es-uuid}", nil, true},
	}

	for _, tc := range tests {
		got, err := parsePostgresUUIDArray(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parsePostgresUUIDArray(%q): se esperaba error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePostgresUUIDArray(%q): error inesperado: %v", tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("parsePostgresUUIDArray(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parsePostgresUUIDArray(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

// El OpenAPI promete un arreglo para uuid[]: el valor debe llegar como tal.
func TestNormalizeValueDeserializaUUIDArray(t *testing.T) {
	def := &FieldDef{Type: "uuid[]"}
	a := "6f1c1a3e-0000-4000-8000-000000000001"
	b := "6f1c1a3e-0000-4000-8000-000000000002"

	got := normalizeValue([]byte("{"+a+","+b+"}"), def)
	arr, ok := got.([]string)
	if !ok {
		t.Fatalf("normalizeValue devolvió %T, se esperaba []string", got)
	}
	if len(arr) != 2 || arr[0] != a || arr[1] != b {
		t.Errorf("normalizeValue = %v, want [%s %s]", arr, a, b)
	}

	// Un array vacío es un arreglo vacío, no null: el tipo declarado es array.
	empty := normalizeValue([]byte("{}"), def)
	if arr, ok := empty.([]string); !ok || len(arr) != 0 {
		t.Errorf("normalizeValue(\"{}\") = %#v, want []string vacío", empty)
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

// sequence controla el orden de presentación sin reordenar el archivo.
func TestSequenceMandaSobreElOrdenDelArchivo(t *testing.T) {
	manifest := `{"name":"m","models":{"item":{"fields":{
		"tercero":{"type":"string","sequence":30},
		"primero":{"type":"string","sequence":10},
		"segundo":{"type":"string","sequence":20}
	}}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	got := s.Manifest.Models["item"].OrderedFields()
	want := "primero,segundo,tercero"
	if strings.Join(got, ",") != want {
		t.Errorf("OrderedFields() = %v, want [%s]", got, want)
	}
}

// La sequence implícita deja huecos (10, 20, 30…) para poder intercalar un
// campo sin tener que numerar todos los demás.
func TestSequenceImplicitaPermiteIntercalar(t *testing.T) {
	// "cuña" con sequence 15 debe caer entre el 1º (10) y el 2º (20).
	manifest := `{"name":"m","models":{"item":{"fields":{
		"uno":{"type":"string"},
		"dos":{"type":"string"},
		"cuna":{"type":"string","sequence":15}
	}}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	got := s.Manifest.Models["item"].OrderedFields()
	want := "uno,cuna,dos"
	if strings.Join(got, ",") != want {
		t.Errorf("OrderedFields() = %v, want [%s]", got, want)
	}
}

// El formulario lleva TODOS los campos editables, no un recorte como la tabla:
// es justo donde se pierden campos cuando el esquema crece.
func TestFormIncluyeTodosLosCamposEditables(t *testing.T) {
	manifest := `{"name":"m","models":{"item":{"fields":{
		"a":{"type":"string","required":true},
		"b":{"type":"text"},
		"c":{"type":"money"},
		"d":{"type":"string"},
		"e":{"type":"string"},
		"f":{"type":"string"},
		"g":{"type":"string"},
		"calculado":{"type":"string","readonly":true}
	}}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	meta, _ := s.Meta("item")

	// La tabla recorta a maxInferredColumns; el formulario no.
	if len(meta.Views.List.Columns) > maxInferredColumns {
		t.Errorf("la tabla no debería pasar de %d columnas: %v",
			maxInferredColumns, meta.Views.List.Columns)
	}
	if len(meta.Views.Form.Fields) != 7 { // los 8 menos el readonly
		t.Errorf("form.fields = %v, want los 7 editables", meta.Views.Form.Fields)
	}
	for _, f := range meta.Views.Form.Fields {
		if f == "calculado" {
			t.Error("un campo readonly no debería ser editable en el formulario")
		}
	}
}

// El largo se valida contra el manifest antes de llegar a Postgres, para poder
// decir qué campo falló en vez de filtrar "pq: value too long for type...".
func TestCheckLength(t *testing.T) {
	tests := []struct {
		name    string
		def     FieldDef
		value   any
		wantErr bool
	}{
		{"dentro del largo", FieldDef{Length: 10}, "corto", false},
		{"justo en el límite", FieldDef{Length: 5}, "12345", false},
		{"excedido", FieldDef{Length: 5}, "123456", true},
		{"sin largo declarado", FieldDef{}, strings.Repeat("x", 500), false},
		{"no es texto", FieldDef{Length: 2}, 12345, false},
		// Los acentos son un carácter, no dos: se cuentan runas, no bytes.
		{"acentos cuentan como un carácter", FieldDef{Length: 5}, "áéíóú", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLength("campo", &tc.def, tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("checkLength(%v) error = %v, wantErr = %v", tc.value, err, tc.wantErr)
			}
		})
	}
}

// El mensaje usa la etiqueta del manifest, que es lo que la persona ve en el
// formulario.
func TestCheckLengthUsaLaEtiquetaDelManifest(t *testing.T) {
	def := FieldDef{Length: 20, Label: "Número de identificación"}
	err := checkLength("tax_id", &def, strings.Repeat("9", 25))
	if err == nil {
		t.Fatal("se esperaba error")
	}
	if !strings.Contains(err.Error(), "Número de identificación") {
		t.Errorf("el error debería usar la etiqueta: %v", err)
	}
}

// El driver entrega NUMERIC como []byte; el manifest dice que es un número.
// Sin esta conversión la API contradiría a su propio OpenAPI.
func TestNormalizeValueRespetaElTipoDeclarado(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		def  *FieldDef
		want any
	}{
		{"money llega como bytes y sale número", []byte("2500.75"), &FieldDef{Type: "money"}, 2500.75},
		{"entero", []byte("42"), &FieldDef{Type: "integer"}, int64(42)},
		{"booleano", []byte("true"), &FieldDef{Type: "boolean"}, true},
		{"texto queda texto", []byte("hola"), &FieldDef{Type: "string"}, "hola"},
		{"columna del core sin declarar", []byte("abc"), nil, "abc"},
		{"nulo se conserva", nil, &FieldDef{Type: "money"}, nil},
		{"lo que no es bytes pasa igual", 7, &FieldDef{Type: "integer"}, 7},
		{"número corrupto cae a texto", []byte("no-es-numero"), &FieldDef{Type: "money"}, "no-es-numero"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeValue(tc.raw, tc.def); got != tc.want {
				t.Errorf("normalizeValue() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// ── Workflow ─────────────────────────────────────────────────────────────

// El manifest referencia un workflow.field, así que debe existir en Fields:
// si no, cualquier transición fallaría al primer intento en vez de al cargar.
func TestValidateWorkflowRechazaCampoInexistente(t *testing.T) {
	manifest := `{"name":"m","models":{"pedido":{
		"fields":{"total":{"type":"money"}},
		"workflow":{"field":"estado","initial":"nuevo","transitions":{
			"avanzar":{"from":["nuevo"],"to":"listo"}
		}}
	}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err == nil {
		t.Fatal("se esperaba rechazo: workflow.field no existe en fields")
	}
}

// El campo de estado necesita "options": son los únicos estados legales, y
// sin ellos no hay contra qué validar initial ni las transiciones.
func TestValidateWorkflowRequiereOptions(t *testing.T) {
	manifest := `{"name":"m","models":{"pedido":{
		"fields":{"estado":{"type":"string"}},
		"workflow":{"field":"estado","initial":"nuevo","transitions":{
			"avanzar":{"from":["nuevo"],"to":"listo"}
		}}
	}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err == nil {
		t.Fatal("se esperaba rechazo: el campo de estado no declara options")
	}
}

func TestValidateWorkflowInitialDebeEstarEnOptions(t *testing.T) {
	manifest := `{"name":"m","models":{"pedido":{
		"fields":{"estado":{"type":"string","options":["nuevo","listo"]}},
		"workflow":{"field":"estado","initial":"fantasma","transitions":{
			"avanzar":{"from":["nuevo"],"to":"listo"}
		}}
	}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err == nil {
		t.Fatal("se esperaba rechazo: initial no está en options")
	}
}

func TestValidateWorkflowTransicionRechazaEstadosFueraDeOptions(t *testing.T) {
	tests := []struct {
		name       string
		transition string
	}{
		{"origen inventado", `"avanzar":{"from":["fantasma"],"to":"listo"}`},
		{"destino inventado", `"avanzar":{"from":["nuevo"],"to":"fantasma"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manifest := `{"name":"m","models":{"pedido":{
				"fields":{"estado":{"type":"string","options":["nuevo","listo"]}},
				"workflow":{"field":"estado","initial":"nuevo","transitions":{` + tc.transition + `}}
			}}}`

			s := NewModuleSDK("m", "t", "", nil)
			if err := s.LoadManifest(manifest); err == nil {
				t.Fatal("se esperaba rechazo: estado fuera de options")
			}
		})
	}
}

func TestValidateWorkflowSinTransicionesSeRechaza(t *testing.T) {
	manifest := `{"name":"m","models":{"pedido":{
		"fields":{"estado":{"type":"string","options":["nuevo"]}},
		"workflow":{"field":"estado","initial":"nuevo","transitions":{}}
	}}}`

	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err == nil {
		t.Fatal("se esperaba rechazo: workflow sin transiciones declaradas")
	}
}

// Un manifest de workflow bien formado se acepta sin objeciones.
func TestValidateWorkflowManifestValidoPasa(t *testing.T) {
	manifest := `{"name":"crm","models":{"lead":{
		"fields":{
			"nombre":{"type":"string","required":true},
			"estado":{"type":"string","options":["prospecto","activo","inactivo"]}
		},
		"workflow":{"field":"estado","initial":"prospecto","transitions":{
			"activar":{"from":["prospecto","inactivo"],"to":"activo","label":"Activar"},
			"desactivar":{"from":["prospecto","activo"],"to":"inactivo"}
		}}
	}}}`

	s := NewModuleSDK("crm", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest válido rechazado: %v", err)
	}
}

// Modelos sin workflow no se ven afectados: sigue sin ser obligatorio.
func TestModeloSinWorkflowSigueFuncionando(t *testing.T) {
	manifest := `{"name":"m","models":{"item":{"fields":{"name":{"type":"string"}}}}}`
	s := NewModuleSDK("m", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest sin workflow rechazado: %v", err)
	}
	if s.Manifest.Models["item"].Workflow != nil {
		t.Fatal("un modelo sin bloque workflow no debería tener uno")
	}
}

// rejectWorkflowField es lo que impide saltarse el flujo escribiendo el
// estado a mano por PUT: sin esto, el workflow sería decorativo.
func TestRejectWorkflowField(t *testing.T) {
	withWorkflow := &ModelDef{
		Fields:   map[string]*FieldDef{"estado": {Type: "string", Options: []string{"a", "b"}}},
		Workflow: &WorkflowDef{Field: "estado", Initial: "a"},
	}
	withoutWorkflow := &ModelDef{Fields: map[string]*FieldDef{"estado": {Type: "string"}}}

	tests := []struct {
		name    string
		model   *ModelDef
		data    map[string]any
		wantErr bool
	}{
		{"intenta fijar el estado directamente", withWorkflow, map[string]any{"estado": "b"}, true},
		{"no toca el campo de estado", withWorkflow, map[string]any{"nombre": "x"}, false},
		{"sin workflow, el campo es libre", withoutWorkflow, map[string]any{"estado": "cualquiera"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectWorkflowField(tc.model, tc.data)
			if (err != nil) != tc.wantErr {
				t.Errorf("rejectWorkflowField() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestContainsState(t *testing.T) {
	if !containsState([]string{"a", "b"}, "b") {
		t.Error("debería encontrar 'b'")
	}
	if containsState([]string{"a", "b"}, "c") {
		t.Error("no debería encontrar 'c'")
	}
	if containsState(nil, "a") {
		t.Error("una lista vacía no contiene nada")
	}
}

// _meta expone el flujo completo, con etiquetas por defecto cuando el
// manifest no las puso — es lo que el cliente usa para dibujar los botones
// sin volver a preguntarle nada al servidor.
func TestMetaExponeElWorkflow(t *testing.T) {
	manifest := `{"name":"crm","models":{"lead":{
		"fields":{
			"nombre":{"type":"string","required":true},
			"estado":{"type":"string","options":["prospecto","activo","inactivo"]}
		},
		"workflow":{"field":"estado","initial":"prospecto","transitions":{
			"activar":{"from":["prospecto","inactivo"],"to":"activo","label":"Activar"},
			"desactivar":{"from":["prospecto","activo"],"to":"inactivo"}
		}}
	}}}`

	s := NewModuleSDK("crm", "t", "", nil)
	if err := s.LoadManifest(manifest); err != nil {
		t.Fatalf("manifest rechazado: %v", err)
	}

	meta, err := s.Meta("lead")
	if err != nil {
		t.Fatalf("Meta() falló: %v", err)
	}

	if meta.Workflow == nil {
		t.Fatal("meta.Workflow no debería ser nil")
	}
	if meta.Workflow.Field != "estado" || meta.Workflow.Initial != "prospecto" {
		t.Errorf("workflow = %+v", meta.Workflow)
	}

	activar, ok := meta.Workflow.Transitions["activar"]
	if !ok {
		t.Fatal("falta la transición 'activar'")
	}
	if activar.Label != "Activar" {
		t.Errorf("label declarada no respetada: %q", activar.Label)
	}

	desactivar, ok := meta.Workflow.Transitions["desactivar"]
	if !ok {
		t.Fatal("falta la transición 'desactivar'")
	}
	if desactivar.Label != "Desactivar" { // sin label explícita: se humaniza el nombre
		t.Errorf("label deducida = %q, want Desactivar", desactivar.Label)
	}

	// El campo de estado no debe aparecer como editable en el formulario.
	for _, f := range meta.Views.Form.Fields {
		if f == "estado" {
			t.Error("el campo de estado no debería estar en el formulario: se mueve con transiciones")
		}
	}

	// Kanban debe agrupar por el campo del workflow, no por cualquier otro
	// campo con options que hubiera en el modelo.
	if meta.Views.Kanban == nil || meta.Views.Kanban.GroupBy != "estado" {
		t.Errorf("kanban.group_by = %v, want estado", meta.Views.Kanban)
	}
}

// ── Catálogo ─────────────────────────────────────────────────────────────

// El catálogo es la base de "extender un módulo existente": sin esto, un
// diseñador visual no puede saber qué campos ya hay para no chocar nombres,
// ni si un modelo ya tiene flujo (agregar otro lo reemplazaría).
func TestBuildCatalogResumeModulosYCampos(t *testing.T) {
	manifests := map[string]*Manifest{
		"contacts": {
			Name:  "contacts",
			Label: "Contactos",
			Icon:  "👥",
			Models: map[string]*ModelDef{
				"contact": {
					Label: "Contacto",
					Fields: map[string]*FieldDef{
						"name":   {Type: "string", Required: true},
						"estado": {Type: "string", Options: []string{"prospecto", "activo"}},
					},
					Workflow: &WorkflowDef{Field: "estado", Initial: "prospecto"},
				},
			},
		},
		"crm": {
			Name: "crm", // sin label: debe humanizarse
			Models: map[string]*ModelDef{
				"lead": {Fields: map[string]*FieldDef{"title": {Type: "string"}}},
			},
		},
	}

	catalog := BuildCatalog(manifests)

	if len(catalog) != 2 {
		t.Fatalf("catalog tiene %d módulos, want 2", len(catalog))
	}
	// Orden alfabético: contacts antes que crm.
	if catalog[0].Name != "contacts" || catalog[1].Name != "crm" {
		t.Errorf("orden = [%s, %s], want [contacts, crm]", catalog[0].Name, catalog[1].Name)
	}

	contacts := catalog[0]
	if contacts.Label != "Contactos" || contacts.Icon != "👥" {
		t.Errorf("label/icon no respetados: %+v", contacts)
	}
	if len(contacts.Models) != 1 || contacts.Models[0].Name != "contact" {
		t.Fatalf("modelos = %+v", contacts.Models)
	}

	contact := contacts.Models[0]
	if !contact.HasWorkflow {
		t.Error("contact tiene workflow declarado, HasWorkflow debería ser true")
	}
	if len(contact.Fields) != 2 {
		t.Fatalf("fields = %+v, want 2", contact.Fields)
	}

	crm := catalog[1]
	if crm.Label != "Crm" { // humanizado a falta de label
		t.Errorf("label humanizada = %q, want Crm", crm.Label)
	}
	if crm.Models[0].HasWorkflow {
		t.Error("lead no declara workflow, HasWorkflow debería ser false")
	}
}

func TestBuildCatalogSinModulosDaListaVacia(t *testing.T) {
	catalog := BuildCatalog(map[string]*Manifest{})
	if catalog == nil || len(catalog) != 0 {
		t.Errorf("catalog = %v, want lista vacía (no nil)", catalog)
	}
}

// ── Parser Mermaid ───────────────────────────────────────────────────────

// El diagrama de contacts, tal como quedó en su manifest.json, debe
// parsear exactamente al mismo workflow que ya está probado en producción.
func TestParseMermaidDiagramaDeContactos(t *testing.T) {
	diagram := `
stateDiagram-v2
    [*] --> prospecto
    prospecto --> activo : Activar
    inactivo --> activo : Activar
    prospecto --> inactivo : Desactivar
    activo --> inactivo : Desactivar
`
	parsed, err := ParseMermaidStateDiagram(diagram)
	if err != nil {
		t.Fatalf("diagrama válido rechazado: %v", err)
	}

	if parsed.Initial != "prospecto" {
		t.Errorf("initial = %q, want prospecto", parsed.Initial)
	}

	wantStates := "prospecto,activo,inactivo"
	if got := strings.Join(parsed.States, ","); got != wantStates {
		t.Errorf("states = %q, want %q (orden de aparición)", got, wantStates)
	}

	activar, ok := parsed.Transitions["activar"]
	if !ok {
		t.Fatal("falta la acción 'activar'")
	}
	if activar.To != "activo" {
		t.Errorf("activar.To = %q, want activo", activar.To)
	}
	wantFrom := map[string]bool{"prospecto": true, "inactivo": true}
	if len(activar.From) != 2 {
		t.Fatalf("activar.From = %v, want 2 orígenes", activar.From)
	}
	for _, f := range activar.From {
		if !wantFrom[f] {
			t.Errorf("origen inesperado en activar.From: %q", f)
		}
	}

	desactivar, ok := parsed.Transitions["desactivar"]
	if !ok {
		t.Fatal("falta la acción 'desactivar'")
	}
	if desactivar.To != "inactivo" || len(desactivar.From) != 2 {
		t.Errorf("desactivar = %+v", desactivar)
	}
}

// Sin espacios alrededor de --> ni de los dos puntos, sigue interpretándose
// bien: por eso se cortó en dos pasos (Cut de "-->", después Cut de ":") en
// vez de una sola regex que confundiría el separador con la etiqueta.
func TestParseMermaidSinEspacios(t *testing.T) {
	diagram := "[*]-->nuevo\nnuevo-->listo:avanzar"
	parsed, err := ParseMermaidStateDiagram(diagram)
	if err != nil {
		t.Fatalf("diagrama sin espacios rechazado: %v", err)
	}
	if parsed.Initial != "nuevo" {
		t.Errorf("initial = %q, want nuevo", parsed.Initial)
	}
	if _, ok := parsed.Transitions["avanzar"]; !ok {
		t.Fatal("falta la acción 'avanzar'")
	}
}

func TestParseMermaidFinalesYComentariosSeIgnoran(t *testing.T) {
	diagram := `
%% esto es un comentario, se ignora
stateDiagram-v2
[*] --> nuevo
nuevo --> listo : avanzar
listo --> [*]
nota_cosmetica : esto no es una transición
`
	parsed, err := ParseMermaidStateDiagram(diagram)
	if err != nil {
		t.Fatalf("diagrama rechazado: %v", err)
	}
	if len(parsed.Transitions) != 1 {
		t.Errorf("transiciones = %v, want sólo 'avanzar'", parsed.Transitions)
	}
}

func TestParseMermaidSinEstadoInicialFalla(t *testing.T) {
	diagram := "nuevo --> listo : avanzar"
	if _, err := ParseMermaidStateDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: no hay [*] --> estado")
	}
}

func TestParseMermaidDosEstadosInicialesFalla(t *testing.T) {
	diagram := `
[*] --> nuevo
[*] --> otro
nuevo --> listo : avanzar
`
	if _, err := ParseMermaidStateDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: dos estados iniciales distintos")
	}
}

// El mismo estado inicial declarado dos veces no es una ambigüedad real.
func TestParseMermaidEstadoInicialRepetidoNoFalla(t *testing.T) {
	diagram := `
[*] --> nuevo
[*] --> nuevo
nuevo --> listo : avanzar
`
	parsed, err := ParseMermaidStateDiagram(diagram)
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	if parsed.Initial != "nuevo" {
		t.Errorf("initial = %q, want nuevo", parsed.Initial)
	}
}

func TestParseMermaidSinTransicionesFalla(t *testing.T) {
	diagram := "[*] --> nuevo"
	if _, err := ParseMermaidStateDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: no hay transiciones")
	}
}

func TestParseMermaidSinEtiquetaFalla(t *testing.T) {
	diagram := "[*] --> nuevo\nnuevo --> listo"
	if _, err := ParseMermaidStateDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: transición sin etiqueta no tiene nombre de acción")
	}
}

func TestParseMermaidMismaAccionDosDestinosFalla(t *testing.T) {
	diagram := `
[*] --> nuevo
nuevo --> listo : avanzar
nuevo --> cancelado : avanzar
`
	_, err := ParseMermaidStateDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba error: 'avanzar' no puede llevar a dos destinos distintos")
	}
}

// Dos etiquetas distintas que casualmente slugifican igual deben rechazarse:
// fusionarlas en silencio perdería la intención de quien dibujó el diagrama.
func TestParseMermaidEtiquetasColisionanFalla(t *testing.T) {
	diagram := `
[*] --> nuevo
nuevo --> listo : Avanzar Ya
otro --> listo : "Avanzar Ya"
`
	// Mismo destino (listo) en ambas líneas, así que el chequeo de "a dónde
	// lleva la acción" no dispara solo. "Avanzar Ya" y "\"Avanzar Ya\"" no son
	// iguales como texto, pero slugifican igual (las comillas se vuelven
	// separador) — eso es lo que debe detectarse como colisión.
	_, err := ParseMermaidStateDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba error: etiquetas distintas que producen la misma acción")
	}
	if !strings.Contains(err.Error(), "misma acción") {
		t.Errorf("el error debería ser por colisión de etiqueta, fue: %v", err)
	}
}

func TestParseMermaidEstadosCompuestosRechazados(t *testing.T) {
	diagram := `
[*] --> nuevo
state nuevo {
  [*] --> interno
}
`
	_, err := ParseMermaidStateDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba rechazo: estados compuestos no soportados")
	}
}

func TestParseMermaidStateAsRechazado(t *testing.T) {
	diagram := `
state "Nombre Bonito" as nuevo
[*] --> nuevo
nuevo --> listo : avanzar
`
	_, err := ParseMermaidStateDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba rechazo: sintaxis 'state ... as ...' no soportada")
	}
}

// El resultado del parser tiene que poder colgarse tal cual de un manifest y
// pasar la MISMA validación que ya protege a los flujos escritos a mano —
// no hay un camino separado y más laxo para lo generado.
func TestParseMermaidProduceUnWorkflowValidoParaElMotor(t *testing.T) {
	diagram := `
[*] --> prospecto
prospecto --> activo : Activar
activo --> inactivo : Desactivar
inactivo --> activo : Activar
`
	parsed, err := ParseMermaidStateDiagram(diagram)
	if err != nil {
		t.Fatalf("parser rechazó un diagrama válido: %v", err)
	}

	manifest := &Manifest{
		Name: "demo",
		Models: map[string]*ModelDef{
			"item": {
				Fields: map[string]*FieldDef{
					"estado": {Type: "string", Options: parsed.States},
				},
				Workflow: &WorkflowDef{
					Field:       "estado",
					Initial:     parsed.Initial,
					Transitions: parsed.Transitions,
				},
			},
		},
	}

	if err := validateWorkflow("item", manifest.Models["item"]); err != nil {
		t.Fatalf("el workflow generado por el parser no pasa validateWorkflow: %v", err)
	}
}

func TestSlugifyAction(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Activar", "activar"},
		{"Confirmar Pedido", "confirmar_pedido"},
		{"Enviar a Bodega", "enviar_a_bodega"},
		{"¡Aprobar!", "aprobar"},
		{"  espacios  al  borde  ", "espacios_al_borde"},
		{"2da revisión", "t_2da_revisi_n"}, // dígito inicial → prefijo; sin normalización unicode
		{"", "accion"},
		{"!!!", "accion"},
	}
	for _, tc := range tests {
		if got := slugifyAction(tc.in); got != tc.want {
			t.Errorf("slugifyAction(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// El resultado siempre debe cumplir identRe, sea cual sea la entrada.
	inputs := []string{"Activar", "2da revisión", "¡Aprobar!", "", "áéíóú", strings.Repeat("x", 200)}
	for _, in := range inputs {
		slug := slugifyAction(in)
		if !identRe.MatchString(slug) {
			t.Errorf("slugifyAction(%q) = %q no cumple identRe", in, slug)
		}
	}
}
