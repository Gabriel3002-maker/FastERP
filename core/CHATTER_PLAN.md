# Plan: Módulo Chatter para FastERP

## Objetivo

Crear un módulo de mensajería interna estilo Odoo `mail.thread` que permita:
- Registrar mensajes, notas y actividad sobre cualquier registro del sistema
- Embeber un widget de timeline en la página de cualquier módulo
- Funcionar como página standalone en `/admin/chatter`

## Alcance (v1)

- 3 tipos de mensaje: **comentario**, **nota interna**, **sistema**
- Hilos con respuestas anidadas (parent_id)
- Widget JS embebible para otros módulos
- Página standalone con timeline global
- Sin dependencias de WebSocket/SSE (polling manual)

---

## 1. Estructura de Directorios

```
modules/chatter/
├── manifest.json              # Schema del módulo
├── main.go                    # WASM manifest export
├── module.wasm                # Binario compilado (opcional)
└── frontend/
    ├── index.html             # Página standalone (/admin/chatter)
    ├── styles.css             # Estilos del módulo
    ├── app.js                 # Lógica de la página standalone
    └── widget.js              # Widget embebible para otros módulos
```

---

## 2. Modelo de Datos (`manifest.json`)

Un único modelo `message` con campos para soportar threads y vinculación a registros:

```json
{
  "name": "chatter",
  "label": "Chatter",
  "version": "1.0.0",
  "author": "EcuaByte",
  "description": "Mensajería interna: comentarios, notas y actividad vinculada a registros",
  "icon": "💬",
  "frontend": {
    "pages": [
      {
        "name": "ChatterPage",
        "path": "/admin/chatter",
        "label": "Chatter",
        "icon": "💬"
      }
    ]
  },
  "models": {
    "message": {
      "name": "message",
      "label": "Mensaje",
      "fields": {
        "body": {
          "type": "text",
          "required": true,
          "label": "Contenido",
          "sequence": 10
        },
        "message_type": {
          "type": "string",
          "length": 20,
          "required": true,
          "options": ["comment", "note", "system"],
          "default": "comment",
          "label": "Tipo",
          "sequence": 20
        },
        "author_name": {
          "type": "string",
          "length": 160,
          "label": "Autor",
          "sequence": 30
        },
        "author_id": {
          "type": "string",
          "length": 60,
          "label": "ID Autor",
          "sequence": 40
        },
        "record_model": {
          "type": "string",
          "length": 80,
          "index": true,
          "label": "Modelo vinculado",
          "help": "ej: contacts/contact, ventas/sale",
          "sequence": 50
        },
        "record_id": {
          "type": "string",
          "length": 60,
          "index": true,
          "label": "ID registro vinculado",
          "sequence": 60
        },
        "parent_id": {
          "type": "string",
          "length": 60,
          "index": true,
          "label": "Mensaje padre",
          "help": "ID del mensaje al que responde (para hilos)",
          "sequence": 70
        },
        "channel": {
          "type": "string",
          "length": 80,
          "index": true,
          "label": "Canal",
          "help": "ej: general, ventas, soporte",
          "sequence": 80
        }
      }
    }
  }
}
```

**Tabla resultante**: `mod_chatter_message` con RLS automático.

**Campos automáticos del SDK** (no declarados, se agregan solos):
- `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
- `tenant_id UUID NOT NULL` (RLS)
- `created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`
- `updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`

---

## 3. WASM Module (`main.go`)

Patrón mínimo idéntico a `contacts/main.go`:

```go
package main

import (
    "encoding/json"
    "unsafe"
)

var manifest = map[string]interface{}{
    "name": "chatter",
    "models": map[string]interface{}{
        "message": map[string]interface{}{
            "fields": map[string]interface{}{
                "body":         map[string]interface{}{"type": "text", "required": true},
                "message_type": map[string]interface{}{"type": "string", "required": true},
                "author_name":  map[string]interface{}{"type": "string"},
                "author_id":    map[string]interface{}{"type": "string"},
                "record_model": map[string]interface{}{"type": "string"},
                "record_id":    map[string]interface{}{"type": "string"},
                "parent_id":    map[string]interface{}{"type": "string"},
                "channel":      map[string]interface{}{"type": "string"},
            },
        },
    },
}

var resultBuf [65536]byte

//go:wasmexport fasterp_get_manifest
func fasterp_get_manifest() uint64 {
    data, _ := json.Marshal(manifest)
    n := copy(resultBuf[:], data)
    return uint64(uintptr(unsafe.Pointer(&resultBuf[0])))<<32 | uint64(n)
}

//go:wasmexport fasterp_on_load
func fasterp_on_load() uint32 { return 0 }

//go:wasmexport fasterp_on_unload
func fasterp_on_unload() uint32 { return 0 }

func main() {}
```

---

## 4. Frontend: Página Standalone (`/admin/chatter`)

### `frontend/index.html`

Estructura HTML con contenedor para el timeline global:

```html
<div class="ch-module">
  <div class="ft-header">
    <h1>💬 Chatter</h1>
    <p>Mensajes y discusiones internas del sistema.</p>
  </div>

  <div class="ft-guide" data-fast-guide="chatter">
    <span>Usá el Chatter para registrar notas, comentarios y actividad sobre cualquier registro.</span>
  </div>

  <!-- Filtros -->
  <div class="ch-filters">
    <select id="ch-filter-type">
      <option value="">Todos los tipos</option>
      <option value="comment">Comentarios</option>
      <option value="note">Notas internas</option>
      <option value="system">Sistema</option>
    </select>
    <select id="ch-filter-channel">
      <option value="">Todos los canales</option>
    </select>
    <input type="search" id="ch-search" placeholder="Buscar mensajes...">
  </div>

  <!-- Timeline -->
  <div id="ch-timeline" class="ch-timeline">
    <div class="ch-empty">Cargando mensajes…</div>
  </div>

  <!-- Paginación -->
  <div id="ch-pagination" class="ch-pagination"></div>
</div>

<script src="/modules/chatter/frontend/widget.js"></script>
<script src="/modules/chatter/frontend/app.js"></script>
```

### `frontend/app.js`

Lógica de la página standalone:
- Carga mensajes paginados con filtros
- Renderiza timeline con agrupación por fecha
- Muestra badges de tipo (comentario/nota/sistema)
- Paginación infinita o por página
- Búsqueda por texto

### `frontend/styles.css`

Estilos con prefijo `ch-`:
- Variables CSS: `--ch-border`, `--ch-surface`, `--ch-muted`, `--ch-accent`, `--ch-radius`
- Timeline layout con líneas conectoras
- Badges de tipo con colores distintos
- Hilos indentados para replies
- Formulario de respuesta

---

## 5. Widget Embebible (`frontend/widget.js`)

El widget es un script que otros módulos incluyen para mostrar el timeline de un registro específico.

### API del Widget

```javascript
// Cualquier módulo puede hacer:
ChatterWidget.render(containerElement, {
  recordModel: 'contacts/contact',    // modelo del registro
  recordId: 'uuid-del-contacto',      // ID del registro
  showComposer: true,                  // mostrar formulario para escribir
  maxHeight: '400px',                  // altura máxima del timeline
});
```

### Funciones Internas del Widget

```javascript
window.ChatterWidget = {
  // Renderiza el timeline completo en el contenedor dado
  render(container, options) { ... },

  // Carga mensajes para un registro específico
  async loadMessages(recordModel, recordId, page) { ... },

  // Renderiza un solo mensaje (con replies anidadas)
  renderMessage(msg, depth) { ... },

  // Envía un nuevo mensaje
  async sendMessage(options) { ... },

  // Responde a un mensaje (crea reply con parent_id)
  async replyTo(parentId, body, authorName) { ... },
};
```

### Cómo Otros Módulos lo Usan

En el `index.html` de cualquier módulo, agregar:

```html
<!-- En la página del módulo (ej: contacts) -->
<div id="contact-chatter"></div>
<script src="/modules/chatter/frontend/widget.js"></script>
<script>
  // Después de cargar el registro:
  ChatterWidget.render(document.getElementById('contact-chatter'), {
    recordModel: 'contacts/contact',
    recordId: currentContactId,
    showComposer: true,
  });
</script>
```

### Endpoints que Usa el Widget

| Método | URL | Descripción |
|--------|-----|-------------|
| `GET` | `/api/chatter/message?record_model=X&record_id=Y&page=1&limit=20` | Mensajes de un registro |
| `POST` | `/api/chatter/message` | Crear mensaje/reply |
| `GET` | `/api/chatter/message?channel=general&page=1&limit=20` | Timeline global (canal) |

**Headers**: `X-Tenant-ID`, `Authorization: Bearer {token}`

---

## 6.flujo de Datos

### Crear un Mensaje

```
1. Usuario escribe en el composer del widget
2. Widget llama POST /api/chatter/message con:
   {
     "body": "Revisar el crédito de este cliente",
     "message_type": "comment",
     "author_name": "Juan Pérez",
     "author_id": "uuid-usuario",
     "record_model": "contacts/contact",
     "record_id": "uuid-contacto",
     "parent_id": ""  // vacío si es mensaje nuevo
   }
3. Backend (auto-CRUD) inserta en mod_chatter_message con tenant_id del JWT
4. Widget recarga el timeline del registro
```

### Responder a un Mensaje

```
1. Usuario hace click en "Responder" en un mensaje
2. Se abre un mini-composer bajo ese mensaje
3. Widget llama POST /api/chatter/message con parent_id = id del mensaje padre
4. Backend inserta el reply
5. Widget re-renderiza el hilo con el reply anidado
```

### Timeline Global (Página Standalone)

```
1. Usuario navega a /admin/chatter
2. app.js llama GET /api/chatter/message?page=1&limit=50
3. Renderiza todos los mensajes (de todos los registros)
4. Filtros por tipo, canal, búsqueda por texto
```

---

## 7. Integración con Otros Módulos

### Opción A: Widget embebido en página existente

Agregar al `index.html` del módulo destino:

```html
<!-- Al final de contacts/frontend/index.html -->
<div id="contact-chatter-section" style="margin-top: 2rem;">
  <h3>💬 Historial</h3>
  <div id="contact-chatter"></div>
</div>
<script src="/modules/chatter/frontend/widget.js"></script>
```

Y en el `app.js` del módulo destino, después de cargar el registro:

```javascript
// Cuando se selecciona un contacto:
ChatterWidget.render(document.getElementById('contact-chatter'), {
  recordModel: 'contacts/contact',
  recordId: selectedContactId,
  showComposer: true,
});
```

### Opción B: Desde la API (para módulos WASM)

Los módulos WASM pueden crear mensajes del tipo `system` para registrar actividad:

```go
// En un módulo WASM, después de una acción importante:
sdk.Create(ctx, "message", map[string]any{
    "body":         "Estado cambiado de 'prospecto' a 'activo'",
    "message_type": "system",
    "author_name":  "Sistema",
    "record_model": "contacts/contact",
    "record_id":    contactID,
})
```

---

## 8. Archivos a Crear

| # | Archivo | Propósito |
|---|---------|-----------|
| 1 | `modules/chatter/manifest.json` | Schema del módulo (modelo message) |
| 2 | `modules/chatter/main.go` | Export WASM del manifest |
| 3 | `modules/chatter/frontend/index.html` | Página standalone (/admin/chatter) |
| 4 | `modules/chatter/frontend/styles.css` | Estilos del módulo (prefijo ch-) |
| 5 | `modules/chatter/frontend/app.js` | Lógica de la página standalone |
| 6 | `modules/chatter/frontend/widget.js` | Widget embebible para otros módulos |

**No se necesita**:
- Backend custom (todo via auto-CRUD del SDK)
- Migraciones SQL (el SDK crea la tabla desde el manifest)
- Cambios en `core/` (el sistema de módulos ya soporta todo)

---

## 9. Pasos de Implementación

1. Crear `modules/chatter/manifest.json` con el modelo message
2. Crear `modules/chatter/main.go` con el export WASM
3. Crear `modules/chatter/frontend/styles.css` con variables y estilos base
4. Crear `modules/chatter/frontend/widget.js` con el widget embebible
5. Crear `modules/chatter/frontend/app.js` con la lógica standalone
6. Crear `modules/chatter/frontend/index.html` con el HTML de la página
7. Compilar WASM (opcional pero recomendado)
8. Instalar módulo desde el Module Store o manualmente
9. Probar: crear mensaje, reply, timeline global, widget embebido

---

## 10. Dependencias

- **contacts** (opcional): Para mostrar info del contacto en mensajes vinculados
- **Ninguna dependencia obligatoria** funciona con cualquier módulo que tenga registros

---

## 11. Seguridad

- RLS automático: cada mensaje pertenece al tenant
- `author_id` se extrae del JWT (no se confía en el body para eso)
- `record_model` y `record_id` son strings genéricos (no FK) → flexible pero sin integridad referencial
- Los mensajes del tipo `system` solo deberían ser creados por el backend/WASM, no por usuarios directamente
