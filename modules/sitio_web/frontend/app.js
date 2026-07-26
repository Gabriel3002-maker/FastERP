// ========================================
// Sitio Web
// ========================================
// Constructor de páginas pensado para que alguien que NO programa pueda
// armar su sitio: se arranca de una plantilla o describiéndolo a la IA, y
// la vista previa está siempre a la vista. El código HTML/CSS/JS existe
// entero pero vive detrás de "Editar el código (avanzado)", colapsado.
//
// El CRUD (guardar/listar/borrar páginas y medios) es @fast normal, sin
// backend propio. La generación con IA usa /api/_ai/generate-page.

const $ = (id) => document.getElementById(id);

function tenantID() {
  const token = localStorage.getItem('access_token');
  if (!token) return null;
  try {
    return JSON.parse(atob(token.split('.')[1])).tenant_id;
  } catch {
    return null;
  }
}

function fastHeaders() {
  return { 'X-Tenant-ID': tenantID(), 'Content-Type': 'application/json' };
}

function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c]);
}

async function errorText(response) {
  try {
    const body = await response.json();
    if (body.error) return body.error;
  } catch { /* respuesta sin JSON */ }
  return `Error ${response.status}`;
}

// ========================================
// Plantillas listas para arrancar
// ========================================
// Constantes puras — no dependen de la IA (por eso son el camino principal,
// funcionan siempre). La de "Tienda" trae su JS ya armado para pintar el
// catálogo publicado; se escribe con concatenación de strings a propósito,
// para no chocar con las comillas invertidas de este archivo.

const TIENDA_JS =
  "fetch('/api/public/products?t=' + window.FASTERP_TENANT)\n" +
  "  .then(function (r) { return r.json(); })\n" +
  "  .then(function (d) {\n" +
  "    var grid = document.getElementById('productos');\n" +
  "    var items = (d.data || []);\n" +
  "    if (!items.length) { grid.innerHTML = '<p>Todavía no hay productos publicados.</p>'; return; }\n" +
  "    grid.innerHTML = items.map(function (p) {\n" +
  "      var img = p.image ? '<img src=\"' + p.image + '\" alt=\"\">' : '';\n" +
  "      return '<div class=\"card\">' + img +\n" +
  "        '<h3>' + p.name + '</h3>' +\n" +
  "        '<p class=\"precio\">$' + (p.price || 0) + '</p></div>';\n" +
  "    }).join('');\n" +
  "  });";

const SITE_TEMPLATES = [
  {
    key: 'blank',
    icon: '📄',
    name: 'En blanco',
    desc: 'Una página vacía para armar a tu gusto.',
    title: '',
    html: '<h1>Mi página</h1>\n<p>Empezá a escribir acá.</p>',
    css: 'body { font-family: system-ui, sans-serif; margin: 2rem; }',
    js: '',
  },
  {
    key: 'landing',
    icon: '🚀',
    name: 'Landing simple',
    desc: 'Portada con título, texto y un botón.',
    title: 'Inicio',
    html: [
      '<header class="hero">',
      '  <h1>Tu empresa, en una línea</h1>',
      '  <p>Contá en pocas palabras qué ofrecés y por qué.</p>',
      '  <a class="cta" href="#contacto">Contactanos</a>',
      '</header>',
      '<section id="contacto" class="contacto">',
      '  <h2>Hablemos</h2>',
      '  <p>Escribinos a hola@tuempresa.com</p>',
      '</section>',
    ].join('\n'),
    css: [
      'body { font-family: system-ui, sans-serif; margin: 0; color: #1f2937; }',
      '.hero { text-align: center; padding: 5rem 1.5rem; background: #0f172a; color: #fff; }',
      '.hero h1 { font-size: 2.4rem; margin: 0 0 1rem; }',
      '.cta { display: inline-block; margin-top: 1.5rem; padding: .8rem 1.6rem; background: #2563eb; color: #fff; border-radius: 8px; text-decoration: none; }',
      '.contacto { padding: 3rem 1.5rem; text-align: center; }',
    ].join('\n'),
    js: '',
  },
  {
    key: 'tienda',
    icon: '🛍️',
    name: 'Tienda',
    desc: 'Muestra solo los productos publicados de tu catálogo.',
    title: 'Tienda',
    html: [
      '<header class="top"><h1>Nuestra tienda</h1></header>',
      '<main><div id="productos" class="grid">Cargando…</div></main>',
    ].join('\n'),
    css: [
      'body { font-family: system-ui, sans-serif; margin: 0; color: #1f2937; }',
      '.top { padding: 2rem 1.5rem; background: #0f172a; color: #fff; text-align: center; }',
      'main { padding: 2rem 1.5rem; }',
      '.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 1.25rem; }',
      '.card { border: 1px solid #e5e7eb; border-radius: 10px; padding: 1rem; text-align: center; }',
      '.card img { width: 100%; height: 150px; object-fit: cover; border-radius: 8px; }',
      '.precio { font-weight: 700; color: #2563eb; }',
    ].join('\n'),
    js: TIENDA_JS,
  },
  {
    key: 'contacto',
    icon: '✉️',
    name: 'Contacto',
    desc: 'Una página con tus datos de contacto.',
    title: 'Contacto',
    html: [
      '<main class="contacto">',
      '  <h1>Contacto</h1>',
      '  <p>📍 Tu dirección</p>',
      '  <p>📞 Tu teléfono</p>',
      '  <p>✉️ hola@tuempresa.com</p>',
      '</main>',
    ].join('\n'),
    css: [
      'body { font-family: system-ui, sans-serif; margin: 0; }',
      '.contacto { max-width: 600px; margin: 3rem auto; padding: 0 1.5rem; line-height: 2; }',
    ].join('\n'),
    js: '',
  },
];

function renderTemplates() {
  const box = $('sw-templates');
  box.innerHTML = SITE_TEMPLATES.map((t) => `
    <button type="button" class="sw-template" data-key="${esc(t.key)}">
      <span class="sw-template-icon">${t.icon}</span>
      <span class="sw-template-name">${esc(t.name)}</span>
      <span class="sw-template-desc">${esc(t.desc)}</span>
    </button>`).join('');
  for (const btn of box.querySelectorAll('.sw-template')) {
    btn.addEventListener('click', () => {
      const tpl = SITE_TEMPLATES.find((t) => t.key === btn.dataset.key);
      startFromTemplate(tpl);
    });
  }
}

// ========================================
// Páginas
// ========================================

let pages = [];
let currentPageId = null;

async function loadPages() {
  const box = $('sw-pages');
  try {
    const response = await fetch('/api/sitio_web/web_page?limit=100&order_by=seq&order_dir=asc', {
      headers: fastHeaders(),
    });
    if (!response.ok) throw new Error(await errorText(response));
    const body = await response.json();
    pages = body.data || [];
    renderPagesList();
  } catch (error) {
    box.innerHTML = `<div class="sw-empty sw-error">${esc(error.message)}</div>`;
  }
}

function renderPagesList() {
  const box = $('sw-pages');
  if (!pages.length) {
    box.innerHTML = '<div class="sw-empty">Todavía no tenés páginas. Creá una a la derecha →</div>';
    return;
  }
  box.innerHTML = pages.map((p) => `
    <button type="button" class="sw-page-item${p.id === currentPageId ? ' sw-page-active' : ''}" data-id="${esc(p.id)}">
      <span class="sw-page-title">${p.is_home ? '⭐ ' : ''}${esc(p.title || p.slug || '(sin título)')}</span>
      <span class="sw-page-slug">/${esc(p.slug || '')}</span>
      ${p.published ? '<span class="sw-badge sw-badge-ok">Publicada</span>' : '<span class="sw-badge">Borrador</span>'}
    </button>`).join('');

  for (const btn of box.querySelectorAll('.sw-page-item')) {
    btn.addEventListener('click', () => selectPage(btn.dataset.id));
  }
}

function selectPage(id) {
  const page = pages.find((p) => p.id === id);
  if (!page) return;
  currentPageId = id;
  fillForm(page);
  renderPagesList();
}

// "+ Nueva" (y el arranque sin páginas) muestran el selector de plantillas,
// no un editor de código vacío.
function showStart() {
  currentPageId = null;
  $('sw-editor-form').hidden = true;
  $('sw-start').hidden = false;
  $('sw-start-error').hidden = true;
  $('sw-start-prompt').value = '';
  renderPagesList();
}

function startBlank() {
  const blank = SITE_TEMPLATES.find((t) => t.key === 'blank');
  startFromTemplate(blank);
}

// Arranca el editor con una plantilla cargada, pero SIN guardar todavía
// (currentPageId sigue null): la persona revisa, ajusta y recién guarda.
function startFromTemplate(tpl) {
  currentPageId = null;
  fillForm({
    title: tpl.title,
    html: tpl.html,
    css: tpl.css,
    js: tpl.js,
  });
}

function fillForm(page) {
  $('sw-start').hidden = true;
  $('sw-editor-form').hidden = false;
  $('sw-editor-error').hidden = true;

  $('sw-title').value = page.title || '';
  $('sw-slug').value = page.slug || '';
  $('sw-seq').value = page.seq ?? 0;
  $('sw-published').checked = Boolean(page.published);
  $('sw-is-home').checked = Boolean(page.is_home);
  $('sw-code-html').value = page.html || '';
  $('sw-code-css').value = page.css || '';
  $('sw-code-js').value = page.js || '';

  $('sw-delete-page').hidden = !currentPageId;
  $('sw-ai-prompt').value = '';
  switchCode('html');
  updatePublicLink();
  renderPreview();
}

// El texto del link muestra la ruta real (no sólo el href) porque "el slug
// es la URL" es la lectura intuitiva y NO es así: la home siempre se sirve
// en /site (además de en /site/tu-slug), nunca en el dominio raíz.
function updatePublicLink() {
  const link = $('sw-public-link');
  const slug = $('sw-slug').value.trim();
  const isHome = $('sw-is-home').checked;

  if (!currentPageId || !slug) {
    link.hidden = true;
    return;
  }
  const t = encodeURIComponent(tenantID() || 'default');
  const path = isHome ? '/site' : `/site/${encodeURIComponent(slug)}`;
  link.href = `${path}?t=${t}`;
  link.textContent = `↗ Ver en vivo (${path})`;
  link.hidden = false;
}

async function savePage() {
  const payload = {
    title: $('sw-title').value.trim(),
    slug: $('sw-slug').value.trim(),
    seq: Number($('sw-seq').value) || 0,
    published: $('sw-published').checked,
    is_home: $('sw-is-home').checked,
    html: $('sw-code-html').value,
    css: $('sw-code-css').value,
    js: $('sw-code-js').value,
  };

  if (!payload.slug) {
    // Sin dirección no se puede guardar: si la persona no la puso, se la
    // sugerimos a partir del título en vez de trabarla con un error.
    payload.slug = slugify(payload.title) || 'pagina';
    $('sw-slug').value = payload.slug;
  }

  const saveBtn = $('sw-save-page');
  saveBtn.disabled = true;
  try {
    const url = currentPageId ? `/api/sitio_web/web_page/${currentPageId}` : '/api/sitio_web/web_page';
    const response = await fetch(url, {
      method: currentPageId ? 'PUT' : 'POST',
      headers: fastHeaders(),
      body: JSON.stringify(payload),
    });
    if (!response.ok) throw new Error(await errorText(response));
    const body = await response.json();
    if (!currentPageId) currentPageId = body.id;

    await loadPages();
    selectPage(currentPageId);
  } catch (error) {
    showEditorError(error.message);
  } finally {
    saveBtn.disabled = false;
  }
}

function slugify(text) {
  return String(text || '').toLowerCase().trim()
    .normalize('NFD').replace(/[̀-ͯ]/g, '') // saca acentos
    .replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
}

async function deletePage() {
  if (!currentPageId) return;
  if (!confirm('¿Eliminar esta página?')) return;
  try {
    const response = await fetch(`/api/sitio_web/web_page/${currentPageId}`, {
      method: 'DELETE', headers: fastHeaders(),
    });
    if (!response.ok) throw new Error(await errorText(response));
    currentPageId = null;
    await loadPages();
    showStart();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

function showEditorError(message) {
  const box = $('sw-editor-error');
  box.textContent = message;
  box.hidden = false;
}

// ========================================
// Generar con IA
// ========================================

// Reutilizada por el prompt del onboarding y por el del editor. Si estamos
// en el onboarding (isStart), primero abre el editor en blanco para volcar
// el resultado ahí.
async function generateWithAI(promptEl, btn, errorEl, isStart) {
  const prompt = promptEl.value.trim();
  if (!prompt) return;

  if (!isStart) {
    const hasContent = $('sw-code-html').value.trim() || $('sw-code-css').value.trim() || $('sw-code-js').value.trim();
    if (hasContent && !confirm('Esto va a reemplazar el contenido actual de la página. ¿Seguir?')) return;
  }

  const original = btn.textContent;
  btn.disabled = true;
  btn.textContent = 'Generando…';
  errorEl.hidden = true;

  try {
    const response = await fetch('/api/_ai/generate-page', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt }),
    });
    if (!response.ok) throw new Error(await errorText(response));
    const result = await response.json();

    if (isStart) startBlank(); // abre el editor donde volcar lo generado
    if (result.title && !$('sw-title').value.trim()) $('sw-title').value = result.title;
    $('sw-code-html').value = result.html || '';
    $('sw-code-css').value = result.css || '';
    $('sw-code-js').value = result.js || '';
    renderPreview();
  } catch (error) {
    errorEl.textContent = error.message;
    errorEl.hidden = false;
  } finally {
    btn.disabled = false;
    btn.textContent = original;
  }
}

// ========================================
// Editor de código (colapsado) + vista previa
// ========================================

function switchCode(which) {
  for (const btn of document.querySelectorAll('.sw-code-tab')) {
    btn.classList.toggle('sw-code-tab-active', btn.dataset.code === which);
  }
  for (const panel of document.querySelectorAll('.sw-code-panel')) {
    panel.hidden = panel.dataset.code !== which;
  }
}

// Mismo documento que arma el servidor en site_public.go, del lado del
// cliente y sin publicar nada.
function renderPreview() {
  const html = $('sw-code-html').value;
  const css = $('sw-code-css').value;
  const js = $('sw-code-js').value;
  const t = JSON.stringify(tenantID() || '');

  $('sw-preview').srcdoc = `<!DOCTYPE html><html><head><meta charset="utf-8">`
    + `<style>${css}</style></head><body>${html}`
    + `<script>window.FASTERP_TENANT = ${t};</script>`
    + `<script>${js}<\/script></body></html>`;
}

// ========================================
// Medios
// ========================================

async function loadMedia() {
  const box = $('sw-media');
  try {
    const response = await fetch('/api/sitio_web/media?limit=100&order_by=created_at&order_dir=desc', {
      headers: fastHeaders(),
    });
    if (!response.ok) throw new Error(await errorText(response));
    const body = await response.json();
    renderMedia(body.data || []);
  } catch (error) {
    box.innerHTML = `<div class="sw-empty sw-error">${esc(error.message)}</div>`;
  }
}

function renderMedia(items) {
  const box = $('sw-media');
  if (!items.length) {
    box.innerHTML = '<div class="sw-empty">Todavía no subiste nada.</div>';
    return;
  }

  box.innerHTML = items.map((m) => {
    const isImage = (m.mime_type || '').startsWith('image/');
    const preview = isImage
      ? `<img src="${esc(m.url)}" alt="${esc(m.alt || m.filename || '')}" loading="lazy">`
      : `<div class="sw-media-file">📄<br>${esc(m.filename || '')}</div>`;
    return `
      <div class="sw-media-item" data-id="${esc(m.id)}" data-url="${esc(m.url)}">
        ${preview}
        <div class="sw-media-actions">
          <button type="button" class="sw-media-copy" title="Copiar dirección">🔗</button>
          <button type="button" class="sw-media-delete" title="Borrar">&times;</button>
        </div>
      </div>`;
  }).join('');

  for (const item of box.querySelectorAll('.sw-media-item')) {
    const id = item.dataset.id;
    const url = item.dataset.url;
    item.querySelector('.sw-media-copy').addEventListener('click', (e) => copyMediaURL(url, e.currentTarget));
    item.querySelector('.sw-media-delete').addEventListener('click', () => deleteMedia(id));
  }
}

function copyMediaURL(url, button) {
  navigator.clipboard?.writeText(url).catch(() => {});
  const original = button.textContent;
  button.textContent = '✓';
  setTimeout(() => { button.textContent = original; }, 1200);
}

async function deleteMedia(id) {
  if (!confirm('¿Borrar este archivo?')) return;
  try {
    const response = await fetch(`/api/sitio_web/media/${id}`, { method: 'DELETE', headers: fastHeaders() });
    if (!response.ok) throw new Error(await errorText(response));
    loadMedia();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

async function uploadMedia(file) {
  const btn = $('sw-media-upload');
  const original = btn.textContent;
  btn.disabled = true;
  btn.textContent = 'Subiendo…';

  try {
    const form = new FormData();
    form.append('file', file);
    const uploadResponse = await fetch('/api/_uploads?kind=media', { method: 'POST', body: form });
    if (!uploadResponse.ok) throw new Error(await errorText(uploadResponse));
    const { url } = await uploadResponse.json();

    const createResponse = await fetch('/api/sitio_web/media', {
      method: 'POST',
      headers: fastHeaders(),
      body: JSON.stringify({
        filename: file.name, url, mime_type: file.type || '', size_bytes: file.size || 0, alt: '',
      }),
    });
    if (!createResponse.ok) throw new Error(await errorText(createResponse));

    loadMedia();
  } catch (error) {
    alert('Error: ' + error.message);
  } finally {
    btn.disabled = false;
    btn.textContent = original;
  }
}

// ========================================
// Arranque
// ========================================

function init() {
  renderTemplates();
  loadPages();
  loadMedia();
  showStart();

  $('sw-new-page').addEventListener('click', showStart);
  $('sw-start-blank').addEventListener('click', startBlank);
  $('sw-start-generate').addEventListener('click', () =>
    generateWithAI($('sw-start-prompt'), $('sw-start-generate'), $('sw-start-error'), true));

  $('sw-save-page').addEventListener('click', savePage);
  $('sw-delete-page').addEventListener('click', deletePage);
  $('sw-slug').addEventListener('input', updatePublicLink);
  $('sw-is-home').addEventListener('change', updatePublicLink);
  $('sw-ai-generate').addEventListener('click', () =>
    generateWithAI($('sw-ai-prompt'), $('sw-ai-generate'), $('sw-editor-error'), false));

  // La vista previa se actualiza mientras se edita el código.
  for (const id of ['sw-code-html', 'sw-code-css', 'sw-code-js']) {
    $(id).addEventListener('input', renderPreview);
  }
  for (const btn of document.querySelectorAll('.sw-code-tab')) {
    btn.addEventListener('click', () => switchCode(btn.dataset.code));
  }

  $('sw-media-upload').addEventListener('click', () => $('sw-media-input').click());
  $('sw-media-input').addEventListener('change', (e) => {
    const file = e.target.files[0];
    if (file) uploadMedia(file);
    e.target.value = '';
  });
}

init();
