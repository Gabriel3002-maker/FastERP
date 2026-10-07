package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"testing/fstest"

	_ "github.com/lib/pq"
)

func TestLoadOrdenaPorVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"sql/0010_segunda.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
		"sql/0002_primera.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}
	all, err := load(fsys, "sql")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("esperaba 2 migraciones, got %d", len(all))
	}
	if all[0].Version != "0002" || all[1].Version != "0010" {
		t.Errorf("orden inesperado: %s, %s", all[0].Version, all[1].Version)
	}
	if all[0].Name != "primera" {
		t.Errorf("nombre inesperado: %q", all[0].Name)
	}
}

func TestLoadRechazaNombreInvalido(t *testing.T) {
	fsys := fstest.MapFS{
		"sql/mala.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}
	if _, err := load(fsys, "sql"); err == nil {
		t.Fatal("un fichero sin versión no debería pasar en silencio")
	}
}

func TestLoadRechazaVersionDuplicada(t *testing.T) {
	fsys := fstest.MapFS{
		"sql/0001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
		"sql/0001_b.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
	}
	if _, err := load(fsys, "sql"); err == nil {
		t.Fatal("dos ficheros con la misma versión deberían ser un error")
	}
}

// baseDePrueba construye una URL apuntando a otra base de datos conservando
// host, usuario y credenciales de FASTERP_TEST_DATABASE_URL.
func baseDePrueba(t *testing.T, nombre string) string {
	t.Helper()

	raw := os.Getenv("FASTERP_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("FASTERP_TEST_DATABASE_URL no definida; sin base de pruebas no hay migraciones que probar")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("FASTERP_TEST_DATABASE_URL no es una URL válida: %v", err)
	}
	u.Path = "/" + nombre
	return u.String()
}

// TestUpAplicaYNoRepite crea su propia base para no pisar a los tests del
// paquete module, que corren en paralelo contra el mismo Postgres.
func TestUpAplicaYNoRepite(t *testing.T) {
	const nombre = "fasterp_migrations_test"

	admin, err := sql.Open("postgres", baseDePrueba(t, "postgres"))
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
	database, err := sql.Open("postgres", baseDePrueba(t, nombre))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("no se pudo conectar a la base nueva: %v", err)
	}

	all, err := load(sqlFS, sqlDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no hay ninguna migración embebida: el paquete no sirve para nada")
	}

	if err := Up(ctx, database); err != nil {
		t.Fatalf("primera pasada: %v", err)
	}
	trasPrimera := contarAplicadas(t, database)
	if trasPrimera != len(all) {
		t.Errorf("aplicadas=%d, esperaba %d (cada migración embebida debe quedar registrada)", trasPrimera, len(all))
	}

	// El esquema del baseline existe y es utilizable.
	for _, tabla := range []string{
		"tenants", "users", "user_permissions", "roles", "role_permissions", "user_roles",
		"refresh_tokens", "installed_modules", "contacts",
	} {
		if _, err := database.ExecContext(ctx, "SELECT * FROM "+tabla+" LIMIT 0"); err != nil {
			t.Errorf("la tabla %s no quedó creada: %v", tabla, err)
		}
	}
	// user_permissions gana o resta ampliado en 0002: la columna allow tiene
	// que existir ya, no basta con que la tabla esté.
	if _, err := database.ExecContext(ctx, "SELECT allow FROM user_permissions LIMIT 0"); err != nil {
		t.Errorf("user_permissions.allow no quedó creada: %v", err)
	}
	if _, err := database.ExecContext(ctx, "SELECT * FROM schema_migrations LIMIT 0"); err != nil {
		t.Errorf("falta schema_migrations: %v", err)
	}

	// Segunda pasada: sin errores y sin duplicar registros. Es lo que hace
	// models.AutoMigrate en cada arranque.
	if err := Up(ctx, database); err != nil {
		t.Fatalf("segunda pasada: %v", err)
	}
	if trasSegunda := contarAplicadas(t, database); trasSegunda != trasPrimera {
		t.Errorf("la segunda pasada cambió el registro: %d -> %d", trasPrimera, trasSegunda)
	}
}

func contarAplicadas(t *testing.T, database *sql.DB) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("no se pudo contar schema_migrations: %v", err)
	}
	return n
}
