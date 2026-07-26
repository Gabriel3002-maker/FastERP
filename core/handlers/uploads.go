package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// UploadHandler guarda archivos subidos desde el admin (imágenes de
// producto de tienda_web, medios de sitio_web) en disco y devuelve la URL
// pública donde quedaron servidos — ver el mount de /uploads/ en main.go.
//
// No hay un modelo @fast para esto a propósito: @fast maneja filas, no
// bytes de archivo. El caller guarda la URL que este endpoint devuelve en
// el campo que corresponda (store_image.url, media.url) con un POST normal.
type UploadHandler struct {
	sessionManager *SessionManager
	dir            string // raíz de subida, ej. "./uploads"
}

func NewUploadHandler(sessionManager *SessionManager, dir string) *UploadHandler {
	return &UploadHandler{sessionManager: sessionManager, dir: dir}
}

const maxUploadBytes = 10 << 20 // 10MB

// allowedUploadExt es deliberadamente chica: lo que hoy necesitan
// productos (imágenes) y medios del sitio (imágenes + algún PDF/video).
var allowedUploadExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".svg": true,
	".pdf": true, ".mp4": true, ".webm": true,
}

// allowedUploadKinds son las únicas subcarpetas válidas de ./uploads — así
// "?kind=" no puede usarse para escribir fuera de ahí.
var allowedUploadKinds = map[string]bool{"products": true, "media": true}

// Upload atiende POST /api/_uploads?kind=products|media (multipart, campo
// "file") y responde {"url": "/uploads/{kind}/{nombre}"}. Exige sesión —
// lo llaman las pantallas de admin de tienda_web/sitio_web, no es público.
func (h *UploadHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}
	if _, err := h.requireSession(r); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}

	kind := r.URL.Query().Get("kind")
	if !allowedUploadKinds[kind] {
		writeErr(w, http.StatusBadRequest, `"kind" debe ser "products" o "media"`)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "falta el archivo (campo \"file\")")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedUploadExt[ext] {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("extensión no permitida: %q", ext))
		return
	}

	kindDir := filepath.Join(h.dir, kind)
	if err := os.MkdirAll(kindDir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo preparar el directorio de subida")
		return
	}

	name, err := randomFilename(ext)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo generar el nombre del archivo")
		return
	}

	dst, err := os.Create(filepath.Join(kindDir, name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo guardar el archivo")
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudo guardar el archivo")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"url": fmt.Sprintf("/uploads/%s/%s", kind, name)})
}

func randomFilename(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}

// requireSession exige una sesión válida — subir un archivo no es más
// sensible que crear el registro que lo va a referenciar, mismo criterio
// que AutomationHandler.requireSession.
func (h *UploadHandler) requireSession(r *http.Request) (*Claims, error) {
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
