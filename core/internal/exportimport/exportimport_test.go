package exportimport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"testing"

	"github.com/fasterp/backend/internal/module"
)

type mockDB struct{}

func (m *mockDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return mockResult{}, nil
}

type mockResult struct{}

func (m mockResult) LastInsertId() (int64, error) { return 1, nil }
func (m mockResult) RowsAffected() (int64, error) { return 1, nil }

func setupTestModel() module.ModelRegistration {
	manifest := &module.Manifest{
		Name:  "contacts",
		Label: "Contactos",
		Models: map[string]*module.ModelDef{
			"contact": {
				Name:  "contact",
				Label: "Contacto",
				Fields: []module.FieldDef{
					{Name: "nombre", Label: "Nombre", Type: "string", Required: true},
					{Name: "tipo_persona", Label: "Tipo de Persona", Type: "enum", Options: []string{"NATURAL", "JURIDICA"}},
					{Name: "email", Label: "Correo Electrónico", Type: "string"},
					{Name: "activo", Label: "Activo", Type: "boolean"},
				},
			},
		},
	}
	_ = manifest.Validate()

	return module.ModelRegistration{
		Manifest:  manifest.Models["contact"],
		TableName: "mod_contacts_contact",
		Columns:   []string{"nombre", "tipo_persona", "email", "activo"},
	}
}

func TestExportCSV(t *testing.T) {
	reg := setupTestModel()
	records := []map[string]any{
		{
			"id":           "123",
			"nombre":       "Empresa SA",
			"tipo_persona": "JURIDICA",
			"email":        "contacto@empresa.com",
			"activo":       true,
		},
	}

	data, mime, filename, err := ExportData(reg, records, ExportOptions{
		Format:    FormatCSV,
		UseLabels: true,
	})
	if err != nil {
		t.Fatalf("ExportData CSV failed: %v", err)
	}

	if mime != "text/csv; charset=utf-8" {
		t.Errorf("expected CSV mime, got %s", mime)
	}
	if filename == "" {
		t.Errorf("expected non-empty filename")
	}

	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("reading exported CSV failed: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (header + 1 record), got %d", len(rows))
	}

	if rows[1][1] != "Empresa SA" {
		t.Errorf("expected 'Empresa SA', got '%s'", rows[1][1])
	}
}

func TestExportXLSX(t *testing.T) {
	reg := setupTestModel()
	records := []map[string]any{
		{
			"id":           "123",
			"nombre":       "Juan Pérez",
			"tipo_persona": "NATURAL",
			"email":        "juan@test.com",
			"activo":       true,
		},
	}

	data, mime, filename, err := ExportData(reg, records, ExportOptions{
		Format:    FormatXLSX,
		UseLabels: true,
	})
	if err != nil {
		t.Fatalf("ExportData XLSX failed: %v", err)
	}

	if mime != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("expected XLSX mime, got %s", mime)
	}
	if filename == "" {
		t.Errorf("expected non-empty filename")
	}
	if len(data) == 0 {
		t.Errorf("expected non-empty XLSX data")
	}
}

func TestImportCSVValidation(t *testing.T) {
	reg := setupTestModel()

	csvContent := []byte("Nombre,Tipo de Persona,Correo Electrónico,Activo\n" +
		"Carlos Lopez,NATURAL,carlos@test.com,Sí\n" +
		",JURIDICA,invalid@test.com,No\n" + // Nombre requerido vacío
		"Ana Gomez,INVALID_PERSONA,ana@test.com,Sí\n") // Enum inválido

	ctx := context.Background()
	res, err := ImportData(ctx, &mockDB{}, reg, "tenant-123", csvContent, ImportOptions{
		Format: FormatCSV,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("ImportData failed: %v", err)
	}

	if res.Success {
		t.Errorf("expected import to fail validation errors, but got success")
	}

	if len(res.Errors) != 2 {
		t.Fatalf("expected 2 validation errors, got %d: %#v", len(res.Errors), res.Errors)
	}
}

// Una cabecera que no corresponde a ningún campo se descarta al guardar. Si no
// se avisa, el archivo "valida" completo y produce filas vacías.
func TestImportReportsUnmatchedColumns(t *testing.T) {
	reg := setupTestModel()

	csvContent := []byte("Nombre,Correo Electrónico,RUC,Cellular\n" +
		"Carlos Lopez,carlos@test.com,1791234567001,0999999999\n")

	res, err := ImportData(context.Background(), &mockDB{}, reg, "tenant-123", csvContent, ImportOptions{
		Format: FormatCSV,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("ImportData failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected success, got errors: %#v", res.Errors)
	}

	if len(res.UnmatchedColumns) != 2 {
		t.Fatalf("expected 2 unmatched columns, got %#v", res.UnmatchedColumns)
	}
	if res.UnmatchedColumns[0] != "RUC" || res.UnmatchedColumns[1] != "Cellular" {
		t.Errorf("unexpected unmatched columns: %#v", res.UnmatchedColumns)
	}
}

// La plantilla exporta la columna ID, que es autoincremental: no debe warned.
func TestImportIgnoresIDColumnAsUnmatched(t *testing.T) {
	reg := setupTestModel()

	csvContent := []byte("ID,Nombre,Correo Electrónico\n" +
		"123,Carlos Lopez,carlos@test.com\n")

	res, err := ImportData(context.Background(), &mockDB{}, reg, "tenant-123", csvContent, ImportOptions{
		Format: FormatCSV,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("ImportData failed: %v", err)
	}

	if len(res.UnmatchedColumns) != 0 {
		t.Errorf("expected no unmatched columns, got %#v", res.UnmatchedColumns)
	}
}

// La plantilla de ejemplo debe salir sin los datos del tenant: si no, al
// reimportarla se duplica todo.
func TestExportCSVHeadersOnly(t *testing.T) {
	reg := setupTestModel()
	records := []map[string]any{
		{"id": "1", "nombre": "Empresa SA", "email": "a@empresa.com"},
	}

	b, _, _, err := ExportData(reg, records, ExportOptions{
		Format:      FormatCSV,
		UseLabels:   true,
		HeadersOnly: true,
	})
	if err != nil {
		t.Fatalf("ExportData failed: %v", err)
	}

	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatalf("no se pudo leer el CSV: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("expected only the header row, got %d rows: %q", len(rows), rows)
	}
	if rows[0][1] != "Nombre" {
		t.Errorf("expected header 'Nombre', got %q", rows[0][1])
	}
}

func TestImportCSVSuccess(t *testing.T) {
	reg := setupTestModel()

	csvContent := []byte("Nombre,Tipo de Persona,Correo Electrónico,Activo\n" +
		"Carlos Lopez,NATURAL,carlos@test.com,Sí\n" +
		"EcuaByte SA,JURIDICA,info@ecuabyte.com,No\n")

	ctx := context.Background()
	res, err := ImportData(ctx, &mockDB{}, reg, "tenant-123", csvContent, ImportOptions{
		Format: FormatCSV,
		DryRun: false,
	})
	if err != nil {
		t.Fatalf("ImportData failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected import success, got errors: %#v", res.Errors)
	}

	if res.CreatedCount != 2 {
		t.Errorf("expected 2 created records, got %d", res.CreatedCount)
	}
}
