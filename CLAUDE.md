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
```

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

### SDK

`core/sdk` es el ORM. Se dirige por manifest: los modelos, campos y tipos salen
del manifest del módulo, y el SDK valida, convierte y genera el SQL. No
escribas SQL a mano para CRUD.

Tipos disponibles: `string`, `text`, `integer`, `bigint`, `decimal`, `float`,
`boolean`, `date`, `datetime`, `json`, `enum`, `uuid`, `uuid[]`, `many2one`,
`one2many`, `many2many`. Alias: `m2o`, `o2m`, `m2m`, `selection`, `int`, `bool`.

`many2one` es una columna UUID. Al declararla, rellena `related_module`,
`related_model` y `related_field` para que la API devuelva el dato relacionado
en vez del UUID crudo.

### Módulos

Un módulo = `manifest.json` + `main.go` → `module.wasm` + `frontend/`.

**El WASM es la fuente de verdad del esquema.** El core llama a
`fasterp_get_manifest` y crea las tablas desde ahí. `manifest.json` cubre
metadata, permisos, `depends` y páginas de frontend. Si cambias modelos,
cambia `main.go` y recompila:

```bash
cd modules/mi_modulo
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o module.wasm main.go
```

**`-buildmode=c-shared` no es opcional.** El core instancia el módulo con
`WithStartFunctions("_initialize")`. Un build normal de Go produce un módulo
*command*, que exporta `_start` en su lugar: compila, aparece el `.wasm`, y al
arrancar el core falla con `wasm error: unreachable` sin más pista. El build
reactor es el único que exporta `_initialize`. Para comprobarlo sin levantar el
core, mira los exports del binario: deben aparecer `_initialize`,
`fasterp_get_manifest`, `fasterp_on_load` y `fasterp_on_unload`.

`module.wasm` se versiona. Si subes `main.go` sin el `.wasm`, el core sigue
sirviendo el esquema viejo — es la forma más fácil de romper un módulo sin
darse cuenta.

Restricciones del sandbox:

- **Sin red y sin sistema de archivos.** Integraciones externas (correo, pagos,
  HMRC/RTI, APIs de terceros) van en Go nativo en `core/internal/`, no en el
  módulo.
- **No declares `tenant_id`.** Lo pone el core.
- Los nombres de tabla se derivan: `mod_<module>_<model>`.

## Antes de dar por terminado un cambio

```bash
cd core && go build ./... && go vet ./... && go test ./...
```

Si tocaste un módulo, recompila su WASM y commitéalo.

## Contexto de seguridad

Dos cosas que parecen aceptables y no lo son:

- **`is_admin` no es autorización.** Es el único control de acceso que hay
  (`users.is_admin`). El CRUD genérico no filtra por rol: cualquier usuario
  autenticado lee todos los modelos del tenant. Trátalo como pendiente conocido,
  no como un control válido. Antes de guardar datos personales o financieros
  hay que añadir roles y permisos por acción aplicados dentro del SDK.
- **Los secretos vienen del entorno, nunca de defaults.** `docker-compose.yml`
  falla a propósito si faltan `FASTERP_JWT_SECRET`, `FASTERP_JWT_REFRESH_SECRET`
  o `FASTERP_DATABASE_URL`. No reintroduzcas valores por defecto para que "levante
  más fácil": un secreto de admin publicado permite forjar tokens.

## Pendientes conocidos

- RBAC / permisos por acción (bloqueante para datos sensibles).
- Decidir cuál de las dos capas HTTP es la definitiva.
- `internal/module` deja `selectColumnsWithJoins` definido y sin usar: es la
  mitad hecha del soporte de relaciones en `List`.
