package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fasterp/backend/internal/config"
	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
	"github.com/gin-gonic/gin"
)

// maxRequestBody acota el cuerpo de las peticiones que no son de subida. La
// Biggest ruta de subida real es multipart con imágenes, y va en su propio
// grupo; el resto de la API recibe JSON de un módulo, y 8 MiB de sobra.
const maxRequestBody = 8 << 20

func SetupRouter(cfg *config.Config, modManager *module.ModuleManager) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// gin trusts X-Forwarded-For from ANY address unless told otherwise, and the
	// rate limiter keys on that value. Left at the default, an attacker rotates
	// the header and gets an unlimited supply of attempts against /api/auth/login.
	// When a reverse proxy is actually in front, set FASTERP_TRUSTED_PROXIES to
	// its CIDRs; with none set, only the peer address counts.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Fatalf("[FATAL] FASTERP_TRUSTED_PROXIES is invalid: %v", err)
	}

	h := NewHandler(modManager, cfg.JWTSecret, cfg.JWTRefreshSecret, r)

	renderer, err := NewRenderer(cfg.TemplatesDir)
	if err != nil {
		log.Fatalf("[FATAL] no se pudieron cargar las plantillas de %s: %v", cfg.TemplatesDir, err)
	}

	// Security middleware
	//
	// Van antes de registrar la UI a propósito. En gin los middleware se atan a la
	// ruta en el momento de registrarla, así que una ruta registrada antes de un
	// Use() se queda sin él: registrar la UI primero dejaba a /admin, /login y
	// /static sin CSP, sin nosniff y sin X-Frame-Options, que es justo lo que
	// protege la sesión contra clickjacking.
	r.Use(securityHeadersMiddleware())
	r.Use(corsMiddleware(cfg.CORSOrigins))
	r.Use(rateLimiterMiddleware(cfg.RateLimit))

	NewUI(renderer, cfg.ModulesDir, cfg.StaticDir).RegisterRoutes(r)

	// Public routes (tenant-resolved)
	// Credential endpoints get a far tighter budget than the global limit: they are
	// the brute-force surface, and a per-IP allowance sized for normal API traffic
	// is not one.
	authPublic := r.Group("/api/auth", rateLimiterMiddleware(cfg.AuthRateLimit))
	authPublic.Use(h.TenantMiddleware())
	{
		authPublic.POST("/login", h.Login)
		authPublic.POST("/refresh", h.RefreshToken)
	}

	// Protected routes
	api := r.Group("/api", h.TenantMiddleware(), h.AuthMiddleware())
	{
		api.GET("/modules", h.ListModules)
		api.GET("/me", h.GetCurrentUser)
		api.GET("/menus", h.GetMenus)
		api.POST("/me/change-password", h.ChangePassword)
		api.POST("/me/logout", h.Logout)

		// Admin-only routes
		admin := api.Group("", h.AdminMiddleware())
		{
			admin.GET("/tenants", h.ListTenants)
			admin.GET("/tenants/:id", h.GetTenant)
			backupLimiter := rateLimiterMiddleware(cfg.BackupRateLimit)
			admin.GET("/tenants/:id/backup", backupLimiter, h.Backup)
			admin.POST("/tenants/:id/restore", backupLimiter, h.Restore)

			// Installing a module writes its schema into the database and
			// executes code inside this process. That is an administrative
			// action: any authenticated user must not reach it, or a normal
			// account can escalate to admin with a single upload.
			admin.POST("/modules/install", h.InstallModule)
			admin.POST("/modules/:name/uninstall", h.UninstallModule)
			admin.POST("/modules/:name/toggle", h.ToggleModule)
			admin.GET("/modules/:name/routes", h.ModuleRoutes)
		}

		// Auto-generated module CRUD: /api/{module}/{model}[/{id}].
		// Registered last so the static routes above win; gin resolves static path
		// segments ahead of wildcards.
		h.RegisterModuleDataRoutes(api)
	}

	// Liveness: el proceso responde. No toca la base a propósito — si Postgres
	// cae, reiniciar el backend no arregla nada y solo genera un bucle de reinicios.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "timestamp": time.Now().Unix()})
	})

	// Readiness: el proceso sirve tráfico de verdad. Aquí sí se consulta la base,
	// porque a diferencia del anterior un "sí" sin base significa.acceptar
	// peticiones que van a fallar una por una.
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		if err := db.DB.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "database": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	// Rutas propias de un módulo.
	//
	// Ninguna se puede servir: un módulo ya no es un binario, así que no hay
	// código que responder a una ruta que el módulo declare. Se avisa en vez de
	// ignorarlas en silencio, porque un módulo con "routes" está pidiendo algo
	// que el core ya no hace y conviene que se note al arrancar.
	//
	// Lo que un módulo necesite más allá del CRUD va en Go nativo en
	// internal/api, no en el manifest. Por eso RegisterStoreRoutes y
	// RegisterWebRoutes, más abajo: los endpoints de sitio_web y tienda_web son
	// código del core, y el manifest solo los documenta.
	for _, inst := range module.Global.All() {
		for _, route := range inst.Routes {
			log.Printf("[API] el módulo %s declara la ruta %s %s, pero un módulo ya no puede registrar rutas: muévela a internal/api",
				inst.Manifest.Name, route.Method, route.Path)
		}
	}

	// Mini-store (tienda_web): product enrichment + image uploads
	h.RegisterStoreRoutes()

	// Public site host (sitio_web): CMS pages + public storefront feed
	h.RegisterWebRoutes()

	return r
}

func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-XSS-Protection", "1; mode=block")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		// El admin es una SPA que carga su propio bundle y sirve ficheros subidos
		// por el usuario. Un XSS aquí se lleva la sesión, y con ella el token
		// guardado, así que la CSP es la única contención real.
		//
		// Se construye sin nonce: esta es la política para todo lo que no es una
		// página (JSON, assets, descargas). El renderer sustituye esta cabecera por
		// una con nonce cuando responde HTML, porque las páginas llevan scripts
		// inline. 'unsafe-inline' en style es deliberado: el cliente inyecta los
		// estilos de los módulos que carga.
		h.Set("Content-Security-Policy", cspHeader(""))

		// Sin esto, ShouldBindJSON lee el cuerpo entero en memoria: un POST con
		// Content-Length enorme contra cualquier ruta JSON es un OOM.
		if c.Request.Body != nil && c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBody)
		}
		c.Next()
	}
}

// corsMiddleware solo devuelve cabeceras CORS cuando el Origin está en la lista.
//
// Con "*" en la configuración se devolvía el Origin que trae la petición, lo que
// convierte cualquier sitio en un origen de confianza: permite credenciales y,
// si además hay cookies, lectura de la respuesta. Un comodín no es "permite
// todo", es "permite a cualquiera". Se avisa y no se concede nada.
func corsMiddleware(origins string) gin.HandlerFunc {
	allowedOrigins := make(map[string]bool, 8)
	wildcard := false
	for _, o := range strings.Split(origins, ",") {
		o = strings.TrimSpace(o)
		switch {
		case o == "":
		case o == "*":
			wildcard = true
		default:
			allowedOrigins[o] = true
		}
	}
	if wildcard {
		log.Printf("[WARN] FASTERP_CORS_ORIGINS contiene '*': no se envió ninguna cabecera CORS. Lista los orígenes reales.")
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowedOrigins[origin] {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Tenant-ID")
			h.Set("Access-Control-Expose-Headers", "Content-Disposition")
			h.Set("Access-Control-Max-Age", "86400")
			// Sin esto, una respuesta cacheada para un origen se puede servir a
			// otro desde la caché compartida.
			h.Set("Vary", "Origin")
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// rateLimiter cuenta peticiones por IP en una ventana fija de un minuto.
//
// Vive en memoria del proceso: con una sola instancia es correcto, y con varias
// el límite efectivo se multiplica por el número de réplicas. Es una decisión
// consciente, no un olvido — para un límite compartido haría falta Redis.
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
}

type visitor struct {
	count    int
	lastSeen time.Time
}

const rateLimitWindow = time.Minute

// maxTrackedVisitors acota el mapa. Sin tope, cada IP distinta vive hasta un
// minuto y un barrido de IPs de Botnet lo infla sin límite.
const maxTrackedVisitors = 50000

// sweeperOnce arranca un único barrido para todos los limiters del proceso.
// Antes cada llamada a SetupRouter lanzaba su propia goroutine en bucle sin fin,
// así que montar el router N veces —en los tests, en un reload— dejaba N
// barridos vivos para siempre.
var (
	sweeperOnce sync.Once
	sweeperMu   sync.Mutex
	sweepers    []*rateLimiter
)

func registerSweeper(l *rateLimiter) {
	sweeperMu.Lock()
	sweepers = append(sweepers, l)
	sweeperMu.Unlock()

	sweeperOnce.Do(func() {
		go func() {
			t := time.NewTicker(rateLimitWindow)
			defer t.Stop()
			for range t.C {
				sweeperMu.Lock()
				list := append([]*rateLimiter(nil), sweepers...)
				sweeperMu.Unlock()
				for _, l := range list {
					l.mu.Lock()
					for ip, v := range l.visitors {
						if time.Since(v.lastSeen) > rateLimitWindow {
							delete(l.visitors, ip)
						}
					}
					l.mu.Unlock()
				}
			}
		}()
	})
}

func rateLimiterMiddleware(maxRequests int) gin.HandlerFunc {
	limiter := &rateLimiter{
		visitors: make(map[string]*visitor),
	}
	registerSweeper(limiter)

	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/health") {
			c.Next()
			return
		}

		// ClientIP() respeta SetTrustedProxies: con la lista vacía devuelve la
		// dirección del peer, no un X-Forwarded-For que el cliente controle.
		ip := c.ClientIP()
		if ip == "" {
			ip = c.Request.RemoteAddr
		}

		now := time.Now()

		limiter.mu.Lock()
		v, exists := limiter.visitors[ip]
		switch {
		case !exists:
			if len(limiter.visitors) >= maxTrackedVisitors {
				// En vez de seguir creciendo, se caduca lo que lleva más rato
				// parado: lo que se acaba de meter no puede seguir creciendo.
				for k, ev := range limiter.visitors {
					if now.Sub(ev.lastSeen) > rateLimitWindow {
						delete(limiter.visitors, k)
					}
				}
			}
			limiter.visitors[ip] = &visitor{count: 1, lastSeen: now}
			limiter.mu.Unlock()
			c.Next()

		case now.Sub(v.lastSeen) > rateLimitWindow:
			v.count = 1
			v.lastSeen = now
			limiter.mu.Unlock()
			c.Next()

		default:
			v.count++
			v.lastSeen = now
			exceeded := v.count > maxRequests
			limiter.mu.Unlock()
			if exceeded {
				log.Printf("[RateLimit] IP %s excedió el límite (%d/min)", ip, maxRequests)
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "demasiadas peticiones; inténtalo en un minuto"})
				return
			}
			c.Next()
		}
	}
}
