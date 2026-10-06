package models

import (
	"fmt"
	"log"

	"github.com/fasterp/backend/internal/db"
)

func ensureColumn(table, column, colType string) {
	_, err := db.DB.Exec(fmt.Sprintf(
		`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s`, table, column, colType,
	))
	if err != nil {
		log.Printf("[DB] Column check %s.%s: %v", table, column, err)
	}
}

func AutoMigrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS tenants (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(255) NOT NULL,
			slug VARCHAR(100) UNIQUE NOT NULL,
			active BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			username VARCHAR(100) NOT NULL,
			email VARCHAR(255) NOT NULL,
			password_hash TEXT NOT NULL,
			is_admin BOOLEAN DEFAULT false,
			active BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(tenant_id, username),
			UNIQUE(tenant_id, email)
		)`,
		`CREATE TABLE IF NOT EXISTS installed_modules (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			name VARCHAR(100) NOT NULL,
			version VARCHAR(50) NOT NULL,
			label VARCHAR(255) NOT NULL,
			description TEXT DEFAULT '',
			author VARCHAR(255) DEFAULT '',
			icon VARCHAR(255) DEFAULT '',
			active BOOLEAN DEFAULT false,
			checksum TEXT DEFAULT '',
			installed_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(tenant_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS user_permissions (
			tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			module VARCHAR(100) NOT NULL,
			model VARCHAR(100) NOT NULL,
			action VARCHAR(20) NOT NULL,
			PRIMARY KEY (tenant_id, user_id, module, model, action)
		)`,
		`CREATE TABLE IF NOT EXISTS refresh_tokens (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL,
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
	}

	for _, q := range queries {
		if _, err := db.DB.Exec(q); err != nil {
			return err
		}
	}

	// Ensure columns exist (safe for re-runs on existing schemas)
	ensureColumn("installed_modules", "checksum", "TEXT DEFAULT ''")

	// Contacts v2 — native table with LATAM accounting fields
	contactsTable := `CREATE TABLE IF NOT EXISTS contacts (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		tenant_id UUID NOT NULL,
		name VARCHAR(255) NOT NULL,
		email VARCHAR(255),
		phone VARCHAR(255),
		mobile VARCHAR(255),
		company VARCHAR(255),
		job_title VARCHAR(255),
		tax_id_type VARCHAR(50),
		tax_id VARCHAR(50),
		person_type VARCHAR(50),
		tax_regime VARCHAR(100),
		accounting_obligation VARCHAR(50),
		retention_agent VARCHAR(50),
		address TEXT,
		city VARCHAR(255),
		province VARCHAR(255),
		country VARCHAR(255),
		postal_code VARCHAR(20),
		website VARCHAR(255),
		notes TEXT,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	)`
	db.DB.Exec(contactsTable)
	db.DB.Exec("CREATE INDEX IF NOT EXISTS idx_contacts_tenant ON contacts(tenant_id)")

	// Tenant isolation as defense-in-depth behind the explicit WHERE tenant_id filters.
	// tenants is deliberately excluded: it is the tenant registry itself and is resolved
	// on the shared pool before any tenant context exists.
	for _, t := range []string{"contacts", "users", "installed_modules", "refresh_tokens"} {
		db.ApplyTenantRLS(t)
	}

	// Add indexes for performance
	indexes := []struct {
		name string
		sql  string
	}{
		{"idx_users_tenant", "CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id)"},
		{"idx_users_username", "CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)"},
		{"idx_installed_modules_tenant", "CREATE INDEX IF NOT EXISTS idx_installed_modules_tenant ON installed_modules(tenant_id)"},
		{"idx_installed_modules_active", "CREATE INDEX IF NOT EXISTS idx_installed_modules_active ON installed_modules(tenant_id, active)"},
		{"idx_refresh_tokens_user", "CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id)"},
		{"idx_refresh_tokens_expires", "CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires ON refresh_tokens(expires_at)"},
	}
	for _, idx := range indexes {
		if _, err := db.DB.Exec(idx.sql); err != nil {
			log.Printf("[DB] Index skipped (%s): %v", idx.name, err)
		}
	}

	log.Println("[DB] Migration completed")
	return nil
}
