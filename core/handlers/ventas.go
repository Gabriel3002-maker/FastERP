package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fasterp/backend/sdk"
)

// VentasHandler mueve stock cuando se cobra o se anula una venta. Igual que
// ManufacturingHandler, la lógica que toca stock de otro módulo (products) va
// por un endpoint propio con sesión (sin X-Tenant-ID): el CRUD y las
// transiciones simples ya salen gratis del dispatcher genérico.
type VentasHandler struct {
	sessionManager *SessionManager
	crud           *GenericCRUDHandler
}

func NewVentasHandler(sessionManager *SessionManager, crud *GenericCRUDHandler) *VentasHandler {
	return &VentasHandler{sessionManager: sessionManager, crud: crud}
}

type checkoutLine struct {
	ProductID   string  `json:"product_id"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

type checkoutReq struct {
	PartnerID    string         `json:"partner_id"`
	CustomerName string         `json:"customer_name"`
	PaidMethod   string         `json:"paid_method"`
	Note         string         `json:"note"`
	Lines        []checkoutLine `json:"lines"`
}

// Checkout atiende POST /api/_ventas/checkout. Crea la venta (nace "done"
// porque el workflow rechaza fijar el estado en Create) y sus líneas, y por
// cada línea descuenta stock en products con un movimiento NEGATIVO
// (reason "venta"). Best-effort: reporta paso a paso, no hay transacción
// entre módulos (mismo criterio que ManufacturingHandler).
func (h *VentasHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}
	claims, err := requireSessionClaims(h.sessionManager, r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req checkoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "cuerpo inválido")
		return
	}
	if len(req.Lines) == 0 {
		writeErr(w, http.StatusBadRequest, "la venta no tiene productos")
		return
	}
	if req.PaidMethod == "" {
		req.PaidMethod = "efectivo"
	}

	ctx := r.Context()
	salesSDK, err := h.crud.sdkFor(ctx, "ventas", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	productsSDK, err := h.crud.sdkFor(ctx, "products", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var total float64
	for _, l := range req.Lines {
		total += l.Quantity * l.UnitPrice
	}

	saleID, err := salesSDK.Create(ctx, "sale", map[string]any{
		"partner_id":    req.PartnerID,
		"customer_name": req.CustomerName,
		"paid_method":   req.PaidMethod,
		"subtotal":      total,
		"total":         total,
		"note":          req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo crear la venta: "+err.Error())
		return
	}

	reference := "VTA-" + saleID
	var steps []runStep
	for _, l := range req.Lines {
		if _, lerr := salesSDK.Create(ctx, "sale_line", map[string]any{
			"sale_id":      saleID,
			"product_id":   l.ProductID,
			"product_name": l.ProductName,
			"quantity":     l.Quantity,
			"unit_price":   l.UnitPrice,
			"line_total":   l.Quantity * l.UnitPrice,
		}); lerr != nil {
			steps = append(steps, runStep{Node: l.ProductID, Type: "linea", OK: false, Detail: "no se pudo registrar la línea: " + lerr.Error()})
			continue
		}

		_, serr := productsSDK.Create(ctx, "stock_move", map[string]any{
			"product_id": l.ProductID,
			"quantity":   -l.Quantity,
			"reason":     "venta",
			"reference":  reference,
			"note":       fmt.Sprintf("Venta %s", saleID),
		})
		detail := fmt.Sprintf("descontó %v de %s", l.Quantity, l.ProductID)
		if serr != nil {
			detail = "no se pudo descontar " + l.ProductID + ": " + serr.Error()
		}
		steps = append(steps, runStep{Node: l.ProductID, Type: "venta", OK: serr == nil, Detail: detail})
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": saleID, "total": total, "steps": steps})
}

// CancelSale atiende POST /api/_ventas/sale/{id}/cancel. Transición
// "cancelar" (bloquea la fila y valida que esté "done" — evita reponer stock
// dos veces) y luego repone el stock con movimientos POSITIVOS
// (reason "devolucion").
func (h *VentasHandler) CancelSale(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusBadRequest, "falta el id de la venta")
		return
	}

	ctx := r.Context()
	salesSDK, err := h.crud.sdkFor(ctx, "ventas", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	productsSDK, err := h.crud.sdkFor(ctx, "products", claims.TenantID, claims.UserID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	transition, err := salesSDK.Transition(ctx, "sale", id, "cancelar")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	lines, err := salesSDK.List(ctx, "sale_line", sdk.ListOptions{
		Limit:   100,
		Filters: []sdk.Filter{{Field: "sale_id", Op: "eq", Value: id}},
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"sale":  transition,
			"steps": []runStep{{Node: id, Type: "sale_line", OK: false, Detail: "no se pudieron leer las líneas: " + err.Error()}},
		})
		return
	}

	reference := "VTA-" + id
	var steps []runStep
	for _, line := range lines.Data {
		productID := stringField(line, "product_id")
		qty := floatField(line, "quantity")
		_, serr := productsSDK.Create(ctx, "stock_move", map[string]any{
			"product_id": productID,
			"quantity":   qty,
			"reason":     "devolucion",
			"reference":  reference,
			"note":       fmt.Sprintf("Anulación de venta %s", id),
		})
		detail := fmt.Sprintf("repuso %v de %s", qty, productID)
		if serr != nil {
			detail = "no se pudo reponer " + productID + ": " + serr.Error()
		}
		steps = append(steps, runStep{Node: productID, Type: "devolucion", OK: serr == nil, Detail: detail})
	}

	writeJSON(w, http.StatusOK, map[string]any{"sale": transition, "steps": steps})
}

// requireSessionClaims valida la cookie de sesión y devuelve los claims.
// Compartida por los handlers de ventas/compras/importaciones (mismo patrón
// que ManufacturingHandler.requireSession, sin X-Tenant-ID).
func requireSessionClaims(sm *SessionManager, r *http.Request) (*Claims, error) {
	if sm == nil {
		return nil, fmt.Errorf("sesión no configurada")
	}
	token := sm.GetTokenFromRequest(r)
	if token == "" {
		return nil, fmt.Errorf("se requiere iniciar sesión")
	}
	claims, err := sm.ValidateToken(token)
	if err != nil {
		return nil, fmt.Errorf("sesión inválida o vencida")
	}
	return claims, nil
}
