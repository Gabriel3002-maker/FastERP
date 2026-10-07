package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/models"
)

// userColumns es la proyección única que devuelven todos los handlers de
// usuarios. password_hash no aparece, y no va a aparecer: el hash solo se lee
// para verificar (ChangePassword) y para reemplazarse (SetUserPassword), nunca
// para mandarlo fuera.
const userColumns = "id, tenant_id, username, email, is_admin, active, created_at, updated_at"

// scanUser recoge una fila de users en el orden de userColumns.
//
// Toda fila que sale de esta tabla pasa por aquí, así que si un día se añade
// columna al SELECT se cambia en un solo sitio.
func scanUser(row interface{ Scan(...interface{}) error }) (models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.Email, &u.IsAdmin, &u.Active, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func userFromRow(c *gin.Context, q db.QueryExecutor, query string, args ...interface{}) (models.User, bool) {
	u, err := scanUser(q.QueryRowContext(c.Request.Context(), query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return models.User{}, false
	}
	if err != nil {
		internalError(c, "leer usuario", err)
		return models.User{}, false
	}
	return u, true
}

// parseUserID valida el path param antes de que llegue a Postgres. Un id que no
// es UUID produciría un error de tipo de dato con traza, que es un 500 para lo
// que en realidad es una petición mal formada.
func parseUserID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id de usuario no válido"})
		return "", false
	}
	return id, true
}

// countOtherActiveAdmins cuenta los administradores vivos distintos de exclude.
//
// Es la comprobación detrás de las dos prohibiciones que no admiten excepción:
// no se puede dejar un tenant sin administrador activo, ni por desactivación ni
// por quitar el flag. Se mira el par (is_admin, active) porque un admin
// desactivado no sirve de nada y no cuenta.
func countOtherActiveAdmins(ctx context.Context, x db.QueryExecutor, exclude string) (int, error) {
	var n int
	err := x.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE is_admin AND active AND id <> $1`, exclude,
	).Scan(&n)
	return n, err
}

// ListUsers devuelve todas las cuentas del tenant, ordenadas por nombre.
func (h *Handler) ListUsers(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := db.Executor(c).QueryContext(ctx,
		`SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		internalError(c, "listar usuarios", err)
		return
	}
	defer rows.Close()

	out := make([]models.User, 0, 16)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			internalError(c, "listar usuarios", err)
			return
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		internalError(c, "listar usuarios", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// CreateUser da de alta una cuenta en el tenant.
//
// El rol de administrador lo elige quien llama, y eso es correcto aquí: solo
// llega quien ya es admin (AdminMiddleware), y restringirlo a "solo is_admin"
// convertiría la creación de administradores en un círculo imposible de abrir.
func (h *Handler) CreateUser(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)

	if msg := invalidCredentials(req.Username, req.Email, req.Password); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "crear usuario: hashear contraseña", err)
		return
	}

	ctx := c.Request.Context()
	var u models.User
	err = scanUserError(db.Executor(c).QueryRowContext(ctx,
		`INSERT INTO users (id, tenant_id, username, email, password_hash, is_admin, active)
		 VALUES ($1, $2, $3, $4, $5, $6, true)
		 RETURNING `+userColumns,
		uuid.New().String(), c.GetString("tenant_id"), req.Username, req.Email, string(hash), req.IsAdmin))
	switch {
	case err == nil:
	case db.IsUniqueViolation(err):
		// UNIQUE(tenant_id, username) y UNIQUE(tenant_id, email): el mismo aviso
		// cubre las dos sin decir cuál, que tampoco es información que haya que
		// filtrar a quien ya está dentro del tenant.
		c.JSON(http.StatusConflict, gin.H{"error": "ya existe un usuario con ese nombre o email en este tenant"})
		return
	default:
		internalError(c, "crear usuario", err)
		return
	}

	log.Printf("[Users] usuario creado: %s (%s)", u.Username, u.ID)
	c.JSON(http.StatusCreated, u)
}

// scanUserError recoge una fila con scanUser sin escribir respuesta: quien
// decide cómo traducir cada error es quien llama.
func scanUserError(row interface{ Scan(...interface{}) error }) error {
	_, err := scanUser(row)
	return err
}

// invalidCredentials valida nombre, email y contraseña a la vez y devuelve el
// mensaje de error vacío si todo está bien. Los tres se compran juntos porque
// los tres viven en el mismo formulario: un único mensaje evita tres idas y
// vueltas con el cliente.
func invalidCredentials(username, email, password string) string {
	if !usernamePattern.MatchString(username) {
		return "el usuario debe tener entre 3 y 100 caracteres (letras, números, punto, guion o guion bajo)"
	}
	if !emailPattern.MatchString(email) || len(email) > 255 {
		return "el email no es válido"
	}
	if !validPassword(password) {
		return "la contraseña debe tener entre 8 y 72 bytes"
	}
	return ""
}

// GetUser devuelve una cuenta por id.
func (h *Handler) GetUser(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}
	u, _ := userFromRow(c, db.Executor(c),
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND tenant_id = $2`,
		id, c.GetString("tenant_id"))
	if u.ID != "" {
		c.JSON(http.StatusOK, u)
	}
}

// UpdateUser modifica los campos que el cliente manda y ninguno más.
//
// Los puntos son opcionales a propósito: un PUT que rellenara con cero lo que
// no venía desactivaría cuentas por accidente. Solo lo que llega se escribe.
//
// Dos prohibiciones, y ninguna es una preferencia de UI:
//
//   - no puedes desactivarte a ti mismo: dejarías la sesión abierta pero la
//     cuenta muerta, y el siguiente refresco te expulsaría sin aviso;
//   - no puedes quitarte is_admin, ni quitárselo al último admin vivo del
//     tenant. Sin eso, un admin curioso se bloquea a sí mismo y queda la cuenta
//     sin nadie que pueda rescatarla desde dentro.
func (h *Handler) UpdateUser(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}

	var req struct {
		Username *string `json:"username"`
		Email    *string `json:"email"`
		IsAdmin  *bool   `json:"is_admin"`
		Active   *bool   `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}
	if req.Username == nil && req.Email == nil && req.IsAdmin == nil && req.Active == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no hay nada que actualizar"})
		return
	}

	// Solo se validan los campos que vienen: los que no viajan se leen de la
	// fila actual y ya pasaron esta misma comprobación al crearse.
	if req.Username != nil {
		*req.Username = strings.TrimSpace(*req.Username)
		if !usernamePattern.MatchString(*req.Username) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "el usuario debe tener entre 3 y 100 caracteres (letras, números, punto, guion o guion bajo)"})
			return
		}
	}
	if req.Email != nil {
		*req.Email = strings.TrimSpace(*req.Email)
		if !emailPattern.MatchString(*req.Email) || len(*req.Email) > 255 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "el email no es válido"})
			return
		}
	}

	ctx := c.Request.Context()
	x := db.Executor(c)

	cur, ok := userFromRow(c, x,
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND tenant_id = $2`,
		id, c.GetString("tenant_id"))
	if !ok {
		return
	}

	if id == c.GetString("user_id") {
		if req.Active != nil && !*req.Active {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no puedes desactivar tu propia cuenta"})
			return
		}
		if req.IsAdmin != nil && !*req.IsAdmin {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no puedes quitarte los permisos de administrador"})
			return
		}
	}

	// Pierde la condición de admin vivo si se le quita el flag, o si se le
	// desactiva estando hoy activo como admin. Un usuario que no es admin
	// desactivable no pone en riesgo nada.
	losesAdmin := cur.IsAdmin && cur.Active &&
		((req.IsAdmin != nil && !*req.IsAdmin) || (req.Active != nil && !*req.Active))
	if losesAdmin {
		others, err := countOtherActiveAdmins(ctx, x, id)
		if err != nil {
			internalError(c, "actualizar usuario: contar administradores", err)
			return
		}
		if others == 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "es el único administrador activo: crea o activa otro antes de cambiarlo"})
			return
		}
	}

	set := []string{"updated_at = NOW()"}
	args := make([]interface{}, 0, 4)
	add := func(col string, v interface{}) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if req.Username != nil {
		add("username", *req.Username)
	}
	if req.Email != nil {
		add("email", *req.Email)
	}
	if req.IsAdmin != nil {
		add("is_admin", *req.IsAdmin)
	}
	if req.Active != nil {
		add("active", *req.Active)
	}

	args = append(args, id, c.GetString("tenant_id"))
	query := `UPDATE users SET ` + strings.Join(set, ", ") +
		fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d", len(args)-1, len(args))

	res, err := x.ExecContext(ctx, query, args...)
	if err != nil {
		if db.IsUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "ya existe un usuario con ese nombre o email en este tenant"})
			return
		}
		internalError(c, "actualizar usuario", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}

	updated, ok := userFromRow(c, x,
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND tenant_id = $2`,
		id, c.GetString("tenant_id"))
	if !ok {
		return
	}
	log.Printf("[Users] usuario actualizado: %s (%s)", updated.Username, updated.ID)
	c.JSON(http.StatusOK, updated)
}

// SetUserPassword sustituye la contraseña de otra cuenta.
//
// Es el flujo de "he perdido mi contraseña": entra un admin y la pone. Al
// cambiarla se revocan los refresh tokens del usuario, porque si no la
// contraseña nueva no sirve de nada — la sesión antigua sigue viva hasta que
// caduque, que son siete días.
func (h *Handler) SetUserPassword(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}
	if !validPassword(req.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "la contraseña debe tener entre 8 y 72 bytes"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "restablecer contraseña: hashear", err)
		return
	}

	ctx := c.Request.Context()
	tenantID := c.GetString("tenant_id")
	x := db.Executor(c)

	res, err := x.ExecContext(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3`,
		string(hash), id, tenantID)
	if err != nil {
		internalError(c, "restablecer contraseña", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}

	if _, err := x.ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = $1 AND tenant_id = $2`,
		id, tenantID); err != nil {
		// No se aborta: la contraseña ya está cambiada, que es lo importante.
		// El token viejo solo sirve para seguir refrescando, y se avisa.
		log.Printf("[Users] no se pudieron revocar los refresh tokens de %s: %v", id, err)
	}

	log.Printf("[Users] contraseña restablecida para %s", id)
	c.JSON(http.StatusOK, gin.H{
		"status":  "updated",
		"message": "Contraseña restablecida. Las sesiones activas de ese usuario se han cerrado.",
	})
}

// ToggleUser enciende o apaga una cuenta sin tocar nada más.
//
// Existe como operación aparte de UpdateUser porque es la acción que la tabla
// ofrece como botón y porque con un solo clic no tiene sentido pedirle al
// cliente que reconstruya el cuerpo completo. Las mismas dos prohibiciones que
// en UpdateUser se aplican aquí.
func (h *Handler) ToggleUser(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	x := db.Executor(c)
	tenantID := c.GetString("tenant_id")

	cur, ok := userFromRow(c, x,
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND tenant_id = $2`,
		id, tenantID)
	if !ok {
		return
	}

	newActive := !cur.Active
	if !newActive {
		if id == c.GetString("user_id") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no puedes desactivar tu propia cuenta"})
			return
		}
		if cur.IsAdmin && cur.Active {
			others, err := countOtherActiveAdmins(ctx, x, id)
			if err != nil {
				internalError(c, "alternar usuario: contar administradores", err)
				return
			}
			if others == 0 {
				c.JSON(http.StatusConflict, gin.H{"error": "es el único administrador activo: crea o activa otro antes de desactivarlo"})
				return
			}
		}
	}

	if _, err := x.ExecContext(ctx,
		`UPDATE users SET active = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3`,
		newActive, id, tenantID); err != nil {
		internalError(c, "alternar usuario", err)
		return
	}

	updated, ok := userFromRow(c, x,
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND tenant_id = $2`,
		id, tenantID)
	if !ok {
		return
	}

	// Al desactivar, la sesión del propio usuario afectado también se cae.
	if !newActive {
		if _, err := x.ExecContext(ctx,
			`DELETE FROM refresh_tokens WHERE user_id = $1 AND tenant_id = $2`,
			id, tenantID); err != nil {
			log.Printf("[Users] no se pudieron revocar los refresh tokens de %s: %v", id, err)
		}
	}

	log.Printf("[Users] usuario %s: %s ahora %v", updated.Username, updated.ID, updated.Active)
	c.JSON(http.StatusOK, updated)
}
