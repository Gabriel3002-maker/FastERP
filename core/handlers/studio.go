package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/fasterp/backend/sdk"
)

// StudioHandler expone las piezas de Studio-Flujo que NO tocan disco.
//
// Interpretar un diagrama es cómputo puro — no crea archivos, no toca
// ningún módulo instalado — así que no necesita el gateo estricto (admin,
// validar-respaldar-reemplazar) que sí va a requerir la pieza que realmente
// escriba manifiestos a modules/. Esa pieza es aparte y vive fuera de este
// handler cuando se construya.
type StudioHandler struct{}

func NewStudioHandler() *StudioHandler {
	return &StudioHandler{}
}

// ParseMermaid interpreta un diagrama de estados y devuelve el workflow que
// el motor entendería — o el error exacto de qué está mal en el diagrama, en
// los mismos términos con que la persona lo dibujó (línea, estados, acción).
func (h *StudioHandler) ParseMermaid(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	var payload struct {
		Diagram string `json:"diagram"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if strings.TrimSpace(payload.Diagram) == "" {
		writeErr(w, http.StatusBadRequest, "falta \"diagram\"")
		return
	}

	parsed, err := sdk.ParseMermaidStateDiagram(payload.Diagram)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parsed)
}
