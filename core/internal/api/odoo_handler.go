package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/odoo"
)

// RegisterOdooRoutes wires the native Odoo integration endpoints. These live in
// the backend (not the WASM module) because the WASI sandbox has no network
// access. All routes are tenant-scoped via AuthMiddleware.
func (h *Handler) RegisterOdooRoutes() {
	g := h.router.Group("/api/odoo", h.TenantMiddleware(), h.AuthMiddleware())
	{
		g.POST("/test-connection", h.OdooTestConnection)
		g.POST("/sync/products", h.OdooSyncProducts)
		g.POST("/push/order", h.OdooPushOrder)
		g.POST("/push/lead", h.OdooPushLead)
		g.POST("/push/product", h.OdooPushProduct)
		g.POST("/push/stock", h.OdooPushStock)
	}
}

func (h *Handler) OdooTestConnection(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	uid, err := odoo.TestConnection(ctx, db.Executor(c), tenantID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Conexión exitosa con Odoo", "uid": uid})
}

func (h *Handler) OdooSyncProducts(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	var body struct {
		Limit int `json:"limit"`
	}
	_ = c.ShouldBindJSON(&body)

	res, err := odoo.SyncProducts(ctx, db.Executor(c), tenantID, body.Limit)
	if err != nil {
		c.JSON(http.StatusBadGateway, res)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) OdooPushOrder(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	var p odoo.OrderPush
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
		return
	}
	res, err := odoo.PushOrder(ctx, db.Executor(c), tenantID, p)
	if err != nil {
		c.JSON(http.StatusBadGateway, res)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) OdooPushLead(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	var p odoo.LeadPush
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
		return
	}
	res, err := odoo.PushLead(ctx, db.Executor(c), tenantID, p)
	if err != nil {
		c.JSON(http.StatusBadGateway, res)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) OdooPushProduct(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	var p odoo.ProductPush
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
		return
	}
	res, err := odoo.PushProduct(ctx, db.Executor(c), tenantID, p)
	if err != nil {
		c.JSON(http.StatusBadGateway, res)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) OdooPushStock(c *gin.Context) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	var p odoo.StockPush
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
		return
	}
	res, err := odoo.PushStock(ctx, db.Executor(c), tenantID, p)
	if err != nil {
		c.JSON(http.StatusBadGateway, res)
		return
	}
	c.JSON(http.StatusOK, res)
}
