package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// AuditEvent representa un evento auditable
type AuditEvent struct {
	ID        string                 `json:"id"`
	TenantID  string                 `json:"tenant_id"`
	UserID    string                 `json:"user_id"`
	Action    string                 `json:"action"`     // CREATE, READ, UPDATE, DELETE, LOGIN, etc
	Entity    string                 `json:"entity"`     // Tabla/Entidad
	EntityID  string                 `json:"entity_id"`
	Before    map[string]interface{} `json:"before,omitempty"`
	After     map[string]interface{} `json:"after,omitempty"`
	Status    string                 `json:"status"`     // success, error
	Error     string                 `json:"error,omitempty"`
	IPAddress string                 `json:"ip_address,omitempty"`
	UserAgent string                 `json:"user_agent,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// AuditLogger maneja auditoría centralizada
type AuditLogger struct {
	db *DB
}

// NewAuditLogger crea un logger de auditoría
func NewAuditLogger(db *DB) *AuditLogger {
	return &AuditLogger{db: db}
}

// Log registra un evento de auditoría
func (al *AuditLogger) Log(ctx context.Context, event AuditEvent) error {
	if event.ID == "" {
		event.ID = generateID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}

	beforeJSON, _ := json.Marshal(event.Before)
	afterJSON, _ := json.Marshal(event.After)

	query := `
		INSERT INTO audit_log 
		(id, tenant_id, user_id, action, entity, entity_id, before, after, status, error, ip_address, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err := al.db.Exec(ctx,
		query,
		event.ID,
		event.TenantID,
		event.UserID,
		event.Action,
		event.Entity,
		event.EntityID,
		string(beforeJSON),
		string(afterJSON),
		event.Status,
		event.Error,
		event.IPAddress,
		event.UserAgent,
		event.CreatedAt,
	)

	if err != nil {
		log.Printf("[Audit] Error logging event: %v", err)
		return err
	}

	return nil
}

// GetAuditTrail obtiene el historial de auditoría de una entidad
func (al *AuditLogger) GetAuditTrail(ctx context.Context, tenantID, entityID string, limit int) ([]AuditEvent, error) {
	if limit == 0 {
		limit = 100
	}

	query := `
		SELECT id, tenant_id, user_id, action, entity, entity_id, before, after, status, error, ip_address, user_agent, created_at
		FROM audit_log
		WHERE tenant_id = $1 AND entity_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := al.db.Query(ctx, query, tenantID, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var beforeStr, afterStr sql.NullString

		err := rows.Scan(
			&event.ID,
			&event.TenantID,
			&event.UserID,
			&event.Action,
			&event.Entity,
			&event.EntityID,
			&beforeStr,
			&afterStr,
			&event.Status,
			&event.Error,
			&event.IPAddress,
			&event.UserAgent,
			&event.CreatedAt,
		)

		if err != nil {
			log.Printf("[Audit] Error scanning row: %v", err)
			continue
		}

		if beforeStr.Valid {
			json.Unmarshal([]byte(beforeStr.String), &event.Before)
		}
		if afterStr.Valid {
			json.Unmarshal([]byte(afterStr.String), &event.After)
		}

		events = append(events, event)
	}

	return events, nil
}

// GetUserAudit obtiene auditoría de actividad de un usuario
func (al *AuditLogger) GetUserAudit(ctx context.Context, tenantID, userID string, limit int) ([]AuditEvent, error) {
	if limit == 0 {
		limit = 100
	}

	query := `
		SELECT id, tenant_id, user_id, action, entity, entity_id, before, after, status, error, ip_address, user_agent, created_at
		FROM audit_log
		WHERE tenant_id = $1 AND user_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := al.db.Query(ctx, query, tenantID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var beforeStr, afterStr sql.NullString

		rows.Scan(
			&event.ID,
			&event.TenantID,
			&event.UserID,
			&event.Action,
			&event.Entity,
			&event.EntityID,
			&beforeStr,
			&afterStr,
			&event.Status,
			&event.Error,
			&event.IPAddress,
			&event.UserAgent,
			&event.CreatedAt,
		)

		if beforeStr.Valid {
			json.Unmarshal([]byte(beforeStr.String), &event.Before)
		}
		if afterStr.Valid {
			json.Unmarshal([]byte(afterStr.String), &event.After)
		}

		events = append(events, event)
	}

	return events, nil
}

// GetAuditStats obtiene estadísticas de auditoría
func (al *AuditLogger) GetAuditStats(ctx context.Context, tenantID string, days int) (map[string]interface{}, error) {
	if days == 0 {
		days = 30
	}

	query := `
		SELECT 
			COUNT(*) as total_events,
			COUNT(DISTINCT user_id) as unique_users,
			COUNT(DISTINCT entity) as unique_entities,
			COUNT(CASE WHEN status = 'error' THEN 1 END) as error_count,
			COUNT(CASE WHEN action = 'DELETE' THEN 1 END) as delete_count,
			COUNT(CASE WHEN action = 'UPDATE' THEN 1 END) as update_count,
			COUNT(CASE WHEN action = 'CREATE' THEN 1 END) as create_count
		FROM audit_log
		WHERE tenant_id = $1 AND created_at > NOW() - INTERVAL '1 day' * $2
	`

	row := al.db.QueryRow(ctx, query, tenantID, days)

	var (
		totalEvents     int64
		uniqueUsers     int64
		uniqueEntities  int64
		errorCount      int64
		deleteCount     int64
		updateCount     int64
		createCount     int64
	)

	err := row.Scan(
		&totalEvents,
		&uniqueUsers,
		&uniqueEntities,
		&errorCount,
		&deleteCount,
		&updateCount,
		&createCount,
	)

	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	stats := map[string]interface{}{
		"total_events":    totalEvents,
		"unique_users":    uniqueUsers,
		"unique_entities": uniqueEntities,
		"error_count":     errorCount,
		"delete_count":    deleteCount,
		"update_count":    updateCount,
		"create_count":    createCount,
	}

	return stats, nil
}

// ExportAudit exporta auditoría a JSON/CSV
func (al *AuditLogger) ExportAudit(ctx context.Context, tenantID string, startDate, endDate time.Time) ([]byte, error) {
	query := `
		SELECT id, tenant_id, user_id, action, entity, entity_id, before, after, status, error, ip_address, user_agent, created_at
		FROM audit_log
		WHERE tenant_id = $1 AND created_at BETWEEN $2 AND $3
		ORDER BY created_at DESC
	`

	rows, err := al.db.Query(ctx, query, tenantID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var beforeStr, afterStr sql.NullString

		rows.Scan(
			&event.ID,
			&event.TenantID,
			&event.UserID,
			&event.Action,
			&event.Entity,
			&event.EntityID,
			&beforeStr,
			&afterStr,
			&event.Status,
			&event.Error,
			&event.IPAddress,
			&event.UserAgent,
			&event.CreatedAt,
		)

		if beforeStr.Valid {
			json.Unmarshal([]byte(beforeStr.String), &event.Before)
		}
		if afterStr.Valid {
			json.Unmarshal([]byte(afterStr.String), &event.After)
		}

		events = append(events, event)
	}

	return json.MarshalIndent(events, "", "  ")
}

// Helper function
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
