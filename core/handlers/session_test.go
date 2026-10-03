package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSessionManagerRoundTrip(t *testing.T) {
	sm := NewSessionManager("test-secret", "test-refresh-secret")

	tok, err := sm.CreateTokens("user-1", "alice", "tenant-1", false)
	if err != nil {
		t.Fatalf("CreateTokens: %v", err)
	}

	claims, err := sm.ValidateToken(tok.AccessToken)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.UserID != "user-1" || claims.TenantID != "tenant-1" || claims.IsAdmin {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestSessionManagerRejectsForeignSignature(t *testing.T) {
	issuer := NewSessionManager("secret-a", "refresh-a")
	verifier := NewSessionManager("secret-b", "refresh-b")

	tok, err := issuer.CreateTokens("user-1", "alice", "tenant-1", true)
	if err != nil {
		t.Fatalf("CreateTokens: %v", err)
	}

	if _, err := verifier.ValidateToken(tok.AccessToken); err == nil {
		t.Fatal("expected ValidateToken to reject a token signed with a different secret")
	}
}

func TestSessionManagerRejectsExpiredToken(t *testing.T) {
	sm := NewSessionManager("test-secret", "test-refresh-secret")

	expired := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		UserID:   "user-1",
		TenantID: "tenant-1",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	})
	tokStr, err := expired.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}

	if _, err := sm.ValidateToken(tokStr); err == nil {
		t.Fatal("expected ValidateToken to reject an expired token")
	}
}

func TestGetTokenFromRequestPrefersAuthorizationHeader(t *testing.T) {
	sm := NewSessionManager("s", "r")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer header-token")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: "cookie-token"})

	if got := sm.GetTokenFromRequest(req); got != "header-token" {
		t.Fatalf("expected header token to win, got %q", got)
	}
}

// TestAuthHandlerRejectsTokensSignedWithARepoSecret fija la corrección del
// secreto de firma. NewAuthHandler lo toma de la configuración, que lo lee del
// entorno; antes usaba el literal "dev-secret", commiteado en este repo.
//
// Con el valor de aquella época en mano, cualquiera podía firmar un token con
// IsAdmin:true para cualquier tenant y pasar el requireAdmin de StudioHandler,
// que escribe ficheros de módulo en disco. Este test falla el día que el secreto
// vuelva a estar fijado en el código, que es justo lo que hay que evitar.
func TestAuthHandlerRejectsTokensSignedWithARepoSecret(t *testing.T) {
	t.Setenv("FASTERP_JWT_SECRET", "un-secreto-propio-de-este-entorno-de-prueba")
	t.Setenv("FASTERP_JWT_REFRESH_SECRET", "otro-secreto-propio-de-prueba-distinto")

	ah := NewAuthHandler(nil)

	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		UserID:   "attacker",
		TenantID: "any-tenant-id",
		IsAdmin:  true,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	forgedStr, err := forged.SignedString([]byte("dev-secret"))
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}

	claims, err := ah.SessionManager().ValidateToken(forgedStr)
	if err == nil {
		t.Fatalf("un token firmado con 'dev-secret' sigue siendo válido (claims: %+v): "+
			"el secreto de firma volvió a estar en el código", claims)
	}
}
