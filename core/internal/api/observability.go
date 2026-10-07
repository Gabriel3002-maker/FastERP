package api

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Observabilidad: access log y métricas.
//
// Los dos se montan justo después de Recovery y antes de todo lo demás, para
// que midan también lo que se descarta antes de llegar a los handlers: una
// petición rechazada por el rate limit o por CORS sigue siendo una petición.

// Métricas a nivel de paquete y registradas una sola vez en init().
//
// No se registran dentro de SetupRouter: los tests montan el router varias
// veces, y volver a registrar el mismo Collector en el DefaultRegisterer
// panica. El middleware solo incrementa vars, así que montar N routers es
// inocuo.
var (
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fasterp_http_requests_total",
		Help: "Peticiones HTTP servidas, por método, ruta y código de estado.",
	}, []string{"method", "path", "status"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fasterp_http_request_duration_seconds",
		Help:    "Duración de las peticiones HTTP en segundos.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"method", "path"})
)

func init() {
	// Solo los dos nuestros. Los de Go y del proceso (go_*, process_*) ya los
	// registra client_golang en su propio init(); volver a hacerlo con
	// collectors.NewGoCollector() panica con "duplicate metrics collector
	// registration attempted" al arrancar.
	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration)
}

// registerMetricsRoute publica /metrics en el formato Prometheus.
//
// Sin autenticación a propósito: el scraper no lleva JWT. El docker-compose
// publica el puerto solo en 127.0.0.1, así que fuera de una red interna hay
// que abrirlo a mano. Si expones el core a Internet, pon delante un proxy que
// filtre esta ruta.
func registerMetricsRoute(r *gin.Engine) {
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
}

// routeLabel devuelve la plantilla de la ruta (`/api/:module/:model`), no la
// ruta concreta. Etiquetar con la ruta literal convertiría cada id de la base
// en una serie distinta y el cardinal de las métricas crecería sin límite con
// cada fila nueva.
func routeLabel(c *gin.Context) string {
	if p := c.FullPath(); p != "" {
		return p
	}
	// 404 y 405 no tienen plantilla: se agrupan en una sola serie.
	return "unmatched"
}

func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		method := c.Request.Method
		route := routeLabel(c)
		status := strconv.Itoa(c.Writer.Status())
		httpRequestsTotal.WithLabelValues(method, route, status).Inc()
		httpRequestDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	}
}

// skipLoggedPath quita del access log lo que se repite por segundos.
//
// /health y /readyz los sondea el orquestador cada pocos segundos, y /metrics
// el scraper: registrarlos rellena el log sin añadir nada que no esté ya en las
// propias métricas. /static es el bundle del admin, con decenas de peticiones
// por carga de página.
//
// Es una función y no LoggerConfig.SkipPaths porque esos campos comparan la
// ruta exacta: no sirven para el prefijo /static/.
func skipLoggedPath(c *gin.Context) bool {
	switch c.Request.URL.Path {
	case "/health", "/readyz", "/metrics":
		return true
	}
	return strings.HasPrefix(c.Request.URL.Path, "/static/")
}

// accessLogMiddleware escribe una línea por petición, después de terminar.
//
// Está escrito a mano y no con gin.LoggerWithConfig. Lo que aporta cada uno:
// LoggerWithConfig hace el filtro solo con rutas exactas, no deja leer el
// tenant del contexto salvo por el mapa de Keys, y en v1.9.1 ni siquiera
// expone el contexto. Aquí el filtro es de verdad y la línea sale por el mismo
// log estándar que ya usan [FATAL], [RateLimit] y [API], así que todo el
// proceso queda en un único stream con un único formato.
func accessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if skipLoggedPath(c) {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		latency := time.Since(start).Round(time.Microsecond)
		status := c.Writer.Status()

		var b strings.Builder
		fmt.Fprintf(&b, "[REQ] %d | %v | %s | %s %s", status, latency, c.ClientIP(), c.Request.Method, c.Request.URL.RequestURI())

		if v, ok := c.Get("tenant_id"); ok {
			fmt.Fprintf(&b, " | tenant=%v", v)
		}
		if v, ok := c.Get("username"); ok {
			fmt.Fprintf(&b, " | user=%v", v)
		}
		if errs := c.Errors.String(); errs != "" {
			fmt.Fprintf(&b, " | error=%s", strings.ReplaceAll(strings.TrimSpace(errs), "\n", "; "))
		}

		log.Print(b.String())
	}
}
