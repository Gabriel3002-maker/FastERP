package sdk

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// ModuleSDK es la interfaz que los módulos usan para hablar con el core
type ModuleSDK struct {
	ModuleID string
	TenantID string
	UserID   string
	DB       *sql.DB
	Manifest *Manifest
}

// Manifest define la estructura de un módulo
type Manifest struct {
	Name   string                     `json:"name"`
	Models map[string]*ModelDef       `json:"models"`
}

// ModelDef define una tabla en el módulo
type ModelDef struct {
	Name   string `json:"name"`
	Fields map[string]*FieldDef `json:"fields"`
}

// FieldDef define una columna
type FieldDef struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
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

// LoadManifest carga el manifest.json del módulo
func (sdk *ModuleSDK) LoadManifest(manifestJSON string) error {
	manifest := &Manifest{}
	if err := json.Unmarshal([]byte(manifestJSON), manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}
	sdk.Manifest = manifest
	return nil
}

// getTableName retorna el nombre de tabla prefijado (mod_moduleid_modelname)
func (sdk *ModuleSDK) getTableName(modelName string) string {
	return fmt.Sprintf("mod_%s_%s", sdk.ModuleID, modelName)
}

// Create inserta un registro genérico basado en el manifest
func (sdk *ModuleSDK) Create(ctx context.Context, modelName string, data map[string]interface{}) (string, error) {
	if sdk.Manifest == nil {
		return "", fmt.Errorf("manifest not loaded")
	}

	model, ok := sdk.Manifest.Models[modelName]
	if !ok {
		return "", fmt.Errorf("model %s not found in manifest", modelName)
	}

	// Validar que los datos coincidan con el manifest
	columns := []string{"tenant_id"}
	values := []interface{}{sdk.TenantID}
	placeholders := []string{"$1"}

	i := 2
	for fieldName, fieldValue := range data {
		if _, exists := model.Fields[fieldName]; !exists {
			log.Printf("[SDK] Warning: field %s not in manifest for model %s", fieldName, modelName)
			continue
		}
		columns = append(columns, fieldName)
		values = append(values, fieldValue)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		i++
	}

	tableName := sdk.getTableName(modelName)
	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		tableName,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)

	var id string
	err := sdk.DB.QueryRowContext(ctx, query, values...).Scan(&id)
	if err != nil {
		log.Printf("[SDK] Create error: %v", err)
		return "", err
	}

	return id, nil
}

// Update actualiza un registro
func (sdk *ModuleSDK) Update(ctx context.Context, modelName string, id string, data map[string]interface{}) error {
	if sdk.Manifest == nil {
		return fmt.Errorf("manifest not loaded")
	}

	model, ok := sdk.Manifest.Models[modelName]
	if !ok {
		return fmt.Errorf("model %s not found in manifest", modelName)
	}

	sets := []string{}
	values := []interface{}{sdk.TenantID, id}
	i := 3

	for fieldName, fieldValue := range data {
		if _, exists := model.Fields[fieldName]; !exists {
			log.Printf("[SDK] Warning: field %s not in manifest for model %s", fieldName, modelName)
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", fieldName, i))
		values = append(values, fieldValue)
		i++
	}

	if len(sets) == 0 {
		return fmt.Errorf("no fields to update")
	}

	tableName := sdk.getTableName(modelName)
	query := fmt.Sprintf(
		"UPDATE %s SET %s, updated_at = CURRENT_TIMESTAMP WHERE id = $2 AND tenant_id = $1",
		tableName,
		strings.Join(sets, ", "),
	)

	_, err := sdk.DB.ExecContext(ctx, query, values...)
	if err != nil {
		log.Printf("[SDK] Update error: %v", err)
		return err
	}

	return nil
}

// Delete borra un registro
func (sdk *ModuleSDK) Delete(ctx context.Context, modelName string, id string) error {
	if sdk.Manifest == nil {
		return fmt.Errorf("manifest not loaded")
	}

	_, ok := sdk.Manifest.Models[modelName]
	if !ok {
		return fmt.Errorf("model %s not found in manifest", modelName)
	}

	tableName := sdk.getTableName(modelName)
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1 AND tenant_id = $2", tableName)

	_, err := sdk.DB.ExecContext(ctx, query, id, sdk.TenantID)
	if err != nil {
		log.Printf("[SDK] Delete error: %v", err)
		return err
	}

	return nil
}

// List retorna todos los registros de un modelo
func (sdk *ModuleSDK) List(ctx context.Context, modelName string) ([]map[string]interface{}, error) {
	if sdk.Manifest == nil {
		return nil, fmt.Errorf("manifest not loaded")
	}

	_, ok := sdk.Manifest.Models[modelName]
	if !ok {
		return nil, fmt.Errorf("model %s not found in manifest", modelName)
	}

	tableName := sdk.getTableName(modelName)
	query := fmt.Sprintf("SELECT * FROM %s WHERE tenant_id = $1 ORDER BY created_at DESC", tableName)

	rows, err := sdk.DB.QueryContext(ctx, query, sdk.TenantID)
	if err != nil {
		log.Printf("[SDK] List error: %v", err)
		return nil, err
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	var results []map[string]interface{}

	for rows.Next() {
		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range cols {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			log.Printf("[SDK] Scan error: %v", err)
			continue
		}

		entry := make(map[string]interface{})
		for i, col := range cols {
			entry[col] = values[i]
		}
		results = append(results, entry)
	}

	return results, nil
}

// Get retorna un registro por ID
func (sdk *ModuleSDK) Get(ctx context.Context, modelName string, id string) (map[string]interface{}, error) {
	if sdk.Manifest == nil {
		return nil, fmt.Errorf("manifest not loaded")
	}

	_, ok := sdk.Manifest.Models[modelName]
	if !ok {
		return nil, fmt.Errorf("model %s not found in manifest", modelName)
	}

	tableName := sdk.getTableName(modelName)
	query := fmt.Sprintf("SELECT * FROM %s WHERE id = $1 AND tenant_id = $2 LIMIT 1", tableName)

	rows, err := sdk.DB.QueryContext(ctx, query, id, sdk.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("record not found")
	}

	cols, _ := rows.Columns()
	values := make([]interface{}, len(cols))
	valuePtrs := make([]interface{}, len(cols))
	for i := range cols {
		valuePtrs[i] = &values[i]
	}

	if err := rows.Scan(valuePtrs...); err != nil {
		return nil, err
	}

	entry := make(map[string]interface{})
	for i, col := range cols {
		entry[col] = values[i]
	}

	return entry, nil
}


