package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/fasterp/backend/db"
)

type AuditHandler struct {
	auditLogger *db.AuditLogger
}

func NewAuditHandler(auditLogger *db.AuditLogger) *AuditHandler {
	return &AuditHandler{
		auditLogger: auditLogger,
	}
}

// GetEntityAudit devuelve historial de auditoría de una entidad
func (ah *AuditHandler) GetEntityAudit(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	entityID := r.URL.Query().Get("entity_id")
	limitStr := r.URL.Query().Get("limit")

	if entityID == "" {
		http.Error(w, "entity_id required", http.StatusBadRequest)
		return
	}

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
		}
	}

	events, err := ah.auditLogger.GetAuditTrail(r.Context(), tenantID, entityID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entity_id": entityID,
		"total":     len(events),
		"events":    events,
	})
}

// GetUserAudit devuelve historial de actividad de un usuario
func (ah *AuditHandler) GetUserAudit(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	userID := r.URL.Query().Get("user_id")
	limitStr := r.URL.Query().Get("limit")

	if userID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
		}
	}

	events, err := ah.auditLogger.GetUserAudit(r.Context(), tenantID, userID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id": userID,
		"total":   len(events),
		"events":  events,
	})
}

// GetAuditStats devuelve estadísticas de auditoría
func (ah *AuditHandler) GetAuditStats(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	daysStr := r.URL.Query().Get("days")

	days := 30
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil {
			days = d
		}
	}

	stats, err := ah.auditLogger.GetAuditStats(r.Context(), tenantID, days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"period": days,
		"stats":  stats,
	})
}

// ExportAudit exporta auditoría en JSON
func (ah *AuditHandler) ExportAudit(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	startDate := time.Now().AddDate(0, -1, 0)
	endDate := time.Now()

	if startStr != "" {
		if t, err := time.Parse("2006-01-02", startStr); err == nil {
			startDate = t
		}
	}

	if endStr != "" {
		if t, err := time.Parse("2006-01-02", endStr); err == nil {
			endDate = t
		}
	}

	data, err := ah.auditLogger.ExportAudit(r.Context(), tenantID, startDate, endDate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=audit_export.json")
	w.Write(data)
}
