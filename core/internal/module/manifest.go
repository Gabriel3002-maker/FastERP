package module

import (
	"fmt"
	"strings"
)

// Manifest es lo que declara un módulo. Es la única fuente de verdad del
// esquema: no hay SQL en los módulos, el core crea y reconcilia las tablas a
// partir de aquí.
//
// Tipos, campos, sequence, options y workflow viven en spec.go; PageDef,
// FrontendDef y MenuDef, en manifest_frontend.go. Este fichero solo agrupa las
// raíces y su carga.
type Manifest struct {
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Label       string `json:"label" yaml:"label"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Author      string `json:"author,omitempty" yaml:"author,omitempty"`
	Icon        string `json:"icon,omitempty" yaml:"icon,omitempty"`
	License     string `json:"license,omitempty" yaml:"license,omitempty"`

	// Depends son los módulos que tienen que estar instalados antes. Se
	// comprueba al instalar, no solo al cargar: un módulo que depende de otro
	// que no está simplemente no arranca, y eso se lee mucho mejor en el
	// instalador que en un error de arranque.
	Depends []string `json:"depends,omitempty" yaml:"depends,omitempty"`

	// Models y Menus van como mapas para que se lean como un documento: en YAML
	// o JSON el orden de un mapa no está garantizado, así que el orden real de
	// declaración se recupera aparte con fieldOrder() y menuOrder().
	Models map[string]*ModelDef `json:"models" yaml:"models"`
	Menus  map[string]*MenuDef  `json:"menus,omitempty" yaml:"menus,omitempty"`

	// Frontend son las páginas propias del módulo, cuando las tiene. Un módulo
	// sin frontend no necesita nada aquí: la UI se deduce del esquema.
	Frontend *FrontendDef `json:"frontend,omitempty" yaml:"frontend,omitempty"`

	// Routes son endpoints propios del módulo. Ninguno de los módulos actuales
	// los usa; se conserva porque es la vía para lógica que no cabe en un CRUD.
	Routes []RouteDef `json:"routes,omitempty" yaml:"routes,omitempty"`

	// fieldOrder y menuOrder guardan el orden real de declaración, que los mapas
	// pierden. Se rellenan al cargar.
	fieldOrder []string
	menuOrder  []string
}

// RouteDef es un endpoint que declara el módulo.
type RouteDef struct {
	Method string `json:"method" yaml:"method"`
	Path   string `json:"path" yaml:"path"`
}

// TableName es el nombre de la tabla física de un modelo: mod_<módulo>_<modelo>.
func (m *Manifest) TableName(model string) string {
	return "mod_" + m.Name + "_" + model
}

// Validate comprueba que el manifest sea instalable. Se ejecuta al cargarlo,
// antes de generar una sola sentencia de DDL.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return errManifest("falta \"name\"")
	}
	if !safeIdentRe.MatchString(m.Name) {
		return errManifest("name %q inválido: minúsculas, dígitos y guion bajo, empezando por letra", m.Name)
	}
	if m.Label == "" {
		return errManifest("falta \"label\"")
	}
	// Un módulo sin modelos es legítimo: hay frontend puro, páginas de
	// configuración y módulos que solo añaden menús. Lo que no puede pasar es
	// depender de algo que no declare modelos esperando tablas.

	for _, dep := range m.Depends {
		if dep == m.Name {
			return errManifest("el módulo %q depende de sí mismo", m.Name)
		}
		if !safeIdentRe.MatchString(dep) {
			return errManifest("dependencia %q inválida", dep)
		}
	}

	for name, model := range m.Models {
		if model == nil {
			return errManifest("el modelo %q del módulo %q está vacío", name, m.Name)
		}
		// La clave del mapa es la que manda: es lo que aparece en la URL.
		model.Name = name
		if err := model.validate(m.Name); err != nil {
			return errManifest("%v", err)
		}
		model.FieldOrder = model.orderedNames()
	}

	for name, menu := range m.Menus {
		if menu == nil {
			continue
		}
		if menu.Label == "" {
			return errManifest("el menú %q del módulo %q no declara label", name, m.Name)
		}
		if menu.Route == "" {
			// Igual que en el SDK: sin ruta explícita el menú apunta al propio
			// módulo, que es lo que quiere el 90% de las veces.
			menu.Route = "/" + m.Name
		}
		if !strings.HasPrefix(menu.Route, "/") {
			menu.Route = "/" + menu.Route
		}
	}

	return nil
}

// orderedNames devuelve los nombres de campo en el orden en que se declararon.
func (m *ModelDef) orderedNames() []string {
	order := make([]string, len(m.Fields))
	for i, f := range m.Fields {
		order[i] = f.Name
	}
	return order
}

// modelError identifica el manifest culpable sin repetir el módulo en cada
// mensaje: quien lo va a leer ya sabe cuál está instalando.
type modelError string

func (e modelError) Error() string { return string(e) }

func errManifest(format string, args ...any) error {
	return modelError(fmt.Sprintf(format, args...))
}
