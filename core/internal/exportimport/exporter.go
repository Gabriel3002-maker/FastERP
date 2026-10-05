package exportimport

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"time"

	"github.com/fasterp/backend/internal/module"
	"github.com/xuri/excelize/v2"
)

// ExportOptions contiene la configuración para exportar datos
type ExportOptions struct {
	Format         ExportFormat
	SelectedFields []string
	UseLabels      bool
	// HeadersOnly emite sólo la fila de cabeceras: es la "plantilla de ejemplo",
	// sin los registros del tenant que si no se duplican al reimportarla.
	HeadersOnly bool
}

// ExportData genera el archivo (CSV o XLSX) a partir del modelo y las filas.
func ExportData(reg module.ModelRegistration, records []map[string]any, opts ExportOptions) ([]byte, string, string, error) {
	if opts.Format == "" {
		opts.Format = FormatCSV
	}

	fields := determineExportFields(reg, opts.SelectedFields)

	if opts.HeadersOnly {
		records = nil
	}

	headers := make([]string, len(fields))
	for i, f := range fields {
		if opts.UseLabels && f.Label != "" {
			headers[i] = f.Label
		} else {
			headers[i] = f.Name
		}
	}

	modelName := reg.Manifest.Name
	if modelName == "" {
		modelName = "data"
	}

	switch opts.Format {
	case FormatXLSX:
		b, err := exportXLSX(headers, fields, records)
		filename := fmt.Sprintf("%s_%s.xlsx", modelName, time.Now().Format("20060102_150405"))
		return b, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", filename, err
	default:
		b, err := exportCSV(headers, fields, records)
		filename := fmt.Sprintf("%s_%s.csv", modelName, time.Now().Format("20060102_150405"))
		return b, "text/csv; charset=utf-8", filename, err
	}
}

func determineExportFields(reg module.ModelRegistration, selected []string) []module.FieldDef {
	var ordered []module.FieldDef
	if reg.Manifest != nil {
		ordered = reg.Manifest.OrderedFields()
	}

	if len(selected) > 0 {
		var filtered []module.FieldDef
		seen := make(map[string]bool)
		for _, name := range selected {
			name = strings.TrimSpace(name)
			if seen[name] {
				continue
			}
			seen[name] = true

			// Buscar primero en campos del manifest
			var found bool
			for _, f := range ordered {
				if f.Name == name {
					filtered = append(filtered, f)
					found = true
					break
				}
			}
			if !found {
				// Columnas core (id, created_at, updated_at, etc.)
				label := name
				switch name {
				case "id":
					label = "ID"
				case "created_at":
					label = "Fecha de Creación"
				case "updated_at":
					label = "Última Actualización"
				}
				filtered = append(filtered, module.FieldDef{
					Name:  name,
					Label: label,
					Type:  "string",
				})
			}
		}
		return filtered
	}

	// Si no especificaron campos, incluir id + campos del manifest + created_at
	res := []module.FieldDef{
		{Name: "id", Label: "ID", Type: "string"},
	}
	res = append(res, ordered...)
	res = append(res, module.FieldDef{Name: "created_at", Label: "Fecha de Creación", Type: "datetime"})
	return res
}

func exportCSV(headers []string, fields []module.FieldDef, records []map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	// UTF-8 BOM para abrir con tildes correctamente en Excel
	buf.WriteString("\xEF\xBB\xBF")

	w := csv.NewWriter(&buf)
	if err := w.Write(headers); err != nil {
		return nil, err
	}

	for _, rec := range records {
		row := make([]string, len(fields))
		for i, f := range fields {
			row[i] = formatValue(rec[f.Name], f)
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func exportXLSX(headers []string, fields []module.FieldDef, records []map[string]any) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	sheet := "Datos"
	f.SetSheetName("Sheet1", sheet)

	// Estilo para la cabecera
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "#FFFFFF", Size: 11},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#3B82F6"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	for colIdx, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}

	for rowIdx, rec := range records {
		rowNum := rowIdx + 2
		for colIdx, fDef := range fields {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, rowNum)
			val := formatValue(rec[fDef.Name], fDef)
			f.SetCellValue(sheet, cell, val)
		}
	}

	// Ajustar ancho de columnas automáticamente
	for colIdx := range headers {
		colName, _ := excelize.ColumnNumberToName(colIdx + 1)
		f.SetColWidth(sheet, colName, colName, 20)
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func formatValue(val any, f module.FieldDef) string {
	if val == nil {
		return ""
	}

	switch v := val.(type) {
	case bool:
		if v {
			return "Sí"
		}
		return "No"
	case time.Time:
		if strings.EqualFold(f.Type, "date") {
			return v.Format("2006-01-02")
		}
		return v.Format("2006-01-02 15:04:05")
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
