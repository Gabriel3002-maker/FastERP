# CLAUDE.md

Guía de trabajo de este repositorio. Describe el estado real del código, no el
deseado: si algo aquí no coincide con el código, el código manda.

## Qué es este repo

Solo el **core** de FastERP. Los módulos están en
`Gabriel3002-maker/FastERP-modules` y entran por `modules/` como submódulo.

```bash
git clone --recurse-submodules <url>
git submodule update --init --recursive   # si ya lo clonaste sin --recurse
```

Al tocar un módulo, el cambio va en el submódulo: commitea dentro de `modules/`
y luego actualiza el puntero en el repo principal.

## Comandos

```bash
./run.sh                       # Postgres + core en :7071
cd core && go test ./...       # tests
cd core && go vet ./...
gofmt -l core                  # debe salir vacío; la CI lo comprueba
```

Hay CI en `.github/workflows/ci.yml`: `gofmt` + `build` + `vet` en paralelo con
`go test -race` contra un Postgres efímero. No inicializa el submódulo `modules/`
(privado, sin credenciales): las pruebas que lo buscan se saltan solas.

En VSCode: "Core: API (Gin)" como entry point único (API + UI).

## Arquitectura

### Una sola capa HTTP

El core es Gin (`core/cmd/server`): API con JWT + refresh, RLS por tenant, y la
UI admin (HTMX) desde `core/templates/`. **Nunca añadas rutas al `gin.Engine`
en runtime**: el CRUD de módulos se registra una vez al arrancar.

La antigua capa legacy (`core/main.go`, `core/handlers/`, `core/sdk/`,
`core/db/`, `core/config/`, `core/middleware/`, `core/utils/`) se eliminó del
repo: compartir schema por ahí ya no es una opción y se perdía tiempo
manteniendo dos mundos. Todo lo vivo está en `core/internal/` y `core/cmd/`.


### Multi-tenant

El aislamiento tiene dos capas y ambas importan:

1. **RLS en Postgres.** `TenantMiddleware` resuelve el tenant, verifica que esté
   activo y fija `app.tenant_id` en la conexión.
2. **Filtro explícito.** Los handlers usan `db.Executor(c)`, que devuelve la
   conexión con el tenant fijado.

`db.DB` es el pool compartido sin contexto de tenant. Se reserva para el
arranque y para rutas públicas que resuelven su propio tenant. **Usa
`db.Executor(c)` en cualquier handler**, o el RLS no aplica y el `tenant_id`
se cuela de otro.

Conéctate con un rol sin `BYPASSRLS` (ver `docker/init-db.sh`). Con superusuario
las políticas no se aplican y el aislamiento queda en manos de los `WHERE`.

### RBAC

La autorización del CRUD vive en `internal/api/handler.go`:
`requirePermission` → `permissionVerdict`, una sola consulta que rellena cinco
hechos por petición — `managed` (¿el tenant tiene algún rol asignado?),
`directGrant`, `directDeny`, `roleGranted`, `resourceTracked` — y `allow()`
aplica en Go la precedencia entera: **directo > rol > default-deny > modo
compatible**.

- **Directo.** `user_permissions` con `allow` otorga; sin `allow` revoca. Gana
  a cualquier rol.
- **Rol.** `roles` + `role_permissions`; concede la acción exacta
  (module, model + `create/read/update/delete`) o, con `reg` nil, cualquier
  permiso del módulo (nivel de módulo).
- **Default-deny.** En cuanto el tenant tiene **un** `user_roles`, lo no
  concedido está prohibido para todos sus usuarios; el usuario sin rol no entra.
- **Modo compatible.** Sin roles, el recurso sin filas directas deja pasar todo
  y el de filas solo lo declarado.

`is_admin` salta el chequeo, nada más. Hay dos tests que prueban lo mismo desde
los dos lados: la matriz de `allow()` (`rbac_test.go`) y el veredicto contra
Postgres con RLS (`rbac_db_test.go`).

Los manejadores de roles y su asignación viven en `internal/api/roles.go`, bajo
`/api/admin/roles*`, `/api/admin/users/:id/roles` y
`/api/admin/permissions/catalog` (catálogo real de módulos activos);
`templates/roles-content.html` es la pantalla. Validaciones: nombre de rol
`^[\p{L}0-9 _.-]{1,100}$` (tildes incluidas), descripción ≤ 500 caracteres,
borrado child-first en una transacción y `Replace*` que validan el catálogo y
deduplican. Toda esta familia va bajo el prefijo `/api/admin` a propósito:
la API sin prefijo se reserva a los datos de los módulos (`/api/:module/:model`).

### La capa de datos

No hay ORM aparte: `core/internal/module` es el que hace de ORM, y va dirigido
por manifest. Los modelos, campos y tipos salen del manifest del módulo
(`spec.go`), `schema.go` los convierte a DDL, `querybuilder.go` monta el SQL y
`internal/api` valida en el handler. No escribas SQL a mano para CRUD.

La referencia de tipos es `columnType()` en `internal/module/schema.go` — la
lista de alias vive ahí y no se duplica aquí: si no coincide, manda ese fichero.

`many2one` es una columna UUID. Al declararla, rellena `related_module`,
`related_model` y `related_field` para que la API devuelva el dato relacionado
en vez del UUID crudo. `many2many` vive en su tabla asociativa y `one2many` es
la inversa de un `many2one`.

### Esquema del core y migraciones

Hay dos esquemas y se confunden fácil:

| | Quién lo define | Dónde |
|---|---|---|
| Tablas del core (`users`, `tenants`, `contacts`, …) | migraciones versionadas | `core/internal/migrations/sql/NNNN_*.sql` |
| Tablas de módulos (`mod_<module>_<model>`) | el manifest | reconciliado por `internal/module` |

Para cambiar una tabla del core añade `0002_*.sql`, `0003_*.sql`, … con el
`ALTER` concreto. **Nunca edites un fichero ya aplicado**: está registrado en
`schema_migrations` y se daría por hecho sin reejecutarse. El baseline
`0001_core_schema.sql` es idempotente a propósito, para que una base existente
lo aplique como una operación nula.

`models.AutoMigrate()` ya no crea tablas: llama a `migrations.Up()` y después
reafirma el RLS por tenant en cada arranque. Cada migración corre en su
transacción con el registro de versión como última sentencia, así que fallar
deshace todo y el proceso no arranca.

### Módulos

Un módulo es **declarativo**: `module.yaml` (o `manifest.json`) + `frontend/`
opcional + `assets/`. El manifest es la fuente de verdad del esquema; no hay
código de módulo que compilar.

Se empaqueta como zip y se instala con `POST /api/modules/install` (admin).
`ModuleManager.ExtractModulePackage` valida el zip **antes** de escribir nada en
disco: manifest inválido o presupuesto de descompresión excedido no dejan
ficheros sueltos, y cualquier binario (`.wasm`, `.so`, `.dll`, `.dylib`) se
rechaza con "un módulo es un manifest, no un binario". No reintroduzcas un
runtime de módulos: `plugin.Open()` sería ejecutar código arbitrario con los
permisos del servidor.

Restricciones que siguen vigentes:

- **No declares `tenant_id`.** Lo pone el core, y la política RLS se deriva de él.
- Los nombres de tabla se derivan: `mod_<module>_<model>`.
- El manifest no puede contener SQL: `default` es el único campo que se
  concatena al DDL, y pasa por el vocabulario cerrado de `sqlLiteral()`. Todo lo
  demás pasa por la whitelist de identificadores de `querybuilder.go`.
- Integraciones externas (correo, pagos, HMRC) van en Go nativo en
  `core/internal/`, no en el módulo.

## Antes de dar por terminado un cambio

```bash
cd core && go build ./... && go vet ./... && go test ./...
gofmt -l core
```

Si tocas RLS, esquema o el query builder, corre además los tests de base de
datos. Se saltan sin `FASTERP_TEST_DATABASE_URL`, y saltarse esos tests es
cómo se pudrieron los de `search_db_test.go`:

```bash
FASTERP_TEST_DATABASE_URL="postgres://user:pass@localhost:5432/fasterp_test?sslmode=disable" \
  go test -race ./...
```

El rol de pruebas tiene que ser **sin** `SUPERUSER` ni `BYPASSRLS`. Un
superusuario salta el RLS y los tests de aislamiento pasarían sin comprobar
nada.

## Contexto de seguridad

Tres cosas que parecen aceptables y no lo son:

- **`is_admin` no es autorización.** El CRUD genérico filtra por acción
  (create/read/update/delete) con la precedencia completa de
  `requirePermission` → `permissionVerdict`; `is_admin` solo salta el chequeo.
  El detalle de la precedencia y del default-deny está en "RBAC" más arriba.
  Un permiso mal dado es tan fácil como no dar el que corresponde.
- **Los secretos vienen del entorno, nunca de defaults.** `docker-compose.yml`
  falla a propósito si faltan `FASTERP_JWT_SECRET`, `FASTERP_JWT_REFRESH_SECRET`,
  `FASTERP_CORS_ORIGINS` o `FASTERP_APP_PASSWORD`. `checkSecrets()` bloquea el
  arranque con secretos débiles salvo con `FASTERP_DEV=true`. No reintroduzcas
  valores por defecto para que "levante más fácil": un secreto de admin
  publicado permite forjar tokens.
- **Conéctate con `fasterp_app`, no con un superusuario.** Un superusuario
  ignora el RLS incluso con `FORCE`, y el aislamiento queda en manos de los
  `WHERE tenant_id` de cada consulta. `CheckRLSEnforcement` lo advierte al
  arrancar.

## Pendientes conocidos

- **Sin dashboard de métricas.** `/metrics` expone las series y el access log
  escribe una línea por petición, pero falta decidir dónde se raspa y qué
  alertas merecen la pena.
- **`/metrics` es pública.** A propósito, para que el scraper no lleve JWT. El
  `docker-compose.yml` solo publica el puerto en `127.0.0.1`; si expones el core
  a Internet, filtra esa ruta en el reverse proxy.
