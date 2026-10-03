package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/models"
	"github.com/fasterp/backend/internal/module"
)

var passwordRegex = regexp.MustCompile(`^.{8,72}$`)

// maxModuleUploadBytes caps a module package upload at 64 MB.
const maxModuleUploadBytes = 64 << 20

// internalError logs the underlying failure and returns an opaque message.
// Driver errors leak schema details (table and column names, constraint names),
// so they never reach the client.
func internalError(c *gin.Context, op string, err error) {
	log.Printf("[API] %s: %v", op, err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

type Handler struct {
	modManager       *module.ModuleManager
	jwtSecret        []byte
	jwtRefreshSecret []byte
	router           *gin.Engine
	trackedRoutes    map[string]map[string]bool
}

func NewHandler(modManager *module.ModuleManager, jwtSecret, jwtRefreshSecret string, router *gin.Engine) *Handler {
	return &Handler{
		modManager:       modManager,
		jwtSecret:        []byte(jwtSecret),
		jwtRefreshSecret: []byte(jwtRefreshSecret),
		router:           router,
		trackedRoutes:    make(map[string]map[string]bool),
	}
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	tenantID := c.GetString("tenant_id")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant identification required"})
		return
	}

	ctx := c.Request.Context()

	var user models.User
	err := db.Executor(c).QueryRowContext(ctx,
		"SELECT id, tenant_id, username, email, password_hash, is_admin, active FROM users WHERE username = $1 AND tenant_id = $2",
		req.Username, tenantID,
	).Scan(&user.ID, &user.TenantID, &user.Username, &user.Email, &user.PasswordHash, &user.IsAdmin, &user.Active)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !user.Active {
		c.JSON(http.StatusForbidden, gin.H{"error": "user is disabled"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	now := time.Now()
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       user.ID,
		"tenant_id": user.TenantID,
		"username":  user.Username,
		"is_admin":  user.IsAdmin,
		"type":      "access",
		"iat":       now.Unix(),
		"exp":       now.Add(15 * time.Minute).Unix(),
	})

	accessStr, err := accessToken.SignedString(h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	refreshToken, err := generateRefreshToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate refresh token"})
		return
	}

	tokenHash := sha256.Sum256([]byte(refreshToken))
	expiresAt := now.Add(7 * 24 * time.Hour)

	_, err = db.Executor(c).ExecContext(ctx,
		`INSERT INTO refresh_tokens (id, tenant_id, user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		uuid.New().String(), user.TenantID, user.ID,
		hex.EncodeToString(tokenHash[:]), expiresAt,
	)
	if err != nil {
		internalError(c, "store refresh token", err)
		return
	}

	user.PasswordHash = ""

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessStr,
		"refresh_token": refreshToken,
		"expires_in":    900,
		"user":          user,
	})
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func (h *Handler) RefreshToken(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	ctx := c.Request.Context()
	x := db.Executor(c)

	tokenHash := sha256.Sum256([]byte(req.RefreshToken))
	hashStr := hex.EncodeToString(tokenHash[:])

	var token models.RefreshToken
	err := x.QueryRowContext(ctx,
		`SELECT id, tenant_id, user_id, expires_at FROM refresh_tokens
		 WHERE token_hash = $1 AND expires_at > NOW()`,
		hashStr,
	).Scan(&token.ID, &token.TenantID, &token.UserID, &token.ExpiresAt)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
		return
	}

	// Rotate: the presented token is single-use. If the delete matches no row another
	// request already consumed it, so refuse rather than mint a second token from it.
	res, err := x.ExecContext(ctx, "DELETE FROM refresh_tokens WHERE id = $1", token.ID)
	if err != nil {
		internalError(c, "rotate refresh token", err)
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
		return
	}

	var user models.User
	err = x.QueryRowContext(ctx,
		"SELECT id, tenant_id, username, is_admin, active FROM users WHERE id = $1 AND active = true",
		token.UserID,
	).Scan(&user.ID, &user.TenantID, &user.Username, &user.IsAdmin, &user.Active)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found or disabled"})
		return
	}

	now := time.Now()
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       user.ID,
		"tenant_id": user.TenantID,
		"username":  user.Username,
		"is_admin":  user.IsAdmin,
		"type":      "access",
		"iat":       now.Unix(),
		"exp":       now.Add(15 * time.Minute).Unix(),
	})

	accessStr, err := accessToken.SignedString(h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	newRefresh, err := generateRefreshToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate refresh token"})
		return
	}

	newHash := sha256.Sum256([]byte(newRefresh))
	expiresAt := now.Add(7 * 24 * time.Hour)

	_, err = x.ExecContext(ctx,
		`INSERT INTO refresh_tokens (id, tenant_id, user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		uuid.New().String(), user.TenantID, user.ID,
		hex.EncodeToString(newHash[:]), expiresAt,
	)
	if err != nil {
		internalError(c, "store refresh token", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessStr,
		"refresh_token": newRefresh,
		"expires_in":    900,
	})
}

func generateRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (h *Handler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := c.GetHeader("Authorization")
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}

		if len(tokenStr) > 7 && strings.ToUpper(tokenStr[:7]) == "BEARER " {
			tokenStr = tokenStr[7:]
		} else if len(tokenStr) > 6 && strings.ToUpper(tokenStr[:6]) == "BEARER" {
			tokenStr = tokenStr[6:]
		}

		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return h.jwtSecret, nil
		})

		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token claims"})
			return
		}

		if claims["type"] != "access" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token type"})
			return
		}

		tokenTenant, _ := claims["tenant_id"].(string)
		if tokenTenant == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "tenant not found in token"})
			return
		}

		// The connection pinned by TenantMiddleware carries the tenant from the
		// request headers. Refuse to serve a token issued for a different tenant,
		// which would otherwise run under the wrong RLS context.
		if tokenTenant != c.GetString("tenant_id") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "token does not belong to this tenant"})
			return
		}

		c.Set("user_id", claims["sub"])
		c.Set("username", claims["username"])
		c.Set("is_admin", claims["is_admin"])
		c.Next()
	}
}

func (h *Handler) ListModules(c *gin.Context) {
	tenantID := c.GetString("tenant_id")

	rows, err := db.Executor(c).QueryContext(c.Request.Context(),
		`SELECT id, tenant_id, name, version, label, description, author, icon, active, installed_at, updated_at
		 FROM installed_modules WHERE tenant_id = $1 ORDER BY name`,
		tenantID,
	)
	if err != nil {
		internalError(c, "list modules", err)
		return
	}
	defer rows.Close()

	mods := make([]gin.H, 0)
	for rows.Next() {
		var m models.InstalledModule
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Name, &m.Version, &m.Label, &m.Description, &m.Author, &m.Icon, &m.Active, &m.InstalledAt, &m.UpdatedAt); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}

		// Enrich with frontend metadata from the loaded module manifest
		modData := gin.H{
			"id":           m.ID,
			"tenant_id":    m.TenantID,
			"name":         m.Name,
			"version":      m.Version,
			"label":        m.Label,
			"description":  m.Description,
			"author":       m.Author,
			"icon":         m.Icon,
			"active":       m.Active,
			"installed_at": m.InstalledAt,
			"updated_at":   m.UpdatedAt,
		}

		if inst := module.Global.Get(m.Name); inst != nil && inst.Manifest != nil && inst.Manifest.Frontend != nil {
			modData["frontend"] = inst.Manifest.Frontend
		}

		mods = append(mods, modData)
	}

	c.JSON(http.StatusOK, mods)
}

func (h *Handler) InstallModule(c *gin.Context) {
	tenantID := c.GetString("tenant_id")

	file, err := c.FormFile("module")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "module file required"})
		return
	}

	if !strings.HasSuffix(file.Filename, ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only .zip files are allowed"})
		return
	}

	if file.Size > maxModuleUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("module package exceeds %d MB", maxModuleUploadBytes/(1024*1024)),
		})
		return
	}

	uploadPath := filepath.Join(h.modManager.UploadDir, uuid.New().String()+".zip")
	if err := c.SaveUploadedFile(file, uploadPath); err != nil {
		internalError(c, "save module upload", err)
		return
	}
	// The extracted artifacts live in ModulesDir; the staging zip is not needed after this.
	defer os.Remove(uploadPath)

	pkg, err := h.modManager.ExtractModulePackage(uploadPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid module package: " + err.Error()})
		return
	}

	ctx := c.Request.Context()

	var existingID string
	err = db.Executor(c).QueryRowContext(ctx,
		"SELECT id FROM installed_modules WHERE name = $1 AND tenant_id = $2",
		pkg.Manifest.Name, tenantID,
	).Scan(&existingID)
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "module already installed for this tenant"})
		return
	}

	_, err = db.Executor(c).ExecContext(ctx,
		`INSERT INTO installed_modules (id, tenant_id, name, version, label, description, author, icon, active, checksum)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.New().String(), tenantID,
		pkg.Manifest.Name, pkg.Manifest.Version, pkg.Manifest.Label,
		pkg.Manifest.Description, pkg.Manifest.Author, pkg.Manifest.Icon,
		false, pkg.Checksum,
	)
	if err != nil {
		internalError(c, "install module", err)
		return
	}

	log.Printf("[API] Module installed: %s v%s for tenant %s", pkg.Manifest.Name, pkg.Manifest.Version, tenantID)
	c.JSON(http.StatusOK, gin.H{"message": "module installed", "module": pkg.Manifest})
}

func (h *Handler) UninstallModule(c *gin.Context) {
	name := c.Param("name")
	tenantID := c.GetString("tenant_id")

	// The plugin stays loaded in the process-wide registry; other tenants may still
	// have it installed. Removing the row is what revokes this tenant's access.
	_, err := db.Executor(c).ExecContext(c.Request.Context(),
		"DELETE FROM installed_modules WHERE name = $1 AND tenant_id = $2",
		name, tenantID,
	)
	if err != nil {
		internalError(c, "uninstall module", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "module uninstalled"})
}

func (h *Handler) ToggleModule(c *gin.Context) {
	name := c.Param("name")
	tenantID := c.GetString("tenant_id")

	ctx := c.Request.Context()
	x := db.Executor(c)

	var active bool
	err := x.QueryRowContext(ctx,
		"SELECT active FROM installed_modules WHERE name = $1 AND tenant_id = $2",
		name, tenantID,
	).Scan(&active)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "module not found for this tenant"})
		return
	}

	// Activar carga el módulo en el registro compartido si no estaba. Desactivar
	// NO lo descarga: el registro es del proceso, y descargarlo rompería a
	// cualquier otro tenant que lo tenga activo. El acceso se filtra por tenant
	// en cada petición, con el active de abajo.
	if !active && module.Global.Get(name) == nil {
		if _, err := h.modManager.LoadModule(name); err != nil {
			log.Printf("[API] no se pudo cargar el módulo %s: %v", name, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load module"})
			return
		}
	}

	_, err = x.ExecContext(ctx,
		"UPDATE installed_modules SET active = $1, updated_at = NOW() WHERE name = $2 AND tenant_id = $3",
		!active, name, tenantID,
	)
	if err != nil {
		internalError(c, "toggle module", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "module toggled", "active": !active})
}

func (h *Handler) ModuleRoutes(c *gin.Context) {
	name := c.Param("name")
	inst := module.Global.Get(name)
	if inst == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "module not loaded"})
		return
	}

	type routeSummary struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	routes := make([]routeSummary, len(inst.Routes))
	for i, r := range inst.Routes {
		routes[i] = routeSummary{Method: r.Method, Path: r.Path}
	}

	c.JSON(http.StatusOK, gin.H{
		"routes":   routes,
		"models":   inst.Models,
		"manifest": inst.Manifest,
	})
}

func (h *Handler) GetMenus(c *gin.Context) {
	c.JSON(http.StatusOK, module.Global.Menus())
}

func (h *Handler) GetCurrentUser(c *gin.Context) {
	userID := c.GetString("user_id")
	tenantID := c.GetString("tenant_id")

	var user models.User
	err := db.Executor(c).QueryRowContext(c.Request.Context(),
		"SELECT id, tenant_id, username, email, is_admin, active, created_at FROM users WHERE id = $1 AND tenant_id = $2",
		userID, tenantID,
	).Scan(&user.ID, &user.TenantID, &user.Username, &user.Email, &user.IsAdmin, &user.Active, &user.CreatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *Handler) ChangePassword(c *gin.Context) {
	userID := c.GetString("user_id")
	tenantID := c.GetString("tenant_id")

	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if !passwordRegex.MatchString(req.NewPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be between 8 and 72 characters"})
		return
	}

	ctx := c.Request.Context()
	x := db.Executor(c)

	var hash string
	err := x.QueryRowContext(ctx,
		"SELECT password_hash FROM users WHERE id = $1 AND tenant_id = $2",
		userID, tenantID,
	).Scan(&hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	_, err = x.ExecContext(ctx,
		"UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3",
		string(newHash), userID, tenantID,
	)
	if err != nil {
		internalError(c, "change password", err)
		return
	}

	// Invalidate all refresh tokens on password change
	if _, err := x.ExecContext(ctx, "DELETE FROM refresh_tokens WHERE user_id = $1", userID); err != nil {
		log.Printf("[Auth] Failed to revoke refresh tokens for user %s: %v", userID, err)
	}

	c.JSON(http.StatusOK, gin.H{"message": "password changed successfully"})
}

func (h *Handler) Logout(c *gin.Context) {
	userID := c.GetString("user_id")
	// Invalidate all refresh tokens for this user
	_, err := db.Executor(c).ExecContext(c.Request.Context(),
		"DELETE FROM refresh_tokens WHERE user_id = $1", userID)
	if err != nil {
		internalError(c, "logout", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}

// TenantMiddleware resolves the tenant from the request, verifies it is real and
// active, and pins a database connection carrying the RLS tenant context for the
// rest of the request. Handlers must reach that connection via db.Executor(c).
//
// The tenant resolved here comes from client-controlled input; AuthMiddleware
// additionally requires it to match the tenant baked into the access token.
func (h *Handler) TenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetHeader("X-Tenant-ID")
		if tenantID == "" {
			tenantID = c.Query("tenant_id")
		}
		if tenantID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "X-Tenant-ID header is required"})
			return
		}

		// Resolve by slug or UUID; tenants itself is not RLS-protected, since it is
		// the registry consulted before any tenant context exists.
		var active bool
		var err error
		if _, uuidErr := uuid.Parse(tenantID); uuidErr == nil {
			err = db.DB.QueryRow(
				"SELECT id, active FROM tenants WHERE id = $1", tenantID,
			).Scan(&tenantID, &active)
		} else {
			err = db.DB.QueryRow(
				"SELECT id, active FROM tenants WHERE slug = $1", tenantID,
			).Scan(&tenantID, &active)
		}
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid tenant"})
			return
		}
		if !active {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant is disabled"})
			return
		}

		conn, err := db.AcquireConn(c.Request.Context(), tenantID)
		if err != nil {
			log.Printf("[Tenant] Failed to acquire connection for %s: %v", tenantID, err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "database connection error"})
			return
		}
		defer conn.Close()

		c.Set("tenant_id", tenantID)
		c.Set("db_conn", conn)
		c.Next()
	}
}

func (h *Handler) AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, exists := c.Get("is_admin")
		if !exists || isAdmin != true {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}
		c.Next()
	}
}

// ListTenants returns the caller's own tenant only. "Admin" here means admin of one
// tenant, not of the deployment, so enumerating every tenant on the instance would
// hand any tenant's admin the full customer list.
func (h *Handler) ListTenants(c *gin.Context) {
	var t models.Tenant
	err := db.DB.QueryRow(
		"SELECT id, name, slug, active, created_at FROM tenants WHERE id = $1",
		c.GetString("tenant_id"),
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Active, &t.CreatedAt)
	if err != nil {
		internalError(c, "list tenants", err)
		return
	}
	c.JSON(http.StatusOK, []models.Tenant{t})
}

func (h *Handler) GetTenant(c *gin.Context) {
	if c.Param("id") != c.GetString("tenant_id") {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
		return
	}

	var t models.Tenant
	err := db.DB.QueryRow(
		"SELECT id, name, slug, active, created_at FROM tenants WHERE id = $1",
		c.GetString("tenant_id"),
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Active, &t.CreatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}
