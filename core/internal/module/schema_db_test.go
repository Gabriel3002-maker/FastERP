package module

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// El reconciliador es lo que hace corto el ciclo de desarrollo: sin él,
// añadir un campo a module.yaml obligaría a escribir una migración a mano.
//
// Estos tests hablan con una base de verdad. Un DDL que parece correcto y no
// corre es la forma más cara de equivocarse, así que no se comprueba con
// strings.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("FASTERP_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("define FASTERP_TEST_DATABASE_URL para correr los tests de esquema")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mod(t *testing.T, yaml string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(yaml), "t/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

const modPrueba = `name: t
label: T
models:
  cosa:
    label: Cosa
    fields:
      - {name: nombre, type: string, required: true, sequence: 10}
      - {name: cantidad, type: integer, sequence: 20}
      - {name: notas, type: text, sequence: 30}
`

// El ciclo completo: crear, y luego añadir un campo editando el manifest.
func TestApplyCreaLaTabla(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m := mod(t, modPrueba)

	cleanup(t, db, "t", "cosa")
	if err := ApplySchema(ctx, m, db); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}

	cols := columnas(t, db, "mod_t_cosa")
	for _, want := range []string{"id", "tenant_id", "nombre", "cantidad", "notas", "created_at", "updated_at"} {
		if !cols[want] {
			t.Errorf("falta la columna %s; hay %v", want, claves(cols))
		}
	}
}

// Esta es la prueba que justifica todo el diseño: editas el YAML, guardas, y la
// columna aparece. Sin una migración escrita a mano.
func TestApplyAñadeLaColumnaDeUnCampoNuevo(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cleanup(t, db, "t", "cosa")

	if err := ApplySchema(ctx, mod(t, modPrueba), db); err != nil {
		t.Fatalf("ApplySchema inicial: %v", err)
	}

	// Se edita el manifest: aparece un campo.
	editado := mod(t, `name: t
label: T
models:
  cosa:
    label: Cosa
    fields:
      - {name: nombre, type: string, required: true, sequence: 10}
      - {name: cantidad, type: integer, sequence: 20}
      - {name: notas, type: text, sequence: 30}
      - {name: telefono, type: phone, sequence: 25}
`)
	if err := ApplySchema(ctx, editado, db); err != nil {
		t.Fatalf("ApplySchema tras editar: %v", err)
	}

	cols := columnas(t, db, "mod_t_cosa")
	if !cols["telefono"] {
		t.Errorf("la columna nueva no apareció; hay %v", claves(cols))
	}
	// Y el tipo es el que toca, no un VARCHAR(255) por el camino.
	if got := tipoColumna(t, db, "mod_t_cosa", "telefono"); got != "character varying(50)" {
		t.Errorf("telefono es %q, se esperaba VARCHAR(50)", got)
	}
}

// Aplicar dos veces no falla: es lo que pasa en cada arranque.
func TestApplyEsIdempotente(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cleanup(t, db, "t", "cosa")
	m := mod(t, modPrueba)

	for i := 0; i < 3; i++ {
		if err := ApplySchema(ctx, m, db); err != nil {
			t.Fatalf("ApplySchema (pasada %d): %v", i, err)
		}
	}
}

// Un default es un dato, y acaba siendo un dato en la base. Esta es la
// comprobación que cierra el círculo con los tests de sqlLiteral: aquí Postgres
// dice que el literal es válido.
func TestApplyDejaElDefaultComoDato(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cleanup(t, db, "t", "cosa")

	if err := ApplySchema(ctx, mod(t, `name: t
label: T
models:
  cosa:
    fields:
      - {name: nombre, type: string, required: true, sequence: 10}
      - {name: estado, type: enum, options: [borrador, activo], default: borrador, sequence: 20}
      - {name: intentos, type: integer, default: 0, sequence: 30}
      - {name: activo, type: boolean, default: true, sequence: 40}
      - {name: apodo, type: string, default: "O'Brien", sequence: 50}
`), db); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}

	// Se inserta sin dar valores: los defaults tienen que salir solos.
	var tenant = "00000000-0000-0000-0000-000000000001"
	conn := conTenant(t, db, tenant)
	_, err := conn.ExecContext(ctx,
		"INSERT INTO mod_t_cosa (tenant_id, nombre) VALUES ($1, $2) RETURNING estado, intentos, activo, apodo",
		tenant, "x")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var (
		estado   string
		intentos int
		activo   bool
		apodo    string
	)
	err = conn.QueryRowContext(ctx,
		"SELECT estado, intentos, activo, apodo FROM mod_t_cosa WHERE tenant_id = $1 AND nombre = 'x'",
		tenant).Scan(&estado, &intentos, &activo, &apodo)
	if err != nil {
		t.Fatal(err)
	}
	if estado != "borrador" || intentos != 0 || !activo {
		t.Errorf("defaults = %q %d %v", estado, intentos, activo)
	}
	// El apostrophe sobrevive: se duplicó al cotizar y Postgres lo leyó como
	// una comilla dentro del texto, no como el fin de la cadena.
	if apodo != "O'Brien" {
		t.Errorf("apodo = %q, se esperaba O'Brien", apodo)
	}
}

// La política de RLS tiene que existir y tener en cuenta el tenant. Sin esto, un
// WHERE olvidado devuelve filas de otro tenant.
func TestApplyDejaRLSAislandoElTenant(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cleanup(t, db, "t", "cosa")

	if err := ApplySchema(ctx, mod(t, modPrueba), db); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}

	var enabled bool
	if err := db.QueryRowContext(ctx,
		"SELECT relrowsecurity FROM pg_class WHERE relname = 'mod_t_cosa'").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("RLS no quedó habilitada")
	}

	var def string
	err := db.QueryRowContext(ctx,
		`SELECT pg_get_expr(polqual, polrelid) FROM pg_policy WHERE polrelid = 'mod_t_cosa'::regclass`).Scan(&def)
	if err != nil {
		t.Fatalf("no hay política RLS: %v", err)
	}
	if !contains(def, "tenant_id") {
		t.Errorf("la política no menciona tenant_id: %s", def)
	}
}

// Un módulo con un default malicioso no puede ejecutar SQL: el DDL se genera,
// Postgres lo acepta, y sigue siendo una tabla.
func TestApplyNoEjecutaElDefault(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cleanup(t, db, "t", "cosa")
	cleanupTable(t, db, "mod_z_z")

	if err := ApplySchema(ctx, mod(t, `name: t
label: T
models:
  cosa:
    fields:
      - {name: nombre, type: string, sequence: 10}
      - {name: notas, type: text, sequence: 20}
      - {name: trampa, type: string, default: "x'); DROP TABLE mod_z_z; --", sequence: 30}
`), db); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}

	// mod_z_z sigue existiendo: la sentencia era un literal, no un DROP.
	var exists bool
	if err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'mod_z_z')").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("el default inyectó DDL")
	}

	// Y el valor es el texto completo, cotizado.
	var def string
	if err := db.QueryRowContext(ctx,
		`SELECT column_default FROM information_schema.columns
		 WHERE table_name = 'mod_t_cosa' AND column_name = 'trampa'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !contains(def, "DROP TABLE") {
		t.Errorf("el default debería haberse guardado como texto, es %q", def)
	}
}

func cleanup(t *testing.T, db *sql.DB, module, model string) {
	t.Helper()
	cleanupTable(t, db, "mod_"+module+"_"+model)
}

func cleanupTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if !safeIdent(table) {
		t.Fatalf("nombre de tabla inseguro en el test: %q", table)
	}
	// El DROP va con CASCADE para llevarse los índices y las políticas.
	if _, err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE"); err != nil {
		t.Logf("limpieza de %s: %v", table, err)
	}
	time.Sleep(10 * time.Millisecond)
}

func columnas(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(
		`SELECT column_name FROM information_schema.columns WHERE table_name = $1`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[c] = true
	}
	return out
}

func tipoColumna(t *testing.T, db *sql.DB, table, column string) string {
	t.Helper()
	var tipo string
	err := db.QueryRow(
		`SELECT data_type || COALESCE('(' || character_maximum_length || ')', '')
		 FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
		table, column).Scan(&tipo)
	if err != nil {
		t.Fatal(err)
	}
	return tipo
}

func claves(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// conTenant fija el contexto de tenant en una conexión dedicada y la devuelve.
//
// Hace falta desde que las tablas de módulo llevan FORCE ROW LEVEL SECURITY:
// con la política activa, escribir una fila de un tenant sin tener ese tenant
// fijado en la conexión es un INSERT rechazado. No es un estorbo del test — es
// exactamente lo que hay que impedir, y por eso el test tiene que escribir
// como lo hace el middleware.
//
// Se usa una conexión dedicada y no el pool a propósito: app.tenant_id es un
// GUC de sesión, y el pool puede devolver otra conexión física entre el
// set_config y el INSERT, con lo que el test mediría el contexto equivocado.
func conTenant(t *testing.T, db *sql.DB, tenant string) *sql.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tenant); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	return conn
}
