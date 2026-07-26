package handlers

import (
	"fmt"
	"net/http"

	"github.com/fasterp/backend/sdk"
)

// ComprasHandler suma stock cuando se recibe una orden de compra. "Pedir" y
// "Cancelar" son sólo cambios de estado (endpoint genérico de transición);
// sólo "Recibir" mueve stock, por eso tiene endpoint propio con sesión.
type ComprasHandler struct {
	sessionManager *SessionManager
	crud           *GenericCRUDHandler
}

func NewComprasHandler(sessionManager *SessionManager, crud *GenericCRUDHandler) *ComprasHandler {
	return &ComprasHandler{sessionManager: sessionManager, crud: crud}
}

// ReceivePurchase atiende POST /api/_compras/purchase/{id}/receive.
// Transición "recibir" (bloquea la fila y valida que esté "ordered" — evita
// sumar stock dos veces), luego movimientos POSITIVOS (reason "compra") y, de
// paso, actualiza el costo del producto al último costo de compra.
func (h *ComprasHandler) ReceivePurchase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}
	claims, err := requireSessionClaims(h.sessionManager, r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "falta el id de la compra")
		return
	}

	ctx := r.Context()
	purchSDK, err := h.crud.sdkFor(ctx, "compras", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	productsSDK, err := h.crud.sdkFor(ctx, "products", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if _, err := purchSDK.Get(ctx, "purchase", id); err != nil {
		writeErr(w, http.StatusNotFound, "compra no encontrada")
		return
	}

	transition, err := purchSDK.Transition(ctx, "purchase", id, "recibir")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	lines, err := purchSDK.List(ctx, "purchase_line", sdk.ListOptions{
		Limit:   100,
		Filters: []sdk.Filter{{Field: "purchase_id", Op: "eq", Value: id}},
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"order": transition,
			"steps": []runStep{{Node: id, Type: "purchase_line", OK: false, Detail: "no se pudieron leer las líneas: " + err.Error()}},
		})
		return
	}

	reference := "OC-" + id
	var steps []runStep
	for _, line := range lines.Data {
		productID := stringField(line, "product_id")
		qty := floatField(line, "quantity")
		cost := floatField(line, "unit_cost")

		_, serr := productsSDK.Create(ctx, "stock_move", map[string]any{
			"product_id": productID,
			"quantity":   qty,
			"reason":     "compra",
			"reference":  reference,
			"note":       fmt.Sprintf("Recepción de compra %s", id),
		})
		detail := fmt.Sprintf("ingresó %v de %s", qty, productID)
		if serr != nil {
			detail = "no se pudo ingresar " + productID + ": " + serr.Error()
		}
		steps = append(steps, runStep{Node: productID, Type: "compra", OK: serr == nil, Detail: detail})

		if serr == nil && cost > 0 {
			if uerr := productsSDK.Update(ctx, "product", productID, map[string]any{"cost": cost}); uerr != nil {
				steps = append(steps, runStep{Node: productID, Type: "costo", OK: false, Detail: "no se pudo actualizar el costo: " + uerr.Error()})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"order": transition, "steps": steps})
}
