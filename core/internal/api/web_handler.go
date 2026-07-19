package api

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/store"
	"github.com/fasterp/backend/internal/web"
)

// RegisterWebRoutes wires the PUBLIC site host (no auth): the CMS pages at
// /site[/:slug] and the storefront feed at /api/public/products. Page editing
// itself uses the module's authed CRUD (/api/sitio_web/web_page).
func (h *Handler) mediaDir() string {
	return filepath.Join(h.modManager.UploadDir, "media")
}

var allowedMediaExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".svg": true,
}

func mimeFromExt(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func (h *Handler) RegisterWebRoutes() {
	h.router.GET("/site", h.PublicHome)
	h.router.GET("/site/:slug", h.PublicPage)
	h.router.GET("/api/public/products", h.PublicProducts)

	os.MkdirAll(h.mediaDir(), 0755)
	h.router.Static("/uploads/media", h.mediaDir())

	g := h.router.Group("/api/web", h.TenantMiddleware(), h.AuthMiddleware())
	{
		g.GET("/media", h.WebListMedia)
		g.POST("/media", h.WebUploadMedia)
		g.DELETE("/media/:id", h.WebDeleteMedia)
	}
}

// defaultTenantID resolves the tenant that owns the public site. For now this is
// the "default" tenant; multi-tenant hosting would resolve by subdomain/slug.
func (h *Handler) defaultTenantID(c *gin.Context) string {
	if slug := c.Query("t"); slug != "" {
		var id string
		if err := db.DB.QueryRow(`SELECT id FROM tenants WHERE slug = $1 AND active = true`, slug).Scan(&id); err == nil {
			return id
		}
	}
	var id string
	_ = db.DB.QueryRow(`SELECT id FROM tenants WHERE slug = 'default' AND active = true`).Scan(&id)
	return id
}

func (h *Handler) PublicHome(c *gin.Context) {
	h.renderPage(c, "")
}

func (h *Handler) PublicPage(c *gin.Context) {
	h.renderPage(c, c.Param("slug"))
}

func (h *Handler) renderPage(c *gin.Context, slug string) {
	tenantID := h.defaultTenantID(c)
	if tenantID == "" {
		c.String(http.StatusNotFound, "site not found")
		return
	}
	conn, err := db.AcquireConn(c.Request.Context(), tenantID)
	if err != nil {
		c.String(http.StatusInternalServerError, "error")
		return
	}
	defer conn.Close()

	var page *web.Page
	if slug == "" {
		page, err = web.GetHome(c.Request.Context(), conn, tenantID)
	} else {
		page, err = web.GetBySlug(c.Request.Context(), conn, tenantID, slug)
	}
	if errors.Is(err, web.ErrNotFound) {
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte(notFoundHTML))
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "error")
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderDocument(page)))
}

// renderDocument composes a full HTML document from the page's html/css/js.
// The body/CSS/JS are authored by the site owner and rendered verbatim (this is
// a CMS by design); only the <title> is escaped so it can't break out of the tag.
func renderDocument(p *web.Page) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>%s</style>
</head>
<body>
%s
<script>%s</script>
</body>
</html>`, html.EscapeString(p.Title), p.CSS, p.HTML, p.JS)
}

// PublicProducts returns published store products for storefront pages (no auth).
func (h *Handler) PublicProducts(c *gin.Context) {
	tenantID := h.defaultTenantID(c)
	if tenantID == "" {
		c.JSON(http.StatusOK, []any{})
		return
	}
	conn, err := db.AcquireConn(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error"})
		return
	}
	defer conn.Close()

	all, err := store.ListProducts(c.Request.Context(), conn, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	published := make([]store.ProductCard, 0, len(all))
	for _, p := range all {
		if p.Published {
			published = append(published, p)
		}
	}
	// Public CORS so the feed can be consumed from anywhere.
	c.Header("Access-Control-Allow-Origin", "*")
	c.JSON(http.StatusOK, published)
}

func (h *Handler) WebListMedia(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	items, err := web.ListMedia(ctx, db.Executor(c), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []web.MediaItem{}
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) WebUploadMedia(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "se requiere un archivo 'file'"})
		return
	}
	if file.Size > maxImageBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "el archivo supera 8 MB"})
		return
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !allowedMediaExt[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "formato no permitido (usa jpg, png, webp, gif o svg)"})
		return
	}

	fname := uuid.New().String() + ext
	dest := filepath.Join(h.mediaDir(), fname)
	if err := c.SaveUploadedFile(file, dest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo guardar el archivo"})
		return
	}

	url := "/uploads/media/" + fname
	mimeType := mimeFromExt(ext)
	alt := strings.TrimSuffix(file.Filename, ext)

	item, err := web.AddMedia(ctx, db.Executor(c), tenantID, file.Filename, url, alt, mimeType, file.Size)
	if err != nil {
		os.Remove(dest)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *Handler) WebDeleteMedia(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	id := c.Param("id")

	url, err := web.DeleteMedia(ctx, db.Executor(c), tenantID, id)
	if errors.Is(err, web.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "archivo no encontrado"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if strings.HasPrefix(url, "/uploads/media/") {
		os.Remove(filepath.Join(h.mediaDir(), filepath.Base(url)))
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Archivo eliminado"})
}

const notFoundHTML = `<!doctype html><html><head><meta charset="utf-8"><title>404</title>
<style>body{font-family:system-ui;display:flex;height:100vh;align-items:center;justify-content:center;margin:0;background:#f8fafc;color:#334155}</style>
</head><body><div style="text-align:center"><h1 style="font-size:3rem;margin:0">404</h1><p>Página no encontrada</p></div></body></html>`
