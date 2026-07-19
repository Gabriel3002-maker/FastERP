# FastERP Audit System

**Auditoría de nivel empresarial** con cumplimiento GDPR, SOX, ISO27001 e ISO27035.

## 🎯 Características

- ✅ **Append-only log** - Una vez registrado, no se puede modificar
- ✅ **Tenant isolation** - Cada tenant solo ve su propia auditoría
- ✅ **User tracking** - Quién, cuándo, desde dónde
- ✅ **Change history** - Before/After JSON para cada operación
- ✅ **Error logging** - Todos los errores se registran
- ✅ **IP tracking** - Dirección IP del cliente
- ✅ **Real-time stats** - Estadísticas instant de actividad
- ✅ **Export ready** - Exportar a JSON para compliance

## 📋 Tabla `audit_log`

```sql
CREATE TABLE audit_log (
  id              VARCHAR(255) PRIMARY KEY,
  tenant_id       UUID NOT NULL,
  user_id         UUID,
  action          VARCHAR(50),     -- CREATE, READ, UPDATE, DELETE, LOGIN
  entity          VARCHAR(255),    -- Tabla afectada
  entity_id       VARCHAR(255),    -- ID del registro
  before          JSONB,           -- Estado anterior
  after           JSONB,           -- Estado nuevo
  status          VARCHAR(50),     -- success, error
  error           TEXT,            -- Mensaje si hubo error
  ip_address      VARCHAR(45),     -- IPv4 o IPv6
  user_agent      TEXT,            -- Navegador/Cliente
  created_at      TIMESTAMP
);
```

## 🔌 Integración en Módulos

### Opción 1: ORM Advanced (Automático)

```go
package main

import (
	"github.com/fasterp/backend/db"
	"github.com/fasterp/backend/sdk"
)

func main() {
	// Crear ORM con auditoría automática
	orm := &sdk.AdvancedORM{
		DB: dbConnection,
		Config: &sdk.ORMConfig{
			EnableAudit:  true,  // ✅ Auditar automáticamente
			EnableEncryption: true,
			EncryptionKey: "32-byte-key-here",
			SoftDelete: true,
		},
	}

	// Crear contacto
	contact := map[string]interface{}{
		"name": "John Doe",
		"email": "john@example.com",
	}

	// Se registra automáticamente en audit_log
	err := orm.Create(tenantID, userID, "contacts", contact)
	// INSERT INTO audit_log (
	//   action='CREATE',
	//   entity='contacts',
	//   after={"name":"John Doe","email":"john@example.com"},
	//   user_id=userID,
	//   tenant_id=tenantID,
	//   created_at=NOW()
	// )

	// Actualizar
	updates := map[string]interface{}{
		"name": "Jane Doe",
	}
	err := orm.Update(tenantID, userID, recordID, "contacts", updates)
	// INSERT INTO audit_log (
	//   action='UPDATE',
	//   before={"name":"John Doe","email":"john@example.com"},
	//   after={"name":"Jane Doe","email":"john@example.com"},
	//   ...
	// )

	// Eliminar (soft delete)
	err := orm.Delete(tenantID, userID, recordID, "contacts")
	// INSERT INTO audit_log (
	//   action='DELETE',
	//   before={full record},
	//   status='success',
	//   ...
	// )
}
```

### Opción 2: Logger Manual (Custom Actions)

```go
import (
	"github.com/fasterp/backend/db"
)

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	auditLogger := db.NewAuditLogger(dbConnection)

	// Validar credenciales
	if !validateUser(username, password) {
		event := db.AuditEvent{
			TenantID:  tenantID,
			UserID:    userID,
			Action:    "LOGIN",
			Entity:    "users",
			EntityID:  userID,
			Status:    "error",
			Error:     "Invalid credentials",
			IPAddress: getClientIP(r),
			UserAgent: r.Header.Get("User-Agent"),
		}
		auditLogger.Log(r.Context(), event)
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Login exitoso
	event := db.AuditEvent{
		TenantID:  tenantID,
		UserID:    userID,
		Action:    "LOGIN",
		Entity:    "users",
		EntityID:  userID,
		Status:    "success",
		IPAddress: getClientIP(r),
		UserAgent: r.Header.Get("User-Agent"),
	}
	auditLogger.Log(r.Context(), event)

	// Emitir JWT y cookies
	w.WriteHeader(http.StatusOK)
}
```

## 🔍 Consultar Auditoría

### Desde Go

```go
auditLogger := db.NewAuditLogger(dbConnection)
ctx := context.Background()

// 1. Historial de un registro
trail, err := auditLogger.GetAuditTrail(ctx, tenantID, entityID, 100)
for _, event := range trail {
	fmt.Printf("%v: %s %s (user=%s)\n",
		event.CreatedAt, event.Action, event.Entity, event.UserID)
	if event.Before != nil {
		fmt.Printf("  Before: %+v\n", event.Before)
	}
	if event.After != nil {
		fmt.Printf("  After: %+v\n", event.After)
	}
}

// 2. Actividad de un usuario
userEvents, err := auditLogger.GetUserAudit(ctx, tenantID, userID, 50)

// 3. Estadísticas
stats, err := auditLogger.GetAuditStats(ctx, tenantID, 30)
fmt.Printf("Events last 30 days: %v\n", stats["total_events"])
fmt.Printf("Deletes: %v\n", stats["delete_count"])

// 4. Exportar
data, err := auditLogger.ExportAudit(ctx, tenantID,
	time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC),
)
os.WriteFile("audit_export.json", data, 0644)
```

### Desde HTTP

```bash
# Historial de un record (e.g. contact ID: 12345)
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/entity?entity_id=12345&limit=50"

# Actividad de un usuario
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/user?user_id=user-uuid&limit=100"

# Estadísticas de los últimos 30 días
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/stats?days=30"

# Respuesta:
# {
#   "period": 30,
#   "stats": {
#     "total_events": 1250,
#     "unique_users": 15,
#     "unique_entities": 8,
#     "error_count": 3,
#     "delete_count": 5,
#     "update_count": 450,
#     "create_count": 700
#   }
# }

# Exportar auditoría (descarga JSON)
curl -H "X-Tenant-ID: tenant-uuid" \
  "http://localhost:7071/api/audit/export?start=2026-07-01&end=2026-07-31" \
  > audit_export.json
```

## 📊 Casos de Uso

### 1. Detectar cambios sospechosos

```sql
-- Qué usuario eliminó más de 100 registros en la última hora
SELECT user_id, COUNT(*) as deletes
FROM audit_log
WHERE tenant_id = 'abc123'
  AND action = 'DELETE'
  AND created_at > NOW() - INTERVAL '1 hour'
GROUP BY user_id
HAVING COUNT(*) > 100;
```

### 2. Compliance: Quién vio datos de cliente

```sql
-- Auditar acceso a emails de clientes
SELECT DISTINCT user_id, MIN(created_at)
FROM audit_log
WHERE entity = 'customers'
  AND action = 'READ'
  AND tenant_id = 'acme-corp'
ORDER BY created_at DESC;
```

### 3. Forensics: Reconstruir estado anterior

```go
// Si un registro fue corrupto, ver todas las versiones
trail, _ := auditLogger.GetAuditTrail(ctx, tenantID, recordID, 1000)

for i := len(trail) - 1; i >= 0; i-- {
	event := trail[i]
	fmt.Printf("[%s] %s by %s\n", 
		event.CreatedAt, event.Action, event.UserID)
	fmt.Printf("  %+v\n", event.Before)
}

// Si necesitas restaurar, extrae el JSON `before` más antiguo
```

### 4. Reportes: Actividad por módulo

```sql
-- Cuántas operaciones por módulo, últimos 7 días
SELECT entity, action, COUNT(*) as count
FROM audit_log
WHERE tenant_id = 'xyz789' AND created_at > NOW() - INTERVAL '7 days'
GROUP BY entity, action
ORDER BY count DESC;
```

## 🔐 Seguridad & Compliance

### GDPR (Right to be Forgotten)

⚠️ **Nota**: La auditoría es **inmutable por diseño**. Para cumplir con GDPR:

1. **No audita datos personales directos** — solo IDs + cambios
2. **Logs pueden se retenidos** según política de la empresa
3. **Permitir acceso** a usuarios sobre su propio audit trail vía `/api/audit/user`

```go
// Ejemplo: Usuario solicita ver su actividad
userID := extractUserIDFromJWT(r)
// Solo ver su propio audit trail
events, _ := auditLogger.GetUserAudit(ctx, tenantID, userID, 1000)
```

### SOX (Sarbanes-Oxley)

✅ **Append-only** — Imposible modificar/borrar eventos  
✅ **Timestamps** — Sincronización de reloj obligatoria  
✅ **User tracking** — Quién, cuándo  
✅ **Change detail** — Before/After JSON  

```sql
-- Verificación de SOX: auditar a auditor (meta)
SELECT * FROM audit_log WHERE entity = 'audit_log';
```

### ISO27001 (Information Security)

✅ **Tenant isolation** — RLS + tenant_id en toda query  
✅ **IP tracking** — Detectar acceso anómalo (IP inesperada)  
✅ **Error logging** — Capturar intentos fallidos de acceso  

```sql
-- Detección: múltiples fallos de login desde misma IP
SELECT ip_address, COUNT(*) as failed_attempts
FROM audit_log
WHERE action = 'LOGIN' AND status = 'error'
  AND created_at > NOW() - INTERVAL '1 hour'
GROUP BY ip_address
HAVING COUNT(*) > 5;
```

## ⚡ Performance

### Índices

```sql
CREATE INDEX idx_audit_tenant ON audit_log(tenant_id);
CREATE INDEX idx_audit_user ON audit_log(user_id);
CREATE INDEX idx_audit_entity ON audit_log(entity);
CREATE INDEX idx_audit_created ON audit_log(created_at DESC);
```

### Retención

Recomendación según industria:
- **Retail**: 1 año
- **Financial**: 7 años (máximo)
- **Healthcare**: 3 años
- **Legal**: Indefinido

```sql
-- Archivar datos antiguos (PostgreSQL)
INSERT INTO audit_log_archive
SELECT * FROM audit_log WHERE created_at < NOW() - INTERVAL '1 year';

DELETE FROM audit_log WHERE created_at < NOW() - INTERVAL '1 year';
```

## 📝 Ejemplo Real: Módulo Contacts

```go
// contacts/main.go
package main

import (
	"github.com/fasterp/backend/sdk"
)

func HandleCreateContact(w http.ResponseWriter, r *http.Request) {
	sdk := r.Context().Value("sdk").(*sdk.ModuleSDK)
	tenantID := r.Header.Get("X-Tenant-ID")
	userID := r.Context().Value("user_id").(string)

	var contact struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Phone string `json:"phone"`
	}
	json.NewDecoder(r.Body).Decode(&contact)

	// ORM con auditoría automática
	orm := &sdk.AdvancedORM{
		DB: sdk.DB,
		Config: &sdk.ORMConfig{
			EnableAudit: true,
			SoftDelete: true,
		},
	}

	data := map[string]interface{}{
		"name":  contact.Name,
		"email": contact.Email,
		"phone": contact.Phone,
	}

	// Crear → audita automáticamente
	err := orm.Create(tenantID, userID, "contacts", data)
	if err != nil {
		// Error también se audita
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
```

### Trazabilidad Automática

```
User: alice@acme.com
1. 2026-07-18 10:30:15 - CREATE contacts "John Doe"
   IP: 192.168.1.100
   After: {"name":"John Doe","email":"john@acme.com","phone":"555-1234"}

2. 2026-07-18 10:32:45 - UPDATE contacts "John Doe"
   IP: 192.168.1.100
   Before: {"name":"John Doe","email":"john@acme.com","phone":"555-1234"}
   After: {"name":"John D.","email":"j.doe@acme.com","phone":"555-1234"}

3. 2026-07-18 10:35:12 - DELETE contacts "John D."
   IP: 192.168.1.100
   Status: success
```

## ✅ Checklist para Módulos

- [ ] Usar `AdvancedORM` con `EnableAudit: true`
- [ ] Capturar `X-Tenant-ID` header en todas requests
- [ ] Pasar `userID` a todas operaciones CRUD
- [ ] Registrar custom events (LOGIN, ERROR, etc) vía `AuditLogger.Log()`
- [ ] Exportar logs para compliance anualmente
- [ ] Revisar `/api/audit/stats` para detectar anomalías

---

**Made with ❤️ for Compliance**
