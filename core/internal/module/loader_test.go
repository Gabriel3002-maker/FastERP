package module

import (
	"os"
	"path/filepath"
	"testing"
)

const yamlModulo = `
name: clientes
label: Clientes
version: 1.0.0
description: Gestión de clientes
depends: [productos]

models:
  cliente:
    label: Cliente
    fields:
      - name: nombre
        type: string
        required: true
        label: Nombre
        sequence: 10
      - name: ruc
        type: string
        label: RUC
        sequence: 20
      - name: email
        type: string
        label: Correo
        sequence: 30
      - name: sector
        type: enum
        label: Sector
        options: [Retail, Industrial, Agrícola]
        sequence: 40
      - name: activo
        type: boolean
        default: true
        sequence: 50
      - name: estado
        type: enum
        label: Estado
        options: [prospecto, activo, archivado]
        sequence: 60
    workflow:
      field: estado
      initial: prospecto
      transitions:
        activar:
          from: [prospecto]
          to: activo
          label: Activar
        archivar:
          from: [activo]
          to: archivado
          label: Archivar

menus:
  clientes:
    label: Clientes
    icon: users
    seq: 20
`

func TestParseManifestYAML(t *testing.T) {
	m, err := ParseManifest([]byte(yamlModulo), "clientes/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	if m.Name != "clientes" {
		t.Errorf("Name = %q, want clientes", m.Name)
	}
	if m.Label != "Clientes" {
		t.Errorf("Label = %q", m.Label)
	}
	if len(m.Depends) != 1 || m.Depends[0] != "productos" {
		t.Errorf("Depends = %v", m.Depends)
	}

	cliente := m.Models["cliente"]
	if cliente == nil {
		t.Fatal("falta el modelo cliente")
	}
	if cliente.Name != "cliente" {
		t.Errorf("el nombre del modelo debe tomarse de la clave del mapa, es %q", cliente.Name)
	}
	if len(cliente.Fields) != 6 {
		t.Fatalf("Fields = %d, want 6", len(cliente.Fields))
	}
	if cliente.Fields[0].Name != "nombre" || !cliente.Fields[0].Required {
		t.Errorf("primer campo = %+v", cliente.Fields[0])
	}
}

func TestParseManifestConservaElOrdenDeCampos(t *testing.T) {
	m, err := ParseManifest([]byte(yamlModulo), "clientes/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	// El orden importa: son las columnas de la tabla y el orden del formulario.
	// Un mapa de Go no lo tiene, y por eso se recupera del texto.
	want := []string{"nombre", "ruc", "email", "sector", "activo", "estado"}
	got := m.FieldOrder("cliente")
	if len(got) != len(want) {
		t.Fatalf("FieldOrder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FieldOrder = %v, want %v", got, want)
		}
	}
}

func TestParseManifestWorkflow(t *testing.T) {
	m, err := ParseManifest([]byte(yamlModulo), "clientes/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	cliente := m.Models["cliente"]

	if cliente.Workflow == nil {
		t.Fatal("falta el workflow")
	}
	if cliente.Workflow.Field != "estado" {
		t.Errorf("campo del workflow = %q", cliente.Workflow.Field)
	}
	if tr := cliente.Workflow.Transitions["activar"]; tr == nil || tr.To != "activo" {
		t.Errorf("transición activar = %+v", tr)
	}
}

func TestParseManifestRechazaWorkflowCoherente(t *testing.T) {
	casos := []struct{ nombre, mod string }{
		{
			"el campo del workflow no existe",
			`name: x
label: X
models:
  a:
    fields:
      - {name: f, type: string}
    workflow:
      field: no_existe
      transitions:
        t: {from: [a], to: b}
`,
		},
		{
			"la transición lleva a un estado no declarado",
			`name: x
label: X
models:
  a:
    fields:
      - {name: e, type: enum, options: [a, b]}
    workflow:
      field: e
      initial: a
      transitions:
        t: {from: [a], to: fantasma}
`,
		},
		{
			"el campo del workflow no tiene options",
			`name: x
label: X
models:
  a:
    fields:
      - {name: e, type: string}
    workflow:
      field: e
      transitions:
        t: {from: [a], to: b}
`,
		},
	}

	for _, c := range casos {
		if _, err := ParseManifest([]byte(c.mod), "x/module.yaml"); err == nil {
			t.Errorf("%s: esperaba error y no lo hubo", c.nombre)
		}
	}
}

func TestParseManifestRechazaTenantID(t *testing.T) {
	// El core pone tenant_id. Declararlo en el módulo rompería el aislamiento.
	mod := `name: x
label: X
models:
  a:
    fields:
      - {name: f, type: string}
      - {name: tenant_id, type: uuid}
`
	if _, err := ParseManifest([]byte(mod), "x/module.yaml"); err == nil {
		t.Fatal("un módulo que declara tenant_id debe rechazarse")
	}
}

func TestParseManifestAceptaJSON(t *testing.T) {
	// Los módulos antiguos son manifest.json: tienen que seguir cargando.
	jsonMod := `{
  "name": "viejo",
  "label": "Viejo",
  "version": "1.0.0",
  "models": {
    "cosa": {
      "label": "Cosa",
      "fields": {
        "nombre": {"type": "string", "required": true, "sequence": 10},
        "notas":  {"type": "text", "sequence": 20}
      }
    }
  }
}`
	m, err := ParseManifest([]byte(jsonMod), "viejo/manifest.json")
	if err != nil {
		t.Fatalf("ParseManifest JSON: %v", err)
	}
	if m.Name != "viejo" {
		t.Errorf("Name = %q", m.Name)
	}
	cosa := m.Models["cosa"]
	if cosa == nil || len(cosa.Fields) != 2 {
		t.Fatalf("modelo cosa = %+v", cosa)
	}
	if !cosa.Fields[0].Required {
		t.Error("nombre debería ser required")
	}
}

func TestFindManifest(t *testing.T) {
	dir := t.TempDir()

	// El nombre canónico gana.
	os.MkdirAll(filepath.Join(dir, "a"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "module.yaml"), []byte(yamlModulo), 0644)
	if got := FindManifest(dir, "a"); filepath.Base(got) != "module.yaml" {
		t.Errorf("FindManifest = %q", got)
	}

	// Y un manifest.json antiguo también se encuentra.
	os.MkdirAll(filepath.Join(dir, "b"), 0755)
	os.WriteFile(filepath.Join(dir, "b", "manifest.json"), []byte(`{"name":"b","label":"B","models":{}}`), 0644)
	if got := FindManifest(dir, "b"); filepath.Base(got) != "manifest.json" {
		t.Errorf("FindManifest(b) = %q", got)
	}

	if got := FindManifest(dir, "no_existe"); got != "" {
		t.Errorf("un módulo inexistente debería devolver \"\", dio %q", got)
	}
}

func TestManifestTableName(t *testing.T) {
	m := &Manifest{Name: "clientes"}
	if got := m.TableName("cliente"); got != "mod_clientes_cliente" {
		t.Errorf("TableName = %q", got)
	}
}
