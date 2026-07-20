package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fasterp/backend/sdk"
)

// StudioHandler es Studio-Flujo: la pieza que convierte un diagrama en un
// módulo instalable.
//
// Dos niveles de riesgo conviven acá:
//   - ParseMermaid y Validate son cómputo puro — no tocan disco, no
//     requieren admin.
//   - Generate SÍ escribe en modules/, así que exige admin y sigue una regla
//     fija: nunca se edita un manifest.json en el lugar. Se arma la versión
//     nueva completa, se valida con el mismo LoadManifest() que protege
//     cualquier manifest escrito a mano, se respalda el original si existía,
//     y sólo entonces se reemplaza con un rename atómico. Si la validación
//     falla, no se toca un solo byte en disco.
type StudioHandler struct {
	modulesDir     string
	sessionManager *SessionManager
	crud           *GenericCRUDHandler // para invalidar su caché tras escribir
}

func NewStudioHandler(modulesDir string, sessionManager *SessionManager, crud *GenericCRUDHandler) *StudioHandler {
	return &StudioHandler{modulesDir: modulesDir, sessionManager: sessionManager, crud: crud}
}

// ParseMermaid interpreta un diagrama de estados y devuelve el workflow que
// el motor entendería — o el error exacto de qué está mal, en los mismos
// términos con que la persona lo dibujó.
func (h *StudioHandler) ParseMermaid(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	var payload struct {
		Diagram string `json:"diagram"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if strings.TrimSpace(payload.Diagram) == "" {
		writeErr(w, http.StatusBadRequest, "falta \"diagram\"")
		return
	}

	parsed, err := sdk.ParseMermaidStateDiagram(payload.Diagram)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parsed)
}

// ParseBPMN interpreta un diagrama BPMN 2.0 (el XML que exportan bpmn.io,
// Camunda Modeler, etc.) y devuelve el mismo workflow que ParseMermaid — sólo
// cambia de dónde viene el dibujo.
func (h *StudioHandler) ParseBPMN(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	var payload struct {
		Diagram string `json:"diagram"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if strings.TrimSpace(payload.Diagram) == "" {
		writeErr(w, http.StatusBadRequest, "falta \"diagram\"")
		return
	}

	parsed, err := sdk.ParseBPMNDiagram(payload.Diagram)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parsed)
}

// studioRequest es lo que el lienzo manda: un modelo (nuevo o agregado a un
// módulo existente) con sus campos y, opcionalmente, su flujo.
//
// El orden de presentación de los campos generados depende de "sequence" en
// cada FieldDef, no del orden de las claves del mapa — un map de Go no tiene
// orden, y json.Marshal escribe sus claves alfabéticamente. Studio-Flujo
// asigna sequence (10, 20, 30…) según el orden en que la persona armó los
// campos en el lienzo; es la misma convención que ya usa contacts.
type studioRequest struct {
	Module      string                   `json:"module"`
	ModuleLabel string                   `json:"module_label,omitempty"`
	ModuleIcon  string                   `json:"module_icon,omitempty"`
	Model       string                   `json:"model"`
	ModelLabel  string                   `json:"model_label,omitempty"`
	Fields      map[string]*sdk.FieldDef `json:"fields"`
	Workflow    *sdk.WorkflowDef         `json:"workflow,omitempty"`
}

// Validate arma el manifest resultante y lo pasa por LoadManifest() sin
// escribir nada — para que el lienzo pueda avisar "esto no es válido" a
// medida que la persona lo arma, antes de llegar al botón de generar.
func (h *StudioHandler) Validate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	var req studioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	manifest, _, _, _, err := h.assembleManifest(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}

	if err := probeManifest(req.Module, manifest); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

// generateResult resume lo que Generate acabó de escribir.
type generateResult struct {
	Module string `json:"module"`
	Model  string `json:"model"`
	Mode   string `json:"mode"` // "created" | "extended"
	Path   string `json:"path"`
}

// Generate escribe el módulo a disco. Sólo admin: es la única operación de
// Studio-Flujo con efectos permanentes.
func (h *StudioHandler) Generate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	claims, err := h.requireAdmin(r)
	if err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	var req studioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	result, err := h.generate(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[Studio] %s %s el módulo %s (modelo %s)",
		claims.Username, result.Mode, result.Module, result.Model)
	writeJSON(w, http.StatusOK, result)
}

// assembleManifest arma el manifest COMPLETO que resultaría del pedido: si el
// módulo ya existe, parte de su contenido real y le agrega el modelo nuevo;
// si no existe, arma uno desde cero. No escribe nada — es el mismo paso que
// usan tanto Validate() como generate(), así no hay dos caminos que puedan
// desincronizarse.
func (h *StudioHandler) assembleManifest(req studioRequest) (manifest *sdk.Manifest, path string, existing []byte, extending bool, err error) {
	moduleName := strings.TrimSpace(req.Module)
	modelName := strings.TrimSpace(req.Model)
	if moduleName == "" || modelName == "" {
		return nil, "", nil, false, fmt.Errorf("falta el nombre del módulo o del modelo")
	}
	if len(req.Fields) == 0 {
		return nil, "", nil, false, fmt.Errorf("el modelo necesita al menos un campo")
	}

	// filepath.Base es defensa en profundidad: identRe (aplicado más abajo vía
	// LoadManifest) ya bloquearía un nombre con "..", pero esto asegura que la
	// ruta ni siquiera se construye con algo que pudiera escapar del directorio.
	dir := filepath.Join(h.modulesDir, filepath.Base(moduleName))
	manifestPath := filepath.Join(dir, "manifest.json")

	raw, readErr := os.ReadFile(manifestPath)
	extending = readErr == nil

	m := &sdk.Manifest{Name: moduleName, Models: map[string]*sdk.ModelDef{}}
	if extending {
		if err := json.Unmarshal(raw, m); err != nil {
			return nil, "", nil, false, fmt.Errorf("el manifest existente de %q no se pudo leer: %w", moduleName, err)
		}
		if m.Models == nil {
			m.Models = map[string]*sdk.ModelDef{}
		}
		if _, clash := m.Models[modelName]; clash {
			return nil, "", nil, false, fmt.Errorf(
				"%q ya tiene un modelo %q — por ahora Studio-Flujo sólo agrega modelos nuevos, no edita uno existente",
				moduleName, modelName)
		}
	}

	if req.ModuleLabel != "" {
		m.Label = req.ModuleLabel
	}
	if req.ModuleIcon != "" {
		m.Icon = req.ModuleIcon
	}

	m.Models[modelName] = &sdk.ModelDef{
		Label:    req.ModelLabel,
		Fields:   req.Fields,
		Workflow: req.Workflow,
	}

	return m, manifestPath, raw, extending, nil
}

// probeManifest corre el manifest propuesto por el MISMO validador que
// protege cualquier módulo escrito a mano — no hay una versión más laxa para
// lo generado.
func probeManifest(moduleName string, manifest *sdk.Manifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("no se pudo serializar el manifest: %w", err)
	}
	probe := sdk.NewModuleSDK(moduleName, "", "", nil) // sólo valida: no toca la BD
	if err := probe.LoadManifest(string(data)); err != nil {
		return err
	}
	return nil
}

// generate valida y, sólo si pasa, escribe: respalda el original si existía,
// arma el contenido nuevo aparte, y lo reemplaza con un rename atómico — así
// nunca queda un manifest.json a medio escribir si el proceso se interrumpe
// a mitad de camino.
func (h *StudioHandler) generate(req studioRequest) (*generateResult, error) {
	manifest, manifestPath, existing, extending, err := h.assembleManifest(req)
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("no se pudo serializar el manifest: %w", err)
	}
	if err := probeManifest(req.Module, manifest); err != nil {
		return nil, fmt.Errorf("el módulo generado no es válido: %w", err)
	}

	dir := filepath.Dir(manifestPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("no se pudo crear el directorio del módulo: %w", err)
	}

	if extending {
		backup := manifestPath + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, existing, 0o644); err != nil {
			return nil, fmt.Errorf("no se pudo respaldar el manifest existente: %w", err)
		}
	}

	tmp := manifestPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, fmt.Errorf("no se pudo escribir el manifest: %w", err)
	}
	if err := os.Rename(tmp, manifestPath); err != nil {
		return nil, fmt.Errorf("no se pudo reemplazar el manifest: %w", err)
	}

	// A partir de acá el archivo ya cambió: cualquier copia en memoria del
	// dispatcher queda obsoleta. Se invalida antes de que llegue la primera
	// petición al modelo recién agregado.
	if h.crud != nil {
		h.crud.InvalidateModule(req.Module)
	}

	if !extending {
		label := req.ModuleLabel
		if label == "" {
			label = req.Module
		}
		if err := scaffoldFrontend(dir, req.Module, req.Model, label); err != nil {
			// El manifest ya quedó escrito y es válido: el módulo funciona vía
			// API aunque falte la página. Se avisa en el log, no se revierte.
			log.Printf("[Studio] aviso: manifest de %q generado pero el frontend no: %v", req.Module, err)
		}
	}

	mode := "created"
	if extending {
		mode = "extended"
	}
	return &generateResult{Module: req.Module, Model: req.Model, Mode: mode, Path: manifestPath}, nil
}

// scaffoldFrontend crea la página mínima de un módulo nuevo: un contenedor
// para el motor de vistas del core, nada más. Nunca se llama si el módulo ya
// existía — jamás pisa un frontend/index.html que alguien pudo haber tocado
// a mano.
func scaffoldFrontend(dir, module, model, label string) error {
	frontendDir := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(frontendDir, 0o755); err != nil {
		return err
	}

	indexPath := filepath.Join(frontendDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		return nil
	}

	html := fmt.Sprintf(`<div class="%s-module">
    <div class="module-header">
        <h1>%s</h1>
    </div>
    <div data-fast-view data-model="%s/%s"></div>
</div>
`, module, label, module, model)

	return os.WriteFile(indexPath, []byte(html), 0o644)
}

// requireAdmin valida el token de sesión y exige is_admin. Generate() es la
// única operación de Studio-Flujo con efectos permanentes en disco — las de
// sólo lectura (ParseMermaid, Validate) no lo exigen.
func (h *StudioHandler) requireAdmin(r *http.Request) (*Claims, error) {
	if h.sessionManager == nil {
		return nil, fmt.Errorf("sesión no configurada")
	}
	token := h.sessionManager.GetTokenFromRequest(r)
	if token == "" {
		return nil, fmt.Errorf("se requiere iniciar sesión")
	}
	claims, err := h.sessionManager.ValidateToken(token)
	if err != nil {
		return nil, fmt.Errorf("sesión inválida o vencida")
	}
	if !claims.IsAdmin {
		return nil, fmt.Errorf("se requieren permisos de administrador")
	}
	return claims, nil
}
