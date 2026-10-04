package api

import (
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
)

// UI son las rutas que sirven la interfaz: páginas del admin, estáticos del core
// y frontend de los módulos.
//
// La UI no va detrás de AuthMiddleware a propósito. El token vive en
// localStorage, no en una cookie: el navegador no lo manda en la navegación, así
// que un guard server-side vería a un usuario autenticado como anónimo y lo
// echaría en un bucle de redirecciones.
//
// Lo que el shell entrega es estructura sin datos: ni el menú, ni las filas de una
// tabla, ni un nombre de usuario salen de aquí. Todo eso llega por /api, que sí
// exige token y aplica RLS. Un guard en el shell no añadiría seguridad, sólo una
// comprobación que se puede saltar con curl — y sí añadiría el bug de bucle.
type UI struct {
	renderer *Renderer
	modules  string // directorio raíz de los módulos, para servir su frontend
	static   string
}

// NewUI construye el conjunto de rutas de interfaz.
func NewUI(renderer *Renderer, modulesDir, staticDir string) *UI {
	return &UI{renderer: renderer, modules: modulesDir, static: staticDir}
}

// LoginData son los datos que la página de login recibe del servidor.
//
// Va aparte de PageData porque login.html se renderiza sin layout: no hay menú ni
// usuario todavía en ese punto, que es justo lo que no se puede saber antes de
// entrar.
type LoginData struct {
	// TenantHint va preescrito cuando la instancia tiene un único tenant. Vacío
	// significa "pregúntaselo al usuario".
	TenantHint string
	// Next es la ruta a la que volver tras entrar, tal y como la envió FastClient
	// al expirar la sesión.
	Next string
	// Nonce lo rellena Bare; ver nonceSetter.
	Nonce string
}

// RegisterRoutes monta la UI en el engine.
//
// El orden importa dentro de cada prefijo: los segmentos literales se registran
// antes que los comodines para que /admin/audit no se interprete como un módulo
// llamado "audit".
func (ui *UI) RegisterRoutes(r *gin.Engine) {
	// Estáticos del core: CSS, JS del motor de vistas y las librerías
	// vendorizadas (swagger, mermaid, drawflow, bpmn-js, echarts). Se sirven con
	// http.FileServer y no con un handler propio: no hay lógica que añadir y
	// gin.Static es justo el FileServer con el prefijo puesto.
	r.Static("/static", ui.static)

	// Frontend de los módulos. Un módulo son tres ficheros sueltos (index.html,
	// app.js, styles.css) que el shell carga por URL, así que se sirve el
	// directorio entero. La ruta se valida antes de tocar el disco: el nombre
	// viene de la URL yModulesDir es un volumen montado.
	r.GET("/modules/*path", ui.serveModuleAsset)

	r.GET("/", ui.Root)
	r.GET("/login", ui.Login)
	r.GET("/setup", ui.Setup)

	admin := r.Group("/admin")
	{
		admin.GET("", ui.Dashboard)
		admin.GET("/modules", ui.ModuleStore)
		admin.GET("/audit", ui.Audit)
		// Cualquier otra cosa bajo /admin es la página de un módulo. Se resuelve
		// por lo que el manifest declara, no por lo que haya en disco: un
		// directorio sin manifest no es un módulo y no merece una página.
		//
		// ":page" y ":page/*rest" en vez de un único "/admin/*path": gin 1.9.1
		// monta un solo catch-all por nivel estático y da panic al registrar el
		// segundo ("conflicts with existing path segment"). Un parámetro simple no
		// es catch-all, así que el segundo cuelga del primero sin chocar con
		// /modules/*path ni con los estáticos. Los literales "modules" y "audit" de
		// arriba se registran antes, y en gin ganan al parámetro.
		admin.GET("/:page", ui.ModulePage)
		admin.GET("/:page/*rest", ui.ModulePage)
	}
}

// modulePath reconstruye la ruta bajo /admin desde los dos parámetros. Gin solo
// reparte un segmento en ":page" y el resto en "*rest", así que una página
// anidada como /admin/productos/producto llega en dos trozos y hay que juntarlos.
func modulePath(c *gin.Context) string {
	page := c.Param("page")
	rest := strings.Trim(c.Param("rest"), "/")
	if rest == "" {
		return page
	}
	return page + "/" + rest
}

// Root decide entre el asistente de instalación y el login.
//
// No hay un flag de "instalado": se pregunta a la base. Un flag en disco se
// desincroniza del primer usuario creado y deja la instancia en un estado en el
// que no se puede entrar ni administrar.
func (ui *UI) Root(c *gin.Context) {
	var count int
	// db.DB sin tenant a propósito: tenants es el registro que se consulta antes
	// de que exista cualquier contexto de tenant, y no lleva RLS.
	if err := db.DB.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM tenants").Scan(&count); err != nil {
		log.Printf("[UI] no se pudo contar los tenants: %v", err)
		// Sin saber si hay tenants no se puede decidir a dónde mandar. El login
		// es el destino menos dañado: el asistente de instalación también exige
		// que no haya tenants, así que redirigir ahí con la base caída daría un
		// error que no explica la causa.
		ui.renderer.Bare(c, "login.html", ui.loginData(c))
		return
	}

	if count == 0 {
		ui.renderer.Bare(c, "setup-wizard.html", &SetupData{})
		return
	}
	ui.renderer.Bare(c, "login.html", ui.loginData(c))
}

// Login sirve la página de acceso.
func (ui *UI) Login(c *gin.Context) {
	ui.renderer.Bare(c, "login.html", ui.loginData(c))
}

// loginData son los datos que la página de login necesita del servidor.
//
// Next es el ?next= que FastClient añade al expired sesión, para que el usuario
// vuelva a donde estaba en vez de siempre al dashboard.
func (ui *UI) loginData(c *gin.Context) *LoginData {
	return &LoginData{
		TenantHint: onlyTenantSlug(c),
		Next:       c.Query("next"),
	}
}

// onlyTenantSlug devuelve el slug del tenant si la instancia tiene exactamente
// uno.
//
// El servidor exige X-Tenant-ID ya en el login, así que sin esto hay que escribir
// el tenant a mano en cada entrada. Se revela solo cuando hay uno: en una
// instancia multi-tenant el campo se deja vacío y hay que rellenarlo.
//
// El coste es que un visitante anónimo puede deducir que la instancia tiene un
// único tenant. Es un dato de configuración, no de negocio, y para una app
// autoalojada la alternativa —exigir el slug siempre— convierte cada login en un
// ticket a soporte sin añadir nada.
func onlyTenantSlug(c *gin.Context) string {
	var slug string
	err := db.DB.QueryRowContext(c.Request.Context(), `
		SELECT slug
		FROM tenants
		WHERE active AND (slug = 'default' OR slug IS NOT NULL)
		ORDER BY CASE WHEN slug = 'default' THEN 0 ELSE 1 END, created_at ASC
		LIMIT 1
	`).Scan(&slug)
	if err != nil {
		log.Printf("[UI] no se pudo leer el tenant por defecto: %v", err)
		return ""
	}
	return slug
}

func singleTenantID(c *gin.Context) string {
	var id string
	err := db.DB.QueryRowContext(c.Request.Context(), `
		SELECT id
		FROM tenants
		WHERE active
		ORDER BY CASE WHEN slug = 'default' THEN 0 ELSE 1 END, created_at ASC
		LIMIT 1
	`).Scan(&id)
	if err != nil {
		log.Printf("[UI] no se pudo leer el tenant activo: %v", err)
		return ""
	}
	return id
}

// Setup sirve el asistente de instalación.
func (ui *UI) Setup(c *gin.Context) {
	ui.renderer.Bare(c, "setup-wizard.html", &SetupData{})
}

// Dashboard es la página de inicio del admin.
func (ui *UI) Dashboard(c *gin.Context) {
	ui.page(c, "dashboard-content.html", PageData{
		Title:       "Dashboard",
		CurrentPage: "dashboard",
	})
}

// ModuleStore es la pantalla de instalar y activar módulos.
func (ui *UI) ModuleStore(c *gin.Context) {
	ui.page(c, "modules-content.html", PageData{
		Title:       "Módulos",
		CurrentPage: "modules",
	})
}

// Audit es la pantalla de auditoría.
func (ui *UI) Audit(c *gin.Context) {
	ui.page(c, "audit-content.html", PageData{
		Title:       "Auditoría",
		CurrentPage: "audit",
	})
}

// page es el camino común de las páginas del admin: renderiza content dentro del
// layout con el menú ya resuelto.
//
// El menú se inyecta aquí y no en cada handler a propósito. Rellenarlo en cada uno
// era un forgot silencioso: el dashboard sePaintaba sin navegación lateral y no
// había ningún error, solo una absencesLinks que costaba un rato detectar. Aquí no
// se puede olvidar porque no hay otro sitio donde rellenarlo.
func (ui *UI) page(c *gin.Context, content string, data PageData) {
	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		tenantID = singleTenantID(c)
		if tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
	}
	if data.Menus == nil {
		data.Menus = module.Global.MenuTreeForTenant(tenantID)
	}
	ui.renderer.Page(c, content, data)
}

// ModulePage sirve la página de un módulo.
//
// Admite tanto /admin/<módulo> como /admin/<ruta-declarada>. Los manifests
// declaran sus páginas con un path propio ("/admin/productos") que no tiene por
// qué ser el nombre del módulo, y el menú lateral apunta a ese path: sin esta
// resolución, los enlaces que el propio menú genera darían 404.
func (ui *UI) ModulePage(c *gin.Context) {
	requested := modulePath(c)
	if requested == "" {
		ui.Dashboard(c)
		return
	}

	name, ok := module.Global.ModuleForPath(requested)
	if !ok {
		// No es una ruta declarada. Se prueba con el nombre del módulo, que es lo
		// que todos los módulos usan hoy.
		if module.Global.Get(requested) != nil {
			name = requested
		} else {
			c.String(http.StatusNotFound, "módulo no encontrado")
			return
		}
	}

	inst := module.Global.Get(name)
	if inst == nil || inst.Manifest == nil {
		c.String(http.StatusNotFound, "módulo no encontrado")
		return
	}

	page := ui.modulePageData(c, name, inst)
	ui.page(c, page.content, page.data)
}

// modulePageResult dice qué plantilla sirve un módulo y con qué datos.
type modulePageResult struct {
	content string
	data    PageData
}

// modulePageData decide entre el frontend del módulo y el fallback genérico.
//
// Un frontend solo sirve si además existe en disco. Declararlo en el manifest no
// garantiza el fichero: un módulo puede declarar frontend.entry y que nadie haya
// subido su index.html. En ese caso module-loader.html se quedaría con un error de
// carga en pantalla, así que se comprueba el index.html antes de elegir y, si no
// está, se sirve la página de modelos con el CRUD deducido del esquema.
func (ui *UI) modulePageData(c *gin.Context, name string, inst *module.ModuleInstance) modulePageResult {
	m := inst.Manifest

	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		tenantID = singleTenantID(c)
		if tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
	}

	data := PageData{
		Title:       m.Label,
		ModuleName:  name,
		CurrentPage: "module",
		Menus:       module.Global.MenuTreeForTenant(tenantID),
	}

	if fe := m.Frontend; fe != nil && fe.Entry != "" {
		index := filepath.Join(moduleFrontendPath(ui.modules, name), filepath.Base(fe.Entry))
		if info, err := os.Stat(index); err == nil && !info.IsDir() {
			return modulePageResult{content: "module-loader.html", data: data}
		}
		log.Printf("[UI] %s declara frontend (%s) pero no está en disco: se sirve la página de modelos",
			name, fe.Entry)
	}

	// Fallback: una lista por modelo con el CRUD genérico.
	data.ModuleLabel = m.Label
	data.ModuleDescription = m.Description
	data.ModuleIcon = m.Icon
	data.ModuleModels = modelsFor(name, m)
	return modulePageResult{content: "module-placeholder.html", data: data}
}

// modelsFor devuelve los modelos de un módulo en el orden en que se declararon,
// con la etiqueta que se puede mostrar y el par "módulo/modelo" que espera el
// data-fast-view.
//
// El orden es el del manifest y no el del mapa porque un mapa no lo tiene: con
// veinte modelos el menú sale en un orden arbitrario en cada arranque, y quien
// busca uno tiene que recorrerlo entero.
func modelsFor(moduleName string, m *module.Manifest) []moduleModel {
	var out []moduleModel

	appendModel := func(key string, def *module.ModelDef) {
		out = append(out, moduleModel{
			// El manifest nombra los modelos sin prefijo ("factura"), pero el CRUD
			// y la API los llaman con él ("ventas/factura"): es lo que los separa
			// de un homónimo de otro módulo.
			Model: moduleName + "/" + key,
			Label: modelLabel(key, def),
		})
	}

	for _, key := range m.ModelOrder() {
		if def := m.Models[key]; def != nil {
			appendModel(key, def)
		}
	}

	// Modelos que el manifest no listó en el orden de declaración: se añaden al
	// final para que no desaparezcan de la página.
	for key, def := range m.Models {
		if def == nil || containsModel(out, moduleName+"/"+key) {
			continue
		}
		appendModel(key, def)
	}
	return out
}

// modelLabel saca una etiqueta legible del nombre del modelo.
//
// Los modelos suelen llamarse como el código ("001_factura", "res_partner"), que
// no es lo que se quiere leer en una pantalla. Si el manifest no pone un label, se
// usa el nombre tal cual: inventar un título bonito a partir del nombre sale mal
// ("001 factura", "Factura001") y es peor que mostrarlo tal cual.
func modelLabel(key string, def *module.ModelDef) string {
	if def != nil && def.Label != "" {
		return def.Label
	}
	return key
}

func containsModel(list []moduleModel, full string) bool {
	for _, m := range list {
		if m.Model == full {
			return true
		}
	}
	return false
}

// moduleModel es un modelo tal y como lo pinta la página de fallback.
type moduleModel struct {
	Model string // "módulo/modelo", tal como lo espera data-fast-view
	Label string
}

// serveModuleAsset sirve /modules/<módulo>/frontend/... — el index.html, el JS y
// el CSS de cada módulo.
//
// El nombre del módulo y el resto de la ruta vienen de la URL. No se resuelve
// contra la base ni se filtra por tenant: un módulo no instalado es un
// directorio que existe, y su frontend es HTML estático sin datos.
//
// Se sirve solo lo que hay bajo frontend/ y no el directorio del módulo entero.
// La diferencia no es cosmética: junto al frontend, el directorio de un módulo
// contiene main.go y module.wasm, y servir el árbol completo los publica sin
// autenticar — el código fuente de cada módulo y varios megas de wasm a
// cualquiera que sepa el nombre del directorio.
func (ui *UI) serveModuleAsset(c *gin.Context) {
	requested := strings.TrimPrefix(c.Param("path"), "/")

	// La ruta se parte en <módulo>/<resto> y el resto tiene que empezar por
	// "frontend/". Se exige el prefijo explícito en vez de deducir dónde empieza
	// el frontend: es lo que hace imposible que un "../" acabe saliendo del
	// directorio aunque safeAssetPathClean cambie.
	name, rest, found := strings.Cut(requested, "/")
	if !found || name == "" || !strings.HasPrefix(rest, "frontend/") {
		c.Status(http.StatusNotFound)
		return
	}

	full, err := safeAssetPathClean(moduleFrontendPath(ui.modules, name), strings.TrimPrefix(rest, "frontend/"))
	if err != nil {
		c.String(http.StatusBadRequest, "ruta inválida")
		return
	}

	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}

	// Se lee el fichero y se contesta con c.Data en vez de delegar en c.File
	// (http.ServeFile) por una razón concreta: ServeFile redirige con 301
	// cualquier petición que acabe en "/index.html" hacia "./", pensando que es
	// la entrada por defecto de un directorio. El frontend de cada módulo se
	// llama justamente index.html y el shell lo pide por esa ruta, así que con
	// c.File el módulo carga un 301 y nunca aparece.
	data, err := os.ReadFile(full)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, assetContentType(full), data)
}

// assetContentType decide el tipo por extensión.
//
// La tabla de mime del sistema puede no tener entradas (una imagen minimalista
// no conoce .wasm ni .css), y mandarlo todo como octet-stream rompe la carga de
// los scripts y las hojas de estilo en el navegador. Los tipos del frontend se
// fijan aquí y el resto se deduce de la extensión.
func assetContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	}
	if ct := mime.TypeByExtension(path); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// modulesDirExists avisa en el arranque cuando el directorio de módulos no está,
// porque el síntoma (una UI sin nada que mostrar) no dice nada sobre la causa.
func modulesDirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// moduleFrontendPath es la ruta en disco del frontend de un módulo. La usan el
// generador de Studio para no repetir el mismo join en tres sitios.
func moduleFrontendPath(modulesDir, name string) string {
	return filepath.Join(modulesDir, name, "frontend")
}
