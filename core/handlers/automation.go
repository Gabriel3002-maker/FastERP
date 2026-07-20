package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/fasterp/backend/sdk"
)

// AutomationHandler es el motor de "Automatizaciones": pipelines de pasos
// tipados (crear registro, actualizar el disparador, buscar registro,
// condición) que se arman con un editor visual (Drawflow, en
// modules/automatizaciones/frontend), con variables entre nodos, y se
// disparan a mano ("Probar"), por webhook o de forma programada.
//
// A propósito NO es una máquina de estados — para eso está Studio-Flujo
// (sdk.WorkflowDef). Acá cada nodo hace algo de verdad (crea/actualiza/
// busca un registro con sdk.ModuleSDK) y una Condición bifurca el camino.
//
// Recortes de alcance deliberados (v1.1): schedule corre siempre contra UN
// registro fijo elegido al guardar, nunca por lote; el programado es un
// intervalo en minutos, no una expresión cron real; las variables son sólo
// {{origen.campo}}, sin funciones ni aritmética. Guardar/listar
// automatizaciones e historial no tienen código acá: salen gratis del
// dispatcher @fast, porque el manifest declara los modelos "automation" y
// "automation_run" normales.
type AutomationHandler struct {
	sessionManager *SessionManager
	crud           *GenericCRUDHandler // resuelve el ModuleSDK de cada módulo destino
}

func NewAutomationHandler(sessionManager *SessionManager, crud *GenericCRUDHandler) *AutomationHandler {
	return &AutomationHandler{sessionManager: sessionManager, crud: crud}
}

// automationGraph es el JSON que arma el editor visual — mismo shape tanto
// si viaja inline en el pedido de "Probar" como si viene de la columna
// "definition" de un registro "automation" ya guardado.
type automationGraph struct {
	Start string                     `json:"start"`
	Nodes map[string]*automationNode `json:"nodes"`
}

type automationNode struct {
	Type string `json:"type"` // "update_trigger" | "create" | "search" | "condition"

	// update_trigger, create, search
	Module string         `json:"module,omitempty"` // create/search: módulo destino
	Model  string         `json:"model,omitempty"`  // create/search: modelo destino
	Fields map[string]any `json:"fields,omitempty"` // update_trigger, create: campo → valor (literal o {{...}})
	Next   string         `json:"next,omitempty"`   // "" = termina el pipeline acá

	// condition, search (search sólo usa Field/Value como filtro "igual a")
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"` // condition: eq | neq | gt | lt | contains
	Value    any    `json:"value,omitempty"`    // literal o {{...}}
	OnTrue   string `json:"on_true,omitempty"`
	OnFalse  string `json:"on_false,omitempty"`
}

type runStep struct {
	Node   string `json:"node"`
	Type   string `json:"type"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type triggerRef struct {
	Module string `json:"module"`
	Model  string `json:"model"`
	ID     string `json:"id"`
}

type runRequest struct {
	AutomationID string          `json:"automation_id,omitempty"`
	Definition   json.RawMessage `json:"definition,omitempty"`
	Trigger      triggerRef      `json:"trigger"`
}

// maxSteps corta un pipeline que entró en ciclo — v1 no prohíbe ciclos al
// armar el grafo, así que la protección es en tiempo de ejecución, con un
// error explícito en vez de colgarse.
const maxSteps = 100

// Run ejecuta un pipeline contra un registro disparador elegido a mano
// ("Probar"). Requiere sesión válida (no hace falta ser admin: correr una
// automatización no es más sensible que editar el registro a mano).
func (h *AutomationHandler) Run(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	claims, err := h.requireSession(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if req.Trigger.Module == "" || req.Trigger.Model == "" || req.Trigger.ID == "" {
		writeErr(w, http.StatusBadRequest, "falta el registro disparador (trigger.module/model/id)")
		return
	}

	ctx := r.Context()
	graph, err := h.resolveGraph(ctx, claims.TenantID, claims.UserID, req.AutomationID, req.Definition)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	finalTrigger, steps, runErr := h.runAndRecord(
		ctx, claims.TenantID, claims.UserID, req.AutomationID, graph, req.Trigger, "manual")
	h.writeRunResult(w, finalTrigger, steps, runErr)
}

// webhookRequest es lo que manda un sistema externo: a qué registro apunta
// esta corrida. La automatización en sí (qué pasos tiene) ya está guardada
// — el webhook no la redefine, sólo la dispara.
type webhookRequest struct {
	Trigger triggerRef `json:"trigger"`
}

// Webhook dispara una automatización guardada desde afuera de la app, sin
// sesión: se autentica con el token propio de la automatización
// (webhook_token, generado al guardarla con trigger_type=webhook). Como
// quien llama no trae X-Tenant-ID ni sesión, el tenant se busca junto con
// el token — ver findAutomationByToken.
func (h *AutomationHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	automationID := r.PathValue("id")
	token := r.URL.Query().Get("token")
	if automationID == "" || token == "" {
		writeErr(w, http.StatusUnauthorized, "falta el token")
		return
	}

	ctx := r.Context()
	tenantID, definition, err := h.findAutomationByToken(ctx, automationID, token)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "token inválido")
		return
	}

	var req webhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if req.Trigger.Module == "" || req.Trigger.Model == "" || req.Trigger.ID == "" {
		writeErr(w, http.StatusBadRequest, "falta el registro disparador (trigger.module/model/id)")
		return
	}

	var graph automationGraph
	if err := json.Unmarshal([]byte(definition), &graph); err != nil {
		writeErr(w, http.StatusInternalServerError, "la definición guardada no es JSON válido")
		return
	}
	if graph.Start == "" || graph.Nodes[graph.Start] == nil {
		writeErr(w, http.StatusInternalServerError, "la definición guardada no tiene nodo inicial")
		return
	}

	// userID vacío: quien dispara es un sistema externo, no una persona con
	// sesión — igual que el scheduler (ver automation_scheduler.go).
	finalTrigger, steps, runErr := h.runAndRecord(ctx, tenantID, "", automationID, graph, req.Trigger, "webhook")
	h.writeRunResult(w, finalTrigger, steps, runErr)
}

// writeRunResult es la respuesta compartida entre Run y Webhook: 400 si no
// se pudo ni arrancar (ej. no se pudo leer el disparador), o 200 con los
// pasos ejecutados — incluso si el pipeline terminó en error a mitad de
// camino, porque esos pasos ya tuvieron efectos reales en la base y la
// persona necesita verlos, no sólo un "falló".
func (h *AutomationHandler) writeRunResult(w http.ResponseWriter, trigger map[string]any, steps []runStep, runErr error) {
	if steps == nil && runErr != nil {
		writeErr(w, http.StatusBadRequest, runErr.Error())
		return
	}
	result := map[string]any{"trigger": trigger, "steps": steps}
	if runErr != nil {
		result["error"] = runErr.Error()
	}
	writeJSON(w, http.StatusOK, result)
}

// resolveGraph arma el automationGraph a partir de un automation_id
// guardado o de una definición inline (para poder "Probar" antes de
// guardar).
// La definición inline SIEMPRE gana sobre la guardada cuando viene la
// automatización: "Probar" manda las dos a la vez (el id para que el
// historial quede asociado, la definición inline para probar el lienzo TAL
// COMO ESTÁ, con ediciones todavía sin guardar) — si automation_id pisara la
// definición inline, "Probar" mostraría resultados de una versión vieja.
func (h *AutomationHandler) resolveGraph(ctx context.Context, tenantID, userID, automationID string, inline json.RawMessage) (automationGraph, error) {
	definitionJSON := []byte(inline)
	if len(definitionJSON) == 0 && automationID != "" {
		automationSDK, err := h.crud.sdkFor(ctx, "automatizaciones", tenantID, userID)
		if err != nil {
			return automationGraph{}, err
		}
		rec, err := automationSDK.Get(ctx, "automation", automationID)
		if err != nil {
			return automationGraph{}, fmt.Errorf("automatización no encontrada")
		}
		def, _ := rec["definition"].(string)
		definitionJSON = []byte(def)
	}
	if len(definitionJSON) == 0 {
		return automationGraph{}, fmt.Errorf("falta \"definition\" o \"automation_id\"")
	}

	var graph automationGraph
	if err := json.Unmarshal(definitionJSON, &graph); err != nil {
		return automationGraph{}, fmt.Errorf("la definición no es JSON válido: %w", err)
	}
	if graph.Start == "" || graph.Nodes[graph.Start] == nil {
		return automationGraph{}, fmt.Errorf("falta el nodo inicial (\"start\")")
	}
	return graph, nil
}

// runAndRecord lee el registro disparador, ejecuta el grafo, y siempre deja
// rastro en el historial (automation_run) — sin importar si vino de
// Probar, un webhook o el programador. Es el único lugar que llama
// execute(), así los tres orígenes quedan auditados igual.
func (h *AutomationHandler) runAndRecord(
	ctx context.Context,
	tenantID, userID, automationID string,
	graph automationGraph,
	trigger triggerRef,
	source string, // "manual" | "webhook" | "schedule"
) (map[string]any, []runStep, error) {
	triggerSDK, err := h.crud.sdkFor(ctx, trigger.Module, tenantID, userID)
	if err != nil {
		return nil, nil, err
	}
	triggerRecord, err := triggerSDK.Get(ctx, trigger.Model, trigger.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("no se pudo leer el registro disparador: %w", err)
	}

	claims := &Claims{TenantID: tenantID, UserID: userID}
	finalTrigger, steps, runErr := h.execute(ctx, claims, graph, triggerSDK, trigger.Model, trigger.ID, triggerRecord)

	h.recordRun(ctx, tenantID, userID, automationID, trigger, source, steps, runErr)

	return finalTrigger, steps, runErr
}

// recordRun guarda cada corrida en el modelo automation_run — nunca hace
// fallar la corrida en sí si esto falla, sólo lo loguea: el pipeline ya
// tuvo sus efectos reales, perder el registro del historial no debería
// esconder eso.
func (h *AutomationHandler) recordRun(
	ctx context.Context,
	tenantID, userID, automationID string,
	trigger triggerRef,
	source string,
	steps []runStep,
	runErr error,
) {
	if automationID == "" {
		return // "Probar" sin guardar: no hay a qué automatización asociarle el historial
	}
	automationSDK, err := h.crud.sdkFor(ctx, "automatizaciones", tenantID, userID)
	if err != nil {
		log.Printf("[Automatizaciones] no se pudo guardar el historial de %s: %v", automationID, err)
		return
	}
	stepsJSON, _ := json.Marshal(steps)
	fields := map[string]any{
		"automation_id":  automationID,
		"trigger_module": trigger.Module,
		"trigger_model":  trigger.Model,
		"trigger_id":     trigger.ID,
		"source":         source,
		"ok":             runErr == nil,
		"steps":          string(stepsJSON),
	}
	if runErr != nil {
		fields["error"] = runErr.Error()
	}
	if _, err := automationSDK.Create(ctx, "automation_run", fields); err != nil {
		log.Printf("[Automatizaciones] no se pudo guardar el historial de %s: %v", automationID, err)
	}
}

// findAutomationByToken busca la automatización por id+token CRUZANDO
// TODOS los tenants — un webhook no trae X-Tenant-ID ni sesión, así que no
// hay otra forma de saber a qué tenant pertenece antes de encontrarla.
// Consulta la tabla directo (mismo patrón que ya usa ModuleHandler para
// installed_modules en modules.go) en vez de sdkFor, que exige conocer el
// tenant de antemano.
func (h *AutomationHandler) findAutomationByToken(ctx context.Context, automationID, token string) (tenantID, definition string, err error) {
	row := h.crud.dbConn.QueryRow(ctx, `
		SELECT tenant_id, definition FROM mod_automatizaciones_automation
		WHERE id = $1 AND webhook_token = $2 AND trigger_type = 'webhook' AND active = true
	`, automationID, token)

	if err := row.Scan(&tenantID, &definition); err != nil {
		return "", "", fmt.Errorf("no encontrada")
	}
	return tenantID, definition, nil
}

// execute camina el grafo desde graph.Start, ejecutando cada nodo de verdad
// contra el ModuleSDK que corresponda. triggerRecord se refresca después de
// cada update_trigger para que una Condición más adelante en el pipeline
// vea el valor nuevo. stepOutputs guarda lo que produjo cada nodo Crear o
// Buscar (el registro completo), para que {{nodoID.campo}} tenga de dónde
// leer en pasos siguientes.
func (h *AutomationHandler) execute(
	ctx context.Context,
	claims *Claims,
	graph automationGraph,
	triggerSDK *sdk.ModuleSDK,
	triggerModel, triggerID string,
	triggerRecord map[string]any,
) (map[string]any, []runStep, error) {
	var steps []runStep
	nodeID := graph.Start
	stepOutputs := map[string]map[string]any{}

	templateCtx := func() map[string]map[string]any {
		c := map[string]map[string]any{"trigger": triggerRecord}
		for id, rec := range stepOutputs {
			c[id] = rec
		}
		return c
	}
	fail := func(nodeID, nodeType string, err error) (map[string]any, []runStep, error) {
		steps = append(steps, runStep{Node: nodeID, Type: nodeType, OK: false, Detail: err.Error()})
		return triggerRecord, steps, err
	}

	for i := 0; i < maxSteps; i++ {
		node := graph.Nodes[nodeID]
		if node == nil {
			return fail(nodeID, "", fmt.Errorf("el nodo %q no existe en la definición", nodeID))
		}

		switch node.Type {
		case "update_trigger":
			fields, err := resolveTemplateFields(node.Fields, templateCtx())
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			if err := triggerSDK.Update(ctx, triggerModel, triggerID, fields); err != nil {
				return fail(nodeID, node.Type, err)
			}
			triggerRecord, err = triggerSDK.Get(ctx, triggerModel, triggerID)
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			steps = append(steps, runStep{Node: nodeID, Type: node.Type, OK: true, Detail: "actualizó el registro disparador"})
			nodeID = node.Next

		case "create":
			if node.Module == "" || node.Model == "" {
				return fail(nodeID, node.Type, fmt.Errorf("al nodo %q le falta el módulo/modelo destino", nodeID))
			}
			fields, err := resolveTemplateFields(node.Fields, templateCtx())
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			targetSDK, err := h.crud.sdkFor(ctx, node.Module, claims.TenantID, claims.UserID)
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			newID, err := targetSDK.Create(ctx, node.Model, fields)
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			newRecord, err := targetSDK.Get(ctx, node.Model, newID)
			if err != nil {
				// El registro se creó igual — no se aborta la corrida, sólo
				// no queda disponible como {{nodeID.campo}} para lo que sigue.
				newRecord = map[string]any{"id": newID}
			}
			stepOutputs[nodeID] = newRecord
			steps = append(steps, runStep{
				Node: nodeID, Type: node.Type, OK: true,
				Detail: fmt.Sprintf("creó %s/%s #%s", node.Module, node.Model, newID),
			})
			nodeID = node.Next

		case "search":
			if node.Module == "" || node.Model == "" {
				return fail(nodeID, node.Type, fmt.Errorf("al nodo %q le falta el módulo/modelo destino", nodeID))
			}
			value, err := resolveTemplateValue(node.Value, templateCtx())
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			targetSDK, err := h.crud.sdkFor(ctx, node.Module, claims.TenantID, claims.UserID)
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			page, err := targetSDK.List(ctx, node.Model, sdk.ListOptions{
				Limit:   1,
				Filters: []sdk.Filter{{Field: node.Field, Op: "eq", Value: fmt.Sprint(value)}},
			})
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			if len(page.Data) == 0 {
				steps = append(steps, runStep{Node: nodeID, Type: node.Type, OK: true, Detail: "no se encontró ningún registro"})
			} else {
				stepOutputs[nodeID] = page.Data[0]
				steps = append(steps, runStep{
					Node: nodeID, Type: node.Type, OK: true,
					Detail: fmt.Sprintf("encontró %s/%s #%v", node.Module, node.Model, page.Data[0]["id"]),
				})
			}
			nodeID = node.Next

		case "condition":
			value, err := resolveTemplateValue(node.Value, templateCtx())
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			result, err := evaluateCondition(triggerRecord[node.Field], node.Operator, value)
			if err != nil {
				return fail(nodeID, node.Type, err)
			}
			branch, label := node.OnFalse, "falso"
			if result {
				branch, label = node.OnTrue, "verdadero"
			}
			steps = append(steps, runStep{
				Node: nodeID, Type: node.Type, OK: true,
				Detail: fmt.Sprintf("%q %s %v → %s", node.Field, node.Operator, value, label),
			})
			nodeID = branch

		default:
			return fail(nodeID, node.Type, fmt.Errorf("el nodo %q tiene un tipo desconocido: %q", nodeID, node.Type))
		}

		if nodeID == "" {
			return triggerRecord, steps, nil // fin del pipeline por esta rama
		}
	}

	return triggerRecord, steps, fmt.Errorf("el pipeline no terminó en %d pasos — ¿hay un ciclo?", maxSteps)
}

// templateTokenRe matchea {{origen.campo}} — espacios opcionales adentro,
// nombres alfanuméricos (con _) para origen y campo.
var templateTokenRe = regexp.MustCompile(`\{\{\s*(\w+)\.(\w+)\s*\}\}`)

// resolveTemplate reemplaza cada {{origen.campo}} en raw por el valor
// correspondiente de ctx[origen][campo] (como texto). "origen" es
// "trigger" o el id de un nodo Crear/Buscar anterior en el pipeline. Un
// string sin tokens vuelve intacto — así las automatizaciones que sólo
// usan literales (v1) siguen funcionando exactamente igual.
//
// Si un token no se puede resolver, la corrida falla con el nombre exacto
// del token que faltó — nunca lo deja pasar en blanco, que generaría un
// dato incompleto en silencio.
func resolveTemplate(raw string, ctx map[string]map[string]any) (string, error) {
	var firstErr error
	result := templateTokenRe.ReplaceAllStringFunc(raw, func(match string) string {
		if firstErr != nil {
			return match
		}
		groups := templateTokenRe.FindStringSubmatch(match)
		source, field := groups[1], groups[2]

		record, ok := ctx[source]
		if !ok {
			firstErr = fmt.Errorf("%s: %q no es el disparador ni un nodo anterior conocido", match, source)
			return match
		}
		value, ok := record[field]
		if !ok {
			firstErr = fmt.Errorf("%s: %q no tiene un campo %q (o ese paso no encontró ningún registro)", match, source, field)
			return match
		}
		return fmt.Sprint(value)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}

// resolveTemplateValue aplica resolveTemplate si el valor es un string;
// cualquier otro tipo (una definición armada a mano podría traer un número
// o booleano ya literal) se deja igual, no hay nada que interpolar.
func resolveTemplateValue(v any, ctx map[string]map[string]any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return v, nil
	}
	return resolveTemplate(s, ctx)
}

func resolveTemplateFields(fields map[string]any, ctx map[string]map[string]any) (map[string]any, error) {
	resolved := make(map[string]any, len(fields))
	for k, v := range fields {
		rv, err := resolveTemplateValue(v, ctx)
		if err != nil {
			return nil, err
		}
		resolved[k] = rv
	}
	return resolved, nil
}

// evaluateCondition compara el valor de un campo contra un literal (o una
// variable ya resuelta). eq/neq comparan como texto — alcanza sin tener que
// adivinar el tipo real de la columna. contains también es texto. gt/lt
// intentan parsear los dos lados como número: si no se puede, error
// explícito en vez de un silencioso "false" que parecería una comparación
// real evaluada.
func evaluateCondition(fieldValue any, operator string, target any) (bool, error) {
	switch operator {
	case "eq":
		return fmt.Sprint(fieldValue) == fmt.Sprint(target), nil
	case "neq":
		return fmt.Sprint(fieldValue) != fmt.Sprint(target), nil
	case "contains":
		return strings.Contains(fmt.Sprint(fieldValue), fmt.Sprint(target)), nil
	case "gt", "lt":
		left, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(fieldValue)), 64)
		if err != nil {
			return false, fmt.Errorf("el campo no es un número (%q): no se puede usar %q", fmt.Sprint(fieldValue), operator)
		}
		right, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(target)), 64)
		if err != nil {
			return false, fmt.Errorf("el valor a comparar no es un número (%q): no se puede usar %q", fmt.Sprint(target), operator)
		}
		if operator == "gt" {
			return left > right, nil
		}
		return left < right, nil
	default:
		return false, fmt.Errorf("operador de condición desconocido: %q (usá eq, neq, gt, lt o contains)", operator)
	}
}

// requireSession exige una sesión válida — a diferencia de
// StudioHandler.requireAdmin, correr una automatización no exige admin: no
// es más sensible que editar el registro a mano.
func (h *AutomationHandler) requireSession(r *http.Request) (*Claims, error) {
	if h.sessionManager == nil {
		return nil, fmt.Errorf("sesión no configurada")
	}
	token := h.sessionManager.GetTokenFromRequest(r)
	if token == "" {
		return nil, fmt.Errorf("se requiere iniciar sesión")
	}
	claims, err := h.sessionManager.ValidateToken(token)
	if err != nil {
		return nil, fmt.Errorf("sesión inválida o vencida")
	}
	return claims, nil
}
