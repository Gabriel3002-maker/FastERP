package module

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	safeIdentRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	allowedOps  = map[string]bool{
		"=": true, "!=": true, ">": true, "<": true, ">=": true, "<=": true,
		"LIKE": true, "ILIKE": true, "NOT ILIKE": true, "IN": true, "NOT IN": true,
		"IS NULL": true, "IS NOT NULL": true,
	}
	allowedDirs = map[string]bool{"ASC": true, "DESC": true, "": true}
)

type whereClause struct {
	col string
	op  string
	arg interface{}

	// raw es la alternativa a col+op+arg: una condición que el builder no sabe
	// montar sola, como un grupo con paréntesis o un ILIKE con ESCAPE. El texto
	// lo escribe el core, nunca el cliente, y sus valores van como parámetros.
	raw  string
	args []interface{}
	// join es cómo se engancha la cláusula a la anterior. La primera de un
	// grupo empieza sola.
	join string
}

// QueryBuilder builds safe SQL with identifier whitelisting.
// Table/column names are validated against the model definition at build time.
type QueryBuilder struct {
	model ModelRegistration

	fields   []string
	wheres   []whereClause
	orderBy  string
	orderDir string
	limit    int
	offset   int
	err      error
}

// NewQueryBuilder creates a builder for a registered model.
func NewQueryBuilder(model ModelRegistration) *QueryBuilder {
	return &QueryBuilder{model: model}
}

func safeIdent(s string) bool {
	return safeIdentRe.MatchString(s)
}

func (qb *QueryBuilder) hasField(name string) bool {
	return qb.model.HasField(name)
}

func (qb *QueryBuilder) fieldDef(name string) *FieldDef {
	for i := range qb.model.Manifest.Fields {
		if qb.model.Manifest.Fields[i].Name == name {
			return &qb.model.Manifest.Fields[i]
		}
	}
	return nil
}

func (qb *QueryBuilder) selectColumnsAndJoins() ([]string, []string, error) {
	fieldNames := qb.fields
	if len(fieldNames) == 0 {
		fieldNames = make([]string, 0, len(qb.model.Manifest.Fields))
		for _, f := range qb.model.Manifest.Fields {
			fieldNames = append(fieldNames, f.Name)
		}
	}

	cols := make([]string, 0, len(fieldNames)+5)
	joins := make([]string, 0, len(fieldNames))
	seen := map[string]bool{"id": true}

	cols = append(cols, "base.id")
	for _, name := range fieldNames {
		if seen[name] {
			continue
		}
		if !qb.hasField(name) {
			return nil, nil, fmt.Errorf("query: unknown field %q in model %s", name, qb.model.Manifest.Name)
		}
		seen[name] = true
		if name == "id" || name == "created_at" || name == "updated_at" || name == "tenant_id" {
			cols = append(cols, "base."+quoteIdent(name))
			continue
		}
		def := qb.fieldDef(name)
		if def != nil && !IsStored(def.Type) {
			// one2many y many2many no son columnas de esta tabla: se hidratan
			// aparte. Pedirlas como columna rompe la consulta.
			continue
		}
		if def != nil && def.Type == "many2one" && def.RelatedModel != "" && def.RelatedField != "" {
			displayAlias := name + "_display"
			joinAlias := "rel_" + name
			cols = append(cols, "base."+quoteIdent(name))
			cols = append(cols, fmt.Sprintf("%s.%s AS %s", quoteIdent(joinAlias), quoteIdent(def.RelatedField), quoteIdent(displayAlias)))
			joins = append(joins, fmt.Sprintf(
				"LEFT JOIN %s %s ON %s.id = base.%s",
				quoteIdent(qb.relatedTableName(def)), quoteIdent(joinAlias), quoteIdent(joinAlias), quoteIdent(name),
			))
			continue
		}
		cols = append(cols, "base."+quoteIdent(name))
	}

	if !seen["created_at"] {
		cols = append(cols, "base.created_at")
	}
	if !seen["updated_at"] {
		cols = append(cols, "base.updated_at")
	}

	return cols, joins, nil
}

func (qb *QueryBuilder) relatedTableName(def *FieldDef) string {
	if def.RelatedModule != "" {
		return fmt.Sprintf("mod_%s_%s", def.RelatedModule, def.RelatedModel)
	}

	// Infer the module name from the current table prefix: mod_<module>_<model>.
	parts := strings.SplitN(qb.model.TableName, "_", 3)
	if len(parts) == 3 {
		return fmt.Sprintf("mod_%s_%s", parts[1], def.RelatedModel)
	}
	return fmt.Sprintf("mod_%s_%s", "unknown", def.RelatedModel)
}

// Select sets the columns to return. Defaults to all fields + id + timestamps.
func (qb *QueryBuilder) Select(fields ...string) *QueryBuilder {
	for _, f := range fields {
		if !qb.hasField(f) {
			qb.err = fmt.Errorf("query: unknown field %q in model %s", f, qb.model.Manifest.Name)
			return qb
		}
	}
	qb.fields = fields
	return qb
}

// Where adds a WHERE condition. col must be a valid field; op must be in whitelist.
func (qb *QueryBuilder) Where(col, op string, val interface{}) *QueryBuilder {
	if !qb.hasField(col) {
		qb.err = fmt.Errorf("query: unknown field %q in WHERE", col)
		return qb
	}
	if !allowedOps[op] {
		qb.err = fmt.Errorf("query: invalid operator %q", op)
		return qb
	}
	qb.wheres = append(qb.wheres, whereClause{col: col, op: op, arg: val, join: "AND"})
	return qb
}

// OrderBy sets ORDER BY. col must be valid; dir must be ASC or DESC.
func (qb *QueryBuilder) OrderBy(col, dir string) *QueryBuilder {
	dirUpper := strings.ToUpper(strings.TrimSpace(dir))
	if col != "" && !qb.hasField(col) {
		qb.err = fmt.Errorf("query: unknown field %q in ORDER BY", col)
		return qb
	}
	if !allowedDirs[dirUpper] {
		qb.err = fmt.Errorf("query: invalid ORDER BY direction %q", dir)
		return qb
	}
	qb.orderBy = col
	qb.orderDir = dirUpper
	return qb
}

func (qb *QueryBuilder) Limit(n int) *QueryBuilder {
	qb.limit = n
	return qb
}

func (qb *QueryBuilder) Offset(n int) *QueryBuilder {
	qb.offset = n
	return qb
}

// HasField dice si un nombre es una columna consultable de este modelo.
func (qb *QueryBuilder) HasField(name string) bool { return qb.hasField(name) }

// WhereRaw añade una condición escrita por el core.
//
// La condición entra como texto y sus valores como parámetros. Eso es lo que
// permite componer un ILIKE con ESCAPE o un grupo con paréntesis sin que el
// cliente pueda inyectar nada: el texto lo escribe el servidor, nunca la
// petición.
func (qb *QueryBuilder) WhereRaw(clause string, args ...interface{}) *QueryBuilder {
	qb.wheres = append(qb.wheres, whereClause{raw: clause, args: args, join: "AND"})
	return qb
}

// OrWhereRaw es WhereRaw unido a la anterior con OR.
//
// El AND del filtro por tenant tiene que separarse del grupo con paréntesis, o
// "tenant = X OR nombre ILIKE %a%" dejaría leer los registros de otro tenant.
// Quien llama mete los paréntesis; el builder no los añade por su cuenta porque
// no sabe dónde empieza el grupo.
func (qb *QueryBuilder) OrWhereRaw(clause string, args ...interface{}) *QueryBuilder {
	qb.wheres = append(qb.wheres, whereClause{raw: clause, args: args, join: "OR"})
	return qb
}

// IsAllowedOp indica si un operador de filtro está en la lista blanca.
//
// EXISTS no está, y no por descuido: permite tocar cosas que no son columnas del
// modelo.
func IsAllowedOp(op string) bool { return allowedOps[strings.ToUpper(op)] }

func quoteIdent(name string) string {
	// PostgreSQL double-quote identifier, with escaping
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func validateSelectColumn(column string) error {
	upper := strings.ToUpper(column)
	expr := column
	alias := ""
	if idx := strings.LastIndex(upper, " AS "); idx != -1 {
		expr = column[:idx]
		alias = strings.TrimSpace(column[idx+4:])
	}

	if alias != "" {
		alias = strings.Trim(alias, `"`)
		if !safeIdent(alias) {
			return fmt.Errorf("query: unsafe alias %q", alias)
		}
	}

	for _, part := range strings.Split(expr, ".") {
		part = strings.Trim(part, `"`)
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !safeIdent(part) {
			return fmt.Errorf("query: unsafe identifier %q", part)
		}
	}
	return nil
}

func (qb *QueryBuilder) BuildSelect() ([]string, string, []interface{}, error) {
	if qb.err != nil {
		return nil, "", nil, qb.err
	}

	tn := qb.model.TableName
	if !safeIdent(tn) {
		return nil, "", nil, fmt.Errorf("query: unsafe table name %q", tn)
	}

	cols, joins, err := qb.selectColumnsAndJoins()
	if err != nil {
		return nil, "", nil, err
	}

	for _, c := range cols {
		if err := validateSelectColumn(c); err != nil {
			return nil, "", nil, err
		}
	}

	var buf strings.Builder
	buf.WriteString("SELECT ")
	for i, c := range cols {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(c)
	}
	buf.WriteString(" FROM ")
	buf.WriteString(quoteIdent(tn))
	buf.WriteString(" AS base")
	for _, join := range joins {
		buf.WriteString(" ")
		buf.WriteString(join)
	}

	// WHERE. La primera cláusula no lleva conector; las siguientes, el suyo.
	// Un OR sin paréntesis después de un AND ampliaría el filtro en vez de
	// añadirlo, así que quien usa OrWhereRaw mete sus propios paréntesis.
	var args []interface{}
	if len(qb.wheres) > 0 {
		buf.WriteString(" WHERE ")
	}
	for i, w := range qb.wheres {
		if i > 0 {
			buf.WriteString(" " + w.join + " ")
		}
		if w.raw != "" {
			buf.WriteString(qb.expandRaw(w.raw, w.args, &args))
			continue
		}
		if w.op == "IS NULL" || w.op == "IS NOT NULL" {
			fmt.Fprintf(&buf, "%s %s", quoteIdent(w.col), w.op)
			continue
		}
		fmt.Fprintf(&buf, "%s %s $%d", quoteIdent(w.col), w.op, len(args)+1)
		args = append(args, w.arg)
	}

	if qb.orderBy != "" {
		fmt.Fprintf(&buf, " ORDER BY %s %s", quoteIdent(qb.orderBy), qb.orderDir)
	}
	if qb.limit > 0 {
		fmt.Fprintf(&buf, " LIMIT %d", qb.limit)
	}
	if qb.offset > 0 {
		fmt.Fprintf(&buf, " OFFSET %d", qb.offset)
	}

	// expandRaw puede fallar al contar los parámetros, y eso pasa durante el
	// ensamblado: el error se guarda aquí y se lee ahora. Sin esta comprobación
	// la consulta mal formada se devolvería como si fuera buena.
	if qb.err != nil {
		return nil, "", nil, qb.err
	}

	return cols, buf.String(), args, nil
}

// expandRaw sustituye los "?" de una cláusula por los marcadores de Postgres y
// va añadiendo sus valores a args.
//
// Los "?" están fuera de las comillas, que es lo único que los hace marcadores.
// Un "?" dentro de un literal de texto es un carácter y no se toca: sin esto, un
// valor que lo contenga rompería la numeración de los parámetros y la consulta
// leería el valor equivocado en la columna equivocada.
func (qb *QueryBuilder) expandRaw(clause string, vals []interface{}, args *[]interface{}) string {
	var out strings.Builder
	inSingle, inDouble := false, false

	for i := 0; i < len(clause); i++ {
		c := clause[i]
		switch {
		case inSingle:
			if c == '\'' {
				// '' es un apostrophe escapado dentro del literal.
				if i+1 < len(clause) && clause[i+1] == '\'' {
					out.WriteString("''")
					i++
					continue
				}
				inSingle = false
			}
		case inDouble:
			if c == '\\' {
				out.WriteByte(c)
				if i+1 < len(clause) {
					i++
					out.WriteByte(clause[i])
				}
				continue
			} else if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == '?':
			*args = append(*args, vals[0])
			vals = vals[1:]
			fmt.Fprintf(&out, "$%d", len(*args))
			continue
		}
		out.WriteByte(c)
	}

	if len(vals) > 0 {
		// Más valores que marcadores: la cláusula se escribió mal y es mejor
		// fallar aquí que mandar una consulta que no es la que se cree.
		qb.err = fmt.Errorf("query: la condición tiene %d parámetros de más", len(vals))
	}
	return out.String()
}

// BuildCount genera el COUNT con los mismos filtros, para el paginador.
//
// Comparte el WHERE con BuildSelect a propósito: si el conteo usara otros
// filtros, el paginador prometería páginas que no existen.
func (qb *QueryBuilder) BuildCount() (string, []interface{}, error) {
	if qb.err != nil {
		return "", nil, qb.err
	}

	tn := qb.model.TableName
	if !safeIdent(tn) {
		return "", nil, fmt.Errorf("count: unsafe table name %q", tn)
	}

	var buf strings.Builder
	buf.WriteString("SELECT COUNT(*) FROM " + quoteIdent(tn))

	var args []interface{}
	if len(qb.wheres) > 0 {
		buf.WriteString(" WHERE ")
	}
	for i, w := range qb.wheres {
		if i > 0 {
			buf.WriteString(" " + w.join + " ")
		}
		if w.raw != "" {
			buf.WriteString(qb.expandRaw(w.raw, w.args, &args))
			continue
		}
		if w.op == "IS NULL" || w.op == "IS NOT NULL" {
			fmt.Fprintf(&buf, "%s %s", quoteIdent(w.col), w.op)
			continue
		}
		fmt.Fprintf(&buf, "%s %s $%d", quoteIdent(w.col), w.op, len(args)+1)
		args = append(args, w.arg)
	}

	// Mismo motivo que en BuildSelect: expandRaw puede fallar al contar.
	if qb.err != nil {
		return "", nil, qb.err
	}
	return buf.String(), args, nil
}

// BuildInsert generates INSERT ... RETURNING. values is a map of field→value.
// userID se graba en created_by/updated_by para dejar quién creó el registro.
func (qb *QueryBuilder) BuildInsert(tenantID, userID string, values map[string]interface{}) (string, []interface{}, error) {
	if qb.err != nil {
		return "", nil, qb.err
	}

	tn := qb.model.TableName
	if !safeIdent(tn) {
		return "", nil, fmt.Errorf("query: unsafe table name %q", tn)
	}

	var cols []string
	var args []interface{}
	for _, f := range qb.model.Manifest.Fields {
		if !IsStored(f.Type) {
			continue
		}
		cols = append(cols, f.Name)
		if v, ok := values[f.Name]; ok {
			args = append(args, v)
		} else {
			args = append(args, nil)
		}
	}
	cols = append(cols, "tenant_id")
	args = append(args, tenantID)
	cols = append(cols, "created_by", "updated_by")
	if userID == "" {
		args = append(args, nil, nil)
	} else {
		args = append(args, userID, userID)
	}

	for _, c := range cols {
		if !safeIdent(c) {
			return "", nil, fmt.Errorf("query: unsafe column name %q", c)
		}
	}

	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	returnCols := []string{"id"}
	for _, f := range qb.model.Manifest.Fields {
		if !IsStored(f.Type) {
			continue
		}
		returnCols = append(returnCols, f.Name)
	}
	returnCols = append(returnCols, "created_at", "updated_at", "created_by", "updated_by")

	var buf strings.Builder
	fmt.Fprintf(&buf, "INSERT INTO %s (%s) VALUES (%s) RETURNING ",
		quoteIdent(tn),
		strings.Join(quoteSlice(cols), ", "),
		strings.Join(placeholders, ", "),
	)
	for i, c := range returnCols {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(quoteIdent(c))
	}

	return buf.String(), args, nil
}

// BuildUpdate generates UPDATE ... RETURNING. values is a map of field→value.
// userID se graba en updated_by para dejar quién editó por última vez.
func (qb *QueryBuilder) BuildUpdate(id, tenantID, userID string, values map[string]interface{}) (string, []interface{}, error) {
	if qb.err != nil {
		return "", nil, qb.err
	}

	tn := qb.model.TableName
	if !safeIdent(tn) {
		return "", nil, fmt.Errorf("query: unsafe table name %q", tn)
	}

	var setCols []string
	var args []interface{}
	argIdx := 1
	for _, f := range qb.model.Manifest.Fields {
		if !IsStored(f.Type) {
			continue
		}
		if v, ok := values[f.Name]; ok {
			setCols = append(setCols, f.Name)
			args = append(args, v)
			argIdx++
		}
	}
	for _, c := range setCols {
		if !safeIdent(c) {
			return "", nil, fmt.Errorf("query: unsafe column name %q", c)
		}
	}

	if len(setCols) == 0 {
		return "", nil, fmt.Errorf("query: no fields to update")
	}

	setClauses := make([]string, len(setCols))
	for i, c := range setCols {
		setClauses[i] = fmt.Sprintf("%s = $%d", quoteIdent(c), i+1)
	}

	if userID == "" {
		args = append(args, nil)
	} else {
		args = append(args, userID)
	}
	args = append(args, id, tenantID)

	returnCols := []string{"id"}
	for _, f := range qb.model.Manifest.Fields {
		if !IsStored(f.Type) {
			continue
		}
		returnCols = append(returnCols, f.Name)
	}
	returnCols = append(returnCols, "created_at", "updated_at", "created_by", "updated_by")

	var buf strings.Builder
	fmt.Fprintf(&buf, "UPDATE %s SET %s, updated_at = NOW(), updated_by = $%d WHERE id = $%d AND tenant_id = $%d RETURNING ",
		quoteIdent(tn),
		strings.Join(setClauses, ", "),
		len(setCols)+1,
		len(setCols)+2,
		len(setCols)+3,
	)
	for i, c := range returnCols {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(quoteIdent(c))
	}

	return buf.String(), args, nil
}

// BuildDelete generates DELETE ... with id + tenant_id.
func (qb *QueryBuilder) BuildDelete() (string, []interface{}, error) {
	if qb.err != nil {
		return "", nil, qb.err
	}

	tn := qb.model.TableName
	if !safeIdent(tn) {
		return "", nil, fmt.Errorf("query: unsafe table name %q", tn)
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1 AND tenant_id = $2", quoteIdent(tn))
	return query, nil, nil
}

func quoteSlice(strs []string) []string {
	res := make([]string, len(strs))
	for i, s := range strs {
		res[i] = quoteIdent(s)
	}
	return res
}
