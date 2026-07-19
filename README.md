# 🚀 FastERP — Modular ERP Built for Speed

**Enterprise-grade, multi-tenant ERP with WebAssembly modules that compile fast.**

- ⚡ **Super Fast Development**: Create a full CRUD module in minutes
- 🔐 **Secure by Default**: Row-level security, multi-tenant isolation built-in
- 📦 **Modular**: Add features without touching core
- 🎯 **No Boilerplate**: SDK abstracts SQL — handlers focus on logic

---

## Quick Start (3 minutes)

### 1. Start the Server

```bash
cd core
go run cmd/server/main.go
```

Server runs at `http://localhost:7071`

### 2. Login

- **URL**: `http://localhost:7071`
- **User**: `admin`
- **Password**: `admin123`

### 3. Navigate to Modules

Click **"Modules"** in the admin panel to install and manage modules.

---

## Create a Module in 5 Minutes

### Step 1: Define Schema

Create `modules/mynewmodule/manifest.json`:

```json
{
  "name": "mynewmodule",
  "models": {
    "item": {
      "fields": {
        "title": {"type": "string", "required": true},
        "description": {"type": "text"},
        "status": {"type": "string"}
      }
    }
  }
}
```

### Step 2: Write Handler (No SQL!)

Create `internal/handlers/mynewmodule.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"github.com/fasterp/backend/db"
	"github.com/fasterp/backend/sdk"
)

type MyModuleHandler struct {
	dbConn *db.DB
	sdk    *sdk.ModuleSDK
}

func NewMyModuleHandler(dbConn *db.DB) *MyModuleHandler {
	moduleSdk := sdk.NewModuleSDK("mynewmodule", "", "", dbConn.Pool())
	moduleSdk.LoadManifest(`{...manifest json...}`)
	return &MyModuleHandler{dbConn: dbConn, sdk: moduleSdk}
}

// CREATE
func (h *MyModuleHandler) CreateItem(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	h.sdk.TenantID = tenantID
	
	id, _ := h.sdk.Create(r.Context(), "item", map[string]interface{}{
		"title": r.FormValue("title"),
		"description": r.FormValue("description"),
		"status": r.FormValue("status"),
	})
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// LIST
func (h *MyModuleHandler) ListItems(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	h.sdk.TenantID = tenantID
	
	items, _ := h.sdk.List(r.Context(), "item")
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}
```

### Step 3: Register Routes

In `internal/api/router.go`, add:

```go
mymoduleHandler := handlers.NewMyModuleHandler(h.db)
r.POST("/api/mynewmodule/item", mymoduleHandler.CreateItem)
r.GET("/api/mynewmodule/item", mymoduleHandler.ListItems)
```

**Done!** 🎉 Your module has:
- ✅ Full CRUD (Create, Read, Update, Delete)
- ✅ Multi-tenant isolation
- ✅ RLS security at database
- ✅ Automatic audit trails

---

## SDK API Cheat Sheet

**No SQL required — just use these methods:**

```go
sdk := sdk.NewModuleSDK("modulename", tenantID, userID, db.Pool())
sdk.LoadManifest(manifestJSON)

// CREATE
id, err := sdk.Create(ctx, "model", map[string]interface{}{
    "field1": value1,
    "field2": value2,
})

// READ
record, err := sdk.Get(ctx, "model", id)

// UPDATE
err := sdk.Update(ctx, "model", id, map[string]interface{}{
    "field1": newValue,
})

// DELETE
err := sdk.Delete(ctx, "model", id)

// LIST
records, err := sdk.List(ctx, "model")
```

---

## Architecture

```
FastERP
├── core/               # Go backend (HTTP handlers, SDK)
│   ├── cmd/server/     # Main entry point
│   ├── internal/
│   │   ├── api/        # Routes
│   │   ├── handlers/   # Handler logic (uses SDK, not SQL)
│   │   ├── db/         # Database connection + migrations
│   │   └── sdk/        # ⭐ SDK with Create, Update, Delete, List
│   └── templates/      # Go templates (HTML, CSS served server-side)
│
├── modules/            # Pluggable features
│   ├── contacts/       # Example module
│   │   ├── manifest.json
│   │   └── frontend/   # HTML/CSS/JS UI
│   └── tienda_web/     # Store module
│
└── db/
    └── migrations/     # SQL migrations (auto-run on startup)
```

**Key Principle**: Backend handlers use **SDK** (type-safe, no SQL), frontend uses **HTTP** (REST API).

---

## Security Features

- **Multi-tenant RLS**: PostgreSQL row-level security enforces tenant isolation
- **JWT Auth**: Stateless authentication with refresh tokens
- **Audit Logs**: Append-only encrypted logs of all changes
- **Input Validation**: SDK validates against manifest schema
- **Secure Headers**: CORS, CSP, X-Content-Type-Options configured

---

## Database

PostgreSQL 16+ required.

**Auto-migrations**: On startup, the server:
1. Creates all tables for installed modules
2. Applies row-level security policies
3. Creates indexes for performance

---

## Development

### Environment

Create `.env`:
```env
FASTERP_PORT=7071
FASTERP_DATABASE_URL=postgres://user:pass@localhost:5432/fasterp
FASTERP_MODULES_DIR=./modules
FASTERP_UPLOAD_DIR=./uploads
FASTERP_JWT_SECRET=dev-secret-change-in-prod
```

### Run

```bash
cd core
go run cmd/server/main.go
```

### Test SDK

See `core/sdk/example_usage.go` for working examples.

---

## Next Steps

1. **Read** `CLAUDE.md` for detailed architecture
2. **Check** `core/sdk/README.md` for SDK deep dive
3. **Explore** `modules/contacts/` for a real example
4. **Create** your first module!

---

## FAQs

**Q: Do I need to write SQL?**  
A: Nope! SDK handles all CRUD. Only SQL if you need custom queries (rare).

**Q: How do I add fields to a model?**  
A: Edit `manifest.json`, restart server. SDK auto-creates/alters tables.

**Q: Is it secure for multi-tenant?**  
A: Yes! RLS policies at DB level + tenant_id header validation + JWT checks.

**Q: Can I use WASM modules?**  
A: Yes! See `CLAUDE.md` → "Module System (WASM)". But for HTTP endpoints, use handlers + SDK.

---

**Made with ❤️ for fast, maintainable module development.**
