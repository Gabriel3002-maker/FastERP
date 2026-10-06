# FastERP

ERP multi-tenant en Go. El core es una plataforma: define el esquema, la
seguridad y el CRUD, y los módulos aportan sus datos sin tocar el core.

Los módulos viven en **[FastERP-modules](https://github.com/Gabriel3002-maker/FastERP-modules)**
y entran aquí como submódulo.

## Arranque rápido

```bash
git clone --recurse-submodules https://github.com/Gabriel3002-maker/FastERP.git
cd fasterp
./run.sh
```

`run.sh` levanta PostgreSQL por Docker, compila el core y lo arranca en
<http://localhost:7071>. Login inicial: `admin` / `admin123`.

Sin submódulos:

```bash
git submodule update --init --recursive
```

## Estructura

```
fasterp/
├── core/                  # el core (todo el Go)
│   ├── cmd/server/        # entry point: API Gin + UI
│   ├── internal/
│   │   ├── api/           # rutas Gin, auth, middleware
│   │   ├── module/        # carga de WASM, migración de esquema
│   │   ├── db/            # pool, RLS, executor por tenant
│   │   └── web/           # sitio público
│   └── templates/  static/
├── modules/               # submódulo → FastERP-modules
├── docker/                # init de Postgres y roles
└── run.sh
```

## Capa HTTP única

El core sirve todo con Gin (`core/cmd/server`): API con JWT + refresh tokens,
RLS por tenant, admin HTMX desde `templates/`, y CRUD de módulos.
La capa legacy (`core/main.go`, `handlers/`, `sdk/`) se eliminó: RLS y RBAC
viven solo en `internal/`.


## API

Sin autenticación:

```
GET  /health
POST /api/auth/login
POST /api/auth/refresh
```

Con sesión (JWT + `X-Tenant-ID`):

```
GET    /api/me
GET    /api/modules
POST   /api/modules/install
GET    /api/menus
GET    /api/{module}/{model}
POST   /api/{module}/{model}
GET    /api/{module}/{model}/{id}
PUT    /api/{module}/{model}/{id}
DELETE /api/{module}/{model}/{id}
```

Públicas, sin sesión:

```
GET /site                 página de inicio del sitio
GET /site/:slug           página del sitio
GET /api/public/products  catálogo publicado
```

Tienda (`tienda_web`):

```
GET    /api/store/products
GET    /api/store/products/:id
PUT    /api/store/products/:id
POST   /api/store/products/:id/images
DELETE /api/store/images/:id
```

## Configuración

Todo por variables de entorno. Las lee `core/internal/config`:

| Variable | Para qué |
|---|---|
| `FASTERP_PORT` | Puerto HTTP (7071) |
| `FASTERP_DATABASE_URL` | Cadena de conexión a Postgres |
| `FASTERP_JWT_SECRET` | Firma de access tokens |
| `FASTERP_JWT_REFRESH_SECRET` | Firma de refresh tokens |
| `FASTERP_MODULES_DIR` | Dónde busca los módulos |
| `FASTERP_UPLOAD_DIR` | Dónde guard las subidas |
| `FASTERP_CORS_ORIGINS` | Orígenes permitidos, separados por coma |
| `FASTERP_RATE_LIMIT` | Límite global de peticiones |
| `FASTERP_AUTH_RATE_LIMIT` | Límite de `/api/auth/*`, más estricto |
| `FASTERP_DB_MAX_OPEN` / `FASTERP_DB_MAX_IDLE` | Tamaño del pool |

`docker-compose.yml` no pone valores por defecto a `FASTERP_JWT_SECRET` ni a
`FASTERP_DATABASE_URL` a propósito: un secreto por defecto en el repo permite
forjar tokens de admin sin credenciales. Defínelos en `.env`.

## Base de datos

Postgres 16+. `docker/init-db.sh` crea `fasterp_app` con `NOSUPERUSER` y
`NOBYPASSRLS` en el primer arranque del volumen.

**Conéctate con ese rol, no con el superusuario.** Un superusuario ignora las
políticas RLS, y entonces el aislamiento entre tenants pasa a depender solo de
los `WHERE tenant_id` de cada consulta. El servidor avisa al arrancar si el rol
puede saltarse RLS.

El esquema de los módulos se materializa al arrancar, a partir del manifest que
expone cada WASM. Las tablas se llaman `mod_<module>_<model>`.

## Módulos

Un módulo es `manifest.json` + `main.go` (compilado a `module.wasm`) + `frontend/`
opcional. El core crea las tablas y expone el CRUD; no hay que escribir SQL ni
handlers.

El sandbox WASI no tiene red ni sistema de archivos, así que cualquier
integración externa (correo, pagos, HMRC) va en Go nativo dentro del core.

Ver [FastERP-modules](https://github.com/Gabriel3002-maker/FastERP-modules)
para el catálogo de módulos y cómo escribir uno nuevo.

## Desarrollo

```bash
cd core
go build ./...
go vet ./...
go test ./...
```

En VSCode: `Ctrl+Shift+D` → "Core: API (Gin)" para depurar. La tarea "Módulos: recompilar
WASM" recompila todos los módulos con `main.go`.

## Docker

```bash
docker compose up --build -d
docker compose down
```

Levanta Postgres y el core. La UI server-rendered va incluida en la imagen.

## Estado

Lo que está verificado: `go build`, `go vet` y `go test` pasan en `core/`.

Lo que está en curso y conviene tener presente:

- **RBAC.** Implementado de forma básica: tabla `user_permissions` y chequeo por
  acción (create/read/update/delete) en el CRUD genérico. `is_admin` salta el
  control. Ampliar a roles sigue siendo trabajo futuro.
