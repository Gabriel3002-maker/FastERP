package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

var DB *sql.DB

// QueryExecutor is satisfied by both *sql.DB and *sql.Conn.
type QueryExecutor interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

// Executor returns the per-request connection pinned by TenantMiddleware, which has
// app.tenant_id configured so RLS policies apply.
//
// There is no fallback to the shared pool, and that is deliberate. The shared
// pool carries whatever app.tenant_id the last request left on it, so a handler
// that reached it by mistake would not run "without a tenant context" — it would
// run under someone else's. Silently returning DB made that mistake invisible,
// and the query would succeed. Any handler behind TenantMiddleware gets its
// connection from it, so a missing db_conn means the middleware did not run:
// that is a routing bug, and it panics rather than reading the wrong tenant's
// data. gin.Recovery turns it into a 500, which is the safe direction.
//
// Routes that legitimately have no tenant context (liveness, readiness, tenant
// resolution, public pages) do not go through Executor: they use DB or
// AcquireConn explicitly.
func Executor(c *gin.Context) QueryExecutor {
	v, ok := c.Get("db_conn")
	if !ok {
		panic("db.Executor called without TenantMiddleware: no connection pinned to a tenant. " +
			"This handler is reachable outside a tenant-scoped route.")
	}
	conn, ok := v.(*TenantConn)
	if !ok {
		panic(fmt.Sprintf("db.Executor: unexpected db_conn type %T", v))
	}
	return conn
}

type ConnConfig struct {
	DatabaseURL  string
	MaxOpenConns int
	MaxIdleConns int
}

func Connect(cfg ConnConfig) error {
	var err error
	DB, err = sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err = DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	DB.SetMaxOpenConns(cfg.MaxOpenConns)
	DB.SetMaxIdleConns(cfg.MaxIdleConns)
	DB.SetConnMaxLifetime(30 * time.Minute)
	DB.SetConnMaxIdleTime(5 * time.Minute)

	log.Printf("[DB] Connected to PostgreSQL (max_open=%d, max_idle=%d)", cfg.MaxOpenConns, cfg.MaxIdleConns)
	return nil
}

func Close() {
	if DB != nil {
		DB.Close()
	}
}

// AcquireConn gets a dedicated connection and sets the RLS tenant context.
// The caller MUST close the returned connection after use.
//
// app.tenant_id se fija con is_local=false —de sesión, no de transacción—
// porque el código no abre una transacción por petición. Eso hace obligatorio
// limpiarlo al devolver la conexión: sql.Conn.Close() devuelve la conexión
// física al pool, database/sql solo invoca driver.SessionResetter, y lib/pq no
// toca ninguna GUC. Sin el RESET, la conexión vuelve al pool con el tenant del
// request anterior pegado, y la siguiente consulta que use el pool compartido
// se ejecuta bajo el contexto de otro usuario. Por eso Close() va sobreescrito.
func AcquireConn(ctx context.Context, tenantID string) (*TenantConn, error) {
	conn, err := DB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tenantID)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	return &TenantConn{Conn: conn}, nil
}

// TenantConn es una conexión fijada a un tenant. Embebe *sql.Conn, así que
// satisface QueryExecutor sin cambiar los handlers, y sobreescribe Close para
// limpiar app.tenant_id antes de devolverla al pool.
//
// El reset va con un contexto nuevo y sin cancelación: si el contexto de la
// petición ya está cancelado, el RESET no llegaría a ejecutarse y la conexión
// volvería al pool sucia. Por eso no se reutiliza el ctx del request.
type TenantConn struct {
	*sql.Conn
	closeOnce sync.Once
	err       error
}

// Close limpia el contexto de tenant y devuelve la conexión al pool. Es
// idempotente: un segundo Close no hace nada y no devuelve error, porque
// database/sql no admite cerrar dos veces la misma Conn.
func (t *TenantConn) Close() error {
	t.closeOnce.Do(func() {
		// El RESET va con un contexto nuevo: si el de la petición ya está
		// cancelado, no llegaría a ejecutarse y la conexión volvería al pool con
		// el tenant del request anterior pegado.
		if _, err := t.Conn.ExecContext(context.Background(), "RESET app.tenant_id"); err != nil {
			// Se anota el fallo pero se cierra igual: soltar la conexión es
			// obligatorio o el pool se agota.
			t.err = fmt.Errorf("failed to reset tenant context: %w", err)
		}
		if err := t.Conn.Close(); err != nil && t.err == nil {
			t.err = err
		}
	})
	return t.err
}

// ApplyTenantRLS enables and enforces row-level security on a tenant-scoped table.
//
// FORCE is required: without it the table owner — which is who the app connects as —
// silently bypasses every policy. A superuser bypasses RLS even with FORCE, which is
// what CheckRLSEnforcement warns about at startup.
//
// The policy reads app.tenant_id with missing_ok=true so a connection that never set it
// (the shared pool, used for DDL) yields NULL, and therefore zero rows, instead of
// raising "unrecognized configuration parameter".
func ApplyTenantRLS(table string) {
	quoted := pq.QuoteIdentifier(table)
	stmts := []string{
		fmt.Sprintf(`ALTER TABLE %s ENABLE ROW LEVEL SECURITY`, quoted),
		fmt.Sprintf(`ALTER TABLE %s FORCE ROW LEVEL SECURITY`, quoted),
		fmt.Sprintf(`DROP POLICY IF EXISTS tenant_isolation ON %s`, quoted),
		fmt.Sprintf(
			`CREATE POLICY tenant_isolation ON %s FOR ALL
			 USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)`,
			quoted,
		),
	}
	for _, s := range stmts {
		if _, err := DB.Exec(s); err != nil {
			log.Printf("[DB] RLS setup failed on %s: %v", table, err)
			return
		}
	}
}

// WithTenant runs fn on a connection scoped to tenantID, closing it afterwards.
// Used by startup/seed code, which writes to RLS-protected tables outside any request.
func WithTenant(ctx context.Context, tenantID string, fn func(QueryExecutor) error) error {
	conn, err := AcquireConn(ctx, tenantID)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(conn)
}

// CheckRLSEnforcement reports whether the connecting role can bypass row-level
// security. A superuser (or a role with BYPASSRLS) ignores every policy, which
// makes tenant isolation silently decorative — so this is loud on purpose.
func CheckRLSEnforcement() {
	var isSuper, canBypass bool
	err := DB.QueryRow(
		"SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user",
	).Scan(&isSuper, &canBypass)
	if err != nil {
		log.Printf("[DB] Could not verify RLS enforcement: %v", err)
		return
	}
	if isSuper || canBypass {
		log.Printf("[WARN] ==========================================================")
		log.Printf("[WARN] Database role has rolsuper=%v rolbypassrls=%v.", isSuper, canBypass)
		log.Printf("[WARN] Row-level security policies are NOT enforced for this role;")
		log.Printf("[WARN] tenant isolation currently rests only on explicit WHERE")
		log.Printf("[WARN] tenant_id filters in application code.")
		log.Printf("[WARN] Fix: connect as a dedicated non-superuser role, e.g.")
		log.Printf("[WARN]   CREATE ROLE fasterp_app LOGIN PASSWORD '...' NOBYPASSRLS;")
		log.Printf("[WARN]   GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES")
		log.Printf("[WARN]     IN SCHEMA public TO fasterp_app;")
		log.Printf("[WARN] ==========================================================")
		return
	}
	log.Println("[DB] RLS enforcement active (role cannot bypass policies)")
}
