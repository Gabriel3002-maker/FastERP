package handlers

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/fasterp/backend/db"
)

// schedulerInterval es cada cuánto se revisan automatizaciones programadas.
// No hace falta más resolución que esto: schedule_minutes es en minutos, no
// segundos.
const schedulerInterval = 1 * time.Minute

// RunScheduler es el disparador "schedule": cada minuto revisa TODOS los
// tenants buscando automatizaciones con trigger_type=schedule que ya
// cumplieron su intervalo, y las corre — siempre contra el mismo registro
// fijo que se guardó en cron_trigger_* al armarlas, nunca por lote.
// Automatizar "por cada registro que cumpla tal condición" es un modelo de
// ejecución distinto (con fallas parciales por registro) que este
// scheduler no resuelve — ver el plan de v1.1.
//
// Corre como goroutine desde main.go y no bloquea el arranque del servidor;
// termina cuando se cancela el context (apagado del servidor).
func RunScheduler(ctx context.Context, dbConn *db.DB, automationHandler *AutomationHandler) {
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runDueAutomations(ctx, dbConn, automationHandler)
		}
	}
}

type dueAutomation struct {
	ID                string
	TenantID          string
	Definition        string
	CronTriggerModule string
	CronTriggerModel  string
	CronTriggerID     string
}

// runDueAutomations consulta la tabla directo (cruzando tenants a
// propósito) en vez de usar sdkFor, que exige conocer el tenant de
// antemano — mismo motivo que Webhook.findAutomationByToken.
func runDueAutomations(ctx context.Context, dbConn *db.DB, h *AutomationHandler) {
	rows, err := dbConn.Query(ctx, `
		SELECT id, tenant_id, definition, cron_trigger_module, cron_trigger_model, cron_trigger_id
		FROM mod_automatizaciones_automation
		WHERE active = true
		  AND trigger_type = 'schedule'
		  AND schedule_minutes IS NOT NULL
		  AND COALESCE(cron_trigger_module, '') != ''
		  AND COALESCE(cron_trigger_model, '') != ''
		  AND COALESCE(cron_trigger_id, '') != ''
		  AND (last_run_at IS NULL OR last_run_at + (schedule_minutes || ' minutes')::interval <= now())
	`)
	if err != nil {
		log.Printf("[Automatizaciones] scheduler: error consultando automatizaciones programadas: %v", err)
		return
	}

	var due []dueAutomation
	for rows.Next() {
		var d dueAutomation
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Definition, &d.CronTriggerModule, &d.CronTriggerModel, &d.CronTriggerID); err != nil {
			log.Printf("[Automatizaciones] scheduler: fila inválida: %v", err)
			continue
		}
		due = append(due, d)
	}
	rows.Close()

	for _, d := range due {
		runOne(ctx, dbConn, h, d)
	}
}

func runOne(ctx context.Context, dbConn *db.DB, h *AutomationHandler, d dueAutomation) {
	var graph automationGraph
	if err := json.Unmarshal([]byte(d.Definition), &graph); err != nil {
		log.Printf("[Automatizaciones] scheduler: %s tiene una definición inválida: %v", d.ID, err)
		return
	}
	if graph.Start == "" || graph.Nodes[graph.Start] == nil {
		log.Printf("[Automatizaciones] scheduler: %s no tiene nodo inicial", d.ID)
		return
	}

	trigger := triggerRef{Module: d.CronTriggerModule, Model: d.CronTriggerModel, ID: d.CronTriggerID}
	// userID vacío: nadie con sesión disparó esto, corrió solo.
	_, _, runErr := h.runAndRecord(ctx, d.TenantID, "", d.ID, graph, trigger, "schedule")
	if runErr != nil {
		log.Printf("[Automatizaciones] scheduler: %s terminó con error: %v", d.ID, runErr)
	}

	if _, err := dbConn.Exec(ctx, `UPDATE mod_automatizaciones_automation SET last_run_at = now() WHERE id = $1`, d.ID); err != nil {
		log.Printf("[Automatizaciones] scheduler: no se pudo actualizar last_run_at de %s: %v", d.ID, err)
	}
}
