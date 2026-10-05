package exportimport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/fasterp/backend/internal/module"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

// ImportOptions configura las opciones de importación
type ImportOptions struct {
	Format ExportFormat
	DryRun bool
}

// DBExecutor es una interfaz común para *sql.Tx o *sql.DB para ejecutar queries.
type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ImportData procesa y valida la importación masiva de un CSV o XLSX
func ImportData(ctx context.Context, db DBExecutor, reg module.ModelRegistration, tenantID string, data []byte, opts ImportOptions) (ImportResult, error) {
	result := ImportResult{
		DryRun: opts.DryRun,
	}

	headers, rowsData, err := parseRows(data, opts.Format)
	if err != nil {
		return result, fmt.Errorf("error al leer archivo de importación: %w", err)
	}

	if len(headers) == 0 {
		return result, fmt.Errorf("el archivo no contiene cabeceras")
	}

	fieldMap, unmatched := matchHeadersToFields(headers, reg)
	result.UnmatchedColumns = unmatched

	if len(fieldMap) == 0 {
		return result, fmt.Errorf("ninguna cabecera coincide con los campos del modelo %s (cabeceras encontradas: %s)", reg.Manifest.Name, strings.Join(headers, ", "))
	}

	result.TotalRows = len(rowsData)

	parsedRecords := make([]map[string]any, 0, len(rowsData))

	for i, row := range rowsData {
		rowNum := i + 2 // Fila 1 es cabecera, los datos empiezan en fila 2
		record := make(map[string]any)
		rowHasError := false

		for colIdx, fieldDef := range fieldMap {
			var rawVal string
			if colIdx < len(row) {
				rawVal = strings.TrimSpace(row[colIdx])
			}

			// Validar requeridos
			if fieldDef.Required && rawVal == "" {
				result.Errors = append(result.Errors, ImportError{
					Row:    rowNum,
					Column: headers[colIdx],
					Field:  fieldDef.Name,
					Error:  fmt.Sprintf("El campo '%s' es obligatorio", fieldDef.Label),
				})
				rowHasError = true
				continue
			}

			if rawVal == "" {
				continue
			}

			// Convertir y validar según tipo
			val, valErr := parseFieldValue(rawVal, fieldDef)
			if valErr != nil {
				result.Errors = append(result.Errors, ImportError{
					Row:    rowNum,
					Column: headers[colIdx],
					Field:  fieldDef.Name,
					Error:  valErr.Error(),
				})
				rowHasError = true
				continue
			}

			record[fieldDef.Name] = val
		}

		if !rowHasError {
			parsedRecords = append(parsedRecords, record)
		}
	}

	if len(result.Errors) > 0 {
		result.Success = false
		return result, nil
	}

	if opts.DryRun {
		result.Success = true
		result.CreatedCount = len(parsedRecords)
		return result, nil
	}

	// Inserción masiva en base de datos
	insertedCount, err := insertRecords(ctx, db, reg, tenantID, parsedRecords)
	if err != nil {
		return result, fmt.Errorf("error guardando registros en base de datos: %w", err)
	}

	result.Success = true
	result.CreatedCount = insertedCount
	return result, nil
}

func parseRows(data []byte, format ExportFormat) ([]string, [][]string, error) {
	switch format {
	case FormatXLSX:
		f, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()

		sheetName := f.GetSheetName(0)
		rows, err := f.GetRows(sheetName)
		if err != nil || len(rows) == 0 {
			return nil, nil, fmt.Errorf("la hoja de cálculo está vacía")
		}
		return rows[0], rows[1:], nil

	default: // CSV
		r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
		r.FieldsPerRecord = -1 // Permitir filas con variable número de campos si hay vacíos al final

		allRows, err := r.ReadAll()
		if err != nil && err != io.EOF {
			return nil, nil, err
		}
		if len(allRows) == 0 {
			return nil, nil, fmt.Errorf("el archivo CSV está vacío")
		}
		return allRows[0], allRows[1:], nil
	}
}

// matchHeadersToFields empareja cada columna del archivo con un campo del
// modelo. Devuelve el mapa columna→campo y las cabeceras que no encajaron con
// ninguno: el llamante las avisa en vez de perderlas en silencio.
func matchHeadersToFields(headers []string, reg module.ModelRegistration) (map[int]module.FieldDef, []string) {
	mapping := make(map[int]module.FieldDef)

	var ordered []module.FieldDef
	if reg.Manifest != nil {
		ordered = reg.Manifest.OrderedFields()
	}

	var unmatched []string
	for colIdx, header := range headers {
		normHeader := normalizeStr(header)
		if normHeader == "" {
			continue
		}

		matched := false
		for _, f := range ordered {
			// Ignorar campos core o calculados
			if f.Readonly || f.Computed || f.Name == "id" || f.Name == "tenant_id" {
				continue
			}

			if normalizeStr(f.Name) == normHeader || normalizeStr(f.Label) == normHeader {
				mapping[colIdx] = f
				matched = true
				break
			}
		}

		// "ID" es una columna de la plantilla pero no importable: no se avisa.
		if !matched && normHeader != "id" {
			unmatched = append(unmatched, strings.TrimSpace(header))
		}
	}
	return mapping, unmatched
}

func parseFieldValue(raw string, f module.FieldDef) (any, error) {
	t := strings.ToLower(strings.TrimSpace(f.Type))

	switch t {
	case "integer", "int", "bigint", "serial":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("valor '%s' no es un entero válido para %s", raw, f.Label)
		}
		return n, nil

	case "decimal", "numeric", "float", "double", "money":
		// Aceptar tanto punto como coma para separador decimal
		rawNorm := strings.ReplaceAll(raw, ",", ".")
		flt, err := strconv.ParseFloat(rawNorm, 64)
		if err != nil {
			return nil, fmt.Errorf("valor '%s' no es un número decimal válido para %s", raw, f.Label)
		}
		return flt, nil

	case "boolean", "bool":
		norm := strings.ToLower(raw)
		switch norm {
		case "true", "1", "sí", "si", "yes", "s", "y", "activo":
			return true, nil
		case "false", "0", "no", "n", "inactivo":
			return false, nil
		default:
			return nil, fmt.Errorf("valor '%s' no es un booleano (Sí/No, true/false) para %s", raw, f.Label)
		}

	case "enum", "selection":
		if len(f.Options) > 0 {
			var match string
			normRaw := normalizeStr(raw)
			for _, opt := range f.Options {
				if normalizeStr(opt) == normRaw {
					match = opt
					break
				}
			}
			if match == "" {
				return nil, fmt.Errorf("valor '%s' no es una opción válida para %s (opciones válidas: %s)", raw, f.Label, strings.Join(f.Options, ", "))
			}
			return match, nil
		}
		return raw, nil

	case "date":
		// Probar formatos comunes de fecha
		formats := []string{"2006-01-02", "02/01/2006", "02-01-2006", "2006/01/02"}
		for _, fmtStr := range formats {
			if parsed, err := time.Parse(fmtStr, raw); err == nil {
				return parsed.Format("2006-01-02"), nil
			}
		}
		return nil, fmt.Errorf("fecha '%s' no tiene un formato válido (ej: YYYY-MM-DD o DD/MM/YYYY)", raw)

	case "datetime":
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
			"02/01/2006 15:04:05",
		}
		for _, fmtStr := range formats {
			if parsed, err := time.Parse(fmtStr, raw); err == nil {
				return parsed.Format(time.RFC3339), nil
			}
		}
		return raw, nil

	default:
		return raw, nil
	}
}

func insertRecords(ctx context.Context, db DBExecutor, reg module.ModelRegistration, tenantID string, records []map[string]any) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}

	tableName := reg.TableName
	if tableName == "" {
		tableName = "mod_" + reg.Manifest.Name + "_" + reg.Manifest.Name
	}

	qb := module.NewQueryBuilder(reg)

	count := 0
	for _, rec := range records {
		query, args, err := qb.BuildInsert(tenantID, rec)
		if err != nil {
			return count, fmt.Errorf("error construyendo SQL insert: %w", err)
		}

		// Reemplazar id autogenerado si no vino
		if _, ok := rec["id"]; !ok {
			rec["id"] = uuid.New().String()
		}

		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			return count, fmt.Errorf("error ejecutando SQL insert: %w", err)
		}
		count++
	}

	return count, nil
}

func normalizeStr(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	r := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n", "_", "", "-", "", " ", "")
	return r.Replace(s)
}
