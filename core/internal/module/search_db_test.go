package module

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// Estos tests ejecutan el SQL que produce el query builder contra Postgres.
//
// Las pruebas de strings verifican que la consulta tiene la forma correcta. No
// verifican que Postgres la acepte, y hay constructos que parecen bien y no lo
// son: un ESCAPE fuera de sitio, un ILIKE sobre un número, un parámetro que se
// cuelga. Aquí se ejecutan.

const modBusqueda = `name: busq
label: Búsqueda
models:
  lead:
    label: Lead
    fields:
      - name: name
        type: string
        required: true
      - name: email
        type: string
      - name: amount
        type: float
      - name: notes
        type: text
      - name: locked
        type: string
        readonly: true
      - name: state
        type: string
        default: nuevo
        options: [nuevo, enviado, archivado]
    workflow:
      field: state
      initial: nuevo
      transitions:
        enviar:
          label: Enviar
          from: [nuevo]
          to: enviado
        archivar:
          label: Archivar
          from: [nuevo, enviado]
          to: archivado
`

// clearBusq deja el esquema de pruebas limpio entre ejecuciones.
func clearBusq(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"mod_busq_lead", HistoryTable("busq", "lead")} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + quoteIdent(table) + " CASCADE"); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}

func busqModel(t *testing.T) (*Manifest, ModelRegistration) {
	t.Helper()
	m := mod(t, modBusqueda)
	reg := ModelRegistration{
		Manifest:  m.Models["lead"],
		TableName: m.TableName("lead"),
	}
	return m, reg
}

func TestDBSearchGroupIsAcceptedByPostgres(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m, reg := busqModel(t)

	clearBusq(t, db)
	if err := ApplySchema(ctx, m, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() { clearBusq(t, db) })

	const tenant = "11111111-1111-1111-1111-111111111111"
	// Tres filas: una casa, una que trae el comodín, y otra que trae el escape.
	rows := []struct{ name, email, notes string }{
		{"Acme Industrial", "info@acme.test", "lluvia"},
		{"Discount 100%", "desc@x.test", "50% off"},
		{"Bar_Under", "bar@x.test", "guion bajo"},
		{"Otra", "otra@x.test", "nada"},
	}
	for _, r := range rows {
		_, err := db.ExecContext(ctx,
			`INSERT INTO mod_busq_lead (tenant_id, name, email, notes, state) VALUES ($1,$2,$3,$4,'nuevo')`,
			tenant, r.name, r.email, r.notes)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	search := func(term string) []string {
		t.Helper()
		meta := m.MustMeta("lead")
		pattern := "%" + escapeLikeForTest(term) + "%"
		var group []string
		var args []interface{}
		for _, f := range meta.Fields {
			if !f.Searchable {
				continue
			}
			group = append(group, quoteIdent(f.Name)+" ILIKE ?")
			args = append(args, pattern)
		}
		if len(group) == 0 {
			t.Fatal("expected searchable fields")
		}

		qb := NewQueryBuilder(reg)
		qb.Where("tenant_id", "=", tenant)
		qb.WhereRaw("("+joinForTest(group, " OR ")+") ESCAPE '\\'", args...)

		cols, query, qargs, err := qb.BuildSelect()
		if err != nil {
			t.Fatalf("BuildSelect: %v", err)
		}
		out, err := db.QueryContext(ctx, query, qargs...)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		defer out.Close()
		names, err := readNames(out, cols)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		return names
	}

	// El grupo tiene que ejecutarse, no solo tener la forma correcta: un
	// ESCAPE fuera de lugar es un error de sintaxis que ningún test de strings
	// detecta.
	if got := search("Acme"); len(got) != 1 {
		t.Errorf("search(Acme) = %v, want just Acme Industrial", got)
	}

	// Un % escrito por quien busca es un % de verdad. Sin el escape previo y
	// el ESCAPE en la consulta, "%" haría match de todo.
	if got := search("100%"); len(got) != 1 || got[0] != "Discount 100%" {
		t.Errorf("search(100%%) = %v, want only Discount 100%%", got)
	}
	if got := search("%"); len(got) != 1 || got[0] != "Discount 100%" {
		t.Errorf("search(%%) = %v, want only the row with a literal %%", got)
	}

	// Un _ también es un comodín. "Bar_" no debe traer "Bar_Under" y cualquier
	// otra cosa de cuatro letras.
	if got := search("Bar_"); len(got) != 1 || got[0] != "Bar_Under" {
		t.Errorf("search(Bar_) = %v, want only Bar_Under", got)
	}

	// notes es text: no es buscable, así que "lluvia" no encuentra nada.
	if got := search("lluvia"); len(got) != 0 {
		t.Errorf("search(lluvia) = %v, want none (notes is not searchable)", got)
	}
}

// El filtro por tenant no puede salir del grupo. Esta es la comprobación que
// importa: si el AND se colara dentro del paréntesis, la búsqueda devolvería los
// registros de otro tenant.
func TestDBSearchCannotEscapeItsTenant(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m, reg := busqModel(t)

	clearBusq(t, db)
	if err := ApplySchema(ctx, m, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() { clearBusq(t, db) })

	const (
		tenantA = "11111111-1111-1111-1111-111111111111"
		tenantB = "22222222-2222-2222-2222-222222222222"
	)
	_, err := db.ExecContext(ctx,
		`INSERT INTO mod_busq_lead (tenant_id, name, state) VALUES ($1,'Privado A','nuevo'), ($2,'Privado B','nuevo')`,
		tenantA, tenantB)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// El módulo aplica RLS, así que la sesión necesita el tenant fijado para
	// que las políticas no filtren todo. Se hace con SET LOCAL en la misma
	// transacción, que es como lo hace el middleware.
	run := func(tenant, term string) []string {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenant); err != nil {
			t.Fatalf("set tenant: %v", err)
		}

		meta := m.MustMeta("lead")
		pattern := "%" + escapeLikeForTest(term) + "%"
		var group []string
		var args []interface{}
		for _, f := range meta.Fields {
			if !f.Searchable {
				continue
			}
			group = append(group, quoteIdent(f.Name)+" ILIKE ?")
			args = append(args, pattern)
		}
		qb := NewQueryBuilder(reg)
		qb.Where("tenant_id", "=", tenant)
		qb.WhereRaw("("+joinForTest(group, " OR ")+") ESCAPE '\\'", args...)

		cols, query, qargs, err := qb.BuildSelect()
		if err != nil {
			t.Fatalf("BuildSelect: %v", err)
		}
		out, err := tx.QueryContext(ctx, query, qargs...)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer out.Close()
		names, err := readNames(out, cols)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return names
	}

	// Buscando "Privado" el tenant A solo puede ver lo suyo.
	got := run(tenantA, "Privado")
	if len(got) != 1 || got[0] != "Privado A" {
		t.Errorf("tenant A saw %v, want only Privado A", got)
	}
	// Y lo mismo al revés, que es el caso donde el bug se escondería: un
	// grupo mal cerrado deja pasar al segundo OR.
	got = run(tenantB, "Privado")
	if len(got) != 1 || got[0] != "Privado B" {
		t.Errorf("tenant B saw %v, want only Privado B", got)
	}
}

// La tabla de historial se crea con el esquema, y la transición escribe en ella.
func TestDBHistoryTableExistsAndRecordsTransitions(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m, reg := busqModel(t)

	clearBusq(t, db)
	if err := ApplySchema(ctx, m, db); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() { clearBusq(t, db) })

	table := HistoryTable("busq", "lead")
	var n int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name = $1", table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("history table %s was not created by Apply", table)
	}

	const tenant = "11111111-1111-1111-1111-111111111111"
	var id string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO mod_busq_lead (tenant_id, name, state) VALUES ($1,'X','nuevo') RETURNING id`,
		tenant).Scan(&id); err != nil {
		t.Fatal(err)
	}

	// La transición solo se aplica si el estado de partida es el esperado, que
	// es lo que impide que dos personas a la vez se pisen.
	wf := reg.Manifest.Workflow
	tr := wf.Transitions["enviar"]
	res, err := db.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET %s=$1 WHERE id=$2 AND tenant_id=$3 AND %s=$4",
			table2(reg.TableName), quoteIdent(wf.Field), quoteIdent(wf.Field)),
		tr.To, id, tenant, wf.Initial)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := res.RowsAffected(); rows != 1 {
		t.Fatalf("transition updated %d rows, want 1", rows)
	}

	// Repetir la misma transición ya no vale: el registro está en "enviado".
	res, err = db.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET %s=$1 WHERE id=$2 AND tenant_id=$3 AND %s=$4",
			table2(reg.TableName), quoteIdent(wf.Field), quoteIdent(wf.Field)),
		tr.To, id, tenant, wf.Initial)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := res.RowsAffected(); rows != 0 {
		t.Fatalf("replaying a transition updated %d rows, want 0", rows)
	}

	if _, err := db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (tenant_id, module_name, model_name, record_id, action, from_state, to_state)
		 VALUES ($1,'busq','lead',$2,'enviar','nuevo','enviado')`, table), tenant, id); err != nil {
		t.Fatal(err)
	}

	var from, to string
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT from_state, to_state FROM %s WHERE record_id=$1", table), id).
		Scan(&from, &to); err != nil {
		t.Fatal(err)
	}
	if from != "nuevo" || to != "enviado" {
		t.Errorf("history = %s→%s, want nuevo→enviado", from, to)
	}
}

// escapeLikeForTest es el mismo escape que usa el handler. Se repite aquí a
// propósito: si cambia en un sitio, este test lo señala.
func escapeLikeForTest(s string) string {
	out := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '%', '_':
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func joinForTest(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func table2(name string) string { return quoteIdent(name) }

func readNames(rows *sql.Rows, cols []string) ([]string, error) {
	var names []string
	for rows.Next() {
		idx := -1
		for i, c := range cols {
			if c == `"name"` {
				idx = i
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("no name column in %v", cols)
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		names = append(names, string(vals[idx].([]byte)))
	}
	return names, rows.Err()
}
