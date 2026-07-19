package api

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
	"github.com/gin-gonic/gin"
)

// RegisterModuleDataRoutes installs a single dispatcher for every module model's
// CRUD surface, registered once at startup.
//
// Previously each model got its own route, added to a live gin.Engine whenever a
// module was toggled. Mutating the router after it starts serving is a data race
// against in-flight requests, and because routes are global it also leaked one
// tenant's modules into every other tenant's routing table. Resolution now happens
// per request instead, against the calling tenant.
func (h *Handler) RegisterModuleDataRoutes(rg *gin.RouterGroup) {
	rg.GET("/:module/:model", h.dispatchModel(listHandler))
	rg.POST("/:module/:model", h.dispatchModel(createHandler))
	rg.GET("/:module/:model/:id", h.dispatchModel(getHandler))
	rg.PUT("/:module/:model/:id", h.dispatchModel(updateHandler))
	rg.DELETE("/:module/:model/:id", h.dispatchModel(deleteHandler))
}

// dispatchModel resolves the module and model named in the path, verifies the module
// is installed and active for the calling tenant, then delegates to build.
func (h *Handler) dispatchModel(build func(module.ModelRegistration) gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		moduleName, modelName := c.Param("module"), c.Param("model")

		inst := module.Global.Get(moduleName)
		if inst == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "module not found"})
			return
		}

		var active bool
		err := db.Executor(c).QueryRowContext(c.Request.Context(),
			"SELECT active FROM installed_modules WHERE name = $1 AND tenant_id = $2",
			moduleName, c.GetString("tenant_id"),
		).Scan(&active)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "module not installed for this tenant"})
			return
		}
		if err != nil {
			internalError(c, "resolve module", err)
			return
		}
		if !active {
			c.JSON(http.StatusForbidden, gin.H{"error": "module is disabled for this tenant"})
			return
		}

		for _, model := range inst.Models {
			if model.Manifest.Name == modelName {
				build(model)(c)
				return
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
	}
}

func listHandler(m module.ModelRegistration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
		qb := module.NewQueryBuilder(m)
		qb.Where("tenant_id", "=", tenantID).OrderBy("created_at", "DESC")
		query, args, err := qb.BuildSelect()
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		rows, err := db.Executor(c).QueryContext(ctx, query, args...)
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		defer rows.Close()

		result, err := rowsToMap(rows, m.Manifest.Fields)
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func getHandler(m module.ModelRegistration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, tenantID := c.Param("id"), c.GetString("tenant_id")
		qb := module.NewQueryBuilder(m)
		qb.Where("id", "=", id).Where("tenant_id", "=", tenantID)
		query, args, err := qb.BuildSelect()
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		row := db.Executor(c).QueryRowContext(ctx, query, args...)
		result, err := rowToMap(row, m.Manifest.Fields)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func createHandler(m module.ModelRegistration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
		var body map[string]interface{}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := validateInput(m.Manifest.Fields, body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		qb := module.NewQueryBuilder(m)
		query, args, err := qb.BuildInsert(tenantID, body)
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}

		row := db.Executor(c).QueryRowContext(ctx, query, args...)
		result, err := rowToMap(row, m.Manifest.Fields)
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		result["tenant_id"] = tenantID
		c.JSON(http.StatusCreated, result)
	}
}

func updateHandler(m module.ModelRegistration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, tenantID := c.Param("id"), c.GetString("tenant_id")
		var body map[string]interface{}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := validateInput(m.Manifest.Fields, body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		qb := module.NewQueryBuilder(m)
		query, args, err := qb.BuildUpdate(id, tenantID, body)
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}

		row := db.Executor(c).QueryRowContext(ctx, query, args...)
		result, err := rowToMap(row, m.Manifest.Fields)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func deleteHandler(m module.ModelRegistration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, tenantID := c.Param("id"), c.GetString("tenant_id")

		qb := module.NewQueryBuilder(m)
		query, _, err := qb.BuildDelete()
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}

		result, err := db.Executor(c).ExecContext(ctx, query, id, tenantID)
		if err != nil {
			internalError(c, "module "+m.Manifest.Name, err)
			return
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}

type FieldInfo struct{ Name string }

func rowsToMap(rows *sql.Rows, fields []module.FieldDef) ([]map[string]interface{}, error) {
	infos := make([]FieldInfo, 0, len(fields)+3)
	infos = append(infos, FieldInfo{Name: "id"})
	for _, f := range fields {
		infos = append(infos, FieldInfo{Name: f.Name})
	}
	infos = append(infos, FieldInfo{Name: "created_at"})
	infos = append(infos, FieldInfo{Name: "updated_at"})

	var result []map[string]interface{}
	for rows.Next() {
		m, err := scanRow(rows, infos)
		if err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	return result, rows.Err()
}

func rowToMap(row *sql.Row, fields []module.FieldDef) (map[string]interface{}, error) {
	infos := make([]FieldInfo, 0, len(fields)+3)
	infos = append(infos, FieldInfo{Name: "id"})
	for _, f := range fields {
		infos = append(infos, FieldInfo{Name: f.Name})
	}
	infos = append(infos, FieldInfo{Name: "created_at"})
	infos = append(infos, FieldInfo{Name: "updated_at"})
	return scanRow(row, infos)
}

func scanRow(row interface {
	Scan(dest ...interface{}) error
}, fields []FieldInfo) (map[string]interface{}, error) {
	vals := make([]interface{}, len(fields))
	ptrs := make([]interface{}, len(fields))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, err
	}
	m := make(map[string]interface{}, len(fields))
	for i, f := range fields {
		v := vals[i]
		switch b := v.(type) {
		case []byte:
			m[f.Name] = string(b)
		default:
			m[f.Name] = v
		}
	}
	return m, nil
}

func validateInput(fields []module.FieldDef, body map[string]interface{}) error {
	for _, f := range fields {
		val, ok := body[f.Name]
		if f.Required && (!ok || val == nil) {
			return fmt.Errorf("%s is required", f.Name)
		}
		if !ok || val == nil {
			continue
		}
		switch f.Type {
		case "string":
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("%s must be a string", f.Name)
			}
			if len(s) > 255 {
				return fmt.Errorf("%s exceeds 255 characters", f.Name)
			}
		case "text":
			_, ok := val.(string)
			if !ok {
				return fmt.Errorf("%s must be a string", f.Name)
			}
		case "int", "integer":
			switch val.(type) {
			case float64:
			case int:
			case int64:
			default:
				return fmt.Errorf("%s must be a number", f.Name)
			}
		case "float":
			switch val.(type) {
			case float64:
			default:
				return fmt.Errorf("%s must be a decimal number", f.Name)
			}
		case "bool", "boolean":
			_, ok := val.(bool)
			if !ok {
				return fmt.Errorf("%s must be a boolean", f.Name)
			}
		case "date", "datetime":
			_, ok := val.(string)
			if !ok {
				return fmt.Errorf("%s must be a date string", f.Name)
			}
		}
	}
	return nil
}
