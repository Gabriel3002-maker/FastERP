package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/fasterp/backend/sdk"
)

type ChatterHandler struct {
	db *sql.DB
}

func NewChatterHandler(db *sql.DB) *ChatterHandler {
	return &ChatterHandler{db: db}
}

// Users devuelve la lista de usuarios del tenant para autocomplete de @mentions
func (h *ChatterHandler) Users(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		http.Error(w, "X-Tenant-ID required", http.StatusBadRequest)
		return
	}

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, username, COALESCE(username, '') as name
		 FROM users
		 WHERE tenant_id = $1 AND active = true
		 ORDER BY username ASC
		 LIMIT $2`, tenantID, sdk.MaxExternalLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.Name); err != nil {
			continue
		}
		if u.Name != "" {
			users = append(users, u)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": users,
	})
}

// Notifications devuelve las notificaciones de un usuario
func (h *ChatterHandler) Notifications(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	userID := r.URL.Query().Get("user_id")
	unreadOnly := r.URL.Query().Get("unread") == "true"
	limitStr := r.URL.Query().Get("limit")

	if tenantID == "" {
		http.Error(w, "X-Tenant-ID required", http.StatusBadRequest)
		return
	}
	if userID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = sdk.ClampExternalLimit(l, 50)
		}
	}

	query := `SELECT id, user_id, user_name, type, message, message_id,
	                 record_model, record_id, read, author_name, created_at
	          FROM mod_chatter_notification
	          WHERE tenant_id = $1 AND user_id = $2`
	args := []interface{}{tenantID, userID}

	if unreadOnly {
		query += ` AND read = false`
	}

	query += ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Notification struct {
		ID          string `json:"id"`
		UserID      string `json:"user_id"`
		UserName    string `json:"user_name"`
		Type        string `json:"type"`
		Message     string `json:"message"`
		MessageID   string `json:"message_id"`
		RecordModel string `json:"record_model"`
		RecordID    string `json:"record_id"`
		Read        bool   `json:"read"`
		AuthorName  string `json:"author_name"`
		CreatedAt   string `json:"created_at"`
	}

	notifications := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.UserName, &n.Type,
			&n.Message, &n.MessageID, &n.RecordModel, &n.RecordID,
			&n.Read, &n.AuthorName, &n.CreatedAt); err != nil {
			continue
		}
		notifications = append(notifications, n)
	}

	// Contar sin leer
	var unreadCount int
	h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM mod_chatter_notification
		 WHERE tenant_id = $1 AND user_id = $2 AND read = false`,
		tenantID, userID).Scan(&unreadCount)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":         notifications,
		"total":        len(notifications),
		"unread_count": unreadCount,
	})
}

// MarkRead marca notificaciones como leídas
func (h *ChatterHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		http.Error(w, "X-Tenant-ID required", http.StatusBadRequest)
		return
	}

	var req struct {
		UserID string   `json:"user_id"`
		IDs    []string `json:"ids"` // IDs específicos, o vacío = marcar todas
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	if len(req.IDs) > 0 {
		// Marcar IDs específicos
		for _, id := range req.IDs {
			h.db.ExecContext(r.Context(),
				`UPDATE mod_chatter_notification SET read = true
				 WHERE id = $1 AND tenant_id = $2 AND user_id = $3`,
				id, tenantID, req.UserID)
		}
	} else {
		// Marcar todas como leídas
		h.db.ExecContext(r.Context(),
			`UPDATE mod_chatter_notification SET read = true
			 WHERE tenant_id = $1 AND user_id = $2 AND read = false`,
			tenantID, req.UserID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
