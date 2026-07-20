# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Quick Start

**Unified mode** (embedded frontend in Go binary):
```bash
make run
# Single process: backend + frontend at http://localhost:7071
```

**Development mode** (separate Vite + Go with hot reload):
```bash
./dev.sh
# Frontend: Vite at http://localhost:5173 (proxies /api to backend)
# Backend: Go at http://localhost:7071

# Or separately:
./dev.sh frontend    # Vite only
./dev.sh backend     # Go only
```

**Debugging in VSCode**:
Open in VSCode and use `Ctrl+Shift+D` → "Full Stack (Backend + Frontend)" → `F5` for breakpoints.

## Architecture Overview

**FastERP** is a multi-tenant ERP built on:
- **Backend**: Go (Gin framework, port 7071)
- **Frontend**: React 19 + TypeScript + Tailwind CSS + Vite (port 5173)
- **Database**: PostgreSQL (with per-tenant row-level security)
- **Modules**: WebAssembly (WASM) for extensibility

**Multi-tenant isolation**: Tenant ID is passed in headers (`X-Tenant-ID`) or set at login; database uses RLS policies on all tables. Default tenant ID is stored in `.default-tenant-id`.

## Project Structure

```
backend/
  cmd/
    server/          # Main server entry (cmd/server/main.go, webdist.go)
    module-builder/  # WASM compilation tool
  internal/
    api/             # HTTP route handlers (router.go, auth handlers, CRUD endpoints)
    models/          # Type definitions
    db/              # Database layer, migrations, RLS policies
    config/          # Config loading from env vars
    module/          # WASM runtime (wazero-based loader, manifest types)
    odoo/            # Odoo JSON-RPC client + sync logic
    store/           # Store/tienda product override layer
    web/             # Public website/sitio_web serving

frontend/
  src/
    pages/           # App-level pages (Login, Dashboard, ModuleStore)
    components/      # Reusable UI components (Layout, PublicLayout)
    api/             # API client wrappers
    hooks/           # Custom React hooks
    moduleRegistry.ts # Dynamic module route discovery (reads manifest.frontend.pages)

modules/
  contacts/          # Contact management module
    main.go          # WASM logic
    manifest.json    # Module metadata + frontend page declarations
    module.wasm      # Compiled WebAssembly binary
    frontend/pages/  # React components (ContactsPage.tsx, etc.)
  tienda_web/        # Store module (product enrichment)
    main.go
    manifest.json
    module.wasm
    frontend/pages/  # StorePage.tsx
  sitio_web/         # CMS module (public website builder)
    main.go
    manifest.json
    module.wasm
    frontend/pages/  # SiteBuilder.tsx, PublicStore.tsx
  [other modules...]
```

**Key change**: Each module now owns its frontend UI under `modules/<name>/frontend/pages/*.tsx`. The app shell (React) discovers and registers routes dynamically at runtime based on `manifest.json`'s `frontend.pages` metadata — no manual route editing needed when adding new modules.

## Module System (WASM)

Modules are compiled Go code (WASM) that extend the ERP:

1. **Structure**: Each module has `manifest.json` (metadata, models, menus) + `module.wasm` (binary)
2. **Auto-generated tables**: For each model in the manifest, the backend creates a PostgreSQL table and REST CRUD routes (`/api/{module_name}/{model}/`)
3. **Auto-generated UI**: A generic `ModelPage` component renders a CRUD grid for each table

**Building a module**:
```bash
# Compile Go to WASM (from module directory)
GOOS=js GOARCH=wasm go build -o module.wasm main.go

# Or use TinyGo for smaller binaries
tinygo build -o module.wasm -target wasm main.go
```

**Key constraint**: WASM modules run in a **sandboxed runtime (wazero)** with **no network access**. Odoo sync and other external integrations are handled by native Go code in `backend/internal/` and exposed via routes.

## @fast API: Zero-SQL CRUD for All Modules

Every module can use **`@fast`** methods directly — **no SQL, no handlers needed.**

### From Any Module:
```
@fast.create("model", {...data})    → Create record
@fast.read("model", id)             → Read single record
@fast.update("model", id, {...})    → Update record
@fast.delete("model", id)           → Delete record
@fast.list("model")                 → List all records
@fast.search("model", {...filters}) → Search with filters
```

**Example in module frontend** (`modules/mymodule/frontend/app.js`):
```javascript
// Create
const id = await fetch('/api/mymodule/record', {
  method: 'POST',
  headers: {'X-Tenant-ID': tenantID},
  body: JSON.stringify({title: 'My Title'})
}).then(r => r.json());

// Read - SDK handles it: @fast.read("record", id)
const record = await fetch(`/api/mymodule/record/${id}`, 
  {headers: {'X-Tenant-ID': tenantID}}
).then(r => r.json());

// Update - @fast.update("record", id, {...})
await fetch(`/api/mymodule/record/${id}`, {
  method: 'PUT',
  headers: {'X-Tenant-ID': tenantID},
  body: JSON.stringify({title: 'Updated Title'})
});

// Delete - @fast.delete("record", id)
await fetch(`/api/mymodule/record/${id}`, {
  method: 'DELETE',
  headers: {'X-Tenant-ID': tenantID}
});

// List - @fast.list("record")
const records = await fetch(`/api/mymodule/record`, 
  {headers: {'X-Tenant-ID': tenantID}}
).then(r => r.json());
```

### How It Works (Behind the Scenes)
1. **Module defines schema** in `manifest.json` (models + fields)
2. **Frontend calls REST API** → `/api/{module}/{model}[/{id}]`
3. **Core dispatcher** reads manifest + calls `@fast.create/read/update/delete/list`
4. **SDK executes** with automatic:
   - ✅ RLS enforcement (tenant isolation)
   - ✅ Field validation (vs manifest schema)
   - ✅ Audit logging (created_at, updated_at)
   - ✅ Type safety

### Why This Matters

| Approach | Code | Safety | Dev Time |
|----------|------|--------|----------|
| **Raw SQL** | `query := "SELECT * FROM mod_..."` | Manual validation | Slow |
| **SDK** | `sdk.List(ctx, "contact")` | Type-safe (manifest) | ⚡ Fast |

**Features**:
- ✅ Zero SQL — schema from `manifest.json`
- ✅ Auto RLS — tenant isolation at DB level
- ✅ Type-safe — fields validated vs manifest
- ✅ Multi-tenant — `tenant_id` handled automatically
- ✅ Audit ready — `created_at`, `updated_at` automatic

### Example: Creating a New Module (Ultra-Fast)

1. **Define manifest** (`modules/mymodule/manifest.json`):
```json
{
  "name": "mymodule",
  "models": {
    "record": {
      "fields": {
        "title": {"type": "string", "required": true},
        "description": {"type": "text"}
      }
    }
  }
}
```

2. **Write handler** (`internal/handlers/mymodule.go`):
```go
func (h *MyModuleHandler) CreateRecord(w http.ResponseWriter, r *http.Request) {
    tenantID := r.Header.Get("X-Tenant-ID")
    h.sdk.TenantID = tenantID
    
    id, err := h.sdk.Create(r.Context(), "record", map[string]interface{}{
        "title": r.FormValue("title"),
        "description": r.FormValue("description"),
    })
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"id": id})
}
```

That's it! No migrations, no queries, no boilerplate. The SDK handles everything. See `core/sdk/example_usage.go` for more.

## Key Development Workflows

### Adding a Database Column to a Module

1. Edit the module's `manifest.json` to update the `models[].fields` array
2. Recompile and restart the backend
3. The backend detects the schema change and creates/alters the table via migrations

### Integrating with Odoo

- **Odoo details** are in the project memory: Odoo runs locally at `http://localhost:8073`, db `intercom`, login `intercom` (uid 2), with token-based auth
- **Native code**: All Odoo JSON-RPC logic lives in `backend/internal/odoo/` (client + service)
- **Module**: `integracion_odoo_fasterp` only declares the tables/menus; the sync happens in native backend code
- **Tables**: Imported Odoo data is written to tables like `mod_integracion_odoo_fasterp_imported_product`, `mod_integracion_odoo_fasterp_product_mapping`, etc.
- **Flows**: Bidirectional sync (pull product.product → imported_product; push product.template, crm.lead, sale.order, stock/price)

### Store Module (`tienda_web`)

- Enriches synced Odoo products with local overrides (display name, description, price, published/featured status)
- **Backend**: `backend/internal/store/` provides `/api/store/products` (merged view), PUT/POST for edits + image uploads
- **Frontend**: `StorePage.tsx` (product grid at `/tienda`) and `ProductEditor.tsx` (detail page + image gallery)
- **Images**: Uploaded to `uploads/products/`, served publicly at `/uploads/products/...`
- **Note**: Images and product edits are local (don't push back to Odoo)

### Website Module (`sitio_web`)

- WordPress-like CMS for building a public storefront
- **Model**: `web_page` (slug, title, html, css, js, published, is_home, seq)
- **Frontend editor**: `SiteBuilder.tsx` at `/sitio` (page list, HTML/CSS/JS panes, live preview, publish button)
- **Public serving**: Native `backend/internal/web/` serves public pages (no auth) at `GET /site` (home) and `GET /site/:slug`
- **Product feed**: `GET /api/public/products` (no auth, returns only published store products)
- **Tenant**: Public site resolves to the "default" tenant (or `?t=slug` for multi-tenant)
- **Security note**: User-authored JS is served same-origin as the app; for production, the public site should run on a separate origin/port

## Common Commands

```bash
# Build
make backend              # Compile Go binaries to ./bin/
make frontend             # npm install + build
make build-module MODULE=./modules/examples/hello-world

# Development
make dev-backend         # Run server with hot reload (uses `go run`)
make dev-frontend        # Run Vite dev server
make lint                # Run go vet + golangci-lint
make tidy                # go mod tidy

# Clean
make clean               # Remove bin/, uploads/, compiled modules

# Docker (for production/CI)
docker compose up --build -d
docker compose down
```

## Environment Variables

Located in `backend/.env` (see `.env.example` for all options):
```env
FASTERP_PORT=7071
FASTERP_DATABASE_URL=postgres://odoo17:odoo17@localhost:5432/fasterp?sslmode=disable
FASTERP_MODULES_DIR=./modules
FASTERP_UPLOAD_DIR=./uploads
FASTERP_CORS_ORIGINS=http://localhost:5173
FASTERP_JWT_SECRET=dev-secret  # Change in production
FASTERP_AUTH_RATE_LIMIT=10     # Separate, tighter limit for /api/auth/*
```

**Database role**: connect as a non-superuser role without `BYPASSRLS`. A superuser
ignores every RLS policy, so tenant isolation would silently fall back to the explicit
`WHERE tenant_id` filters alone. The server logs a loud warning at startup if the
connecting role can bypass RLS.

## Frontend Development

- **Tech stack**: React 19 + TypeScript + Tailwind CSS + Vite
- **Hot reload**: Changes to `.tsx`/`.css` auto-refresh via Vite
- **API client**: Wrappers in `frontend/src/api/` for backend endpoints
- **Tenant context**: Tenant ID is stored in React context/localStorage; passed in `X-Tenant-ID` header to all API calls
- **Dev server port**: 5173; Vite config proxies `/api/*` to `http://localhost:7071` and `/uploads` to the backend

## Backend Development

- **Gin router**: Main routes registered in `backend/internal/api/router.go`
- **WASM loading**: `backend/internal/module/loader.go` loads `.wasm` files from `FASTERP_MODULES_DIR`
- **Auto-CRUD**: For each module model, `backend/internal/api/` generates REST endpoints (GET, POST, PUT, DELETE)
- **Database**: Direct SQL via `database/sql`; migrations are applied on startup (see `backend/internal/db/`)
- **Multi-tenant**: `TenantMiddleware` resolves the tenant (slug or UUID), verifies it is
  active, and pins a connection with `app.tenant_id` set for RLS. `AuthMiddleware` then
  requires the JWT's tenant to match. Handlers **must** use `db.Executor(c)` to reach that
  connection — `db.DB` is the shared pool with no tenant context, reserved for startup and
  for public paths that resolve their own tenant.
- **Module routes**: auto-generated CRUD is served by one dispatcher registered at startup
  (`/api/{module}/{model}[/{id}]`), which resolves the module per request and checks it is
  active for the calling tenant. Never add routes to the running `gin.Engine`.

## Deployment

- **Docker**: `docker-compose.yml` and `docker-compose.odoo.yml` for orchestration
- **Odoo setup**: See `ODOO_SETUP.md` and `ODOO_CONFIG_STEP_BY_STEP.md` for local Odoo instance configuration
- **Database**: Requires PostgreSQL 16+; migrations run automatically on backend startup

## Debugging

**Go (Backend)**:
- Use VSCode debugger: `Ctrl+Shift+D` → "Backend (Go)" → `F9` for breakpoints
- Logs in `backend/server.log` or stdout during `go run`
- Use `curl` to test API endpoints

**React (Frontend)**:
- DevTools (`F12`) for DOM/state inspection
- React DevTools browser extension for component inspection
- HMR (hot module replacement) via Vite

## Testing

- Backend: `go test ./...` from `backend/` directory
- Frontend: `npm test` from `frontend/` directory (if configured)
- Integration: Use `curl` or Postman to test API + WASM module loading

## Important Notes

- **WASM is sandboxed**: Modules cannot access the network or filesystem directly; use native backend code for external integrations
- **Multi-tenant RLS**: All tables use row-level security; ensure tenant filtering is applied in all queries
- **Vite dev server**: Must be restarted if proxy mappings change (e.g., after editing `vite.config.ts`)
- **Module recompilation**: Old modules compiled with `GOOS=js` may not load; recompile with `GOOS=wasip1 GOARCH=wasm GOTOOLCHAIN=go1.25.11` if issues arise
- **Token auth for Odoo**: The Odoo user must generate an API token in Settings → API Keys; use that token instead of a password in RPC calls
