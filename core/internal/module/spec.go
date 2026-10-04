package module

import (
	"fmt"
	"strings"
)

// FieldDef es un campo declarado en el manifest de un módulo.
//
// El manifest es la única fuente de verdad del esquema: no hay SQL en los
// módulos, el core crea y reconcilia las tablas a partir de aquí. Por eso
// Default es `any` y no una cadena — un default escrito a mano se convierte en
// DDL concatenado, y el DDL se ejecuta fuera de RLS. Al materializar la tabla,
// sqlLiteral() solo admite literales que se pueden citar de forma segura.
type FieldDef struct {
	// Name lo rellena la clave del mapa (forma antigua) o el propio campo
	// "name:" (forma de lista). FieldsList se encarga de las dos.
	Name  string `json:"name" yaml:"name"`
	Type  string `json:"type" yaml:"type"`
	Label string `json:"label" yaml:"label,omitempty"`

	Required bool `json:"required" yaml:"required,omitempty"`

	// Dimensiones. Length es el tamaño del VARCHAR; Precision y Scale, los de
	// un NUMERIC.
	Length    int `json:"length,omitempty" yaml:"length,omitempty"`
	Precision int `json:"precision,omitempty" yaml:"precision,omitempty"`
	Scale     int `json:"scale,omitempty" yaml:"scale,omitempty"`

	// Default se valida al crear la tabla, no al leer el manifest. Un valor que
	// no sea un literal admisible se descarta con un aviso: la columna queda sin
	// default, que es visible, en vez de inyectar DDL.
	Default any `json:"default,omitempty" yaml:"default,omitempty"`

	Index  bool `json:"index,omitempty" yaml:"index,omitempty"`
	Unique bool `json:"unique,omitempty" yaml:"unique,omitempty"`

	// Metadatos de presentación. El motor de vistas los usa para decidir el
	// control HTML, el encabezado de columna y el orden.
	Help        string   `json:"help,omitempty" yaml:"help,omitempty"`
	Placeholder string   `json:"placeholder,omitempty" yaml:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty" yaml:"options,omitempty"`
	Readonly    bool     `json:"readonly,omitempty" yaml:"readonly,omitempty"`
	Computed    bool     `json:"computed,omitempty" yaml:"computed,omitempty"`
	VisibleIf   string   `json:"visibleIf,omitempty" yaml:"visibleIf,omitempty"`
	RequiredIf  string   `json:"requiredIf,omitempty" yaml:"requiredIf,omitempty"`
	Example     any      `json:"example,omitempty" yaml:"example,omitempty"`

	// Relaciones. many2one es una columna UUID: sin RelatedField la API
	// devuelve el UUID crudo en vez del dato de la fila relacionada.
	RelatedModule string `json:"related_module,omitempty" yaml:"related_module,omitempty"`
	RelatedModel  string `json:"related_model,omitempty" yaml:"related_model,omitempty"`
	RelatedField  string `json:"related_field,omitempty" yaml:"related_field,omitempty"`

	// Sequence ordena columnas y formulario. Convención 10, 20, 30… Un campo sin
	// sequence recibe uno implícito según su posición en el manifest, así que
	// añadir "sequence: 5" lo manda al frente sin renumerar los demás.
	Sequence int `json:"sequence,omitempty" yaml:"sequence,omitempty"`
}

// WorkflowDef convierte un campo de opciones en una máquina de estados.
//
// El campo de "field" debe existir y traer "options": los mismos valores que ya
// alimentan el <select> del formulario pasan a ser los estados legales. Desde
// que se declara, ese campo deja de aceptarse en un PUT normal y solo cambia
// por una transición válida, para que el historial refleje el camino real.
type WorkflowDef struct {
	Field       string                    `json:"field" yaml:"field"`
	Initial     string                    `json:"initial" yaml:"initial"`
	Transitions map[string]*TransitionDef `json:"transitions" yaml:"transitions"`
}

// TransitionDef declara una acción legal de la máquina de estados.
type TransitionDef struct {
	From  []string `json:"from" yaml:"from"`
	To    string   `json:"to" yaml:"to"`
	Label string   `json:"label,omitempty" yaml:"label,omitempty"`
}

// ViewsDef permite al módulo sobrescribir las vistas que el motor deduciría.
// Todo es opcional: lo que falte se infiere del esquema.
type ViewsDef struct {
	Default string      `json:"default,omitempty" yaml:"default,omitempty"`
	List    *ListView   `json:"list,omitempty" yaml:"list,omitempty"`
	Form    *FormView   `json:"form,omitempty" yaml:"form,omitempty"`
	Card    *CardView   `json:"card,omitempty" yaml:"card,omitempty"`
	Kanban  *KanbanView `json:"kanban,omitempty" yaml:"kanban,omitempty"`
}

type ListView struct {
	Columns []string `json:"columns" yaml:"columns"`
}

type FormView struct {
	Fields []string `json:"fields" yaml:"fields"`
}

type CardView struct {
	Title    string `json:"title" yaml:"title"`
	Subtitle string `json:"subtitle,omitempty" yaml:"subtitle,omitempty"`
	Badge    string `json:"badge,omitempty" yaml:"badge,omitempty"`
}

type KanbanView struct {
	GroupBy  string `json:"group_by" yaml:"group_by"`
	Title    string `json:"title" yaml:"title"`
	Subtitle string `json:"subtitle,omitempty" yaml:"subtitle,omitempty"`
}

// MenuDef es una entrada del menú lateral.
type MenuDef struct {
	Label  string `json:"label" yaml:"label"`
	Icon   string `json:"icon,omitempty" yaml:"icon,omitempty"`
	Route  string `json:"route,omitempty" yaml:"route,omitempty"`
	Parent string `json:"parent,omitempty" yaml:"parent,omitempty"`
	Seq    int    `json:"seq,omitempty" yaml:"seq,omitempty"`
	// Model es "módulo/modelo" y abre esa lista con el CRUD deducido del
	// esquema. Es opcional: una entrada puede apuntar sólo a una ruta y que la
	// página la sirva el frontend del módulo.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
}

// FrontendDef describe las páginas propias del módulo, cuando las tiene.
type FrontendDef struct {
	Entry string    `json:"entry,omitempty" yaml:"entry,omitempty"`
	Pages []PageDef `json:"pages,omitempty" yaml:"pages,omitempty"`
}

type PageDef struct {
	Name  string `json:"name" yaml:"name"`
	Path  string `json:"path" yaml:"path"`
	Label string `json:"label" yaml:"label"`
	Icon  string `json:"icon,omitempty" yaml:"icon,omitempty"`
}

// ModelDef es una tabla del módulo.
type ModelDef struct {
	Name   string     `json:"name" yaml:"-"`
	Label  string     `json:"label,omitempty" yaml:"label,omitempty"`
	Fields FieldsList `json:"fields" yaml:"fields"`

	Views    *ViewsDef    `json:"views,omitempty" yaml:"views,omitempty"`
	Workflow *WorkflowDef `json:"workflow,omitempty" yaml:"workflow,omitempty"`

	// FieldOrder conserva el orden de declaración del manifest. Sin esto, las
	// columnas y el formulario saldrían en el orden que imponga el mapa, que
	// no es el que escribió quien hizo el módulo.
	FieldOrder []string `json:"-" yaml:"-"`
}

// FieldByName devuelve el campo con ese nombre, o nil.
func (m *ModelDef) FieldByName(name string) *FieldDef {
	for i := range m.Fields {
		if m.Fields[i].Name == name {
			return &m.Fields[i]
		}
	}
	return nil
}

// OrderedFields devuelve los campos en el orden de presentación: por sequence,
// y a igualdad de sequence en el orden en que se declararon.
//
// Es lo que ven las columnas de la tabla y el formulario, así que el orden no es
// decorativo: quien escribe sequence: 25 está diciendo "este campo va entre el
// primero y el segundo".
func (m *ModelDef) OrderedFields() []FieldDef { return m.orderedFields() }

// orderedFields es la implementación; se usa internamente donde ya se tiene el
// puntero al modelo.
func (m *ModelDef) orderedFields() []FieldDef {
	out := make([]FieldDef, len(m.Fields))
	copy(out, m.Fields)
	for i := range out {
		if out[i].Sequence == 0 {
			out[i].Sequence = (i + 1) * sequenceStep
		}
	}
	// Orden estable: a igual sequence manda el orden de declaración, que es lo
	// que resolvió los empates arriba.
	stableSortBy(out, func(a, b FieldDef) bool { return a.Sequence < b.Sequence })
	return out
}

// sequenceStep es el espacio entre campos consecutivos, para poder intercalar
// sin renumerar el resto.
const sequenceStep = 10

// stableSortBy es sort.SliceStable sin el slice de reflectores: son como mucho
// unas decenas de campos y se llama en cada arranque de módulo.
func stableSortBy[T any](items []T, less func(a, b T) bool) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && less(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// validate comprueba que el modelo sea construible. Se ejecuta al cargar el
// manifest, antes de tocar la base: un manifest con un error debe no llegar a
// generar DDL.
func (m *ModelDef) validate(moduleName string) error {
	if m.Name == "" {
		return fmt.Errorf("modelo sin nombre en el módulo %q", moduleName)
	}
	if !safeIdentRe.MatchString(m.Name) {
		return fmt.Errorf("nombre de modelo inválido %q en %q: debe empezar por letra minúscula y solo llevar minúsculas, dígitos y _", m.Name, moduleName)
	}
	if len(m.Fields) == 0 {
		return fmt.Errorf("el modelo %q no declara campos", m.Name)
	}

	seen := make(map[string]bool, len(m.Fields))
	for i := range m.Fields {
		f := &m.Fields[i]
		if f.Name == "" {
			return fmt.Errorf("el modelo %q tiene un campo sin nombre", m.Name)
		}
		if f.Name == "tenant_id" {
			// El core lo pone. Declararlo aquí rompería el aislamiento.
			return fmt.Errorf("el modelo %q declara tenant_id, que lo pone el core", m.Name)
		}
		if !safeIdentRe.MatchString(f.Name) {
			return fmt.Errorf("nombre de campo inválido %q en el modelo %q", f.Name, m.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("el modelo %q declara dos veces el campo %q", m.Name, f.Name)
		}
		seen[f.Name] = true

		if strings.TrimSpace(f.Type) == "" {
			return fmt.Errorf("el campo %q del modelo %q no declara tipo", f.Name, m.Name)
		}
		if strings.EqualFold(f.Type, "many2one") {
			if f.RelatedModel == "" {
				return fmt.Errorf("el campo %q es many2one pero no declara related_model", f.Name)
			}
			if !safeIdentRe.MatchString(f.RelatedModel) {
				return fmt.Errorf("related_model inválido %q en el campo %q", f.RelatedModel, f.Name)
			}
		}
	}

	if m.Workflow != nil {
		if err := m.validateWorkflow(); err != nil {
			return err
		}
	}
	return nil
}

// validateWorkflow comprueba que la máquina de estados sea coherente con los
// campos: que el campo exista, tenga options, y que toda transición hable de
// estados declarados. Una transición a un estado inexistente deja el registro
// en un valor que el formulario no sabe dibujar.
func (m *ModelDef) validateWorkflow() error {
	wf := m.Workflow
	def := m.FieldByName(wf.Field)
	if def == nil {
		return fmt.Errorf("el workflow del modelo %q apunta al campo %q, que no existe", m.Name, wf.Field)
	}
	if len(def.Options) == 0 {
		return fmt.Errorf("el workflow usa el campo %q del modelo %q, pero ese campo no declara options", wf.Field, m.Name)
	}
	if wf.Initial != "" && !containsString(def.Options, wf.Initial) {
		return fmt.Errorf("el estado inicial %q del modelo %q no está entre las options de %q", wf.Initial, m.Name, wf.Field)
	}
	if len(wf.Transitions) == 0 {
		return fmt.Errorf("el workflow del modelo %q no declara transiciones", m.Name)
	}

	for action, tr := range wf.Transitions {
		if tr == nil {
			return fmt.Errorf("la transición %q del modelo %q está vacía", action, m.Name)
		}
		if !safeIdentRe.MatchString(action) {
			return fmt.Errorf("acción de transición inválida %q en el modelo %q", action, m.Name)
		}
		if tr.To == "" {
			return fmt.Errorf("la transición %q del modelo %q no declara to", action, m.Name)
		}
		if !containsString(def.Options, tr.To) {
			return fmt.Errorf("la transición %q del modelo %q lleva a %q, que no está entre las options de %q", action, m.Name, tr.To, wf.Field)
		}
		if len(tr.From) == 0 {
			return fmt.Errorf("la transición %q del modelo %q no declara from", action, m.Name)
		}
		for _, from := range tr.From {
			if !containsString(def.Options, from) {
				return fmt.Errorf("la transición %q del modelo %q sale de %q, que no está entre las options de %q", action, m.Name, from, wf.Field)
			}
		}
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
