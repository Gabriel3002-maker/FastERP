# Auditoría de seguridad y testing — FastERP

Fecha: 2026-07-19 · Alcance: `core/` (Go), `modules/` (WASM), `core/templates/` + `core/static/` (UI servida por el servidor).
Método: build + `go test ./...` en todo `core/`, tests nuevos donde faltaba cobertura, y revisión estática de código (sin explotación activa contra un servidor corriendo).

## Resumen ejecutivo

El repo tiene **dos implementaciones de backend paralelas y muy distintas en postura de seguridad**, y eso es en sí mismo el hallazgo más importante de esta auditoría:

| | `core/main.go` (paquete `handlers/`) | `core/cmd/server/` (paquete `internal/api/`) |
|---|---|---|
| Se arranca con | `make run` / `go run ./main.go` (documentado en `CLAUDE.md` como "Unified mode") | `go run cmd/server/main.go` (documentado en `README.md`), y es lo que compila `core/Dockerfile` |
| Router | `net/http` + `html/template` | Gin |
| Autenticación en el CRUD genérico (`/api/{módulo}/{modelo}`) | **Ninguna** — el propio código lo documenta así (ver Crítico #1) | JWT + verificación de tenant en cada request |
| Aislamiento multi-tenant | Filtros `WHERE tenant_id` manuales, sin RLS forzado por el rol de conexión que usa este binario | RLS con `FORCE ROW LEVEL SECURITY` + rol de BD sin `BYPASSRLS` (correctamente configurado en `docker-compose.yml`/`docker/init-db.sh`) |
| Secreto JWT | Hardcodeado en el código fuente (ver Crítico #2) | Viene de `FASTERP_JWT_SECRET`, y `docker-compose.yml` falla el arranque si no está definido |

**Ahora mismo, la versión con mejor arquitectura (`cmd/server`) es la que la documentación llama "producción" vía Docker — pero `docker-compose.yml` apunta a directorios `./backend` y `./frontend` que ya no existen tras la migración a `core/`, así que ese `docker compose up --build` falla antes de arrancar nada.** Eso significa que, tal como está el repo hoy, el camino que sí se puede levantar sin tocar nada (`make run`) es el que tiene los problemas críticos de este informe.

Todos los hallazgos de abajo están verificados leyendo el código real que ejecuta cada ruta (no son inferencias sobre el manifest ni sobre nombres de archivo) y, donde tenía sentido, con un test que lo demuestra.

---

## Qué se probó

- `go build ./...` y `go vet ./...` sobre todo `core/` — limpio, sin errores.
- `go test ./... -count=1` — **100% verde**, 8 archivos de test existentes (auth middleware, RLS de la query builder, parser BPMN, generación de Studio, validación de inputs del CRUD genérico de `internal/api`, etc.).
- Se agregaron tests nuevos en `core/handlers/session_test.go` (paquete que no tenía ningún test) cubriendo el `SessionManager` que emite y valida los JWT del binario `core/main.go`: emisión/validación normal, rechazo de firma ajena, rechazo de token expirado, precedencia del header `Authorization` sobre la cookie, y un test de regresión de seguridad (`TestAuthHandlerSigningSecretIsHardcoded`) que firma un token por fuera del código —tal como lo haría un atacante que solo leyó el repo— y confirma que el propio `SessionManager` del servidor lo acepta como admin válido. Ese test está pensado para **fallar el día que se arregle el secreto hardcodeado**, como recordatorio de borrarlo.
- Paquetes de `core/` sin ningún test, antes y después de esta pasada (quedan pendientes, ver Recomendaciones): `db/`, `middleware/`, `internal/db/`, `internal/odoo/`, `internal/store/`, `internal/web/`, `utils/`, `config/`, `internal/config/`. De estos, `internal/store` e `internal/web` fueron revisados a mano (ver "Lo que está bien") y no mostraron problemas; no se agregaron tests ahí porque requieren una base Postgres real y no se levantó infraestructura nueva sin pedir permiso primero.
- `modules/*` (11 módulos WASM): sin tests propios — son solo declaraciones de esquema (`main.go` exporta un manifest), la lógica real vive en `core/internal/{store,web,odoo}` y en el motor `@fast` (`core/sdk/`, `core/internal/module/`), que sí tienen tests.

---

## Hallazgos — servidor `core/main.go` (`make run`)

### 🔴 Crítico 1 — El CRUD genérico no exige autenticación

`core/handlers/generic_crud.go:48-54` lo dice explícitamente en un comentario del propio código:

> "estas rutas no exigen login (X-Tenant-ID alcanza para operar)"

`HandleCRUD` (línea 71) resuelve el tenant únicamente desde el header `X-Tenant-ID` (línea 83) y nunca verifica un token de sesión antes de leer, crear, modificar o borrar cualquier registro de cualquier módulo. El JWT solo se usa, si está presente, para anotar *quién* hizo un cambio en el historial de auditoría (línea 55-67) — pero su ausencia no bloquea nada.

**Escenario concreto de explotación:** cualquiera que conozca (o adivine) el UUID de un tenant —el tenant "default" que se crea en el primer arranque queda escrito en `.default-tenant-id` y en el log del servidor, y además viaja en claro por `localStorage`/cookies del navegador de cualquier usuario legítimo— puede hacer `GET/POST/PUT/DELETE /api/{módulo}/{modelo}` sin ninguna contraseña ni token y leer o alterar todos los contactos, productos, automatizaciones, conexiones de Odoo, etc. de ese tenant.

**Arreglo:** exigir un JWT válido (y que su `tenant_id` coincida con el header) antes de despachar a `HandleCRUD`, igual que ya hace `AuthMiddleware` en `internal/api/handler.go:249`. Es el mismo patrón que ya existe en el otro binario — portarlo aquí, no reinventarlo.

### 🔴 Crítico 2 — Secreto JWT hardcodeado, permite forjar tokens de admin

`core/handlers/auth.go:36`:

```go
sessionManager: NewSessionManager("dev-secret", "dev-secret-refresh"),
```

El secreto con el que se firman y verifican **todos** los JWT de este binario es el string literal `"dev-secret"`, escrito en el código fuente que está en este repositorio — no viene de ninguna variable de entorno (a diferencia de `core/config/config.go:77`, que sí define `FASTERP_JWT_SECRET`, pero ese valor nunca se usa en este camino de código: es un campo de configuración muerto).

Esto por sí solo ya es grave, pero se vuelve crítico porque `StudioHandler.requireAdmin` (`core/handlers/studio.go:341-356`) valida el token con este mismo `SessionManager` y exige `claims.IsAdmin == true` para autorizar `POST /api/_studio/generate` (`studio.go:152`), que **escribe archivos nuevos en `modules/<nombre>/`** (manifest, frontend) para instalar un módulo generado desde un diagrama.

**Escenario concreto:** cualquiera firma un JWT HS256 con la clave `"dev-secret"` y el claim `is_admin: true` (unas pocas líneas con cualquier librería JWT), lo manda como `Authorization: Bearer ...` a `/api/_studio/generate`, y el servidor lo acepta como admin legítimo — sin haber iniciado sesión nunca. Queda demostrado ejecutablemente en `TestAuthHandlerSigningSecretIsHardcoded` (`core/handlers/session_test.go`).

**Arreglo:** que `NewAuthHandler` reciba el secreto desde `config.Load().Security.JWTSecret` (que ya existe y ya lee `FASTERP_JWT_SECRET`) en vez de hardcodearlo.

### 🟠 Alto 3 — `/modules/` sirve archivos sin autenticación ni control por tenant

`core/main.go:174-176`:

```go
modulesFs := http.FileServer(http.Dir("../modules"))
mux.Handle("/modules/", http.StripPrefix("/modules/", modulesFs))
```

A diferencia de `/admin/{módulo}` (línea 135-168), que sí verifica que el módulo esté instalado para el tenant de la cookie antes de servir nada, este handler no tiene ningún middleware de auth ni de tenant. Cualquiera puede pedir `GET /modules/<módulo>/manifest.json`, el código fuente `main.go` de cualquier módulo (en un checkout de desarrollo, que es como está pensado correr `make run`), o listar el directorio si Go no encuentra un `index.html` en esa carpeta (el `http.FileServer` de Go lista directorios por defecto).

**Arreglo:** servir solo `frontend/` de cada módulo (no la raíz del directorio), y aun así, condicionar a que el módulo esté instalado y activo para el tenant que pide el archivo — igual que ya hace `/admin/`.

### 🟠 Alto 4 — XSS almacenado en las plantillas del panel (module store y auditoría)

Confirmado en `core/templates/modules-content.html:461-494`, `module-store.html:688-721`, `layout.html:351-356` (sidebar, se ejecuta en *toda* página autenticada), `audit-content.html:236-245` y `audit.html:542-551`: los campos `icon/label/name/description/version/author` del manifest de un módulo, y `user_id/action/entity/entity_id/status` de una entrada de auditoría, se insertan en `innerHTML` **sin escapar**, a diferencia de `core/static/js/fast-views.js`, que sí usa un helper `esc()` de forma consistente en las 584 líneas del archivo.

Como el manifest de un módulo puede llegar a existir por otras vías que no son "un desarrollador lo escribió a mano" (por ejemplo, el flujo de Studio que convierte un diagrama en módulo instalable, o directamente el Crítico 1: cualquiera puede crear registros vía CRUD sin login), un `label` o `entity_id` con `"><img src=x onerror=...>` ejecuta JavaScript en la sesión de quien abra `/admin/modules` o `/admin/audit` — típicamente un admin.

**Arreglo:** reusar el `esc()` de `fast-views.js` en estas cuatro plantillas; cambiar los `onclick="fn('${id}')"` inline por `data-id` + `addEventListener` para no depender de escapar correctamente dentro de un atributo.

### 🟡 Medio 5 — Tokens JWT en `localStorage`

`core/templates/login.html:236-241` guarda `access_token`/`refresh_token` en `localStorage` (se leen de vuelta en `layout.html`, `audit.html`, `dashboard.html`, `module-store.html`, `fast-views.js`). Cualquier XSS (como el #4) puede robarlos directamente con `localStorage.getItem(...)`, sin necesitar cookies `httpOnly`. `SessionManager.SetSessionCookie` (`session.go:108`) sí crea cookies `HttpOnly`+`SameSite=Lax` en paralelo — pero el frontend no las usa, usa `localStorage`.

**Arreglo:** que el frontend deje de leer/escribir el token en `localStorage` y confíe solo en la cookie `HttpOnly` que el propio backend ya emite.

### 🟡 Medio 6 — Sin rate limiting ni protección contra fuerza bruta en `/api/auth/login`

`core/main.go` registra `/api/auth/login` directo en el mux, sin el `rateLimiterMiddleware` que sí existe en `internal/api/router.go:31` para el otro binario. Combinado con la credencial por defecto documentada en el propio `README.md` (`admin` / `admin123`) y el hecho de que `core/handlers/auth.go:88-96` responde distinto según exista o no el usuario recién antes de correr `bcrypt` (diferencia de tiempo medible → permite enumerar usuarios válidos), un despliegue que no cambie la contraseña por defecto es vulnerable a fuerza bruta sin fricción.

**Arreglo:** envolver `/api/auth/login` con un rate limiter por IP (ya existe el middleware equivalente en el otro binario, es cuestión de portarlo), y forzar el cambio de la contraseña `admin123` en el primer login.

### ⚪ Informativo 7 — Sin cabeceras de seguridad (CSP, X-Frame-Options, etc.)

No hay ningún middleware que agregue `Content-Security-Policy`, `X-Content-Type-Options` o `X-Frame-Options` en `core/main.go`/`core/middleware/` (que solo tiene `logging.go`). Mitigaría el impacto del #4 aunque no lo reemplaza. Cualquier CSP futura va a necesitar nonces o mover a archivos externos los `<script>` inline que hoy bootstrean el tema en cada plantilla (`layout.html:20-52` y repetido en varias más).

### ⚪ Informativo 8 — `htmx` cargado desde CDN sin integridad (SRI)

`core/templates/base.html:7` carga `htmx@1.9.10` desde `unpkg.com` sin hash de integridad — pero ese archivo (`base.html`, y `audit-dashboard.html`) no está referenciado desde ningún handler de `core/main.go` ni `core/handlers/`; parece código muerto. Si se reconecta algún día, agregar `integrity=`.

---

## Hallazgos — servidor `cmd/server` (Docker, la implementación más sólida)

Este binario está considerablemente mejor diseñado: `AuthMiddleware`/`TenantMiddleware`/`AdminMiddleware` (`internal/api/handler.go:249,626,673`) están bien testeados (`internal/api/handler_test.go`), el pool de conexiones se pinea por tenant con `SET app.tenant_id` (`internal/db/db.go:72-83`) y las tablas usan `FORCE ROW LEVEL SECURITY` con una policy que cae a cero filas si la conexión nunca fijó el tenant (`internal/db/db.go:94-112`). Confirmé además que la advertencia sobre el rol de base de datos superusuario que estaba pendiente en una nota anterior **ya está resuelta**: `docker-compose.yml`/`docker/init-db.sh` crean un rol `fasterp_app` sin `BYPASSRLS` y `.env` documenta que el superusuario "la app NO la usa".

### 🟠 Alto 9 — `docker-compose.yml` no puede construir: apunta a directorios que ya no existen

```yaml
backend:
  build:
    context: ./backend      # no existe — el código vive en ./core
frontend:
  build:
    context: ./frontend     # no existe
```

Confirmado con `docker compose config` (falla al resolver el build context) y con `ls`: no hay `backend/` ni `frontend/` en el repo, solo `core/`. Es decir: el camino "de producción" que describe `CLAUDE.md` y que tiene mejor arquitectura **no se puede levantar tal como está** — hay que corregir el `context:` a `./core` (y el `dockerfile:` sigue siendo válido, `core/Dockerfile` existe y compila `./cmd/server`).

**Impacto indirecto pero real:** mientras esto esté roto, cualquiera que siga el "Quick Start" de `README.md` o `CLAUDE.md` termina en `make run` (el binario del Crítico 1/2) creyendo que tiene el mismo nivel de seguridad que "producción".

### 🟡 Medio 10 — Sitio público (`sitio_web`) sirve HTML/CSS/JS del tenant sin escapar, mismo origen que la app autenticada

`internal/api/web_handler.go:120-134` (`renderDocument`): solo el `<title>` se escapa; `p.HTML`, `p.CSS` y `p.JS` —contenido que cualquier usuario autenticado del tenant con acceso al módulo `sitio_web` puede editar vía el CRUD normal— se insertan tal cual en la página pública servida en `GET /site` y `GET /site/:slug`, **sin autenticación**, en el mismo origen (mismo host:puerto) que el resto de la aplicación. Esto es una decisión de diseño reconocida en el propio `CLAUDE.md` ("Security note: User-authored JS is served same-origin as the app"), no un descuido — pero conviene tenerlo explícito acá: cualquier usuario con permiso de editar páginas del sitio tiene, de hecho, ejecución de JS arbitraria en el origen de la app.

**Arreglo, tal como ya sugiere `CLAUDE.md`:** servir el sitio público en un subdominio u origen distinto del panel de administración, para que ese JS no comparta cookies/`localStorage` con la sesión autenticada.

### 🟠 Alto 11 — Credenciales de Odoo en texto plano, legibles por cualquier usuario autenticado del tenant (no solo admins)

`modules/integracion_odoo_fasterp/main.go:70` y `modules/odoo_sync/main.go:62-63` declaran `odoo_password`/`api_key` como un campo `string`/`text` más dentro de su `Models` — el mismo array que exporta `fasterp_get_manifest` y que `internal/module/wasm.go:245-258` usa para poblar `inst.Models` en tiempo de ejecución (esto pasa para **todos** los módulos, no solo los que declaran `models` en su `manifest.json` estático — esa es la fuente que lee `LoadManifests()` para generar Swagger, pero el dispatcher de CRUD usa el manifest que exporta el `.wasm`, que sí trae `models` para los 8 módulos "legacy"). Confirmé el camino completo: `internal/api/data.go:59` despacha por nombre de modelo contra `inst.Models`, y `listHandler`/`getHandler` (`data.go:69-118`) usan `module.NewQueryBuilder(m).BuildSelect()`, que por diseño selecciona **todos los campos** del modelo (no hay ningún tipo de campo "secreto" ni redacción en `internal/module/querybuilder.go`).

La ruta que expone esto (`internal/api/router.go:39,62`, `h.RegisterModuleDataRoutes(api)`) solo pasa por `TenantMiddleware`+`AuthMiddleware` — nunca por `AdminMiddleware`. Es decir: **`GET /api/integracion_odoo_fasterp/odoo_connection` devuelve el password/token de Odoo en texto plano a cualquier usuario logueado del tenant**, no solo a un admin, y ese mismo usuario puede además hacer `PUT`/`POST`/`DELETE` sobre esa conexión (re-apuntarla a un servidor Odoo propio para exfiltrar datos, por ejemplo).

**Arreglo:** agregar un tipo de campo "secreto" al `FieldDef` (write-only: se acepta en `create`/`update` pero se excluye del `SELECT` de `list`/`get`, y se cifra en reposo), y/o exigir `AdminMiddleware` para cualquier modelo que declare un campo de ese tipo.

### ⚪ Informativo 12 — SVG permitido en subida de medios del CMS

`internal/api/web_handler.go:28` incluye `.svg` en `allowedMediaExt`. Un SVG puede contener `<script>` embebido; servido directamente (no como `<img>`) desde `/uploads/media/`, un navegador lo ejecuta como documento HTML. El nombre de archivo es un UUID (no adivinable) y la subida ya requiere estar autenticado en el tenant, así que el impacto real es bajo, pero es una superficie de XSS clásica y evitable.

**Arreglo:** o bien quitar `.svg` de la lista, o servir esos archivos con `Content-Disposition: attachment` / sanitizar el SVG al subirlo.

---

## Lo que está bien (confirmado leyendo el código, no solo el manifest)

- **`internal/module/querybuilder.go`** (usado por el CRUD genérico de `cmd/server`): todo identificador (tabla, columna, dirección de orden) se valida contra una whitelist antes de interpolarse en SQL; todos los valores van parametrizados. Los tests (`querybuilder_test.go`) cubren explícitamente intentos de inyección en `WHERE`/`ORDER BY`.
- **`internal/store/service.go`** y **`internal/web/service.go`** (tienda_web, sitio_web — lógica nativa fuera del sandbox WASM): cada consulta escopa por `tenant_id` parametrizado, sin excepciones; no hay SQL armado con `fmt.Sprintf` sobre datos de usuario, solo sobre nombres de tabla constantes.
- **Subida de imágenes** (`internal/api/store_handler.go`, `web_handler.go`): nombre de archivo siempre `uuid.New()+ext` (no hay traversal posible vía nombre de archivo del cliente), whitelist de extensiones, límite de 8 MB.
- **`core/sdk/`** (motor `@fast` para módulos declarativos como `contacts`/`automatizaciones`): parametrizado y escopado por tenant en cada `Create/Read/Update/Delete/List` (confirmado por agente de revisión + tests existentes).
- **RLS en `cmd/server`**: `FORCE ROW LEVEL SECURITY` + rol de conexión sin `BYPASSRLS`, con warning en el arranque (`CheckRLSEnforcement`, `cmd/server/main.go:48`) si algún día se conecta como superusuario por error.

---

## Recomendaciones priorizadas

1. **Decidir cuál de los dos binarios es "el" servidor de FastERP** y retirar o poner detrás de un flag explícito de "solo desarrollo local, nunca exponer" al que no se adopte. Mientras convivan los dos, cualquier instrucción de "quick start" es una trampa de seguridad.
2. Si `core/main.go` sigue vivo: arreglar los Críticos 1 y 2 antes que cualquier otra cosa — son bypass completo de autenticación.
3. Arreglar el `context:` de `docker-compose.yml` para que el binario mejor diseñado sea el que de verdad se pueda desplegar con un comando.
4. Escapar las cuatro plantillas del Alto 4; es un cambio mecánico y acotado.
5. Agregar tests de integración contra una Postgres real (`docker compose up -d postgres`) para `middleware/`, `internal/db/`, `internal/store/`, `internal/web/` — hoy es el código con más superficie de ataque y cero cobertura automatizada.
