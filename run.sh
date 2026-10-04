#!/usr/bin/env bash
# Levanta FastERP: PostgreSQL + el core Gin (API). La UI server-rendered sigue
# en la capa legacy (core/main.go); este script no la sirve.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

# El core lee la configuración del entorno y no trae defaults para los secretos:
# sin esto no arranca (o peor, arranca con secretos de relleno).
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

: "${FASTERP_JWT_SECRET:?falta FASTERP_JWT_SECRET en .env}"
: "${FASTERP_JWT_REFRESH_SECRET:?falta FASTERP_JWT_REFRESH_SECRET en .env}"
: "${FASTERP_APP_PASSWORD:?falta FASTERP_APP_PASSWORD en .env}"

# Desarrollo: los secretos por defecto están permitidos, pero se avisa. Sin esto
# el servidor ni arranca y el mensaje de error no dice por qué.
export FASTERP_DEV="${FASTERP_DEV:-true}"
# En dev se crea la cuenta admin para no tener que pasar por /setup cada vez. La
# contraseña sale del entorno; ya no hay admin/admin123 fijo en el binario.
export FASTERP_SEED="${FASTERP_SEED:-true}"
if [ "$FASTERP_SEED" = "true" ] && [ -z "${FASTERP_ADMIN_PASSWORD:-}" ]; then
  export FASTERP_ADMIN_PASSWORD="admin123"
  echo "      (dev) usando FASTERP_ADMIN_PASSWORD=admin123 — no hagas esto en producción"
fi

PG_PORT="${FASTERP_PG_PORT:-5456}"
APP_USER="${FASTERP_DB_USER:-fasterp_app}"
DB_URL="${FASTERP_DATABASE_URL:-postgres://${APP_USER}:${FASTERP_APP_PASSWORD}@localhost:${PG_PORT}/fasterp?sslmode=disable}"
export FASTERP_DATABASE_URL="$DB_URL"
export FASTERP_MODULES_DIR="${FASTERP_MODULES_DIR:-$DIR/modules}"
export FASTERP_UPLOAD_DIR="${FASTERP_UPLOAD_DIR:-$DIR/core/uploads}"

echo "=== FastERP ==="

fasterp_stop() {
  local pids=()
  if command -v lsof >/dev/null 2>&1; then
    while IFS= read -r pid; do
      [ -n "${pid:-}" ] && pids+=("$pid")
    done < <(lsof -t -i:7071 2>/dev/null || true)
  fi

  if command -v pgrep >/dev/null 2>&1; then
    while IFS= read -r pid; do
      [ -n "${pid:-}" ] && pids+=("$pid")
    done < <(pgrep -af '/tmp/fasterp-server|fasterp-server|cmd/server' 2>/dev/null | awk '{print $1}' || true)
  fi

  if [ "${#pids[@]}" -gt 0 ]; then
    echo "      deteniendo procesos anteriores..."
    for pid in $(printf '%s\n' "${pids[@]}" | sort -u); do
      kill "$pid" 2>/dev/null || true
    done
    sleep 1
  fi
}

# 1. PostgreSQL (Docker, solo la base de datos)
if ! pg_isready -h localhost -p "$PG_PORT" -q 2>/dev/null; then
  echo "[1/3] Levantando PostgreSQL en :${PG_PORT}..."
  docker compose up -d postgres
  sleep 2
else
  echo "[1/3] PostgreSQL ya corriendo en :${PG_PORT}"
fi

# 2. Compilar el core
echo "[2/3] Compilando el core..."
(cd core && go build -o /tmp/fasterp-server ./cmd/server)

# 3. Arrancar
echo "[3/3] Iniciando el core en http://localhost:7071"
fasterp_stop
cd "$DIR/core"
setsid /tmp/fasterp-server > /tmp/fasterp-server.log 2>&1 &
sleep 3

TENANT_ID="$(psql "${FASTERP_DATABASE_URL}" -At -c "SELECT id FROM tenants WHERE slug = 'default' ORDER BY created_at LIMIT 1;" 2>/dev/null | tr -d '\r' | head -n 1)"
if [ -z "${TENANT_ID:-}" ]; then
  TENANT_ID="$(cat .default-tenant-id 2>/dev/null || echo "sin tenant default")"
fi

if [ -z "${TENANT_ID:-}" ] || [ "${TENANT_ID}" = "sin tenant default" ]; then
  TENANT_ID="revisar /tmp/fasterp-server.log"
fi

echo ""
echo "  URL:        http://localhost:7071"
if [ "$FASTERP_SEED" = "true" ]; then
  echo "  Login:      admin / ${FASTERP_ADMIN_PASSWORD}"
fi
echo "  Tenant ID:  ${TENANT_ID}"
echo "  Logs:       /tmp/fasterp-server.log"
echo ""
echo "  Detener:    kill \$(pgrep -f fasterp-server)"
