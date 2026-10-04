package module

import (
	"context"
	"log"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fasterp/backend/internal/db"
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

// MenuItem es una entrada del menú lateral.
//
// Model dice qué modelo abre la entrada, cuando la abre. Es lo que permite que
// el mismo módulo sirva varias páginas sin que el menú tenga que conocer los
// nombres de sus modelos: la UI usa ese campo para montar un data-fast-view con
// el CRUD ya deducido del esquema.
type MenuItem struct {
	Module string `json:"module"`
	Key    string `json:"key"`
	Label  string `json:"label"`
	Icon   string `json:"icon"`
	Route  string `json:"route"`
	Parent string `json:"parent,omitempty"`
	Seq    int    `json:"seq"`
	Model  string `json:"model,omitempty"`
	// Auto marca las entradas que el core dedujo en vez de las que escribió el
	// módulo. El cliente las puede tratar distinto (por ejemplo, agruparlas) sin
	// tener que comparar rutas.
	Auto bool `json:"auto,omitempty"`
}

// MenuNode es una entrada del menú ya resuelta en árbol, lista para pintar.
type MenuNode struct {
	MenuItem
	Children []MenuNode `json:"children,omitempty"`
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

// Menus devuelve el menú lateral: primero lo que los módulos declaran y, para los
// que no declaran nada, las entradas deducidas de sus modelos.
//
// La deducción es lo que hace que instalar un módulo baste para que aparezca en
// el menú. Antes un módulo sin "menus" no aparecía en ninguna parte: el sidebar
// los armaba a mano con los instalados, lo que rompía en cuanto un módulo
// declaraba una página con un path propio (/admin/productos) o quería dos
// entradas en vez de una.
//
// Ordena por seq y luego por módulo, para que dos módulos con el mismo seq no se
// reordenen entre llamadas. SortStable sobre un slice, así que el orden de
// entrada manda en los empates.
func (r *Registry) Menus() []MenuItem {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var items []MenuItem
	for name, m := range r.modules {
		if m.Manifest == nil {
			continue
		}
		items = append(items, menuItemsFor(name, m.Manifest)...)
	}

	// La ruta se normaliza aquí, una sola vez para todas las entradas, y no en el
	// punto donde se construye cada una.
	//
	// Las páginas de un módulo las sirve el admin, así que un enlace de menú solo
	// funciona si empieza por /admin. Un manifest puede declarar "/ventas" y otro
	// "/admin/ventas" —los dos son válidos para quien los escribe— y sin esto el
	// primero genera un enlace a una ruta que no está registrada y el clic se
	// lleva un 404.
	for i := range items {
		items[i].Route = adminRoute(items[i].Route)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Seq != items[j].Seq {
			return items[i].Seq < items[j].Seq
		}
		return items[i].Module < items[j].Module
	})
	return items
}

// adminRoute convierte una ruta declarada en la ruta que sirve el admin.
//
// Normaliza antes de decidir el prefijo, porque un manifest puede llevar las
// barras de más ("//admin//ventas") y compararlas después daría una ruta que no
// existe en el router.
func adminRoute(route string) string {
	route = strings.TrimSpace(route)
	if route != "" && !strings.HasPrefix(route, "/") {
		route = "/" + route
	}

	// path.Clean resuelve "//", "/./" y los ".." que sobre. La barra final se
	// quita porque "/admin/ventas/" y "/admin/ventas" son la misma página, y
	// compararlas como distintas rompe el marcado de la entrada activa del menú.
	route = strings.TrimSuffix(path.Clean(route), "/")
	if route == "" || route == "." {
		return "/admin"
	}

	// No se limita a comprobar el prefijo con HasPrefix: "/administracion"
	// empieza por "/admin" pero es otra página, y tratarla como si ya estuviera
	// bajo el admin produce un enlace roto sin ningún error visible.
	if route == "/admin" || strings.HasPrefix(route, "/admin/") {
		return route
	}
	return "/admin" + route
}

// menuItemsFor arma las entradas de un módulo: las suyas y, si no tiene ninguna,
// las que se deducen de sus modelos.
func menuItemsFor(moduleName string, m *Manifest) []MenuItem {
	// El orden de declaración manda: es el que escribió el módulo.
	keys := m.MenuOrder()
	if len(keys) == 0 {
		for k := range m.Menus {
			keys = append(keys, k)
		}
	}

	items := make([]MenuItem, 0, len(keys))
	for _, key := range keys {
		menu := m.Menus[key]
		if menu == nil {
			continue
		}
		items = append(items, MenuItem{
			Module: moduleName,
			Key:    key,
			Label:  menu.Label,
			Icon:   menu.Icon,
			Route:  menu.Route,
			Parent: menu.Parent,
			Seq:    menu.Seq,
			Model:  menu.Model,
		})
	}

	if len(items) > 0 {
		return items
	}
	return deducedMenus(moduleName, m)
}

// deducedMenus construye las entradas de un módulo que no declara menús.
//
// Un módulo con frontend propio aporta sus páginas, en el orden en que las
// declara y con la ruta que declaró. Uno sin frontend aporta una entrada por
// modelo, que es justo el caso que la UI genérica cubre sola: instalar el módulo
// crea el CRUD y el menú sin tocar una línea de código.
func deducedMenus(moduleName string, m *Manifest) []MenuItem {
	var items []MenuItem

	if fe := m.Frontend; fe != nil {
		for _, page := range fe.Pages {
			items = append(items, MenuItem{
				Module: moduleName,
				Key:    "page:" + page.Name,
				Label:  page.Label,
				Icon:   page.Icon,
				Route:  page.Path,
				Seq:    deducedPageSeq(page.Path),
				Auto:   true,
			})
		}
	}
	if len(items) > 0 {
		return items
	}

	// Sin frontend y sin menús: una entrada por modelo. El seq arranca en 10 para
	// que quien declare un menú con seq 1 lo ponga por delante sin tener que
	// renumerar los deducidos.
	for _, modelName := range m.ModelOrder() {
		def := m.Models[modelName]
		if def == nil {
			continue
		}
		label := def.Label
		if label == "" {
			label = modelName
		}
		items = append(items, MenuItem{
			Module: moduleName,
			Key:    "model:" + modelName,
			Label:  label,
			Icon:   m.Icon,
			Route:  "/" + moduleName + "/" + modelName,
			Seq:    deducedPageSeq("/" + moduleName + "/" + modelName),
			Model:  moduleName + "/" + modelName,
			Auto:   true,
		})
	}
	return items
}

// deducedPageSeq reparte los seq deducidos por el orden alfabético de la ruta.
//
// Sin esto, todos los módulos autogenerados compartido seq y el menú quedaría en
// orden de módulo, no de nombre. Con esto el criterio es el mismo para todos y
// se puede insertar un módulo nuevo sin renumerar nada.
func deducedPageSeq(route string) int {
	seq := 1000
	for _, ch := range route {
		seq += int(ch)
		if seq > 9000 {
			// Un seq enorme no aporta nada y complica el orden. Se satura.
			return 9000
		}
	}
	return seq
}

// MenuTree devuelve el menú como árbol, con los hijos colgando del padre.
//
// Las entradas cuyo padre no existe se quedan en la raíz en vez de desaparecer:
// un módulo que declara parent: "ventas" cuando "ventas" no está instalado es un
// error de configuración, y esconder la entrada es peor que mostrarla mal —queda
// visible que falta algo—.
func (r *Registry) MenusForTenant(tenantID string) []MenuItem {
	items := r.Menus()
	if tenantID == "" {
		return items
	}

	active := tenantActiveModules(tenantID)
	if len(active) == 0 {
		return nil
	}
	return filterMenuItemsByNames(items, active)
}

func (r *Registry) MenuTree() []MenuNode {
	items := r.Menus()
	return buildMenuTree(items)
}

func (r *Registry) MenuTreeForTenant(tenantID string) []MenuNode {
	items := r.MenusForTenant(tenantID)
	return buildMenuTree(items)
}

func filterMenuItemsByNames(items []MenuItem, active map[string]bool) []MenuItem {
	if len(active) == 0 {
		return nil
	}
	out := make([]MenuItem, 0, len(items))
	for _, item := range items {
		if active[item.Module] {
			out = append(out, item)
		}
	}
	return out
}

func tenantActiveModules(tenantID string) map[string]bool {
	active := make(map[string]bool)
	if tenantID == "" {
		return active
	}

	rows, err := db.DB.QueryContext(context.Background(),
		"SELECT name FROM installed_modules WHERE tenant_id = $1 AND active = true",
		tenantID,
	)
	if err != nil {
		log.Printf("[Modules] no se pudieron leer los módulos activos del tenant %s: %v", tenantID, err)
		return active
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		active[name] = true
	}
	return active
}

func buildMenuTree(items []MenuItem) []MenuNode {
	byKey := make(map[string]*MenuNode, len(items))
	nodes := make([]MenuNode, len(items))
	for i, item := range items {
		nodes[i] = MenuNode{MenuItem: item}
		byKey[item.Module+"/"+item.Key] = &nodes[i]
	}

	roots := make([]MenuNode, 0, len(items))
	for i := range nodes {
		parentKey := nodes[i].Parent
		if parentKey == "" {
			roots = append(roots, nodes[i])
			continue
		}
		// El padre se busca dentro del mismo módulo primero. Buscar en todos
		// los módulos daría parenting entre primos, que casi siempre es un
		// error de escribir la clave sin el prefijo del módulo.
		full := nodes[i].Module + "/" + parentKey
		if parent, ok := byKey[full]; ok {
			parent.Children = append(parent.Children, nodes[i])
			continue
		}
		roots = append(roots, nodes[i])
	}
	return roots
}

// ModuleForPath resuelve la parte de una ruta /admin/... al módulo que la
// declara.
//
// Se prueban las dos fuentes de rutas que un módulo puede declarar: la ruta de
// un menú y el path de una página de frontend. Devolver el nombre del módulo y no
// la ruta es lo que permite que la página se sirva aunque el manifest la haya
// escrito como /admin/productos en vez de /admin/products.
func (r *Registry) ModuleForPath(requested string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	want := "/" + strings.Trim(requested, "/")

	// Se recorren en orden inverso al de registro para que el resultado sea
	// estable: Names() está ordenado, así que el primer match es el mismo en
	// cada llamada.
	names := make([]string, 0, len(r.modules))
	for name := range r.modules {
		names = append(names, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	for _, name := range names {
		m := r.modules[name].Manifest
		if m == nil {
			continue
		}
		for _, menu := range m.Menus {
			if menu != nil && trimRoute(menu.Route) == want {
				return name, true
			}
		}
		if fe := m.Frontend; fe != nil {
			for _, page := range fe.Pages {
				if trimRoute(page.Path) == want {
					return name, true
				}
			}
		}
		// Los menús que el core dedujo también son rutas reales: el sidebar
		// genera enlaces a ellas y tienen que resolver a un módulo. Sin esto, cada
		// entrada autogenerada ("/productos/product") daría 404 al pulsarla, que
		// es justo el caso que se quiere cubrir sin escribir un manifest.
		for _, item := range deducedMenus(name, m) {
			if trimRoute(item.Route) == want {
				return name, true
			}
		}
	}
	return "", false
}

// trimRoute normaliza una ruta declarada para compararla con lo que llega de la
// URL: sin barra final y sin prefijo /admin, que es el que aporta el router.
func trimRoute(route string) string {
	route = strings.TrimSuffix(strings.TrimSpace(route), "/")
	route = strings.TrimPrefix(route, "/admin")
	if route == "" {
		return "/"
	}
	return route
}
