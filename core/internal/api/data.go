package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
	"github.com/gin-gonic/gin"
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
	rg.GET("/:module/:model/_meta", h.dispatchModel(modelMetaHandler))

	rg.GET("/:module/:model", h.dispatchModel(listHandler))
	rg.POST("/:module/:model", h.dispatchModel(createHandler))
	rg.GET("/:module/:model/:id", h.dispatchModel(getHandler))
	rg.PUT("/:module/:model/:id", h.dispatchModel(updateHandler))
	rg.PATCH("/:module/:model/:id", h.dispatchModel(updateHandler))
	rg.DELETE("/:module/:model/:id", h.dispatchModel(deleteHandler))

	// Workflow. El historial va antes que la transición porque las dos cuelgan
	// de /:id y una ruta con más segmentos no compite con la otra; el orden solo
	// afecta a la legibilidad.
	rg.GET("/:module/:model/:id/transitions", h.dispatchModel(transitionsHandler))
	rg.POST("/:module/:model/:id/transitions/:action", h.dispatchModel(transitionHandler))
	rg.GET("/:module/:model/:id/history", h.dispatchModel(historyHandler))
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
func (h *Handler) dispatchModel(action moduleAction) gin.HandlerFunc {
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
		action(c, inst, reg)
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
	// El ESCAPE va una vez, al final del grupo: el carácter de escape es el del
	// patrón, y todos los operandos de la comparación comparten el mismo.
	var group []string
	for _, f := range meta.Fields {
		if f.Searchable {
			group = append(group, quoteIdent(f.Name)+" ILIKE ?")
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
	qb.WhereRaw("("+strings.Join(group, " OR ")+") ESCAPE '\\'", args...)
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

	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "25"))
	if limit <= 0 {
		limit = 25
	}
	// Se ajusta al tamaño permitido más cercano en vez de rechazar: un
	// limit=30 no es un error, es un 25 con un redondeo.
	limit = nearestPageSize(limit)
	return page, limit
}

func nearestPageSize(want int) int {
	best := module.PageSizes[0]
	for _, size := range module.PageSizes {
		if size > want {
			break
		}
		best = size
	}
	return best
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
func getHandler(c *gin.Context, _ *module.ModuleInstance, reg *module.ModelRegistration) {
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

	qb := module.NewQueryBuilder(*reg)
	query, args, err := qb.BuildInsert(tenantID, body)
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

	qb := module.NewQueryBuilder(*reg)
	query, args, err := qb.BuildUpdate(c.Param("id"), tenantID, body)
	if err != nil {
		internalError(c, "update "+reg.Manifest.Name, err)
		return
	}

	cols := returningColumns(reg)
	item, err := rowToMap(db.Executor(c).QueryRowContext(ctx, query, args...), cols)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		internalError(c, "update "+reg.Manifest.Name, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// deleteHandler borra un registro.
func deleteHandler(c *gin.Context, _ *module.ModuleInstance, reg *module.ModelRegistration) {
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

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

// returningColumns son las columnas que el INSERT y el UPDATE devuelven.
func returningColumns(reg *module.ModelRegistration) []string {
	cols := []string{"id"}
	for _, f := range reg.Manifest.OrderedFields() {
		cols = append(cols, f.Name)
	}
	return append(cols, "created_at", "updated_at")
}

// quoteIdent entrecomilla un identificador para Postgres.
//
// Lo usan el historial y las transiciones, donde el nombre de la tabla y el del
// campo salen del manifest. Se entrecomilla, pero no se valida: el manifest ya
// pasó la whitelist al cargarse, y volver a validarlo aquí duplicaría la regla
// en dos sitios que se pueden desincronizar.
func quoteIdent(name string) string { return `"` + name + `"` }
