package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-not-used-in-production"

func init() { gin.SetMode(gin.TestMode) }

// signToken builds an access token with the given claims overridden.
func signToken(t *testing.T, overrides map[string]interface{}) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":       "user-1",
		"tenant_id": "tenant-1",
		"username":  "admin",
		"is_admin":  true,
		"type":      "access",
		"iat":       time.Now().Unix(),
		"exp":       time.Now().Add(15 * time.Minute).Unix(),
	}
	for k, v := range overrides {
		claims[k] = v
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

// runAuth sends a request through AuthMiddleware with tenant_id already set in the
// context, mimicking what TenantMiddleware does ahead of it.
func runAuth(t *testing.T, authHeader, contextTenant string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(nil, testSecret, testSecret+"-refresh", gin.New())

	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		if contextTenant != "" {
			c.Set("tenant_id", contextTenant)
		}
		c.Next()
	}, h.AuthMiddleware(), func(c *gin.Context) {
		c.String(http.StatusOK, "reached")
	})

	req := httptest.NewRequest("GET", "/x", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddlewareAcceptsValidToken(t *testing.T) {
	w := runAuth(t, "Bearer "+signToken(t, nil), "tenant-1")
	if w.Code != http.StatusOK || w.Body.String() != "reached" {
		t.Fatalf("got %d %q, want 200 \"reached\"", w.Code, w.Body.String())
	}
}

// A token minted for one tenant must not be usable against another, even though both
// tenants are real: the pinned RLS connection belongs to the header's tenant.
func TestAuthMiddlewareRejectsCrossTenantToken(t *testing.T) {
	w := runAuth(t, "Bearer "+signToken(t, nil), "tenant-2")
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", w.Code)
	}
}

func TestAuthMiddlewareRejectsBadTokens(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"missing header", ""},
		{"garbage", "Bearer not-a-jwt"},
		{"refresh token used as access", "Bearer " + signToken(t, map[string]interface{}{"type": "refresh"})},
		{"expired", "Bearer " + signToken(t, map[string]interface{}{"exp": time.Now().Add(-time.Minute).Unix()})},
		{"no tenant claim", "Bearer " + signToken(t, map[string]interface{}{"tenant_id": ""})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := runAuth(t, tc.header, "tenant-1"); w.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", w.Code)
			}
		})
	}
}

// A token signed with a different key must never be accepted, which is what an
// "alg: none" or wrong-secret forgery attempt looks like.
func TestAuthMiddlewareRejectsForeignSignature(t *testing.T) {
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "tenant_id": "tenant-1", "type": "access",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("attacker-secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if w := runAuth(t, "Bearer "+forged, "tenant-1"); w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", w.Code)
	}
}

func TestAdminMiddleware(t *testing.T) {
	cases := []struct {
		name    string
		isAdmin interface{}
		set     bool
		want    int
	}{
		{"admin", true, true, http.StatusOK},
		{"non-admin", false, true, http.StatusForbidden},
		{"claim absent", nil, false, http.StatusForbidden},
		{"non-boolean claim", "true", true, http.StatusForbidden},
	}

	h := NewHandler(nil, testSecret, testSecret+"-refresh", gin.New())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/x", func(c *gin.Context) {
				if tc.set {
					c.Set("is_admin", tc.isAdmin)
				}
				c.Next()
			}, h.AdminMiddleware(), func(c *gin.Context) {
				c.String(http.StatusOK, "reached")
			})

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestInternalErrorHidesDriverDetail(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		internalError(c, "test op", errDetailed{})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", w.Code)
	}
	if body := w.Body.String(); body != `{"error":"internal server error"}` {
		t.Fatalf("driver detail leaked to client: %s", body)
	}
}

type errDetailed struct{}

func (errDetailed) Error() string {
	return `pq: column "password_hash" of relation "users" does not exist`
}
