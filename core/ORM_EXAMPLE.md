# FastERP Module ORM - Ejemplo

Cómo usar el ORM simple en un módulo WASM.

## 📋 Definir Modelo

```go
package contacts

import "github.com/fasterp/backend/sdk"

// Contact es el modelo para la tabla contacts
type Contact struct {
	sdk.BaseModel          // Hereda ID, TenantID, CreatedAt, UpdatedAt
	Name       string      `db:"name"`
	Email      string      `db:"email"`
	Phone      string      `db:"phone"`
	Company    string      `db:"company"`
	TaxID      string      `db:"tax_id"`
}

// TableName retorna el nombre de la tabla
func (c *Contact) TableName() string {
	return "contact"
}
```

## 🚀 Usar el ORM

### 1. Crear tabla

```go
func InitModule(moduleID, tenantID, userID string, db *sql.DB) error {
	sdk := sdk.NewModuleSDK(moduleID, tenantID, userID, db)
	orm := sdk.ORM()

	contact := &Contact{}
	
	// Crear tabla: mod_contacts_contact
	// (automáticamente con campos de BaseModel)
	schema := `
		name VARCHAR(255) NOT NULL,
		email VARCHAR(255),
		phone VARCHAR(20),
		company VARCHAR(255),
		tax_id VARCHAR(50)
	`
	
	return sdk.CreateTable(context.Background(), contact.TableName(), schema)
}
```

### 2. Crear registro

```go
func CreateContact(orm *ORM, name, email string) error {
	contact := &Contact{
		Name:  name,
		Email: email,
	}
	
	// Inserta automáticamente
	// - ID generado
	// - TenantID inyectado
	// - CreatedAt = ahora
	// - UpdatedAt = ahora
	return orm.Create(context.Background(), contact)
}
```

### 3. Buscar por ID

```go
func GetContact(orm *ORM, id string) (*Contact, error) {
	contact := &Contact{}
	err := orm.FindByID(context.Background(), contact, id)
	return contact, err
}
```

### 4. Listar contactos

```go
func ListContacts(orm *ORM) (interface{}, error) {
	contact := &Contact{}
	return orm.All(context.Background(), contact, 100) // Máximo 100
}
```

### 5. Buscar con condición

```go
func SearchByEmail(orm *ORM, email string) (interface{}, error) {
	contact := &Contact{}
	return orm.Where(context.Background(), contact, "email = $1", email)
}
```

### 6. Actualizar

```go
func UpdateContact(orm *ORM, id, name, email string) error {
	contact := &Contact{}
	err := orm.FindByID(context.Background(), contact, id)
	if err != nil {
		return err
	}
	
	contact.Name = name
	contact.Email = email
	
	// Automáticamente:
	// - UpdatedAt = ahora
	// - TenantID no cambia
	// - ID no cambia
	return orm.Update(context.Background(), contact)
}
```

### 7. Borrar

```go
func DeleteContact(orm *ORM, id string) error {
	contact := &Contact{}
	contact.SetID(id)
	return orm.Delete(context.Background(), contact)
}
```

## 🎯 API Completo

```go
type ORM struct {
	sdk *ModuleSDK
}

// CRUD
Create(ctx context.Context, model Model) error
FindByID(ctx context.Context, model Model, id string) error
Update(ctx context.Context, model Model) error
Delete(ctx context.Context, model Model) error

// Query
All(ctx context.Context, model Model, limit int) (interface{}, error)
Where(ctx context.Context, model Model, where string, args ...interface{}) (interface{}, error)
```

## ✨ Características

✅ **Automático**: ID, TenantID, timestamps
✅ **Seguro**: SQL injection safe
✅ **Multi-tenant**: Aislamiento automático
✅ **Simple**: 4 líneas para CRUD completo
✅ **Type-safe**: Usa structs, no strings

## 🔒 Seguridad

El ORM automáticamente:
- Inyecta `tenant_id` en todas las queries
- Valida identificadores (previene SQL injection)
- Filtra por tenant en todas las operaciones

```go
// Esto...
orm.Where(ctx, contact, "email = $1", "test@example.com")

// Se convierte internamente en...
// SELECT * FROM mod_contacts_contact
// WHERE tenant_id = $1 AND email = $2
// Params: [tenantID, "test@example.com"]
```

## 📝 Ejemplo Completo

```go
package main

import (
	"context"
	"database/sql"
	"log"

	"github.com/fasterp/backend/sdk"
)

type Contact struct {
	sdk.BaseModel
	Name  string `db:"name"`
	Email string `db:"email"`
}

func (c *Contact) TableName() string {
	return "contact"
}

func main() {
	// Crear SDK
	moduleSDK := sdk.NewModuleSDK("contacts", tenantID, userID, db)
	orm := moduleSDK.ORM()

	// Crear contacto
	contact := &Contact{
		Name:  "Juan Pérez",
		Email: "juan@example.com",
	}
	
	if err := orm.Create(context.Background(), contact); err != nil {
		log.Fatal(err)
	}
	
	log.Printf("Contacto creado: %s", contact.ID)

	// Buscar por email
	results, _ := orm.Where(context.Background(), contact, "email = $1", "juan@example.com")
	log.Printf("Encontrados: %v", results)

	// Actualizar
	contact.Name = "Juan Carlos"
	if err := orm.Update(context.Background(), contact); err != nil {
		log.Fatal(err)
	}
	
	log.Println("✓ Actualizado")
}
```

---

**El ORM es el puente entre tu módulo y el SDK, proporcionando una interfaz limpia y segura.**


---

## 🔐 ORM Avanzado - Seguridad Mejorada

### Características de Seguridad

✅ **Validación automática** - requerido, email, longitud
✅ **Encripción AES-256** - para campos sensibles
✅ **Auditoría de cambios** - log de CREATE/UPDATE/DELETE
✅ **Soft delete** - registros marcados como eliminados
✅ **Aislamiento multi-tenant** - garantizado
✅ **SQL injection safe** - validación de identificadores
✅ **Timestamps automáticos** - created_at, updated_at

### Usar ORM Avanzado

```go
// Configurar ORM con seguridad
config := &sdk.ORMConfig{
	EnableAudit:      true,        // Auditoría
	EnableEncryption: true,        // Encripción AES-256
	EncryptionKey:    "base64_32_bytes_key",
	SoftDelete:       true,        // Marcar como eliminado en lugar de borrar
}

orm, err := sdk.AdvancedORM(config)
if err != nil {
	log.Fatal(err)
}
```

### Modelo con Validación

```go
type Contact struct {
	sdk.BaseModel
	Name     string `db:"name,required,max:255"`
	Email    string `db:"email,email,encrypt"`      // Encriptado
	Phone    string `db:"phone,encrypt"`             // Encriptado
	TaxID    string `db:"tax_id,encrypt"`            // Encriptado
	Company  string `db:"company,max:255"`
	Notes    string `db:"notes,max:1000"`
}

func (c *Contact) TableName() string {
	return "contact"
}
```

### CRUD con Seguridad

```go
// Crear (con validación)
contact := &Contact{
	Name:  "Juan Pérez",
	Email: "juan@example.com",  // ← Será encriptado
	Phone: "+593991234567",      // ← Será encriptado
	TaxID: "1234567890",         // ← Será encriptado
}

if err := orm.Create(ctx, contact); err != nil {
	// Validación falló (email inválido, campo requerido, etc)
	log.Fatal(err)
}
// ✓ Creado + Auditado + Encriptado

// Buscar (desencriptación automática)
orm.FindByID(ctx, contact, id)
// ✓ Email, phone, tax_id están desencriptados automáticamente

// Actualizar (con auditoría)
contact.Phone = "+593987654321"
orm.Update(ctx, contact)
// ✓ Actualizado + Auditado + Encriptado

// Borrar (soft delete)
orm.Delete(ctx, contact)
// ✓ Marcado como eliminado + Auditado
// (SELECT no mostrará registros con deleted_at != NULL)
```

### Auditoría

Todos los cambios se registran automáticamente en `mod_moduleid_audit_log`:

| tenant_id | user_id | action | table_name | record_id | created_at |
|-----------|---------|--------|------------|-----------|------------|
| uuid-123  | uuid-456| CREATE | contact    | uuid-789  | 2026-07-18|
| uuid-123  | uuid-456| UPDATE | contact    | uuid-789  | 2026-07-18|
| uuid-123  | uuid-456| DELETE | contact    | uuid-789  | 2026-07-18|

### Encripción

Campos marcados con `encrypt` son automáticamente:
- **Encriptados** al guardar en BD
- **Desencriptados** al leer desde BD
- **Seguro**: Usa AES-256-CFB

```go
// En BD (encriptado)
email: "e2Nf8vX9pQ7aL2mN0..."

// En memoria (desencriptado)
contact.Email == "juan@example.com"
```

### Validación Integrada

```go
type Contact struct {
	Name  string `db:"name,required"`          // Requerido
	Email string `db:"email,email"`            // Validar email
	Phone string `db:"phone,max:20"`           // Máximo 20 chars
	Age   int    `db:"age,min:18,max:120"`    // Rango
}

// Si validación falla:
err := orm.Create(ctx, contact)
// error: "field Email is not a valid email"
// error: "field Name is required"
```

### Comparativa

**ORM Simple:**
- ✅ Fácil de usar
- ❌ Sin encripción
- ❌ Sin auditoría
- ❌ Sin validación

**ORM Avanzado:**
- ✅ Fácil de usar
- ✅ Encripción AES-256
- ✅ Auditoría automática
- ✅ Validación automática
- ✅ Soft delete
- ✅ Multi-tenant garantizado

---

**Recomendación**: Usa AdvancedORM para módulos con datos sensibles (contacts, usuarios, pagos). Usa ORM simple para datos públicos.

## 🔍 Acceder a Auditoría

### Desde Módulo Go

```go
import "github.com/fasterp/backend/db"

// Crear logger de auditoría
auditLogger := db.NewAuditLogger(dbConnection)

// Ver historial de un registro
trail, err := auditLogger.GetAuditTrail(ctx, tenantID, recordID, 50)
for _, event := range trail {
    fmt.Printf("%v: %s by %s\n", 
        event.CreatedAt, 
        event.Action, 
        event.UserID)
}

// Ver actividad de un usuario
userActivity, err := auditLogger.GetUserAudit(ctx, tenantID, userID, 100)

// Estadísticas
stats, err := auditLogger.GetAuditStats(ctx, tenantID, 30)
fmt.Printf("Events last 30 days: %v\n", stats["total_events"])

// Exportar
data, err := auditLogger.ExportAudit(ctx, tenantID, startDate, endDate)
os.WriteFile("audit.json", data, 0644)
```

### Desde HTTP (API)

```bash
# Ver historial de un contacto (ID: contact-123)
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/entity?entity_id=contact-123&limit=50"

# Ver actividad de un usuario
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/user?user_id=user-uuid&limit=100"

# Estadísticas
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/stats?days=30"

# Exportar JSON
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/export?start=2026-07-01&end=2026-07-31" \
  > audit_export.json

# Ver dashboard visual
curl "http://localhost:7071/admin/audit"
```

