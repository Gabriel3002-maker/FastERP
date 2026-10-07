package api

import "testing"

// TestPermissionVerdictAllow fija la precedencia entera de la decisión de
// permisos: lo directo sobre el rol, la revocación sobre la concesión, y los
// dos modos de funcionamiento (compatible y default-deny).
//
// permissionVerdict es la única parte de RBAC sin base de datos: el SQL podrá
// cambiar de forma mientras el veredicto resultante sea este, y este test lo
// protege igual.
func TestPermissionVerdictAllow(t *testing.T) {
	casos := []struct {
		nombre string
		v      permissionVerdict
		want   bool
	}{
		// Instancia virgen (sin roles ni filas directas): todo permitido.
		{"instancia virgen", permissionVerdict{}, true},

		// Tenant con roles: default-deny. Lo no concedido está prohibido y el
		// usuario sin rol no entra.
		{"managed sin nada", permissionVerdict{managed: true}, false},
		{"managed sin rol", permissionVerdict{managed: true, resourceTracked: true}, false},
		{"grant por rol", permissionVerdict{managed: true, roleGranted: true}, true},

		// Lo directo gana al rol, por arriba y por abajo.
		{"deny directo pese al rol", permissionVerdict{managed: true, roleGranted: true, directDeny: true}, false},
		{"grant directo pese al deny del rol", permissionVerdict{managed: true, directGrant: true}, true},
		{"deny directo sin managed", permissionVerdict{directDeny: true}, false},

		// Modo compatible con el recurso declarado: solo lo declarado. Una fila
		// de revocación cuenta igual que una de otorgamiento.
		{"recurso declarado sin la accion", permissionVerdict{resourceTracked: true}, false},
		{"recurso declarado con grant", permissionVerdict{resourceTracked: true, directGrant: true}, true},
		{"recurso declarado con deny", permissionVerdict{resourceTracked: true, directDeny: true}, false},

		// Concesión por rol sin tenant managed: inalcanzable en la práctica (sin
		// user_roles no hay role_permissions de dónde colgar), pero es una
		// concesión y como tal se respeta.
		{"rol sin managed", permissionVerdict{roleGranted: true}, true},
	}

	for _, tc := range casos {
		if got := tc.v.allow(); got != tc.want {
			t.Errorf("%s: allow() = %v, se esperaba %v (verdicto: %+v)", tc.nombre, got, tc.want, tc.v)
		}
	}
}
