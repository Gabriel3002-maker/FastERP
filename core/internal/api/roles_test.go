package api

import (
	"strings"
	"testing"
)

// TestValidateRoleName fija lo que un nombre de rol acepta. Es la única
// frontera de validación de roles que no depende de la base: el resto del CRUD
// se decide con datos reales.
func TestValidateRoleName(t *testing.T) {
	casos := []struct {
		nombre string
		name   string
		wantOK bool
	}{
		{"normal", "Vendedor", true},
		{"con espacios", "Jefe de almacén", true},
		{"minusculas y guion", "logistica-2", true},
		{"vacio", "", false},
		{"solo espacios", "   ", false},
		{"demasiado largo", "rol-" + strings.Repeat("x", 100), false},
		{"con simbolo", "rol&admin", false},
	}

	for _, tc := range casos {
		if got := validateRoleName(tc.name); (got == "") != tc.wantOK {
			t.Errorf("%s: validateRoleName(%q) = %q, se esperaba válido=%v", tc.nombre, tc.name, got, tc.wantOK)
		}
	}
}

// TestValidAction fija las únicas cuatro acciones que existen. Cualquier otra
// provocaría permisos que el CRUD de datos nunca comprobaría.
func TestValidAction(t *testing.T) {
	for _, a := range []string{"read", "create", "update", "delete"} {
		if !validAction(a) {
			t.Errorf("validAction(%q) debería ser true", a)
		}
	}
	for _, a := range []string{"Read", "", "list", "drop"} {
		if validAction(a) {
			t.Errorf("validAction(%q) debería ser false", a)
		}
	}
}

// TestMenuModelName fija el formato del campo Model del menú: el catálogo de
// permisos guarda el nombre del manifest sin el prefijo de módulo, y el menú lo
// lleva con él. Si el formato cambiara, el filtro del sidebar preguntaría por
// un modelo que no existe y escondería módulos legibles.
func TestMenuModelName(t *testing.T) {
	casos := []struct{ in, want string }{
		{"ventas/factura", "factura"},
		{"ventas/contacto", "contacto"},
		{"factura", "factura"},
		{"", ""},
	}
	for _, tc := range casos {
		if got := menuModelName(tc.in); got != tc.want {
			t.Errorf("menuModelName(%q) = %q, se esperaba %q", tc.in, got, tc.want)
		}
	}
}
