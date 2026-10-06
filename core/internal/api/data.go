package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/exportimport"
	"github.com/fasterp/backend/internal/module"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Rutas del CRUD de un módulo.
//
// Se registran una sola vez y resuelven por petición, contra el tenant que
// llama. Añadir rutas a un gin.Engine con el servidor ya escuchando es una
// carrera contra las peticiones en vuelo, y como el router es global también
// metía los módulos de un tenant en la tabla de rutas de los demás.
func (h *Handler) RegisterModuleDataRoutes(rg *gin.RouterGroup) {
	// _meta va antes que :model para que "meta" no se interprete como el
	// nombre de un modelo. Gin resuelve los segmentos estáticos antes que los
	// comodines, pero registrar la ruta en orden deja el router legible.
	rg.GET("/:module/_meta", h.dispatchModuleList(moduleMetaHandler))
	rg.GET("/:module/:model/_meta", h.dispatchModel("read", modelMetaHandler))

	rg.GET("/:module/:model/export", h.dispatchModel("read", exportHandler))
	rg.POST("/:module/:model/import", h.dispatchModel("create", importHandler))

	rg.GET("/:module/:model", h.dispatchModel("read", listHandler))
	rg.POST("/:module/:model", h.dispatchModel("create", createHandler))
	rg.GET("/:module/:model/:id", h.dispatchModel("read", getHandler))
	rg.PUT("/:module/:model/:id", h.dispatchModel("update", updateHandler))
	rg.PATCH("/:module/:model/:id", h.dispatchModel("update", updateHandler))
	rg.DELETE("/:module/:model/:id", h.dispatchModel("delete", deleteHandler))

	// Workflow. El historial va antes que la transición porque las dos cuelgan
	// de /:id y una ruta con más segmentos no compite con la otra; el orden solo
	// afecta a la legibilidad.
	rg.GET("/:module/:model/:id/transitions", h.dispatchModel("read", transitionsHandler))
	rg.POST("/:module/:model/:id/transitions/:action", h.dispatchModel("update", transitionHandler))
	rg.GET("/:module/:model/:id/history", h.dispatchModel("read", historyHandler))
}

// moduleAction es una operación sobre un modelo. model es nil en las acciones a
// nivel de módulo.
type moduleAction func(c *gin.Context, inst *module.ModuleInstance, model *module.ModelRegistration)

// resolveModule comprueba que el módulo existe y está activo para el tenant que
// llama.
//
// Que el módulo esté instalado y activo se lee de installed_modules con el
// executor del tenant: es una tabla con RLS, y db.DB (el pool compartido) no la
// ve. El registro en memoria, en cambio, es del proceso y no lleva tenant.
func (h *Handler) resolveModule(c *gin.Context, name string) (*module.ModuleInstance, bool) {
	inst := module.Global.Get(name)
	if inst == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "module not found"})
		return nil, false
	}

	if bypass, ok := c.Get("bypass_installed_check"); ok && bypass == true {
		return inst, true
	}

	var active bool
	err := db.Executor(c).QueryRowContext(c.Request.Context(),
		"SELECT active FROM installed_modules WHERE name = $1 AND tenant_id = $2",
		name, c.GetString("tenant_id"),
	).Scan(&active)
	switch {
	case err == sql.ErrNoRows:
		c.JSON(http.StatusNotFound, gin.H{"error": "module not installed for this tenant"})
		return nil, false
	case err != nil:
		internalError(c, "resolve module", err)
		return nil, false
	case !active:
		c.JSON(http.StatusForbidden, gin.H{"error": "module is disabled for this tenant"})
		return nil, false
	}
	return inst, true
}

// dispatchModuleList opera a nivel de módulo, para el _meta que lista sus
// modelos.
// dispatchModuleList opera a nivel de módulo, para el _meta que lista sus
// modelos. El modelo de la ruta no se usa aquí: es nil.
func (h *Handler) dispatchModuleList(build moduleAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		inst, ok := h.resolveModule(c, c.Param("module"))
		if !ok {
			return
		}
		build(c, inst, nil)
	}
}

// dispatchModel resuelve el modelo y ejecuta la acción.
func (h *Handler) dispatchModel(action string, build moduleAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		inst, ok := h.resolveModule(c, c.Param("module"))
		if !ok {
			return
		}

		reg := inst.Model(c.Param("model"))
		if reg == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
			return
		}
		if !h.requirePermission(c, inst, reg, action) {
			return
		}
		build(c, inst, reg)
	}
}

// listHandler devuelve las filas de un modelo, paginadas y filtradas.
//
// La respuesta lleva los metadatos de paginación dentro del mismo objeto, en
// vez de una cabecera: el cliente la entrega tal cual a la tabla, que necesita
// el total para pintar el paginador y no va a hacer una segunda petición por eso.
func listHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	modelName := reg.Manifest.Name

	qb := module.NewQueryBuilder(*reg)

	// El filtro por tenant va primero y no se quita: es el límite de seguridad,
	// no un criterio de búsqueda.
	qb.Where("tenant_id", "=", tenantID)

	// Búsqueda libre sobre los campos de texto del manifest.
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		applySearch(qb, inst.Manifest, *reg, search)
	}

	// Filtros explícitos: ?filter=campo:op:valor, repetibles.
	filters, err := parseFilters(c, reg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	for _, f := range filters {
		qb.Where(f.field, f.op, f.value)
	}

	// Orden. Por defecto, lo más reciente primero, que es lo que se espera al
	// abrir una lista. dir se pasa tal cual: OrderBy rechaza lo que no sea ASC o
	// DESC, y un "desc; DROP TABLE" tiene que acabar en un 400, no en una
	// consulta.
	orderBy, orderDir := c.DefaultQuery("order", "created_at"), c.DefaultQuery("dir", "desc")
	if orderBy != "created_at" && orderBy != "updated_at" && orderBy != "id" && !reg.HasField(orderBy) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown order field: " + orderBy})
		return
	}
	qb.OrderBy(orderBy, orderDir)

	// Paginación. El límite se ajusta al tamaño de página permitido más
	// cercano, y no se acepta el que venga: pedir 100000 filas no es una
	// paginación, es una forma de tumbar el proceso.
	page, limit := parsePagination(c)
	qb.Limit(limit).Offset((page - 1) * limit)

	cols, query, args, err := qb.BuildSelect()
	if err != nil {
		internalError(c, "list "+modelName, err)
		return
	}

	rows, err := db.Executor(c).QueryContext(ctx, query, args...)
	if err != nil {
		internalError(c, "list "+modelName, err)
		return
	}
	defer rows.Close()

	items, err := rowsToMaps(rows, cols)
	if err != nil {
		internalError(c, "list "+modelName, err)
		return
	}
	if err := hydrateManyToMany(c, inst, reg, items); err != nil {
		internalError(c, "hydrate "+modelName, err)
		return
	}

	total, err := countRows(c, qb, modelName)
	if err != nil {
		internalError(c, "count "+modelName, err)
		return
	}

	// El total va como int64 porque es lo que devuelve COUNT, y el limit como
	// int porque viene de la URL. La paginación se hace en int64 para que el
	// total de una tabla grande no se corte al convertirlo.
	totalPages := (total + int64(limit) - 1) / int64(limit)

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"page":     page,
			"limit":    limit,
			"total":    total,
			"pages":    totalPages,
			"has_more": int64(page*limit) < total,
		},
	})
}

// applySearch añade la condición de búsqueda sobre los campos de texto.
//
// Se construye con ILIKE y parámetros, nunca con Sprintf: un % en lo que
// escribe la persona en el buscador rompería la sentencia, y sin el ESCAPE un
// wildcard inesperado traería más filas de las que se piden.
func applySearch(qb *module.QueryBuilder, m *module.Manifest, reg module.ModelRegistration, search string) {
	meta, err := m.Meta(reg.Manifest.Name)
	if err != nil {
		return
	}
	pattern := "%" + escapeLike(search) + "%"

	// El grupo se entreparéntesis porque viene detrás del AND del tenant:
	// sin ellos, "tenant = A OR nombre ILIKE %x%" deja leer los registros del
	// otro tenant. El builder no los añade por su cuenta porque no sabe dónde
	// empieza el grupo.
	//
	// El ESCAPE va dentro del paréntesis, pegado a cada ILIKE, y no una vez al
	// final del grupo: Postgres rechaza "(... ILIKE ?) ESCAPE '\'" con syntax
	// error, y el 500 que salía era de aquí, no de los datos.
	var group []string
	for _, f := range meta.Fields {
		if f.Searchable {
			group = append(group, quoteIdent(f.Name)+` ILIKE ? ESCAPE '\'`)
		}
	}

	if len(group) == 0 {
		// Un modelo sin campos buscables devuelve cero filas en vez de todas: un
		// ILIKE sobre un id UUID no tiene sentido, y devolverlo todo sería
		// ignorar la búsqueda.
		qb.WhereRaw("FALSE")
		return
	}

	// Un ? por columna, y un valor por ?: el builder no reutiliza un
	// parámetro para varios marcadores.
	args := make([]any, len(group))
	for i := range args {
		args[i] = pattern
	}
	qb.WhereRaw("("+strings.Join(group, " OR ")+")", args...)
}

// escapeLike neutraliza los comodines de LIKE para que un % escrito por la
// persona busque un % de verdad.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// countRows cuenta el total con los mismos filtros, para el paginador.
func countRows(c *gin.Context, qb *module.QueryBuilder, model string) (int64, error) {
	query, args, err := qb.BuildCount()
	if err != nil {
		return 0, err
	}
	var total int64
	err = db.Executor(c).QueryRowContext(c.Request.Context(), query, args...).Scan(&total)
	return total, err
}

// parsePagination lee page y limit, y ajusta el limit a un tamaño permitido.
func parsePagination(c *gin.Context) (page, limit int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}

	limit, _ = strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(module.DefaultPageSize)))
	if limit <= 0 {
		limit = module.DefaultPageSize
	}
	// Se ajusta al tamaño permitido más cercano en vez de rechazar: un
	// limit=30 no es un error, es un 25 con un redondeo.
	limit = module.NearestPageSize(limit)
	return page, limit
}

func nearestPageSize(want int) int {
	return module.NearestPageSize(want)
}

// opAliases son los nombres de operador que usan los clientes para los que
// Postgres ya tiene notación propia.
var opAliases = map[string]string{
	"EQ": "=", "NE": "!=", "GT": ">", "LT": "<",
	"GTE": ">=", "LTE": "<=", "NLIKE": "NOT ILIKE",
	"NOTLIKE": "NOT ILIKE", "NULL": "IS NULL", "NOTNULL": "IS NOT NULL",
}

// filter es un ?filter=campo:op:valor ya validado.
//
// Vive aquí y no en el paquete module porque es una forma de la URL: el parser
// HTTP es del handler, y el builder solo necesita columnas y operadores.
type filter struct {
	field string
	op    string
	value any
}

// parseFilters lee ?filter=campo:op:valor, repetible.
func parseFilters(c *gin.Context, reg *module.ModelRegistration) ([]filter, error) {
	raw := c.QueryArray("filter")
	out := make([]filter, 0, len(raw))

	for _, raw := range raw {
		// SplitN con 3: un valor puede contener dos puntos —una hora, un
		// UUID, una URL— y con un Split normal se partiría por la mitad.
		parts := strings.SplitN(raw, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("filtro inválido %q: se espera campo:operador:valor", raw)
		}
		field, op, text := parts[0], strings.ToUpper(parts[1]), parts[2]

		if !reg.HasField(field) {
			return nil, fmt.Errorf("el campo %q no existe en %s", field, reg.Manifest.Name)
		}

		// Los alias van del idioma del cliente al operador de SQL. Se aceptan
		// aquí y no en la lista blanca porque "eq" no es un operador: es otra
		// forma de escribir "=", y un cliente que manda "eq" no es un ataque.
		if alias, ok := opAliases[op]; ok {
			op = alias
		}
		if !module.IsAllowedOp(op) {
			return nil, fmt.Errorf("operador %q no permitido", op)
		}

		switch op {
		case "IS NULL", "IS NOT NULL":
			// Unarios: el valor se descarta. Aceptarlo sin más dejaría que
			// "x:IS NULL:loquesea" pareciera meaningful.
			out = append(out, filter{field: field, op: op})
		case "IN", "NOT IN":
			items := splitList(text)
			if len(items) == 0 {
				return nil, fmt.Errorf("el filtro %s necesita al menos un valor", op)
			}
			out = append(out, filter{field: field, op: op, value: items})
		default:
			out = append(out, filter{field: field, op: op, value: reg.CastValue(field, text)})
		}
	}
	return out, nil
}

// splitList parte "a,b,c" en sus partes, respetando el separador dentro de
// comillas para el caso de "O'Brien,Ana".
// maxExportIDs acota cuántos registros se pueden exportar por selección. El
// export completo ya se topa con limit; esta lista llega en la URL y sin tope
// una de 100k ids se vuelve un Where de 100k parámetros.
const maxExportIDs = 1000

// maxExportRows es el tope de filas de un export. Va aparte del tamaño de
// página a propósito: el listado pide limit=10 para pintar diez filas, y si el
// export leyera ese mismo parámetro el archivo salía truncado a la página
// visible sin avisar.
const maxExportRows = 50000

// parseIDList valida la lista de ids de una exportación por selección. Cada uno
// tiene que ser un UUID: es lo que garantiza que el valor vaya como parámetro
// de tipo uuid y no como texto que Postgres tenga que adivinar.
func parseIDList(raw string) ([]any, error) {
	parts := strings.Split(raw, ",")
	if len(parts) > maxExportIDs {
		return nil, fmt.Errorf("no se pueden exportar más de %d registros seleccionados", maxExportIDs)
	}

	out := make([]any, 0, len(parts))
	for _, p := range parts {
		id := strings.TrimSpace(p)
		if id == "" {
			continue
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("id inválido en la selección: %q", id)
		}
		out = append(out, parsed)
	}
	if len(out) == 0 {
		return nil, errors.New("la selección no contiene ningún id")
	}
	return out, nil
}

func splitList(text string) []any {
	parts := strings.Split(text, ",")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(strings.Trim(p, `"`)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// getHandler devuelve un registro.
func getHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

	qb := module.NewQueryBuilder(*reg).
		Where("id", "=", c.Param("id")).
		Where("tenant_id", "=", tenantID)

	cols, query, args, err := qb.BuildSelect()
	if err != nil {
		internalError(c, "get "+reg.Manifest.Name, err)
		return
	}

	item, err := rowToMap(db.Executor(c).QueryRowContext(ctx, query, args...), cols)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		internalError(c, "get "+reg.Manifest.Name, err)
		return
	}
	if err := hydrateManyToMany(c, inst, reg, []map[string]any{item}); err != nil {
		internalError(c, "hydrate "+reg.Manifest.Name, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// createHandler inserta un registro.
//
// El campo de estado del workflow no se acepta aquí aunque venga en el cuerpo:
// sale por su valor inicial y solo cambia por una transición. Aceptarlo dejaría
// un registro en un estado por el que nunca pasó, y el historial mentiría.
func createHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// El estado se pone al crear, y sale del workflow. Por eso el estado
	// inicial no se manda en el INSERT: lo escribe la columna, con el default
	// que el reconciliador dejó puesto. Si viniera en el cuerpo, se aceptaría
	// sin comprobar que sea un estado válido y el registro nacería en un estado
	// por el que nunca pasó.
	if wf := reg.Manifest.Workflow; wf != nil {
		if _, enviado := body[wf.Field]; enviado {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "el estado lo pone el workflow: " + wf.Initial,
			})
			return
		}
	}

	meta, err := inst.Manifest.Meta(reg.Manifest.Name)
	if err != nil {
		internalError(c, "meta "+reg.Manifest.Name, err)
		return
	}
	if err := validateInput(meta, body, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	stored, m2m, err := relationalFields(reg, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := checkRelatedExist(c, inst, reg, stored); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	qb := module.NewQueryBuilder(*reg)
	query, args, err := qb.BuildInsert(tenantID, c.GetString("user_id"), stored)
	if err != nil {
		internalError(c, "create "+reg.Manifest.Name, err)
		return
	}

	cols := returningColumns(reg)
	item, err := rowToMap(db.Executor(c).QueryRowContext(ctx, query, args...), cols)
	if err != nil {
		internalError(c, "create "+reg.Manifest.Name, err)
		return
	}
	item["tenant_id"] = tenantID

	id, _ := item["id"].(string)
	if err := applyManyToMany(c, inst, reg, id, m2m); err != nil {
		internalError(c, "create m2m "+reg.Manifest.Name, err)
		return
	}
	if err := hydrateManyToMany(c, inst, reg, []map[string]any{item}); err != nil {
		internalError(c, "hydrate "+reg.Manifest.Name, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

// updateHandler actualiza un registro.
//
// Es un PATCH y no un PUT a propósito: se manda lo que cambia, no el registro
// entero. Un PUT que no incluye un campo lo borraría, y una lista parcial
// acabaría vaciando columnas que nadie tocó.
func updateHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// El estado tampoco entra por aquí. Su única vía es la transición, que
	// comprueba el estado de partida y deja la fila en el historial; aceptarlo
	// aquí dejaría estados que nadie recorrió y el historial incompleto.
	if wf := reg.Manifest.Workflow; wf != nil {
		if _, enviado := body[wf.Field]; enviado {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": wf.Field + " solo cambia por una transición",
			})
			return
		}
	}

	if len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no hay nada que actualizar"})
		return
	}

	// En una actualización ningún campo es obligatorio: se manda lo que se
	// cambia, no el registro entero.
	meta, err := inst.Manifest.Meta(reg.Manifest.Name)
	if err != nil {
		internalError(c, "meta "+reg.Manifest.Name, err)
		return
	}
	if err := validateInput(meta, body, false); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	stored, m2m, err := relationalFields(reg, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := checkRelatedExist(c, inst, reg, stored); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var item map[string]any
	if len(stored) > 0 {
		qb := module.NewQueryBuilder(*reg)
		query, args, err := qb.BuildUpdate(c.Param("id"), tenantID, c.GetString("user_id"), stored)
		if err != nil {
			internalError(c, "update "+reg.Manifest.Name, err)
			return
		}

		cols := returningColumns(reg)
		item, err = rowToMap(db.Executor(c).QueryRowContext(ctx, query, args...), cols)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if err != nil {
			internalError(c, "update "+reg.Manifest.Name, err)
			return
		}
	}

	if err := applyManyToMany(c, inst, reg, c.Param("id"), m2m); err != nil {
		internalError(c, "update m2m "+reg.Manifest.Name, err)
		return
	}

	if item == nil {
		// Solo cambiaron relaciones: se devuelve el registro actualizado.
		qb := module.NewQueryBuilder(*reg).
			Where("id", "=", c.Param("id")).
			Where("tenant_id", "=", tenantID)
		cols, query, args, err := qb.BuildSelect()
		if err != nil {
			internalError(c, "update "+reg.Manifest.Name, err)
			return
		}
		item, err = rowToMap(db.Executor(c).QueryRowContext(ctx, query, args...), cols)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if err != nil {
			internalError(c, "update "+reg.Manifest.Name, err)
			return
		}
	}
	if err := hydrateManyToMany(c, inst, reg, []map[string]any{item}); err != nil {
		internalError(c, "hydrate "+reg.Manifest.Name, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// deleteHandler borra un registro.
func deleteHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

	// Borra primero los enlaces m2m: la tabla asociativa no tiene FK con ON
	// DELETE CASCADE y quedarían huérfanos.
	for _, f := range reg.Manifest.Fields {
		if module.IsManyToMany(f.Type) {
			join := module.M2MTableName(inst.Manifest.Name, reg.Manifest.Name, f.Name)
			if _, err := db.Executor(c).ExecContext(ctx,
				fmt.Sprintf("DELETE FROM %s WHERE left_id = $1 AND tenant_id = $2", quoteIdent(join)),
				c.Param("id"), tenantID); err != nil {
				internalError(c, "delete m2m "+reg.Manifest.Name, err)
				return
			}
		}
	}

	qb := module.NewQueryBuilder(*reg)
	query, _, err := qb.BuildDelete()
	if err != nil {
		internalError(c, "delete "+reg.Manifest.Name, err)
		return
	}

	result, err := db.Executor(c).ExecContext(ctx, query, c.Param("id"), tenantID)
	if err != nil {
		internalError(c, "delete "+reg.Manifest.Name, err)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// exportHandler exporta los datos de un modelo en formato CSV o XLSX.
func exportHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	modelName := reg.Manifest.Name

	fmtStr := c.DefaultQuery("format", "csv")
	format := exportimport.ParseFormat(fmtStr)

	var selectedFields []string
	if fieldsStr := c.Query("fields"); fieldsStr != "" {
		selectedFields = strings.Split(fieldsStr, ",")
	}

	qb := module.NewQueryBuilder(*reg)
	qb.Where("tenant_id", "=", tenantID)

	if search := strings.TrimSpace(c.Query("search")); search != "" {
		applySearch(qb, inst.Manifest, *reg, search)
	}

	filters, err := parseFilters(c, reg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	for _, f := range filters {
		qb.Where(f.field, f.op, f.value)
	}

	// Exportar solo lo que el usuario marcó en la tabla. Los ids los manda el
	// cliente, así que se validan uno a uno: un id que no sea un UUID no se
	// busca, se rechaza.
	if idsStr := strings.TrimSpace(c.Query("ids")); idsStr != "" {
		ids, err := parseIDList(idsStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		qb.WhereRaw("id IN ("+marks+")", ids...)
	}

	orderBy, orderDir := c.DefaultQuery("order", "created_at"), c.DefaultQuery("dir", "desc")
	if orderBy != "created_at" && orderBy != "updated_at" && orderBy != "id" && !reg.HasField(orderBy) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown order field: " + orderBy})
		return
	}
	qb.OrderBy(orderBy, orderDir)

	// limit=0 es la petición de "plantilla de ejemplo": sólo la fila de
	// cabeceras. Sin esto la plantilla salía con los registros del tenant ya
	// rellenos, que es como se duplica todo al reimportarla.
	headersOnly := false
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(maxExportRows)))
	if limit == 0 {
		headersOnly = true
		limit = 1
	} else if limit < 0 || limit > maxExportRows {
		limit = maxExportRows
	}
	qb.Limit(limit)

	cols, query, args, err := qb.BuildSelect()
	if err != nil {
		internalError(c, "export "+modelName, err)
		return
	}

	rows, err := db.Executor(c).QueryContext(ctx, query, args...)
	if err != nil {
		internalError(c, "export "+modelName, err)
		return
	}
	defer rows.Close()

	records, err := rowsToMaps(rows, cols)
	if err != nil {
		internalError(c, "export "+modelName, err)
		return
	}

	opts := exportimport.ExportOptions{
		Format:         format,
		SelectedFields: selectedFields,
		UseLabels:      true,
		HeadersOnly:    headersOnly,
	}

	fileBytes, contentType, filename, err := exportimport.ExportData(*reg, records, opts)
	if err != nil {
		internalError(c, "export "+modelName, err)
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.Data(http.StatusOK, contentType, fileBytes)
}

// importHandler importa registros de un archivo CSV o XLSX enviado por el cliente.
func importHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	dryRun := c.Query("dry_run") == "true"

	var fileBytes []byte
	var format exportimport.ExportFormat

	fileHeader, err := c.FormFile("file")
	if err == nil && fileHeader != nil {
		f, err := fileHeader.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no se pudo abrir el archivo subido: " + err.Error()})
			return
		}
		defer f.Close()

		fileBytes, err = io.ReadAll(f)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "error leyendo el archivo: " + err.Error()})
			return
		}

		if strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".xlsx") {
			format = exportimport.FormatXLSX
		} else {
			format = exportimport.FormatCSV
		}
	} else {
		fileBytes, err = c.GetRawData()
		if err != nil || len(fileBytes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "se requiere un archivo adjunto 'file' o datos CSV/XLSX en el cuerpo"})
			return
		}
		fmtStr := c.DefaultQuery("format", "csv")
		format = exportimport.ParseFormat(fmtStr)
	}

	opts := exportimport.ImportOptions{
		Format: format,
		DryRun: dryRun,
	}

	res, err := exportimport.ImportData(ctx, db.Executor(c), *reg, tenantID, fileBytes, opts)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !res.Success {
		c.JSON(http.StatusUnprocessableEntity, res)
		return
	}

	c.JSON(http.StatusOK, res)
}

// returningColumns son las columnas que el INSERT y el UPDATE devuelven.
func returningColumns(reg *module.ModelRegistration) []string {
	cols := []string{"id"}
	for _, f := range reg.Manifest.OrderedFields() {
		if !module.IsStored(f.Type) {
			continue
		}
		cols = append(cols, f.Name)
	}
	return append(cols, "created_at", "updated_at", "created_by", "updated_by")
}

// relationalFields separa del cuerpo los campos que no son columnas: los
// many2many se aplican a su tabla asociativa y no entran en el INSERT/UPDATE,
// y los one2many no se aceptan como escritura directa.
func relationalFields(reg *module.ModelRegistration, body map[string]any) (stored, m2m map[string]any, err error) {
	stored = make(map[string]any, len(body))
	m2m = make(map[string]any)
	for _, f := range reg.Manifest.Fields {
		v, ok := body[f.Name]
		if !ok {
			continue
		}
		switch {
		case module.IsManyToMany(f.Type):
			m2m[f.Name] = v
		case module.IsRelational(f.Type) && !module.IsStored(f.Type):
			return nil, nil, fmt.Errorf("%s es one2many: se deriva del otro lado y no se escribe", f.Name)
		default:
			stored[f.Name] = v
		}
	}
	return stored, m2m, nil
}

// checkRelatedExist valida que los UUIDs de los many2one apunten a filas que
// existen en el modelo relacionado (mismo tenant, por RLS). Sin esto, un
// product_id inventado quedaba guardado y el join solo devolvía de NULL.
func checkRelatedExist(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration, body map[string]any) error {
	for _, f := range reg.Manifest.Fields {
		if !strings.EqualFold(f.Type, "many2one") && !strings.EqualFold(f.Type, "m2o") {
			continue
		}
		v, ok := body[f.Name]
		if !ok || v == nil {
			continue
		}
		id, _ := v.(string)
		relatedModule := f.RelatedModule
		if relatedModule == "" {
			relatedModule = inst.Manifest.Name
		}
		table := fmt.Sprintf("mod_%s_%s", relatedModule, f.RelatedModel)
		var one int
		err := db.Executor(c).QueryRowContext(c.Request.Context(),
			fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1", quoteIdent(table)), id,
		).Scan(&one)
		if err != nil {
			return fmt.Errorf("%s: la referencia no existe en %s", f.Name, f.RelatedModel)
		}
	}
	return nil
}

// applyManyToMany persiste los enlaces m2m de un registro: borra los suyos y
// inserta los recibidos. El cuerpo llega validado por validateInput.
func applyManyToMany(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration, id string, links map[string]any) error {
	ctx := c.Request.Context()
	for _, f := range reg.Manifest.Fields {
		if !module.IsManyToMany(f.Type) {
			continue
		}
		raw, ok := links[f.Name]
		if !ok {
			continue
		}
		arr, _ := raw.([]any)
		join := module.M2MTableName(inst.Manifest.Name, reg.Manifest.Name, f.Name)
		if _, err := db.Executor(c).ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE left_id = $1 AND tenant_id = $2", quoteIdent(join)),
			id, c.GetString("tenant_id")); err != nil {
			return fmt.Errorf("many2many %s: %w", f.Name, err)
		}
		for _, item := range arr {
			s, _ := item.(string)
			if _, err := db.Executor(c).ExecContext(ctx,
				fmt.Sprintf("INSERT INTO %s (tenant_id, left_id, right_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", quoteIdent(join)),
				c.GetString("tenant_id"), id, s); err != nil {
				return fmt.Errorf("many2many %s: %w", f.Name, err)
			}
		}
	}
	return nil
}

// hydrateManyToMany rellena, en cada item, los campos m2m con la lista de ids
// enlazados. Sin esto, la API devolvería la relación como columna inexistente.
func hydrateManyToMany(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration, items []map[string]any) error {
	if len(items) == 0 {
		return nil
	}
	ctx := c.Request.Context()
	for _, f := range reg.Manifest.Fields {
		if !module.IsManyToMany(f.Type) {
			continue
		}
		join := module.M2MTableName(inst.Manifest.Name, reg.Manifest.Name, f.Name)
		ids := make([]any, 0, len(items))
		params := make([]string, 0, len(items))
		for i, it := range items {
			ids = append(ids, it["id"])
			params = append(params, fmt.Sprintf("$%d", i+2))
		}
		rows, err := db.Executor(c).QueryContext(ctx,
			fmt.Sprintf("SELECT left_id::text, right_id::text FROM %s WHERE tenant_id = $1 AND left_id IN (%s)", quoteIdent(join), strings.Join(params, ",")),
			append([]any{c.GetString("tenant_id")}, ids...)...)
		if err != nil {
			return err
		}
		links := make(map[string][]string)
		for rows.Next() {
			var left, right string
			if err := rows.Scan(&left, &right); err != nil {
				rows.Close()
				return err
			}
			links[left] = append(links[left], right)
		}
		rows.Close()
		for _, it := range items {
			id, _ := it["id"].(string)
			if lst, ok := links[id]; ok {
				it[f.Name] = lst
			} else {
				it[f.Name] = []string{}
			}
		}
	}
	return nil
}

// quoteIdent entrecomilla un identificador para Postgres.
//
// Lo usan el historial y las transiciones, donde el nombre de la tabla y el del
// campo salen del manifest. Se entrecomilla, pero no se valida: el manifest ya
// pasó la whitelist al cargarse, y volver a validarlo aquí duplicaría la regla
// en dos sitios que se pueden desincronizar.
func quoteIdent(name string) string { return `"` + name + `"` }
