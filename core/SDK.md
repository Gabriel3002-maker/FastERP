# FastERP Module SDK

SDK para que los módulos WASM interactúen con el core de FastERP.

## 📋 Sobre el SDK

- **Solo intermediario**: Los módulos NO acceden directamente a la API
- **Seguridad multi-tenant**: Aislamiento automático por tenant
- **Tablas prefijadas**: Cada módulo obtiene `mod_moduleid_tablename`
- **Validación**: Validación automática de identificadores (SQL injection safe)

## 🚀 Uso en Módulos

### 1. Crear tabla

```go
sdk := NewModuleSDK(moduleID, tenantID, userID, db)

// Crear tabla: mod_contacts_contact
err := sdk.CreateTable(ctx, "contact", `
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255),
    phone VARCHAR(20)
`)
```

**Resultado en BD**: tabla `mod_contacts_contact`

### 2. Insertar datos

```go
id, err := sdk.Insert(ctx, "contact", map[string]interface{}{
    "name":  "Juan Pérez",
    "email": "juan@example.com",
    "phone": "+593991234567",
})
// Automáticamente asigna tenant_id = tenantID del SDK
```

### 3. Consultar datos

```go
rows, err := sdk.Query(ctx, "contact", "name = $1", "Juan Pérez")
defer rows.Close()

for rows.Next() {
    // Procesar...
}
```

**Nota**: `WHERE tenant_id = $1` se agrega automáticamente

### 4. Obtener contexto del módulo

```go
ctx := sdk.GetContext()
// {
//   "module_id": "contacts",
//   "tenant_id": "uuid-123",
//   "user_id": "uuid-456"
// }
```

## 🔒 Seguridad

### Aislamiento multi-tenant

Cada tabla del módulo es prefijada:
```
mod_contacts_contact       (módulo contacts)
mod_tienda_web_product     (módulo tienda_web)
mod_odoo_sync_imported     (módulo odoo_sync)
```

Todas las queries automáticamente filtran por `tenant_id`:
```go
// Esto...
sdk.Query(ctx, "contact", "email = $1", email)

// Se convierte en...
// SELECT * FROM mod_contacts_contact 
// WHERE tenant_id = $1 AND email = $2
```

### Validación de identificadores

```go
// ✅ Permitido
CreateTable(ctx, "contact", ...)
Query(ctx, "users_log", ...)

// ❌ Rechazado (SQL injection)
CreateTable(ctx, "contact; DROP TABLE users", ...)
Query(ctx, "contact; DELETE FROM users", ...)
```

## 📚 API Completo

```go
type ModuleSDK struct {
    ModuleID string  // "contacts", "tienda_web", etc.
    TenantID string  // UUID del tenant
    UserID   string  // UUID del usuario actual
    DB       *sql.DB // Conexión compartida a BD
}

// Crear SDK
NewModuleSDK(moduleID, tenantID, userID, db) *ModuleSDK

// DDL
CreateTable(ctx, tableName, schema) error

// CRUD
Insert(ctx, tableName, data) (id string, err)
Query(ctx, tableName, whereClause, args...) (*sql.Rows, error)

// Contexto
GetContext() map[string]string
```

## 🎯 Ejemplo Completo (Módulo Contacts)

```go
package contacts

import (
    "context"
    "log"

    "github.com/fasterp/backend/sdk"
)

func InitModule(moduleID, tenantID, userID string, db *sql.DB) error {
    sdk := sdk.NewModuleSDK(moduleID, tenantID, userID, db)

    // Crear tabla de contactos
    err := sdk.CreateTable(context.Background(), "contact", `
        name VARCHAR(255) NOT NULL,
        email VARCHAR(255),
        phone VARCHAR(20),
        company VARCHAR(255),
        tax_id VARCHAR(50)
    `)
    if err != nil {
        return err
    }

    log.Println("[Contacts] Module initialized")
    return nil
}

func CreateContact(sdk *ModuleSDK, name, email string) error {
    id, err := sdk.Insert(context.Background(), "contact", map[string]interface{}{
        "name":  name,
        "email": email,
    })
    if err != nil {
        return err
    }

    log.Printf("[Contacts] Created contact: %s", id)
    return nil
}
```

## ✨ Ventajas

✅ **Sin SQL injection**: Validación automática
✅ **Multi-tenant seguro**: Aislamiento garantizado
✅ **Fácil de usar**: API simple
✅ **Extensible**: Los módulos solo usan el SDK
✅ **Auditable**: Todas las queries pasan por el SDK

---

**SDK versión:** 1.0  
**Mínimo Go:** 1.20
