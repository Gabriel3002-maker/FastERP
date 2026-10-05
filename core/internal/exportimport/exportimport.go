package exportimport

import (
	"strings"
)

type ExportFormat string

const (
	FormatCSV  ExportFormat = "csv"
	FormatXLSX ExportFormat = "xlsx"
)

// ParseFormat normaliza el formato solicitado (default "csv")
func ParseFormat(raw string) ExportFormat {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "xlsx", "excel":
		return FormatXLSX
	default:
		return FormatCSV
	}
}

// ImportError representa un error por fila/campo durante la importación
type ImportError struct {
	Row    int    `json:"row"`
	Column string `json:"column,omitempty"`
	Field  string `json:"field,omitempty"`
	Error  string `json:"error"`
}

// ImportResult devuelve el resumen del proceso de importación
type ImportResult struct {
	Success      bool          `json:"success"`
	TotalRows    int           `json:"total_rows"`
	CreatedCount int           `json:"created_count"`
	DryRun       bool          `json:"dry_run"`
	Errors       []ImportError `json:"errors,omitempty"`
	// UnmatchedColumns son las cabeceras del archivo que no corresponden a
	// ningún campo del modelo. Sus valores se descartan al insertar, así que
	// sin avisar un archivo con cabeceras equivocadas se "valida" y produce
	// filas vacías.
	UnmatchedColumns []string `json:"unmatched_columns,omitempty"`
}
