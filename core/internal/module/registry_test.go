package module

import "testing"

// TestAdminRouteNormalizaLasRutasDelMenu fija el invariante del que dependen los
// enlaces del menú lateral: toda ruta sale bajo /admin.
//
// Los manifests escriben la ruta a su manera y los dos formatos son válidos para
// quien los lee —"/ventas" y "/admin/ventas"—, pero solo la segunda existe en el
// router. Sin normalizar, el enlace del menú apunta a una ruta no registrada y el
// clic se lleva un 404 que no dice nada del menú.
func TestAdminRouteNormalizaLasRutasDelMenu(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "/admin"},
		{"/", "/admin"},
		{"   ", "/admin"},
		{"ventas", "/admin/ventas"},
		{"/ventas", "/admin/ventas"},
		{"/admin", "/admin"},
		{"/admin/ventas", "/admin/ventas"},
		{"//admin//ventas", "/admin/ventas"},
		// La trampa: empieza por "/admin" pero no está bajo el admin.
		{"/administracion", "/admin/administracion"},
		{"/admin-ventas", "/admin/admin-ventas"},
	} {
		if got := adminRoute(tc.in); got != tc.want {
			t.Errorf("adminRoute(%q) = %q, se esperaba %q", tc.in, got, tc.want)
		}
	}
}

// TestTrimRouteDeshaceElPrefijoDelAdmin comprueba que el par adminRoute/trimRoute
// se cancelan, que es lo que hace que un menú siga resolviendo a su módulo.
//
// Si TrimRoute dejara el prefijo, ModuleForPath compararía "/admin/ventas" contra
// una ruta ya normalizada y no encontraría nada: el menú se dibujaría pero cada
// enlace daría 404.
func TestTrimRouteDeshaceElPrefijoDelAdmin(t *testing.T) {
	for _, route := range []string{"/admin/ventas", "/ventas", "ventas"} {
		if got := trimRoute(adminRoute(route)); got != "/ventas" {
			t.Errorf("trimRoute(adminRoute(%q)) = %q, se esperaba %q", route, got, "/ventas")
		}
	}
}

func TestFilterMenuItemsByActiveModules(t *testing.T) {
	items := []MenuItem{
		{Module: "hello", Key: "home", Label: "Hello"},
		{Module: "contacts", Key: "contacts", Label: "Contacts"},
		{Module: "products", Key: "products", Label: "Products"},
	}

	filtered := filterMenuItemsByNames(items, map[string]bool{
		"hello":    true,
		"contacts": false,
		"products": true,
	})

	if len(filtered) != 2 {
		t.Fatalf("se esperaban 2 elementos activos, pero hay %d", len(filtered))
	}
	if filtered[0].Module != "hello" || filtered[1].Module != "products" {
		t.Fatalf("los elementos filtrados no son los esperados: %#v", filtered)
	}
}
