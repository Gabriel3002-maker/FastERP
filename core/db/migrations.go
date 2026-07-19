package db

import (
	"context"
	"log"
)

func RunMigrations(db *DB) error {
	log.Println("[DB] Running migrations...")

	migrations := []struct {
		name  string
		query string
	}{
		{
			name: "create_tenants",
			query: `CREATE TABLE IF NOT EXISTS tenants (
				id UUID PRIMARY KEY,
				name VARCHAR(255) NOT NULL,
				slug VARCHAR(255) UNIQUE NOT NULL,
				active BOOLEAN DEFAULT true,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);`,
		},
		{
			name: "create_users",
			query: `CREATE TABLE IF NOT EXISTS users (
				id UUID PRIMARY KEY,
				tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
				username VARCHAR(255) NOT NULL,
				email VARCHAR(255) NOT NULL,
				password_hash VARCHAR(255) NOT NULL,
				is_admin BOOLEAN DEFAULT false,
				active BOOLEAN DEFAULT true,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(tenant_id, username),
				UNIQUE(tenant_id, email)
			);`,
		},
		{
			name: "create_installed_modules",
			query: `CREATE TABLE IF NOT EXISTS installed_modules (
				id UUID PRIMARY KEY,
				tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
				name VARCHAR(255) NOT NULL,
				version VARCHAR(50) NOT NULL,
				label VARCHAR(255) NOT NULL,
				description TEXT,
				author VARCHAR(255),
				icon VARCHAR(100),
				active BOOLEAN DEFAULT true,
				installed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(tenant_id, name)
			);`,
		},
		{
			name: "create_module_manifests",
			query: `CREATE TABLE IF NOT EXISTS module_manifests (
				id UUID PRIMARY KEY,
				module_id UUID NOT NULL REFERENCES installed_modules(id) ON DELETE CASCADE,
				manifest_json JSONB NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);`,
		},
		{
			name: "create_audit_log",
			query: `CREATE TABLE IF NOT EXISTS audit_log (
				id VARCHAR(255) PRIMARY KEY,
				tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
				user_id UUID REFERENCES users(id) ON DELETE SET NULL,
				action VARCHAR(50) NOT NULL,
				entity VARCHAR(255) NOT NULL,
				entity_id VARCHAR(255) NOT NULL,
				before JSONB,
				after JSONB,
				status VARCHAR(50) DEFAULT 'success',
				error TEXT,
				ip_address VARCHAR(45),
				user_agent TEXT,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);`,
		},
		{
			name: "create_audit_log_indexes",
			query: `
				CREATE INDEX IF NOT EXISTS idx_audit_tenant ON audit_log(tenant_id);
				CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_log(user_id);
				CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_log(entity);
				CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC);
			`,
		},
	}

	ctx := context.Background()

	for _, mig := range migrations {
		log.Printf("[Migration] %s...", mig.name)
		if _, err := db.Exec(ctx, mig.query); err != nil {
			return err
		}
		log.Printf("[Migration] ✓ %s", mig.name)
	}

	log.Println("[DB] ✓ All migrations completed")
	return nil
}
