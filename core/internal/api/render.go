package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// Renderer renderiza las páginas del admin desde core/templates.
//
// Las plantillas se parsean una vez al arrancar, no en cada petición. La versión
// anterior usaba template.ParseFiles por petición: pagaba el coste de parseo en
// cada carga de página, y el precio sube con el número de plantillas porque
// ParseFiles vuelve a parsear el conjunto entero cada vez que se le añade uno.
type Renderer struct {
	tpl    *template.Template
	pages  map[string]string // nombre base -> ruta en disco, para los diagnostics
	layout string
}

// layoutTemplate es la plantilla que envuelve a todas las páginas del admin.
const layoutTemplate = "layout.html"

// NewRenderer parsea todas las plantillas de templatesDir.
//
// Un error aquí es de arranque, no de petición: si una plantilla no compila, la
// UI está rota para todo el mundo y conviene que el proceso no levante en vez de
// servir un 500 en cada clic.
func NewRenderer(templatesDir string) (*Renderer, error) {
	entries, err := filepath.Glob(filepath.Join(templatesDir, "*.html"))
	if err != nil {
		return nil, fmt.Errorf("no se pudieron listar las plantillas: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no hay plantillas en %s", templatesDir)
	}

	tpl := template.New("").Funcs(templateFuncs())
	pages := make(map[string]string, len(entries))
	for _, path := range entries {
		name := filepath.Base(path)
		if _, err := tpl.ParseFiles(path); err != nil {
			return nil, fmt.Errorf("plantilla %s: %w", name, err)
		}
		pages[name] = path
	}

	if _, ok := pages[layoutTemplate]; !ok {
		return nil, fmt.Errorf("falta %s en %s", layoutTemplate, templatesDir)
	}

	return &Renderer{tpl: tpl, pages: pages, layout: layoutTemplate}, nil
}

// templateFuncs son las funciones disponibles dentro de las plantillas.
//
// Deliberadamente pocas. Una plantilla es HTML que el servidor ejecuta: cuanto
// más se le da acceso a datos, más superficie hay para que un valor llegue al
// navegador sin escapar. Todo lo que necesite lógica se calcula en Go y se pasa
// ya en la data.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// dict arma un mapa desde pares clave/valor, para que una vista partial
		// reciba exactamente los campos que necesita sin pasarles la struct
		// entera.
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict: se esperaba un número par de argumentos,/%d", len(values))
			}
			out := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: la clave %d no es un string", i/2)
				}
				out[key] = values[i+1]
			}
			return out, nil
		},
	}
}

// PageData son los campos que toda página del admin recibe.
//
// Menus viene del registro de módulos y se calcula antes de renderizar, no en el
// cliente: el menú lateral es la única parte de la UI que no puede esperar a un
// fetch para saber qué enlaces existen, porque sin él no hay con qué navegar.
type PageData struct {
	Title        string
	CurrentPage  string
	ModuleName   string
	Content      template.HTML
	ExtraStyles  template.CSS
	ExtraScripts template.JS
	Menus        any
	IsAdmin      bool
	UserName     string
	TenantSlug   string

	// Nonce es el valor que la CSP exige en cada <script> inline. Lo rellena el
	// renderer por petición; las plantillas no lo eligen.
	Nonce string

	// Campos de module-placeholder.html, la página que se sirve cuando un módulo
	// no trae frontend. Solo se rellenan en ese caso; el resto de páginas los
	// deja vacíos y no los tocan.
	ModuleLabel       string
	ModuleDescription string
	ModuleIcon        string
	ModuleModels      []moduleModel
}

// Page renderiza content dentro del layout y responde.
//
// Un fallo al renderizar se registra entero en el log y se responde con un 500
// escueto: el error de plantilla puede traer rutas internas y fragmentos de las
// plantillas, y eso no se devuelve al navegador.
func (rd *Renderer) Page(c *gin.Context, content string, data PageData) {
	// El nonce va antes de renderizar el contenido, no después: las plantillas de
	// contenido también llevan <script> inline y necesitan el valor.
	data.Nonce = newNonce()

	body, err := rd.renderInto(content, data)
	if err != nil {
		log.Printf("[UI] no se pudo renderizar %s: %v", content, err)
		c.String(http.StatusInternalServerError, "error interno renderizando la página")
		return
	}

	data.Content = template.HTML(body)
	if data.ExtraStyles == "" {
		data.ExtraStyles = template.CSS("")
	}
	if data.ExtraScripts == "" {
		data.ExtraScripts = template.JS("")
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Security-Policy", cspHeader(data.Nonce))
	if err := rd.tpl.ExecuteTemplate(c.Writer, rd.layout, data); err != nil {
		// La cabecera ya salió: lo único que queda es cortar la respuesta. No se
		// puede escribir un 500 a mitad de un HTML.
		log.Printf("[UI] no se pudo renderizar %s: %v", rd.layout, err)
		c.Abort()
	}
}

// nonceSetter lo implementan las páginas que se renderizan sin layout para poder
// recibir el nonce de la CSP. Bare() acepta cualquier data, así que no puede
// tocar sus campos directamente.
type nonceSetter interface {
	SetNonce(string)
}

// SetNonce lo implementa LoginData.
func (d *LoginData) SetNonce(nonce string) { d.Nonce = nonce }

// SetupData es la data del asistente de instalación.
type SetupData struct {
	Nonce string
}

// SetNonce lo implementa SetupData.
func (d *SetupData) SetNonce(nonce string) { d.Nonce = nonce }

// Bare renderiza una plantilla sin layout: login, asistente de instalación y
// cualquier página que traiga su propio esqueleto HTML.
func (rd *Renderer) Bare(c *gin.Context, name string, data any) {
	if _, ok := rd.pages[name]; !ok {
		log.Printf("[UI] no existe la plantilla %s", name)
		c.String(http.StatusNotFound, "página no encontrada")
		return
	}

	// Sin data no hay dónde poner el nonce, y el resultado sería una página con
	// scripts inline que la CSP bloquea: en el navegador no se ejecuta nada y no
	// hay ningún error en el log. Se prefiera el 500 a servir una página muerta.
	if data == nil {
		log.Printf("[UI] %s se renderiza sin data y no puede llevar nonce", name)
		c.String(http.StatusInternalServerError, "error interno renderizando la página")
		return
	}
	nonce := newNonce()
	if s, ok := data.(nonceSetter); ok {
		s.SetNonce(nonce)
	}

	var buf bytes.Buffer
	if err := rd.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("[UI] no se pudo renderizar %s: %v", name, err)
		c.String(http.StatusInternalServerError, "error interno renderizando la página")
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Security-Policy", cspHeader(nonce))
	if _, err := c.Writer.Write(buf.Bytes()); err != nil {
		log.Printf("[UI] no se pudo escribir la respuesta de %s: %v", name, err)
	}
}

// newNonce genera el valor que ata los scripts inline de una página a su CSP.
//
// Se genera uno por petición y no uno por proceso: reutilizarlo sería
// equivalente a permitir los scripts inline de cualquiera, que es justo lo que
// la CSP pretende impedir.
//
// Se codifica en base64url y no en base64 normal porque el valor se escribe en un
// atributo HTML y html/template escapa '+' como '&#43;'. El navegador deshace esa
// entidad y todo funcionaría, pero el HTML servido dejaría de contener el nonce y
// cualquier comprobación que compare cabecera y cuerpo —un test, una auditoría—
// vería dos valores distintos. Base64url solo usa '-', '_' y alfanuméricos, que no
// necesitan escape.
func newNonce() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand no falla salvo catástrofe del sistema. Si falla se sirve
		// la página sin nonce y sus scripts no se ejecutan: preferible a
		// ejecutarlos sin restricción.
		log.Printf("[UI] no se pudo generar nonce: %v", err)
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

// cspHeader construye la Content-Security-Policy.
//
// La define aquí el renderer y no el middleware porque el script-src depende del
// nonce, que solo existe cuando la respuesta es una página. El middleware la
// llama con nonce vacío para el resto de respuestas —JSON, assets, descargas—,
// donde 'self' basta y no hay nada inline que autorizar.
func cspHeader(nonce string) string {
	scriptSrc := "'self'"
	if nonce != "" {
		scriptSrc += " 'nonce-" + nonce + "'"
	}

	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + scriptSrc,
		// 'unsafe-inline' en estilo es deliberado: el cliente inyecta los estilos
		// de los módulos que carga y no hay forma de ponerles nonce.
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}, "; ")
}

// renderInto ejecuta la plantilla de contenido y devuelve el HTML.
//
// El resultado va como template.HTML al layout, sin escapar: es HTML que el
// propio servidor acaba de producir a partir de una plantilla del repositorio,
// no entrada de usuario. Escaparlo aparecería como texto literal en el navegador.
func (rd *Renderer) renderInto(name string, data PageData) (string, error) {
	if _, ok := rd.pages[name]; !ok {
		return "", fmt.Errorf("no existe la plantilla %s", name)
	}

	var buf bytes.Buffer
	if err := rd.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// HasPage indica si existe una plantilla con ese nombre.
func (rd *Renderer) HasPage(name string) bool {
	_, ok := rd.pages[name]
	return ok
}

// PageNames lista las plantillas disponibles, para diagnóstico en el arranque.
func (rd *Renderer) PageNames() []string {
	out := make([]string, 0, len(rd.pages))
	for name := range rd.pages {
		out = append(out, name)
	}
	return out
}

// safeAssetPathClean normaliza una ruta de asset bajo un directorio raíz.
//
// Se usa para los módulos: el nombre del módulo viene de la URL y se concatena
// con el directorio de módulos. filepath.Join limpia los "..", pero solo si la
// ruta no es absoluta: un "/etc" inicial devuelve la ruta absoluta entera y
// escapa del directorio. Por eso se exige que la ruta combinada siga dentro de
// root y se devuelve error si no.
func safeAssetPathClean(root, requested string) (string, error) {
	if strings.ContainsRune(requested, 0) {
		return "", fmt.Errorf("ruta inválida")
	}

	cleaned := filepath.Join(root, filepath.Clean("/"+requested))
	if cleaned != root && !strings.HasPrefix(cleaned, root+string(filepath.Separator)) {
		return "", fmt.Errorf("la ruta sale de %s", root)
	}
	return cleaned, nil
}
