package sdk

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"reflect"
	"strings"
	"time"
)

// ORMConfig configura el ORM
type ORMConfig struct {
	EnableAudit      bool   // Auditoría de cambios
	EnableEncryption bool   // Encriptar campos sensibles
	EncryptionKey    string // Clave para encripción (32 bytes en base64)
	SoftDelete       bool   // Soft delete (deleted_at)
}

// AdvancedORM es el ORM mejorado con seguridad
type AdvancedORM struct {
	sdk    *ModuleSDK
	config *ORMConfig
	cipher cipher.Block
}

// NewAdvancedORM crea el ORM mejorado
func (sdk *ModuleSDK) AdvancedORM(config *ORMConfig) (*AdvancedORM, error) {
	orm := &AdvancedORM{
		sdk:    sdk,
		config: config,
	}

	// Inicializar encripción si está habilitada
	if config.EnableEncryption && config.EncryptionKey != "" {
		key, err := base64.StdEncoding.DecodeString(config.EncryptionKey)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid encryption key: must be 32 bytes in base64")
		}

		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}

		orm.cipher = block
		log.Println("[ORM] Encryption enabled")
	}

	return orm, nil
}

// Validador para campos sensibles
type Sensitive string

// Encrypt encripta un valor
func (orm *AdvancedORM) Encrypt(value string) (string, error) {
	if orm.cipher == nil {
		return value, nil
	}

	plaintext := []byte(value)
	ciphertext := make([]byte, aes.BlockSize+len(plaintext))
	iv := ciphertext[:aes.BlockSize]

	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	stream := cipher.NewCFBEncrypter(orm.cipher, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintext)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt desencripta un valor
func (orm *AdvancedORM) Decrypt(encrypted string) (string, error) {
	if orm.cipher == nil {
		return encrypted, nil
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}

	if len(ciphertext) < aes.BlockSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(orm.cipher, iv)
	plaintext := make([]byte, len(ciphertext))
	stream.XORKeyStream(plaintext, ciphertext)

	return string(plaintext), nil
}

// ValidateModel valida un modelo antes de guardar
func (orm *AdvancedORM) ValidateModel(model interface{}) error {
	v := reflect.ValueOf(model).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i).Interface()

		// Validar campo requerido
		tags := field.Tag.Get("db")
		if strings.Contains(tags, "required") {
			if value == "" || value == nil {
				return fmt.Errorf("field %s is required", field.Name)
			}
		}

		// Validar longitud
		if strings.Contains(tags, "max") {
			parts := strings.Split(tags, ",")
			for _, part := range parts {
				if strings.HasPrefix(part, "max:") {
					// Implementar validación de longitud
				}
			}
		}

		// Validar email
		if strings.Contains(tags, "email") {
			if str, ok := value.(string); ok && str != "" {
				if !isValidEmail(str) {
					return fmt.Errorf("field %s is not a valid email", field.Name)
				}
			}
		}
	}

	return nil
}

// Create con validación y encripción
func (orm *AdvancedORM) Create(ctx context.Context, model Model) error {
	// Validar modelo
	if err := orm.ValidateModel(model); err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	data := structToMap(model)
	delete(data, "id")
	delete(data, "tenant_id")
	delete(data, "created_at")
	delete(data, "updated_at")
	delete(data, "deleted_at")

	// Encriptar campos sensibles
	data = orm.encryptSensitiveFields(model, data)

	id, err := orm.sdk.Insert(ctx, tableName, data)
	if err != nil {
		log.Printf("[ORM] Create error: %v", err)
		return err
	}

	model.SetID(id)

	// Auditar
	if orm.config.EnableAudit {
		orm.auditLog(ctx, "CREATE", tableName, id, "")
	}

	return nil
}

// Update con validación y encripción
func (orm *AdvancedORM) Update(ctx context.Context, model Model) error {
	// Validar
	if err := orm.ValidateModel(model); err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	data := structToMap(model)
	delete(data, "id")
	delete(data, "tenant_id")
	delete(data, "created_at")
	delete(data, "deleted_at")

	data["updated_at"] = time.Now()

	// Encriptar
	data = orm.encryptSensitiveFields(model, data)

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)

	sets := []string{}
	values := []interface{}{orm.sdk.TenantID, model.GetID()}

	i := 3
	for k, v := range data {
		if err := validateIdentifier(k); err != nil {
			return err
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", k, i))
		values = append(values, v)
		i++
	}

	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE tenant_id = $1 AND id = $2",
		fullTable,
		strings.Join(sets, ", "),
	)

	_, err := orm.sdk.DB.ExecContext(ctx, query, values...)
	if err != nil {
		log.Printf("[ORM] Update error: %v", err)
		return err
	}

	// Auditar
	if orm.config.EnableAudit {
		orm.auditLog(ctx, "UPDATE", tableName, model.GetID(), "")
	}

	return nil
}

// Delete con soft delete si está habilitado
func (orm *AdvancedORM) Delete(ctx context.Context, model Model) error {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)

	if orm.config.SoftDelete {
		// Soft delete: actualizar deleted_at
		query := fmt.Sprintf(
			"UPDATE %s SET deleted_at = CURRENT_TIMESTAMP WHERE tenant_id = $1 AND id = $2",
			fullTable,
		)
		_, err := orm.sdk.DB.ExecContext(ctx, query, orm.sdk.TenantID, model.GetID())
		if err != nil {
			return err
		}
	} else {
		// Hard delete
		query := fmt.Sprintf(
			"DELETE FROM %s WHERE tenant_id = $1 AND id = $2",
			fullTable,
		)
		_, err := orm.sdk.DB.ExecContext(ctx, query, orm.sdk.TenantID, model.GetID())
		if err != nil {
			return err
		}
	}

	// Auditar
	if orm.config.EnableAudit {
		orm.auditLog(ctx, "DELETE", tableName, model.GetID(), "")
	}

	return nil
}

// FindByID con desencripción automática
func (orm *AdvancedORM) FindByID(ctx context.Context, model Model, id string) error {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)
	
	whereClause := "WHERE tenant_id = $1 AND id = $2"
	if orm.config.SoftDelete {
		whereClause += " AND deleted_at IS NULL"
	}

	query := fmt.Sprintf("SELECT * FROM %s %s LIMIT 1", fullTable, whereClause)

	row := orm.sdk.DB.QueryRowContext(ctx, query, orm.sdk.TenantID, id)

	columns := getStructColumns(model)
	values := make([]interface{}, len(columns))
	for i := range columns {
		values[i] = reflect.New(reflect.TypeOf(model).Elem().FieldByName(columns[i]).Type).Interface()
	}

	if err := row.Scan(values...); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("not found")
		}
		return err
	}

	mapToStruct(model, columns, values)

	// Desencriptar
	orm.decryptSensitiveFields(model)

	return nil
}

// All con filtro soft delete
func (orm *AdvancedORM) All(ctx context.Context, model Model, limit int) (interface{}, error) {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return nil, err
	}

	if limit == 0 {
		limit = 100
	}

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)
	
	whereClause := "WHERE tenant_id = $1"
	if orm.config.SoftDelete {
		whereClause += " AND deleted_at IS NULL"
	}

	query := fmt.Sprintf(
		"SELECT * FROM %s %s LIMIT %d",
		fullTable,
		whereClause,
		limit,
	)

	rows, err := orm.sdk.DB.QueryContext(ctx, query, orm.sdk.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]interface{}{}
	columns, _ := rows.Columns()

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))

		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		rows.Scan(valuePtrs...)

		entry := make(map[string]interface{})
		for i, col := range columns {
			entry[col] = values[i]
		}

		results = append(results, entry)
	}

	return results, nil
}

// Helper functions

func (orm *AdvancedORM) encryptSensitiveFields(model interface{}, data map[string]interface{}) map[string]interface{} {
	if !orm.config.EnableEncryption {
		return data
	}

	v := reflect.ValueOf(model).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tags := field.Tag.Get("db")

		if strings.Contains(tags, "encrypt") {
			key := toSnakeCase(field.Name)
			if val, ok := data[key].(string); ok && val != "" {
				encrypted, _ := orm.Encrypt(val)
				data[key] = encrypted
			}
		}
	}

	return data
}

func (orm *AdvancedORM) decryptSensitiveFields(model interface{}) {
	if !orm.config.EnableEncryption {
		return
	}

	v := reflect.ValueOf(model).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tags := field.Tag.Get("db")

		if strings.Contains(tags, "encrypt") {
			val := v.Field(i).Interface()
			if str, ok := val.(string); ok && str != "" {
				decrypted, _ := orm.Decrypt(str)
				v.Field(i).SetString(decrypted)
			}
		}
	}
}

func (orm *AdvancedORM) auditLog(ctx context.Context, action, table, id, changes string) {
	auditTable := fmt.Sprintf("mod_%s_audit_log", orm.sdk.ModuleID)

	query := fmt.Sprintf(
		"INSERT INTO %s (tenant_id, user_id, action, table_name, record_id, changes, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)",
		auditTable,
	)

	orm.sdk.DB.ExecContext(ctx, query,
		orm.sdk.TenantID,
		orm.sdk.UserID,
		action,
		table,
		id,
		changes,
		time.Now(),
	)
}

func isValidEmail(email string) bool {
	return strings.Contains(email, "@") && strings.Contains(email, ".")
}
