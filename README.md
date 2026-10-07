# FastERP

[![CI](https://github.com/Gabriel3002-maker/FastERP/actions/workflows/ci.yml/badge.svg)](https://github.com/Gabriel3002-maker/FastERP/actions/workflows/ci.yml)

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
<http://localhost:7071>.

En desarrollo, si no hay `FASTERP_ADMIN_PASSWORD`, `run.sh` usa `admin123` y lo
avisa por consola. **Fuera de `run.sh` no hay contraseña por defecto**: si no
existen usuarios y falta `FASTERP_ADMIN_PASSWORD` (mínimo 12 caracteres), el
servidor no arranca a propósito. El primer usuario también se puede crear desde
`/setup` con `FASTERP_SEED=false`.

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
│   │   ├── module/        # manifest, esquema, RLS, query builder
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
GET  /health             vivo, sin tocar la BD
GET  /readyz             vivo y con la BD y los roles en condiciones
GET  /metrics            métricas Prometheus, sin autenticación
POST /api/auth/login
POST /api/auth/refresh
```

Con sesión (JWT + `X-Tenant-ID`):

```
GET    /api/me
GET    /api/modules
GET    /api/menus
POST   /api/me/change-password
POST   /api/me/logout
GET    /api/{module}/{model}
POST   /api/{module}/{model}
GET    /api/{module}/{model}/{id}
PUT    /api/{module}/{model}/{id}
DELETE /api/{module}/{model}/{id}
```

Administración, con sesión y rol administrador (`/api/admin/*`):

```
GET    /api/admin/tenants
GET    /api/admin/tenants/:id
GET    /api/admin/tenants/:id/backup
POST   /api/admin/tenants/:id/restore
GET    /api/admin/users
POST   /api/admin/users
GET    /api/admin/users/:id
PUT    /api/admin/users/:id
POST   /api/admin/users/:id/password
POST   /api/admin/users/:id/toggle
GET    /api/admin/users/:id/roles
PUT    /api/admin/users/:id/roles
GET    /api/admin/roles
POST   /api/admin/roles
GET    /api/admin/roles/:id
PUT    /api/admin/roles/:id
DELETE /api/admin/roles/:id
GET    /api/admin/roles/:id/permissions
PUT    /api/admin/roles/:id/permissions
GET    /api/admin/permissions/catalog
POST   /api/admin/modules/install
POST   /api/admin/modules/:name/uninstall
POST   /api/admin/modules/:name/toggle
GET    /api/admin/modules/:name/routes
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

## Permisos y roles

El CRUD de módulos exige una acción por recurso (`create`, `read`, `update`,
`delete`) sobre un modelo. `is_admin` salta siempre el control; el resto decide
la precedencia completa:

1. lo **directo** gana al rol: un `allow` del usuario en `user_permissions`
   abre, un `deny` cierra, pese a lo que diga el rol;
2. si ningún rol del usuario concede la acción, el rol se descarta;
3. si el tenant tiene **algun rol asignado**, lo no concedido está prohibido
   (_default-deny_): el usuario sin rol no entra;
4. sin roles se conserva el comportamiento histórico: el recurso sin filas
   directas deja pasar todo, y el recurso con filas solo lo declarado.

Al asignar el **primer rol** de un tenant, ese tenant pasa a modo gestionado
para todos sus usuarios; a partir de ahí un usuario nuevo no tiene acceso a
nada hasta que reciba un rol o un permiso directo.

La pantalla `/admin/roles` gestiona roles, sus permisos (contra el catálogo
real de módulos instalados) y la asignación a usuarios; detrás está
`/api/admin/roles*` y `/api/admin/permissions/catalog`. El menú lateral se
filtra por lo que el usuario puede leer: un módulo sin `read` desaparece junto
con los apartados que quedan vacíos.

## Configuración

Todo por variables de entorno. Las lee `core/internal/config`:

| Variable | Por defecto | Para qué |
|---|---|---|
| `FASTERP_PORT` / `FASTERP_BIND_PORT` | 7071 | Puerto HTTP |
| `FASTERP_DATABASE_URL` | `…@localhost:5456/…` | Cadena de conexión a Postgres |
| `FASTERP_JWT_SECRET` | *(sin default usable)* | Firma de access tokens |
| `FASTERP_JWT_REFRESH_SECRET` | *(sin default usable)* | Firma de refresh tokens |
| `FASTERP_MODULES_DIR` | `./modules` | Dónde busca los módulos (`:` como separador) |
| `FASTERP_UPLOAD_DIR` | `./uploads` | Dónde guarda las subidas |
| `FASTERP_CORS_ORIGINS` | `http://localhost:5051` | Orígenes permitidos, separados por coma |
| `FASTERP_RATE_LIMIT` | 100 | Límite global de peticiones |
| `FASTERP_AUTH_RATE_LIMIT` | 10 | Límite de `/api/auth/*`, más estricto |
| `FASTERP_BACKUP_RATE_LIMIT` | 5 | Límite de `/api/admin/backup*` |
| `FASTERP_DB_MAX_OPEN` / `FASTERP_DB_MAX_IDLE` | 50 / 10 | Tamaño del pool |
| `FASTERP_SEED` | `true` | Crear el tenant `default` y el primer usuario |
| `FASTERP_ADMIN_PASSWORD` | *(vacío)* | Contraseña del primer usuario. Obligatoria con `SEED=true`, mínimo 12 caracteres |
| `FASTERP_DEV` | `false` | Salta el chequeo de secretos débiles y de RLS. **Nunca en producción** |
| `FASTERP_TRUSTED_PROXIES` | *(vacío)* | IPs/proxies de confianza para `X-Forwarded-For`. Ver más abajo |
| `FASTERP_STATIC_DIR` / `FASTERP_TEMPLATES_DIR` | `./static` / `./templates` | Rutas de los assets y de la UI server-rendered |

`docker-compose.yml` no pone valores por defecto a `FASTERP_JWT_SECRET`,
`FASTERP_JWT_REFRESH_SECRET`, `FASTERP_CORS_ORIGINS` ni a
`FASTERP_APP_PASSWORD` (de la que se construye `FASTERP_DATABASE_URL`) a
propósito: un secreto por defecto en el repo permite forjar tokens de admin sin
credenciales. El arranque falla con un mensaje que dice qué falta. Defínelos en
`.env`.

`FASTERP_TRUSTED_PROXIES` vacío significa **no confiar en ningún proxy**: la IP
que usa el rate limit es la del peer, y un `X-Forwarded-For` que escriba el
cliente se ignora. Pon aquí los CIDRs de tu reverse proxy; si lo dejas vacío
detrás de uno, todas las peticiones cuentan con la IP del proxy y el límite
global se aplica a todo el mundo a la vez.

## Base de datos

Postgres 16+. `docker/init-db.sh` crea `fasterp_app` con `NOSUPERUSER` y
`NOBYPASSRLS` en el primer arranque del volumen.

**Conéctate con ese rol, no con el superusuario.** Un superusuario ignora las
políticas RLS, y entonces el aislamiento entre tenants pasa a depender solo de
los `WHERE tenant_id` de cada consulta. El servidor avisa al arrancar si el rol
puede saltarse RLS.

El esquema de los módulos se materializa al arrancar, a partir del manifest.
Las tablas se llaman `mod_<module>_<model>`.

### Migraciones

El esquema del **core** se define con migraciones versionadas en
`core/internal/migrations/sql/`: un fichero `NNNN_nombre.sql` por versión, en
orden numérico, cada una en su propia transacción y registrada en
`schema_migrations`. Se aplican solas en cada arranque y son idempotentes.

- **Para cambiar una tabla del core** (`users`, `tenants`, `contacts`, …):
  añade `0002_*.sql`, `0003_*.sql`, … con el `ALTER` concreto. No edites los
  ficheros ya aplicados.
- **Para cambiar una tabla de un módulo**: no toques migraciones. Ese esquema
  lo reconcilia `internal/module` desde el manifest.
- `models.AutoMigrate` ya no crea tablas: aplica las migraciones y reafirma el
  RLS por tenant en cada arranque.

`schema_migrations` guarda `version`, `name` y `applied_at`. Una migración que
falla deshace su transacción y el proceso no arranca.

## Módulos

Un módulo es **declarativo**: un manifest (`module.yaml`, o `manifest.json`)
con modelos, campos, permisos y menús, más un `frontend/` opcional. El core crea
las tablas, valida el input y expone el CRUD; no hay que escribir SQL ni
handlers.

Se instala como **zip** desde `POST /api/modules/install` (rol admin). El zip
se valida antes de tocar el disco y se rechaza cualquier binario
(`.wasm`, `.so`, `.dll`, `.dylib`): un módulo es un manifest, no código que se
ejecuta dentro del servidor. Las integraciones externas (correo, pagos, APIs de
terceros) van en Go nativo dentro de `core/internal/`.

Ver [FastERP-modules](https://github.com/Gabriel3002-maker/FastERP-modules)
para el catálogo de módulos y cómo escribir uno nuevo.

## Desarrollo

```bash
cd core
go build ./...
go vet ./...
go test ./...
```

Los tests de RLS y de esquema hablan con Postgres de verdad y se saltan si no
hay base de datos de pruebas. Para ejecutarlos:

```bash
createdb fasterp_test
cd core
FASTERP_TEST_DATABASE_URL="postgres://usuario:clave@localhost:5432/fasterp_test?sslmode=disable" go test ./...
```

Usa un rol **sin** `SUPERUSER` ni `BYPASSRLS` y que no sea dueño de las tablas
que se creen: los tests verifican justo eso, y con un superusuario pasarían
sin comprobar nada.

El rol necesita además `CREATEDB`: el test de migraciones crea y borra su
propia base para no pisar a los tests de módulos, que corren en paralelo.

Antes de mandar nada:

```bash
gofmt -l core    # debe salir vacío
cd core && go build ./... && go vet ./... && go test -race ./...
```

### CI

`.github/workflows/ci.yml` corre en cada push y en cada PR, con dos trabajos en
paralelo: `gofmt` + `go build` + `go vet`, y `go test -race` contra un
Postgres 16 efímero con `FASTERP_TEST_DATABASE_URL` ya apuntando a él.

Ese contenedor arranca con un rol de pruebas creado por
`docker/ci-test-role.sql` (`NOSUPERUSER`, `NOBYPASSRLS`, `CREATEDB`), y un paso
del workflow falla si algún test de RLS se salta por privilegios del rol: con
el superusuario saltarían en vez de fallar, y un verde que no mide nada es peor
que un rojo.

El checkout **no** inicializa el submódulo `modules/` (es privado y el workflow
no lleva credenciales): las pruebas que buscan el repositorio real se saltan
solas.

En VSCode: `Ctrl+Shift+D` → "Core: API (Gin)" para depurar.

## Docker

```bash
docker compose up --build -d
docker compose down
```

Levanta Postgres y el core. La UI server-rendered va incluida en la imagen.

## Estado

Verificado en `core/`: `go build`, `go vet` y `go test -race` en verde contra
Postgres real, con `FASTERP_TEST_DATABASE_URL` apuntando a una base de pruebas
con un rol `NOSUPERUSER` / `NOBYPASSRLS`. Hay CI en cada push y en cada PR.

Lo que está en curso y conviene tener presente:

- **RBAC.** Roles + permisos por usuario (`allow`/deny) sobre el CRUD genérico,
  con default-deny al primer rol asignado e `is_admin` como bypass. Pantalla y
  API en `/admin/roles` y `/api/admin/roles*`; el menú lateral refleja lo que el
  usuario puede leer. Ver "Permisos y roles".
- **Métricas.** `/metrics` expone peticiones por ruta/método/estado, su
  duración y el runtime de Go, y el access log escribe una línea por petición
  con tenant y usuario. Falta decidir dónde se raspa (Grafana, Prometheus) y
  qué alertas merecen la pena.
