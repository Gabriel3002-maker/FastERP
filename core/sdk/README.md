# FastERP SDK

Biblioteca genérica para que módulos WASM interactúen con la BD sin escribir SQL.

## Concepto

En lugar de que cada módulo escriba queries SQL, el SDK lee el `manifest.json` y proporciona métodos genéricos:

```go
// Sin SDK (antes)
query := `INSERT INTO mod_contacts_contact (tenant_id, name, email) VALUES ($1, $2, $3)`
var id string
sdk.DB.QueryRow(ctx, query, tenantID, name, email).Scan(&id)

// Con SDK (ahora)
id, _ := sdk.Create(ctx, "contact", map[string]interface{}{
    "name": name,
    "email": email,
})
```

## API

### Inicializar

```go
import "github.com/fasterp/backend/sdk"

sdk := sdk.NewModuleSDK(
    "contacts",           // módulo ID
    "tenant-uuid",        // tenant ID
    "user-uuid",          // user ID (para auditoría)
    db,                   // *sql.DB
)

// Cargar manifest (en producción desde archivo)
sdk.LoadManifest(manifestJSON)
```

### CRUD Genérico

```go
ctx := context.Background()

// CREATE
id, err := sdk.Create(ctx, "contact", map[string]interface{}{
    "name": "Juan",
    "email": "juan@example.com",
})

// READ
contact, err := sdk.Get(ctx, "contact", id)

// UPDATE
err := sdk.Update(ctx, "contact", id, map[string]interface{}{
    "email": "nuevo@example.com",
})

// DELETE
err := sdk.Delete(ctx, "contact", id)

// LIST
contacts, err := sdk.List(ctx, "contact")
```

## Manifest Format

```json
{
  "name": "contacts",
  "models": {
    "contact": {
      "name": "contact",
      "fields": {
        "name": { "type": "string", "required": true },
        "email": { "type": "string", "required": false },
        "phone": { "type": "string", "required": false },
        "company": { "type": "string", "required": false }
      }
    }
  }
}
```

## Ventajas

- ✅ **Cero SQL** — CRUD completo sin escribir queries
- ✅ **Type-safe** — Validación automática vs manifest
- ✅ **Multi-tenant** — RLS automático por tenant_id
- ✅ **Audit** — created_at, updated_at automáticos
- ✅ **Convenciones** — Tablas siempre nombradas `mod_<moduleid>_<modelname>`

## Flujo

1. **Handler HTTP** recibe petición `/api/contacts`
2. **Llama SDK** → `sdk.Create(ctx, "contact", data)`
3. **SDK lee manifest** → valida campos
4. **SDK ejecuta query** → prefija tabla, agrega tenant_id
5. **PostgreSQL RLS** → enforza aislamiento por tenant
6. **Retorna ID** → JSON al cliente

## Migración

Para convertir un handler viejo a usar SDK:

**Antes:**
```go
query := `SELECT * FROM mod_contacts_contact WHERE tenant_id = $1`
rows, _ := ch.dbConn.Query(ctx, query, tenantID)
```

**Después:**
```go
contacts, _ := ch.sdk.List(ctx, "contact")
```

Ejemplo completo en `example_usage.go`.
