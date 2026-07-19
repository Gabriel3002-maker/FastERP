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

// ModuleSDK es la interfaz que los módulos usan para hablar con el core.
type ModuleSDK struct {
	ModuleID string
	TenantID string
	UserID   string
	DB       *sql.DB
	Manifest *Manifest
}

// Manifest define la estructura de un módulo.
type Manifest struct {
	Name   string               `json:"name"`
	Models map[string]*ModelDef `json:"models"`
}

// ModelDef define una tabla en el módulo.
type ModelDef struct {
	Name   string               `json:"name"`
	Label  string               `json:"label,omitempty"`
	Fields map[string]*FieldDef `json:"fields"`
	Views  *ViewsDef            `json:"views,omitempty"`

	// FieldOrder conserva el orden en que el módulo declaró los campos.
	// Un map de Go no tiene orden, y ese orden es información: quien escribió
	// el manifest puso el campo identificador primero por algo. Las columnas
	// de la tabla y la documentación lo respetan.
	FieldOrder []string `json:"-"`
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
	Label    string   `json:"label,omitempty"`
	Help     string   `json:"help,omitempty"`
	Options  []string `json:"options,omitempty"`
	Readonly bool     `json:"readonly,omitempty"`
	Example  any      `json:"example,omitempty"`

	// Sequence controla el orden de presentación (columnas y formulario).
	// Convención tipo Odoo: 10, 20, 30… Un campo sin sequence recibe una
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
	return &ModuleSDK{ModuleID: moduleID, TenantID: tenantID, UserID: userID, DB: db}
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
		}
	}

	s.Manifest = m
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

// Create inserta un registro validando contra el manifest. → @fast.create()
func (s *ModuleSDK) Create(ctx context.Context, modelName string, data map[string]any) (string, error) {
	model, err := s.model(modelName)
	if err != nil {
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
		cols = append(cols, fieldName)
		vals = append(vals, raw)
		holders = append(holders, fmt.Sprintf("$%d", len(vals)))
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		s.tableName(modelName), strings.Join(cols, ", "), strings.Join(holders, ", "),
	)

	var id string
	if err := s.DB.QueryRowContext(ctx, query, vals...).Scan(&id); err != nil {
		log.Printf("[SDK] Create %s: %v", s.tableName(modelName), err)
		return "", err
	}
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
		vals = append(vals, raw)
		sets = append(sets, fmt.Sprintf("%s = $%d", fieldName, len(vals)))
	}

	if len(sets) == 0 {
		return fmt.Errorf("no hay campos válidos para actualizar")
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
	return nil
}

// Delete borra un registro. → @fast.delete()
func (s *ModuleSDK) Delete(ctx context.Context, modelName, id string) error {
	if _, err := s.model(modelName); err != nil {
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
	return nil
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

	return &Page{
		Data:       records,
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
		PageSizes:  PageSizes,
	}, nil
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
	}
	return string(text)
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
