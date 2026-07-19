package sdk

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"
)

// Model es la interfaz que los modelos deben implementar
type Model interface {
	TableName() string
	GetID() string
	SetID(id string)
}

// BaseModel proporciona ID y timestamps
type BaseModel struct {
	ID        string    `db:"id,primarykey"`
	TenantID  string    `db:"tenant_id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (m *BaseModel) GetID() string {
	return m.ID
}

func (m *BaseModel) SetID(id string) {
	m.ID = id
}

// ORM es el ORM simple para módulos
type ORM struct {
	sdk *ModuleSDK
}

// NewORM crea una instancia del ORM
func (sdk *ModuleSDK) ORM() *ORM {
	return &ORM{sdk: sdk}
}

// Create inserta un modelo
func (orm *ORM) Create(ctx context.Context, model Model) error {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	data := structToMap(model)
	delete(data, "id")        // El ID se genera automáticamente
	delete(data, "tenant_id") // Injected by SDK
	delete(data, "created_at")
	delete(data, "updated_at")

	id, err := orm.sdk.Insert(ctx, tableName, data)
	if err != nil {
		log.Printf("[ORM] Create error: %v", err)
		return err
	}

	model.SetID(id)
	return nil
}

// FindByID busca un modelo por ID
func (orm *ORM) FindByID(ctx context.Context, model Model, id string) error {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)
	query := fmt.Sprintf(
		"SELECT * FROM %s WHERE tenant_id = $1 AND id = $2 LIMIT 1",
		fullTable,
	)

	row := orm.sdk.DB.QueryRowContext(ctx, query, orm.sdk.TenantID, id)

	// Scan columns into model
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
	return nil
}

// All obtiene todos los modelos (con límite)
func (orm *ORM) All(ctx context.Context, model Model, limit int) (interface{}, error) {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return nil, err
	}

	if limit == 0 {
		limit = 100
	}

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)
	query := fmt.Sprintf(
		"SELECT * FROM %s WHERE tenant_id = $1 LIMIT %d",
		fullTable,
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

// Update actualiza un modelo
func (orm *ORM) Update(ctx context.Context, model Model) error {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	data := structToMap(model)
	delete(data, "id")        // No actualizar ID
	delete(data, "tenant_id") // No actualizar tenant
	delete(data, "created_at")

	data["updated_at"] = time.Now()

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

	return nil
}

// Delete borra un modelo
func (orm *ORM) Delete(ctx context.Context, model Model) error {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)
	query := fmt.Sprintf(
		"DELETE FROM %s WHERE tenant_id = $1 AND id = $2",
		fullTable,
	)

	_, err := orm.sdk.DB.ExecContext(ctx, query, orm.sdk.TenantID, model.GetID())
	if err != nil {
		log.Printf("[ORM] Delete error: %v", err)
		return err
	}

	return nil
}

// Where busca modelos con condición
func (orm *ORM) Where(ctx context.Context, model Model, where string, args ...interface{}) (interface{}, error) {
	tableName := model.TableName()
	if err := validateIdentifier(tableName); err != nil {
		return nil, err
	}

	fullTable := fmt.Sprintf("mod_%s_%s", orm.sdk.ModuleID, tableName)
	query := fmt.Sprintf(
		"SELECT * FROM %s WHERE tenant_id = $1 AND %s",
		fullTable,
		where,
	)

	allArgs := append([]interface{}{orm.sdk.TenantID}, args...)

	rows, err := orm.sdk.DB.QueryContext(ctx, query, allArgs...)
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

func structToMap(model interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	v := reflect.ValueOf(model).Elem()

	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		value := v.Field(i).Interface()
		result[toSnakeCase(field.Name)] = value
	}

	return result
}

func mapToStruct(model interface{}, columns []string, values []interface{}) {
	v := reflect.ValueOf(model).Elem()
	t := v.Type()

	for i, col := range columns {
		for j := 0; j < t.NumField(); j++ {
			field := t.Field(j)
			if toSnakeCase(field.Name) == col {
				v.Field(j).Set(reflect.ValueOf(values[i]))
			}
		}
	}
}

func getStructColumns(model interface{}) []string {
	var columns []string
	v := reflect.ValueOf(model).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		columns = append(columns, field.Name)
	}

	return columns
}

func toSnakeCase(str string) string {
	result := ""
	for i, r := range str {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result += "_"
			}
			result += string(r + 32)
		} else {
			result += string(r)
		}
	}
	return result
}
