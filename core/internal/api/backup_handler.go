package api

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fasterp/backend/internal/backup"
	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
)

// Backup descarga un zip con manifest.json, database.sql y resources/. Solo
// admins del tenant, y solo del propio tenant.
func (h *Handler) Backup(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if c.Param("id") != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
		return
	}

	var slug, name string
	if err := db.DB.QueryRow("SELECT slug, name FROM tenants WHERE id = $1", tenantID).Scan(&slug, &name); err != nil {
		internalError(c, "backup tenant", err)
		return
	}

	var modules []backup.ModuleRef
	rows, err := db.Executor(c).QueryContext(c.Request.Context(),
		"SELECT name, version FROM installed_modules WHERE tenant_id = $1 ORDER BY name", tenantID)
	if err != nil {
		internalError(c, "backup modules", err)
		return
	}
	for rows.Next() {
		var m backup.ModuleRef
		if err := rows.Scan(&m.Name, &m.Version); err != nil {
			rows.Close()
			internalError(c, "backup modules", err)
			return
		}
		modules = append(modules, m)
	}
	rows.Close()

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="backup-%s-%s.zip"`, slug, time.Now().Format("20060102")))
	if err := backup.WriteZip(c.Request.Context(), db.Executor(c), tenantID, name, slug, h.modManager.UploadDir, modules, c.Writer); err != nil {
		// El cuerpo ya empezó a bajar: no se puede cambiar el código. Se
		// registra para depurar y el cliente verá un zip truncado.
		log.Printf("[Backup] error escribiendo el zip: %v", err)
	}
}

// Restore sobrescribe el tenant actual con el contenido de un zip de backup.
// Exige ?confirm=true: es destructivo para los datos del tenant.
func (h *Handler) Restore(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	if c.Param("id") != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
		return
	}
	if c.Query("confirm") != "true" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "restaurar borra los datos actuales del tenant: vuelve a llamar con ?confirm=true"})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "falta el archivo 'file' (el zip de backup)"})
		return
	}
	tmp, err := os.CreateTemp("", "fasterp-backup-*.zip")
	if err != nil {
		internalError(c, "restore tempfile", err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	src, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no se pudo leer el archivo subido"})
		return
	}
	defer src.Close()
	if _, err := io.Copy(tmp, src); err != nil {
		internalError(c, "restore copy", err)
		return
	}

	stat, err := tmp.Stat()
	if err != nil {
		internalError(c, "restore stat", err)
		return
	}
	zr, err := zip.NewReader(tmp, stat.Size())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el archivo no es un zip válido"})
		return
	}

	manifest, err := backup.LoadManifest(zr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Aviso de módulos: si el VPS destino no tiene uno de los módulos del
	// backup, sus tablas no existen y el restore fallará o dejará huecos.
	var warnings []string
	for _, m := range manifest.Modules {
		if module.Global.Get(m.Name) == nil {
			warnings = append(warnings, "módulo no presente en este servidor: "+m.Name)
		}
	}

	dump, err := backup.ReadDatabaseSQL(zr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dump = backup.RewriteTenantID(dump, manifest.TenantID, tenantID)

	// Si el tenant de origen del backup sigue existiendo en ESTE servidor, el
	// restore pisa su PK... mejor: avisar. Restaurar un export propio sobre sí
	// mismo no es redirigible porque los ids del dump ya están en uso.
	if manifest.TenantID != tenantID {
		var exists int
		_ = db.DB.QueryRow("SELECT 1 FROM tenants WHERE id = $1", manifest.TenantID).Scan(&exists)
		if exists == 1 {
			c.JSON(http.StatusConflict, gin.H{"error": "este backup pertenece a un tenant que aún existe en este servidor; restaura sobre otro tenant o borra el origen primero"})
			return
		}
	}

	ctx := c.Request.Context()
	conn, err := db.AcquireConn(ctx, tenantID)
	if err != nil {
		internalError(c, "restore conn", err)
		return
	}
	defer conn.Close()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		internalError(c, "restore tx", err)
		return
	}
	if err := backup.PurgeTenant(ctx, tx, tenantID); err != nil {
		tx.Rollback()
		internalError(c, "restore purge", err)
		return
	}
	// El tenant destino conserva su id, pero hereda nombre y slug del backup:
	// es lo que lo deja idéntico al origen. Si el slug ya lo usa otro tenant,
	// la tx aborta y no se pierde nada.
	if manifest.Slug != "" {
		if _, err := tx.ExecContext(ctx,
			"UPDATE tenants SET slug = $1, name = $2, updated_at = NOW() WHERE id = $3",
			manifest.Slug, manifest.Name, tenantID); err != nil {
			tx.Rollback()
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "el slug del backup ya está en uso: " + err.Error()})
			return
		}
	}
	dump = backup.StripTenantsInsert(dump)
	if _, err := tx.ExecContext(ctx, dump); err != nil {
		tx.Rollback()
		v := gin.H{"error": "restore fallido: " + err.Error()}
		if len(warnings) > 0 {
			v["warnings"] = warnings
		}
		c.JSON(http.StatusUnprocessableEntity, v)
		return
	}
	if err := tx.Commit(); err != nil {
		internalError(c, "restore commit", err)
		return
	}

	files, err := backup.ExtractResources(zr, h.modManager.UploadDir)
	if err != nil {
		internalError(c, "restore resources", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "restore completado",
		"rows_restored": backup.CountInserts(dump),
		"files":         files,
		"warnings":      warnings,
	})
}
