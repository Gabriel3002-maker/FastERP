package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/store"
)

const maxImageBytes = 8 << 20 // 8 MB

var allowedImageExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
}

// productsImageDir is the on-disk folder that backs the public /uploads/products path.
func (h *Handler) productsImageDir() string {
	return filepath.Join(h.modManager.UploadDir, "products")
}

// RegisterStoreRoutes wires the mini-store (tienda_web) endpoints and serves
// product images statically from /uploads/products.
func (h *Handler) RegisterStoreRoutes() {
	os.MkdirAll(h.productsImageDir(), 0755)

	// Public static serving of product images (so <img src> works without a token).
	h.router.Static("/uploads/products", h.productsImageDir())

	g := h.router.Group("/api/store", h.TenantMiddleware(), h.AuthMiddleware())
	{
		g.GET("/products", h.StoreListProducts)
		g.GET("/products/:id", h.StoreGetProduct)
		g.PUT("/products/:id", h.StoreUpsertProduct)
		g.POST("/products/:id/images", h.StoreUploadImage)
		g.DELETE("/images/:id", h.StoreDeleteImage)
	}
}

// storeProductID extracts and validates the product UUID from the route. The id
// goes straight into a SQL parameter, but rejecting malformed UUIDs here keeps
// a bad id from becoming a database error in the caller's face.
func storeProductID(c *gin.Context) (string, bool) {
	raw := c.Param("id")
	if _, err := uuid.Parse(raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid product id"})
		return "", false
	}
	return raw, true
}

func (h *Handler) StoreListProducts(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	items, err := store.ListProducts(ctx, db.Executor(c), tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) StoreGetProduct(c *gin.Context) {
	productID, ok := storeProductID(c)
	if !ok {
		return
	}
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	d, err := store.GetProduct(ctx, db.Executor(c), tenantID, productID)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "producto no encontrado"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *Handler) StoreUpsertProduct(c *gin.Context) {
	productID, ok := storeProductID(c)
	if !ok {
		return
	}
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	var in store.UpsertInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := store.UpsertProduct(ctx, db.Executor(c), tenantID, productID, in); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "producto no encontrado"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Producto actualizado"})
}

func (h *Handler) StoreUploadImage(c *gin.Context) {
	productID, ok := storeProductID(c)
	if !ok {
		return
	}
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "se requiere un archivo 'image'"})
		return
	}
	if file.Size > maxImageBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "la imagen supera 8 MB"})
		return
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !allowedImageExt[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "formato no permitido (usa jpg, png, webp o gif)"})
		return
	}

	fname := uuid.New().String() + ext
	dest := filepath.Join(h.productsImageDir(), fname)
	if err := c.SaveUploadedFile(file, dest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo guardar la imagen"})
		return
	}

	url := "/uploads/products/" + fname
	img, err := store.AddImage(ctx, db.Executor(c), tenantID, productID, url, file.Filename)
	if err != nil {
		os.Remove(dest) // roll back the file if the DB insert failed
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "producto no encontrado"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, img)
}

func (h *Handler) StoreDeleteImage(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	id := c.Param("id")

	url, err := store.DeleteImage(ctx, db.Executor(c), tenantID, id)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "imagen no encontrada"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Best-effort file cleanup for locally stored images.
	if strings.HasPrefix(url, "/uploads/products/") {
		os.Remove(filepath.Join(h.productsImageDir(), filepath.Base(url)))
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Imagen eliminada"})
}
