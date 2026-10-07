package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
)

// roleColumns es la proyección única de los handlers de roles. Igual que con
// users: toda fila que sale de la tabla pasa por aquí, y añadir una columna
// cambia este const y nada más.
const roleColumns = "id, tenant_id, name, description, created_at, updated_at"

// roleNamePattern valida el nombre de un rol. Menos estricto que el de usuario
// porque es una etiqueta que se ve, no una credencial: admite espacios, punto y
// cualquier letra (tildes y ñ incluidas).
var roleNamePattern = regexp.MustCompile(`^[\p{L}0-9 _.-]{1,100}$`)

// roleOut es la forma JSON de un rol. Owner de su propia serialización para que
// el tiempo no se filtre como cadena local ni con zona rara.
type roleOut struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func scanRole(row interface{ Scan(...interface{}) error }) (roleOut, error) {
	var r roleOut
	err := row.Scan(&r.ID, &r.TenantID, &r.Name, &r.Description, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func roleFromRow(c *gin.Context, q db.QueryExecutor, query string, args ...interface{}) (roleOut, bool) {
	r, err := scanRole(q.QueryRowContext(c.Request.Context(), query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "rol no encontrado"})
		return roleOut{}, false
	}
	if err != nil {
		internalError(c, "leer rol", err)
		return roleOut{}, false
	}
	return r, true
}

// parseRoleID valida el path param antes de que llegue a Postgres: un id que no
// es UUID daría un error de tipo de dato con traza, que es un 500 para lo que
// es una petición mal formada.
func parseRoleID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id de rol no válido"})
		return "", false
	}
	return id, true
}

// permRef es un permiso, sin el contenedor: bajo un rol o en el catálogo.
type permRef struct {
	Module string `json:"module"`
	Model  string `json:"model"`
	Action string `json:"action"`
}

// permActions son las cuatro operaciones del CRUD, en el mismo orden en que se
// declaran en dispatchModel.
var permActions = []string{"read", "create", "update", "delete"}

// ListRoles devuelve los roles del tenant, ordenados por nombre.
func (h *Handler) ListRoles(c *gin.Context) {
	rows, err := db.Executor(c).QueryContext(c.Request.Context(),
		`SELECT `+roleColumns+` FROM roles ORDER BY name`)
	if err != nil {
		internalError(c, "listar roles", err)
		return
	}
	defer rows.Close()

	out := make([]roleOut, 0, 8)
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			internalError(c, "listar roles", err)
			return
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		internalError(c, "listar roles", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// CreateRole da de alta un rol. Solo define la etiqueta: los permisos y las
// asignaciones a usuarios llegan por sus propios endpoints.
func (h *Handler) CreateRole(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}
	if msg := validateRoleName(req.Name); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	if len(req.Description) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "la descripción no puede superar los 500 caracteres"})
		return
	}

	var r roleOut
	r, err := scanRole(db.Executor(c).QueryRowContext(c.Request.Context(),
		`INSERT INTO roles (id, tenant_id, name, description)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+roleColumns,
		uuid.New().String(), c.GetString("tenant_id"), req.Name, req.Description))
	switch {
	case err == nil:
	case db.IsUniqueViolation(err):
		c.JSON(http.StatusConflict, gin.H{"error": "ya existe un rol con ese nombre"})
		return
	default:
		internalError(c, "crear rol", err)
		return
	}

	log.Printf("[Roles] rol creado: %s (%s)", r.Name, r.ID)
	c.JSON(http.StatusCreated, r)
}

func validateRoleName(name string) string {
	name = strings.TrimSpace(name)
	if !roleNamePattern.MatchString(name) {
		return "el nombre debe tener entre 1 y 100 caracteres (letras, números, espacios, punto o guion)"
	}
	return ""
}

// GetRole devuelve un rol por id.
func (h *Handler) GetRole(c *gin.Context) {
	id, ok := parseRoleID(c)
	if !ok {
		return
	}
	r, _ := roleFromRow(c, db.Executor(c),
		`SELECT `+roleColumns+` FROM roles WHERE id = $1 AND tenant_id = $2`,
		id, c.GetString("tenant_id"))
	if r.ID != "" {
		c.JSON(http.StatusOK, r)
	}
}

// UpdateRole modifica nombre y/o descripción. Solo se escribe lo que llega,
// igual que en UpdateUser: un cero no borra una descripción.
func (h *Handler) UpdateRole(c *gin.Context) {
	id, ok := parseRoleID(c)
	if !ok {
		return
	}

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}
	if req.Name == nil && req.Description == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no hay nada que actualizar"})
		return
	}
	if req.Name != nil {
		if msg := validateRoleName(*req.Name); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
	}
	description := ""
	if req.Description != nil {
		description = strings.TrimSpace(*req.Description)
		if len(description) > 500 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "la descripción no puede superar los 500 caracteres"})
			return
		}
	}

	set := []string{"updated_at = NOW()"}
	args := make([]interface{}, 0, 3)
	add := func(col string, v interface{}) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if req.Name != nil {
		add("name", *req.Name)
	}
	if req.Description != nil {
		add("description", description)
	}

	args = append(args, id, c.GetString("tenant_id"))
	query := `UPDATE roles SET ` + strings.Join(set, ", ") +
		fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d", len(args)-1, len(args))

	res, err := db.Executor(c).ExecContext(c.Request.Context(), query, args...)
	if err != nil {
		if db.IsUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "ya existe un rol con ese nombre"})
			return
		}
		internalError(c, "actualizar rol", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rol no encontrado"})
		return
	}

	updated, ok := roleFromRow(c, db.Executor(c),
		`SELECT `+roleColumns+` FROM roles WHERE id = $1 AND tenant_id = $2`,
		id, c.GetString("tenant_id"))
	if !ok {
		return
	}
	log.Printf("[Roles] rol actualizado: %s (%s)", updated.Name, updated.ID)
	c.JSON(http.StatusOK, updated)
}

// DeleteRole borra un rol y sus consecuencias: las asignaciones a usuarios y
// los permisos del rol salen por delante, explícitos, porque bajo FORCE RLS no
// se da por hecho que la cascada de la FK corra como el dueño.
func (h *Handler) DeleteRole(c *gin.Context) {
	id, ok := parseRoleID(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	x := db.Executor(c)
	tenantID := c.GetString("tenant_id")

	tx, err := db.BeginTenantTx(ctx, x)
	if err != nil {
		internalError(c, "borrar rol", err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_roles WHERE role_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		internalError(c, "borrar rol: desasignar usuarios", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM role_permissions WHERE role_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		internalError(c, "borrar rol: quitar permisos", err)
		return
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM roles WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		internalError(c, "borrar rol", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rol no encontrado"})
		return
	}
	if err := tx.Commit(); err != nil {
		internalError(c, "borrar rol", err)
		return
	}

	log.Printf("[Roles] rol borrado: %s", id)
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ListRolePermissions devuelve los permisos de un rol, sin comprobar que el rol
// exista primero: una lista vacía para un id ajeno y un 404 para el rol son el
// mismo trabajo en dos idas. Si el rol no existe responde 404 igualmente.
func (h *Handler) ListRolePermissions(c *gin.Context) {
	id, ok := parseRoleID(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	x := db.Executor(c)
	tenantID := c.GetString("tenant_id")

	var exists bool
	if err := x.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1 AND tenant_id = $2)`, id, tenantID,
	).Scan(&exists); err != nil {
		internalError(c, "leer permisos del rol", err)
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "rol no encontrado"})
		return
	}

	rows, err := x.QueryContext(ctx,
		`SELECT module, model, action FROM role_permissions
		 WHERE role_id = $1 AND tenant_id = $2 ORDER BY module, model, action`,
		id, tenantID)
	if err != nil {
		internalError(c, "leer permisos del rol", err)
		return
	}
	defer rows.Close()

	out := make([]permRef, 0, 16)
	for rows.Next() {
		var p permRef
		if err := rows.Scan(&p.Module, &p.Model, &p.Action); err != nil {
			internalError(c, "leer permisos del rol", err)
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		internalError(c, "leer permisos del rol", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ReplaceRolePermissions sustituye el conjunto completo de permisos de un rol:
// lo que no viene, desaparece. Es un PUT del conjunto, no una edición de filas.
//
// Cada permiso se valida contra el catálogo real (módulo activo y modelo del
// manifest) para que no haya permisos que nadie podrá conceder ni comprobar.
func (h *Handler) ReplaceRolePermissions(c *gin.Context) {
	id, ok := parseRoleID(c)
	if !ok {
		return
	}

	var req struct {
		Permissions []permRef `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}

	valid, err := h.activeModuleModels(c)
	if err != nil {
		internalError(c, "validar permisos del rol", err)
		return
	}

	seen := make(map[permRef]bool, len(req.Permissions))
	perms := make([]permRef, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		if !validAction(p.Action) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "acción de permiso no válida: " + p.Action})
			return
		}
		models, ok := valid[p.Module]
		if !ok || !models[p.Model] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "módulo o modelo no disponible: " + p.Module + "." + p.Model})
			return
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		perms = append(perms, p)
	}

	ctx := c.Request.Context()
	x := db.Executor(c)
	tenantID := c.GetString("tenant_id")

	tx, err := db.BeginTenantTx(ctx, x)
	if err != nil {
		internalError(c, "guardar permisos del rol", err)
		return
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1 AND tenant_id = $2)`, id, tenantID,
	).Scan(&exists); err != nil {
		internalError(c, "guardar permisos del rol", err)
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "rol no encontrado"})
		return
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM role_permissions WHERE role_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		internalError(c, "guardar permisos del rol", err)
		return
	}
	for _, p := range perms {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO role_permissions (tenant_id, role_id, module, model, action)
			 VALUES ($1, $2, $3, $4, $5)`,
			tenantID, id, p.Module, p.Model, p.Action); err != nil {
			internalError(c, "guardar permisos del rol", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		internalError(c, "guardar permisos del rol", err)
		return
	}

	log.Printf("[Roles] %d permisos guardados en el rol %s", len(perms), id)
	c.JSON(http.StatusOK, gin.H{"status": "updated", "stored": len(perms)})
}

func validAction(action string) bool {
	for _, a := range permActions {
		if a == action {
			return true
		}
	}
	return false
}

// activeModuleModels devuelve el mapa módulo → modelo de los módulos activos
// del tenant. Es lo que valida los permisos al guardarlos: si el módulo no
// está activo no hay nadie comprobando sus permisos, y si el modelo no existe
// en el manifest ni siquiera hay base.
func (h *Handler) activeModuleModels(c *gin.Context) (map[string]map[string]bool, error) {
	rows, err := db.Executor(c).QueryContext(c.Request.Context(),
		`SELECT name FROM installed_modules WHERE tenant_id = $1 AND active ORDER BY name`,
		c.GetString("tenant_id"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		inst := module.Global.Get(name)
		if inst == nil {
			continue
		}
		models := make(map[string]bool)
		for _, m := range inst.Models {
			if m.Manifest != nil {
				models[m.Manifest.Name] = true
			}
		}
		out[name] = models
	}
	return out, rows.Err()
}

// ListUserRoles devuelve los roles asignados a un usuario.
func (h *Handler) ListUserRoles(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	x := db.Executor(c)
	tenantID := c.GetString("tenant_id")

	var exists bool
	if err := x.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND tenant_id = $2)`, id, tenantID,
	).Scan(&exists); err != nil {
		internalError(c, "leer roles del usuario", err)
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}

	rows, err := x.QueryContext(ctx,
		`SELECT `+roleColumns+` FROM roles r
		 JOIN user_roles ur ON ur.role_id = r.id AND ur.tenant_id = r.tenant_id
		 WHERE ur.user_id = $1 AND ur.tenant_id = $2 ORDER BY r.name`,
		id, tenantID)
	if err != nil {
		internalError(c, "leer roles del usuario", err)
		return
	}
	defer rows.Close()

	out := make([]roleOut, 0, 4)
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			internalError(c, "leer roles del usuario", err)
			return
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		internalError(c, "leer roles del usuario", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ReplaceUserRoles sustituye el conjunto de roles de un usuario en una
// operación: lo que no viene, se quita.
//
// Es la operación que enciende el default-deny del tenant: en cuanto hay una
// fila en user_roles, los usuarios sin rol dejan de heredar el todo-permitido.
// La advertencia vive en la UI, no aquí; este endpoint es solo el medio.
func (h *Handler) ReplaceUserRoles(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}

	var req struct {
		RoleIDs []string `json:"role_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "petición inválida"})
		return
	}

	seen := make(map[string]bool, len(req.RoleIDs))
	roleIDs := make([]string, 0, len(req.RoleIDs))
	for _, rid := range req.RoleIDs {
		if _, err := uuid.Parse(rid); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "id de rol no válido: " + rid})
			return
		}
		if seen[rid] {
			continue
		}
		seen[rid] = true
		roleIDs = append(roleIDs, rid)
	}

	ctx := c.Request.Context()
	x := db.Executor(c)
	tenantID := c.GetString("tenant_id")

	tx, err := db.BeginTenantTx(ctx, x)
	if err != nil {
		internalError(c, "guardar roles del usuario", err)
		return
	}
	defer tx.Rollback()

	var userExists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND tenant_id = $2)`, id, tenantID,
	).Scan(&userExists); err != nil {
		internalError(c, "guardar roles del usuario", err)
		return
	}
	if !userExists {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}

	for _, rid := range roleIDs {
		var okRole bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1 AND tenant_id = $2)`, rid, tenantID,
		).Scan(&okRole); err != nil {
			internalError(c, "guardar roles del usuario", err)
			return
		}
		if !okRole {
			c.JSON(http.StatusBadRequest, gin.H{"error": "el rol " + rid + " no existe en este tenant"})
			return
		}
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_roles WHERE user_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		internalError(c, "guardar roles del usuario", err)
		return
	}
	for _, rid := range roleIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1, $2, $3)`,
			tenantID, id, rid); err != nil {
			internalError(c, "guardar roles del usuario", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		internalError(c, "guardar roles del usuario", err)
		return
	}

	log.Printf("[Roles] %d roles asignados al usuario %s", len(roleIDs), id)
	c.JSON(http.StatusOK, gin.H{"status": "updated", "assigned": len(roleIDs)})
}

// catalogModule es la entrada del catálogo de permisos: lo que un rol puede
// pedir, y lo que la UI pinta para montar el editor.
type catalogModule struct {
	Module string         `json:"module"`
	Label  string         `json:"label"`
	Models []catalogModel `json:"models"`
}

type catalogModel struct {
	Model   string   `json:"model"`
	Label   string   `json:"label"`
	Actions []string `json:"actions"`
}

// PermissionsCatalog enumera lo concedible: módulos activos del tenant con sus
// modelos. Sin este contrato, el editor de permisos arriesga a escribir
// permisos que nadie podrá comprobar nunca.
func (h *Handler) PermissionsCatalog(c *gin.Context) {
	rows, err := db.Executor(c).QueryContext(c.Request.Context(),
		`SELECT name FROM installed_modules WHERE tenant_id = $1 AND active ORDER BY name`,
		c.GetString("tenant_id"))
	if err != nil {
		internalError(c, "listar catálogo de permisos", err)
		return
	}
	defer rows.Close()

	out := make([]catalogModule, 0, 8)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			internalError(c, "listar catálogo de permisos", err)
			return
		}
		inst := module.Global.Get(name)
		if inst == nil {
			continue
		}
		models := make([]catalogModel, 0, len(inst.Models))
		for _, m := range inst.Models {
			if m.Manifest == nil {
				continue
			}
			models = append(models, catalogModel{
				Model:   m.Manifest.Name,
				Label:   m.Manifest.Label,
				Actions: append([]string(nil), permActions...),
			})
		}
		sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
		out = append(out, catalogModule{
			Module: name,
			Label:  inst.Manifest.Label,
			Models: models,
		})
	}
	if err := rows.Err(); err != nil {
		internalError(c, "listar catálogo de permisos", err)
		return
	}
	c.JSON(http.StatusOK, out)
}
