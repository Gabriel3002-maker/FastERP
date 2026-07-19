package sdk

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// ModuleSDK es la interfaz que los módulos usan para hablar con el core
type ModuleSDK struct {
	ModuleID string
	TenantID string
	UserID   string
	DB       *sql.DB
}

// NewModuleSDK crea una instancia del SDK para un módulo
func NewModuleSDK(moduleID, tenantID, userID string, db *sql.DB) *ModuleSDK {
	return &ModuleSDK{
		ModuleID: moduleID,
		TenantID: tenantID,
		UserID:   userID,
		DB:       db,
	}
}

// CreateTable crea una tabla para el módulo (prefijada con mod_moduleid_)
func (sdk *ModuleSDK) CreateTable(ctx context.Context, tableName string, schema string) error {
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", sdk.ModuleID, tableName)
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			%s
		)
	`, fullTable, schema)

	_, err := sdk.DB.ExecContext(ctx, query)
	if err != nil {
		log.Printf("[SDK] CreateTable error: %v", err)
		return err
	}

	log.Printf("[SDK] ✓ Created table: %s", fullTable)
	return nil
}

// Insert inserta en tabla del módulo
func (sdk *ModuleSDK) Insert(ctx context.Context, tableName string, data map[string]interface{}) (string, error) {
	if err := validateIdentifier(tableName); err != nil {
		return "", err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", sdk.ModuleID, tableName)

	columns := []string{"tenant_id"}
	values := []interface{}{sdk.TenantID}
	placeholders := []string{"$1"}

	i := 2
	for k, v := range data {
		if err := validateIdentifier(k); err != nil {
			return "", err
		}
		columns = append(columns, k)
		values = append(values, v)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		i++
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		fullTable,
		joinStrings(columns, ", "),
		joinStrings(placeholders, ", "),
	)

	var id string
	err := sdk.DB.QueryRowContext(ctx, query, values...).Scan(&id)
	if err != nil {
		log.Printf("[SDK] Insert error: %v", err)
		return "", err
	}

	return id, nil
}

// Query ejecuta SELECT en tabla del módulo
func (sdk *ModuleSDK) Query(ctx context.Context, tableName string, whereClause string, args ...interface{}) (*sql.Rows, error) {
	if err := validateIdentifier(tableName); err != nil {
		return nil, err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", sdk.ModuleID, tableName)
	query := fmt.Sprintf("SELECT * FROM %s WHERE tenant_id = $1", fullTable)

	if whereClause != "" {
		query += " AND " + whereClause
	}

	args = append([]interface{}{sdk.TenantID}, args...)
	return sdk.DB.QueryContext(ctx, query, args...)
}

// GetContext retorna información del módulo
func (sdk *ModuleSDK) GetContext() map[string]string {
	return map[string]string{
		"module_id": sdk.ModuleID,
		"tenant_id": sdk.TenantID,
		"user_id":   sdk.UserID,
	}
}

// Helper functions

func validateIdentifier(name string) error {
	if name == "" || len(name) > 255 {
		return fmt.Errorf("invalid identifier: %s", name)
	}

	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return fmt.Errorf("invalid identifier: %s", name)
		}
	}

	return nil
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
