-- RBAC por roles: la asignación de permisos que hasta ahora solo podía hacerse
-- por usuario pasa a poder expresarse también por rol.
--
-- Este fichero es una migración versionada: se aplica una sola vez y no se
-- edita. Los cambios posteriores van en 0003_, 0004_...
--
-- Las tres tablas siguen el patrón del core: tenant_id + RLS, y las PK
-- compuestas evitan que una fila se repita por accidente. Ninguna se borra con
-- un ON DELETE CASCADE desde tenants: PurgeTenant debe cumplir todas las
-- dependencias en orden explícito.

CREATE TABLE IF NOT EXISTS roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    description TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

CREATE TABLE IF NOT EXISTS role_permissions (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    module VARCHAR(100) NOT NULL,
    model VARCHAR(100) NOT NULL,
    action VARCHAR(20) NOT NULL,
    PRIMARY KEY (tenant_id, role_id, module, model, action)
);

CREATE TABLE IF NOT EXISTS user_roles (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, user_id, role_id)
);

-- Los permisos por usuario ganan o restan sobre los del rol: sin esta columna,
-- una fila directa solo podía otorgar y nunca quitar.
ALTER TABLE user_permissions ADD COLUMN IF NOT EXISTS allow BOOLEAN NOT NULL DEFAULT true;

CREATE INDEX IF NOT EXISTS idx_roles_tenant ON roles(tenant_id);
CREATE INDEX IF NOT EXISTS idx_role_permissions_role ON role_permissions(role_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_user ON user_roles(user_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_role ON user_roles(role_id);
CREATE INDEX IF NOT EXISTS idx_user_permissions_user ON user_permissions(user_id);