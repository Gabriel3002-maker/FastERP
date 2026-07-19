package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/fasterp/backend/db"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	db             *db.DB
	sessionManager *SessionManager
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Status       string `json:"status"`
	Message      string `json:"message"`
	UserID       string `json:"user_id"`
	TenantID     string `json:"tenant_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func NewAuthHandler(database *db.DB) *AuthHandler {
	return &AuthHandler{
		db:             database,
		sessionManager: NewSessionManager("dev-secret", "dev-secret-refresh"),
	}
}

func (ah *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parsear JSON o FormData
	var loginReq LoginRequest
	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == "" {
		json.NewDecoder(r.Body).Decode(&loginReq)
		username = loginReq.Username
		password = loginReq.Password
	}

	if username == "" || password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Username and password required",
		})
		return
	}

	// Buscar usuario (asumiendo tenant default por ahora)
	var userID, tenantID, passwordHash string
	var isAdmin bool

	ctx, cancel := context.WithTimeout(context.Background(), 5000000000)
	defer cancel()

	err := ah.db.QueryRow(ctx,
		`SELECT u.id, u.tenant_id, u.password_hash, u.is_admin
		 FROM users u
		 WHERE u.username = $1 AND u.active = true
		 LIMIT 1`,
		username).Scan(&userID, &tenantID, &passwordHash, &isAdmin)

	if err != nil {
		log.Printf("[Auth] Login failed for %s: user not found", username)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Invalid credentials",
		})
		return
	}

	// Validar password
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		log.Printf("[Auth] Login failed for %s: invalid password", username)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Invalid credentials",
		})
		return
	}

	// Crear JWT token
	tokenResp, err := ah.sessionManager.CreateTokens(userID, username, tenantID, isAdmin)
	if err != nil {
		log.Printf("[Auth] Error creating token: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Error creating token",
		})
		return
	}

	// Guardar en cookies
	ah.sessionManager.SetSessionCookie(w, tokenResp)

	log.Printf("[Auth] ✓ Login successful: %s", username)

	// Devolver respuesta con tokens
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(LoginResponse{
		Status:       "ok",
		Message:      "Login successful",
		UserID:       userID,
		TenantID:     tenantID,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
	})
}

func (ah *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// TODO: Invalidar session/JWT
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","message":"Logout successful"}`))
}
