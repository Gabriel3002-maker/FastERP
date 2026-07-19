# FastERP Core

ERP moderno y potente construido con **Go + HTMX + PostgreSQL**.

## ⚡ Quick Start (Dos Opciones)

### Opción A: Setup Wizard (RECOMENDADO - Sin DB manual)

```bash
# 1. Solo crear la BD vacía
psql -U postgres -c "CREATE DATABASE fasterp;"

# 2. Ejecutar
cd core
make run

# 3. Abrir en navegador
# http://localhost:7071
# 
# Se abrirá el Setup Wizard
# Llenar:
# - Nombre de la organización
# - Subdomain/Slug
# - Admin username
# - Admin email
# - Contraseña
```

### Opción B: Manual (Legacy)

Si prefieres configurar todo por código:

```bash
# 1. Crear base de datos
psql -U postgres -c "CREATE DATABASE fasterp;"

# 2. Configurar `fast.conf`
cat > fast.conf << EOF
[database]
host = localhost
port = 5432
user = postgres
password = postgres
name = fasterp

[server]
port = 7071
host = 0.0.0.0
EOF

# 3. Variables de env (opcional, sobrescriben fast.conf)
export FASTERP_DB_HOST=myhost
export FASTERP_PORT=8000

# 4. Ejecutar
cd core
make run

# 5. Login
# Usuario: admin
# Contraseña: admin123
```

## 📁 Estructura

```
core/
├── fast.conf           ← Configuración (estilo Odoo)
├── main.go             ← Entry point
├── config/             ← Config loader
├── db/                 ← Database layer
├── handlers/           ← HTTP handlers
├── middleware/         ← Logging, auth
├── utils/              ← Error handling
├── templates/          ← HTMX HTML
├── static/             ← CSS, JS
└── Makefile
```

## 🔧 Configuración (fast.conf)

Secciones disponibles:

### `[server]`
```ini
[server]
port = 7071          # Puerto
host = 0.0.0.0       # Host
```

### `[database]`
```ini
[database]
host = localhost     # Host PostgreSQL
port = 5432          # Puerto
user = postgres      # Usuario
password = postgres  # Contraseña
name = fasterp       # Base de datos
```

### `[modules]`
```ini
[modules]
path = ../modules    # Directorio de módulos WASM
```

### `[logging]`
```ini
[logging]
level = info         # debug, info, warning, error
```

### `[security]`
```ini
[security]
jwt_secret = xxxxx
jwt_refresh_secret = xxxxx
```

## 🗄️ Base de Datos

Tablas creadas automáticamente:

- `tenants` - Multi-tenancia
- `users` - Usuarios
- `installed_modules` - Módulos activos
- `module_manifests` - Metadatos de módulos

## 🧪 Tests

```bash
go test -v ./...
```

## 📊 Auditoría Empresarial

FastERP incluye **auditoría de primer nivel** para cumplimiento normativo y trazabilidad:

### Eventos Auditados
- `CREATE` - Creación de registros
- `READ` - Lectura de datos (opcional)
- `UPDATE` - Modificación de registros
- `DELETE` - Eliminación de registros (soft delete)
- `LOGIN` - Acceso de usuarios
- `ERROR` - Errores del sistema

### Tabla `audit_log`
```sql
id              VARCHAR(255) PRIMARY KEY
tenant_id       UUID           -- Aislamiento multi-tenant
user_id         UUID           -- Quién hizo la acción
action          VARCHAR(50)    -- CREATE, UPDATE, DELETE, etc
entity          VARCHAR(255)   -- Tabla/entidad afectada
entity_id       VARCHAR(255)   -- ID del registro modificado
before          JSONB          -- Estado anterior
after           JSONB          -- Estado nuevo
status          VARCHAR(50)    -- success, error
error           TEXT           -- Mensaje de error (si aplica)
ip_address      VARCHAR(45)    -- IP del usuario
user_agent      TEXT           -- Navegador/cliente
created_at      TIMESTAMP      -- Cuándo ocurrió
```

### API Endpoints de Auditoría

#### Obtener historial de una entidad
```bash
GET /api/audit/entity?entity_id=12345&limit=50
```

#### Obtener historial de actividad de un usuario
```bash
GET /api/audit/user?user_id=user-uuid&limit=100
```

#### Obtener estadísticas
```bash
GET /api/audit/stats?days=30

Respuesta:
{
  "period": 30,
  "stats": {
    "total_events": 1250,
    "unique_users": 15,
    "unique_entities": 8,
    "error_count": 3,
    "delete_count": 5,
    "update_count": 450,
    "create_count": 700
  }
}
```

#### Exportar auditoría
```bash
GET /api/audit/export?start=2026-07-01&end=2026-07-31

Descarga: audit_export.json (con todos los eventos en el período)
```

### Integración en Módulos

El SDK automáticamente registra operaciones de módulos en auditoría:

```go
// Usando ORM Advanced (con auditoría habilitada)
orm := sdk.NewAdvancedORM(&db, &ORMConfig{
    EnableAudit: true,
    EnableEncryption: false,
})

// Todas las operaciones se auditan automáticamente
record := orm.Create(tenant_id, user_id, "contacts", data)
// → audit_log: INSERT con user_id, timestamp, before/after JSON
```

### Compliance Ready

✅ **GDPR**: IP tracking + timestamp  
✅ **SOX**: Audit trail inmutable (PostgreSQL append-only lógico)  
✅ **ISO27001**: Encrypted connections + tenant isolation  
✅ **PCI**: Soft delete (no hard delete de datos sensibles)  

## 📝 Comandos Makefile

```bash
make run              # Compilar y ejecutar
make build            # Compilar binario
make lint             # go vet
make clean            # Limpiar
make docker-build     # Compilar Docker
make docker-run       # Ejecutar en Docker
```

## 🚀 Modo Producción

```ini
[security]
jwt_secret = GENERARUNSECRETO
jwt_refresh_secret = GENERAROTROSECRET

[environment]
env = production
```

```bash
FASTERP_ENV=production make run
```

## 🔗 API Endpoints

- `GET /` - Login page
- `POST /api/auth/login` - Authenticate
- `POST /api/auth/logout` - Logout
- `GET /admin` - Dashboard
- `GET /health` - Health check

## 📦 Próximamente

- [ ] JWT sessions
- [ ] Carga de módulos WASM
- [ ] API CRUD dinámico
- [ ] Templates por módulo
- [ ] Rate limiting
- [ ] CORS

## 🎯 Filosofía

- **Simple:** ~300 líneas de core
- **Rápido:** Single binary, no runtime
- **Potente:** Extensible con módulos WASM
- **Moderno:** HTMX, no React complexity

---

**Made with Go + HTMX + ❤️**
