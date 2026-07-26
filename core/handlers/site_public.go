package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fasterp/backend/sdk"
)

// SitePublicHandler sirve el sitio público de sitio_web (sin sesión):
// GET /site, GET /site/{slug} y el feed de productos publicados de
// tienda_web que esas páginas consumen desde su propio JS.
//
// A propósito NO reusa el dispatcher @fast genérico para leer web_page ni
// store_product: ese dispatcher no filtra por "publicado" ni exige nada —
// dejar que una página pública lo llame directo filtraría borradores y
// productos sin publicar a cualquiera que mire la pestaña de red. Estos
// tres handlers son el único camino de lectura pública, y siempre agregan
// el filtro published=true.
type SitePublicHandler struct {
	crud *GenericCRUDHandler
}

func NewSitePublicHandler(crud *GenericCRUDHandler) *SitePublicHandler {
	return &SitePublicHandler{crud: crud}
}

// publicTenant resuelve "?t=<slug o uuid>", con "default" como valor por
// defecto — el mismo tenant semilla que crea db/seeds.go. Así un cliente
// sin multi-tenant configurado tiene su sitio andando en /site sin tocar
// nada, y uno con varios tenants los separa con /site?t=suempresa.
func (h *SitePublicHandler) publicTenant(ctx context.Context, r *http.Request) (string, error) {
	slug := r.URL.Query().Get("t")
	if slug == "" {
		slug = "default"
	}
	return h.crud.ResolveTenant(ctx, slug)
}

// PublicHome sirve GET /site: la página publicada marcada is_home, o si
// ninguna lo está, la publicada con menor "seq".
func (h *SitePublicHandler) PublicHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID, err := h.publicTenant(ctx, r)
	if err != nil {
		http.Error(w, "sitio no encontrado", http.StatusNotFound)
		return
	}

	record, ok := h.findHome(ctx, tenantID)
	if !ok {
		http.Error(w, "todavía no hay ninguna página publicada", http.StatusNotFound)
		return
	}
	h.renderPage(w, tenantID, record)
}

// findHome busca la home pública de un tenant sin escribir ninguna
// respuesta — la usan tanto PublicHome como TryRenderHome (esta última
// desde "/" en main.go, que necesita poder "no encontrar nada" en
// silencio y caer al login en su lugar).
func (h *SitePublicHandler) findHome(ctx context.Context, tenantID string) (map[string]any, bool) {
	siteSDK, err := h.crud.sdkFor(ctx, "sitio_web", tenantID, "")
	if err != nil {
		return nil, false
	}

	page, err := siteSDK.List(ctx, "web_page", sdk.ListOptions{
		Limit: 1,
		Filters: []sdk.Filter{
			{Field: "is_home", Op: "eq", Value: "true"},
			{Field: "published", Op: "eq", Value: "true"},
		},
	})
	if err == nil && len(page.Data) > 0 {
		return page.Data[0], true
	}

	fallback, err := siteSDK.List(ctx, "web_page", sdk.ListOptions{
		Limit: 1, OrderBy: "seq", OrderDir: "asc",
		Filters: []sdk.Filter{{Field: "published", Op: "eq", Value: "true"}},
	})
	if err == nil && len(fallback.Data) > 0 {
		return fallback.Data[0], true
	}
	return nil, false
}

// TryRenderHome sirve la home pública del tenant si tiene una publicada, y
// devuelve true. Si no hay nada que mostrar, NO escribe respuesta y
// devuelve false — así "/" en main.go puede mostrar el login en su lugar
// en vez de un error. Esto es lo que hace que la raíz del dominio muestre
// el sitio de un cliente (una vez que publicó algo) en lugar de la
// pantalla de login del ERP.
func (h *SitePublicHandler) TryRenderHome(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	record, ok := h.findHome(r.Context(), tenantID)
	if !ok {
		return false
	}
	h.renderPage(w, tenantID, record)
	return true
}

// PublicPage sirve GET /site/{slug}: la página publicada con ese slug.
func (h *SitePublicHandler) PublicPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ctx := r.Context()
	tenantID, err := h.publicTenant(ctx, r)
	if err != nil {
		http.Error(w, "sitio no encontrado", http.StatusNotFound)
		return
	}

	siteSDK, err := h.crud.sdkFor(ctx, "sitio_web", tenantID, "")
	if err != nil {
		http.Error(w, "sitio no disponible", http.StatusNotFound)
		return
	}

	page, err := siteSDK.List(ctx, "web_page", sdk.ListOptions{
		Limit: 1,
		Filters: []sdk.Filter{
			{Field: "slug", Op: "eq", Value: slug},
			{Field: "published", Op: "eq", Value: "true"},
		},
	})
	if err != nil || len(page.Data) == 0 {
		http.Error(w, "página no encontrada", http.StatusNotFound)
		return
	}

	h.renderPage(w, tenantID, page.Data[0])
}

// renderPage arma el documento HTML completo a partir de una fila
// web_page. El HTML/CSS/JS del usuario se sirve tal cual, sin escapar —
// es la funcionalidad pedida (páginas con código propio); en producción
// conviene servir esto desde otro origen, como ya advertía el módulo.
//
// Antes del <script> del usuario se inyecta window.FASTERP_TENANT con el
// UUID del tenant resuelto, para que ese JS pueda pedir
// /api/public/products?t=<lo que sea> sin tener que adivinarlo.
//
// También se inyecta un link chico y fijo a /login: como "/" ahora puede
// mostrar directo el sitio del cliente en vez del login (ver
// TryRenderHome, llamado desde "/" en main.go), sin esto un admin no
// tendría cómo volver a entrar al ERP una vez que su sitio está publicado
// en la raíz. Estilo inline a propósito, para no depender del CSS de la
// página (que es del cliente, no del core) ni chocar con él.
func (h *SitePublicHandler) renderPage(w http.ResponseWriter, tenantID string, page map[string]any) {
	title, _ := page["title"].(string)
	html, _ := page["html"].(string)
	css, _ := page["css"].(string)
	js, _ := page["js"].(string)

	tenantJSON, _ := json.Marshal(tenantID)
	titleJSON, _ := json.Marshal(title)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>%s</style>
</head>
<body>
%s
<a href="/login" style="position:fixed;bottom:10px;right:10px;z-index:2147483647;font:12px/1.4 system-ui,sans-serif;background:#111827;color:#fff;padding:5px 10px;border-radius:6px;text-decoration:none;opacity:.55;">Iniciar sesión</a>
<script>window.FASTERP_TENANT = %s;</script>
<script>%s</script>
</body>
</html>`, jsonToInnerText(titleJSON), css, html, tenantJSON, js)
}

// jsonToInnerText pela las comillas de un string ya codificado con
// json.Marshal, para poder ponerlo dentro de <title> sin las comillas
// literales que json.Marshal agrega (y sin volver a escapar HTML, que ya
// hizo encoding/json con < etc. al codificar el string).
func jsonToInnerText(quoted []byte) string {
	if len(quoted) >= 2 {
		return string(quoted[1 : len(quoted)-1])
	}
	return ""
}

// productCard es lo que ve la tienda pública por producto — sin nada
// interno (id de Odoo, stock exacto no hace falta acá, etc.).
type productCard struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Sku         string  `json:"sku"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Featured    bool    `json:"featured"`
	Image       string  `json:"image"`
}

// PublicProducts sirve GET /api/public/products: por cada store_product
// publicado, busca el producto real en "products" (nombre/sku/precio, y
// que siga activo — un producto desactivado ahí desaparece de la tienda
// aunque su store_product siga marcado publicado) y arma la tarjeta
// combinando los dos, más su primera imagen. tienda_web ya no guarda
// nombre/precio propios — son sólo "publicado/destacado/descripción de
// marketing" sobre un product_id real (ver modules/tienda_web/manifest.json).
func (h *SitePublicHandler) PublicProducts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID, err := h.publicTenant(ctx, r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "sitio no encontrado")
		return
	}

	storeSDK, err := h.crud.sdkFor(ctx, "tienda_web", tenantID, "")
	if err != nil {
		writeErr(w, http.StatusNotFound, "tienda no disponible")
		return
	}
	productsSDK, err := h.crud.sdkFor(ctx, "products", tenantID, "")
	if err != nil {
		writeErr(w, http.StatusNotFound, "catálogo no disponible")
		return
	}

	page, err := storeSDK.List(ctx, "store_product", sdk.ListOptions{
		// ListOptions.Limit sólo acepta sdk.PageSizes (10/20/50/100); 100 es
		// el techo real, no un número elegido a mano acá.
		Limit: 100, OrderBy: "featured", OrderDir: "desc",
		Filters: []sdk.Filter{{Field: "published", Op: "eq", Value: "true"}},
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	cards := make([]productCard, 0, len(page.Data))
	for _, row := range page.Data {
		productID := stringField(row, "product_id")
		product, err := productsSDK.Get(ctx, "product", productID)
		if err != nil || !boolField(product, "active") {
			continue // producto borrado o desactivado: no se publica
		}

		description := stringField(row, "display_description")
		if description == "" {
			description = stringField(product, "description")
		}

		card := productCard{
			ID:          productID,
			Name:        stringField(product, "name"),
			Sku:         stringField(product, "sku"),
			Description: description,
			Price:       floatField(product, "sale_price"),
			Featured:    boolField(row, "featured"),
		}
		if images, err := storeSDK.List(ctx, "store_image", sdk.ListOptions{
			Limit: 1, OrderBy: "position", OrderDir: "asc",
			Filters: []sdk.Filter{{Field: "product_id", Op: "eq", Value: productID}},
		}); err == nil && len(images.Data) > 0 {
			card.Image = stringField(images.Data[0], "url")
		}
		cards = append(cards, card)
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": cards})
}

func stringField(row map[string]any, field string) string {
	s, _ := row[field].(string)
	return s
}

func boolField(row map[string]any, field string) bool {
	b, _ := row[field].(bool)
	return b
}

func floatField(row map[string]any, field string) float64 {
	switch v := row[field].(type) {
	case float64:
		return v
	case string:
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f
	default:
		return 0
	}
}
