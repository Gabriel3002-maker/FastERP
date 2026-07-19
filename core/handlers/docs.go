package handlers

import (
	"net/http"

	"github.com/fasterp/backend/sdk"
)

// DocsHandler publica la API generada desde los manifiestos de los módulos.
//
// No hay documentación escrita a mano: lo que un módulo declara en su
// manifest.json es lo que aparece aquí y lo que el motor realmente expone.
type DocsHandler struct {
	modulesDir string
}

func NewDocsHandler(modulesDir string) *DocsHandler {
	return &DocsHandler{modulesDir: modulesDir}
}

// OpenAPI sirve la especificación OpenAPI 3.0 en JSON.
// Es el contrato que consume Swagger UI y cualquier otra plataforma.
func (h *DocsHandler) OpenAPI(w http.ResponseWriter, r *http.Request) {
	manifests, err := sdk.LoadManifests(h.modulesDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, sdk.BuildOpenAPI(manifests, scheme+"://"+r.Host))
}

// Catalog lista los módulos instalados con sus modelos y campos.
//
// Es la base para cualquier herramienta que quiera extender un módulo
// existente en vez de crear siempre uno nuevo (Studio-Flujo, por ejemplo):
// sin saber qué ya hay, no puede ofrecer "agregá esto a contacts" en vez de
// forzar un módulo vacío por cada flujo.
func (h *DocsHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	manifests, err := sdk.LoadManifests(h.modulesDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"modules": sdk.BuildCatalog(manifests),
	})
}

// Docs sirve Swagger UI. Los assets están vendorizados en static/swagger/, así
// que la documentación funciona sin internet — requisito para on-premise.
func (h *DocsHandler) Docs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(swaggerHTML))
}

const swaggerHTML = `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FastERP — API @fast</title>
<link rel="stylesheet" href="/static/swagger/swagger-ui.css">
<style>
  body { margin: 0; background: #fafafa; }
  .topbar { display: none; }
  .fast-header {
    background: #1b1f2a; color: #e6e9ef; padding: 1.25rem 1.5rem;
    font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
  }
  .fast-header h1 { margin: 0 0 .3rem; font-size: 1.3rem; }
  .fast-header p { margin: 0; font-size: .88rem; color: #9aa4b2; max-width: 80ch; }
  .fast-header code {
    background: rgba(255,255,255,.1); padding: .1rem .35rem; border-radius: 4px;
  }
  .fast-header a { color: #4f8cff; }
</style>
</head>
<body>
<div class="fast-header">
  <h1>FastERP — API <code>@fast</code></h1>
  <p>
    Generada desde el <code>manifest.json</code> de cada módulo: todo modelo declarado
    obtiene CRUD, paginación (10/20/50/100), búsqueda y filtros sin escribir código.
    Para probar los endpoints pulsa <strong>Authorize</strong> e indica el tenant
    (por ejemplo <code>default</code>). Contrato:
    <a href="/api/openapi.json">/api/openapi.json</a>
  </p>
</div>
<div id="swagger-ui"></div>

<script src="/static/swagger/swagger-ui-bundle.js"></script>
<script src="/static/swagger/swagger-ui-standalone-preset.js"></script>
<script>
window.onload = () => {
  const ui = SwaggerUIBundle({
    url: '/api/openapi.json',
    dom_id: '#swagger-ui',
    deepLinking: true,
    displayRequestDuration: true,
    filter: true,
    tryItOutEnabled: true,
    defaultModelsExpandDepth: 1,
    docExpansion: 'list',
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    layout: 'BaseLayout',
    // El tenant se guarda entre recargas para no re-autorizar en cada prueba.
    onComplete: () => {
      const saved = localStorage.getItem('fasterp_tenant');
      if (saved) ui.preauthorizeApiKey('TenantID', saved);
    },
    requestInterceptor: (request) => {
      const tenant = request.headers['X-Tenant-ID'];
      if (tenant) localStorage.setItem('fasterp_tenant', tenant);
      return request;
    },
  });
  window.ui = ui;
};
</script>
</body>
</html>`
