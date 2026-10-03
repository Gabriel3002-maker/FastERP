package module

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
)

// Executor es lo mínimo que necesita el core para aplicar DDL. Es *sql.DB o
// *sql.Conn, según quién llame.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// typeSQL traduce el vocabulario de tipos del manifest al DDL de Postgres.
//
// A diferencia de la versión anterior, un tipo desconocido es un error y no un
// VARCHAR(255) silencioso. El fallback tenía un coste concreto: "decimal" caía a
// VARCHAR(255), el CRUD seguía validando contra el manifest esperando un número,
// y el fallo aparecía en la primera escritura de un cliente, no al instalar.
func typeSQL(f FieldDef) (string, error) {
	switch strings.ToLower(strings.TrimSpace(f.Type)) {
	case "string", "char":
		return varchar(f.Length), nil
	case "text":
		return "TEXT", nil
	// Los tres siguientes son string con formato. La diferencia no está en la
	// columna sino en lo que el formulario dibuja y en lo que la API valida:
	// un email es un string que además tiene que tener forma de email. Por eso
	// el motor de vistas los usa y el DDL no.
	case "email":
		return varchar(255), nil
	case "phone":
		return varchar(50), nil
	case "url":
		return varchar(500), nil
	case "int", "integer":
		return "INTEGER", nil
	case "bigint":
		return "BIGINT", nil
	case "float", "double":
		return "DOUBLE PRECISION", nil
	case "decimal", "numeric", "money":
		prec, scale := f.Precision, f.Scale
		if prec == 0 {
			prec = 18
		}
		if scale == 0 && f.Scale == 0 && f.Precision == 0 {
			scale = 4
		}
		if prec < 1 || prec > 1000 {
			return "", fmt.Errorf("campo %q: precision %d fuera de rango", f.Name, prec)
		}
		if scale < 0 || scale > prec {
			return "", fmt.Errorf("campo %q: scale %d fuera de rango para precision %d", f.Name, scale, prec)
		}
		return fmt.Sprintf("NUMERIC(%d,%d)", prec, scale), nil
	case "bool", "boolean":
		return "BOOLEAN", nil
	case "date":
		return "DATE", nil
	case "datetime", "timestamp":
		return "TIMESTAMP", nil
	case "json", "jsonb":
		return "JSONB", nil
	case "enum", "selection":
		// VARCHAR y no un ENUM de Postgres: cambiar las options de un enum
		// exige soltar y recrear el tipo, y un módulo que añade una opción
		// rompería la tabla. Las options se validan en la API.
		return varchar(f.Length), nil
	case "uuid", "many2one", "m2o":
		return "UUID", nil
	case "uuid[]", "many2many_ids":
		return "UUID[]", nil
	default:
		return "", fmt.Errorf("campo %q: tipo %q desconocido", f.Name, f.Type)
	}
}

func varchar(length int) string {
	if length <= 0 {
		length = 255
	}
	if length > 10000 {
		length = 10000
	}
	return "VARCHAR(" + strconv.Itoa(length) + ")"
}

// sqlLiteral convierte el valor de un default en un literal de Postgres.
//
// El DDL se arma concatenando y se ejecuta con db.DB, el pool compartido que
// está fuera de RLS. Con lib/pq, un Exec sin argumentos usa el protocolo de
// consulta simple, que acepta varias sentencias separadas por ';'. Por eso
// "default" es el único campo del manifest que no puede pasar por la whitelist
// de identificadores, y por eso aquí solo se admite un vocabulario cerrado:
// números, booleanos y texto entre comillas.
//
// Un default que no sea admisible no se adiventa: se descarta con un aviso y la
// columna queda sin default, que es visible y se puede arreglar. Aceptarlo en
// silencio convertiría a quien publica un módulo en dueño de la base.
func sqlLiteral(v any) (string, bool) {
	switch d := v.(type) {
	case nil:
		return "", false
	case bool:
		if d {
			return "TRUE", true
		}
		return "FALSE", true
	case string:
		return quoteLiteral(d)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		n, ok := asInt64(d)
		if !ok {
			return "", false
		}
		return strconv.FormatInt(n, 10), true
	case float32, float64:
		f, _ := d.(float64)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", false
		}
		return strconv.FormatFloat(f, 'f', -1, 64), true
	default:
		return "", false
	}
}

// asInt64 convierte los enteros de cualquier ancho, incluyendo los sin signo,
// y corta los que no caben en un int64 de Postgres.
func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	}
	return 0, false
}

// quoteLiteral envuelve un texto en comillas simples duplicando los apostrophes.
//
// El texto no se re-emite: se vuelve a construir desde el valor original. Con
// standard_conforming_strings (el default desde Postgres 9.1) la barra
// invertida no escapa nada, así que un default re-emitido tal cual convertiría
// 'O'Brien' en la cadena "O" seguida del identificador Brien, y un default mal
// formado rompería la sentencia entera.
func quoteLiteral(s string) (string, bool) {
	if len(s) > 4096 {
		return "", false
	}
	for _, r := range s {
		// Los bytes de control rompen el DDL y no aparecen en un default
		// escrito a mano.
		if r < 0x20 && r != '\n' && r != '\t' {
			return "", false
		}
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", true
}

// CreateTableSQL genera el DDL de un modelo.
//
// Las columnas salen en el orden que declara el manifest, no en el de un mapa:
// ese orden es la disposición de la tabla y del formulario, y quien lo escribió
// lo eligió.
func CreateTableSQL(m *Manifest, md *ModelDef) (string, error) {
	table := m.TableName(md.Name)

	cols := []string{
		"  id UUID PRIMARY KEY DEFAULT gen_random_uuid()",
		"  tenant_id UUID NOT NULL",
	}

	for _, f := range md.orderedFields() {
		colType, err := typeSQL(f)
		if err != nil {
			return "", err
		}
		notNull := ""
		if f.Required {
			notNull = " NOT NULL"
		}
		def := ""
		if lit, ok := sqlLiteral(f.Default); ok {
			def = " DEFAULT " + lit
		} else if f.Default != nil {
			log.Printf("[Modules] default no válido ignorado en %s.%s: %v", table, f.Name, f.Default)
		}
		cols = append(cols, "  "+f.Name+" "+colType+notNull+def)
	}

	cols = append(cols,
		"  created_at TIMESTAMP NOT NULL DEFAULT NOW()",
		"  updated_at TIMESTAMP NOT NULL DEFAULT NOW()",
	)

	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n%s\n)", table, strings.Join(cols, ",\n")), nil
}

// IndexSQL genera los índices declarados en el manifest.
//
// Los índices y las restricciones unique no pueden ir dentro del CREATE TABLE de
// forma que sea idempotente, así que van aparte: añadir un índice a un módulo que
// ya está instalado tiene que poder repetirse sin error.
func IndexSQL(m *Manifest, md *ModelDef) []string {
	table := m.TableName(md.Name)
	var out []string
	for _, f := range md.orderedFields() {
		if !f.Unique && !f.Index {
			continue
		}
		kind := "INDEX"
		if f.Unique {
			kind = "UNIQUE INDEX"
		}
		name := fmt.Sprintf("idx_%s_%s_%s", m.Name, md.Name, f.Name)
		out = append(out, fmt.Sprintf("CREATE %s IF NOT EXISTS %s ON %s (%s, tenant_id)", kind, name, table, f.Name))
	}
	return out
}

// tenantPolicyExpr es la condición de aislamiento por tenant.
//
// El GUC se lee con missing_ok=true y se envuelve en NULLIF a propósito: una
// conexión que nunca fijó tenant (el pool compartido, usado para el DDL) no
// debe reventar con "unrecognized configuration parameter", sino devolver NULL
// y por tanto cero filas. Falla cerrado.
const tenantPolicyExpr = `tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid`

// RLS deja activa la seguridad por fila de una tabla de módulo.
//
// FORCE no es opcional. El rol con el que conecta la aplicación es el dueño de
// las tablas que crea (tiene CREATE ON SCHEMA public), y PostgreSQL no evalúa
// las políticas para el dueño: sin FORCE, ENABLE ROW LEVEL SECURITY no hace
// nada y el cruce de tenants depende al cien por cien de que cada consulta
// lleve su WHERE tenant_id. Con FORCE, un WHERE olvidado deja de filtrar.
//
// La segunda capa no es un adorno: es la que cubre los handlers futuros, los
// scripts de un módulo y el DDL que se ejecuta desde una ruta de escritura.
func RLS(table string) []string {
	return []string{
		fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", table),
		fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY", table),
		fmt.Sprintf("DROP POLICY IF EXISTS tenant_isolation ON %s", table),
		fmt.Sprintf("CREATE POLICY tenant_isolation ON %s FOR ALL USING (%s) WITH CHECK (%s)", table, tenantPolicyExpr, tenantPolicyExpr),
	}
}

// SchemaStatement is one DDL statement ready to execute, with a description for
// logs and for the error message if it fails.
type SchemaStatement struct {
	SQL  string
	What string
}

// HistoryTable es donde se guarda el recorrido de un registro por su máquina de
// estados.
//
// Una tabla por tenant, no por modelo: una tabla por modelo cruzaría todos los
// módulos entre sí, y el nombre de la tabla tendría que llevar el del módulo
// para no chocar. Con module_name y model_name como columnas, la RLS protege
// igual y las consultas del historial comparten índice.
func HistoryTable(moduleName, modelName string) string {
	return "mod_" + moduleName + "_" + modelName + "_hist"
}

// HistoryDDL es el plan de la tabla de historial de un modelo con workflow.
//
// Vive aquí y no en el handler porque tiene que ser el mismo esquema el que crea
// el reconciliador y el que usa la ruta de transición: si divergieran, el
// historial se escribiría en una tabla con otro nombre que la que se consulta.
func HistoryDDL(moduleName, modelName string) []SchemaStatement {
	table := HistoryTable(moduleName, modelName)
	policy := fmt.Sprintf("USING (%s) WITH CHECK (%s)", tenantPolicyExpr, tenantPolicyExpr)
	return []SchemaStatement{
		{SQL: fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  module_name VARCHAR(63) NOT NULL,
  model_name VARCHAR(63) NOT NULL,
  record_id UUID NOT NULL,
  action VARCHAR(63) NOT NULL,
  from_state VARCHAR(255) NOT NULL DEFAULT '',
  to_state VARCHAR(255) NOT NULL,
  changed_at TIMESTAMP NOT NULL DEFAULT NOW()
)`, table), What: "crear tabla de historial " + table},
		{SQL: fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_%s_hist ON %s (record_id, changed_at DESC)", moduleName, modelName, table),
			What: "crear índice del historial"},
		{SQL: fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", table), What: "activar RLS en " + table},
		// FORCE por lo mismo que en RLS(): la aplicación es la dueña de esta
		// tabla y sin FORCE la política no se llega a evaluar.
		{SQL: fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY", table), What: "forzar RLS en " + table},
		{SQL: fmt.Sprintf("DROP POLICY IF EXISTS tenant_isolation ON %s", table), What: "rehacer política de " + table},
		{SQL: fmt.Sprintf("CREATE POLICY tenant_isolation ON %s %s", table, policy), What: "crear política de " + table},
	}
}

// PlanSchema devuelve todo lo necesario para poner las tablas de un módulo al
// día con su manifest, sin tocar la base.
//
// Reconciliar en vez de solo crear es lo que hace corto el ciclo: añades un
// campo a module.yaml y la columna aparece. Las sentencias son idempotentes, así
// que ejecutar el mismo plan dos veces no hace nada.
func PlanSchema(m *Manifest) ([]SchemaStatement, error) {
	var stmts []SchemaStatement

	for _, name := range m.FieldOrderModel() {
		md := m.Models[name]
		if md == nil {
			continue
		}

		create, err := CreateTableSQL(m, md)
		if err != nil {
			return nil, fmt.Errorf("modelo %q: %w", name, err)
		}
		table := m.TableName(name)
		stmts = append(stmts, SchemaStatement{SQL: create, What: "crear tabla " + table})

		// Lo que ya existe se añade después: ADD COLUMN IF NOT EXISTS sobre
		// columnas que ya están no hace nada y no da error.
		for _, f := range md.orderedFields() {
			colType, err := typeSQL(f)
			if err != nil {
				return nil, fmt.Errorf("modelo %q: %w", name, err)
			}
			// Un required nuevo se añade sin NOT NULL: la tabla puede tener
			// ya miles de filas y un NOT NULL sobre una columna vacía falla.
			// Rellenar, y luego endurecer, es una migración aparte.
			stmts = append(stmts, SchemaStatement{
				SQL:  fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s", table, f.Name, colType),
				What: "añadir columna " + table + "." + f.Name,
			})
		}

		for _, idx := range IndexSQL(m, md) {
			stmts = append(stmts, SchemaStatement{SQL: idx, What: "crear índice de " + table})
		}
		for _, r := range RLS(table) {
			stmts = append(stmts, SchemaStatement{SQL: r, What: "activar RLS en " + table})
		}

		// La tabla de historial solo si el modelo tiene máquina de estados:
		// crearla para todo llenaría la base de tablas que nadie consulta.
		if md.Workflow != nil {
			stmts = append(stmts, HistoryDDL(m.Name, name)...)
		}
	}

	return stmts, nil
}

// ApplySchema ejecuta un plan contra la base.
//
// db.DB es el pool compartido y está fuera de RLS, y tiene que serlo: aplicar
// DDL a las tablas de un tenant concreto no tiene sentido y el CREATE POLICY
// necesita los privilegios de superusuario del rol inicial.
func ApplySchema(ctx context.Context, m *Manifest, exec Executor) error {
	stmts, err := PlanSchema(m)
	if err != nil {
		return err
	}
	for _, s := range stmts {
		if _, err := exec.ExecContext(ctx, s.SQL); err != nil {
			return fmt.Errorf("módulo %q, %s: %w\nSQL: %s", m.Name, s.What, err, s.SQL)
		}
	}
	return nil
}

// FieldOrderModel returns the models in the order they were declared.
func (m *Manifest) FieldOrderModel() []string {
	if len(m.fieldOrder) > 0 {
		return m.fieldOrder
	}
	out := make([]string, 0, len(m.Models))
	for name := range m.Models {
		out = append(out, name)
	}
	return out
}
