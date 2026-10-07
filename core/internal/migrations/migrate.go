// Package migrations aplica las migraciones versionadas del esquema del core.
//
// El diseño es deliberadamente pequeño. Cada migración es un fichero SQL
// embebido en el binario con el formato NNNN_nombre.sql; el orden es el del
// número y cada una se ejecuta una sola vez, dentro de su propia transacción,
// dejando constancia en schema_migrations. Si algo falla, la transacción se
// deshace y el proceso no arranca: no hay forma de quedar a medias.
//
// Lo que NO hace este paquete es reconciliar el esquema de los módulos. Eso
// vive en internal/module a partir de los manifests, porque el esquema de un
// módulo no lo decide este proceso sino quien lo instala. Aquí solo va el
// esquema del core, que sí es de esta base y sí cambia por código nuestro.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"path"
	"regexp"
	"sort"
)

//go:embed sql/*.sql
var sqlFS embed.FS

// sqlDir es el directorio dentro del paquete donde viven los ficheros.
const sqlDir = "sql"

// filePattern exige NNNN_nombre_en_minusculas.sql.
//
// Un nombre que no encaje es un error y no un fichero que se salta: una
// migración que se ignora en silencio y no avisa es exactamente el fallo que
// existe este paquete para evitar.
var filePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migration es un fichero pendiente de aplicar.
type Migration struct {
	Version string
	Name    string
	SQL     string
}

// load ordena por versión y rechaza duplicados.
func load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer %s: %w", dir, err)
	}

	out := make([]Migration, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := filePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("nombre de migración inválido: %q (formato esperado 0001_nombre.sql)", e.Name())
		}
		version, name := m[1], m[2]
		if seen[version] {
			return nil, fmt.Errorf("versión %s duplicada: %s", version, e.Name())
		}
		seen[version] = true

		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer %s: %w", e.Name(), err)
		}
		out = append(out, Migration{Version: version, Name: name, SQL: string(body)})
	}

	// El orden es el de la versión, no el de ReadDir: el sistema de ficheros
	// de Windows no ordena como el de Linux y el binario debe migrar igual
	// en los dos.
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

// Up aplica todas las migraciones pendientes en orden.
//
// Es idempotente: se puede llamar en cada arranque sin coste, que es justo lo
// que hace models.AutoMigrate. El registro de cada versión es la última
// sentencia de su transacción, así que "aplicada" y "hecha" significan lo
// mismo.
func Up(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("no se pudo crear schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, database)
	if err != nil {
		return err
	}

	all, err := load(sqlFS, sqlDir)
	if err != nil {
		return err
	}

	pending := 0
	for _, m := range all {
		if applied[m.Version] {
			continue
		}
		if err := applyOne(ctx, database, m); err != nil {
			return fmt.Errorf("migración %s_%s: %w", m.Version, m.Name, err)
		}
		log.Printf("[DB] Migración aplicada: %s_%s", m.Version, m.Name)
		pending++
	}

	if pending == 0 {
		log.Printf("[DB] Esquema al día: %d migraciones ya aplicadas", len(all))
	}
	return nil
}

// Applied devuelve las versiones ya registradas.
func Applied(ctx context.Context, database *sql.DB) (map[string]bool, error) {
	if _, err := database.ExecContext(ctx, createMigrationsTable); err != nil {
		return nil, fmt.Errorf("no se pudo crear schema_migrations: %w", err)
	}
	return appliedVersions(ctx, database)
}

func appliedVersions(ctx context.Context, database *sql.DB) (map[string]bool, error) {
	rows, err := database.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer schema_migrations: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func applyOne(ctx context.Context, database *sql.DB, m Migration) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Inofensivo tras un Commit: devuelve ErrTxDone y lo ignora el caller.
	defer tx.Rollback()

	// lib/pq manda el cuerpo entero en un solo mensaje de consulta simple,
	// así que el fichero puede llevar varias sentencias separadas por ';'.
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.Version, m.Name,
	); err != nil {
		return err
	}
	return tx.Commit()
}
