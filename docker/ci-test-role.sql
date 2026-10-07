-- Rol con el que corre la suite en CI.
--
-- POSTGRES_USER es superusuario, y un superusuario ignora las políticas RLS
-- incluso con FORCE: exigeRLSVerificable() se salta los tests de aislamiento en
-- vez de fallar, así que un CI con el superusuario quedaría en verde sin haber
-- comprobado nada. Este rol es NOSUPERUSER y NOBYPASSRLS, como pide el propio
-- test.
--
-- CREATEDB lo necesita el test de migraciones, que crea y borra su propia base
-- para no pisar a los tests de módulos, que corren en paralelo.
--
-- Solo se usa en .github/workflows/ci.yml: se monta en
-- /docker-entrypoint-initdb.d/, así que solo se ejecuta la primera vez que se
-- inicializa el volumen del contenedor.
CREATE ROLE fasterp_test LOGIN PASSWORD 'fasterp_test_pw'
	NOSUPERUSER NOBYPASSRLS CREATEDB NOCREATEROLE;

-- En PostgreSQL 15+ PUBLIC no puede crear objetos en el esquema public, y los
-- tests crean sus propias tablas.
GRANT USAGE, CREATE ON SCHEMA public TO fasterp_test;
