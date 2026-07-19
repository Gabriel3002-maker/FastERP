# FastERP Setup Wizard

Sistema de configuración inicial tipo Odoo para crear la base de datos y tenants desde una interfaz web.

## 🎯 Características

- ✅ **Setup automático** — No necesitas psql ni comandos
- ✅ **Interfaz web** — Setup wizard elegante (tipo Odoo)
- ✅ **Multi-tenant** — Crear organizaciones desde la UI
- ✅ **Admin auto-generado** — Crear usuario administrador automáticamente
- ✅ **Validación** — Verificar datos antes de crear
- ✅ **Protección** — Solo funciona si la BD está vacía

## 🚀 Uso

### Primer Inicio (Setup Wizard)

```bash
# 1. Crear BD vacía
psql -U postgres -c "CREATE DATABASE fasterp;"

# 2. Ejecutar FastERP
cd /home/sucode/Documents/ecuabyte/fasterp/core
make run

# Output:
# [FastERP] Starting Core (port=0.0.0.0:7071)
# [FastERP] Listening on 0.0.0.0:7071
# [FastERP] Open http://localhost:7071
```

### 3. Abrir en Navegador

```
http://localhost:7071
```

Se verá el **Setup Wizard**:

```
┌─────────────────────────────────┐
│      ⚡ FastERP                 │
│   Multi-tenant ERP              │
│                                 │
│     Initial Setup               │
│  Configure your FastERP system  │
│                                 │
│  Organization Name:             │
│  [Acme Corp          ]          │
│                                 │
│  Subdomain/Slug:                │
│  [acme-corp          ]          │
│  (auto-generated from name)     │
│                                 │
│  Admin Username:                │
│  [admin              ]          │
│                                 │
│  Admin Email:                   │
│  [admin@acme.com     ]          │
│                                 │
│  Password:                      │
│  [••••••••           ]          │
│                                 │
│  Confirm:                       │
│  [••••••••           ]          │
│                                 │
│  [Create Setup]                 │
│                                 │
└─────────────────────────────────┘
```

### 4. Llenar Formulario

| Campo | Descripción | Ejemplo |
|-------|-------------|---------|
| **Organization Name** | Nombre de tu empresa | Acme Corp |
| **Subdomain/Slug** | URL-friendly (auto) | acme-corp |
| **Admin Username** | Usuario admin | admin |
| **Admin Email** | Email del admin | admin@acme.com |
| **Password** | Contraseña (8+ chars) | SecurePass123 |
| **Confirm** | Repetir contraseña | SecurePass123 |

### 5. Crear Setup

Click en **Create Setup**

Verás:
```
Creating your system...
[spinner]
```

Luego:
```
✓ Setup Complete!
  Setup complete! Tenant: Acme Corp, Admin: admin
  
  [Go to Login]
```

### 6. Login

Automáticamente redirige a `/login`:

```
Login
═════════════════════════════════

Username: admin
Password: ••••••••

[Sign In]
```

Entra con las credenciales que creaste.

## 📊 API Endpoints

### Verificar Setup Status

```bash
curl http://localhost:7071/api/setup/check
```

Respuesta si no hay setup:
```json
{
  "setup_required": true,
  "message": "System needs configuration"
}
```

Respuesta si ya está configurado:
```json
{
  "setup_required": false,
  "message": "System already configured"
}
```

### Crear Setup Inicial (POST)

```bash
curl -X POST http://localhost:7071/api/setup/initial \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_name": "Acme Corp",
    "tenant_slug": "acme-corp",
    "admin_user": "admin",
    "admin_email": "admin@acme.com",
    "password": "SecurePass123",
    "confirm": "SecurePass123"
  }'
```

Respuesta:
```json
{
  "success": true,
  "message": "Setup complete! Tenant: Acme Corp, Admin: admin"
}
```

### Crear Nuevo Tenant (POST)

Solo para admins (requiere JWT token):

```bash
TOKEN="tu_jwt_token_aqui"

curl -X POST http://localhost:7071/api/setup/tenant \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Tenant-ID: tenant-uuid" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_name": "Beta Corp",
    "tenant_slug": "beta-corp"
  }'
```

Respuesta:
```json
{
  "success": true,
  "message": "Tenant Beta Corp created successfully (ID: uuid-123)"
}
```

## 🔐 Validaciones

El wizard valida:

| Campo | Validación | Error |
|-------|-----------|-------|
| **Tenant Name** | Requerido | "Tenant name required" |
| **Tenant Slug** | Único, sin espacios | "Tenant slug already exists" |
| **Admin User** | Requerido | "Admin user required" |
| **Admin Email** | Email válido | "Invalid email" |
| **Password** | 8+ chars | "Password must be at least 8 characters" |
| **Confirm** | Match con Password | "Passwords do not match" |

## 🛡️ Seguridad

- ✅ **Solo funciona si BD vacía** — Imposible reescribir una config existente
- ✅ **Validación de datos** — Todos los campos se validan
- ✅ **Slug único** — No permite duplicados
- ✅ **Password hashing** — Bcrypt(12) automático
- ✅ **No se guarda en plaintext** — Todo encriptado en BD

## 🔧 Troubleshooting

### Error: "System already configured"

Ya hay tenants en la BD. Para reiniciar:

```bash
# ⚠️ PELIGRO: Borra toda la BD
psql -U postgres -c "DROP DATABASE fasterp;"
psql -U postgres -c "CREATE DATABASE fasterp;"

# Luego:
make run
```

### Error: "Tenant slug already exists"

El slug ya está usado. Usa otro nombre:

```bash
# En lugar de "acme-corp", prueba:
# - acme-corp-2
# - acme-corp-west
# - acmecorp
```

### Error: "Password must be at least 8 characters"

La contraseña es muy corta. Usa mínimo 8 caracteres:

```
SecurePass123  ✓
MyPass123      ✓
Abc123         ✗ (solo 6)
```

### Error: "connection refused"

PostgreSQL no está corriendo:

```bash
# Iniciar PostgreSQL
# macOS:
brew services start postgresql

# Linux:
sudo systemctl start postgresql

# Windows:
# Busca "PostgreSQL" en Services
```

## 📝 Base de Datos Creada

Después de setup, la BD tendrá:

```sql
-- Tenants (organizaciones)
id: uuid-123
name: "Acme Corp"
slug: "acme-corp"
active: true
created_at: 2026-07-18 10:30:15

-- Users (admins)
id: uuid-456
tenant_id: uuid-123
username: "admin"
email: "admin@acme.com"
password_hash: "$2a$12$..." (bcrypt)
is_admin: true
active: true

-- Tablas automáticas
installed_modules (vacía)
module_manifests (vacía)
audit_log (vacía)
```

## 🎯 Flujo Completo

```
┌─────────────────────────────────────┐
│  1. Crear BD vacía (psql)           │
│     CREATE DATABASE fasterp;        │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  2. Ejecutar FastERP                │
│     make run                        │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  3. Abrir en navegador              │
│     http://localhost:7071           │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  4. Setup Wizard aparece            │
│     (detecta BD vacía)              │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  5. Llenar formulario               │
│     - Org name                      │
│     - Admin credentials             │
│     - Password                      │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  6. Click "Create Setup"            │
│     POST /api/setup/initial         │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  7. Validación en servidor          │
│     - Datos válidos                 │
│     - Tenant slug único             │
│     - Password 8+ chars             │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  8. Crear en BD                     │
│     - INSERT tenants                │
│     - INSERT users (admin)          │
│     - Hash password (bcrypt)        │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  9. Redirigir a login               │
│     /login                          │
│     (Usuario: admin, Pass: xxx)     │
└─────────────────────────────────────┘
                ↓
┌─────────────────────────────────────┐
│  10. Login & Dashboard              │
│      Listo para usar                │
└─────────────────────────────────────┘
```

## 🔗 Crear Múltiples Tenants

Después del setup inicial, los admins pueden crear más tenants:

1. Login como admin
2. Ir a `/admin/tenants` (futuro: panel de administración)
3. Click "Crear Tenant"
4. Llenar nombre y slug
5. Click "Crear"

O por API:

```bash
TOKEN=$(curl -X POST http://localhost:7071/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"xxx","tenant_id":"uuid-123"}' \
  | jq -r '.access_token')

curl -X POST http://localhost:7071/api/setup/tenant \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Tenant-ID: uuid-123" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_name": "Beta Corp",
    "tenant_slug": "beta-corp"
  }'
```

---

**Setup Wizard: Configuración en 5 minutos. Sin SQL. Sin terminal.**

Made with ❤️ for ease of use.
