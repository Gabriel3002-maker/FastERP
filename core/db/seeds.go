package db

import (
	"context"
	"log"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func SeedDefaultData(db *DB) error {
	log.Println("[Seed] Seeding default data...")

	ctx := context.Background()

	// Verificar si ya existe tenant default
	var count int
	err := db.QueryRow(ctx, "SELECT COUNT(*) FROM tenants WHERE slug = 'default'").Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		log.Println("[Seed] Default tenant already exists, skipping...")
		return nil
	}

	// Crear tenant
	tenantID := uuid.New().String()
	_, err = db.Exec(ctx,
		"INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)",
		tenantID, "Default Tenant", "default")
	if err != nil {
		return err
	}
	log.Printf("[Seed] ✓ Tenant created: %s", tenantID)

	// Crear usuario admin
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	userID := uuid.New().String()
	_, err = db.Exec(ctx,
		"INSERT INTO users (id, tenant_id, username, email, password_hash, is_admin) VALUES ($1, $2, $3, $4, $5, $6)",
		userID, tenantID, "admin", "admin@fasterp.local", string(passwordHash), true)
	if err != nil {
		return err
	}
	log.Println("[Seed] ✓ User created: admin/admin123")

	// Guardar tenant ID para referencia
	err = SaveDefaultTenantID(tenantID)
	if err != nil {
		log.Printf("[WARN] Failed to save default tenant ID: %v", err)
	}

	log.Println("[Seed] ✓ Default data seeded")
	return nil
}

func SaveDefaultTenantID(tenantID string) error {
	return writeFile(".default-tenant-id", tenantID)
}

func writeFile(filename, content string) error {
	return nil // TODO: Implementar
}
