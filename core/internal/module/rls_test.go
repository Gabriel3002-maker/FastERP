package module

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// El aislamiento por tenant tiene dos capas: el WHERE tenant_id que pone el
// SDK, y la política de RLS. La segunda es la que impide que un WHERE olvidado
// —el de un handler nuevo, el de un script, el de una ruta de escritura que
// hace DDL— devuelva filas de otro tenant.
//
// Para que esa segunda capa exista hace falta FORCE. El rol con el que conecta
// la aplicación es el dueño de las tablas que crea (tiene CREATE ON SCHEMA
// public), y PostgreSQL no evalúa las políticas para el dueño: sin FORCE,
// ENABLE ROW LEVEL SECURITY no hace absolutamente nada.
//
// Estos tests hay que leerlos como una afirmación sobre el esquema, no sobre
// el SQL que se genera. El test de abajo necesita una base real porque el
// defecto no se ve en el DDL: el DDL era válido, solo que no hacía nada.

const tenantA = "11111111-1111-1111-1111-111111111111"
const tenantB = "22222222-2222-2222-2222-222222222222"

// El DDL de toda tabla tenant-scoped tiene que llevar FORCE. Sin BD: esto corre
// siempre y es lo que frena a quien quite el FORCE por cleanersía.
func TestRLSForzaLaPolitica(t *testing.T) {
	stmts := RLS("mod_x_y")
	joined := strings.Join(stmts, "\n")

	if !strings.Contains(joined, "ENABLE ROW LEVEL SECURITY") {
		t.Error("falta ENABLE ROW LEVEL SECURITY")
	}
	if !strings.Contains(joined, "FORCE ROW LEVEL SECURITY") {
		t.Error("falta FORCE ROW LEVEL SECURITY: sin él la política no se evalúa " +
			"porque la aplicación es la dueña de la tabla")
	}

	// El orden importa: la política no sirve de nada sobre una tabla sin RLS
	// activo, y el FORCE tiene que existir antes de que haya filas.
	var idxEnable, idxForce, idxPolicy = -1, -1, -1
	for i, s := range stmts {
		up := strings.ToUpper(s)
		switch {
		case strings.Contains(up, "ENABLE ROW LEVEL SECURITY"):
			idxEnable = i
		case strings.Contains(up, "FORCE ROW LEVEL SECURITY"):
			idxForce = i
		case strings.Contains(up, "CREATE POLICY"):
			idxPolicy = i
		}
	}
	if idxEnable < 0 || idxForce < 0 || idxPolicy < 0 {
		t.Fatalf("faltan sentencias: enable=%d force=%d policy=%d", idxEnable, idxForce, idxPolicy)
	}
	if !(idxEnable < idxPolicy && idxForce < idxPolicy) {
		t.Errorf("RLS debe activarse y forzarse antes de crear la política: %v", stmts)
	}
}

// La tabla de historial es donde aterriza el estado anterior y posterior de cada
// cambio. Es el registro de auditoría del ERP, y sin FORCE su política tampoco
// se aplica.
func TestHistoryDDLForzaLaPolitica(t *testing.T) {
	stmts := HistoryDDL("x", "y")

	var sawForce bool
	for _, s := range stmts {
		if strings.Contains(strings.ToUpper(s.SQL), "FORCE ROW LEVEL SECURITY") {
			sawForce = true
		}
	}
	if !sawForce {
		t.Error("HistoryDDL no lleva FORCE ROW LEVEL SECURITY: el historial de estados " +
			"queda sin aislar por tenant")
	}
}

// La política tiene que tolerar una conexión sin tenant. El pool compartido se
// usa para el DDL y no fija el GUC; sin missing_ok=true, current_setting
// revienta con "unrecognized configuration parameter" en vez de devolver cero
// filas. Falla cerrado, que es lo que se quiere.
func TestPoliticaRLSToleraConexionSinTenant(t *testing.T) {
	joined := strings.Join(RLS("mod_x_y"), "\n")
	if !strings.Contains(joined, "current_setting('app.tenant_id', true)") {
		t.Error("la política debería leer app.tenant_id con missing_ok=true")
	}
	if !strings.Contains(joined, "NULLIF") {
		t.Error("la política debería pasar el GUC por NULLIF para que '' no castee a uuid")
	}
}

// La prueba de verdad, contra Postgres: dos tenants, y el cruce tiene que dar
// cero filas.
//
// El matiz que hace este test necesario es el rol. Si la conexión fuera
// superusuario la política no se aplicaría con ni con FORCE, y el test
// pasaría sin decir nada — estaría probando superusuario en vez de RLS. Por eso
// se comprueba antes que el rol no se salte las políticas: si lo hace, el test
// se salta en vez de dar un falso verde.
func TestRLSImpideLeerOtroTenant(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	m := mod(t, modPrueba)
	table := "mod_" + m.Name + "_cosa"
	cleanupTable(t, db, table)
	t.Cleanup(func() { cleanupTable(t, db, table) })

	if err := ApplySchema(ctx, m, db); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}

	exigeRLSVerificable(t, db, table)

	exigeRLSVerificable(t, db, table)

	// Una sola conexión para toda la prueba. app.tenant_id es un GUC de sesión:
	// si el pool devolviera otra conexión física entre el set_config y el
	// SELECT, el test mediría el contexto equivocado y pasaría sin comprobar
	// nada. Es el mismo motivo por el que AcquireConn entrega una conexión
	// dedicada al handler.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()

	ver := func(tenant string) []string {
		t.Helper()
		if _, err := conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tenant); err != nil {
			t.Fatalf("set_config: %v", err)
		}
		rows, err := conn.QueryContext(ctx, "SELECT nombre FROM "+table+" ORDER BY nombre")
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	insertar := func(tenant, nombre string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tenant); err != nil {
			t.Fatalf("set_config: %v", err)
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO "+table+" (tenant_id, nombre, cantidad) VALUES ($1, $2, 1)", tenant, nombre,
		); err != nil {
			t.Fatalf("no se pudo insertar para %s: %v", tenant, err)
		}
	}

	// Sembrar como lo hace producción: cada fila se escribe con su tenant ya
	// fijado en la conexión. Con FORCE el WITH CHECK también aplica al insert, y
	// es justo por eso que no se puede plantar una fila ajena.
	insertar(tenantA, "secreto-A")
	insertar(tenantB, "secreto-B")

	// Cada tenant ve lo suyo y solo lo suyo. Las dos comprobaciones juntas
	// demuestra que ambas filas existen —si no, la segunda pasaría por el motivo
	// equivocado— y que ninguna se ve desde la otra sesión.
	if v := ver(tenantA); len(v) != 1 || v[0] != "secreto-A" {
		t.Errorf("con el tenant A fijado vieron %v; solo debería verse la fila de A", v)
	}
	if v := ver(tenantB); len(v) != 1 || v[0] != "secreto-B" {
		t.Errorf("con el tenant B fijado vieron %v; solo debería verse la fila de B", v)
	}
}

// exigeRLSVerificable aborta el test, no la suite, si la conexión no es capaz
// de demostrar nada. Sin esto un test de RLS puede pasar verde porque el rol se
// salta las políticas, y un verde que no significa nada es peor que un rojo.
func exigeRLSVerificable(t *testing.T, db *sql.DB, table string) {
	t.Helper()

	var super, bypass bool
	if err := db.QueryRow(
		"SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user",
	).Scan(&super, &bypass); err != nil {
		t.Fatalf("no se pudo leer pg_roles: %v", err)
	}
	if super || bypass {
		t.Skipf("el rol de la conexión puede saltarse RLS (superuser=%v bypassrls=%v); "+
			"con ese rol la política no se aplica y el test no mediría nada. "+
			"Usa un rol NOSUPERUSER NOBYPASSRLS.", super, bypass)
	}

	// Las dos banderas por separado, para que el fallo diga cuál falta.
	var enable, force bool
	if err := db.QueryRow(
		"SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = $1",
		table,
	).Scan(&enable, &force); err != nil {
		t.Fatalf("no se pudo leer pg_class: %v", err)
	}
	if !enable {
		t.Fatalf("%s no tiene ENABLE ROW LEVEL SECURITY: no hay política que evalúe", table)
	}
	if !force {
		t.Fatalf("%s tiene ENABLE pero no FORCE: la conexión es la dueña de la tabla, "+
			"así que PostgreSQL no evalúa ninguna política y el aislamiento depende "+
			"solo del WHERE tenant_id. El resto de este test no significaría nada.", table)
	}
}

// Un INSERT con el tenant B mientras el contexto está fijado a A tiene que
// fallar. El USING de la política filtra la lectura; el WITH CHECK es lo que
// impide escribir una fila que después nadie sería capaz de leer, y que otro
// tenant vería al no tener filtro.
func TestRLSImpideEscribirEnOtroTenant(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	m := mod(t, modPrueba)
	table := "mod_" + m.Name + "_cosa"
	cleanupTable(t, db, table)
	t.Cleanup(func() { cleanupTable(t, db, table) })

	if err := ApplySchema(ctx, m, db); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}

	exigeRLSVerificable(t, db, table)
	if _, err := db.Exec("SELECT set_config('app.tenant_id', $1, false)", tenantA); err != nil {
		t.Fatalf("set_config: %v", err)
	}

	_, err := db.Exec(
		"INSERT INTO "+table+" (tenant_id, nombre) VALUES ($1, 'infiltrado')", tenantB)
	if err == nil {
		t.Fatal("se pudo escribir una fila del tenant B con el contexto en A: " +
			"falta WITH CHECK en la política")
	}
}
