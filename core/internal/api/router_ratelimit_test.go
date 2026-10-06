package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRateLimiterMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(rateLimiterMiddleware(3))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	for i := 1; i <= 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
		if w.Code != 200 {
			t.Fatalf("petición %d: esperado 200, got %d", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("4ª petición: esperado 429, got %d", w.Code)
	}
}

func TestRateLimiterSinContarHealthcheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(rateLimiterMiddleware(1))
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
		if w.Code != 200 {
			t.Fatalf("/health no debería contar para el límite, got %d", w.Code)
		}
	}
}
