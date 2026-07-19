#!/bin/bash
# Crea el rol de aplicación con el que FastERP se conecta.
#
# POSTGRES_USER es superusuario de la base, y un superusuario ignora las políticas
# RLS: si la app se conectara con él, el aislamiento entre tenants dependería solo
# de los filtros WHERE tenant_id del código. Este rol no es superusuario y no tiene
# BYPASSRLS, así que las políticas sí se le aplican.
#
# La app crea sus propias tablas al arrancar, por lo que queda como dueña de ellas.
# Por eso el migrador aplica FORCE ROW LEVEL SECURITY: sin FORCE, el dueño de una
# tabla se salta sus propias políticas.
#
# Ojo: los scripts de /docker-entrypoint-initdb.d/ solo corren la PRIMERA vez que se
# inicializa el volumen de datos. Sobre un volumen existente hay que crear el rol a mano.
set -e

if [ -z "$FASTERP_APP_PASSWORD" ]; then
	echo "[init-db] FALTA FASTERP_APP_PASSWORD; abortando" >&2
	exit 1
fi

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
	CREATE ROLE fasterp_app LOGIN PASSWORD '$FASTERP_APP_PASSWORD'
		NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
	GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO fasterp_app;
	GRANT USAGE, CREATE ON SCHEMA public TO fasterp_app;
EOSQL

echo "[init-db] rol fasterp_app creado (sin superusuario, sin BYPASSRLS)"
