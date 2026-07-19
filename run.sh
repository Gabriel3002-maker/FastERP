#!/usr/bin/env bash
set -e

DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

echo "=== FastERP Multi-Tenant ==="

# 1. PostgreSQL (Docker solo para la BD)
echo "[1] Levantando PostgreSQL..."
docker compose up -d postgres 2>/dev/null
sleep 2

# 2. Construir backend
echo "[2] Compilando backend..."
cd backend
go build -o /tmp/fasterp-server ./cmd/server
cd ..

# 3. Iniciar backend (fondo)
echo "[3] Iniciando backend en http://localhost:7071"
FASTERP_DATABASE_URL="postgres://fasterp:fasterp@localhost:5456/fasterp?sslmode=disable" /tmp/fasterp-server &
sleep 3

# Mostrar tenant ID
if [ -f backend/.default-tenant-id ]; then
  echo ""
  echo "=== TENANT ID: $(cat backend/.default-tenant-id) ==="
  echo ""
else
  echo "=== ESPERA 3 segundos y revisa backend/.default-tenant-id ==="
fi

# 4. Frontend
echo "[4] Iniciando frontend en http://localhost:5051"
cd frontend
npx vite --port 5051 --host
