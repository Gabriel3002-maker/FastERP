package models

import (
	"context"
	"log"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/migrations"
)

// AutoMigrate deja la base del core en su estado conocido y se puede llamar en
// cada arranque.
//
// Ya no define el esquema: eso lo hacen las migraciones versionadas de
// internal/migrations, que además dejan constancia en schema_migrations y
// fallan en seco si algo no cuadra. Lo que queda aquí es lo que hay que
// reafirmar en cada arranque porque no es DDL sino estado: el aislamiento por
// tenant se puede perder si alguien ejecuta un ALTER a mano, y al ser la
// defensa en profundidad de los filtros WHERE, conviene repasarla siempre.
//
// El esquema de los módulos tampoco pasa por aquí: lo reconcilia
// internal/module a partir de los manifests, porque no es de esta base sino de
// quien instala el módulo.
func AutoMigrate() error {
	if err := migrations.Up(context.Background(), db.DB); err != nil {
		return err
	}

	// Tenant isolation as defense-in-depth behind the explicit WHERE tenant_id filters.
	// tenants is deliberately excluded: it is the tenant registry itself and is resolved
	// on the shared pool before any tenant context exists.
	for _, t := range []string{
		"contacts", "users", "user_permissions",
		"roles", "role_permissions", "user_roles",
		"installed_modules", "refresh_tokens",
	} {
		db.ApplyTenantRLS(t)
	}

	log.Println("[DB] Migration completed")
	return nil
}
