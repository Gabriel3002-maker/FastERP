package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
	"github.com/gin-gonic/gin"
)

// modelMetaHandler devuelve la metadata de un modelo: qué campos tiene, qué
// control usa cada uno y qué vistas se deducen.
//
// Es lo que hace que la interfaz no la escriba nadie. El cliente pide el _meta,
// monta la tabla y el formulario con lo que viene, y no hay un archivo de UI que
// se quede corto cuando el esquema crece.
func modelMetaHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	meta, err := inst.Manifest.Meta(reg.Manifest.Name)
	if err != nil {
		internalError(c, "meta", err)
		return
	}
	c.JSON(http.StatusOK, meta)
}

// moduleMetaHandler devuelve los modelos de un módulo. El cliente lo usa para el
// menú lateral y para saber qué hay instalado.
func moduleMetaHandler(c *gin.Context, inst *module.ModuleInstance, _ *module.ModelRegistration) {
	models := make([]gin.H, 0, len(inst.Models))
	for _, reg := range inst.Models {
		meta, err := inst.Manifest.Meta(reg.Manifest.Name)
		if err != nil {
			continue
		}
		models = append(models, gin.H{
			"model":  meta.Model,
			"label":  meta.Label,
			"table":  meta.Table,
			"fields": meta.Fields,
			"views":  meta.Views,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"module":  inst.Manifest.Name,
		"label":   inst.Manifest.Label,
		"icon":    inst.Manifest.Icon,
		"version": inst.Manifest.Version,
		"models":  models,
		"menus":   module.Global.Menus(),
	})
}

// transitionsHandler dice qué transiciones son legales desde el estado actual
// del registro.
//
// El cliente no lo deduce de la metadata que ya tiene: se lo pregunta. La razón
// es que la respuesta es por registro, y pedirla evita el caso en que el cliente
// calcula mal qué botón va y el servidor lo rechaza.
func transitionsHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	wf := reg.Manifest.Workflow
	if wf == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el modelo no declara workflow"})
		return
	}

	state, err := currentState(c, reg)
	if err != nil {
		handleRowError(c, err, reg)
		return
	}

	meta, _ := inst.Manifest.Meta(reg.Manifest.Name)
	available := meta.Workflow.AvailableTransitions(state)

	c.JSON(http.StatusOK, gin.H{
		"field":       wf.Field,
		"state":       state,
		"transitions": available,
	})
}

// transitionHandler aplica una transición.
//
// El campo de estado queda fuera de la validación del update normal, así que
// esta es la única vía por la que cambia, y por eso cada cambio queda registrado
// en el historial: el estado que se ve es el estado por el que pasó el
// registro, no un campo que alguien pueda escribir a mano.
func transitionHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	wf := reg.Manifest.Workflow
	if wf == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el modelo no declara workflow"})
		return
	}

	action := c.Param("action")
	tr := wf.Transitions[action]
	if tr == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transición desconocida: " + action})
		return
	}

	from, err := currentState(c, reg)
	if err != nil {
		handleRowError(c, err, reg)
		return
	}

	// La transición se comprueba contra el estado que tenía el registro al
	// empezar, no contra el que trae el cuerpo. Si dos personas pulsan a la vez,
	// la segunda ve el estado nuevo y su transición ya no es legal: sale un
	// 409, no un historial con dos cambios contradictorios.
	if !contains(tr.From, from) {
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("no se puede pasar de %q a %q", from, tr.To),
			"state": from,
		})
		return
	}

	ctx := c.Request.Context()
	id, tenantID := c.Param("id"), c.GetString("tenant_id")
	now := time.Now().UTC()

	// El UPDATE lleva el estado de partida en el WHERE. Si otra petición lo
	// cambió entre el SELECT y aquí, no actualiza ninguna fila y se responde
	// 409, en vez de pisar su cambio.
	res, err := db.Executor(c).ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET %s = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4 AND %s = $5 RETURNING id",
			quoteIdent(reg.TableName), quoteIdent(wf.Field), quoteIdent(wf.Field)),
		tr.To, now, id, tenantID, from)
	if err != nil {
		internalError(c, "transition "+reg.Manifest.Name, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error": "el registro cambió mientras se aplicaba la transición",
			"state": from,
		})
		return
	}

	// El historial se escribe después del cambio, no antes. La tabla se
	// asegura aquí y no al arrancar para que una transición contra un módulo
	// recién editado no falle por una tabla que el watcher aún no ha creado.
	if err := ensureHistoryTable(inst, reg); err != nil {
		internalError(c, "historial", err)
		return
	}
	if err := recordTransition(c, inst, reg, id, tenantID, action, from, tr.To, now); err != nil {
		internalError(c, "historial", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         id,
		"field":      wf.Field,
		"state":      tr.To,
		"action":     action,
		"label":      tr.Label,
		"changed_at": now,
	})
}

// currentState lee el campo de estado del registro, o nil si el registro no
// existe para este tenant.
func currentState(c *gin.Context, reg *module.ModelRegistration) (string, error) {
	wf := reg.Manifest.Workflow
	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")

	var state sql.NullString
	err := db.Executor(c).QueryRowContext(ctx,
		fmt.Sprintf("SELECT %s FROM %s WHERE id = $1 AND tenant_id = $2",
			quoteIdent(wf.Field), quoteIdent(reg.TableName)),
		c.Param("id"), tenantID).Scan(&state)
	if err != nil {
		return "", err
	}
	if !state.Valid || state.String == "" {
		// Un registro sin estado es un registro creado por una vía antigua o
		// escrito a mano. Se trata como el estado inicial declarado, que es lo
		// que el motor pone al crear.
		return reg.Manifest.Workflow.Initial, nil
	}
	return state.String, nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func handleRowError(c *gin.Context, err error, reg *module.ModelRegistration) {
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	internalError(c, "read "+reg.Manifest.Name, err)
}

// historyHandler devuelve el recorrido del registro por la máquina de estados.
func historyHandler(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration) {
	if reg.Manifest.Workflow == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el modelo no declara workflow"})
		return
	}

	ctx, tenantID := c.Request.Context(), c.GetString("tenant_id")
	table := module.HistoryTable(inst.Manifest.Name, reg.Manifest.Name)

	rows, err := db.Executor(c).QueryContext(ctx,
		fmt.Sprintf(`SELECT action, from_state, to_state, changed_at
		             FROM %s
		             WHERE module_name = $1 AND model_name = $2 AND record_id = $3 AND tenant_id = $4
		             ORDER BY changed_at DESC, id DESC
		             LIMIT 200`, quoteIdent(table)),
		inst.Manifest.Name, reg.Manifest.Name, c.Param("id"), tenantID)
	if err != nil {
		internalError(c, "history", err)
		return
	}
	defer rows.Close()

	items := make([]gin.H, 0, 16)
	for rows.Next() {
		var (
			action, from, to string
			at               time.Time
		)
		if err := rows.Scan(&action, &from, &to, &at); err != nil {
			internalError(c, "history", err)
			return
		}
		// La etiqueta sale de la transición declarada, para que el historial
		// lea "Pedido" y no "ordered" junto al resto de la interfaz.
		label := action
		if tr := reg.Manifest.Workflow.Transitions[action]; tr != nil && tr.Label != "" {
			label = tr.Label
		}
		items = append(items, gin.H{
			"action": action,
			"label":  label,
			"from":   from,
			"to":     to,
			"at":     at,
		})
	}
	if err := rows.Err(); err != nil {
		internalError(c, "history", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// recordTransition deja una fila en el historial.
func recordTransition(c *gin.Context, inst *module.ModuleInstance, reg *module.ModelRegistration,
	id, tenantID, action, from, to string, at time.Time) error {

	table := module.HistoryTable(inst.Manifest.Name, reg.Manifest.Name)
	_, err := db.Executor(c).ExecContext(c.Request.Context(),
		fmt.Sprintf(`INSERT INTO %s (tenant_id, module_name, model_name, record_id, action, from_state, to_state, changed_at)
		             VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, quoteIdent(table)),
		tenantID, inst.Manifest.Name, reg.Manifest.Name, id, action, from, to, at)
	return err
}

// ensureHistoryTable crea la tabla de historial de un modelo si no existe.
//
// Va por petición y con IF NOT EXISTS porque la ruta de transición puede
// ejecutarse antes de que el watcher haya reconciliado el módulo: una
// transición contra un módulo recién editado no puede fallar porque su tabla de
// historial todavía no exista. El coste es una entrada de catálogo por
// transición, que es barato al lado del UPDATE.
func ensureHistoryTable(inst *module.ModuleInstance, reg *module.ModelRegistration) error {
	for _, s := range module.HistoryDDL(inst.Manifest.Name, reg.Manifest.Name) {
		if _, err := db.DB.Exec(s.SQL); err != nil {
			// Dos transiciones a la vez pueden intentar crear la tabla. La
			// segunda ve "already exists", que no es un fallo. Cualquier otro
			// error sí se propaga.
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			return fmt.Errorf("%s: %w", s.What, err)
		}
	}
	return nil
}

// jsonColumns convierte un []byte de JSONB en algo que gin pueda serializar.
func jsonColumns(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}
