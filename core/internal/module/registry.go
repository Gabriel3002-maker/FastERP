package module

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Route es una ruta que un módulo quiere añadir al router.
type Route struct {
	Method string
	Path   string
}

// ModelRegistration es un modelo ya resuelto contra la base: sabe su tabla y qué
// columnas existen.
type ModelRegistration struct {
	Manifest  *ModelDef
	TableName string
	// Columns son los nombres de campo del manifest, en orden de declaración.
	Columns []string
}

// ModuleInstance es un módulo cargado y listo para servir.
type ModuleInstance struct {
	Manifest *Manifest
	Routes   []Route
	Models   []ModelRegistration
	// SourcePath es el manifest del que salió. El watcher lo usa para saber qué
	// recargar, y un mensaje de error tiene que poder señalar el fichero.
	SourcePath string
}

// Model busca el registro de un modelo por nombre.
func (mi *ModuleInstance) Model(name string) *ModelRegistration {
	for i := range mi.Models {
		if mi.Models[i].Manifest != nil && mi.Models[i].Manifest.Name == name {
			return &mi.Models[i]
		}
	}
	return nil
}

// HasField dice si un nombre corresponde a un campo del modelo.
//
// id, created_at y updated_at también cuentan: son columnas de toda tabla, las
// pone el core, y sin ellas ?order=created_at y ?filter=id:eq:… se rechazarían
// como si fueran errores de dedo del cliente.
func (r *ModelRegistration) HasField(name string) bool {
	if isCoreColumn(name) {
		return true
	}
	for _, f := range r.Manifest.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func isCoreColumn(name string) bool {
	switch name {
	case "id", "tenant_id", "created_at", "updated_at":
		return true
	}
	return false
}

// CastValue convierte un texto de la URL al tipo Go de la columna.
//
// El valor de un filtro llega siempre como texto porque viaja en la query
// string, y lib/pq no convierte: comparar una columna integer con la cadena "30"
// en lib/pq da error de tipos, y comparar en la base deja que sea Postgres quien
// decida la conversión. Convierte aquí, que es donde está el tipo declarado.
//
// Un texto que no se puede convertir devuelve el original sin error: el error lo
// da la columna, y un mensaje de Postgres sobre el valor exacto es más útil que
// uno inventado aquí.
func (r *ModelRegistration) CastValue(field, text string) any {
	if isCoreColumn(field) {
		return text
	}

	var def *FieldDef
	for i := range r.Manifest.Fields {
		if r.Manifest.Fields[i].Name == field {
			def = &r.Manifest.Fields[i]
			break
		}
	}
	if def == nil {
		return text
	}

	switch strings.ToLower(def.Type) {
	case "integer", "int", "bigint", "serial":
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n
		}
	case "decimal", "numeric", "float", "double", "money":
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return f
		}
	case "boolean", "bool":
		if b, err := strconv.ParseBool(text); err == nil {
			return b
		}
	}
	return text
}

type MenuItem struct {
	Module string `json:"module"`
	Key    string `json:"key"`
	Label  string `json:"label"`
	Icon   string `json:"icon"`
	Route  string `json:"route"`
	Parent string `json:"parent,omitempty"`
	Seq    int    `json:"seq"`
}

// Registry guarda los módulos cargados. El watcher lo modifica en caliente, así
// que todo lo que se lee pasa por el mutex.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]*ModuleInstance
}

var Global = &Registry{
	modules: make(map[string]*ModuleInstance),
}

func (r *Registry) Register(mod *ModuleInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules[mod.Manifest.Name] = mod
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.modules, name)
}

func (r *Registry) Get(name string) *ModuleInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.modules[name]
}

func (r *Registry) All() []*ModuleInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mods := make([]*ModuleInstance, 0, len(r.modules))
	for _, m := range r.modules {
		mods = append(mods, m)
	}
	return mods
}

// Names devuelve los nombres de módulo cargados, ordenados. El orden importa:
// las dependencias se instalan en orden, y un mapa de Go no lo tiene.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.modules))
	for name := range r.modules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Menus devuelve el menú lateral, ordenado por seq y luego por módulo, para que
// dos módulos con el mismo seq no se reordenen entre llamadas.
func (r *Registry) Menus() []MenuItem {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var items []MenuItem
	for name, m := range r.modules {
		if m.Manifest == nil {
			continue
		}
		// El orden de declaración manda: es el que escribió el módulo.
		keys := m.Manifest.MenuOrder()
		if len(keys) == 0 {
			for k := range m.Manifest.Menus {
				keys = append(keys, k)
			}
		}
		for _, key := range keys {
			menu := m.Manifest.Menus[key]
			if menu == nil {
				continue
			}
			items = append(items, MenuItem{
				Module: name,
				Key:    key,
				Label:  menu.Label,
				Icon:   menu.Icon,
				Route:  menu.Route,
				Parent: menu.Parent,
				Seq:    menu.Seq,
			})
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Seq != items[j].Seq {
			return items[i].Seq < items[j].Seq
		}
		return items[i].Module < items[j].Module
	})
	return items
}
