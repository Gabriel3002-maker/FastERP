package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/fasterp/backend/internal/db"
)

// Formato de un slug de tenant.
//
// TenantMiddleware resuelve el header X-Tenant-ID primero como UUID y después
// como slug, así que un slug que parezca un UUID acabaría mirando por id y
// fallando de forma confusa. La primera letra es siempre una para que ningún
// slug empiece por dígito y se parezca a un id por accidente.
var tenantSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,49}$`)

// Formato de nombre de usuario y email, compartido por el alta inicial y por el
// CRUD de usuarios: si solo uno validara, un usuario válido en /setup sería
// inválido al editarlo después.
var (
	usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,100}$`)
	emailPattern    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// validPassword aplica el mismo criterio que ChangePassword (8-72 caracteres) y
// añade el tope de 72 *bytes*.
//
// El regex se cuenta en runes y bcrypt se come bytes: "ññññ" son 4 runes y 8
// bytes, y 40 emojis de 4 bytes pasan el regex y luego hacen que
// GenerateFromPassword devuelva ErrPasswordTooLong. Aquí eso es un 400; sin la
// comprobación sería un 500 con traza en el log.
func validPassword(pw string) bool {
	return passwordRegex.MatchString(pw) && len(pw) <= 72
}

// setupMu serializa el alta inicial.
//
// El chequeo "¿no existe ningún usuario?" y la escritura que viene después son
// dos operaciones: en una base recién montada, dos peticiones simultáneas pasan
// ambas la lectura y crean dos tenants. El proceso es único, así que un mutex en
// memoria cierra la carrera; con más de una instancia haría falta un advisory
// lock de Postgres, que es un problema que aún no existe.
var setupMu sync.Mutex

// SetupInitial crea el primer tenant y el primer administrador de la instancia.
//
// Es el endpoint que el asistente de /setup llama con POST /api/setup/initial.
// El router no lo monta dentro de /api/auth porque ese grupo pasa por
// TenantMiddleware, y quien llega aquí justamente todavía no tiene tenant: se
// resuelve el propio.
//
// Tres reglas, en este orden:
//
//  1. Solo funciona si la instancia entera no tiene ni un usuario. En cuanto
//     alguien existe, responde 409 y la única vía de entrada es el login — no
//     hay una puerta trasera que volver a abrir con la misma llamada.
//  2. El tenant se toma del slug que escribe el asistente. Si ya existe se
//     reutiliza; si no, se crea. Nada de "siempre el default".
//  3. El usuario creado es siempre administrador y siempre activo: es la
//     cuenta con la que se va a montar todo lo demás, y no puede salir
//     desactivada ni sin privilegios desde la propia llamada.
func (h *Handler) SetupInitial(c *gin.Context) {
	setupMu.Lock()
	defer setupMu.Unlock()

	var req struct {
		TenantName string `json:"tenant_name"`
		TenantSlug string `json:"tenant_slug"`
		AdminUser  string `json:"admin_user" binding:"required"`
		AdminEmail string `json:"admin_email" binding:"required"`
		Password   string `json:"password" binding:"required"`
		Confirm    string `json:"confirm" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}

	req.AdminUser = strings.TrimSpace(req.AdminUser)
	req.AdminEmail = strings.TrimSpace(req.AdminEmail)
	slug := strings.ToLower(strings.TrimSpace(req.TenantSlug))
	if slug == "" {
		slug = "default"
	}

	switch {
	case req.Password != req.Confirm:
		c.JSON(http.StatusBadRequest, gin.H{"error": "las contraseñas no coinciden"})
		return
	case !validPassword(req.Password):
		c.JSON(http.StatusBadRequest, gin.H{"error": "la contraseña debe tener entre 8 y 72 bytes"})
		return
	case !usernamePattern.MatchString(req.AdminUser):
		c.JSON(http.StatusBadRequest, gin.H{"error": "el usuario debe tener entre 3 y 100 caracteres (letras, números, punto, guion o guion bajo)"})
		return
	case !emailPattern.MatchString(req.AdminEmail) || len(req.AdminEmail) > 255:
		c.JSON(http.StatusBadRequest, gin.H{"error": "el email no es válido"})
		return
	case !tenantSlugPattern.MatchString(slug):
		c.JSON(http.StatusBadRequest, gin.H{"error": "el identificador de empresa debe tener entre 2 y 50 caracteres en minúsculas (letras, números, punto, guion o guion bajo) y empezar por una letra"})
		return
	}

	// Un slug con forma de UUID chocaría con la resolución por id de
	// TenantMiddleware: se rechaza en la entrada en vez de dejar que alguien
	// descubra el problema al no poder loguearse.
	if _, err := uuid.Parse(slug); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el identificador de empresa no puede tener forma de UUID"})
		return
	}

	ctx := c.Request.Context()

	empty, err := db.InstanceHasUsers(ctx)
	if err != nil {
		internalError(c, "setup: comprobar usuarios existentes", err)
		return
	}
	if !empty {
		c.JSON(http.StatusConflict, gin.H{"error": "la instalación ya tiene usuarios; entra con tus credenciales"})
		return
	}

	tenantID, err := resolveOrCreateTenant(ctx, slug, strings.TrimSpace(req.TenantName))
	if err != nil {
		internalError(c, "setup: crear tenant", err)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "setup: hashear contraseña", err)
		return
	}

	var user struct {
		ID   string
		Name string
	}
	if err := db.WithTenant(ctx, tenantID, func(x db.QueryExecutor) error {
		return x.QueryRowContext(ctx,
			`INSERT INTO users (id, tenant_id, username, email, password_hash, is_admin, active)
			 VALUES ($1, $2, $3, $4, $5, true, true)
			 RETURNING id, username`,
			uuid.New().String(), tenantID, req.AdminUser, req.AdminEmail, string(hash),
		).Scan(&user.ID, &user.Name)
	}); err != nil {
		if db.IsUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "ya existe un usuario con ese nombre o email"})
			return
		}
		internalError(c, "setup: crear administrador", err)
		return
	}

	// El log no lleva la contraseña. Lleva el usuario y el tenant, que son lo
	// que hace falta para saber qué se montó y con qué entrada.
	log.Printf("[Setup] primer administrador creado: %s (%s) en el tenant %s (%s)",
		user.Name, user.ID, slug, tenantID)

	c.JSON(http.StatusCreated, gin.H{
		"message":     "Instalación completada. Entra con el usuario " + user.Name,
		"tenant_id":   tenantID,
		"tenant_slug": slug,
	})
}

// resolveOrCreateTenant devuelve el id del tenant con ese slug, creándolo si no
// existe todavía.
func resolveOrCreateTenant(ctx context.Context, slug, name string) (string, error) {
	var id string
	err := db.DB.QueryRowContext(ctx, "SELECT id FROM tenants WHERE slug = $1", slug).Scan(&id)
	switch {
	case err == nil:
		return id, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}

	if name == "" {
		name = slug
	}
	id = uuid.New().String()
	if _, err := db.DB.ExecContext(ctx,
		`INSERT INTO tenants (id, name, slug, active) VALUES ($1, $2, $3, true)`,
		id, name, slug,
	); err != nil {
		return "", err
	}
	log.Printf("[Setup] tenant creado: %s (%s)", slug, id)
	return id, nil
}
