package sdk

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// EnsureSchema crea o actualiza las tablas de TODOS los modelos del manifest.
//
// Es lo que hace autónomo a un módulo: declara sus campos en manifest.json y el
// motor materializa la tabla. Agregar un campo al manifest y reiniciar basta —
// no hay migraciones que escribir ni columnas que el core deba conocer.
func (s *ModuleSDK) EnsureSchema(ctx context.Context) error {
	if s.Manifest == nil {
		return fmt.Errorf("manifest no cargado")
	}

	for modelName, model := range s.Manifest.Models {
		if err := s.ensureTable(ctx, modelName, model); err != nil {
			return fmt.Errorf("modelo %s: %w", modelName, err)
		}
	}
	return nil
}

func (s *ModuleSDK) ensureTable(ctx context.Context, modelName string, model *ModelDef) error {
	table := s.tableName(modelName)

	// 1. Tabla base: sólo las columnas que administra el core.
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`, table)

	if _, err := s.DB.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("crear tabla: %w", err)
	}

	// 2. Columnas del manifest. El tipo, el largo, el default y las
	//    restricciones vienen del módulo; el motor sólo las traduce.
	existing, err := s.existingColumns(ctx, table)
	if err != nil {
		return err
	}

	for fieldName, def := range model.Fields {
		current, exists := existing[fieldName]
		if !exists {
			if err := s.addColumn(ctx, table, fieldName, def); err != nil {
				return err
			}
			continue
		}
		// La columna ya existe: si el manifest cambió el tipo, reconciliar.
		s.reconcileColumn(ctx, table, fieldName, def, current)
	}

	// 3. Índices: tenant_id siempre; los demás los pide el manifest.
	s.ensureIndexes(ctx, table, model)

	// 4. Aislamiento por tenant.
	if err := s.ensureRLS(ctx, table); err != nil {
		log.Printf("[SDK] aviso: RLS en %s: %v", table, err)
	}

	return nil
}

// addColumn agrega una columna con el tipo y las restricciones del manifest.
func (s *ModuleSDK) addColumn(ctx context.Context, table, field string, def *FieldDef) error {
	clause := fmt.Sprintf("%s %s", field, def.SQLType())

	if def.Default != nil {
		literal, err := sqlLiteral(def.Default)
		if err != nil {
			return fmt.Errorf("default inválido en %s: %w", field, err)
		}
		clause += " DEFAULT " + literal
	}

	alter := fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s", table, clause)
	if _, err := s.DB.ExecContext(ctx, alter); err != nil {
		return fmt.Errorf("agregar columna %s: %w", field, err)
	}
	log.Printf("[SDK] %s: columna %s %s", table, field, def.SQLType())

	// NOT NULL va aparte: si la tabla ya tiene filas sin valor, Postgres lo
	// rechaza. Se avisa en vez de abortar para no bloquear el arranque.
	if def.Required {
		notNull := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL", table, field)
		if _, err := s.DB.ExecContext(ctx, notNull); err != nil {
			log.Printf("[SDK] aviso: %s.%s queda nullable (hay filas sin valor): %v",
				table, field, err)
		}
	}

	if def.Unique {
		constraint := fmt.Sprintf("uq_%s_%s", table, field)
		// Único por tenant, no global: dos tenants pueden repetir el valor.
		idx := fmt.Sprintf("CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (tenant_id, %s)",
			constraint, table, field)
		if _, err := s.DB.ExecContext(ctx, idx); err != nil {
			log.Printf("[SDK] aviso: índice único %s: %v", constraint, err)
		}
	}

	return nil
}

func (s *ModuleSDK) ensureIndexes(ctx context.Context, table string, model *ModelDef) {
	indexes := map[string]string{
		fmt.Sprintf("idx_%s_tenant", table): "tenant_id",
	}
	for fieldName, def := range model.Fields {
		if def.Index {
			indexes[fmt.Sprintf("idx_%s_%s", table, fieldName)] = fieldName
		}
	}

	for name, col := range indexes {
		idx := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, table, col)
		if _, err := s.DB.ExecContext(ctx, idx); err != nil {
			log.Printf("[SDK] aviso: índice %s: %v", name, err)
		}
	}
}

// columnInfo es el tipo que la columna tiene HOY en PostgreSQL.
type columnInfo struct {
	DataType  string
	MaxLength int
	Precision int
	Scale     int
}

// SQLType reconstruye el tipo actual en la misma forma que FieldDef.SQLType(),
// para poder compararlos directamente.
func (c columnInfo) SQLType() string {
	switch c.DataType {
	case "character varying":
		if c.MaxLength > 0 {
			return fmt.Sprintf("VARCHAR(%d)", c.MaxLength)
		}
		return "VARCHAR"
	case "numeric":
		if c.Precision > 0 {
			return fmt.Sprintf("NUMERIC(%d,%d)", c.Precision, c.Scale)
		}
		return "NUMERIC"
	case "text":
		return "TEXT"
	case "integer":
		return "INTEGER"
	case "bigint":
		return "BIGINT"
	case "double precision":
		return "DOUBLE PRECISION"
	case "boolean":
		return "BOOLEAN"
	case "date":
		return "DATE"
	case "timestamp without time zone", "timestamp with time zone":
		return "TIMESTAMP"
	case "jsonb":
		return "JSONB"
	case "uuid":
		return "UUID"
	default:
		return strings.ToUpper(c.DataType)
	}
}

// reconcileColumn alinea una columna existente con lo que declara el manifest.
//
// Sólo aplica cambios que no pueden perder datos (ampliar un VARCHAR, o pasar a
// TEXT). Si el manifest pide algo más estrecho o de otra familia, avisa y deja
// la columna intacta: truncar datos del usuario en un arranque sería peor que
// el desajuste.
func (s *ModuleSDK) reconcileColumn(ctx context.Context, table, field string, def *FieldDef, current columnInfo) {
	desired := def.SQLType()
	if desired == current.SQLType() {
		return
	}

	if !isSafeWidening(current, desired) {
		// Estrechar sólo es peligroso si hay datos que no caben. Si los datos
		// reales entran en el tamaño nuevo, el cambio es lossless y se aplica.
		fits, err := s.dataFitsIn(ctx, table, field, desired)
		if err != nil || !fits {
			log.Printf("[SDK] aviso: %s.%s es %s pero el manifest pide %s; "+
				"hay datos que no caben, se conserva el tipo actual",
				table, field, current.SQLType(), desired)
			return
		}
	}

	alter := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s", table, field, desired)
	if _, err := s.DB.ExecContext(ctx, alter); err != nil {
		log.Printf("[SDK] aviso: no se pudo migrar %s.%s a %s: %v", table, field, desired, err)
		return
	}
	log.Printf("[SDK] %s.%s migrada de %s a %s", table, field, current.SQLType(), desired)
}

// dataFitsIn verifica contra los datos reales si estrechar la columna es
// lossless. Sólo aplica a VARCHAR(n): para otros tipos devuelve false y el
// cambio queda en manos del desarrollador.
func (s *ModuleSDK) dataFitsIn(ctx context.Context, table, field, desired string) (bool, error) {
	var want int
	if _, err := fmt.Sscanf(desired, "VARCHAR(%d)", &want); err != nil {
		return false, nil // no es un VARCHAR: no se decide automáticamente
	}

	var longest int
	query := fmt.Sprintf("SELECT COALESCE(MAX(LENGTH(%s)), 0) FROM %s", field, table)
	if err := s.DB.QueryRowContext(ctx, query).Scan(&longest); err != nil {
		return false, err
	}
	return longest <= want, nil
}

// isSafeWidening indica si pasar de current a desired conserva todos los datos.
func isSafeWidening(current columnInfo, desired string) bool {
	// Cualquier tipo textual cabe en TEXT.
	if desired == "TEXT" {
		return current.DataType == "character varying" || current.DataType == "text"
	}

	// VARCHAR(n) → VARCHAR(m) sólo si m >= n.
	if current.DataType == "character varying" {
		var want int
		if _, err := fmt.Sscanf(desired, "VARCHAR(%d)", &want); err != nil {
			return false
		}
		return want >= current.MaxLength
	}

	// NUMERIC(p,s) → NUMERIC(p2,s2) si no se pierden dígitos por ningún lado.
	if current.DataType == "numeric" {
		var wantPrec, wantScale int
		if _, err := fmt.Sscanf(desired, "NUMERIC(%d,%d)", &wantPrec, &wantScale); err != nil {
			return false
		}
		return wantScale >= current.Scale &&
			wantPrec-wantScale >= current.Precision-current.Scale
	}

	return false
}

func (s *ModuleSDK) existingColumns(ctx context.Context, table string) (map[string]columnInfo, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT column_name, data_type,
		        COALESCE(character_maximum_length, 0),
		        COALESCE(numeric_precision, 0),
		        COALESCE(numeric_scale, 0)
		 FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1`, table)
	if err != nil {
		return nil, fmt.Errorf("leer columnas: %w", err)
	}
	defer rows.Close()

	cols := make(map[string]columnInfo)
	for rows.Next() {
		var name string
		var info columnInfo
		if err := rows.Scan(&name, &info.DataType, &info.MaxLength, &info.Precision, &info.Scale); err != nil {
			return nil, err
		}
		cols[name] = info
	}
	return cols, rows.Err()
}

// ensureRLS activa row-level security y la política de aislamiento por tenant.
//
// Ojo: si el rol de conexión es superusuario (o tiene BYPASSRLS) PostgreSQL
// ignora estas políticas. El aislamiento real lo sigue garantizando el
// "WHERE tenant_id" que el SDK añade en toda consulta; RLS es la segunda capa.
func (s *ModuleSDK) ensureRLS(ctx context.Context, table string) error {
	if _, err := s.DB.ExecContext(ctx,
		fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", table)); err != nil {
		return err
	}

	policy := fmt.Sprintf("%s_tenant_isolation", table)
	if _, err := s.DB.ExecContext(ctx,
		fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s", policy, table)); err != nil {
		return err
	}

	create := fmt.Sprintf(`
		CREATE POLICY %s ON %s
		USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)`,
		policy, table)
	_, err := s.DB.ExecContext(ctx, create)
	return err
}

// sqlLiteral convierte un default del manifest en literal SQL.
// Sólo acepta escalares: nada del manifest se interpola sin pasar por aquí.
func sqlLiteral(v any) (string, error) {
	switch value := v.(type) {
	case bool:
		return strconv.FormatBool(value), nil
	case float64: // todo número que viene de JSON
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10), nil
		}
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	case string:
		// Palabras clave de SQL que se pasan tal cual.
		switch strings.ToUpper(strings.TrimSpace(value)) {
		case "NOW()", "CURRENT_TIMESTAMP", "CURRENT_DATE", "NULL":
			return strings.ToUpper(strings.TrimSpace(value)), nil
		}
		return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
	default:
		return "", fmt.Errorf("tipo de default no soportado: %T", v)
	}
}
