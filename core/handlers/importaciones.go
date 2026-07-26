package handlers

import (
	"fmt"
	"net/http"

	"github.com/fasterp/backend/sdk"
)

// ImportacionesHandler recibe una importación repartiendo los gastos (flete +
// aduana + seguro + otros) proporcional al valor FOB de cada línea, para
// obtener el costo real por unidad (landed cost). Con eso suma stock y graba
// el costo real en cada producto. Las etapas (en tránsito / aduana /
// cancelar) son cambios de estado por el endpoint genérico de transición.
type ImportacionesHandler struct {
	sessionManager *SessionManager
	crud           *GenericCRUDHandler
}

func NewImportacionesHandler(sessionManager *SessionManager, crud *GenericCRUDHandler) *ImportacionesHandler {
	return &ImportacionesHandler{sessionManager: sessionManager, crud: crud}
}

// ReceiveImport atiende POST /api/_importaciones/import_order/{id}/receive.
// Transición "recibir" (bloquea la fila y valida la etapa — evita recibir dos
// veces), luego por línea: reparte los gastos, crea el movimiento POSITIVO
// (reason "importacion"), graba landed_unit_cost en la línea y actualiza el
// costo del producto.
func (h *ImportacionesHandler) ReceiveImport(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusBadRequest, "falta el id de la importación")
		return
	}

	ctx := r.Context()
	impSDK, err := h.crud.sdkFor(ctx, "importaciones", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	productsSDK, err := h.crud.sdkFor(ctx, "products", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	order, err := impSDK.Get(ctx, "import_order", id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "importación no encontrada")
		return
	}

	extra := floatField(order, "freight_cost") +
		floatField(order, "customs_cost") +
		floatField(order, "insurance_cost") +
		floatField(order, "other_cost")

	// Se leen las líneas ANTES de la transición para calcular el total FOB
	// que sirve de base al reparto.
	lines, err := impSDK.List(ctx, "import_line", sdk.ListOptions{
		Limit:   100,
		Filters: []sdk.Filter{{Field: "import_order_id", Op: "eq", Value: id}},
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudieron leer las líneas: "+err.Error())
		return
	}

	var goods float64
	for _, line := range lines.Data {
		goods += floatField(line, "line_fob")
	}

	transition, err := impSDK.Transition(ctx, "import_order", id, "recibir")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	reference := "IMP-" + id
	var steps []runStep
	for _, line := range lines.Data {
		lineID := stringField(line, "id")
		productID := stringField(line, "product_id")
		qty := floatField(line, "quantity")
		unitFob := floatField(line, "unit_fob")
		lineFob := floatField(line, "line_fob")

		// Reparto proporcional al FOB; si no hay base FOB, el gasto no se
		// puede repartir y el costo real queda igual al FOB.
		share := 0.0
		if goods > 0 {
			share = extra * (lineFob / goods)
		}
		landedUnit := unitFob
		if qty > 0 {
			landedUnit = unitFob + share/qty
		}

		_, serr := productsSDK.Create(ctx, "stock_move", map[string]any{
			"product_id": productID,
			"quantity":   qty,
			"reason":     "importacion",
			"reference":  reference,
			"note":       fmt.Sprintf("Recepción de importación %s", id),
		})
		detail := fmt.Sprintf("ingresó %v de %s a costo %.4f", qty, productID, landedUnit)
		if serr != nil {
			detail = "no se pudo ingresar " + productID + ": " + serr.Error()
		}
		steps = append(steps, runStep{Node: productID, Type: "importacion", OK: serr == nil, Detail: detail})

		if serr == nil {
			if uerr := impSDK.Update(ctx, "import_line", lineID, map[string]any{"landed_unit_cost": landedUnit}); uerr != nil {
				steps = append(steps, runStep{Node: lineID, Type: "costo_linea", OK: false, Detail: "no se pudo guardar el costo real en la línea: " + uerr.Error()})
			}
			if uerr := productsSDK.Update(ctx, "product", productID, map[string]any{"cost": landedUnit}); uerr != nil {
				steps = append(steps, runStep{Node: productID, Type: "costo", OK: false, Detail: "no se pudo actualizar el costo del producto: " + uerr.Error()})
			}
		}
	}

	// Totales informativos en la cabecera.
	if uerr := impSDK.Update(ctx, "import_order", id, map[string]any{
		"goods_total":  goods,
		"landed_total": goods + extra,
	}); uerr != nil {
		steps = append(steps, runStep{Node: id, Type: "totales", OK: false, Detail: "no se pudieron guardar los totales: " + uerr.Error()})
	}

	writeJSON(w, http.StatusOK, map[string]any{"order": transition, "steps": steps})
}
