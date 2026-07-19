-- Tabla de contactos
CREATE TABLE IF NOT EXISTS mod_contacts_contact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255),
    phone VARCHAR(20),
    mobile VARCHAR(20),
    company VARCHAR(255),
    job_title VARCHAR(255),
    tax_id_type VARCHAR(50),
    tax_id VARCHAR(50),
    person_type VARCHAR(50),
    tax_regime VARCHAR(50),
    accounting_obligation VARCHAR(255),
    retention_agent VARCHAR(255),
    address TEXT,
    city VARCHAR(255),
    province VARCHAR(255),
    country VARCHAR(255),
    postal_code VARCHAR(20),
    website VARCHAR(255),
    notes TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Index por tenant
CREATE INDEX IF NOT EXISTS idx_contacts_tenant ON mod_contacts_contact(tenant_id);

-- RLS Policy
ALTER TABLE mod_contacts_contact ENABLE ROW LEVEL SECURITY;

CREATE POLICY contacts_tenant_isolation ON mod_contacts_contact
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
