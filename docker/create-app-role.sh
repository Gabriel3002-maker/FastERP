#!/bin/bash
# Crea (o actualiza) el rol restringido fasterp_app sobre una base YA EXISTENTE.
#
# docker/init-db.sh solo corre la primera vez que se inicializa el volumen de datos.
# Si el volumen ya existía —lo normal al actualizar un despliegue— ese script nunca se
# ejecuta y el backend no puede conectarse. Este script cubre ese caso y es idempotente:
# se puede correr las veces que haga falta.
#
# Uso:
#   ./docker/create-app-role.sh                 # contra el compose local
#   PGHOST=... PGPORT=... ./docker/create-app-role.sh
set -euo pipefail

cd "$(dirname "$0")/.."

if [ -f .env ]; then
	set -a; . ./.env; set +a
fi

: "${FASTERP_APP_PASSWORD:?define FASTERP_APP_PASSWORD en .env}"
: "${POSTGRES_PASSWORD:?define POSTGRES_PASSWORD en .env}"

DB="${POSTGRES_DB:-fasterp}"
SUPERUSER="${POSTGRES_USER:-fasterp}"

echo "[create-app-role] configurando fasterp_app en la base '$DB'..."

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$SUPERUSER" -d "$DB" <<EOSQL
DO \$\$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fasterp_app') THEN
		ALTER ROLE fasterp_app LOGIN PASSWORD '$FASTERP_APP_PASSWORD'
			NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
	ELSE
		CREATE ROLE fasterp_app LOGIN PASSWORD '$FASTERP_APP_PASSWORD'
			NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
	END IF;
END \$\$;

GRANT CONNECT ON DATABASE "$DB" TO fasterp_app;
GRANT USAGE, CREATE ON SCHEMA public TO fasterp_app;

-- La app tiene que ser dueña de sus tablas para poder migrarlas. El migrador aplica
-- FORCE ROW LEVEL SECURITY, así que ser dueña no le permite saltarse las políticas.
DO \$\$
DECLARE t text;
BEGIN
	FOR t IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
		EXECUTE format('ALTER TABLE public.%I OWNER TO fasterp_app', t);
	END LOOP;
END \$\$;

-- Alinea la contraseña del superusuario con .env (POSTGRES_PASSWORD solo se aplica
-- al inicializar el volumen, no en arranques posteriores).
ALTER ROLE "$SUPERUSER" PASSWORD '$POSTGRES_PASSWORD';
EOSQL

echo "[create-app-role] listo. Verificación:"
docker compose exec -T postgres psql -U "$SUPERUSER" -d "$DB" -c \
	"SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname='fasterp_app';"
