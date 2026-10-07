-- Esquema base del core: tenants, usuarios, permisos, refresh tokens, módulos
-- instalados y contactos.
--
-- Este fichero es el baseline y no se edita. Su única obligación es reproducir,
-- de forma idempotente, el esquema que el reconciliador creaba antes de que
-- existieran migraciones versionadas: así una base ya existente lo aplica como
-- una operación nula y solo queda el registro en schema_migrations, mientras
-- que una base nueva sale creada desde cero.
--
-- Los cambios reales de esquema del core van en 0002_, 0003_..., con ALTER
-- concretos y sobre esta versión ya aplicada. Nunca aquí.

CREATE TABLE IF NOT EXISTS tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(100) UNIQUE NOT NULL,
    active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
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
);

CREATE TABLE IF NOT EXISTS installed_modules (
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
);

CREATE TABLE IF NOT EXISTS user_permissions (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    module VARCHAR(100) NOT NULL,
    model VARCHAR(100) NOT NULL,
    action VARCHAR(20) NOT NULL,
    PRIMARY KEY (tenant_id, user_id, module, model, action)
);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Contacts v2 — tabla nativa con campos contables LATAM.
CREATE TABLE IF NOT EXISTS contacts (
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
);

ALTER TABLE installed_modules ADD COLUMN IF NOT EXISTS checksum TEXT DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_contacts_tenant ON contacts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_installed_modules_tenant ON installed_modules(tenant_id);
CREATE INDEX IF NOT EXISTS idx_installed_modules_active ON installed_modules(tenant_id, active);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires ON refresh_tokens(expires_at);
