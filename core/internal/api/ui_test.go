package api

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fasterp/backend/internal/module"
)

// rutaDe busca en la tabla de rutas de gin la que cubre path y devuelve el
// nombre del handler registrado.
//
// Se mira la tabla y no se despacha una petición porque los handlers reales
// tocan la base de datos y el disco: un test de enrutado no debería necesitar
// ni una cosa ni la otra.
func rutaDe(t *testing.T, r *gin.Engine, path string) string {
	t.Helper()
	for _, ri := range r.Routes() {
		if ri.Path != path {
			continue
		}
		// gin nombra el handler con el método que lo registra, que es justo lo
		// que hay que comprobar aquí.
		if i := strings.LastIndex(ri.Handler, ")."); i >= 0 {
			return ri.Handler[i+2:]
		}
		return ri.Handler
	}
	t.Fatalf("no hay ninguna ruta registrada para %s", path)
	return ""
}

// TestRegisterRoutesNoRompeElArbol protege una limitación concreta de gin 1.9.1:
// sólo admite un catch-all por nivel estático. "/admin/*path" hace panic en
// cuanto existe otro ("conflicts with existing path segment"), así que las
// páginas de módulo se cuelgan de ":page" y ":page/*rest".
//
// Si alguien "simplifica" el registro a un solo comodín, el arranque revienta
// con un panic que no dice qué ruta registrar. Aquí se falla antes, con nombre.
func TestRegisterRoutesNoRompeElArbol(t *testing.T) {
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("RegisterRoutes no admite esta combinación de rutas: %v", v)
		}
	}()

	r := gin.New()
	(&UI{}).RegisterRoutes(r)

	for _, tc := range []struct{ path, want string }{
		{"/", "Root-fm"},
		{"/login", "Login-fm"},
		{"/setup", "Setup-fm"},
		{"/admin", "Dashboard-fm"},
		{"/admin/modules", "ModuleStore-fm"},
		{"/admin/audit", "Audit-fm"},
		{"/admin/:page", "ModulePage-fm"},
		{"/admin/:page/*rest", "ModulePage-fm"},
		{"/modules/*path", "serveModuleAsset"},
	} {
		if got := rutaDe(t, r, tc.path); !strings.HasPrefix(got, tc.want) {
			t.Errorf("ruta %-24s -> %s, se esperaba %s", tc.path, got, tc.want)
		}
	}

	// Las literales tienen que existir por su cuenta. Si "/admin/modules" se
	// sirviera con el parámetro ":page", el parámetro gana en gin y la tienda de
	// módulos quedaría inalcanzable.
	for _, path := range []string{"/admin/modules", "/admin/audit"} {
		for _, ri := range r.Routes() {
			if ri.Path == path && strings.Contains(ri.Handler, "ModulePage") {
				t.Errorf("%s no debe resolver a ModulePage", path)
			}
		}
	}
}

// TestModulePathReconstruyeLaRuta comprueba el helper que junta ":page" y
// "*rest". Sin él, una página anidada perdería la segunda mitad de la ruta y
// ningún módulo con subruta ("/admin/productos/producto") sería resoluble.
func TestModulePathReconstruyeLaRuta(t *testing.T) {
	r := gin.New()
	var got string
	rec := func(c *gin.Context) { got = modulePath(c) }
	r.GET("/admin/:page", rec)
	r.GET("/admin/:page/*rest", rec)

	for _, tc := range []struct{ path, want string }{
		{"/admin/products", "products"},
		{"/admin/products/", "products"},
		{"/admin/products/product", "products/product"},
		{"/admin/a/b/c", "a/b/c"},
	} {
		got = ""
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if got != tc.want {
			t.Errorf("ruta %-24s -> %q, se esperaba %q", tc.path, got, tc.want)
		}
	}
}

// TestLasPlantillasCompilan ejecuta el renderer contra los templates de verdad.
//
// Parsear no es lo mismo que ejecutar, y el fallo aparece al arrancar el
// servidor, no en un test. Además el caso que se ha dado dos veces aquí es
// escribir la sintaxis de una plantilla (las llaves dobles) dentro de un
// comentario de JavaScript: el parser la ve igual, porque las plantillas se
// parsean como texto plano y no distinguen el interior de un <script>.
//
// La lista de páginas que hay que ejecutar sale del propio renderer, así que
// añadir una plantilla nueva la mete en el test sin tocarlo.
func TestLasPlantillasCompilan(t *testing.T) {
	rd, err := NewRenderer("../../templates")
	if err != nil {
		t.Fatalf("las plantillas no compilan: %v", err)
	}

	menus := []module.MenuNode{
		{Children: []module.MenuNode{}},
	}

	for _, name := range rd.PageNames() {
		data := PageData{
			Title:       "t",
			CurrentPage: "dashboard",
			ModuleName:  "products",
			Menus:       menus,
			ModuleLabel: "Productos",
			// El login se renderiza sin layout y con LoginData, no con PageData.
		}
		var target any = data
		switch name {
		case "login.html":
			target = LoginData{TenantHint: "acme", Next: "/admin/products"}
		case "setup-wizard.html":
			target = nil
		}

		var buf bytes.Buffer
		if err := rd.tpl.ExecuteTemplate(&buf, name, target); err != nil {
			t.Errorf("no se pudo ejecutar %s: %v", name, err)
		}
	}
}

// TestElNonceDelHTMLCoincideConElDeLaCSP comprueba que el nonce escrito en la
// cabecera es exactamente el mismo que aparece en el atributo nonce de los
// <script>, sin depender de que el navegador deshace entidades HTML.
//
// Es un test y no una comprobación manual porque el fallo es invisible: con
// base64 normal, html/template escribe '+' como '&#43;', el navegador lo
// deshace y la página funciona igual. Solo se rompe la comparación entre
// cabecera y cuerpo, que es justo la que permite verificar que la CSP está bien.
func TestElNonceDelHTMLCoincideConElDeLaCSP(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	rd, err := NewRenderer("../../templates")
	if err != nil {
		t.Fatalf("no se pudieron cargar las plantillas: %v", err)
	}

	rd.Page(c, "dashboard-content.html", PageData{Title: "Dashboard", CurrentPage: "dashboard"})
	body := rec.Body.String()

	headerNonce := cspNonceFrom(rec.Header().Get("Content-Security-Policy"))
	if headerNonce == "" {
		t.Fatal("la cabecera CSP no lleva nonce")
	}

	// El valor del atributo no debe llevar entidades. Solo se mira el atributo:
	// el resto del HTML sí lleva &lt; y &amp; legítimamente, porque el template
	// escapa las comparaciones de JavaScript.
	for _, atributo := range regexp.MustCompile(`nonce="[^"]*"`).FindAllString(body, -1) {
		if strings.ContainsAny(atributo, "&;") {
			t.Errorf("el nonce se sirve escapado (%s): no coincidirá con la cabecera", atributo)
		}
	}

	if !strings.Contains(body, `nonce="`+headerNonce+`"`) {
		t.Errorf("el atributo nonce del HTML no coincide con el de la cabecera (%q)", headerNonce)
	}
}

// cspNonceFrom extrae el nonce de un script-src de la CSP.
func cspNonceFrom(csp string) string {
	const marker = "'nonce-"
	i := strings.Index(csp, marker)
	if i < 0 {
		return ""
	}
	rest := csp[i+len(marker):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// TestLasPaginasNoLlevanScriptsInlineSinNonce recorre las plantillas y avisa de
// cualquier <script> sin src al que se le haya olvidado el nonce. Un script así
// no se ejecuta bajo la CSP actual y su fallo no aparece en ningún log.
func TestLasPaginasNoLlevanScriptsInlineSinNonce(t *testing.T) {
	entradas, err := filepath.Glob("../../templates/*.html")
	if err != nil {
		t.Fatalf("no se pudieron listar las plantillas: %v", err)
	}

	for _, ruta := range entradas {
		nombre := filepath.Base(ruta)
		contenido, err := os.ReadFile(ruta)
		if err != nil {
			t.Fatalf("%s: %v", nombre, err)
		}

		for i, linea := range strings.Split(string(contenido), "\n") {
			resto := strings.TrimSpace(linea)
			if !strings.HasPrefix(resto, "<script") {
				continue
			}
			if strings.Contains(resto, " src=") {
				continue // los externos pasan por 'self'
			}
			if !strings.Contains(resto, "nonce=") {
				t.Errorf("%s:%d: <script> inline sin nonce -> la CSP lo bloqueará", nombre, i+1)
			}
		}
	}
}
