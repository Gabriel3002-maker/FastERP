package sdk

import (
	"context"
	"database/sql"
	"fmt"
)

// ExampleModuleUsage muestra cómo un módulo usa el SDK
func ExampleModuleUsage(db *sql.DB) {
	// 1. Inicializar SDK
	sdk := NewModuleSDK("contacts", "tenant-uuid-123", "user-uuid-456", db)

	// 2. Cargar manifest (en producción viene del manifest.json)
	manifestJSON := `{
		"name": "contacts",
		"models": {
			"contact": {
				"name": "contact",
				"fields": {
					"name": {"type": "string", "required": true},
					"email": {"type": "string", "required": false},
					"phone": {"type": "string", "required": false},
					"company": {"type": "string", "required": false}
				}
			}
		}
	}`
	sdk.LoadManifest(manifestJSON)

	ctx := context.Background()

	// 3. CREAR (sin escribir SQL)
	contactID, err := sdk.Create(ctx, "contact", map[string]interface{}{
		"name":    "Juan García",
		"email":   "juan@example.com",
		"phone":   "+57 301 234 5678",
		"company": "Acme Corp",
	})
	if err != nil {
		fmt.Printf("Error creando: %v\n", err)
		return
	}
	fmt.Printf("✓ Contacto creado: %s\n", contactID)

	// 4. LEER (sin escribir SQL)
	contact, err := sdk.Get(ctx, "contact", contactID)
	if err != nil {
		fmt.Printf("Error leyendo: %v\n", err)
		return
	}
	fmt.Printf("✓ Contacto: %v\n", contact)

	// 5. ACTUALIZAR (sin escribir SQL)
	err = sdk.Update(ctx, "contact", contactID, map[string]interface{}{
		"email": "juan.nuevo@example.com",
	})
	if err != nil {
		fmt.Printf("Error actualizando: %v\n", err)
		return
	}
	fmt.Println("✓ Contacto actualizado")

	// 6. LISTAR (sin escribir SQL)
	contacts, err := sdk.List(ctx, "contact")
	if err != nil {
		fmt.Printf("Error listando: %v\n", err)
		return
	}
	fmt.Printf("✓ Total contactos: %d\n", len(contacts))

	// 7. ELIMINAR (sin escribir SQL)
	err = sdk.Delete(ctx, "contact", contactID)
	if err != nil {
		fmt.Printf("Error eliminando: %v\n", err)
		return
	}
	fmt.Println("✓ Contacto eliminado")
}

// VENTAJAS del SDK:
// ✅ Cero SQL escribiendo CRUD — todo sale del manifest
// ✅ Type-safe validación de campos
// ✅ RLS automático con tenant_id
// ✅ Tablas prefijadas automáticamente (mod_contacts_contact)
// ✅ Audit trail (created_at, updated_at automáticos)
// ✅ Un módulo nuevo = solo declarar manifest.json + usar SDK
