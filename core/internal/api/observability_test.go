package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// cuentaReq devuelve el valor del contador para un rótulo concreto. WithLabelValues
// crea la serie si no existe, así que devuelve 0 la primera vez.
func cuentaReq(method, path, status string) float64 {
	return testutil.ToFloat64(httpRequestsTotal.WithLabelValues(method, path, status))
}

func TestMetricsMiddlewareCuentaPeticiones(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(metricsMiddleware())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	antes := cuentaReq("GET", "/x", "200")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("esperado 200, got %d", w.Code)
	}
	if despues := cuentaReq("GET", "/x", "200"); despues-antes != 1 {
		t.Fatalf("el contador de /x no avanzó: antes=%v despues=%v", antes, despues)
	}
}

// Las métricas etiquetan con la plantilla, nunca con la ruta literal: cada id
// de la base es una fila distinta y etiquetar con ella multiplica las series
// por el tamaño de la tabla.
func TestMetricsUsaLaPlantillaDeLaRuta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(metricsMiddleware())
	r.GET("/api/:module/:model", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	antes := cuentaReq("GET", "/api/:module/:model", "200")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/inmobiliaria/propiedades", nil))
	if despues := cuentaReq("GET", "/api/:module/:model", "200"); despues-antes != 1 {
		t.Fatalf("esperaba medir /api/:module/:model, antes=%v despues=%v", antes, despues)
	}
}

// Las rutas sin plantilla (404, 405) no tienen plantilla: se agrupan en una
// sola serie en vez de escapar con la ruta cruda.
func TestMetricsAgrupaLasRutasSinPlantilla(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(metricsMiddleware())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	antes := cuentaReq("GET", "unmatched", "404")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/no-existe", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("esperado 404, got %d", w.Code)
	}
	if despues := cuentaReq("GET", "unmatched", "404"); despues-antes != 1 {
		t.Fatalf("el 404 no se agrupó en 'unmatched': antes=%v despues=%v", antes, despues)
	}
}

func TestRutaMetricsResponde(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(metricsMiddleware())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	registerMetricsRoute(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/metrics: esperado 200, got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "fasterp_http_requests") {
		t.Fatalf("/metrics no expone fasterp_http_requests:\n%s", body)
	}
}

func TestSkipLoggedPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	casos := []struct {
		path   string
		saltar bool
	}{
		{"/health", true},
		{"/readyz", true},
		{"/metrics", true},
		{"/static/admin/bundle.js", true},
		{"/static", false},
		{"/api/me", false},
		{"/login", false},
	}
	for _, c := range casos {
		r := gin.New()
		var got bool
		r.GET("/*p", func(ctx *gin.Context) { got = skipLoggedPath(ctx) })
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", c.path, nil))
		if got != c.saltar {
			t.Errorf("skipLoggedPath(%q) = %v, esperado %v", c.path, got, c.saltar)
		}
	}
}

func TestAccessLogSaltaSondeosYRegistraElResto(t *testing.T) {
	var buf bytes.Buffer
	anterior := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(anterior)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(accessLogMiddleware())
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	buf.Reset()
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health", nil))
	if salida := buf.String(); strings.Contains(salida, "/health") {
		t.Fatalf("/health no debería registrarse, salió: %s", salida)
	}

	buf.Reset()
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	salida := buf.String()
	if !strings.Contains(salida, "[REQ] 200") {
		t.Fatalf("falta el código de estado en la línea de acceso: %s", salida)
	}
	if !strings.Contains(salida, "GET /x") {
		t.Fatalf("falta método y ruta en la línea de acceso: %s", salida)
	}
}
