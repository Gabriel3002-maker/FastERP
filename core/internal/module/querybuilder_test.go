package module

import (
	"strings"
	"testing"
)

// testModel returns a small model registration to build queries against.
func testModel() ModelRegistration {
	return ModelRegistration{
		TableName: "mod_crm_lead",
		Manifest: &ModelDef{
			Name:  "lead",
			Label: "Lead",
			Fields: []FieldDef{
				{Name: "name", Type: "string", Required: true},
				{Name: "amount", Type: "float"},
				{Name: "won", Type: "bool"},
			},
		},
	}
}

func TestBuildSelectDefaultColumns(t *testing.T) {
	_, q, args, err := NewQueryBuilder(testModel()).BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want none", args)
	}
	for _, col := range []string{`base.id`, `base."name"`, `base."amount"`, `base."won"`, `base.created_at`, `base.updated_at`} {
		if !strings.Contains(q, col) {
			t.Errorf("query %q missing column %s", q, col)
		}
	}
	if !strings.Contains(q, `FROM "mod_crm_lead"`) {
		t.Errorf("query %q does not select from the model table", q)
	}
}

func TestBuildSelectParameterizesWhereValues(t *testing.T) {
	qb := NewQueryBuilder(testModel())
	qb.Where("tenant_id", "=", "t-1").Where("name", "=", "'; DROP TABLE users; --")

	_, q, args, err := qb.BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	// The injection payload must travel as an argument, never inlined into SQL.
	if strings.Contains(q, "DROP TABLE") {
		t.Fatalf("value was interpolated into SQL: %q", q)
	}
	if len(args) != 2 || args[1] != "'; DROP TABLE users; --" {
		t.Errorf("args = %v, want the raw value passed through as $2", args)
	}
	if !strings.Contains(q, "$1") || !strings.Contains(q, "$2") {
		t.Errorf("query %q does not use positional placeholders", q)
	}
}

func TestBuildSelectRejectsUnknownIdentifiers(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*QueryBuilder)
	}{
		{"unknown where column", func(qb *QueryBuilder) { qb.Where("password_hash", "=", "x") }},
		{"unknown select column", func(qb *QueryBuilder) { qb.Select("secret") }},
		{"unknown order column", func(qb *QueryBuilder) { qb.OrderBy("secret", "ASC") }},
		{"injected order column", func(qb *QueryBuilder) { qb.OrderBy("name; DROP TABLE users", "ASC") }},
		{"invalid operator", func(qb *QueryBuilder) { qb.Where("name", "=1 OR 1", "x") }},
		{"invalid direction", func(qb *QueryBuilder) { qb.OrderBy("name", "ASC; DROP TABLE users") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qb := NewQueryBuilder(testModel())
			tc.mut(qb)
			if _, _, _, err := qb.BuildSelect(); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestBuildSelectRejectsUnsafeTableName(t *testing.T) {
	m := testModel()
	m.TableName = `users"; DROP TABLE users; --`
	if _, _, _, err := NewQueryBuilder(m).BuildSelect(); err == nil {
		t.Fatal("expected an error for an unsafe table name, got nil")
	}
}

func TestBuildSelectMany2OneJoins(t *testing.T) {
	m := testModel()
	m.Manifest.Fields = append(m.Manifest.Fields, FieldDef{Name: "company_id", Type: "many2one", RelatedModel: "company", RelatedField: "name"})
	_, q, _, err := NewQueryBuilder(m).BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	if !strings.Contains(q, "LEFT JOIN \"mod_\"") && !strings.Contains(q, "LEFT JOIN") {
		t.Fatalf("expected query with LEFT JOIN, got %q", q)
	}
	if !strings.Contains(q, "company_id_display") {
		t.Fatalf("expected display alias in query, got %q", q)
	}
}

func TestBuildInsertIgnoresUndeclaredFields(t *testing.T) {
	// A client posting a column that is not in the manifest must not reach SQL.
	q, args, err := NewQueryBuilder(testModel()).BuildInsert("t-1", "u-1", map[string]interface{}{
		"name":     "Acme",
		"is_admin": true,
	})
	if err != nil {
		t.Fatalf("BuildInsert: %v", err)
	}
	if strings.Contains(q, "is_admin") {
		t.Fatalf("undeclared field leaked into query: %q", q)
	}
	for _, a := range args {
		if a == true {
			t.Fatalf("undeclared field value leaked into args: %v", args)
		}
	}
}

func TestBuildUpdateAndDeleteScopeByTenant(t *testing.T) {
	up, _, err := NewQueryBuilder(testModel()).BuildUpdate("id-1", "t-1", "u-1", map[string]interface{}{"name": "Acme"})
	if err != nil {
		t.Fatalf("BuildUpdate: %v", err)
	}
	if !strings.Contains(up, "tenant_id") {
		t.Errorf("UPDATE is not tenant-scoped: %q", up)
	}

	del, _, err := NewQueryBuilder(testModel()).BuildDelete()
	if err != nil {
		t.Fatalf("BuildDelete: %v", err)
	}
	if !strings.Contains(del, "tenant_id") {
		t.Errorf("DELETE is not tenant-scoped: %q", del)
	}
}

func TestSafeIdent(t *testing.T) {
	valid := []string{"name", "tenant_id", "mod_crm_lead", "a1"}
	invalid := []string{"", "Name", "1name", "na me", "name;", `na"me`, "_name", strings.Repeat("a", 64)}

	for _, s := range valid {
		if !safeIdent(s) {
			t.Errorf("safeIdent(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if safeIdent(s) {
			t.Errorf("safeIdent(%q) = true, want false", s)
		}
	}
}

// El conteo tiene que llevar los mismos filtros que el listado. Si no, el
// paginador promete páginas que la consulta de datos no devuelve.
func TestBuildCountSharesWhereWithSelect(t *testing.T) {
	qb := NewQueryBuilder(testModel())
	qb.Where("tenant_id", "=", "t-1").Where("won", "=", true)

	cq, cargs, err := qb.BuildCount()
	if err != nil {
		t.Fatalf("BuildCount: %v", err)
	}
	if !strings.Contains(cq, `FROM "mod_crm_lead"`) {
		t.Errorf("count query %q does not count the model table", cq)
	}
	if !strings.Contains(cq, "WHERE") || !strings.Contains(cq, `"tenant_id" = $1`) {
		t.Errorf("count query %q lost the filters", cq)
	}
	if len(cargs) != 2 {
		t.Errorf("count args = %v, want 2", cargs)
	}

	// El LIMIT del listado no puede contaminar el conteo.
	qb.Limit(25).Offset(50)
	_, cargs2, err := qb.BuildCount()
	if err != nil {
		t.Fatalf("BuildCount after limit: %v", err)
	}
	if len(cargs2) != 2 {
		t.Errorf("count picked up pagination args: %v", cargs2)
	}
	if !strings.Contains(cq, "COUNT(*)") {
		t.Errorf("query %q is not a count", cq)
	}
}

// WhereRaw y OrWhereRaw existen para condiciones que el builder no sabe montar
// sola, como el grupo con paréntesis de la búsqueda. Los paréntesis los pone
// quien llama, porque el builder no sabe dónde empieza el grupo.
func TestWhereRawGroupKeepsTenantScoped(t *testing.T) {
	qb := NewQueryBuilder(testModel())
	// Los ? los numera el builder: quien llama no sabe en qué posición va su
	// valor, porque depende de cuántas cláusulas haya antes.
	qb.Where("tenant_id", "=", "t-1").
		WhereRaw(`("name" ILIKE ? OR "amount" ILIKE ? ESCAPE '\')`, "%a%", "%b%")

	_, q, args, err := qb.BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	// El AND del tenant tiene que quedar FUERA del grupo, o la búsqueda
	// dejaría leer los registros de otro tenant.
	want := `WHERE "tenant_id" = $1 AND ("name" ILIKE $2 OR "amount" ILIKE $3 ESCAPE '\')`
	if !strings.Contains(q, want) {
		t.Errorf("query %q\ndoes not contain\n%q", q, want)
	}
	if len(args) != 3 || args[0] != "t-1" || args[1] != "%a%" || args[2] != "%b%" {
		t.Errorf("args = %v, want the values in order", args)
	}
}

// El patrón de búsqueda va como parámetro. Con Sprintf, un % escrito por quien
// busca rompería la sentencia.
func TestWhereRawParameterizesPattern(t *testing.T) {
	qb := NewQueryBuilder(testModel())
	qb.Where("tenant_id", "=", "t-1").
		WhereRaw("(\"name\" ILIKE ? ESCAPE '\\')", "%100%25%")

	_, q, args, err := qb.BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	if !strings.Contains(q, `"name" ILIKE $2 ESCAPE '\'`) {
		t.Errorf("query %q did not parameterize the pattern", q)
	}
	if args[1] != "%100%25%" {
		t.Errorf("pattern = %v, want it untouched", args[1])
	}
}

// Un "?" dentro de un literal es un carácter, no un marcador. Si se contara,
// la numeración de los parámetros se correría y cada valor acabaría en la
// columna equivocada.
func TestExpandRawIgnoresQuestionMarkInsideLiteral(t *testing.T) {
	qb := NewQueryBuilder(testModel())
	qb.Where("name", "=", "x").
		WhereRaw(`("name" = '¿?' OR "name" ILIKE ? ESCAPE '\')`, "%y%")

	_, q, args, err := qb.BuildSelect()
	if err != nil {
		t.Fatalf("BuildSelect: %v", err)
	}
	if !strings.Contains(q, `'¿?'`) {
		t.Errorf("query %q mangled the literal", q)
	}
	if !strings.Contains(q, `ILIKE $2`) {
		t.Errorf("query %q misnumbered the marker after the literal", q)
	}
	if len(args) != 2 || args[1] != "%y%" {
		t.Errorf("args = %v, want 2 values in order", args)
	}
}

// Más valores que marcadores es un error de escritura, no algo que se ignore:
// si no, la consulta que se manda no es la que el código cree.
func TestExpandRawRejectsExtraValues(t *testing.T) {
	qb := NewQueryBuilder(testModel())
	qb.WhereRaw("(\"name\" ILIKE ?)", "%a%", "sobrante")

	if _, _, _, err := qb.BuildSelect(); err == nil {
		t.Fatal("expected an error for an unused parameter")
	}
}

// EXISTS no está en la lista blanca: permite tocar cosas que no son columnas del
// modelo.
func TestIsAllowedOpRejectsExists(t *testing.T) {
	for _, op := range []string{"EXISTS", "exists"} {
		if IsAllowedOp(op) {
			t.Errorf("IsAllowedOp(%q) = true, want false", op)
		}
	}
	for _, op := range []string{"=", "!=", "LIKE", "ILIKE", "IN", "IS NULL"} {
		if !IsAllowedOp(op) {
			t.Errorf("IsAllowedOp(%q) = false, want true", op)
		}
	}
}

// Un id, un created_at o un tenant_id son columnas de toda tabla. Sin
// contarlas, ?order=created_at sería un error de dedo del cliente.
func TestHasFieldCountsCoreColumns(t *testing.T) {
	reg := testModel()
	qb := NewQueryBuilder(reg)
	for _, col := range []string{"id", "tenant_id", "created_at", "updated_at", "name"} {
		if !qb.HasField(col) {
			t.Errorf("HasField(%q) = false, want true", col)
		}
		if !reg.HasField(col) {
			t.Errorf("ModelRegistration.HasField(%q) = false, want true", col)
		}
	}
	if qb.HasField("nope") {
		t.Error("HasField(nope) = true, want false")
	}
}

// Un filtro llega como texto; la columna es integer. Sin convertir, lib/pq
// compararía un INTEGER con una cadena y fallaría por tipo.
func TestCastValueUsesDeclaredType(t *testing.T) {
	reg := testModel()
	cases := []struct {
		field, in string
		want      any
	}{
		{"name", "acme", "acme"},
		{"amount", "12.50", float64(12.5)},
		{"won", "true", true},
		{"won", "false", false},
		{"id", "abc", "abc"},
		{"created_at", "2026-01-01", "2026-01-01"},
		// Un texto que no se puede convertir se queda como texto: el error lo
		// da la columna, con el valor exacto, y el mensaje es más útil.
		{"amount", "no-es-un-número", "no-es-un-número"},
	}
	for _, tc := range cases {
		if got := reg.CastValue(tc.field, tc.in); got != tc.want {
			t.Errorf("CastValue(%q, %q) = %#v, want %#v", tc.field, tc.in, got, tc.want)
		}
	}
}
