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
		"ILIKE": true, "IN": true, "NOT IN": true,
		"IS NULL": true, "IS NOT NULL": true,
	}
	allowedDirs = map[string]bool{"ASC": true, "DESC": true, "": true}
)

type whereClause struct {
	col string
	op  string
	arg interface{}
}

// QueryBuilder builds safe SQL with identifier whitelisting.
// Table/column names are validated against the model definition at build time.
type QueryBuilder struct {
	model ModelRegistration

	fields  []string
	wheres  []whereClause
	orderBy string
	orderDir string
	limit   int
	offset  int
	err     error
}

// NewQueryBuilder creates a builder for a registered model.
func NewQueryBuilder(model ModelRegistration) *QueryBuilder {
	return &QueryBuilder{model: model}
}

func safeIdent(s string) bool {
	return safeIdentRe.MatchString(s)
}

func (qb *QueryBuilder) hasField(name string) bool {
	if name == "id" || name == "tenant_id" || name == "created_at" || name == "updated_at" {
		return true
	}
	for _, f := range qb.model.Manifest.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
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
	qb.wheres = append(qb.wheres, whereClause{col, op, val})
	return qb
}

// OrderBy sets ORDER BY. col must be valid; dir must be ASC or DESC.
func (qb *QueryBuilder) OrderBy(col, dir string) *QueryBuilder {
	if col != "" && !qb.hasField(col) {
		qb.err = fmt.Errorf("query: unknown field %q in ORDER BY", col)
		return qb
	}
	if !allowedDirs[dir] {
		qb.err = fmt.Errorf("query: invalid ORDER BY direction %q", dir)
		return qb
	}
	qb.orderBy = col
	qb.orderDir = dir
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

	var args []interface{}
	argIdx := 1
	for i, w := range qb.wheres {
		sep := " WHERE "
		if i > 0 {
			sep = " AND "
		}
		if w.op == "IS NULL" || w.op == "IS NOT NULL" {
			fmt.Fprintf(&buf, "%s%s %s", sep, quoteIdent(w.col), w.op)
		} else {
			fmt.Fprintf(&buf, "%s%s %s $%d", sep, quoteIdent(w.col), w.op, argIdx)
			args = append(args, w.arg)
			argIdx++
		}
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

	return cols, buf.String(), args, nil
}

// BuildInsert generates INSERT ... RETURNING. values is a map of field→value.
func (qb *QueryBuilder) BuildInsert(tenantID string, values map[string]interface{}) (string, []interface{}, error) {
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
		cols = append(cols, f.Name)
		if v, ok := values[f.Name]; ok {
			args = append(args, v)
		} else {
			args = append(args, nil)
		}
	}
	cols = append(cols, "tenant_id")
	args = append(args, tenantID)

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
		returnCols = append(returnCols, f.Name)
	}
	returnCols = append(returnCols, "created_at", "updated_at")

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
func (qb *QueryBuilder) BuildUpdate(id, tenantID string, values map[string]interface{}) (string, []interface{}, error) {
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

	args = append(args, id, tenantID)

	returnCols := []string{"id"}
	for _, f := range qb.model.Manifest.Fields {
		returnCols = append(returnCols, f.Name)
	}
	returnCols = append(returnCols, "created_at", "updated_at")

	var buf strings.Builder
	fmt.Fprintf(&buf, "UPDATE %s SET %s, updated_at = NOW() WHERE id = $%d AND tenant_id = $%d RETURNING ",
		quoteIdent(tn),
		strings.Join(setClauses, ", "),
		len(setCols)+1,
		len(setCols)+2,
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
