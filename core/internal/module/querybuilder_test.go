package module

import (
	"strings"
	"testing"
)

// testModel returns a small model registration to build queries against.
func testModel() ModelRegistration {
	return ModelRegistration{
		TableName: "mod_crm_lead",
		Manifest: ModelDef{
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
	if !strings.Contains(q, "LEFT JOIN \"mod_\"" ) && !strings.Contains(q, "LEFT JOIN") {
		t.Fatalf("expected query with LEFT JOIN, got %q", q)
	}
	if !strings.Contains(q, "company_id_display") {
		t.Fatalf("expected display alias in query, got %q", q)
	}
}

func TestBuildInsertIgnoresUndeclaredFields(t *testing.T) {
	// A client posting a column that is not in the manifest must not reach SQL.
	q, args, err := NewQueryBuilder(testModel()).BuildInsert("t-1", map[string]interface{}{
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
	up, _, err := NewQueryBuilder(testModel()).BuildUpdate("id-1", "t-1", map[string]interface{}{"name": "Acme"})
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
