package sdk

import "strings"

// ViewsDef permite que un módulo sobrescriba las vistas deducidas.
// Es opcional: si no viene, el motor las infiere del propio esquema.
type ViewsDef struct {
	Default string      `json:"default,omitempty"`
	List    *ListView   `json:"list,omitempty"`
	Form    *FormView   `json:"form,omitempty"`
	Card    *CardView   `json:"card,omitempty"`
	Kanban  *KanbanView `json:"kanban,omitempty"`
}

type ListView struct {
	Columns []string `json:"columns"`
}

// FormView describe el formulario de alta y edición.
type FormView struct {
	Fields []string `json:"fields"`
}

type CardView struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Badge    string `json:"badge,omitempty"`
}

type KanbanView struct {
	GroupBy  string `json:"group_by"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

// FieldMeta describe un campo para quien tenga que dibujarlo.
type FieldMeta struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`  // tipo del manifest
	Input         string   `json:"input"` // control HTML sugerido
	Label         string   `json:"label"` // encabezado de columna / etiqueta
	Help          string   `json:"help,omitempty"`
	Placeholder   string   `json:"placeholder,omitempty"`
	VisibleIf     string   `json:"visibleIf,omitempty"`
	RequiredIf    string   `json:"requiredIf,omitempty"`
	Options       []string `json:"options,omitempty"`
	Required      bool     `json:"required"`
	Readonly      bool     `json:"readonly,omitempty"`
	Computed      bool     `json:"computed,omitempty"`
	Searchable    bool     `json:"searchable"` // admite búsqueda por columna
	MaxLength     int      `json:"max_length,omitempty"`
	RelationModule string   `json:"relation_module,omitempty"`
	RelationModel  string   `json:"relation_model,omitempty"`
	RelationField  string   `json:"relation_field,omitempty"`
}

// ModelMeta es todo lo que el motor de vistas necesita para renderizar un
// modelo sin que el módulo escriba UI.
type ModelMeta struct {
	Module    string        `json:"module"`
	Model     string        `json:"model"`
	Label     string        `json:"label"`
	Fields    []FieldMeta   `json:"fields"` // en el orden del manifest
	Views     ViewsDef      `json:"views"`
	PageSizes []int         `json:"page_sizes"`
	FilterOps []string      `json:"filter_ops"`
	Workflow  *WorkflowMeta `json:"workflow,omitempty"`
}

// TransitionMeta describe una transición para quien tenga que dibujar el
// botón: desde qué estados aplica, a cuál lleva y con qué etiqueta.
type TransitionMeta struct {
	From  []string `json:"from"`
	To    string   `json:"to"`
	Label string   `json:"label"`
}

// WorkflowMeta expone el flujo completo: con esto el cliente decide solo, por
// registro, qué botones de transición mostrar — sin pedirle nada más al
// servidor que el estado actual, que ya viaja en el propio registro.
type WorkflowMeta struct {
	Field       string                     `json:"field"`
	Initial     string                     `json:"initial"`
	Transitions map[string]*TransitionMeta `json:"transitions"`
}

// maxInferredColumns limita las columnas deducidas: una tabla con 19 columnas
// no se lee. Si el módulo quiere más, las declara en views.list.
const maxInferredColumns = 6

// Meta arma la metadata del modelo: los campos tal como se declararon y las
// vistas, deducidas del esquema salvo que el módulo las haya declarado.
func (s *ModuleSDK) Meta(modelName string) (*ModelMeta, error) {
	model, err := s.model(modelName)
	if err != nil {
		return nil, err
	}

	order := model.OrderedFields()

	fields := make([]FieldMeta, 0, len(order))
	for _, name := range order {
		def := model.Fields[name]
		fields = append(fields, FieldMeta{
			Name:         name,
			Type:         def.Type,
			Input:        inputControl(def),
			Label:        labelFor(name, def),
			Help:         def.Help,
			Placeholder:  def.Placeholder,
			VisibleIf:    def.VisibleIf,
			RequiredIf:   def.RequiredIf,
			Options:      def.Options,
			Required:     def.Required,
			Readonly:     def.Readonly,
			Computed:     def.Computed,
			Searchable:   def.IsSearchable(),
			MaxLength:    def.Length,
			RelationModule: def.RelatedModule,
			RelationModel:  def.RelatedModel,
			RelationField:  def.RelatedField,
		})
	}

	ops := make([]string, 0, len(filterOps))
	for op := range filterOps {
		ops = append(ops, op)
	}

	return &ModelMeta{
		Module:    s.ModuleID,
		Model:     modelName,
		Label:     labelForModel(modelName, model),
		Fields:    fields,
		Views:     inferViews(model, order),
		PageSizes: PageSizes,
		FilterOps: ops,
		Workflow:  workflowMeta(model.Workflow),
	}, nil
}

// workflowMeta traduce el flujo del manifest a su forma pública, agregando la
// etiqueta legible cuando el módulo no puso una explícita.
func workflowMeta(wf *WorkflowDef) *WorkflowMeta {
	if wf == nil {
		return nil
	}

	transitions := make(map[string]*TransitionMeta, len(wf.Transitions))
	for action, def := range wf.Transitions {
		label := def.Label
		if label == "" {
			label = humanize(action)
		}
		transitions[action] = &TransitionMeta{From: def.From, To: def.To, Label: label}
	}

	return &WorkflowMeta{Field: wf.Field, Initial: wf.Initial, Transitions: transitions}
}

// inferViews deduce las vistas del esquema. Lo que el módulo haya declarado
// en views gana; el resto se completa solo.
func inferViews(model *ModelDef, order []string) ViewsDef {
	views := ViewsDef{}
	if model.Views != nil {
		views = *model.Views
	}

	if views.List == nil {
		views.List = &ListView{Columns: inferColumns(model, order)}
	}
	if views.Form == nil {
		views.Form = inferForm(model, order)
	}
	if views.Card == nil {
		views.Card = inferCard(model, order)
	}
	if views.Kanban == nil {
		views.Kanban = inferKanban(model, order) // nil si nada es agrupable
	}
	if views.Default == "" {
		views.Default = "list"
	}
	return views
}

// inferColumns elige las columnas de la tabla: los campos cortos, en el orden
// del manifest. Los "text" quedan fuera — no caben en una celda.
func inferColumns(model *ModelDef, order []string) []string {
	var columns []string
	for _, name := range order {
		def := model.Fields[name]
		if strings.EqualFold(def.Type, "text") || def.Type == "json" {
			continue
		}
		columns = append(columns, name)
		if len(columns) == maxInferredColumns {
			break
		}
	}

	// Modelo hecho sólo de campos largos: mejor mostrar algo que nada.
	if len(columns) == 0 && len(order) > 0 {
		columns = order[:min(len(order), maxInferredColumns)]
	}
	return columns
}

// inferForm arma el formulario con TODOS los campos editables del manifest, en
// orden de sequence. A diferencia de la tabla no se recorta: si el módulo
// declaró el campo, la persona tiene que poder llenarlo. Un formulario escrito
// a mano es justo donde se pierden campos cuando el esquema crece.
//
// El campo de estado del workflow queda fuera: Create/Update lo rechazan si
// llega en el payload, así que mostrar un <select> editable para él sería
// prometer algo que el servidor no permite. Se mueve, en cambio, con los
// botones de transición.
func inferForm(model *ModelDef, order []string) *FormView {
	fields := make([]string, 0, len(order))
	for _, name := range order {
		if model.Fields[name].Readonly {
			continue
		}
		if model.Workflow != nil && name == model.Workflow.Field {
			continue
		}
		fields = append(fields, name)
	}
	return &FormView{Fields: fields}
}

// inferCard usa el campo identificador como título y el siguiente texto corto
// como subtítulo.
func inferCard(model *ModelDef, order []string) *CardView {
	title := identifierField(model, order)
	if title == "" {
		return nil
	}

	card := &CardView{Title: title}
	for _, name := range order {
		def := model.Fields[name]
		if name == title || !def.IsSearchable() || strings.EqualFold(def.Type, "text") {
			continue
		}
		card.Subtitle = name
		break
	}

	// El primer campo con opciones sirve de distintivo.
	for _, name := range order {
		if len(model.Fields[name].Options) > 0 {
			card.Badge = name
			break
		}
	}
	return card
}

// inferKanban sólo ofrece kanban si hay un campo con opciones declaradas: así
// las columnas se conocen de antemano y su conteo es real, no el de la página
// que se esté mostrando.
//
// Si el modelo tiene un workflow, kanban se vuelve exactamente eso: el
// tablero del flujo, agrupado por su campo de estado. Es el mismo campo que
// ya trae "options" (la validación del workflow lo exige), así que no hace
// falta ninguna configuración extra para que coincidan.
func inferKanban(model *ModelDef, order []string) *KanbanView {
	title := identifierField(model, order)
	if title == "" {
		return nil
	}

	groupBy := ""
	if model.Workflow != nil {
		groupBy = model.Workflow.Field
	} else {
		for _, name := range order {
			if len(model.Fields[name].Options) > 0 {
				groupBy = name
				break
			}
		}
	}
	if groupBy == "" {
		return nil
	}

	kanban := &KanbanView{GroupBy: groupBy, Title: title}
	for _, candidate := range order {
		def := model.Fields[candidate]
		if candidate != title && candidate != groupBy && def.IsSearchable() {
			kanban.Subtitle = candidate
			break
		}
	}
	return kanban
}

// identifierField busca el campo que identifica al registro: el primero que
// sea obligatorio y de texto; si no hay, el primero de texto.
func identifierField(model *ModelDef, order []string) string {
	for _, name := range order {
		def := model.Fields[name]
		if def.Required && def.IsSearchable() {
			return name
		}
	}
	for _, name := range order {
		if model.Fields[name].IsSearchable() {
			return name
		}
	}
	if len(order) > 0 {
		return order[0]
	}
	return ""
}

// inputControl sugiere el control HTML según el tipo declarado.
func inputControl(def *FieldDef) string {
	if len(def.Options) > 0 {
		return "select"
	}
	switch strings.ToLower(def.Type) {
	case "text", "json":
		return "textarea"
	case "many2one":
		return "select"
	case "one2many", "many2many":
		return "multiselect"
	case "integer", "int", "bigint", "decimal", "money", "float", "numeric":
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

// labelFor usa la etiqueta del manifest, o convierte el nombre del campo en
// algo legible: tax_id_type → "Tax id type".
func labelFor(name string, def *FieldDef) string {
	if def.Label != "" {
		return def.Label
	}
	return humanize(name)
}

func labelForModel(name string, model *ModelDef) string {
	if model.Label != "" {
		return model.Label
	}
	return humanize(name)
}

func humanize(name string) string {
	words := strings.ReplaceAll(name, "_", " ")
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
