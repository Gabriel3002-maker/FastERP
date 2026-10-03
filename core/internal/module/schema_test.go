package module

import (
	"strings"
	"testing"
)

// El DDL se ejecuta con db.DB, el pool compartido, fuera de RLS. Con lib/pq,
// Exec sin argumentos usa el protocolo de consulta simple, que acepta varias
// sentencias separadas por ';'. Por eso "default" no puede concatenarse tal cual:
// es el único campo del manifest que no pasa por el whitelist de
// identificadores, y controlarlo es lo que impide que publicar un módulo sea
// tomar el control de la base.
//
// La defensa no es una lista negra de cadenas: es cotizar el valor. Un default
// es un dato, y un dato va entre comillas. Estos tests comprueban la propiedad
// que importa —que el DDL generado no pueda salir de su literal— en vez de
// enumerar lo que se recuerda rechazar.
func TestDefaultInyectadoNoSaleDelLiteral(t *testing.T) {
	injections := []string{
		"0); DROP TABLE users CASCADE; --",
		"'x'); DELETE FROM mod_crm_contact; --",
		"1; SELECT pg_sleep(10)",
		"0) ; UPDATE users SET is_admin = true; --",
		"now() at time zone 'utc', (select password_hash from users limit 1)",
		`" OR 1=1`,
		"'); DROP TABLE tenants; --",
	}

	for _, in := range injections {
		m := &Manifest{Name: "demo", fieldOrder: []string{"x"}}
		m.Models = map[string]*ModelDef{
			"x": {Name: "x", Fields: []FieldDef{{Name: "estado", Type: "string", Default: in}}},
		}

		sql, err := CreateTableSQL(m, m.Models["x"])
		if err != nil {
			t.Fatalf("CreateTableSQL: %v", err)
		}

		// La sentencia tiene que seguir siendo una sola sentencia. Se mide
		// contando los separadores de nivel superior, ignorando los que caen
		// dentro de un literal de texto.
		if n := countTopLevelSemicolons(sql); n != 0 {
			t.Errorf("default %q produjo %dsentencias extra:\n%s", in, n, sql)
		}
		// Y el texto inyectado tiene que estar citado, no suelto.
		if !containsQuoted(sql, in) {
			t.Errorf("el default %q no aparece cotizado en el DDL:\n%s", in, sql)
		}
	}
}

// countTopLevelSemicolons cuenta los ';' que no están dentro de un literal.
func countTopLevelSemicolons(sql string) int {
	inSingle, inDouble := false, false
	n := 0
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case inSingle:
			if c == '\'' {
				// '' es un apostrophe escapado, no el fin del literal.
				if i+1 < len(sql) && sql[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
		case inDouble:
			if c == '\\' {
				i++
			} else if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == ';':
			n++
		}
	}
	return n
}

// containsQuoted comprueba que value aparezca dentro de un literal de texto.
func containsQuoted(sql, value string) bool {
	needle := strings.ReplaceAll(value, "'", "''")
	return strings.Contains(sql, "'"+needle+"'")
}

func TestSQLLiteralRejectsLoQueNoEsUnDato(t *testing.T) {
	// nil no es un default.
	if _, ok := sqlLiteral(nil); ok {
		t.Error("sqlLiteral(nil) debería rechazarse")
	}
	// Un mapa o una lista no son escalares: vienen de un manifest escrito a
	// mano, y alguien metió algo que no es un valor.
	for _, v := range []any{map[string]any{"a": 1}, []string{"a"}, struct{}{}} {
		if got, ok := sqlLiteral(v); ok {
			t.Errorf("sqlLiteral(%#v) = %q, true; debe rechazar", v, got)
		}
	}
}

func TestSQLLiteralAceptaDefaultsReales(t *testing.T) {
	ok := []struct {
		in   any
		want string
	}{
		{0, "0"},
		{int64(42), "42"},
		{-12.5, "-12.5"},
		{3.14, "3.14"},
		{true, "TRUE"},
		{false, "FALSE"},
		{"borrador", "'borrador'"},
		// El apostrophe se duplica, no se re-emite: des-escapar y volver a
		// escapar produciría 'O'Brien', que Postgres leería como la cadena "O"
		// seguida del identificador Brien.
		{"O'Brien", "'O''Brien'"},
	}

	for _, c := range ok {
		got, valid := sqlLiteral(c.in)
		if !valid {
			t.Errorf("sqlLiteral(%#v) rechazado; debería aceptarse", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("sqlLiteral(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuoteLiteralRechazaBytesDeControl(t *testing.T) {
	// Un byte nulo rompe la sentencia entera, y no aparece en un default
	// escrito a mano.
	if got, ok := quoteLiteral("a\x00b"); ok {
		t.Errorf("un byte nulo debe rechazarse, dio %q", got)
	}
	if got, ok := quoteLiteral("a\x1bb"); ok {
		t.Errorf("un byte de escape debe rechazarse, dio %q", got)
	}
	// El tabulador y el salto sí son legales en un literal.
	if _, ok := quoteLiteral("a\tb"); !ok {
		t.Error("un tabulador debería poder cotizarse")
	}
}

// Un módulo escrito a mano pone el valor del default tal cual, sin comillas:
// `default: borrador`. Ese es el caso normal y tiene que acabar siendo un
// literal de texto, no fallar.
func TestManifestDefaultSinComillasAcabaSiendoTexto(t *testing.T) {
	mod := `name: x
label: X
models:
  a:
    fields:
      - {name: estado, type: enum, options: [borrador, listo], default: borrador}
      - {name: intentos, type: integer, default: 0}
      - {name: activo, type: boolean, default: true}
`
	m, err := ParseManifest([]byte(mod), "x/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	sql, err := CreateTableSQL(m, m.Models["a"])
	if err != nil {
		t.Fatalf("CreateTableSQL: %v", err)
	}
	for _, want := range []string{
		"estado VARCHAR(255) DEFAULT 'borrador'",
		"intentos INTEGER DEFAULT 0",
		"activo BOOLEAN DEFAULT TRUE",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("falta %q en el DDL:\n%s", want, sql)
		}
	}
}

// Un default con SQL dentro se cotiza, no se concatena: la columna se crea con
// un literal y el DDL sigue siendo una sola sentencia.
func TestCreateTableSQLCotaElDefault(t *testing.T) {
	m := &Manifest{Name: "demo", fieldOrder: []string{"x"}}
	m.Models = map[string]*ModelDef{
		"x": {
			Name: "x",
			Fields: []FieldDef{
				{Name: "estado", Type: "string", Default: "0); DROP TABLE users CASCADE; CREATE TABLE mod_z_z (id int"},
				{Name: "notas", Type: "text"},
			},
		},
	}

	sql, err := CreateTableSQL(m, m.Models["x"])
	if err != nil {
		t.Fatalf("CreateTableSQL: %v", err)
	}
	if n := countTopLevelSemicolons(sql); n != 0 {
		t.Errorf("el DDL tiene %d sentencias extra:\n%s", n, sql)
	}
	if !strings.Contains(sql, "  estado VARCHAR(255) DEFAULT '0); DROP TABLE users CASCADE; CREATE TABLE mod_z_z (id int'") {
		t.Errorf("el default debería quedar cotizado como dato:\n%s", sql)
	}
}

// El orden de las columnas es el orden de declaración del manifest: es la
// disposición de la tabla y del formulario, y quien lo escribió lo eligió.
func TestCreateTableSQLRespetaElOrdenDeCampos(t *testing.T) {
	mod := `name: x
label: X
models:
  a:
    fields:
      - {name: codigo, type: string, sequence: 10}
      - {name: nombre, type: string, sequence: 20}
      - {name: notas, type: text, sequence: 30}
`
	m, err := ParseManifest([]byte(mod), "x/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	sql, err := CreateTableSQL(m, m.Models["a"])
	if err != nil {
		t.Fatalf("CreateTableSQL: %v", err)
	}

	pos := make([]int, 0, 3)
	for _, col := range []string{"codigo", "nombre", "notas"} {
		i := strings.Index(sql, "\n  "+col+" ")
		if i < 0 {
			t.Fatalf("falta la columna %s en:\n%s", col, sql)
		}
		pos = append(pos, i)
	}
	if !(pos[0] < pos[1] && pos[1] < pos[2]) {
		t.Errorf("las columnas no salen en el orden del manifest: %v\n%s", pos, sql)
	}
}

// Un tipo desconocido es un error, no un VARCHAR(255) silencioso. El fallback
// viejo.convertía "decimal" en VARCHAR(255) y el fallo aparecía en la primera
// escritura de un cliente, no al instalar el módulo.
func TestTypeDesconocidoFallaEnInstallar(t *testing.T) {
	mod := `name: x
label: X
models:
  a:
    fields:
      - {name: importe, type: deceimal}
`
	m, err := ParseManifest([]byte(mod), "x/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if _, err := CreateTableSQL(m, m.Models["a"]); err == nil {
		t.Error("un tipo mal escrito debe fallar al generar el DDL, no crear VARCHAR")
	}
}

func TestTypeSQLCubreElVocabulario(t *testing.T) {
	casos := []struct{ tipo, want string }{
		{"string", "VARCHAR(255)"},
		{"string", "VARCHAR(255)"},
		{"text", "TEXT"},
		{"integer", "INTEGER"},
		{"int", "INTEGER"},
		{"bigint", "BIGINT"},
		{"boolean", "BOOLEAN"},
		{"bool", "BOOLEAN"},
		{"date", "DATE"},
		{"datetime", "TIMESTAMP"},
		{"json", "JSONB"},
		{"uuid", "UUID"},
		{"many2one", "UUID"},
		{"enum", "VARCHAR(255)"},
		{"decimal", "NUMERIC(18,4)"},
		{"uuid[]", "UUID[]"},
	}
	for _, c := range casos {
		got, err := typeSQL(FieldDef{Name: "f", Type: c.tipo})
		if err != nil {
			t.Errorf("typeSQL(%q): %v", c.tipo, err)
			continue
		}
		if got != c.want {
			t.Errorf("typeSQL(%q) = %q, want %q", c.tipo, got, c.want)
		}
	}
}

func TestTypeSQLRespetaLongitudYPrecision(t *testing.T) {
	got, _ := typeSQL(FieldDef{Name: "f", Type: "string", Length: 80})
	if got != "VARCHAR(80)" {
		t.Errorf("string con length 80 = %q", got)
	}
	got, _ = typeSQL(FieldDef{Name: "f", Type: "decimal", Precision: 12, Scale: 2})
	if got != "NUMERIC(12,2)" {
		t.Errorf("decimal 12,2 = %q", got)
	}
	if _, err := typeSQL(FieldDef{Name: "f", Type: "decimal", Precision: 5, Scale: 9}); err == nil {
		t.Error("scale > precision debería ser un error")
	}
}

// El plan de esquema tiene que poder repetirse: es lo que se ejecuta en cada
// arranque y después de cada recarga del watcher.
func TestPlanSchemaEsIdempotente(t *testing.T) {
	mod := `name: x
label: X
models:
  a:
    fields:
      - {name: nombre, type: string, index: true, unique: true}
`
	m, err := ParseManifest([]byte(mod), "x/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	first, err := PlanSchema(m)
	if err != nil {
		t.Fatalf("PlanSchema: %v", err)
	}
	second, err := PlanSchema(m)
	if err != nil {
		t.Fatalf("PlanSchema: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("dos planes difieren: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].SQL != second[i].SQL {
			t.Errorf("sentencia %d difiere entre dos planes:\n%s\n%s", i, first[i].SQL, second[i].SQL)
		}
	}

	// Todas las sentencias tienen que poder repetirse sin error. Las que no
	// llevan "IF NOT EXISTS" se permiten solo si su naturaleza lo garantiza:
	// habilitar RLS dos veces no hace nada, y CREATE POLICY va precedida de su
	// DROP.
	for _, s := range first {
		upper := strings.ToUpper(s.SQL)
		idem := strings.Contains(upper, "IF NOT EXISTS") ||
			strings.Contains(upper, "IF EXISTS") ||
			// ENABLE y FORCE no admiten IF NOT EXISTS: ambas marcan una bandera
			// en la relación, así que repetirlas no hace nada. Comprobado contra
			// postgres:16, no es una suposición.
			strings.Contains(upper, "ENABLE ROW LEVEL SECURITY") ||
			strings.Contains(upper, "FORCE ROW LEVEL SECURITY") ||
			// CREATE POLICY no es idempotente por sí solo; su idempotencia
			// viene del DROP de la sentencia anterior, que se comprueba
			// justo debajo.
			strings.Contains(upper, "CREATE POLICY")
		if !idem {
			t.Errorf("sentencia no idempotente: %s", s.SQL)
		}
	}

	// Y el orden importa: la política se crea después de su DROP.
	for i, s := range first {
		if !strings.Contains(strings.ToUpper(s.SQL), "CREATE POLICY") {
			continue
		}
		if i == 0 || !strings.Contains(strings.ToUpper(first[i-1].SQL), "DROP POLICY IF EXISTS") {
			t.Errorf("CREATE POLICY sin su DROP POLICY IF EXISTS justo antes:\n%s\n%s", first[i-1].SQL, s.SQL)
		}
	}
}

// Un campo nuevo tiene que aparecer sin migración escrita a mano, y sin NOT
// NULL: la tabla puede tener ya miles de filas.
func TestPlanSchemaAñadeColumnasNuevasSinNotNull(t *testing.T) {
	mod := `name: x
label: X
models:
  a:
    fields:
      - {name: nombre, type: string, required: true}
      - {name: apellido, type: string, required: true}
`
	m, err := ParseManifest([]byte(mod), "x/module.yaml")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	stmts, err := PlanSchema(m)
	if err != nil {
		t.Fatalf("PlanSchema: %v", err)
	}

	var add string
	for _, s := range stmts {
		if strings.Contains(s.SQL, "ADD COLUMN IF NOT EXISTS apellido") {
			add = s.SQL
		}
	}
	if add == "" {
		t.Fatal("no se genera el ADD COLUMN para un campo nuevo")
	}
	if strings.Contains(add, "NOT NULL") {
		t.Errorf("un campo nuevo no puede añadirse con NOT NULL sobre una tabla poblada: %s", add)
	}
}

// La política de RLS es lo único que impide que un WHERE olvidado devuelva filas
// de otro tenant, así que tiene que estar en el plan.
func TestPlanSchemaIncluyeRLS(t *testing.T) {
	mod := `name: x
label: X
models:
  a:
    fields:
      - {name: nombre, type: string}
`
	m, _ := ParseManifest([]byte(mod), "x/module.yaml")
	stmts, _ := PlanSchema(m)

	var enable, policy bool
	for _, s := range stmts {
		if strings.Contains(s.SQL, "ENABLE ROW LEVEL SECURITY") {
			enable = true
		}
		if strings.Contains(s.SQL, "CREATE POLICY") && strings.Contains(s.SQL, "app.tenant_id") {
			policy = true
		}
	}
	if !enable || !policy {
		t.Errorf("falta RLS (enable=%v policy=%v)", enable, policy)
	}
}

// Un índice único sin tenant delante permitiría el mismo valor en dos tenants y
// además no es lo que consulta el CRUD, que siempre filtra por tenant.
func TestIndicesIncluyenTenant(t *testing.T) {
	m := &Manifest{Name: "x", fieldOrder: []string{"a"}}
	m.Models = map[string]*ModelDef{
		"a": {Name: "a", Fields: []FieldDef{
			{Name: "nombre", Type: "string", Unique: true},
			{Name: "email", Type: "string", Index: true},
		}},
	}
	idx := IndexSQL(m, m.Models["a"])
	if len(idx) != 2 {
		t.Fatalf("Indices = %v", idx)
	}
	for _, s := range idx {
		if !strings.Contains(s, "(tenant_id)") && !strings.Contains(s, ", tenant_id)") {
			t.Errorf("el índice debe llevar tenant_id: %s", s)
		}
	}
}
