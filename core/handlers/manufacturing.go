package handlers

import (
	"fmt"
	"net/http"

	"github.com/fasterp/backend/sdk"
)

// ManufacturingHandler completa órdenes de producción: consume el stock de
// los componentes de la BOM y produce el stock del producto terminado.
//
// Sólo existe este endpoint para "completar" — "iniciar" y "cancelar" son
// sólo cambiar el estado (sin efectos de stock) y ya salen gratis del
// endpoint genérico de transición (POST /api/{módulo}/{modelo}/{id}/transition,
// ver GenericCRUDHandler.HandleTransition). La pestaña Producción del
// módulo products a propósito no usa <div data-fast-view> para
// production_order, así nadie completa una orden por el camino genérico sin
// mover stock. Los modelos bom/bom_line/production_order viven en el módulo
// "products" (antes eran del módulo "manufactura", que se fusionó).
type ManufacturingHandler struct {
	sessionManager *SessionManager
	crud           *GenericCRUDHandler
}

func NewManufacturingHandler(sessionManager *SessionManager, crud *GenericCRUDHandler) *ManufacturingHandler {
	return &ManufacturingHandler{sessionManager: sessionManager, crud: crud}
}

// CompleteProductionOrder atiende POST
// /api/_products/production_order/{id}/complete. Exige sesión, sin
// X-Tenant-ID (como /api/_automation/run).
//
// Orden de operaciones a propósito: primero Transition() (bloquea la fila
// con SELECT FOR UPDATE y valida que esté "in_progress" — eso es lo que
// evita que dos clicks de "Completar" muevan stock dos veces), recién
// después los movimientos de stock. Si algo falla a mitad de los
// movimientos, la orden ya quedó "done" pero el stock puede quedar
// incompleto — no hay transacción entre módulos (el SDK no la expone entre
// modelos distintos), por eso se reporta paso a paso qué se aplicó, en vez
// de fingir una atomicidad que no existe (mismo criterio que
// AutomationHandler.execute).
func (h *ManufacturingHandler) CompleteProductionOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}
	claims, err := h.requireSession(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "falta el id de la orden")
		return
	}

	ctx := r.Context()
	mfgSDK, err := h.crud.sdkFor(ctx, "products", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	order, err := mfgSDK.Get(ctx, "production_order", id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "orden de producción no encontrada")
		return
	}
	productID := stringField(order, "product_id")
	bomID := stringField(order, "bom_id")
	quantity := floatField(order, "quantity")
	if productID == "" || bomID == "" || quantity <= 0 {
		writeErr(w, http.StatusBadRequest, "la orden no tiene producto/BOM/cantidad válidos")
		return
	}

	transition, err := mfgSDK.Transition(ctx, "production_order", id, "completar")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	lines, err := mfgSDK.List(ctx, "bom_line", sdk.ListOptions{
		Limit:   100,
		Filters: []sdk.Filter{{Field: "bom_id", Op: "eq", Value: bomID}},
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"order": transition,
			"steps": []runStep{{Node: bomID, Type: "bom_line", OK: false, Detail: "no se pudieron leer las líneas de la BOM: " + err.Error()}},
		})
		return
	}

	productsSDK, err := h.crud.sdkFor(ctx, "products", claims.TenantID, claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"order": transition,
			"steps": []runStep{{Node: "products", Type: "stock_move", OK: false, Detail: "no se pudo acceder al módulo de productos: " + err.Error()}},
		})
		return
	}

	reference := "OP-" + id
	var steps []runStep

	for _, line := range lines.Data {
		componentID := stringField(line, "component_product_id")
		perUnit := floatField(line, "quantity")
		consumed := quantity * perUnit

		_, err := productsSDK.Create(ctx, "stock_move", map[string]any{
			"product_id": componentID,
			"quantity":   -consumed,
			"reason":     "produccion_consumo",
			"reference":  reference,
			"note":       fmt.Sprintf("Consumido por orden de producción %s", id),
		})
		detail := fmt.Sprintf("consumió %v de %s", consumed, componentID)
		if err != nil {
			detail = "no se pudo descontar " + componentID + ": " + err.Error()
		}
		steps = append(steps, runStep{Node: componentID, Type: "consumo", OK: err == nil, Detail: detail})
	}

	_, err = productsSDK.Create(ctx, "stock_move", map[string]any{
		"product_id": productID,
		"quantity":   quantity,
		"reason":     "produccion_entrada",
		"reference":  reference,
		"note":       fmt.Sprintf("Producido por orden de producción %s", id),
	})
	detail := fmt.Sprintf("produjo %v de %s", quantity, productID)
	if err != nil {
		detail = "no se pudo acreditar " + productID + ": " + err.Error()
	}
	steps = append(steps, runStep{Node: productID, Type: "entrada", OK: err == nil, Detail: detail})

	writeJSON(w, http.StatusOK, map[string]any{"order": transition, "steps": steps})
}

func (h *ManufacturingHandler) requireSession(r *http.Request) (*Claims, error) {
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
