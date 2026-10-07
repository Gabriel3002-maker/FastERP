package api

import (
	"strings"
	"testing"
)

// TestValidPassword fija el criterio de contraseña compartido por el alta
// inicial, el CRUD de usuarios y ChangePassword.
//
// El tope está en bytes y el regex en runes, que es justo la asimetría que
// convierte un 400 en un 500: un password de 19 emojis son 19 runes (pasa el
// regex) y 76 bytes (bcrypt devuelve ErrPasswordTooLong). Si alguien
// "simplifica" validPassword a solo el regex, este caso empieza a fallar.
func TestValidPassword(t *testing.T) {
	casos := []struct {
		nombre string
		pw     string
		want   bool
	}{
		{"corta", "abc123", false},
		{"justo ocho", "abc12345", true},
		{"justo 72 bytes", strings.Repeat("a", 72), true},
		{"73 bytes", strings.Repeat("a", 73), false},
		{"multibyte dentro del tope", "ñññññññññ", true},
		{"multibyte fuera del tope", strings.Repeat("🙂", 19), false},
		{"con espacios", "        x", true},
		{"con salto de linea", "abcdefgh\nij", false},
	}

	for _, tc := range casos {
		if got := validPassword(tc.pw); got != tc.want {
			t.Errorf("validPassword(%q) = %v, se esperaba %v (%s)", tc.pw, got, tc.want, tc.nombre)
		}
	}
}

// TestPatronesDeAlta recorre los tres regex que comparten el asistente de
// /setup y el CRUD de usuarios.
//
// Están en el mismo test a propósito: si solo uno validara, una cuenta válida
// al crearse podría rechazarse al editarla después, y eso es un bug que no
// avisa.
func TestPatronesDeAlta(t *testing.T) {
	t.Run("slug de tenant", func(t *testing.T) {
		for _, tc := range []struct {
			slug string
			want bool
		}{
			{"acme", true},
			{"a", false},     // mínimo dos caracteres
			{"1acme", false}, // no puede empezar por número
			{"Acme", false},  // minúsculas obligatorias
			{"ac me", false}, // sin espacios
			{"acme_customer-2", true},
			{"acme ", false}, // el ancla no deja cola
			{strings.Repeat("a", 50), true},
			{strings.Repeat("a", 51), false},
		} {
			if got := tenantSlugPattern.MatchString(tc.slug); got != tc.want {
				t.Errorf("tenantSlugPattern(%q) = %v, se esperaba %v", tc.slug, got, tc.want)
			}
		}
	})

	t.Run("usuario", func(t *testing.T) {
		for _, tc := range []struct {
			user string
			want bool
		}{
			{"admin", true},
			{"ad", false},         // mínimo tres
			{"admin user", false}, // sin espacios
			{"maria.garcia_1", true},
			{"Ünico", false}, // sin acentos ni letras ajenas al ASCII
			{strings.Repeat("a", 100), true},
			{strings.Repeat("a", 101), false},
		} {
			if got := usernamePattern.MatchString(tc.user); got != tc.want {
				t.Errorf("usernamePattern(%q) = %v, se esperaba %v", tc.user, got, tc.want)
			}
		}
	})

	t.Run("email", func(t *testing.T) {
		for _, tc := range []struct {
			email string
			want  bool
		}{
			{"maria@acme.es", true},
			{"maria@servidor", false}, // sin punto en el dominio
			{"maria @acme.es", false}, // sin espacios
			{"@acme.es", false},
			{"maria@@acme.es", false},
			{"a@b.c", true},
		} {
			if got := emailPattern.MatchString(tc.email); got != tc.want {
				t.Errorf("emailPattern(%q) = %v, se esperaba %v", tc.email, got, tc.want)
			}
		}
	})
}

// TestInvalidCredentials comprueba que el alta y la edición devuelven el mismo
// mensaje para el mismo error, y que el orden es usuario → email → contraseña.
//
// El único mensaje es el que ve la persona en el formulario, así que un
// desorden (por ejemplo validar la contraseña antes que el email) cambiaría lo
// que se ve en una misma pantalla según el campo que esté mal.
func TestInvalidCredentials(t *testing.T) {
	casos := []struct {
		nombre              string
		username, email, pw string
		want                string
	}{
		{"todo correcto", "maria", "maria@acme.es", "contrasena1", ""},
		{"usuario corto", "ma", "maria@acme.es", "contrasena1",
			"el usuario debe tener entre 3 y 100 caracteres (letras, números, punto, guion o guion bajo)"},
		{"usuario con espacios", "ma ria", "maria@acme.es", "contrasena1",
			"el usuario debe tener entre 3 y 100 caracteres (letras, números, punto, guion o guion bajo)"},
		{"email sin dominio", "maria", "maria@servidor", "contrasena1", "el email no es válido"},
		{"email demasiado largo", "maria", strings.Repeat("a", 250) + "@acme.es", "contrasena1", "el email no es válido"},
		{"contraseña corta", "maria", "maria@acme.es", "corta", "la contraseña debe tener entre 8 y 72 bytes"},
	}

	for _, tc := range casos {
		got := invalidCredentials(tc.username, tc.email, tc.pw)
		if got != tc.want {
			t.Errorf("%s: invalidCredentials() = %q, se esperaba %q", tc.nombre, got, tc.want)
		}
	}
}
