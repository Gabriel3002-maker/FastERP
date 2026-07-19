package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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
// app.tenant_id configured so RLS policies apply. It falls back to the shared pool,
// which carries no tenant context — that fallback is for startup and public paths
// that resolve their own tenant, never for handlers behind TenantMiddleware.
func Executor(c *gin.Context) QueryExecutor {
	if conn, ok := c.Get("db_conn"); ok {
		return conn.(*sql.Conn)
	}
	return DB
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
// app.tenant_id is set with is_local=false so it survives for the whole session
// rather than only the current transaction — the connection is scoped to one
// request and returned to the pool on Close, which resets it.
func AcquireConn(ctx context.Context, tenantID string) (*sql.Conn, error) {
	conn, err := DB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tenantID)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	return conn, nil
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
