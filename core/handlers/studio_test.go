package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasterp/backend/sdk"
)

func fieldMap(fields ...string) map[string]*sdk.FieldDef {
	m := make(map[string]*sdk.FieldDef, len(fields))
	for i, name := range fields {
		m[name] = &sdk.FieldDef{Type: "string", Sequence: (i + 1) * 10}
	}
	return m
}

// generate() sobre un directorio vacío debe crear el módulo desde cero, con
// un manifest válido y el frontend mínimo (sólo si no existía ya).
func TestStudioGenerateCreaModuloNuevo(t *testing.T) {
	dir := t.TempDir()
	h := NewStudioHandler(dir, nil, nil)

	req := studioRequest{
		Module:     "demo",
		ModelLabel: "Item",
		Model:      "item",
		Fields:     fieldMap("nombre"),
	}
	result, err := h.generate(req)
	if err != nil {
		t.Fatalf("generate() falló: %v", err)
	}
	if result.Mode != "created" {
		t.Errorf("mode = %q, want created", result.Mode)
	}

	manifestPath := filepath.Join(dir, "demo", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("no se escribió el manifest: %v", err)
	}

	var manifest sdk.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("el manifest escrito no es JSON válido: %v", err)
	}
	if _, ok := manifest.Models["item"]; !ok {
		t.Fatal("el modelo 'item' no quedó en el manifest escrito")
	}

	// El scaffold de frontend debe existir y apuntar al modelo correcto.
	indexPath := filepath.Join(dir, "demo", "frontend", "index.html")
	html, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("no se generó el frontend: %v", err)
	}
	if !strings.Contains(string(html), `data-model="demo/item"`) {
		t.Errorf("el frontend generado no referencia demo/item: %s", html)
	}
}

// Un manifest inválido (sin campos) no debe escribir NADA — ni el manifest
// ni el frontend. La validación corre antes de tocar disco, no después.
func TestStudioGenerateManifestInvalidoNoEscribeNada(t *testing.T) {
	dir := t.TempDir()
	h := NewStudioHandler(dir, nil, nil)

	_, err := h.generate(studioRequest{Module: "demo", Model: "item"}) // sin fields
	if err == nil {
		t.Fatal("se esperaba error: el modelo no tiene campos")
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("no debería haberse creado nada en %s, hay: %v", dir, entries)
	}
}

// Extender un módulo existente agrega el modelo nuevo y CONSERVA lo que ya
// había — no es un reemplazo del archivo, es un merge.
func TestStudioGenerateExtiendeModuloExistente(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "contacts")
	os.MkdirAll(moduleDir, 0o755)

	original := `{"name":"contacts","label":"Contactos","models":{"contact":{"fields":{"name":{"type":"string","required":true}}}}}`
	manifestPath := filepath.Join(moduleDir, "manifest.json")
	os.WriteFile(manifestPath, []byte(original), 0o644)

	h := NewStudioHandler(dir, nil, nil)
	result, err := h.generate(studioRequest{
		Module: "contacts",
		Model:  "nota",
		Fields: fieldMap("texto"),
	})
	if err != nil {
		t.Fatalf("generate() falló: %v", err)
	}
	if result.Mode != "extended" {
		t.Errorf("mode = %q, want extended", result.Mode)
	}

	data, _ := os.ReadFile(manifestPath)
	var manifest sdk.Manifest
	json.Unmarshal(data, &manifest)

	if _, ok := manifest.Models["contact"]; !ok {
		t.Error("el modelo original 'contact' se perdió al extender")
	}
	if _, ok := manifest.Models["nota"]; !ok {
		t.Error("el modelo nuevo 'nota' no quedó agregado")
	}
	if manifest.Label != "Contactos" {
		t.Errorf("label original no conservada: %q", manifest.Label)
	}
}

// Extender debe dejar un respaldo del manifest anterior — es la red de
// seguridad si algo sale mal después de escribir.
func TestStudioGenerateRespaldaAlExtender(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "contacts")
	os.MkdirAll(moduleDir, 0o755)

	original := `{"name":"contacts","models":{"contact":{"fields":{"name":{"type":"string"}}}}}`
	os.WriteFile(filepath.Join(moduleDir, "manifest.json"), []byte(original), 0o644)

	h := NewStudioHandler(dir, nil, nil)
	if _, err := h.generate(studioRequest{Module: "contacts", Model: "nota", Fields: fieldMap("texto")}); err != nil {
		t.Fatalf("generate() falló: %v", err)
	}

	entries, _ := os.ReadDir(moduleDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "manifest.json.bak-") {
			found = true
		}
	}
	if !found {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("no se encontró respaldo del manifest original entre: %v", names)
	}
}

// No se puede agregar un modelo que ya existe: Studio-Flujo v1 sólo agrega
// modelos nuevos, no reescribe uno con datos reales detrás.
func TestStudioGenerateRechazaModeloQueYaExiste(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "contacts")
	os.MkdirAll(moduleDir, 0o755)

	original := `{"name":"contacts","models":{"contact":{"fields":{"name":{"type":"string"}}}}}`
	manifestPath := filepath.Join(moduleDir, "manifest.json")
	os.WriteFile(manifestPath, []byte(original), 0o644)

	h := NewStudioHandler(dir, nil, nil)
	_, err := h.generate(studioRequest{Module: "contacts", Model: "contact", Fields: fieldMap("otro")})
	if err == nil {
		t.Fatal("se esperaba rechazo: 'contact' ya existe en el módulo")
	}

	// Y el archivo original debe seguir intacto: el rechazo fue ANTES de escribir.
	data, _ := os.ReadFile(manifestPath)
	if string(data) != original {
		t.Error("el manifest original se modificó pese al rechazo")
	}
}

// Extender NO debe tocar el frontend/index.html existente: alguien pudo
// haberlo personalizado a mano.
func TestStudioGenerateNuncaPisaFrontendExistente(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "contacts")
	frontendDir := filepath.Join(moduleDir, "frontend")
	os.MkdirAll(frontendDir, 0o755)

	os.WriteFile(filepath.Join(moduleDir, "manifest.json"),
		[]byte(`{"name":"contacts","models":{"contact":{"fields":{"name":{"type":"string"}}}}}`), 0o644)

	customHTML := "<div>personalizado a mano, no tocar</div>"
	indexPath := filepath.Join(frontendDir, "index.html")
	os.WriteFile(indexPath, []byte(customHTML), 0o644)

	h := NewStudioHandler(dir, nil, nil)
	if _, err := h.generate(studioRequest{Module: "contacts", Model: "nota", Fields: fieldMap("texto")}); err != nil {
		t.Fatalf("generate() falló: %v", err)
	}

	got, _ := os.ReadFile(indexPath)
	if string(got) != customHTML {
		t.Errorf("el frontend personalizado se sobrescribió: %s", got)
	}
}

// requireAdmin: sin sessionManager, sin token, o sin is_admin, se rechaza.
func TestStudioRequireAdminSinSessionManager(t *testing.T) {
	h := NewStudioHandler(t.TempDir(), nil, nil)
	req, _ := http.NewRequest(http.MethodPost, "/api/_studio/generate", nil)
	if _, err := h.requireAdmin(req); err == nil {
		t.Fatal("sin sessionManager configurado, requireAdmin debería fallar")
	}
}
