package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fasterp/backend/db"
	"github.com/fasterp/backend/sdk"
)

// GenericCRUDHandler resuelve /api/{module}/{model}[/{id}] para CUALQUIER módulo.
//
// No conoce ningún módulo en particular: lee su manifest.json, arma el SDK y
// delega en @fast. Agregar un módulo nuevo no requiere tocar el core.
type GenericCRUDHandler struct {
	dbConn         *db.DB
	modulesDir     string
	sessionManager *SessionManager

	mu            sync.RWMutex
	manifestCache map[string]string // módulo → manifest.json
	schemaEnsured map[string]bool   // módulo → tablas ya materializadas
	tenantCache   map[string]string // slug → uuid
}

// metaSegment es el pseudo-id que devuelve la metadata del modelo.
const metaSegment = "_meta"

func NewGenericCRUDHandler(dbConn *db.DB, modulesDir string, sessionManager *SessionManager) *GenericCRUDHandler {
	return &GenericCRUDHandler{
		dbConn:         dbConn,
		modulesDir:     modulesDir,
		sessionManager: sessionManager,
		manifestCache:  make(map[string]string),
		schemaEnsured:  make(map[string]bool),
		tenantCache:    make(map[string]string),
	}
}

// userIDFrom identifica a quien hace la petición, si trae un token válido.
//
// Es identificación "a lo mejor", no autorización: estas rutas no exigen
// login (X-Tenant-ID alcanza para operar), pero el historial de un flujo
// necesita saber QUIÉN hizo cada transición para que sea auditoría real y no
// una lista de movimientos anónimos. Sin token o con uno inválido, se sigue
// atendiendo la petición — sólo que el historial queda sin ese dato.
func (h *GenericCRUDHandler) userIDFrom(r *http.Request) string {
	if h.sessionManager == nil {
		return ""
	}
	token := h.sessionManager.GetTokenFromRequest(r)
	if token == "" {
		return ""
	}
	claims, err := h.sessionManager.ValidateToken(token)
	if err != nil {
		return ""
	}
	return claims.UserID
}

// HandleCRUD despacha POST, GET, PUT y DELETE según el manifest del módulo.
func (h *GenericCRUDHandler) HandleCRUD(w http.ResponseWriter, r *http.Request) {
	module := r.PathValue("module")
	model := r.PathValue("model")
	id := r.PathValue("id") // vacío en las rutas de colección

	if module == "" || model == "" {
		writeErr(w, http.StatusBadRequest, "ruta inválida: se espera /api/{module}/{model}")
		return
	}

	ctx := r.Context()

	tenantID, err := h.resolveTenant(ctx, r.Header.Get("X-Tenant-ID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	moduleSdk, err := h.sdkFor(ctx, module, tenantID, h.userIDFrom(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGet(ctx, w, r, moduleSdk, model, id)
	case http.MethodPost:
		h.handlePost(ctx, w, r, moduleSdk, model)
	case http.MethodPut, http.MethodPatch:
		h.handleUpdate(ctx, w, r, moduleSdk, model, id)
	case http.MethodDelete:
		h.handleDelete(ctx, w, moduleSdk, model, id)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
	}
}

// handleGet: metadata, un registro si viene id, o una página de resultados.
func (h *GenericCRUDHandler) handleGet(ctx context.Context, w http.ResponseWriter, r *http.Request, s *sdk.ModuleSDK, model, id string) {
	// _meta ocupa el lugar del {id} en la ruta: se intercepta antes de que
	// llegue al SDK, que lo trataría como un UUID.
	if id == metaSegment {
		meta, err := s.Meta(model)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, meta)
		return
	}

	if id != "" {
		record, err := s.Get(ctx, model, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, record)
		return
	}

	page, err := s.List(ctx, model, listOptionsFrom(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *GenericCRUDHandler) handlePost(ctx context.Context, w http.ResponseWriter, r *http.Request, s *sdk.ModuleSDK, model string) {
	data, err := decodeBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	id, err := s.Create(ctx, model, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (h *GenericCRUDHandler) handleUpdate(ctx context.Context, w http.ResponseWriter, r *http.Request, s *sdk.ModuleSDK, model, id string) {
	if id == "" {
		writeErr(w, http.StatusBadRequest, "falta el id del registro")
		return
	}

	data, err := decodeBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.Update(ctx, model, id, data); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "message": "actualizado"})
}

func (h *GenericCRUDHandler) handleDelete(ctx context.Context, w http.ResponseWriter, s *sdk.ModuleSDK, model, id string) {
	if id == "" {
		writeErr(w, http.StatusBadRequest, "falta el id del registro")
		return
	}

	if err := s.Delete(ctx, model, id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "message": "eliminado"})
}

// HandleTransition mueve un registro por su flujo: POST /api/{module}/{model}/{id}/transition
// con {"action": "confirmar"}. Es la única forma de cambiar el campo de
// estado — el SDK rechaza tocarlo desde un PUT normal.
func (h *GenericCRUDHandler) HandleTransition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	module, model, id := r.PathValue("module"), r.PathValue("model"), r.PathValue("id")
	if module == "" || model == "" || id == "" {
		writeErr(w, http.StatusBadRequest, "ruta inválida")
		return
	}

	ctx := r.Context()
	tenantID, err := h.resolveTenant(ctx, r.Header.Get("X-Tenant-ID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	s, err := h.sdkFor(ctx, module, tenantID, h.userIDFrom(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	var payload struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if payload.Action == "" {
		writeErr(w, http.StatusBadRequest, "falta \"action\"")
		return
	}

	result, err := s.Transition(ctx, model, id, payload.Action)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleHistory lista las transiciones aplicadas a un registro:
// GET /api/{module}/{model}/{id}/history
func (h *GenericCRUDHandler) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	module, model, id := r.PathValue("module"), r.PathValue("model"), r.PathValue("id")
	if module == "" || model == "" || id == "" {
		writeErr(w, http.StatusBadRequest, "ruta inválida")
		return
	}

	ctx := r.Context()
	tenantID, err := h.resolveTenant(ctx, r.Header.Get("X-Tenant-ID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	s, err := h.sdkFor(ctx, module, tenantID, h.userIDFrom(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	history, err := s.History(ctx, model, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": history})
}

// listOptionsFrom traduce ?page=&limit=&search=&order_by=&order_dir= a ListOptions.
//
// Cualquier otro parámetro es un filtro por columna. El operador va sufijado
// con doble guion bajo — name__contains=juan, credit_limit__gte=1000 — y sin
// sufijo es igualdad exacta. El SDK valida campo y operador contra el manifest.
func listOptionsFrom(r *http.Request) sdk.ListOptions {
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))

	opts := sdk.ListOptions{
		Page:     page,
		Limit:    limit,
		Search:   q.Get("search"),
		OrderBy:  q.Get("order_by"),
		OrderDir: q.Get("order_dir"),
	}

	reserved := map[string]bool{
		"page": true, "limit": true, "search": true,
		"order_by": true, "order_dir": true,
	}
	for key, vals := range q {
		if reserved[key] || len(vals) == 0 || vals[0] == "" {
			continue
		}

		field, op := key, "eq"
		if at := strings.LastIndex(key, "__"); at > 0 {
			field, op = key[:at], key[at+2:]
		}
		opts.Filters = append(opts.Filters, sdk.Filter{
			Field: field, Op: op, Value: vals[0],
		})
	}

	// Orden estable: el mismo query siempre produce el mismo SQL.
	sort.Slice(opts.Filters, func(i, j int) bool {
		if opts.Filters[i].Field != opts.Filters[j].Field {
			return opts.Filters[i].Field < opts.Filters[j].Field
		}
		return opts.Filters[i].Op < opts.Filters[j].Op
	})
	return opts
}

// sdkFor arma el SDK del módulo y garantiza que sus tablas existan.
func (h *GenericCRUDHandler) sdkFor(ctx context.Context, module, tenantID, userID string) (*sdk.ModuleSDK, error) {
	manifestJSON, err := h.manifest(module)
	if err != nil {
		return nil, err
	}

	moduleSdk := sdk.NewModuleSDK(module, tenantID, userID, h.dbConn.Pool())
	if err := moduleSdk.LoadManifest(manifestJSON); err != nil {
		return nil, err
	}

	h.mu.RLock()
	ready := h.schemaEnsured[module]
	h.mu.RUnlock()

	if !ready {
		if err := moduleSdk.EnsureSchema(ctx); err != nil {
			return nil, fmt.Errorf("no se pudo preparar el esquema de %s: %w", module, err)
		}
		h.mu.Lock()
		h.schemaEnsured[module] = true
		h.mu.Unlock()
	}

	return moduleSdk, nil
}

// InvalidateModule limpia el manifest y el estado de esquema en caché de un
// módulo, para que la próxima petición relea manifest.json desde disco.
//
// Studio-Flujo la llama después de escribir un manifest nuevo o extendido:
// sin esto, un módulo que ya estaba "caliente" en memoria (el caso típico al
// EXTENDER uno existente, que por definición ya se usó antes) seguiría
// sirviendo la versión vieja — el modelo recién agregado quedaría invisible
// hasta reiniciar el servidor, contradiciendo la idea de "generar y usar".
func (h *GenericCRUDHandler) InvalidateModule(module string) {
	h.mu.Lock()
	delete(h.manifestCache, module)
	delete(h.schemaEnsured, module)
	h.mu.Unlock()
}

// manifest lee (y cachea) el manifest.json del módulo.
func (h *GenericCRUDHandler) manifest(module string) (string, error) {
	h.mu.RLock()
	cached, ok := h.manifestCache[module]
	h.mu.RUnlock()
	if ok {
		return cached, nil
	}

	// filepath.Base evita que un ".." en la URL escape del directorio de módulos.
	path := filepath.Join(h.modulesDir, filepath.Base(module), "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("módulo %q no encontrado", module)
	}

	h.mu.Lock()
	h.manifestCache[module] = string(data)
	h.mu.Unlock()
	return string(data), nil
}

// resolveTenant acepta el UUID del tenant o su slug (ej. "default").
func (h *GenericCRUDHandler) resolveTenant(ctx context.Context, header string) (string, error) {
	if header == "" {
		return "", fmt.Errorf("falta el header X-Tenant-ID")
	}

	h.mu.RLock()
	cached, ok := h.tenantCache[header]
	h.mu.RUnlock()
	if ok {
		return cached, nil
	}

	var tenantID string
	err := h.dbConn.QueryRow(ctx,
		"SELECT id FROM tenants WHERE id::text = $1 OR slug = $1 LIMIT 1", header,
	).Scan(&tenantID)
	if err != nil {
		return "", fmt.Errorf("tenant %q no existe", header)
	}

	h.mu.Lock()
	h.tenantCache[header] = tenantID
	h.mu.Unlock()
	return tenantID, nil
}

func decodeBody(r *http.Request) (map[string]any, error) {
	var data map[string]any
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("JSON inválido: %w", err)
	}
	return data, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func writeErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
