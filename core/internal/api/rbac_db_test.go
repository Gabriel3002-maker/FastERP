package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/migrations"
	"github.com/fasterp/backend/internal/module"
)

// Los tests de RBAC puro (TestPermissionVerdictAllow) protegen la precedencia
// sin base de datos. Este fichero hace la parte que aquél no puede: que la
// consulta real de permissionVerdict devuelva exactamente esos hechos contra
// Postgres, con el RLS y la precedencia funcionando.
//
// Tres afirmaciones que aquí son carga útil y no formalidad:
//
//  1. El default-deny depende de que EXISTA una fila en user_roles del tenant
//     que pregunta. Sin RLS, esa fila podría ser de otro tenant y el modo
//     "gestionado" se encendería para todos.
//  2. La precedencia "deny directo gana al rol" se decide en Go con allow().
//     El test de la matriz la cubre; aquí se cubre que los hechos que llegan
//     desde SQL son los esperados (managed, directGrant, directDeny, ...).
//  3. El aislamiento es la defensa real: un usuario del tenant B no puede leer
//     los permisos del tenant C. Sin FORCE RLS la consulta los vería.
//
// El harness crea una base dedicada y la borra. Como los otros paquetes con
// tests de base (migrations, module) usan bases con otros nombres y corren en
// procesos separados, no hay pisos.
func TestRBACContraPostgres(t *testing.T) {
	const nombre = "fasterp_rbac_test"

	admin, err := sql.Open("postgres", baseRbac(t, "postgres"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer admin.Close()

	// FORCE corta conexiones huérfanas de un run anterior.
	if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", nombre)); err != nil {
		t.Skipf("no se pudo limpiar la base de pruebas: %v", err)
	}
	if _, err := admin.Exec("CREATE DATABASE " + nombre); err != nil {
		t.Skipf("la base no permite crear bases de datos (CREATEDB): %v", err)
	}
	t.Cleanup(func() {
		admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", nombre))
	})

	ctx := context.Background()
	database, err := sql.Open("postgres", baseRbac(t, nombre))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("no se pudo conectar a la base nueva: %v", err)
	}

	// AcquireConn y ApplyTenantRLS se apoyan en el pool global. Se apunta a la
	// base dedicada durante la prueba; el worker original vuelve al terminar.
	originalDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = originalDB })

	if err := migrations.Up(ctx, database); err != nil {
		t.Fatalf("migrations.Up: %v", err)
	}
	// El RLS de las tablas del core no lo aplican las migraciones sino el
	// arranque (models.AutoMigrate). Aquí se replica esa parte: sin FORCE, la
	// consulta del veredicto no vería nada y el test no probaría nada.
	for _, tabla := range []string{
		"contacts", "users", "user_permissions",
		"roles", "role_permissions", "user_roles",
		"installed_modules", "refresh_tokens",
	} {
		db.ApplyTenantRLS(tabla)
	}

	h := &Handler{}
	ventas := &module.ModuleInstance{
		Manifest: &module.Manifest{Name: "ventas"},
		Models: []module.ModelRegistration{
			{Manifest: &module.ModelDef{Name: "factura"}},
		},
	}
	factura := ventas.Model("factura")

	t.Run("modo compatible sin roles", func(t *testing.T) {
		seedTenant(t, database, tenantLibre, "libre", "Libre")
		seedUser(t, tenantLibre, userLegacy, "legacy")
		seedUser(t, tenantLibre, userTracked, "tracked")
		seedUserPerm(t, tenantLibre, userTracked, "ventas", "factura", "read", true)

		// Una instancia virgen deja pasar todo: el comportamiento histórico.
		if got := h.requirePermission(ctxapi(t, tenantLibre, userLegacy, false), ventas, factura, "read"); !got {
			t.Error("sin roles ni permisos, la lectura debería estar permitida")
		}

		// Una vez declarado el recurso, en modo compatible solo pasa lo declarado.
		if got := h.requirePermission(ctxapi(t, tenantLibre, userTracked, false), ventas, factura, "read"); !got {
			t.Error("el recurso declarado con allow directo debería dejar pasar la lectura")
		}
		if got := h.requirePermission(ctxapi(t, tenantLibre, userTracked, false), ventas, factura, "update"); got {
			t.Error("recurso declarado sin la acción: en modo compatible la acción debería estar denegada")
		}
	})

	t.Run("default-deny y precedencia", func(t *testing.T) {
		seedTenant(t, database, tenantGestionado, "gestionado", "Gestionado")
		seedRole(t, tenantGestionado, rolVentas, "Ventas", "Acceso a ventas")
		seedRolePerm(t, tenantGestionado, rolVentas, "ventas", "factura", "read")

		seedUser(t, tenantGestionado, userRol, "con-rol")
		seedUserRole(t, tenantGestionado, userRol, rolVentas)

		seedUser(t, tenantGestionado, userNada, "sin-rol")
		seedUser(t, tenantGestionado, userDeny, "deny-directo")
		seedUserRole(t, tenantGestionado, userDeny, rolVentas)
		seedUserPerm(t, tenantGestionado, userDeny, "ventas", "factura", "read", false)

		seedUser(t, tenantGestionado, userUpd, "grant-directo")
		seedUserRole(t, tenantGestionado, userUpd, rolVentas)
		seedUserPerm(t, tenantGestionado, userUpd, "ventas", "factura", "update", true)

		seedUser(t, tenantGestionado, userAdmin, "admin")

		// El rol concede exactamente la acción declarada.
		c := ctxapi(t, tenantGestionado, userRol, false)
		if got := h.requirePermission(c, ventas, factura, "read"); !got {
			t.Error("el rol con 'ventas:factura read' debería dejar pasar la lectura")
		}
		c = ctxapi(t, tenantGestionado, userRol, false)
		if got := h.requirePermission(c, ventas, factura, "update"); got {
			t.Error("default-deny: lo no concedido por el rol debería estar denegado")
		}
		// Con reg nil se pregunta a nivel de módulo: basta un permiso en el módulo.
		c = ctxapi(t, tenantGestionado, userRol, false)
		if got := h.requirePermission(c, ventas, nil, "read"); !got {
			t.Error("el rol con cualquier permiso del módulo debería abrir el nivel de módulo")
		}

		// El usuario sin rol, en un tenant gestionado, no entra: es la frontera
		// del default-deny y no depende de que el usuario tenga filas.
		c = ctxapi(t, tenantGestionado, userNada, false)
		if got := h.requirePermission(c, ventas, factura, "read"); got {
			t.Error("en un tenant con roles, el usuario sin rol no debería leer nada")
		}
		c = ctxapi(t, tenantGestionado, userNada, false)
		if got := h.requirePermission(c, ventas, nil, "read"); got {
			t.Error("en un tenant con roles, el usuario sin rol no debería entrar ni a nivel de módulo")
		}

		// El deny directo gana al rol concedido.
		c = ctxapi(t, tenantGestionado, userDeny, false)
		if got := h.requirePermission(c, ventas, factura, "read"); got {
			t.Error("el deny directo debería ganar al permiso del rol")
		}
		if c.Writer.Status() != http.StatusForbidden {
			t.Errorf("deny directo: el handler debería responder 403, obtuvo %d", c.Writer.Status())
		}

		// El grant directo abre lo que el rol no concede.
		c = ctxapi(t, tenantGestionado, userUpd, false)
		if got := h.requirePermission(c, ventas, factura, "update"); !got {
			t.Error("el grant directo debería abrir la acción aunque el rol no la conceda")
		}

		// is_admin pasa siempre, sin depender del rol.
		if got := h.requirePermission(ctxapi(t, tenantGestionado, userAdmin, true), ventas, factura, "update"); !got {
			t.Error("is_admin debería pasar cualquier acción")
		}
	})

	t.Run("aislamiento por tenant", func(t *testing.T) {
		seedTenant(t, database, tenantOtro, "otro", "Otro")
		seedRole(t, tenantOtro, rolOtro, "Creadores", "Crea facturas")
		seedRolePerm(t, tenantOtro, rolOtro, "ventas", "factura", "create")
		seedUser(t, tenantOtro, userOtro, "de-otro-tenant")
		seedUserRole(t, tenantOtro, userOtro, rolOtro)

		// Desde el propio tenant, el permiso existe y deja pasar.
		if got := h.requirePermission(ctxapi(t, tenantOtro, userOtro, false), ventas, factura, "create"); !got {
			t.Error("un usuario debería ver su propio rol dentro de su tenant")
		}

		// Desde un tenant gestionado ajeno, el mismo usuario no puede colgarse
		// del rol del otro tenant. Sin FORCE RLS, la consulta del veredicto vería
		// las user_roles de tenantOtro y este assert fallaría con allow()=true:
		// aquí es donde el RLS es la defensa y no un adorno.
		c := ctxapi(t, tenantGestionado, userOtro, false)
		if got := h.requirePermission(c, ventas, factura, "create"); got {
			t.Error("el rol de otro tenant no debería conceder permisos: el RLS no está aplicando")
		}
		if c.Writer.Status() != http.StatusForbidden {
			t.Errorf("rol de otro tenant: se esperaba 403, obtuvo %d", c.Writer.Status())
		}
	})
}

// Identificadores fijos para que las semillas sean estables entre runs.
const (
	tenantLibre      = "10000000-0000-0000-0000-000000000001"
	tenantGestionado = "10000000-0000-0000-0000-000000000002"
	tenantOtro       = "10000000-0000-0000-0000-000000000003"
	userLegacy       = "20000000-0000-0000-0000-000000000001"
	userTracked      = "20000000-0000-0000-0000-000000000002"
	userRol          = "20000000-0000-0000-0000-000000000003"
	userNada         = "20000000-0000-0000-0000-000000000004"
	userDeny         = "20000000-0000-0000-0000-000000000005"
	userUpd          = "20000000-0000-0000-0000-000000000006"
	userAdmin        = "20000000-0000-0000-0000-000000000007"
	userOtro         = "20000000-0000-0000-0000-000000000008"
	rolVentas        = "30000000-0000-0000-0000-000000000001"
	rolOtro          = "30000000-0000-0000-0000-000000000002"
)

// baseRbac construye una URL apuntando a otra base conservando host, usuario y
// credenciales de FASTERP_TEST_DATABASE_URL.
func baseRbac(t *testing.T, nombre string) string {
	t.Helper()

	raw := os.Getenv("FASTERP_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("FASTERP_TEST_DATABASE_URL no definida; sin base de pruebas no hay RBAC contra Postgres")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("FASTERP_TEST_DATABASE_URL no es una URL válida: %v", err)
	}
	u.Path = "/" + nombre
	return u.String()
}

// ctxapi monta un contexto de gin como el que dejarían los middleware: conexión
// fijada al tenant con su GUC, el user y su is_admin. La conexión se devuelve al
// pool al terminar el subtest.
func ctxapi(t *testing.T, tenantID, userID string, isAdmin bool) *gin.Context {
	t.Helper()

	conn, err := db.AcquireConn(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("no se pudo fijar la conexión al tenant %s: %v", tenantID, err)
	}
	t.Cleanup(func() { conn.Close() })

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/nada", nil)
	c.Set("db_conn", conn)
	c.Set("tenant_id", tenantID)
	c.Set("user_id", userID)
	c.Set("is_admin", isAdmin)
	return c
}

func seedTenant(t *testing.T, database *sql.DB, id, slug, name string) {
	t.Helper()
	// tenants es el registro de tenants y no tiene RLS: se siembra en el pool.
	if _, err := database.ExecContext(context.Background(),
		"INSERT INTO tenants (id, slug, name) VALUES ($1, $2, $3)", id, slug, name); err != nil {
		t.Fatalf("semilla: %v", err)
	}
}

// Las tablas con RLS se siembran dentro de una conexión fijada al tenant: la
// política tenant_isolation usa USING (tenante_isolation también) de check para
// INSERT, y sin app.tenant_id la fila se rechaza. Es la misma frontera que
// protege los handlers, aplicada a la semilla.
func seedUser(t *testing.T, tenantID, id, username string) {
	t.Helper()
	withTenant(t, tenantID, func(x db.QueryExecutor) error {
		_, err := x.ExecContext(context.Background(),
			"INSERT INTO users (id, tenant_id, username, email, password_hash, is_admin, active) VALUES ($1, $2, $3, $4, $5, false, true)",
			id, tenantID, username, username+"@test.local", "no-se-usa")
		return err
	})
}

func seedRole(t *testing.T, tenantID, id, name, description string) {
	t.Helper()
	withTenant(t, tenantID, func(x db.QueryExecutor) error {
		_, err := x.ExecContext(context.Background(),
			"INSERT INTO roles (id, tenant_id, name, description) VALUES ($1, $2, $3, $4)",
			id, tenantID, name, description)
		return err
	})
}

func seedRolePerm(t *testing.T, tenantID, roleID, module, model, action string) {
	t.Helper()
	withTenant(t, tenantID, func(x db.QueryExecutor) error {
		_, err := x.ExecContext(context.Background(),
			"INSERT INTO role_permissions (tenant_id, role_id, module, model, action) VALUES ($1, $2, $3, $4, $5)",
			tenantID, roleID, module, model, action)
		return err
	})
}

func seedUserRole(t *testing.T, tenantID, userID, roleID string) {
	t.Helper()
	withTenant(t, tenantID, func(x db.QueryExecutor) error {
		_, err := x.ExecContext(context.Background(),
			"INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1, $2, $3)",
			tenantID, userID, roleID)
		return err
	})
}

func seedUserPerm(t *testing.T, tenantID, userID, module, model, action string, allow bool) {
	t.Helper()
	withTenant(t, tenantID, func(x db.QueryExecutor) error {
		_, err := x.ExecContext(context.Background(),
			"INSERT INTO user_permissions (tenant_id, user_id, module, model, action, allow) VALUES ($1, $2, $3, $4, $5, $6)",
			tenantID, userID, module, model, action, allow)
		return err
	})
}

func withTenant(t *testing.T, tenantID string, fn func(db.QueryExecutor) error) {
	t.Helper()
	if err := db.WithTenant(context.Background(), tenantID, fn); err != nil {
		t.Fatalf("semilla: %v", err)
	}
}
