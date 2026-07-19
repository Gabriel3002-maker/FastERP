package api

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fasterp/backend/internal/config"
	"github.com/fasterp/backend/internal/module"
	"github.com/gin-gonic/gin"
)

func SetupRouter(cfg *config.Config, modManager *module.ModuleManager, webDist embed.FS) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	h := NewHandler(modManager, cfg.JWTSecret, cfg.JWTRefreshSecret, r)

	// Security middleware
	r.Use(securityHeadersMiddleware())
	r.Use(corsMiddleware(cfg.CORSOrigins))
	r.Use(rateLimiterMiddleware(cfg.RateLimit))

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
		api.POST("/modules/install", h.InstallModule)
		api.POST("/modules/:name/uninstall", h.UninstallModule)
		api.POST("/modules/:name/toggle", h.ToggleModule)
		api.GET("/modules/:name/routes", h.ModuleRoutes)

		api.GET("/me", h.GetCurrentUser)
		api.GET("/menus", h.GetMenus)
		api.POST("/me/change-password", h.ChangePassword)
		api.POST("/me/logout", h.Logout)

		// Admin-only routes
		admin := api.Group("", h.AdminMiddleware())
		{
			admin.GET("/tenants", h.ListTenants)
			admin.GET("/tenants/:id", h.GetTenant)
		}

		// Auto-generated module CRUD: /api/{module}/{model}[/{id}].
		// Registered last so the static routes above win; gin resolves static path
		// segments ahead of wildcards.
		h.RegisterModuleDataRoutes(api)
	}

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "timestamp": time.Now().Unix()})
	})

	// Custom routes declared in module manifests. These use module-chosen paths, so
	// unlike model CRUD they cannot go through a single dispatcher. They are bound
	// once here, at startup, and never after the engine begins serving — a module
	// installed at runtime needs a restart before its custom routes resolve.
	for _, inst := range module.Global.All() {
		if h.trackedRoutes[inst.Manifest.Name] == nil {
			h.trackedRoutes[inst.Manifest.Name] = make(map[string]bool)
		}
		for _, route := range inst.Routes {
			key := route.Method + ":" + route.Path
			if h.trackedRoutes[inst.Manifest.Name][key] {
				continue
			}
			h.trackedRoutes[inst.Manifest.Name][key] = true

			handler := route.Handler
			if handler == nil {
				handler = h.wasmModuleHandler(inst.Manifest.Name, route.Path)
			}
			r.Handle(route.Method, route.Path, handler)
		}
	}

	// Native Odoo integration endpoints (WASM sandbox cannot do network I/O)
	h.RegisterOdooRoutes()

	// Mini-store (tienda_web): product enrichment + image uploads
	h.RegisterStoreRoutes()

	// Public site host (sitio_web): CMS pages + public storefront feed
	h.RegisterWebRoutes()

	// SPA fallback: serve embedded frontend assets or index.html for SPA routing
	if distFS, err := fs.Sub(webDist, "webdist"); err == nil {
		r.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path
			if strings.HasPrefix(path, "/api") || strings.HasPrefix(path, "/uploads") || strings.HasPrefix(path, "/site") || strings.HasPrefix(path, "/health") {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
				return
			}
			if file, err := distFS.Open(path); err == nil {
				file.Close()
				c.FileFromFS(path, http.FS(distFS))
				return
			}
			c.FileFromFS("index.html", http.FS(distFS))
		})
	}

	return r
}

func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		c.Next()
	}
}

func corsMiddleware(origins string) gin.HandlerFunc {
	allowedOrigins := strings.Split(origins, ",")
	for i := range allowedOrigins {
		allowedOrigins[i] = strings.TrimSpace(allowedOrigins[i])
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := false
		for _, o := range allowedOrigins {
			if o == "*" || o == origin {
				allowed = true
				break
			}
		}
		if allowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Tenant-ID")
			c.Header("Access-Control-Expose-Headers", "Content-Disposition")
			c.Header("Access-Control-Max-Age", "86400")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
}

type visitor struct {
	count    int
	lastSeen time.Time
}

func rateLimiterMiddleware(maxRequests int) gin.HandlerFunc {
	limiter := &rateLimiter{
		visitors: make(map[string]*visitor),
	}

	go func() {
		for {
			time.Sleep(1 * time.Minute)
			limiter.mu.Lock()
			for ip, v := range limiter.visitors {
				if time.Since(v.lastSeen) > 1*time.Minute {
					delete(limiter.visitors, ip)
				}
			}
			limiter.mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/health") {
			c.Next()
			return
		}

		ip := c.ClientIP()
		if ip == "" {
			ip = c.Request.RemoteAddr
		}

		limiter.mu.Lock()
		v, exists := limiter.visitors[ip]
		if !exists {
			limiter.visitors[ip] = &visitor{count: 1, lastSeen: time.Now()}
			limiter.mu.Unlock()
			c.Next()
			return
		}

		if time.Since(v.lastSeen) > 1*time.Minute {
			v.count = 1
			v.lastSeen = time.Now()
			limiter.mu.Unlock()
			c.Next()
			return
		}

		v.count++
		v.lastSeen = time.Now()
		if v.count > maxRequests {
			limiter.mu.Unlock()
			log.Printf("[RateLimit] IP %s exceeded limit (%d/min)", ip, maxRequests)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded. try again later"})
			return
		}
		limiter.mu.Unlock()
		c.Next()
	}
}
