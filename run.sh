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

PG_PORT="${FASTERP_PG_PORT:-5456}"
APP_USER="${FASTERP_DB_USER:-fasterp_app}"
DB_URL="${FASTERP_DATABASE_URL:-postgres://${APP_USER}:${FASTERP_APP_PASSWORD}@localhost:${PG_PORT}/fasterp?sslmode=disable}"
export FASTERP_DATABASE_URL="$DB_URL"
export FASTERP_MODULES_DIR="${FASTERP_MODULES_DIR:-$DIR/modules}"
export FASTERP_UPLOAD_DIR="${FASTERP_UPLOAD_DIR:-$DIR/core/uploads}"

echo "=== FastERP ==="

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
if lsof -t -i:7071 >/dev/null 2>&1; then
  echo "      puerto 7071 ocupado, deteniendo el proceso anterior..."
  kill "$(lsof -t -i:7071)" 2>/dev/null || true
  sleep 1
fi
setsid /tmp/fasterp-server > /tmp/fasterp-server.log 2>&1 &
sleep 3

TENANT_ID="$(cat core/.default-tenant-id 2>/dev/null || echo "ver /tmp/fasterp-server.log")"

echo ""
echo "  URL:        http://localhost:7071"
echo "  Login:      admin / admin123"
echo "  Tenant ID:  ${TENANT_ID}"
echo "  Logs:       /tmp/fasterp-server.log"
echo ""
echo "  Detener:    kill \$(pgrep -f fasterp-server)"
