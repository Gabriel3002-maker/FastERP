# @fast API — Zero-SQL CRUD for FastERP

The **@fast** namespace provides automatic CRUD for all modules.

## Concept

Modules define schema in `manifest.json`. Core automatically provides CRUD via HTTP — **no SQL writing needed.**

```
Module defines:  manifest.json
                 ↓
Core reads:      schema + field types
                 ↓
@fast provides:  Create, Read, Update, Delete, List (automatic)
                 ↓
Frontend calls:  /api/{module}/{model} (REST endpoints)
                 ↓
SDK executes:    Type-safe queries with RLS + audit
```

## Usage: From Any Module

### REST API (Frontend calls)

```javascript
// @fast.create() — INSERT
POST /api/contacts/contact
{"name": "Juan", "email": "juan@example.com"}
→ {id: "uuid"}

// @fast.read() — SELECT by ID
GET /api/contacts/contact/{id}
→ {id: "uuid", name: "Juan", ...}

// @fast.update() — UPDATE
PUT /api/contacts/contact/{id}
{"name": "Juan Updated"}
→ {message: "Updated"}

// @fast.delete() — DELETE
DELETE /api/contacts/contact/{id}
→ {message: "Deleted"}

// @fast.list() — SELECT all
GET /api/contacts/contact
→ {data: [{id: "...", name: "...", ...}]}
```

### Example: Complete Module Flow

**1. Define schema** (`modules/mymodule/manifest.json`):
```json
{
  "name": "mymodule",
  "models": {
    "item": {
      "fields": {
        "title": {"type": "string", "required": true},
        "price": {"type": "decimal"}
      }
    }
  }
}
```

**2. Frontend uses @fast** (`modules/mymodule/frontend/app.js`):
```javascript
const tenantID = getTenantID(); // from JWT

// @fast.create()
const {id} = await fetch('/api/mymodule/item', {
  method: 'POST',
  headers: {'X-Tenant-ID': tenantID},
  body: JSON.stringify({title: 'Product', price: 99.99})
}).then(r => r.json());

// @fast.list()
const items = await fetch('/api/mymodule/item',
  {headers: {'X-Tenant-ID': tenantID}}
).then(r => r.json());

// @fast.update()
await fetch(`/api/mymodule/item/${id}`, {
  method: 'PUT',
  headers: {'X-Tenant-ID': tenantID},
  body: JSON.stringify({price: 109.99})
});

// @fast.delete()
await fetch(`/api/mymodule/item/${id}`, {
  method: 'DELETE',
  headers: {'X-Tenant-ID': tenantID}
});
```

**3. Core does the rest** (automatic):
- ✅ Reads `manifest.json`
- ✅ Validates fields
- ✅ Executes via SDK
- ✅ Enforces RLS (tenant isolation)
- ✅ Logs audit trail

## Manifest Format

```json
{
  "name": "mymodule",
  "models": {
    "model_name": {
      "fields": {
        "field_name": {"type": "string|text|decimal|...", "required": true|false}
      }
    }
  }
}
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
