package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type SessionManager struct {
	jwtSecret        []byte
	jwtRefreshSecret []byte
}

type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	TenantID string `json:"tenant_id"`
	IsAdmin  bool   `json:"is_admin"`
	jwt.RegisteredClaims
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func NewSessionManager(jwtSecret, jwtRefreshSecret string) *SessionManager {
	return &SessionManager{
		jwtSecret:        []byte(jwtSecret),
		jwtRefreshSecret: []byte(jwtRefreshSecret),
	}
}

func (sm *SessionManager) CreateTokens(userID, username, tenantID string, isAdmin bool) (*TokenResponse, error) {
	now := time.Now()
	expiresIn := int64(3600) // 1 hora

	// Access token
	accessClaims := Claims{
		UserID:   userID,
		Username: username,
		TenantID: tenantID,
		IsAdmin:  isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expiresIn) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(sm.jwtSecret)
	if err != nil {
		return nil, err
	}

	// Refresh token (más largo plazo)
	refreshToken := generateRefreshToken()

	return &TokenResponse{
		AccessToken:  accessTokenString,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	}, nil
}

func (sm *SessionManager) ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return sm.jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}

	return claims, nil
}

// ExtractTenantIDFromCookie extrae el tenant_id del JWT en la cookie
func (sm *SessionManager) ExtractTenantIDFromCookie(r *http.Request) string {
	cookie, err := r.Cookie("access_token")
	if err != nil {
		return ""
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(cookie.Value, claims, func(token *jwt.Token) (interface{}, error) {
		return sm.jwtSecret, nil
	})

	if err != nil || !token.Valid {
		return ""
	}

	return claims.TenantID
}

func (sm *SessionManager) SetSessionCookie(w http.ResponseWriter, tokenResponse *TokenResponse) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    tokenResponse.AccessToken,
		MaxAge:   int(tokenResponse.ExpiresIn),
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    tokenResponse.RefreshToken,
		MaxAge:   86400 * 7, // 7 días
		HttpOnly: true,
		Path:     "/api/auth/refresh",
		SameSite: http.SameSiteLaxMode,
	})
}

func (sm *SessionManager) GetTokenFromRequest(r *http.Request) string {
	// Intenta header Authorization primero
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			return authHeader[7:]
		}
	}

	// Luego intenta cookie
	if cookie, err := r.Cookie("access_token"); err == nil {
		return cookie.Value
	}

	return ""
}

func generateRefreshToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (sm *SessionManager) ClearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    "",
		MaxAge:   -1,
		HttpOnly: true,
		Path:     "/",
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		MaxAge:   -1,
		HttpOnly: true,
		Path:     "/api/auth/refresh",
	})
}
