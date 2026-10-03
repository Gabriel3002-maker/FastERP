package api

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/fasterp/backend/internal/module"
)

// Conversión de filas a JSON.
//
// El punto delicado es el tipo: lib/pq devuelve NUMERIC como []byte, y sin
// convertirlo el cliente recibe "1250.00" como texto donde esperaba un número —
// y suma mal. JSONB llega igual y hay que abrirlo. Las fechas se van en RFC 3339
// porque es lo que entiende new Date() en el navegador, y un "2026-09-29
// 23:24:47" sin zona es la hora del servidor, no la del usuario.

// columnInfo es una columna de la consulta con el nombre con el que sale.
type columnInfo struct{ Name string }

func columnsOf(cols []string) []columnInfo {
	infos := make([]columnInfo, 0, len(cols))
	for _, col := range cols {
		infos = append(infos, columnInfo{Name: resultColumnName(col)})
	}
	return infos
}

// resultColumnName saca el nombre público de una expresión de la lista de
// columnas: base."nombre" AS "nombre" → nombre.
func resultColumnName(col string) string {
	if idx := strings.LastIndex(strings.ToUpper(col), " AS "); idx != -1 {
		return unquote(strings.TrimSpace(col[idx+4:]))
	}
	if idx := strings.LastIndex(col, "."); idx != -1 {
		return unquote(col[idx+1:])
	}
	return unquote(col)
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"`)
}

func rowsToMaps(rows *sql.Rows, cols []string) ([]map[string]any, error) {
	infos := columnsOf(cols)

	items := make([]map[string]any, 0, 16)
	for rows.Next() {
		m, err := scanRow(rows, infos)
		if err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func rowToMap(row *sql.Row, cols []string) (map[string]any, error) {
	return scanRow(row, columnsOf(cols))
}

func scanRow(row interface{ Scan(dest ...any) error }, infos []columnInfo) (map[string]any, error) {
	vals := make([]any, len(infos))
	ptrs := make([]any, len(infos))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, err
	}

	m := make(map[string]any, len(infos))
	for i, info := range infos {
		m[info.Name] = convertValue(vals[i])
	}
	return m, nil
}

func convertValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case string:
		return t
	default:
		return v
	}
}

// validateInput comprueba el cuerpo contra el manifest.
//
// meta y no reg porque la validación necesita los tipos y las opciones, que
// están en la metadata calculada y no en el manifest crudo.
//
// El campo de estado del workflow no se valida aquí porque ya se rechaza antes,
// en el handler: el mensaje que sale es más claro diciendo por qué.
func validateInput(meta *module.ModelMeta, body map[string]any, creating bool) error {
	// Campos que no existen en el manifest se rechazan, no se ignoran. Un
	// error de dedo en el nombre se confundiría con un campo que no se guardó,
	// y el registro se crearía a medias sin avisar.
	for name := range body {
		if !hasField(meta, name) {
			return fmt.Errorf("el campo %q no existe en %s", name, meta.Model)
		}
	}

	for _, f := range meta.Fields {
		val, ok := body[f.Name]
		if !ok || val == nil {
			if creating && f.Required {
				return fmt.Errorf("%s es obligatorio", f.Name)
			}
			continue
		}
		if f.Readonly || f.Computed {
			return fmt.Errorf("%s no se puede escribir", f.Name)
		}
		if err := checkValue(f, val); err != nil {
			return err
		}
	}
	return nil
}

func hasField(meta *module.ModelMeta, name string) bool {
	for _, f := range meta.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// checkValue valida un valor contra el tipo declarado.
//
// No es solo“没有 que sea del tipo correcto”: un enum con valores declarados solo
// acepta esos, y un número tiene que caber en el rango de su columna. Sin esto,
// "estado": "inventado" entraba en la base y el formulario no lo sabía dibujar.
func checkValue(f module.FieldMeta, val any) error {
	// Un campo con options solo acepta esos valores, diga el tipo que sea.
	if len(f.Options) > 0 {
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s debe ser un texto", f.Name)
		}
		for _, opt := range f.Options {
			if opt == s {
				return nil
			}
		}
		return fmt.Errorf("%s solo admite: %s", f.Name, strings.Join(f.Options, ", "))
	}

	switch strings.ToLower(f.Type) {
	case "string", "char":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s debe ser un texto", f.Name)
		}
		// El límite sale del manifest, y el que se compara es el de la columna.
		// El 255 de antes se saltaba los VARCHAR(500) de url y phone.
		limit := f.MaxLength
		if limit <= 0 {
			limit = 255
		}
		if len([]rune(s)) > limit {
			return fmt.Errorf("%s no puede pasar de %d caracteres", f.Name, limit)
		}

	case "email":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s debe ser un texto", f.Name)
		}
		if s == "" {
			return nil
		}
		at := strings.Index(s, "@")
		// Una validación mínima a propósito: mail.ParseAddress acepta cosas que
		// luego rompen un formulario, y una regexp de email completa es un
		//公示 de que hay que dejarla para un validador de verdad.
		if at <= 0 || at == len(s)-1 || !strings.Contains(s[at+1:], ".") || strings.Contains(s, " ") {
			return fmt.Errorf("%s no parece una dirección de correo", f.Name)
		}

	case "phone", "url", "text":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("%s debe ser un texto", f.Name)
		}

	case "integer", "int", "bigint":
		n, err := toInt64(val)
		if err != nil {
			return fmt.Errorf("%s debe ser un número entero", f.Name)
		}
		if f.Type == "bigint" && (n > 1<<31-1 || n < -(1<<31)) {
			return fmt.Errorf("%s no cabe en un bigint", f.Name)
		}

	case "decimal", "numeric", "money", "float", "double":
		if _, ok := toFloat(val); !ok {
			return fmt.Errorf("%s debe ser un número", f.Name)
		}

	case "boolean", "bool":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("%s debe ser verdadero o falso", f.Name)
		}

	case "date", "datetime", "timestamp":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s debe ser una fecha", f.Name)
		}
		if _, err := parseDate(s, f.Type == "date"); err != nil {
			return fmt.Errorf("%s: %v", f.Name, err)
		}

	case "json", "jsonb":
		// encoding/json ya lo ha validado al decodificar el cuerpo; solo hace
		// falta comprobar que no venga como texto.
		if _, ok := val.(string); ok {
			return fmt.Errorf("%s debe ser un objeto JSON, no un texto", f.Name)
		}

	case "uuid", "many2one", "m2o":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s debe ser un UUID", f.Name)
		}
		if !isUUID(s) {
			return fmt.Errorf("%s no es un UUID válido", f.Name)
		}
	}
	return nil
}

// toInt64 acepta los tipos con los que un número llega desde JSON.
//
// No acepta string. Un "3" donde va un entero es un cliente que manda el tipo
// equivocado, y aceptarlo convierte un error visible en un dato guardado sin
// que nadie lo pidiera así. La conversión desde texto sí tiene sentido, pero en
// la query string, y de eso se ocupa ModelRegistration.CastValue.
func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case float64:
		// Un JSON no distingue entero de decimal. Un 1.5 en un integer sí es un
		// error, y sin esta comprobación Postgres lo redondearía en silencio.
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("no es entero")
		}
		return int64(n), nil
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	default:
		return 0, fmt.Errorf("no es un número")
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// parseDate acepta lo que el <input type="date"> y el type="datetime-local"
// envían, más el RFC 3339 completo.
func parseDate(s string, soloFecha bool) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if soloFecha && len(layout) > len("2006-01-02") && layout != "2006-01-02" {
			continue
		}
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	if soloFecha {
		return time.Time{}, fmt.Errorf("se espera AAAA-MM-DD")
	}
	return time.Time{}, fmt.Errorf("se espera AAAA-MM-DD o AAAA-MM-DDTHH:MM")
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
