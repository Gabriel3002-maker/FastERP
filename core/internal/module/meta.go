package module

import (
	"sort"
	"strings"
)

// Esto es el motor de vistas: a partir del esquema de un modelo deduce qué hay
// que dibujar. Un módulo declara campos y no escribe una línea de UI.
//
// La idea es que añadir un campo a module.yaml lo haga aparecer en la tabla, en
// el formulario y en la tarjeta sin tocar nada más. Un formulario escrito a mano
// es justo donde se pierden los campos cuando el esquema crece, porque nadie
// vuelve a la vista a añadir la columna nueva.

// maxInferredColumns limita las columnas deducidas: una tabla con 19 columnas no
// se lee. Si el módulo quiere más, las declara en views.list.
const maxInferredColumns = 6

// PageSizes son los tamaños de página que el cliente ofrece. Fijarlos aquí y no
// aceptarlos del cliente es lo que evita que alguien pida limit=100000.
//
// El 5 entra porque revisar una tabla corta paginada de cinco en cinco es más
// cómodo que con la página siguiente, y porque el selector lo ofrece.
var PageSizes = []int{5, 10, 25, 50, 100}

// DefaultPageSize es el tamaño con el que se abre una lista.
const DefaultPageSize = 10

// NearestPageSize ajusta un limit arbitrario al tamaño permitido más cercano.
// Un limit=7 no es un error: es un 5 redondeado. Debe ser monótona en want.
func NearestPageSize(want int) int {
	if want <= 0 {
		return DefaultPageSize
	}
	best := PageSizes[0]
	for _, size := range PageSizes {
		if size > want {
			break
		}
		best = size
	}
	return best
}

// FilterOps son los operadores de filtro que el cliente puede construir.
var FilterOps = []string{"=", "!=", ">", "<", ">=", "<=", "ILIKE", "IN", "NOT IN", "IS NULL", "IS NOT NULL"}

// FieldMeta describe un campo para quien tenga que dibujarlo.
type FieldMeta struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`  // el tipo tal cual lo declara el manifest
	Input       string   `json:"input"` // el control HTML que sugiere
	Label       string   `json:"label"`
	Help        string   `json:"help,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	VisibleIf   string   `json:"visible_if,omitempty"`
	RequiredIf  string   `json:"required_if,omitempty"`
	Options     []string `json:"options,omitempty"`
	Required    bool     `json:"required"`
	Readonly    bool     `json:"readonly,omitempty"`
	Computed    bool     `json:"computed,omitempty"`
	// Searchable dice si el filtro rápido lo recorre. Un text largo no: buscar
	// "a" en unas notas no devuelve nada útil.
	Searchable bool `json:"searchable"`
	MaxLength  int  `json:"max_length,omitempty"`
	// Default es el valor que trae el manifest, para que el formulario pueda
	// prellenarlo y no deje a la persona en blanco si el servidor ya lo haría.
	Default        any    `json:"default,omitempty"`
	RelationModule string `json:"relation_module,omitempty"`
	RelationModel  string `json:"relation_model,omitempty"`
	RelationField  string `json:"relation_field,omitempty"`
}

// ModelMeta es todo lo que el cliente necesita para pintar un modelo sin que el
// módulo escriba UI.
type ModelMeta struct {
	Module    string        `json:"module"`
	Model     string        `json:"model"`
	Label     string        `json:"label"`
	Table     string        `json:"table"`
	Fields    []FieldMeta   `json:"fields"`
	Views     *ViewsDef     `json:"views"`
	PageSizes []int         `json:"page_sizes"`
	FilterOps []string      `json:"filter_ops"`
	Workflow  *WorkflowMeta `json:"workflow,omitempty"`
}

// TransitionMeta describe una transición para quien tenga que dibujar el botón:
// desde qué estados aplica, a cuál lleva y con qué etiqueta.
type TransitionMeta struct {
	From  []string `json:"from"`
	To    string   `json:"to"`
	Label string   `json:"label"`
}

// WorkflowMeta expone el flujo entero. Con esto el cliente decide por su cuenta
// qué botones mostrar en cada registro, sin pedir nada más al servidor que el
// estado actual — que ya viaja en el propio registro.
type WorkflowMeta struct {
	Field       string                     `json:"field"`
	Initial     string                     `json:"initial"`
	Transitions map[string]*TransitionMeta `json:"transitions"`
}

// Meta arma la metadata de un modelo: los campos tal como se declararon y las
// vistas, deducidas del esquema salvo que el módulo las haya declarado.
func (m *Manifest) Meta(modelName string) (*ModelMeta, error) {
	def := m.Models[modelName]
	if def == nil {
		return nil, errManifest("el módulo %q no declara el modelo %q", m.Name, modelName)
	}

	order := def.OrderedFields()

	fields := make([]FieldMeta, 0, len(order))
	for i := range order {
		f := &order[i]
		fields = append(fields, FieldMeta{
			Name:           f.Name,
			Type:           f.Type,
			Input:          inputControl(f),
			Label:          labelFor(f),
			Help:           f.Help,
			Placeholder:    f.Placeholder,
			VisibleIf:      f.VisibleIf,
			RequiredIf:     f.RequiredIf,
			Options:        f.Options,
			Required:       f.Required,
			Readonly:       f.Readonly,
			Computed:       f.Computed,
			Searchable:     isSearchable(f),
			MaxLength:      f.Length,
			Default:        f.Default,
			RelationModule: f.RelatedModule,
			RelationModel:  f.RelatedModel,
			RelationField:  f.RelatedField,
		})
	}

	return &ModelMeta{
		Module:    m.Name,
		Model:     modelName,
		Label:     labelForModel(def),
		Table:     m.TableName(modelName),
		Fields:    fields,
		Views:     inferViews(def, order),
		PageSizes: PageSizes,
		FilterOps: FilterOps,
		Workflow:  workflowMeta(def.Workflow),
	}, nil
}

// MustMeta es Meta para los sitios donde el modelo no puede faltar: tests y
// callers que ya lo resolvieron. Pánico en vez de un error que nadie mira.
func (m *Manifest) MustMeta(modelName string) *ModelMeta {
	mt, err := m.Meta(modelName)
	if err != nil {
		panic(err)
	}
	return mt
}

// workflowMeta traduce el flujo del manifest a su forma pública, poniendo la
// etiqueta legible cuando el módulo no escribió una.
func workflowMeta(wf *WorkflowDef) *WorkflowMeta {
	if wf == nil {
		return nil
	}
	transitions := make(map[string]*TransitionMeta, len(wf.Transitions))
	for action, def := range wf.Transitions {
		if def == nil {
			continue
		}
		label := def.Label
		if label == "" {
			label = humanize(action)
		}
		transitions[action] = &TransitionMeta{From: def.From, To: def.To, Label: label}
	}
	return &WorkflowMeta{Field: wf.Field, Initial: wf.Initial, Transitions: transitions}
}

// AvailableTransitions devuelve las acciones legales desde un estado.
//
// El cliente lo usa para pintar los botones, pero también lo usa el servidor al
// validar: que la lista de lo que se puede hacer salga de un solo sitio es lo que
// evita que el botón y la validación discrepen.
func (w *WorkflowMeta) AvailableTransitions(state string) map[string]*TransitionMeta {
	out := map[string]*TransitionMeta{}
	if w == nil {
		return out
	}
	for action, tr := range w.Transitions {
		if tr == nil {
			continue
		}
		for _, from := range tr.From {
			if from == state {
				out[action] = tr
				// Sin return: un estado puede tener varias salidas. Pedir una
				// transición y cortar aquí dejaba el resto de botones fuera.
				break
			}
		}
	}
	return out
}

// TransitionNames devuelve las acciones de un estado, ordenadas. El mapa de Go
// no tiene orden, y un conjunto de botones que cambia de sitio entre dos
// peticiones se nota.
func (w *WorkflowMeta) TransitionNames(state string) []string {
	available := w.AvailableTransitions(state)
	out := make([]string, 0, len(available))
	for action := range available {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

// inferViews deduce las vistas del esquema. Lo que el módulo declaró en views
// gana; el resto se completa solo.
func inferViews(def *ModelDef, order []FieldDef) *ViewsDef {
	views := &ViewsDef{}
	if def.Views != nil {
		*views = *def.Views
	}
	if views.List == nil {
		views.List = &ListView{Columns: inferColumns(order)}
	}
	if views.Form == nil {
		views.Form = inferForm(def, order)
	}
	if views.Card == nil {
		views.Card = inferCard(order)
	}
	if views.Kanban == nil {
		views.Kanban = inferKanban(def, order)
	}
	if views.Default == "" {
		views.Default = "list"
	}
	return views
}

// inferColumns elige las columnas de la tabla: los campos cortos, en el orden
// del manifest. Los "text" quedan fuera — no caben en una celda.
func inferColumns(order []FieldDef) []string {
	columns := make([]string, 0, maxInferredColumns)
	for i := range order {
		f := &order[i]
		if isLongText(f.Type) {
			continue
		}
		columns = append(columns, f.Name)
		if len(columns) == maxInferredColumns {
			break
		}
	}
	// Un modelo hecho solo de campos largos: mejor mostrar algo que nada.
	if len(columns) == 0 && len(order) > 0 {
		n := len(order)
		if n > maxInferredColumns {
			n = maxInferredColumns
		}
		columns = make([]string, 0, n)
		for i := 0; i < n; i++ {
			columns = append(columns, order[i].Name)
		}
	}
	return columns
}

// inferForm arma el formulario con TODOS los campos editables, en orden de
// sequence.
//
// A diferencia de la tabla, aquí no se recorta: si el módulo declaró el campo,
// la persona tiene que poder llenarlo. Un formulario escrito a mano es
// exactamente donde se pierden campos cuando el esquema crece.
//
// El campo de estado del workflow queda fuera: Create y Update lo rechazan si
// llega en el payload, así que pintar un <select> editable para él sería
// prometer algo que el servidor no permite. Se mueve con los botones de
// transición, que es lo que sí funciona.
func inferForm(def *ModelDef, order []FieldDef) *FormView {
	fields := make([]string, 0, len(order))
	for i := range order {
		f := &order[i]
		if f.Readonly || f.Computed {
			continue
		}
		if def.Workflow != nil && f.Name == def.Workflow.Field {
			continue
		}
		fields = append(fields, f.Name)
	}
	return &FormView{Fields: fields}
}

// inferCard usa el campo identificador como título y el siguiente texto corto
// como subtítulo. El primer campo con opciones sirve de distintivo.
func inferCard(order []FieldDef) *CardView {
	title := identifierField(order)
	if title == "" {
		return nil
	}
	card := &CardView{Title: title}
	for i := range order {
		f := &order[i]
		if f.Name == title || !isSearchable(f) || isLongText(f.Type) {
			continue
		}
		card.Subtitle = f.Name
		break
	}
	for i := range order {
		if len(order[i].Options) > 0 {
			card.Badge = order[i].Name
			break
		}
	}
	return card
}

// inferKanban solo se ofrece si hay un campo con options: así las columnas se
// conocen de antemano y su conteo es real, no el de la página que se esté
// mostrando.
//
// Si el modelo tiene workflow, el kanban es exactamente el tablero del flujo,
// agrupado por su campo de estado. Es el mismo campo que ya trae options —la
// validación del workflow lo exige—, así que no hace falta configurarlo dos
// veces para que coincidan.
func inferKanban(def *ModelDef, order []FieldDef) *KanbanView {
	title := identifierField(order)
	if title == "" {
		return nil
	}

	groupBy := ""
	if def.Workflow != nil {
		groupBy = def.Workflow.Field
	} else {
		for i := range order {
			if len(order[i].Options) > 0 {
				groupBy = order[i].Name
				break
			}
		}
	}
	if groupBy == "" {
		return nil
	}

	kanban := &KanbanView{GroupBy: groupBy, Title: title}
	for i := range order {
		f := &order[i]
		if f.Name != title && f.Name != groupBy && isSearchable(f) {
			kanban.Subtitle = f.Name
			break
		}
	}
	return kanban
}

// identifierField busca el campo que identifica al registro: el primero que sea
// obligatorio y de texto; si no hay, el primero de texto.
func identifierField(order []FieldDef) string {
	for i := range order {
		if order[i].Required && isSearchable(&order[i]) {
			return order[i].Name
		}
	}
	for i := range order {
		if isSearchable(&order[i]) {
			return order[i].Name
		}
	}
	if len(order) > 0 {
		return order[0].Name
	}
	return ""
}

// inputControl sugiere el control HTML según el tipo declarado.
func inputControl(f *FieldDef) string {
	// Un campo con options es un desplegable, diga el tipo que diga. Es lo que
	// hace que "estado" sea un select aunque su tipo sea string.
	if len(f.Options) > 0 {
		return "select"
	}
	switch strings.ToLower(strings.TrimSpace(f.Type)) {
	case "text", "json", "jsonb":
		return "textarea"
	case "many2one", "m2o", "uuid":
		return "select"
	case "integer", "int", "bigint", "decimal", "numeric", "money", "float", "double":
		return "number"
	case "boolean", "bool":
		return "checkbox"
	case "date":
		return "date"
	case "datetime", "timestamp":
		return "datetime-local"
	case "email":
		return "email"
	case "phone":
		return "tel"
	case "url":
		return "url"
	default:
		return "text"
	}
}

// isSearchable dice si el filtro rápido debe recorrer el campo. Un text largo
// no: buscar "a" entre unas notas no devuelve nada útil.
func isSearchable(f *FieldDef) bool {
	if f.Readonly {
		// Un campo calculado no se busca: no es un dato de entrada.
		return false
	}
	if isLongText(f.Type) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(f.Type)) {
	case "string", "email", "phone", "url", "enum", "selection", "char":
		return true
	default:
		return false
	}
}

func isLongText(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "text", "json", "jsonb":
		return true
	}
	return false
}

func labelFor(f *FieldDef) string {
	if f.Label != "" {
		return f.Label
	}
	return humanize(f.Name)
}

func labelForModel(def *ModelDef) string {
	if def.Label != "" {
		return def.Label
	}
	return humanize(def.Name)
}

func humanize(name string) string {
	words := strings.ReplaceAll(name, "_", " ")
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}
