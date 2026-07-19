#!/usr/bin/env bash
set -e

echo "=== FastERP Multi-Tenant - Start ==="

# 1. Ensure PostgreSQL is up (Docker or local)
if ! pg_isready -h localhost -p 5456 -q 2>/dev/null; then
  echo "[1/4] Starting PostgreSQL via Docker..."
  docker compose up -d postgres
  sleep 2
else
  echo "[1/4] PostgreSQL already running on :5456"
fi

# 2. Build backend
echo "[2/4] Building backend..."
cd backend
go build -o /tmp/fasterp-server ./cmd/server
cd ..

# 3. Start backend
echo "[3/4] Starting backend on :7071..."
kill $(lsof -t -i:7071) 2>/dev/null || true
sleep 1
setsid /tmp/fasterp-server > /tmp/fasterp-server.log 2>&1 &
sleep 2

TENANT_ID=$(cat backend/.default-tenant-id 2>/dev/null || echo "check logs")
echo "   Backend PID: $(pgrep -f fasterp-server)"
echo "   Tenant ID:   $TENANT_ID"

# 4. Start frontend
echo "[4/4] Starting frontend on :5051..."
kill $(lsof -t -i:5051) 2>/dev/null || true
sleep 1
cd frontend
setsid npx vite --port 5051 --host > /tmp/frontend.log 2>&1 &
cd ..
sleep 2

echo ""
echo "=== FastERP is running! ==="
echo "  Frontend: http://localhost:5051"
echo "  Backend:  http://localhost:7071"
echo "  Tenant ID: $TENANT_ID"
echo "  Login:     admin / admin123"
echo ""
echo "To stop: kill \$(pgrep -f fasterp-server) && kill \$(pgrep -f vite)"
