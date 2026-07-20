package sdk

import (
	"context"
	"database/sql"
	"fmt"
)

// ExampleModuleUsage muestra el ciclo completo de un módulo usando @fast.
//
// Todo lo que hace un módulo es: declarar su manifest y llamar estos métodos.
// Nunca escribe SQL, nunca crea tablas a mano, nunca toca el core.
func ExampleModuleUsage(db *sql.DB) {
	// 1. Inicializar el SDK del módulo
	s := NewModuleSDK("contacts", "tenant-uuid-123", "user-uuid-456", db)

	// 2. Cargar el manifest (en producción se lee de manifest.json)
	manifestJSON := `{
		"name": "contacts",
		"models": {
			"contact": {
				"name": "contact",
				"fields": {
					"name":    {"type": "string", "required": true, "index": true},
					"email":   {"type": "email"},
					"phone":   {"type": "phone"},
					"company": {"type": "string"}
				}
			}
		}
	}`
	if err := s.LoadManifest(manifestJSON); err != nil {
		fmt.Printf("manifest inválido: %v\n", err)
		return
	}

	ctx := context.Background()

	// 3. Materializar las tablas del manifest (idempotente)
	if err := s.EnsureSchema(ctx); err != nil {
		fmt.Printf("error preparando esquema: %v\n", err)
		return
	}

	// 4. CREAR → @fast.create()
	contactID, err := s.Create(ctx, "contact", map[string]any{
		"name":    "Juan García",
		"email":   "juan@example.com",
		"phone":   "+57 301 234 5678",
		"company": "Acme Corp",
	})
	if err != nil {
		fmt.Printf("error creando: %v\n", err)
		return
	}
	fmt.Printf("✓ Contacto creado: %s\n", contactID)

	// 5. LEER → @fast.read()
	contact, err := s.Get(ctx, "contact", contactID)
	if err != nil {
		fmt.Printf("error leyendo: %v\n", err)
		return
	}
	fmt.Printf("✓ Contacto: %v\n", contact)

	// 6. ACTUALIZAR → @fast.update()
	if err := s.Update(ctx, "contact", contactID, map[string]any{
		"email": "juan.nuevo@example.com",
	}); err != nil {
		fmt.Printf("error actualizando: %v\n", err)
		return
	}
	fmt.Println("✓ Contacto actualizado")

	// 7. LISTAR PAGINADO → @fast.list()
	// El límite se ajusta solo a 10, 20, 50 o 100.
	page, err := s.List(ctx, "contact", ListOptions{
		Page:     1,
		Limit:    20,
		OrderBy:  "name",
		OrderDir: "asc",
	})
	if err != nil {
		fmt.Printf("error listando: %v\n", err)
		return
	}
	fmt.Printf("✓ Página %d/%d — %d de %d contactos\n",
		page.Page, page.TotalPages, len(page.Data), page.Total)

	// 8. BUSCAR → @fast.search()
	results, err := s.Search(ctx, "contact", "acme", ListOptions{Limit: 50})
	if err != nil {
		fmt.Printf("error buscando: %v\n", err)
		return
	}
	fmt.Printf("✓ Coincidencias: %d\n", results.Total)

	// 9. ELIMINAR → @fast.delete()
	if err := s.Delete(ctx, "contact", contactID); err != nil {
		fmt.Printf("error eliminando: %v\n", err)
		return
	}
	fmt.Println("✓ Contacto eliminado")
}

// Lo que el módulo obtiene gratis:
//   ✅ Cero SQL — el CRUD sale del manifest
//   ✅ Tablas creadas y migradas solas desde manifest.json
//   ✅ Paginación 10/20/50/100 con total y total_pages
//   ✅ Búsqueda y filtros validados contra el manifest
//   ✅ Aislamiento por tenant (WHERE tenant_id + RLS)
//   ✅ Identificadores saneados: nada del manifest entra crudo al SQL
//   ✅ created_at / updated_at automáticos
