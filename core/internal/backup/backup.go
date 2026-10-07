// Package backup genera y restaura el paquete portable de un tenant:
// los datos en SQL plano y los ficheros subidos en resources/.
//
// El formato es zip:
//
//	backup-<slug>-<fecha>.zip
//	├── manifest.json   metadatos del tenant y sus módulos
//	├── database.sql    INSERTs de todas las tablas del tenant
//	└── resources/      copia de UploadDir (media, products, …)
//
// No incluye código ni vistas: la UI se deduce del manifest y se regenera en
// el destino, así que un backup no puede dejar vistas rotas como en Odoo.
package backup

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fasterp/backend/internal/db"
	"github.com/lib/pq"
)

// AppVersion es la versión del formato, no de la app. Si cambia el layout del
// zip, se sube para que el restore rechace paquetes incompatibles con un
// mensaje claro en vez de un fallo a medias.
const AppVersion = "1.0"

// ModuleRef es un módulo instalado según el backup.
type ModuleRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Manifest es lo primero que se lee al restaurar: identifica el tenant de
// origen y sus módulos sin tocar la base de datos.
type Manifest struct {
	AppVersion string      `json:"app_version"`
	TenantID   string      `json:"tenant_id"`
	Name       string      `json:"name"`
	Slug       string      `json:"slug"`
	CreatedAt  string      `json:"created_at"`
	Modules    []ModuleRef `json:"modules"`
}

// tenantScopedTables devuelve las tablas con tenant_id, menos refresh_tokens
// (se descartan a propósito: restaurar sesiones ajenas impediría el re-login
// limpio tras una migración/robo de backup).
func tenantScopedTables(ctx context.Context, qe db.QueryExecutor) ([]string, error) {
	rows, err := qe.QueryContext(ctx, `
		SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'tenant_id'
		  AND table_name <> 'refresh_tokens'
		ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DumpTable vuelca una tabla a INSERTs. Todos los valores salen como texto con
// cast explícito: Postgres los parsea al tipo de la columna en el destino, que
// es justo lo que hace un literal sin tipar al insertar. Así no hace falta
// conocer el tipo de cada columna aquí, y jsonb, arrays o bytea viajan bien.
func DumpTable(ctx context.Context, qe db.QueryExecutor, table, where string, args ...any) (string, error) {
	cols, err := columnsOf(ctx, qe, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", nil
	}

	// Los identificadores pasan por pq.QuoteIdentifier, no por %q ni por
	// comillas a mano: %q escapa como un literal de Go, y un nombre con una
	// comilla doble produce SQL con un backslash que Postgres no acepta.
	// QuoteIdentifier duplica la comilla, que es la regla del parser.
	casts := make([]string, len(cols))
	for i, c := range cols {
		casts[i] = pq.QuoteIdentifier(c) + "::text"
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(casts, ", "), pq.QuoteIdentifier(table), where)
	rows, err := qe.QueryContext(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("backup: dump %s: %w", table, err)
	}
	defer rows.Close()

	var b strings.Builder
	quotedCols := make([]string, len(cols))
	for i, c := range cols {
		quotedCols[i] = pq.QuoteIdentifier(c)
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		b.WriteString("INSERT INTO " + pq.QuoteIdentifier(table) + " (" + strings.Join(quotedCols, ", ") + ") VALUES (")
		for i, v := range vals {
			if i > 0 {
				b.WriteString(", ")
			}
			if v.Valid {
				b.WriteString("'" + strings.ReplaceAll(v.String, "'", "''") + "'")
			} else {
				b.WriteString("NULL")
			}
			vals[i] = sql.NullString{}
		}
		b.WriteString(");\n")
	}
	return b.String(), rows.Err()
}

func columnsOf(ctx context.Context, qe db.QueryExecutor, table string) ([]string, error) {
	rows, err := qe.QueryContext(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// DumpSQL genera el script completo del tenant: primero tenants (sin
// RLS) y luego el resto de tablas con tenant_id. Sin DDL: en el destino las
// tablas ya existen porque el arranque las reconcilia desde el manifest.
func DumpSQL(ctx context.Context, qe db.QueryExecutor, tenantID string) (string, error) {
	var b strings.Builder
	b.WriteString("-- fasterp backup 1.0\n")
	if tenants, err := DumpTable(ctx, qe, "tenants", "id = $1", tenantID); err == nil {
		b.WriteString(tenants)
	} else {
		return "", err
	}
	tables, err := tenantScopedTables(ctx, qe)
	if err != nil {
		return "", err
	}
	for _, t := range tables {
		dump, err := DumpTable(ctx, qe, t, "tenant_id = $1", tenantID)
		if err != nil {
			return "", err
		}
		b.WriteString(dump)
	}
	return b.String(), nil
}

// StripTenantsInsert elimina del dump la línea de tenants: el tenant destino
// ya existe (si no, no se podría llamar a restore), y reinsertarlo rompería
// por PK si coincide el id o por UNIQUE si coincide el slug con otro tenant.
func StripTenantsInsert(sqlText string) string {
	var lines []string
	for _, ln := range strings.Split(sqlText, "\n") {
		if strings.HasPrefix(ln, `INSERT INTO "tenants"`) {
			continue
		}
		lines = append(lines, ln)
	}
	return strings.Join(lines, "\n")
}

// LoadManifest lee y valida el manifest.json de un zip de backup.
func LoadManifest(zr *zip.Reader) (*Manifest, error) {
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		var m Manifest
		if err := json.NewDecoder(rc).Decode(&m); err != nil {
			return nil, fmt.Errorf("manifest.json inválido: %w", err)
		}
		if m.AppVersion != AppVersion {
			return nil, fmt.Errorf("versión de backup %q no compatible (se espera %q)", m.AppVersion, AppVersion)
		}
		if m.TenantID == "" {
			return nil, fmt.Errorf("manifest.json sin tenant_id")
		}
		return &m, nil
	}
	return nil, fmt.Errorf("manifest.json no encontrado en el zip")
}

// ReadDatabaseSQL extrae database.sql del zip.
func ReadDatabaseSQL(zr *zip.Reader) (string, error) {
	for _, f := range zr.File {
		if f.Name != "database.sql" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		return string(b), err
	}
	return "", fmt.Errorf("database.sql no encontrado en el zip")
}

// WriteZip escribe el paquete completo: manifest, database.sql y todos los
// ficheros de UploadDir bajo resources/.
func WriteZip(ctx context.Context, qe db.QueryExecutor, tenantID, name, slug, uploadsDir string, modules []ModuleRef, w io.Writer) error {
	dump, err := DumpSQL(ctx, qe, tenantID)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)

	m := Manifest{
		AppVersion: AppVersion,
		TenantID:   tenantID,
		Name:       name,
		Slug:       slug,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Modules:    modules,
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(mw).Encode(m); err != nil {
		return err
	}

	dw, err := zw.Create("database.sql")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(dw, dump); err != nil {
		return err
	}

	err = filepath.WalkDir(uploadsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(uploadsDir, path)
		if err != nil {
			return err
		}
		// Solo el subárbol del tenant: uploads/<kind>/<tenantID>/<fichero>.
		// Filestore segregado por tenant; exportar todo sería exportar a los
		// demás tenants también.
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 3 || parts[1] != tenantID {
			return nil
		}
		fw, err := zw.Create("resources/" + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(fw, f)
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return zw.Close()
}

// ExtractResources descomprime resources/* dentro de dest, rechazando
// rutas que se salgan del directorio (zip-slip).
func ExtractResources(zr *zip.Reader, dest string) (int, error) {
	count := 0
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "resources/") || f.FileInfo().IsDir() {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(f.Name, "resources/")))
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return count, fmt.Errorf("entrada sospechosa en resources/: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		rc, err := f.Open()
		if err != nil {
			return count, err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return count, err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return count, err
		}
		out.Close()
		rc.Close()
		count++
	}
	return count, nil
}

// PurgeTenant borra los datos del tenant sin tocar su fila en tenants: anotarla
// en el INSERT del dump rompería la FK de users, y sin ella el tenant destino
// ya no existe. En su lugar se borran las filas enlazadas y las mod_*.
func PurgeTenant(ctx context.Context, tx db.QueryExecutor, tenantID string) error {
	// Hijos primero para no depender del cascada al borrar: si el FORCE RLS
	// cortara el ON DELETE CASCADE (los triggers de integridad corren como el
	// dueño, que aquí también está sujeto a la política), las tablas intermedias
	// quedarían huérfanas sin que nadie avise.
	for _, t := range []string{
		"user_roles", "role_permissions", "user_permissions",
		"users", "roles", "installed_modules", "refresh_tokens",
	} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", pq.QuoteIdentifier(t)), tenantID); err != nil {
			return fmt.Errorf("purge %s: %w", t, err)
		}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema='public' AND table_name LIKE 'mod\_%'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, t)
	}
	rows.Close()
	for _, t := range tables {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", pq.QuoteIdentifier(t)), tenantID); err != nil {
			return fmt.Errorf("purge %s: %w", t, err)
		}
	}
	return nil
}

// RewriteTenantID sustituye todas las menciones al tenant de origen por el
// tenant destino. Los UUID no aparecen en otro contexto dentro de los
// literales del dump, y el dump solo lleva datos de un tenant.
func RewriteTenantID(sqlText, sourceID, targetID string) string {
	return strings.ReplaceAll(sqlText, sourceID, targetID)
}

// CountInserts es un resumen barato para la respuesta del restore.
func CountInserts(sqlText string) int {
	return strings.Count(sqlText, "\nINSERT")
}
