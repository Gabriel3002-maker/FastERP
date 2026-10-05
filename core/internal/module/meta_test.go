package module

import (
	"strings"
	"testing"
)

func cargar(t *testing.T, yaml string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(yaml), "t/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

// Sin bloque "views", el motor deduce las tres vistas del propio esquema. Es lo
// que hace que añadir un campo no exija tocar la interfaz.
func TestMetaDeduceLasVistasSinConfiguracion(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    label: Oportunidad
    fields:
      - {name: title, type: string, required: true, label: Título, sequence: 10}
      - {name: contact, type: string, sequence: 20}
      - {name: stage, type: string, options: [NUEVO, GANADO], sequence: 30}
      - {name: notes, type: text, sequence: 40}
      - {name: amount, type: money, sequence: 50}
`)

	meta, err := m.Meta("lead")
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if meta.Label != "Oportunidad" {
		t.Errorf("Label = %q", meta.Label)
	}
	if meta.Table != "mod_crm_lead" {
		t.Errorf("Table = %q", meta.Table)
	}

	// La tabla deja fuera el text: no cabe en una celda.
	cols := strings.Join(meta.Views.List.Columns, ",")
	if strings.Contains(cols, "notes") {
		t.Errorf("la lista no debería traer el campo text: %s", cols)
	}
	if !strings.Contains(cols, "title") || !strings.Contains(cols, "stage") {
		t.Errorf("faltan columnas en la lista: %s", cols)
	}

	// El formulario sí trae todos los editables, incluido el text.
	form := strings.Join(meta.Views.Form.Fields, ",")
	if !strings.Contains(form, "notes") {
		t.Errorf("el formulario debe traer el text: %s", form)
	}
	if meta.Views.Default != "list" {
		t.Errorf("Default = %q, se esperaba list", meta.Views.Default)
	}
}

func TestMetaEligeElControlDeCadaTipo(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: nombre, type: string, sequence: 10}
      - {name: email, type: email, sequence: 20}
      - {name: tel, type: phone, sequence: 30}
      - {name: web, type: url, sequence: 40}
      - {name: notas, type: text, sequence: 50}
      - {name: n, type: integer, sequence: 60}
      - {name: ok, type: boolean, sequence: 70}
      - {name: dia, type: date, sequence: 80}
      - {name: momento, type: datetime, sequence: 90}
      - {name: meta, type: json, sequence: 100}
      - {name: sector, type: enum, options: [a, b], sequence: 110}
`)

	meta, _ := m.Meta("lead")
	want := map[string]string{
		"nombre": "text", "email": "email", "tel": "tel", "web": "url",
		"notas": "textarea", "n": "number", "ok": "checkbox",
		"dia": "date", "momento": "datetime-local", "meta": "textarea",
		"sector": "select",
	}
	for _, f := range meta.Fields {
		if got := f.Input; got != want[f.Name] {
			t.Errorf("%s: input = %q, se esperaba %q", f.Name, got, want[f.Name])
		}
	}
}

// Un campo con options es un desplegable diga lo que diga su tipo, y su valor
// por defecto tiene que estar entre ellas.
func TestMetaUnCampoConOptionsEsSelect(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: stage, type: string, options: [NUEVO, GANADO, PERDIDO], default: NUEVO, sequence: 10}
`)
	meta, _ := m.Meta("lead")
	if meta.Fields[0].Input != "select" {
		t.Errorf("Input = %q", meta.Fields[0].Input)
	}
	if len(meta.Fields[0].Options) != 3 {
		t.Errorf("Options = %v", meta.Fields[0].Options)
	}
	if meta.Fields[0].Default != "NUEVO" {
		t.Errorf("Default = %v; el formulario necesita saberlo para prellenarlo", meta.Fields[0].Default)
	}
}

// El campo de estado del workflow no aparece en el formulario: el servidor lo
// rechaza en un PUT normal, así que pintarlo sería prometer algo que no pasa.
func TestMetaSacaElEstadoDelWorkflowDelFormulario(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: title, type: string, required: true, sequence: 10}
      - {name: stage, type: enum, options: [nuevo, ganado], sequence: 20}
      - {name: notas, type: text, sequence: 30}
    workflow:
      field: stage
      initial: nuevo
      transitions:
        ganar: {from: [nuevo], to: ganado, label: Ganar}
`)
	meta, _ := m.Meta("lead")

	form := strings.Join(meta.Views.Form.Fields, ",")
	if strings.Contains(form, "stage") {
		t.Errorf("el estado no debe estar en el formulario: %s", form)
	}
	if !strings.Contains(form, "notas") {
		t.Errorf("los demás campos sí deben estar: %s", form)
	}
	// Pero sí en la tabla, donde se ve en qué estado está cada cosa.
	if !strings.Contains(strings.Join(meta.Views.List.Columns, ","), "stage") {
		t.Error("el estado sí debería salir en la lista")
	}
}

// Con workflow, el kanban es el tablero del flujo, sin configuración extra: el
// campo de estado ya trae options porque la validación lo exige.
func TestMetaKanbanEsElFlujoCuandoHayWorkflow(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: title, type: string, required: true, sequence: 10}
      - {name: company, type: string, sequence: 20}
      - {name: stage, type: enum, options: [nuevo, ganado], sequence: 30}
    workflow:
      field: stage
      initial: nuevo
      transitions:
        ganar: {from: [nuevo], to: ganado, label: Ganar}
`)
	meta, _ := m.Meta("lead")

	if meta.Views.Kanban == nil {
		t.Fatal("debería deducirse un kanban")
	}
	if meta.Views.Kanban.GroupBy != "stage" {
		t.Errorf("GroupBy = %q, se esperaba el campo de estado", meta.Views.Kanban.GroupBy)
	}
	if meta.Views.Kanban.Title != "title" {
		t.Errorf("Title = %q", meta.Views.Kanban.Title)
	}
	if meta.Views.Kanban.Subtitle != "company" {
		t.Errorf("Subtitle = %q", meta.Views.Kanban.Subtitle)
	}
}

// Sin un campo agrupable no se ofrece kanban: es mejor no tener la vista que
// tener una que no significa nada.
func TestMetaNoOfreceKanbanSinCampoAgrupable(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: title, type: string, required: true, sequence: 10}
      - {name: amount, type: decimal, sequence: 20}
`)
	meta, _ := m.Meta("lead")
	if meta.Views.Kanban != nil {
		t.Errorf("no debería haber kanban: %+v", meta.Views.Kanban)
	}
}

// Lo que el módulo declara en views gana sobre lo deducido.
func TestMetaRespetaLasVistasDeclaradas(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: title, type: string, required: true, sequence: 10}
      - {name: company, type: string, sequence: 20}
      - {name: amount, type: decimal, sequence: 30}
      - {name: stage, type: enum, options: [nuevo, ganado], sequence: 40}
    views:
      default: kanban
      list:
        columns: [title, amount]
      card:
        title: company
        badge: stage
`)
	meta, _ := m.Meta("lead")

	if meta.Views.Default != "kanban" {
		t.Errorf("Default = %q", meta.Views.Default)
	}
	if strings.Join(meta.Views.List.Columns, ",") != "title,amount" {
		t.Errorf("List = %v", meta.Views.List.Columns)
	}
	if meta.Views.Card.Title != "company" || meta.Views.Card.Badge != "stage" {
		t.Errorf("Card = %+v", meta.Views.Card)
	}
	// El formulario sigue deduciéndose: declararlo es opcional.
	if meta.Views.Form == nil || len(meta.Views.Form.Fields) == 0 {
		t.Error("el formulario debería deducirse aunque no se declare")
	}
}

// La etiqueta de un campo es la del manifest, y si no la hay, el nombre
// separado en palabras: tax_id_type → "Tax id type".
func TestMetaDerivaLaEtiquetaDelNombre(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: tax_id_type, type: string, sequence: 10}
      - {name: nombre_completo, type: string, label: Nombre completo, sequence: 20}
`)
	meta, _ := m.Meta("lead")

	if meta.Fields[0].Label != "Tax id type" {
		t.Errorf("Label = %q", meta.Fields[0].Label)
	}
	if meta.Fields[1].Label != "Nombre completo" {
		t.Errorf("Label = %q; la del manifest manda", meta.Fields[1].Label)
	}
}

// El texto largo no se busca: buscar "a" entre unas notas no devuelve nada útil.
func TestMetaSearchableExcluyeElTextoLargo(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: title, type: string, sequence: 10}
      - {name: notes, type: text, sequence: 20}
      - {name: meta, type: json, sequence: 30}
      - {name: amount, type: decimal, sequence: 40}
`)
	meta, _ := m.Meta("lead")

	for _, f := range meta.Fields {
		want := f.Name == "title"
		if f.Searchable != want {
			t.Errorf("%s: Searchable = %v, se esperaba %v", f.Name, f.Searchable, want)
		}
	}
}

// El motor tiene que saber qué botones pintar en cada estado, y la respuesta
// no puede cambiar entre dos peticiones: sale de un solo sitio.
func TestWorkflowMetaDigaQueSePuedeHacer(t *testing.T) {
	m := cargar(t, `name: crm
label: CRM
models:
  lead:
    fields:
      - {name: title, type: string, required: true, sequence: 10}
      - {name: stage, type: enum, options: [nuevo, ganado, perdido], sequence: 20}
    workflow:
      field: stage
      initial: nuevo
      transitions:
        ganar: {from: [nuevo], to: ganado, label: Ganar}
        perder: {from: [nuevo], to: perdido, label: Perder}
        reabrir: {from: [ganado, perdido], to: nuevo, label: Reabrir}
`)
	meta, _ := m.Meta("lead")
	wf := meta.Workflow
	if wf == nil {
		t.Fatal("no hay workflow")
	}
	if wf.Field != "stage" || wf.Initial != "nuevo" {
		t.Errorf("workflow = %+v", wf)
	}

	casos := []struct {
		estado string
		queda  []string
	}{
		{"nuevo", []string{"ganar", "perder"}},
		{"ganado", []string{"reabrir"}},
		{"perdido", []string{"reabrir"}},
		{"inventado", nil},
	}
	for _, c := range casos {
		got := wf.TransitionNames(c.estado)
		if strings.Join(got, ",") != strings.Join(c.queda, ",") {
			t.Errorf("desde %q: %v, se esperaba %v", c.estado, got, c.queda)
		}
	}

	// La etiqueta del botón sale del manifest, o se humaniza el nombre.
	if wf.Transitions["ganar"].Label != "Ganar" {
		t.Errorf("Label = %q", wf.Transitions["ganar"].Label)
	}
}

// La lista se recorta, el formulario no. Es deliberado: una tabla de 19
// columnas no se lee, pero un campo declarado tiene que poder llenarse.
func TestLaListaSeRecortaYElFormularioNo(t *testing.T) {
	var campos []string
	for i := 1; i <= 12; i++ {
		campos = append(campos, "      - {name: c"+itoa(i)+", type: string, sequence: "+itoa(i*10)+"}")
	}
	m := cargar(t, "name: crm\nlabel: CRM\nmodels:\n  lead:\n    fields:\n"+strings.Join(campos, "\n")+"\n")
	meta, _ := m.Meta("lead")

	if len(meta.Views.List.Columns) != maxInferredColumns {
		t.Errorf("la lista trae %d columnas, se esperaban %d", len(meta.Views.List.Columns), maxInferredColumns)
	}
	if len(meta.Views.Form.Fields) != 12 {
		t.Errorf("el formulario trae %d campos, se esperaban 12: un campo declarado tiene que poder llenarse",
			len(meta.Views.Form.Fields))
	}
}

func TestMetaDeUnModeloInexistenteFalla(t *testing.T) {
	m := cargar(t, "name: crm\nlabel: CRM\nmodels:\n  lead:\n    fields:\n      - {name: a, type: string}\n")
	if _, err := m.Meta("no_existe"); err == nil {
		t.Error("pedir la meta de un modelo inexistente debería fallar")
	}
}

// Un limit que no está en la lista blanca se ajusta al tamaño más cercano, no
// se rechaza: limit=5 tiene que devolver 5 filas y no las 10 del mínimo viejo.
func TestNearestPageSizeAjustaAlPermitido(t *testing.T) {
	casos := []struct{ want, got int }{
		{5, 5},
		{7, 5},
		{10, 10},
		{20, 10},
		{25, 25},
		{30, 25},
		{1000, 100},
		{0, DefaultPageSize},
		{-3, DefaultPageSize},
	}
	for _, c := range casos {
		if got := NearestPageSize(c.want); got != c.got {
			t.Errorf("NearestPageSize(%d) = %d, se esperaba %d", c.want, got, c.got)
		}
	}
}

// El tamaño por defecto tiene que estar en la lista: si no, el cliente abre
// pidiendo un limit que el servidor le redondea y el selector va sin marcar.
func TestDefaultPageSizeEstaEnLaLista(t *testing.T) {
	for _, s := range PageSizes {
		if s == DefaultPageSize {
			return
		}
	}
	t.Errorf("DefaultPageSize (%d) no está en PageSizes %v", DefaultPageSize, PageSizes)
}

func TestPageSizesEstaOrdenada(t *testing.T) {
	for i := 1; i < len(PageSizes); i++ {
		if PageSizes[i] <= PageSizes[i-1] {
			t.Fatalf("PageSizes debe crecer sin repetir: %v", PageSizes)
		}
	}
}
