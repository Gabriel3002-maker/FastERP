// Package sdk expone el API @fast que usan TODOS los módulos.
//
// Un módulo NUNCA escribe SQL ni toca el core. Solo:
//  1. Declara su esquema en manifest.json
//  2. Llama a los métodos @fast (Create/Get/Update/Delete/List/Search)
//
// El SDK se encarga de: crear la tabla, validar campos, aislar por tenant,
// paginar, y sanear cualquier identificador antes de tocar SQL.
package sdk

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// PageSizes son los únicos tamaños de página que el usuario puede elegir.
var PageSizes = []int{10, 20, 50, 100}

const DefaultPageSize = 20

// identRe valida nombres de módulo, modelo y campo antes de interpolarlos en SQL.
// Sin esto, un manifest malicioso podría inyectar SQL vía el nombre de un campo.
var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Columnas que el SDK administra y el módulo no puede declarar ni sobrescribir.
var reservedFields = map[string]bool{
	"id":         true,
	"tenant_id":  true,
	"created_at": true,
	"updated_at": true,
}

type HookEvent string

const (
	HookBeforeCreate     HookEvent = "before_create"
	HookAfterCreate      HookEvent = "after_create"
	HookBeforeUpdate     HookEvent = "before_update"
	HookAfterUpdate      HookEvent = "after_update"
	HookBeforeDelete     HookEvent = "before_delete"
	HookAfterDelete      HookEvent = "after_delete"
	HookBeforeTransition HookEvent = "before_transition"
	HookAfterTransition  HookEvent = "after_transition"
	HookBeforeList       HookEvent = "before_list"
	HookAfterList        HookEvent = "after_list"
)

type HookContext struct {
	Event    HookEvent
	Module   string
	Model    string
	ID       string
	Action   string
	Data     map[string]any
	Result   any
	TenantID string
	UserID   string
}

type HookFunc func(ctx context.Context, info *HookContext) error

// ModuleSDK es la interfaz que los módulos usan para hablar con el core.
type ModuleSDK struct {
	ModuleID string
	TenantID string
	UserID   string
	DB       *sql.DB
	Manifest *Manifest
	hooks    map[HookEvent][]HookFunc
}

// Manifest define la estructura de un módulo.
type Manifest struct {
	Name   string               `json:"name"`
	Label  string               `json:"label,omitempty"`
	Icon   string               `json:"icon,omitempty"`
	Models map[string]*ModelDef `json:"models"`
}

// ModelDef define una tabla en el módulo.
type ModelDef struct {
	Name        string               `json:"name"`
	Label       string               `json:"label,omitempty"`
	Fields      map[string]*FieldDef `json:"fields"`
	Views       *ViewsDef            `json:"views,omitempty"`
	Workflow    *WorkflowDef         `json:"workflow,omitempty"`
	Permissions map[string][]string  `json:"permissions,omitempty"`
	Hooks       map[string]any       `json:"hooks,omitempty"`

	// FieldOrder conserva el orden en que el módulo declaró los campos.
	// Un map de Go no tiene orden, y ese orden es información: quien escribió
	// el manifest puso el campo identificador primero por algo. Las columnas
	// de la tabla y la documentación lo respetan.
	FieldOrder []string `json:"-"`
}

// WorkflowDef convierte un campo de opciones en una máquina de estados.
//
// El campo declarado en "field" debe existir en Fields y traer "options" con
// los estados válidos: los mismos options que ya alimentan el <select> del
// formulario y el agrupado de kanban pasan a ser también los estados legales.
//
// Una vez declarado, ese campo deja de aceptarse en Create/Update directo —
// sólo cambia a través de una Transition() válida, así el historial siempre
// refleja el camino real que tomó el registro.
type WorkflowDef struct {
	Field       string                    `json:"field"`
	Initial     string                    `json:"initial"`
	Transitions map[string]*TransitionDef `json:"transitions"`
}

// TransitionDef declara una acción legal de la máquina de estados.
type TransitionDef struct {
	From  []string `json:"from"`
	To    string   `json:"to"`
	Label string   `json:"label,omitempty"`
}

// UnmarshalJSON carga el modelo conservando el orden de declaración.
func (m *ModelDef) UnmarshalJSON(data []byte) error {
	type alias ModelDef
	if err := json.Unmarshal(data, (*alias)(m)); err != nil {
		return err
	}

	order, err := jsonKeyOrder(data, "fields")
	if err != nil {
		return err
	}
	m.FieldOrder = order
	return nil
}

// OrderedFields devuelve los campos en el orden de presentación: por sequence
// si el manifest la declara, y si no, en el orden en que fueron escritos.
func (m *ModelDef) OrderedFields() []string {
	declared := m.FieldOrder
	if len(declared) != len(m.Fields) {
		// Sin orden de declaración (modelo armado en código): alfabético, que
		// al menos es determinista.
		declared = make([]string, 0, len(m.Fields))
		for name := range m.Fields {
			declared = append(declared, name)
		}
		sort.Strings(declared)
	}

	type entry struct {
		name     string
		sequence int
		position int
	}

	entries := make([]entry, len(declared))
	for i, name := range declared {
		sequence := (i + 1) * sequenceStep // implícita según la posición
		if declared := m.Fields[name].Sequence; declared != 0 {
			sequence = declared
		}
		entries[i] = entry{name: name, sequence: sequence, position: i}
	}

	// Empate de sequence: gana quien se declaró primero. Orden estable.
	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].sequence != entries[b].sequence {
			return entries[a].sequence < entries[b].sequence
		}
		return entries[a].position < entries[b].position
	})

	order := make([]string, len(entries))
	for i, e := range entries {
		order[i] = e.name
	}
	return order
}

// jsonKeyOrder lee las claves de un objeto anidado en el orden en que aparecen.
func jsonKeyOrder(data []byte, key string) ([]string, error) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}

	raw, ok := wrapper[key]
	if !ok {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // consume '{'
		return nil, err
	}

	var order []string
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("clave inesperada en %q", key)
		}
		order = append(order, name)

		var skip json.RawMessage // descarta el valor y avanza
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// FieldDef define una columna. TODO lo que el motor necesita saber sobre un
// campo viene de aquí: el SDK no asume tamaños, precisiones ni defaults.
type FieldDef struct {
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`

	// Dimensiones — las decide el módulo, no el core.
	// Length acepta también el alias "value" usado en algunos manifiestos.
	Length    int `json:"length,omitempty"`
	Precision int `json:"precision,omitempty"`
	Scale     int `json:"scale,omitempty"`

	// Restricciones
	Default any  `json:"default,omitempty"`
	Unique  bool `json:"unique,omitempty"`
	Index   bool `json:"index,omitempty"`

	// Metadatos para la UI y la documentación OpenAPI
	Label         string   `json:"label,omitempty"`
	Help          string   `json:"help,omitempty"`
	Placeholder   string   `json:"placeholder,omitempty"`
	VisibleIf     string   `json:"visibleIf,omitempty"`
	RequiredIf    string   `json:"requiredIf,omitempty"`
	Options       []string `json:"options,omitempty"`
	Readonly      bool     `json:"readonly,omitempty"`
	Computed      bool     `json:"computed,omitempty"`
	Example       any      `json:"example,omitempty"`
	RelatedModule string   `json:"related_module,omitempty"`
	RelatedModel  string   `json:"related_model,omitempty"`
	RelatedField  string   `json:"related_field,omitempty"`

	// Sequence controla el orden de presentación (columnas y formulario).
	// Convención de numeración: 10, 20, 30… Un campo sin sequence recibe una
	// implícita según su posición en el manifest —(posición+1)*10— así que
	// poner "sequence": 5 lo manda al frente y 15 lo mete entre el primero y
	// el segundo, sin tener que numerar todos los demás.
	Sequence int `json:"sequence,omitempty"`
}

// sequenceStep es el espacio que queda entre campos consecutivos sin sequence
// explícita, para poder intercalar sin renumerar.
const sequenceStep = 10

// UnmarshalJSON acepta "length" y su alias "value" para el tamaño del campo.
func (f *FieldDef) UnmarshalJSON(data []byte) error {
	type alias FieldDef // evita recursión infinita
	aux := struct {
		*alias
		Value *int `json:"value,omitempty"`
	}{alias: (*alias)(f)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if f.Length == 0 && aux.Value != nil {
		f.Length = *aux.Value
	}
	return nil
}

// ListOptions controla paginación, búsqueda y orden de List().
type ListOptions struct {
	Page     int      // 1-based
	Limit    int      // se ajusta al PageSize permitido más cercano
	Search   string   // busca en todos los campos de texto
	OrderBy  string   // campo del manifest, o created_at por defecto
	OrderDir string   // asc | desc
	Filters  []Filter // filtros por columna
}

// Filter es una condición sobre un campo declarado en el manifest.
type Filter struct {
	Field string
	Op    string // ver filterOps
	Value string
}

// filterOps son los operadores que un módulo puede usar por columna.
// La plantilla SQL es fija: del exterior sólo entra el valor, como parámetro.
var filterOps = map[string]string{
	"eq":       "%s = $%d",
	"ne":       "%s <> $%d",
	"contains": "%s ILIKE $%d",
	"starts":   "%s ILIKE $%d",
	"gt":       "%s > $%d",
	"gte":      "%s >= $%d",
	"lt":       "%s < $%d",
	"lte":      "%s <= $%d",
}

// filterValue adapta el valor al operador (los ILIKE necesitan comodines).
func filterValue(op, value string) string {
	switch op {
	case "contains":
		return "%" + value + "%"
	case "starts":
		return value + "%"
	default:
		return value
	}
}

// Page es la respuesta paginada estándar de todos los módulos.
type Page struct {
	Data       []map[string]any `json:"data"`
	Total      int              `json:"total"`
	Page       int              `json:"page"`
	Limit      int              `json:"limit"`
	TotalPages int              `json:"total_pages"`
	PageSizes  []int            `json:"page_sizes"`
}

// NewModuleSDK crea una instancia del SDK para un módulo.
func NewModuleSDK(moduleID, tenantID, userID string, db *sql.DB) *ModuleSDK {
	return &ModuleSDK{
		ModuleID: moduleID,
		TenantID: tenantID,
		UserID:   userID,
		DB:       db,
		hooks:    make(map[HookEvent][]HookFunc),
	}
}

// RegisterHook agrega un hook al SDK para un evento específico.
func (s *ModuleSDK) RegisterHook(event HookEvent, hook HookFunc) {
	if s.hooks == nil {
		s.hooks = make(map[HookEvent][]HookFunc)
	}
	s.hooks[event] = append(s.hooks[event], hook)
}

func (s *ModuleSDK) runHooks(ctx context.Context, event HookEvent, info *HookContext, failFast bool) error {
	if s.hooks == nil {
		return nil
	}
	for _, hook := range s.hooks[event] {
		if err := hook(ctx, info); err != nil {
			if failFast {
				return err
			}
			log.Printf("[SDK] hook %s error: %v", event, err)
		}
	}
	return nil
}

// LoadManifest carga y valida el manifest.json del módulo.
func (s *ModuleSDK) LoadManifest(manifestJSON string) error {
	m := &Manifest{}
	if err := json.Unmarshal([]byte(manifestJSON), m); err != nil {
		return fmt.Errorf("manifest inválido: %w", err)
	}

	if !identRe.MatchString(s.ModuleID) {
		return fmt.Errorf("nombre de módulo inválido: %q", s.ModuleID)
	}

	for modelName, model := range m.Models {
		if !identRe.MatchString(modelName) {
			return fmt.Errorf("nombre de modelo inválido: %q", modelName)
		}
		for fieldName, def := range model.Fields {
			if !identRe.MatchString(fieldName) {
				return fmt.Errorf("campo inválido %q en modelo %q", fieldName, modelName)
			}
			if reservedFields[fieldName] {
				return fmt.Errorf("campo %q en modelo %q está reservado por el core", fieldName, modelName)
			}
			if _, known := resolveType(def.Type); !known {
				log.Printf("[SDK] %s.%s.%s: tipo %q desconocido, se usará TEXT (tipos válidos: %s)",
					s.ModuleID, modelName, fieldName, def.Type, strings.Join(KnownTypes(), ", "))
			}
			fieldType := strings.ToLower(strings.TrimSpace(def.Type))
			if fieldType == "many2one" || fieldType == "one2many" || fieldType == "many2many" {
				if def.RelatedModel == "" {
					return fmt.Errorf("campo de relación %q en modelo %q debe declarar related_model", fieldName, modelName)
				}
				if !identRe.MatchString(def.RelatedModel) {
					return fmt.Errorf("related_model %q en campo %q no es un identificador válido", def.RelatedModel, fieldName)
				}
				if def.RelatedModule != "" && !identRe.MatchString(def.RelatedModule) {
					return fmt.Errorf("related_module %q en campo %q no es un identificador válido", def.RelatedModule, fieldName)
				}
				if def.RelatedModule == "" {
					def.RelatedModule = s.ModuleID
				}
			}
			if fieldType == "enum" || fieldType == "selection" {
				if len(def.Options) == 0 {
					return fmt.Errorf("campo %q en modelo %q de tipo %q requiere opciones", fieldName, modelName, def.Type)
				}
			}
		}
		if err := validateWorkflow(modelName, model); err != nil {
			return err
		}
	}

	s.Manifest = m
	return nil
}

// validateWorkflow verifica que el flujo declarado sea consistente ANTES de
// que el módulo se sirva: una transición hacia un estado que no existe se
// rechaza al cargar, no la primera vez que alguien la use.
func validateWorkflow(modelName string, model *ModelDef) error {
	wf := model.Workflow
	if wf == nil {
		return nil
	}

	field, ok := model.Fields[wf.Field]
	if !ok {
		return fmt.Errorf("workflow de %q: el campo %q no existe en el modelo", modelName, wf.Field)
	}
	if len(field.Options) == 0 {
		return fmt.Errorf(
			"workflow de %q: el campo %q debe declarar \"options\" con los estados válidos",
			modelName, wf.Field)
	}
	if wf.Initial == "" {
		return fmt.Errorf("workflow de %q: falta el estado inicial", modelName)
	}
	if len(wf.Transitions) == 0 {
		return fmt.Errorf("workflow de %q: no declara ninguna transición", modelName)
	}

	valid := make(map[string]bool, len(field.Options))
	for _, opt := range field.Options {
		valid[opt] = true
	}
	if !valid[wf.Initial] {
		return fmt.Errorf(
			"workflow de %q: el estado inicial %q no está en options de %q",
			modelName, wf.Initial, wf.Field)
	}

	for action, def := range wf.Transitions {
		if !identRe.MatchString(action) {
			return fmt.Errorf("workflow de %q: nombre de transición inválido %q", modelName, action)
		}
		if len(def.From) == 0 {
			return fmt.Errorf("workflow de %q: la transición %q necesita al menos un estado de origen",
				modelName, action)
		}
		for _, from := range def.From {
			if !valid[from] {
				return fmt.Errorf(
					"workflow de %q: la transición %q parte de %q, que no está en options de %q",
					modelName, action, from, wf.Field)
			}
		}
		if !valid[def.To] {
			return fmt.Errorf(
				"workflow de %q: la transición %q llega a %q, que no está en options de %q",
				modelName, action, def.To, wf.Field)
		}
	}
	return nil
}

// model resuelve un modelo del manifest, validando que exista.
func (s *ModuleSDK) model(modelName string) (*ModelDef, error) {
	if s.Manifest == nil {
		return nil, fmt.Errorf("manifest no cargado")
	}
	m, ok := s.Manifest.Models[modelName]
	if !ok {
		return nil, fmt.Errorf("modelo %q no existe en el manifest de %q", modelName, s.ModuleID)
	}
	return m, nil
}

// tableName retorna el nombre de tabla prefijado: mod_<modulo>_<modelo>.
func (s *ModuleSDK) tableName(modelName string) string {
	return fmt.Sprintf("mod_%s_%s", s.ModuleID, modelName)
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (s *ModuleSDK) relatedTableName(def *FieldDef) string {
	module := def.RelatedModule
	if module == "" {
		module = s.ModuleID
	}
	return fmt.Sprintf("mod_%s_%s", module, def.RelatedModel)
}

func (s *ModuleSDK) relatedDisplayField(def *FieldDef) string {
	if def.RelatedField != "" && identRe.MatchString(def.RelatedField) {
		return def.RelatedField
	}
	if def.RelatedModule == "" || def.RelatedModule == s.ModuleID {
		if related, ok := s.Manifest.Models[def.RelatedModel]; ok {
			return identifierField(related, related.OrderedFields())
		}
	}
	return "id"
}

func (s *ModuleSDK) selectColumnsWithJoins(model *ModelDef) ([]string, []string) {
	cols := []string{"base.id"}
	joins := []string{}

	for fieldName, def := range model.Fields {
		if def.Type == "many2one" && def.RelatedModel != "" {
			displayField := s.relatedDisplayField(def)
			alias := "rel_" + fieldName
			cols = append(cols,
				fmt.Sprintf("base.%s", quoteIdent(fieldName)),
				fmt.Sprintf("%s.%s AS __rel_%s", quoteIdent(alias), quoteIdent(displayField), fieldName),
			)
			joins = append(joins, fmt.Sprintf(
				"LEFT JOIN %s %s ON %s.id = base.%s",
				quoteIdent(s.relatedTableName(def)), quoteIdent(alias), quoteIdent(alias), quoteIdent(fieldName),
			))
			continue
		}
		cols = append(cols, fmt.Sprintf("base.%s", quoteIdent(fieldName)))
	}
	cols = append(cols, "base.created_at", "base.updated_at")
	return cols, joins
}

// Create inserta un registro validando contra el manifest. → @fast.create()
func (s *ModuleSDK) Create(ctx context.Context, modelName string, data map[string]any) (string, error) {
	model, err := s.model(modelName)
	if err != nil {
		return "", err
	}

	if err := rejectWorkflowField(model, data); err != nil {
		return "", err
	}

	cols := []string{"tenant_id"}
	vals := []any{s.TenantID}
	holders := []string{"$1"}

	for fieldName, def := range model.Fields {
		raw, present := data[fieldName]
		if !present || isEmpty(raw) {
			if def.Required {
				return "", fmt.Errorf("el campo %q es obligatorio", fieldName)
			}
			continue
		}
		if err := checkLength(fieldName, def, raw); err != nil {
			return "", err
		}
		prepared, err := prepareValue(def, raw)
		if err != nil {
			return "", err
		}
		cols = append(cols, fieldName)
		vals = append(vals, prepared)
		holders = append(holders, fmt.Sprintf("$%d", len(vals)))
	}

	// El estado inicial lo asigna el flujo, nunca quien crea el registro.
	if wf := model.Workflow; wf != nil {
		cols = append(cols, wf.Field)
		vals = append(vals, wf.Initial)
		holders = append(holders, fmt.Sprintf("$%d", len(vals)))
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		s.tableName(modelName), strings.Join(cols, ", "), strings.Join(holders, ", "),
	)

	var id string
	if err := s.runHooks(ctx, HookBeforeCreate, &HookContext{
		Event:    HookBeforeCreate,
		Module:   s.ModuleID,
		Model:    modelName,
		Data:     data,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, true); err != nil {
		return "", err
	}

	if err := s.DB.QueryRowContext(ctx, query, vals...).Scan(&id); err != nil {
		log.Printf("[SDK] Create %s: %v", s.tableName(modelName), err)
		return "", err
	}

	_ = s.runHooks(ctx, HookAfterCreate, &HookContext{
		Event:    HookAfterCreate,
		Module:   s.ModuleID,
		Model:    modelName,
		ID:       id,
		Data:     data,
		Result:   id,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, false)
	return id, nil
}

// Get retorna un registro por ID. → @fast.read()
func (s *ModuleSDK) Get(ctx context.Context, modelName, id string) (map[string]any, error) {
	model, err := s.model(modelName)
	if err != nil {
		return nil, err
	}

	cols := selectColumns(model)
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE id = $1 AND tenant_id = $2 LIMIT 1",
		strings.Join(cols, ", "), s.tableName(modelName),
	)

	rows, err := s.DB.QueryContext(ctx, query, id, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("registro no encontrado")
	}
	return scanRow(rows, cols, model)
}

// Update actualiza un registro. → @fast.update()
func (s *ModuleSDK) Update(ctx context.Context, modelName, id string, data map[string]any) error {
	model, err := s.model(modelName)
	if err != nil {
		return err
	}

	if err := rejectWorkflowField(model, data); err != nil {
		return err
	}

	sets := []string{}
	vals := []any{s.TenantID, id}

	for fieldName, def := range model.Fields {
		raw, present := data[fieldName]
		if !present {
			continue
		}
		if def.Required && isEmpty(raw) {
			return fmt.Errorf("el campo %q es obligatorio", fieldName)
		}
		if err := checkLength(fieldName, def, raw); err != nil {
			return err
		}
		prepared, err := prepareValue(def, raw)
		if err != nil {
			return err
		}
		vals = append(vals, prepared)
		sets = append(sets, fmt.Sprintf("%s = $%d", fieldName, len(vals)))
	}

	if len(sets) == 0 {
		return fmt.Errorf("no hay campos válidos para actualizar")
	}

	if err := s.runHooks(ctx, HookBeforeUpdate, &HookContext{
		Event:    HookBeforeUpdate,
		Module:   s.ModuleID,
		Model:    modelName,
		ID:       id,
		Data:     data,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, true); err != nil {
		return err
	}

	query := fmt.Sprintf(
		"UPDATE %s SET %s, updated_at = CURRENT_TIMESTAMP WHERE id = $2 AND tenant_id = $1",
		s.tableName(modelName), strings.Join(sets, ", "),
	)

	res, err := s.DB.ExecContext(ctx, query, vals...)
	if err != nil {
		log.Printf("[SDK] Update %s: %v", s.tableName(modelName), err)
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("registro no encontrado")
	}

	_ = s.runHooks(ctx, HookAfterUpdate, &HookContext{
		Event:    HookAfterUpdate,
		Module:   s.ModuleID,
		Model:    modelName,
		ID:       id,
		Data:     data,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, false)
	return nil
}

// Delete borra un registro. → @fast.delete()
func (s *ModuleSDK) Delete(ctx context.Context, modelName, id string) error {
	if _, err := s.model(modelName); err != nil {
		return err
	}

	if err := s.runHooks(ctx, HookBeforeDelete, &HookContext{
		Event:    HookBeforeDelete,
		Module:   s.ModuleID,
		Model:    modelName,
		ID:       id,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, true); err != nil {
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1 AND tenant_id = $2", s.tableName(modelName))
	res, err := s.DB.ExecContext(ctx, query, id, s.TenantID)
	if err != nil {
		log.Printf("[SDK] Delete %s: %v", s.tableName(modelName), err)
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("registro no encontrado")
	}

	_ = s.runHooks(ctx, HookAfterDelete, &HookContext{
		Event:    HookAfterDelete,
		Module:   s.ModuleID,
		Model:    modelName,
		ID:       id,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, false)
	return nil
}

// directamente.
//
// Si se pudiera, el flujo sería decorativo: cualquiera podría saltarse las
// transiciones escribiendo el estado a mano vía PUT, y el historial dejaría
// de reflejar el camino real que tomó el registro.
func rejectWorkflowField(model *ModelDef, data map[string]any) error {
	wf := model.Workflow
	if wf == nil {
		return nil
	}
	if _, present := data[wf.Field]; present {
		return fmt.Errorf(
			"%q se cambia con una transición del flujo, no editando el campo directamente",
			wf.Field)
	}
	return nil
}

// TransitionResult resume el movimiento que acaba de aplicar Transition().
type TransitionResult struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// Transition mueve un registro de un estado a otro por una acción declarada
// en el manifest. Es el único camino para cambiar el campo de estado — no
// tiene equivalente @fast directo, reemplaza a "editar el campo a mano".
//
// Corre dentro de una transacción con SELECT ... FOR UPDATE: si dos personas
// disparan una transición sobre el mismo registro al mismo tiempo, la segunda
// espera a que la primera termine y relee el estado ya actualizado — así
// nunca aplican ambas partiendo del mismo estado de origen.
func (s *ModuleSDK) Transition(ctx context.Context, modelName, id, action string) (*TransitionResult, error) {
	model, err := s.model(modelName)
	if err != nil {
		return nil, err
	}

	wf := model.Workflow
	if wf == nil {
		return nil, fmt.Errorf("el modelo %q no tiene un flujo definido", modelName)
	}
	def, ok := wf.Transitions[action]
	if !ok {
		return nil, fmt.Errorf("la acción %q no existe en el flujo de %q", action, modelName)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	table := s.tableName(modelName)
	var current string
	lockQuery := fmt.Sprintf(
		"SELECT %s FROM %s WHERE id = $1 AND tenant_id = $2 FOR UPDATE", wf.Field, table)
	if err := tx.QueryRowContext(ctx, lockQuery, id, s.TenantID).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("registro no encontrado")
		}
		return nil, err
	}

	if !containsState(def.From, current) {
		return nil, fmt.Errorf("no se puede %q desde %q (estado actual)", action, current)
	}

	if err := s.runHooks(ctx, HookBeforeTransition, &HookContext{
		Event:    HookBeforeTransition,
		Module:   s.ModuleID,
		Model:    modelName,
		ID:       id,
		Action:   action,
		Data:     map[string]any{"from": current, "to": def.To},
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, true); err != nil {
		return nil, err
	}

	update := fmt.Sprintf(
		"UPDATE %s SET %s = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2 AND tenant_id = $3",
		table, wf.Field)
	if _, err := tx.ExecContext(ctx, update, def.To, id, s.TenantID); err != nil {
		log.Printf("[SDK] Transition %s: %v", table, err)
		return nil, err
	}

	if err := s.insertHistory(ctx, tx, modelName, id, action, current, def.To); err != nil {
		return nil, fmt.Errorf("no se pudo registrar el historial: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	result := &TransitionResult{ID: id, Action: action, From: current, To: def.To}
	_ = s.runHooks(ctx, HookAfterTransition, &HookContext{
		Event:    HookAfterTransition,
		Module:   s.ModuleID,
		Model:    modelName,
		ID:       id,
		Action:   action,
		Result:   result,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, false)

	return result, nil
}

// History devuelve el historial de transiciones de un registro.
func (s *ModuleSDK) CanTransition(ctx context.Context, modelName, id, action string) (bool, error) {
	model, err := s.model(modelName)
	if err != nil {
		return false, err
	}
	wf := model.Workflow
	if wf == nil {
		return false, fmt.Errorf("el modelo %q no tiene un flujo definido", modelName)
	}
	def, ok := wf.Transitions[action]
	if !ok {
		return false, fmt.Errorf("la acción %q no existe en el flujo de %q", action, modelName)
	}

	var current string
	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1 AND tenant_id = $2 LIMIT 1", wf.Field, s.tableName(modelName))
	if err := s.DB.QueryRowContext(ctx, query, id, s.TenantID).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("registro no encontrado")
		}
		return false, err
	}
	return containsState(def.From, current), nil
}

func (s *ModuleSDK) AvailableTransitions(ctx context.Context, modelName, id string) ([]string, error) {
	model, err := s.model(modelName)
	if err != nil {
		return nil, err
	}
	wf := model.Workflow
	if wf == nil {
		return nil, fmt.Errorf("el modelo %q no tiene un flujo definido", modelName)
	}

	var current string
	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1 AND tenant_id = $2 LIMIT 1", wf.Field, s.tableName(modelName))
	if err := s.DB.QueryRowContext(ctx, query, id, s.TenantID).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("registro no encontrado")
		}
		return nil, err
	}

	available := make([]string, 0, len(wf.Transitions))
	for action, def := range wf.Transitions {
		if containsState(def.From, current) {
			available = append(available, action)
		}
	}
	return available, nil
}

func (s *ModuleSDK) History(ctx context.Context, modelName, id string) ([]map[string]any, error) {
	model, err := s.model(modelName)
	if err != nil {
		return nil, err
	}
	if model.Workflow == nil {
		return nil, fmt.Errorf("el modelo %q no tiene un flujo definido", modelName)
	}

	query := fmt.Sprintf(
		`SELECT action, from_state, to_state, user_id, created_at
		 FROM %s WHERE tenant_id = $1 AND record_id = $2 ORDER BY created_at ASC`,
		s.historyTableName(modelName))

	rows, err := s.DB.QueryContext(ctx, query, s.TenantID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]map[string]any, 0)
	for rows.Next() {
		var action, from, to string
		var userID sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&action, &from, &to, &userID, &createdAt); err != nil {
			return nil, err
		}
		entry := map[string]any{
			"action": action, "from": from, "to": to, "created_at": createdAt,
		}
		if userID.Valid {
			entry["user_id"] = userID.String
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// execer es lo mínimo que insertHistory necesita: lo cumplen tanto *sql.DB
// como *sql.Tx, así el mismo código escribe el historial dentro o fuera de
// una transacción.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *ModuleSDK) insertHistory(ctx context.Context, exec execer, modelName, recordID, action, from, to string) error {
	query := fmt.Sprintf(
		`INSERT INTO %s (tenant_id, record_id, action, from_state, to_state, user_id)
		 VALUES ($1, $2, $3, $4, $5, $6)`, s.historyTableName(modelName))

	var userID any
	if s.UserID != "" {
		userID = s.UserID
	}
	_, err := exec.ExecContext(ctx, query, s.TenantID, recordID, action, from, to, userID)
	return err
}

// historyTableName: mod_<modulo>_<modelo>_history.
func (s *ModuleSDK) historyTableName(modelName string) string {
	return fmt.Sprintf("mod_%s_%s_history", s.ModuleID, modelName)
}

func containsState(states []string, target string) bool {
	for _, state := range states {
		if state == target {
			return true
		}
	}
	return false
}

// List retorna registros paginados. → @fast.list() / @fast.search()
func (s *ModuleSDK) List(ctx context.Context, modelName string, opts ListOptions) (*Page, error) {
	model, err := s.model(modelName)
	if err != nil {
		return nil, err
	}

	limit := normalizeLimit(opts.Limit)
	page := opts.Page
	if page < 1 {
		page = 1
	}

	where := []string{"tenant_id = $1"}
	args := []any{s.TenantID}

	// Filtros por columna, sólo sobre campos declarados en el manifest.
	for _, filter := range opts.Filters {
		if _, ok := model.Fields[filter.Field]; !ok {
			return nil, fmt.Errorf("no se puede filtrar por %q: no existe en el manifest", filter.Field)
		}

		op := filter.Op
		if op == "" {
			op = "eq"
		}
		template, ok := filterOps[op]
		if !ok {
			return nil, fmt.Errorf("operador %q no soportado en %q", op, filter.Field)
		}

		args = append(args, filterValue(op, filter.Value))
		where = append(where, fmt.Sprintf(template, filter.Field, len(args)))
	}

	// Búsqueda libre sobre los campos de texto del modelo.
	if q := strings.TrimSpace(opts.Search); q != "" {
		var ors []string
		args = append(args, "%"+q+"%")
		pos := len(args)
		for fieldName, def := range model.Fields {
			if def.IsSearchable() {
				ors = append(ors, fmt.Sprintf("%s ILIKE $%d", fieldName, pos))
			}
		}
		if len(ors) > 0 {
			where = append(where, "("+strings.Join(ors, " OR ")+")")
		}
	}

	whereSQL := strings.Join(where, " AND ")
	table := s.tableName(modelName)

	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", table, whereSQL)
	if err := s.runHooks(ctx, HookBeforeList, &HookContext{
		Event:  HookBeforeList,
		Module: s.ModuleID,
		Model:  modelName,
		Data: map[string]any{
			"page":      page,
			"limit":     limit,
			"order_by":  opts.OrderBy,
			"order_dir": opts.OrderDir,
			"filters":   opts.Filters,
			"search":    opts.Search,
		},
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, true); err != nil {
		return nil, err
	}

	if err := s.DB.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		log.Printf("[SDK] Count %s: %v", table, err)
		return nil, err
	}

	totalPages := (total + limit - 1) / limit
	if totalPages > 0 && page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * limit

	orderBy, orderDir := s.normalizeOrder(model, opts.OrderBy, opts.OrderDir)
	cols := selectColumns(model)

	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s ORDER BY %s %s LIMIT %d OFFSET %d",
		strings.Join(cols, ", "), table, whereSQL, orderBy, orderDir, limit, offset,
	)

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("[SDK] List %s: %v", table, err)
		return nil, err
	}
	defer rows.Close()

	records := make([]map[string]any, 0, limit)
	for rows.Next() {
		entry, err := scanRow(rows, cols, model)
		if err != nil {
			return nil, err
		}
		records = append(records, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pageResult := &Page{
		Data:       records,
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
		PageSizes:  PageSizes,
	}

	_ = s.runHooks(ctx, HookAfterList, &HookContext{
		Event:    HookAfterList,
		Module:   s.ModuleID,
		Model:    modelName,
		Data:     map[string]any{"page": page, "limit": limit},
		Result:   pageResult,
		TenantID: s.TenantID,
		UserID:   s.UserID,
	}, false)

	return pageResult, nil
}

// Search es azúcar sintáctico sobre List con un término de búsqueda.
func (s *ModuleSDK) Search(ctx context.Context, modelName, query string, opts ListOptions) (*Page, error) {
	opts.Search = query
	return s.List(ctx, modelName, opts)
}

// Count retorna el total de registros del modelo para el tenant actual.
func (s *ModuleSDK) Count(ctx context.Context, modelName string) (int, error) {
	if _, err := s.model(modelName); err != nil {
		return 0, err
	}
	var total int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE tenant_id = $1", s.tableName(modelName))
	err := s.DB.QueryRowContext(ctx, query, s.TenantID).Scan(&total)
	return total, err
}

// normalizeOrder valida el campo de orden contra el manifest para evitar inyección.
func (s *ModuleSDK) normalizeOrder(model *ModelDef, orderBy, orderDir string) (string, string) {
	col := "created_at"
	if orderBy != "" {
		if _, ok := model.Fields[orderBy]; ok {
			col = orderBy
		} else if orderBy == "created_at" || orderBy == "updated_at" {
			col = orderBy
		}
	}

	dir := "DESC"
	if strings.EqualFold(orderDir, "asc") {
		dir = "ASC"
	}
	return col, dir
}

// normalizeLimit ajusta cualquier valor al PageSize permitido más cercano.
func normalizeLimit(limit int) int {
	for _, allowed := range PageSizes {
		if limit == allowed {
			return limit
		}
	}
	return DefaultPageSize
}

// MaxExternalLimit es el techo para un limit que viene de fuera (query string,
// headers) en endpoints que no son el List del SDK: sin tope, ?limit=999999999
// materializa la tabla entera en memoria y el proceso muere por OOM.
const MaxExternalLimit = 1000

// ClampExternalLimit acota un limit externo a [1, MaxExternalLimit]. Un valor
// no parseable, cero o negativo devuelve def.
func ClampExternalLimit(limit int, def int) int {
	if limit <= 0 {
		return def
	}
	if limit > MaxExternalLimit {
		return MaxExternalLimit
	}
	return limit
}

// selectColumns lista las columnas a devolver: nunca incluye tenant_id.
func selectColumns(model *ModelDef) []string {
	cols := []string{"id"}
	for fieldName := range model.Fields {
		cols = append(cols, fieldName)
	}
	return append(cols, "created_at", "updated_at")
}

// scanRow convierte una fila en map, devolviendo cada campo con el tipo que
// el manifest promete.
//
// El driver entrega NUMERIC como []byte: sin esta conversión un campo money
// saldría como "2500.75" (texto) mientras el OpenAPI declara type: number, y
// la API estaría mintiendo sobre su propio contrato.
func scanRow(rows *sql.Rows, cols []string, model *ModelDef) (map[string]any, error) {
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range cols {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}

	entry := make(map[string]any, len(cols))
	for i, col := range cols {
		entry[col] = normalizeValue(values[i], model.Fields[col])
	}
	return entry, nil
}

// normalizeValue lleva el valor crudo del driver al tipo declarado.
func normalizeValue(value any, def *FieldDef) any {
	if value == nil {
		return nil
	}

	text, isBytes := value.([]byte)
	if !isBytes {
		return value
	}

	// Columnas administradas por el core (id, fechas) o campos sin declarar.
	if def == nil {
		return string(text)
	}

	fieldType := strings.ToLower(strings.TrimSpace(def.Type))
	if fieldType == "uuid[]" {
		if arr, err := parsePostgresUUIDArray(string(text)); err == nil {
			return arr
		}
		return string(text)
	}

	switch def.Spec().JSONType {
	case "number":
		if n, err := strconv.ParseFloat(string(text), 64); err == nil {
			return n
		}
	case "integer":
		if n, err := strconv.ParseInt(string(text), 10, 64); err == nil {
			return n
		}
	case "boolean":
		if b, err := strconv.ParseBool(string(text)); err == nil {
			return b
		}
	case "array", "object":
		var decoded any
		if err := json.Unmarshal(text, &decoded); err == nil {
			return decoded
		}
	}
	return string(text)
}

func prepareValue(def *FieldDef, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	fieldType := strings.ToLower(strings.TrimSpace(def.Type))
	switch fieldType {
	case "uuid", "many2one":
		s, ok := value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("el campo %q debe ser un UUID como string", def.Type)
		}
		if _, err := uuid.Parse(s); err != nil {
			return nil, fmt.Errorf("el campo %q debe ser un UUID válido: %w", def.Type, err)
		}
		return s, nil
	case "uuid[]":
		ids, err := parseUUIDArray(value)
		if err != nil {
			return nil, err
		}
		return pq.Array(ids), nil
	case "enum", "selection":
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("el campo %q debe ser un texto", def.Type)
		}
		if !isAllowedOption(def.Options, s) {
			return nil, fmt.Errorf("el campo %q debe ser una de las opciones válidas", def.Type)
		}
		return s, nil
	case "date":
		return parseDateTime(value, []string{"2006-01-02"})
	case "datetime", "timestamp":
		return parseDateTime(value, []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"})
	case "one2many", "many2many", "json":
		if raw, ok := value.([]byte); ok {
			return raw, nil
		}
		if s, ok := value.(string); ok {
			return []byte(s), nil
		}
		marshaled, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("no se puede serializar %q como JSON: %w", def.Type, err)
		}
		return marshaled, nil
	default:
		return value, nil
	}
}

// prepareUUIDDefault valida el default de un campo uuid[] declarado en el
// manifest y lo devuelve como lista de UUIDs lista para el literal SQL.
func prepareUUIDDefault(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, err := parseUUIDArray(value)
	if err != nil {
		return nil, err
	}
	for _, id := range items {
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("default uuid[] inválido: %w", err)
		}
	}
	return items, nil
}

// parsePostgresUUIDArray convierte la literal de array que devuelve Postgres
// (`{a,b,c}`, y `{}` cuando viene vacía) en []string.
//
// El driver entrega uuid[] como []byte, no como un arreglo nativo: sin esto el
// campo saldría en la API como el texto "{a,b}" en lugar de un arreglo JSON.
func parsePostgresUUIDArray(text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}
	// "{}" y "NULL" son las dos formas en que Postgres representa un vacío.
	if strings.EqualFold(trimmed, "null") {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return nil, fmt.Errorf("literal de array de Postgres inválida: %q", text)
	}

	body := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if body == "" {
		return nil, nil
	}

	items := make([]string, 0, 4)
	var current strings.Builder
	inQuotes := false
	escaped := false
	quoted := false

	flush := func() {
		item := strings.TrimSpace(current.String())
		current.Reset()
		quoted = false
		// Un elemento NULL de Postgres significa "sin valor", no la cadena.
		if !quoted && strings.EqualFold(item, "null") {
			return
		}
		items = append(items, item)
	}

	for _, r := range body {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && inQuotes:
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
			quoted = true
		case r == ',' && !inQuotes:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if inQuotes || escaped {
		return nil, fmt.Errorf("literal de array de Postgres sin cerrar: %q", text)
	}
	flush()

	for _, id := range items {
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("uuid[] contiene un UUID inválido: %w", err)
		}
	}
	return items, nil
}

func parseUUIDArray(value any) ([]string, error) {
	var items []string

	switch v := value.(type) {
	case []string:
		items = v
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("uuid[] debe ser un arreglo de strings")
			}
			items = append(items, s)
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, nil
		}
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
				return nil, fmt.Errorf("uuid[] debe ser un arreglo JSON de strings: %w", err)
			}
			return items, nil
		}
		return nil, fmt.Errorf("uuid[] debe ser un arreglo de UUIDs")
	case []byte:
		trimmed := strings.TrimSpace(string(v))
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal(v, &items); err != nil {
				return nil, fmt.Errorf("uuid[] debe ser un arreglo JSON de strings: %w", err)
			}
			return items, nil
		}
		return nil, fmt.Errorf("uuid[] debe ser un arreglo de UUIDs")
	default:
		return nil, fmt.Errorf("uuid[] debe ser un arreglo de UUIDs")
	}

	for _, id := range items {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("uuid[] no puede contener valores vacíos")
		}
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("uuid[] contiene un UUID inválido: %w", err)
		}
	}
	return items, nil
}

func parseDateTime(value any, layouts []string) (any, error) {
	if value == nil {
		return nil, nil
	}
	if t, ok := value.(time.Time); ok {
		return t, nil
	}
	var text string
	switch v := value.(type) {
	case string:
		text = strings.TrimSpace(v)
	case []byte:
		text = strings.TrimSpace(string(v))
	default:
		return nil, fmt.Errorf("el campo debe ser una cadena de fecha/hora")
	}
	if text == "" {
		return nil, nil
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("el campo debe ser una fecha/hora válida en uno de los formatos aceptados")
}

func isAllowedOption(options []string, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// checkLength valida el largo contra el manifest antes de llegar a Postgres.
//
// Sin esto el usuario recibiría "pq: value too long for type character
// varying(20)": el motor sabe cuál es el campo y cuánto admite, así que puede
// decirlo en esos términos.
func checkLength(field string, def *FieldDef, value any) error {
	if def.Length <= 0 {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return nil
	}
	if len([]rune(text)) > def.Length {
		label := field
		if def.Label != "" {
			label = def.Label
		}
		return fmt.Errorf("%s admite máximo %d caracteres (se enviaron %d)",
			label, def.Length, len([]rune(text)))
	}
	return nil
}
