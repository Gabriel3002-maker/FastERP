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
	"strconv"
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

		// El token vale 15 minutos, pero un usuario desactivado o degradado no
		// tiene por qué poder usarlos: se confirma contra la base en cada
		// petición. Es un lookup por PK, barato, y cierra la ventana de revocación.
		userID, _ := claims["sub"].(string)
		var active, isAdmin bool
		// En producción TenantMiddleware siempre pinea db_conn; si falta (un
		// test, una ruta mal montada) se omite el re-chequeo en vez de romper.
		if conn, ok := c.Get("db_conn"); ok && conn != nil {
			err = db.Executor(c).QueryRowContext(c.Request.Context(),
				"SELECT active, is_admin FROM users WHERE id = $1 AND tenant_id = $2",
				userID, c.GetString("tenant_id"),
			).Scan(&active, &isAdmin)
			if err != nil || !active {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user not found or disabled"})
				return
			}
			c.Set("is_admin", isAdmin)
		} else {
			c.Set("is_admin", claims["is_admin"])
		}
		c.Next()
	}
}

// permissionVerdict reúne en una fila los cinco hechos que deciden un permiso.
//
// Se pide todo en la misma consulta y con EXISTS: no hay tablas que unir ni
// resultados que juntar en Go, y la precedencia la aplica allow, no la forma
// del SQL.
type permissionVerdict struct {
	// managed es true en cuanto el tenant tiene al menos un rol asignado. Es la
	// frontera entre el modo compatible y el default-deny: no depende del
	// usuario que pregunta, sino del tenant entero.
	managed bool
	// directGrant: el usuario tiene el permiso otorgado explícitamente.
	directGrant bool
	// directDeny: el usuario tiene el permiso revocado explícitamente. Gana
	// sobre cualquier rol: una revocación es una instrucción personal.
	directDeny bool
	// roleGranted: algún rol del usuario concede el permiso.
	roleGranted bool
	// resourceTracked: existe alguna fila directa para el recurso. En modo
	// compatible convierte el "todo permitido" en "solo lo declarado".
	resourceTracked bool
}

// allow resuelve el veredicto con la precedencia entera:
//
//  1. lo directo gana al rol: un allow directo abre, un deny directo cierra;
//  2. el rol concede;
//  3. si el tenant tiene roles asignados, lo que no está concedido está
//     prohibido (default-deny);
//  4. sin roles, el recurso con filas directas solo deja pasar lo declarado, y
//     el recurso sin filas deja pasar todo: el comportamiento histórico.
func (v permissionVerdict) allow() bool {
	if v.directGrant {
		return true
	}
	if v.directDeny {
		return false
	}
	if v.roleGranted {
		return true
	}
	if v.managed {
		return false
	}
	if v.resourceTracked {
		return false
	}
	return true
}

// requirePermission comprueba una acción sobre el CRUD de módulos. is_admin
// pasa siempre.
//
// reg decide el nivel de la consulta: con un modelo se pregunta por el permiso
// exacto (modelo + acción); con reg nil se pregunta a nivel de módulo, "¿tiene
// el usuario algún permiso en este módulo?", que es lo que protege la lista de
// modelos del _meta.
//
// Los rechazos no son errores: 403. Solo el fallo de base de datos es 500.
func (h *Handler) requirePermission(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration, action string) bool {
	if a, _ := c.Get("is_admin"); a == true {
		return true
	}

	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(string)
	if userID == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return false
	}

	v, err := h.permissionVerdict(c, userID, inst.Manifest.Name, reg, action)
	if err != nil {
		log.Printf("[RBAC] permission check failed: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "permission check failed"})
		return false
	}
	if v.allow() {
		return true
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permission denied"})
	return false
}

// permissionVerdict executa la consulta que rellena el veredicto.
//
// La consulta filtra por modelo y acción solo cuando reg no es nil; a nivel de
// módulo reduce las condiciones a "algún permiso directo" y "algún permiso por
// rol", y se montan los placeholders $-1 una sola vez para los dos niveles.
func (h *Handler) permissionVerdict(c *gin.Context, userID, moduleName string, reg *module.ModelRegistration, action string) (permissionVerdict, error) {
	ctx := c.Request.Context()

	modelWhere, actionWhere, roleWhere := "", "", ""
	args := []any{userID, moduleName}
	if reg != nil {
		next := len(args) + 1
		modelWhere = " AND model = $" + strconv.Itoa(next)
		actionWhere = " AND action = $" + strconv.Itoa(next+1)
		roleWhere = modelWhere + actionWhere
		args = append(args, reg.Manifest.Name, action)
	}
	args = append(args, c.GetString("tenant_id"))
	managedParam := "$" + strconv.Itoa(len(args))

	query := `SELECT
		(SELECT EXISTS(SELECT 1 FROM user_roles WHERE tenant_id = ` + managedParam + `)) AS managed,
		(SELECT EXISTS(SELECT 1 FROM user_permissions WHERE user_id = $1 AND module = $2` + actionWhere + ` AND allow)) AS direct_grant,
		(SELECT EXISTS(SELECT 1 FROM user_permissions WHERE user_id = $1 AND module = $2` + actionWhere + ` AND NOT allow)) AS direct_deny,
		(SELECT EXISTS(
			SELECT 1 FROM user_roles ur
			JOIN role_permissions rp ON rp.tenant_id = ur.tenant_id AND rp.role_id = ur.role_id
			WHERE ur.user_id = $1 AND rp.module = $2` + roleWhere + `)) AS role_granted,
		(SELECT EXISTS(SELECT 1 FROM user_permissions WHERE user_id = $1 AND module = $2` + modelWhere + `)) AS resource_tracked`

	var v permissionVerdict
	err := db.Executor(c).QueryRowContext(ctx, query, args...).Scan(
		&v.managed, &v.directGrant, &v.directDeny, &v.roleGranted, &v.resourceTracked,
	)
	return v, err
}

// moduleReadable es la pregunta "¿puede este usuario ver este módulo?" sin
// escribir la respuesta: renuncia a tinta, solo devuelve un booleano. Lo usa el
// filtro del menú lateral, donde un "no" no es un fallo de la petición, solo
// una entrada que se omite.
//
// Con modelName filtra por el modelo de la entrada (un rizo del menú que apunta
// al CRUD de un modelo); sin él, por el módulo entero: "algún permiso en este
// módulo", que es la misma frontera que protege el _meta.
func (h *Handler) moduleReadable(c *gin.Context, inst *module.ModuleInstance, modelName string) bool {
	if a, _ := c.Get("is_admin"); a == true {
		return true
	}
	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(string)
	if userID == "" {
		return false
	}

	var reg *module.ModelRegistration
	if modelName != "" {
		reg = findModelReg(inst, modelName)
	}
	v, err := h.permissionVerdict(c, userID, inst.Manifest.Name, reg, "read")
	if err != nil {
		log.Printf("[RBAC] no se pudo filtrar el menú para %s: %v", inst.Manifest.Name, err)
		// Ante la duda se muestra: esconder una entrada por un fallo de lectura
		// ocultaría módulos sin que nadie lo pida.
		return true
	}
	return v.allow()
}

// findModelReg localiza el registro de modelo por su nombre de manifest. Los
// nombres de permiso son los del manifest, sin prefijo de módulo.
func findModelReg(inst *module.ModuleInstance, modelName string) *module.ModelRegistration {
	for i := range inst.Models {
		if inst.Models[i].Manifest != nil && inst.Models[i].Manifest.Name == modelName {
			return &inst.Models[i]
		}
	}
	return nil
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
	installedMap := make(map[string]bool)

	for rows.Next() {
		var m models.InstalledModule
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Name, &m.Version, &m.Label, &m.Description, &m.Author, &m.Icon, &m.Active, &m.InstalledAt, &m.UpdatedAt); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}

		installedMap[m.Name] = true

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
			"installed":    true,
			"installed_at": m.InstalledAt,
			"updated_at":   m.UpdatedAt,
		}

		if inst := module.Global.Get(m.Name); inst != nil && inst.Manifest != nil && inst.Manifest.Frontend != nil {
			modData["frontend"] = inst.Manifest.Frontend
		}

		mods = append(mods, modData)
	}

	// Append disk modules that are available in module.Global but not yet installed in DB
	for _, inst := range module.Global.All() {
		if inst == nil || inst.Manifest == nil {
			continue
		}
		name := inst.Manifest.Name
		if installedMap[name] {
			continue
		}

		modData := gin.H{
			"id":          "",
			"tenant_id":   tenantID,
			"name":        name,
			"version":     inst.Manifest.Version,
			"label":       inst.Manifest.Label,
			"description": inst.Manifest.Description,
			"author":      inst.Manifest.Author,
			"icon":        inst.Manifest.Icon,
			"active":      false,
			"installed":   false,
		}
		if inst.Manifest.Frontend != nil {
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
		// Module not yet in installed_modules DB table: check if it exists on disk/registry
		inst := module.Global.Get(name)
		if inst == nil {
			var loadErr error
			inst, loadErr = h.modManager.LoadModule(name)
			if loadErr != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "module not found for this tenant"})
				return
			}
		}

		if err := h.modManager.Apply(ctx, inst); err != nil {
			log.Printf("[API] no se pudo aplicar el esquema del módulo %s: %v", name, err)
		}

		m := inst.Manifest
		modID := uuid.New().String()
		_, err = x.ExecContext(ctx,
			`INSERT INTO installed_modules (id, tenant_id, name, version, label, description, author, icon, active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true)`,
			modID, tenantID, m.Name, m.Version, m.Label, m.Description, m.Author, m.Icon,
		)
		if err != nil {
			internalError(c, "install and toggle module", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "module installed and activated", "active": true})
		return
	}

	// Activar carga el módulo en el registro compartido si no estaba. Desactivar
	// NO lo descarga: el registro es del proceso, y descargarlo rompería a
	// cualquier otro tenant que lo tenga activo. El acceso se filtra por tenant
	// en cada petición, con el active de abajo.
	if !active && module.Global.Get(name) == nil {
		if inst, err := h.modManager.LoadModule(name); err != nil {
			log.Printf("[API] no se pudo cargar el módulo %s: %v", name, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load module"})
			return
		} else {
			h.modManager.Apply(ctx, inst)
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

// GetMenus devuelve el menú lateral del tenant filtrado por lo que el usuario
// puede leer.
//
// La barra lateral se renderiza en el servidor sin sesión (las páginas /admin
// son carcasas), así que este endpoint es quien le dice al cliente qué entradas
// ocultar: el cliente no repinta el árbol, solo tacha lo que aquí no viene.
func (h *Handler) GetMenus(c *gin.Context) {
	items := module.Global.MenusForTenant(c.GetString("tenant_id"))
	out := make([]module.MenuItem, 0, len(items))
	for _, it := range items {
		inst := module.Global.Get(it.Module)
		if inst == nil || h.moduleReadable(c, inst, menuModelName(it.Model)) {
			out = append(out, it)
		}
	}
	c.JSON(http.StatusOK, out)
}

// menuModelName quita el prefijo "módulo/" del modelo de una entrada de menú.
// Los permisos guardan el nombre del manifest solo; el menú lo lleva completo.
func menuModelName(model string) string {
	_, name, found := strings.Cut(model, "/")
	if !found {
		return model
	}
	return name
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
