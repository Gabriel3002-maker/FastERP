package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AIHandler genera una página (HTML/CSS/JS) a partir de un prompt en texto
// libre, para el botón "✨ Generar" de sitio_web — bajar la barrera de
// "tenés que saber HTML" a "escribí qué querés".
//
// Agnóstico de proveedor a propósito: "openai" y "anthropic" cubren las dos
// formas de API dominantes y no compatibles entre sí; cualquier servicio
// que hable el formato de chat completions de OpenAI (Groq, Together, un
// modelo local, etc.) ya funciona con provider="openai" apuntando su
// BaseURL ahí, sin código nuevo.
type AIHandler struct {
	sessionManager *SessionManager
	provider       string // "openai" | "anthropic" | ""
	apiKey         string
	model          string
	baseURL        string
}

func NewAIHandler(sessionManager *SessionManager, provider, apiKey, model, baseURL string) *AIHandler {
	if baseURL == "" {
		switch provider {
		case "anthropic":
			baseURL = "https://api.anthropic.com"
		case "openai":
			baseURL = "https://api.openai.com"
		}
	}
	return &AIHandler{
		sessionManager: sessionManager,
		provider:       provider,
		apiKey:         apiKey,
		model:          model,
		baseURL:        strings.TrimSuffix(baseURL, "/"),
	}
}

const aiRequestTimeout = 60 * time.Second

// aiSystemPrompt es fijo — no lo arma el usuario. Le pide al modelo un
// único objeto JSON, sin cerco de markdown, y documenta el único contrato
// con el que la página generada puede integrarse con el resto de FastERP:
// el catálogo público de Tienda Web.
const aiSystemPrompt = `Sos un generador de páginas web para un constructor de sitios simple.
Te van a pedir una página en lenguaje natural. Respondé ÚNICAMENTE con un
objeto JSON (nada de texto antes o después, nada de cercos de markdown)
con exactamente estas claves, todas string (pueden venir vacías si no
aplican):

{"title": "...", "html": "...", "css": "...", "js": "..."}

- "html" es el contenido de <body> (sin las etiquetas <html>/<head>/<body>).
- "css" es CSS plano (sin la etiqueta <style>).
- "js" es JavaScript plano (sin la etiqueta <script>).
- Si el pedido incluye mostrar un catálogo o tienda de productos, tu JS
  puede usar la variable global ya disponible window.FASTERP_TENANT y pedir
  fetch("/api/public/products?t=" + window.FASTERP_TENANT) — devuelve
  {"data": [{"id","name","sku","description","price","featured","image"}]}
  con sólo los productos publicados. No inventes otros endpoints.
- HTML/CSS/JS simple y autocontenido, sin frameworks ni librerías externas.`

type aiGenerateRequest struct {
	Prompt string `json:"prompt"`
}

type aiPageResult struct {
	Title string `json:"title"`
	HTML  string `json:"html"`
	CSS   string `json:"css"`
	JS    string `json:"js"`
}

// GeneratePage atiende POST /api/_ai/generate-page. Exige sesión — no
// X-Tenant-ID, es un endpoint de admin como /api/_uploads, no del
// dispatcher @fast.
func (h *AIHandler) GeneratePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}
	if _, err := h.requireSession(r); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}

	if h.provider == "" || h.apiKey == "" || h.model == "" {
		writeErr(w, http.StatusBadRequest,
			"IA no configurada: definí FASTERP_AI_PROVIDER, FASTERP_AI_API_KEY y FASTERP_AI_MODEL (o el bloque [ai] en fast.conf)")
		return
	}

	var req aiGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeErr(w, http.StatusBadRequest, "falta \"prompt\"")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), aiRequestTimeout)
	defer cancel()

	var (
		text string
		err  error
	)
	switch h.provider {
	case "anthropic":
		text, err = h.callAnthropic(ctx, req.Prompt)
	case "openai":
		text, err = h.callOpenAI(ctx, req.Prompt)
	default:
		err = fmt.Errorf("proveedor de IA desconocido: %q (usá \"openai\" o \"anthropic\")", h.provider)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	page, err := parseAIPage(text)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// parseAIPage limpia un posible cerco ```json ... ``` (los modelos lo
// agregan seguido pese a la instrucción de no hacerlo) y parsea el JSON.
// Si no parsea, el error incluye un fragmento de lo recibido — no se
// intenta adivinar/reparar el JSON a mano.
func parseAIPage(text string) (*aiPageResult, error) {
	clean := strings.TrimSpace(text)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)

	var page aiPageResult
	if err := json.Unmarshal([]byte(clean), &page); err != nil {
		snippet := clean
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return nil, fmt.Errorf("la IA no devolvió JSON válido: %v (recibido: %q)", err, snippet)
	}
	return &page, nil
}

// --- Anthropic ---

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (h *AIHandler) callAnthropic(ctx context.Context, prompt string) (string, error) {
	body, _ := json.Marshal(anthropicRequest{
		Model:     h.model,
		MaxTokens: 8192,
		System:    aiSystemPrompt,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", h.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	respBody, err := doAIRequest(req)
	if err != nil {
		return "", err
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("no se pudo leer la respuesta de Anthropic: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("Anthropic: %s", parsed.Error.Message)
	}
	if len(parsed.Content) == 0 {
		return "", fmt.Errorf("Anthropic no devolvió contenido")
	}
	return parsed.Content[0].Text, nil
}

// --- OpenAI (y compatibles) ---

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (h *AIHandler) callOpenAI(ctx context.Context, prompt string) (string, error) {
	body, _ := json.Marshal(openAIRequest{
		Model: h.model,
		Messages: []openAIMessage{
			{Role: "system", Content: aiSystemPrompt},
			{Role: "user", Content: prompt},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.apiKey)

	respBody, err := doAIRequest(req)
	if err != nil {
		return "", err
	}

	var parsed openAIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("no se pudo leer la respuesta de OpenAI: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("OpenAI: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("OpenAI no devolvió ninguna respuesta")
	}
	return parsed.Choices[0].Message.Content, nil
}

func doAIRequest(req *http.Request) ([]byte, error) {
	client := &http.Client{Timeout: aiRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar con la IA: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la respuesta de la IA: %w", err)
	}
	if resp.StatusCode >= 400 {
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, fmt.Errorf("la IA respondió %d: %s", resp.StatusCode, snippet)
	}
	return body, nil
}

func (h *AIHandler) requireSession(r *http.Request) (*Claims, error) {
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
