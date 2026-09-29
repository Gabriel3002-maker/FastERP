package sdk

import (
	"fmt"
	"strings"
)

// TypeSpec describe un tipo del vocabulario del motor.
//
// Esto es lo ÚNICO que el SDK sabe de antemano: cómo traducir un tipo abstracto
// a SQL y a OpenAPI. Las dimensiones (largo, precisión, escala), los defaults y
// las restricciones salen siempre del manifest del módulo.
type TypeSpec struct {
	// SQL construye el tipo PostgreSQL usando lo que el manifest declaró.
	SQL func(f *FieldDef) string
	// JSONType y Format describen el campo en la documentación OpenAPI.
	JSONType string
	Format   string
	// Searchable marca los tipos sobre los que aplica la búsqueda libre (ILIKE).
	Searchable bool
}

// typeRegistry es el vocabulario de tipos disponible para cualquier módulo.
var typeRegistry = map[string]TypeSpec{
	"string": {
		SQL:        func(f *FieldDef) string { return varchar(f, 255) },
		JSONType:   "string",
		Searchable: true,
	},
	"text": {
		SQL:        func(f *FieldDef) string { return "TEXT" },
		JSONType:   "string",
		Searchable: true,
	},
	"email": {
		SQL:        func(f *FieldDef) string { return varchar(f, 255) },
		JSONType:   "string",
		Format:     "email",
		Searchable: true,
	},
	"phone": {
		SQL:        func(f *FieldDef) string { return varchar(f, 50) },
		JSONType:   "string",
		Searchable: true,
	},
	"url": {
		SQL:        func(f *FieldDef) string { return varchar(f, 500) },
		JSONType:   "string",
		Format:     "uri",
		Searchable: true,
	},
	"integer": {
		SQL:      func(f *FieldDef) string { return "INTEGER" },
		JSONType: "integer",
		Format:   "int32",
	},
	"bigint": {
		SQL:      func(f *FieldDef) string { return "BIGINT" },
		JSONType: "integer",
		Format:   "int64",
	},
	"decimal": {
		SQL:      func(f *FieldDef) string { return numeric(f, 18, 4) },
		JSONType: "number",
		Format:   "double",
	},
	"money": {
		SQL:      func(f *FieldDef) string { return numeric(f, 18, 2) },
		JSONType: "number",
		Format:   "double",
	},
	"float": {
		SQL:      func(f *FieldDef) string { return "DOUBLE PRECISION" },
		JSONType: "number",
		Format:   "double",
	},
	"boolean": {
		SQL:      func(f *FieldDef) string { return "BOOLEAN" },
		JSONType: "boolean",
	},
	"date": {
		SQL:      func(f *FieldDef) string { return "DATE" },
		JSONType: "string",
		Format:   "date",
	},
	"datetime": {
		SQL:      func(f *FieldDef) string { return "TIMESTAMP" },
		JSONType: "string",
		Format:   "date-time",
	},
	"json": {
		SQL:      func(f *FieldDef) string { return "JSONB" },
		JSONType: "object",
	},
	"uuid": {
		SQL:      func(f *FieldDef) string { return "UUID" },
		JSONType: "string",
		Format:   "uuid",
	},
	"uuid[]": {
		SQL:        func(f *FieldDef) string { return "UUID[]" },
		JSONType:   "array",
		Searchable: false,
	},
	"enum": {
		SQL:        func(f *FieldDef) string { return varchar(f, 255) },
		JSONType:   "string",
		Searchable: true,
	},
	"many2one": {
		SQL:        func(f *FieldDef) string { return "UUID" },
		JSONType:   "string",
		Format:     "uuid",
		Searchable: false,
	},
	"one2many": {
		SQL:        func(f *FieldDef) string { return "JSONB" },
		JSONType:   "array",
		Searchable: false,
	},
	"many2many": {
		SQL:        func(f *FieldDef) string { return "JSONB" },
		JSONType:   "array",
		Searchable: false,
	},
}

// Alias para que los módulos puedan escribir el tipo como les resulte natural.
var typeAliases = map[string]string{
	"varchar":   "string",
	"char":      "string",
	"int":       "integer",
	"int32":     "integer",
	"int64":     "bigint",
	"long":      "bigint",
	"numeric":   "decimal",
	"double":    "float",
	"real":      "float",
	"bool":      "boolean",
	"timestamp": "datetime",
	"jsonb":     "json",
	"selection": "enum",
	"m2o":       "many2one",
	"o2m":       "one2many",
	"m2m":       "many2many",
}

// resolveType normaliza el tipo declarado y devuelve su spec.
func resolveType(declared string) (TypeSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(declared))
	if canonical, ok := typeAliases[key]; ok {
		key = canonical
	}
	spec, ok := typeRegistry[key]
	return spec, ok
}

// Spec devuelve el TypeSpec del campo, con TEXT como refugio si el tipo es
// desconocido (así un manifest con un typo no tumba el módulo entero).
func (f *FieldDef) Spec() TypeSpec {
	if spec, ok := resolveType(f.Type); ok {
		return spec
	}
	return TypeSpec{
		SQL:        func(*FieldDef) string { return "TEXT" },
		JSONType:   "string",
		Searchable: true,
	}
}

// SQLType construye el tipo PostgreSQL del campo a partir del manifest.
func (f *FieldDef) SQLType() string { return f.Spec().SQL(f) }

// IsSearchable indica si el campo participa en la búsqueda libre.
func (f *FieldDef) IsSearchable() bool { return f.Spec().Searchable }

// KnownTypes lista el vocabulario disponible (para mensajes de error y docs).
func KnownTypes() []string {
	types := make([]string, 0, len(typeRegistry))
	for name := range typeRegistry {
		types = append(types, name)
	}
	return types
}

// varchar respeta el largo del manifest y cae al default del tipo si no vino.
func varchar(f *FieldDef, fallback int) string {
	length := f.Length
	if length <= 0 {
		length = fallback
	}
	return fmt.Sprintf("VARCHAR(%d)", length)
}

// numeric respeta precisión y escala del manifest.
func numeric(f *FieldDef, defPrecision, defScale int) string {
	precision, scale := f.Precision, f.Scale
	if precision <= 0 {
		precision = defPrecision
	}
	if scale < 0 || scale > precision {
		scale = defScale
	}
	if scale == 0 && f.Scale == 0 {
		scale = defScale
	}
	return fmt.Sprintf("NUMERIC(%d,%d)", precision, scale)
}
