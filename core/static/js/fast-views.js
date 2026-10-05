/**
 * fast-views — motor de vistas del core.
 *
 * Un módulo no escribe UI de listado. Declara su esquema en manifest.json y
 * pone en su página:
 *
 *   <div data-fast-view data-model="contacts/contact"></div>
 *   <script src="/static/js/fast-views.js"></script>
 *
 * El motor pide la metadata a /api/{módulo}/{modelo}/_meta (columnas, tipos,
 * etiquetas y opciones, todo deducido del manifest) y los datos a
 * /api/{módulo}/{modelo}. De ahí arma la tabla, la paginación, la búsqueda
 * global y la búsqueda por columna.
 *
 * El alta y la edición también salen del manifest: el formulario se arma con
 * todos los campos editables, con el control que corresponde a cada tipo.
 *
 * Si el manifest declara "workflow", cada fila muestra los botones de las
 * transiciones legales desde SU estado actual — no hace falta pedirle nada
 * extra al servidor: la metadata ya trae el grafo completo y el registro ya
 * trae su estado, el cliente sólo cruza los dos.
 *
 * Eventos que emite el contenedor, para que el módulo reaccione si quiere:
 *   fast:change      — la vista se recargó         (detail: { page })
 *   fast:saved       — se creó o editó un registro (detail: { id, record, creating })
 *   fast:transitioned — se aplicó una transición    (detail: { id, action, from, to })
 */
(() => {
  'use strict';

  const SEARCH_DEBOUNCE_MS = 300;

  // Páginas que se pintan a cada lado de la actual. El primero y el último
  // siempre están, así que esto solo decide cuántas intermedias se ven.
  const PAGE_WINDOW = 3;

  function esc(value) {
    return String(value ?? '').replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  function debounce(fn, ms) {
    let timer;
    return (...args) => {
      clearTimeout(timer);
      timer = setTimeout(() => fn(...args), ms);
    };
  }

  /**
   * Convierte lo que escribió la persona al tipo que espera la API.
   * Un <input> siempre entrega texto; el manifest dice qué es en realidad.
   */
  function coerce(raw, field) {
    if (field.input === 'checkbox') return Boolean(raw);
    if (raw === '' || raw === null || raw === undefined) return null;
    if (field.input === 'number') {
      const n = Number(raw);
      return Number.isNaN(n) ? null : n;
    }
    return raw;
  }

  /**
   * Sistema de Notificaciones Toast (Estilo PrimeNG)
   */
  function showToast({ severity = 'info', summary = '', detail = '', life = 4500 }) {
    let container = document.querySelector('.fv-toast-container');
    if (!container) {
      container = document.createElement('div');
      container.className = 'fv-toast-container';
      document.body.appendChild(container);
    }

    const toast = document.createElement('div');
    toast.className = `fv-toast fv-toast-${severity}`;

    const icons = {
      success: '✓',
      info: 'ℹ',
      warn: '⚠️',
      error: '✕'
    };

    toast.innerHTML = `
      <div class="fv-toast-icon">${icons[severity] || 'ℹ'}</div>
      <div class="fv-toast-content">
        <div class="fv-toast-summary">${esc(summary)}</div>
        ${detail ? `<div class="fv-toast-detail">${esc(detail)}</div>` : ''}
      </div>
      <button class="fv-toast-close" type="button" title="Cerrar">&times;</button>
    `;

    container.appendChild(toast);

    const closeBtn = toast.querySelector('.fv-toast-close');
    const dismiss = () => {
      if (toast.classList.contains('fv-toast-fade-out')) return;
      toast.classList.add('fv-toast-fade-out');
      setTimeout(() => toast.remove(), 300);
    };

    closeBtn.addEventListener('click', dismiss);
    if (life > 0) setTimeout(dismiss, life);
  }
  if (typeof window !== 'undefined') {
    window.showToast = showToast;
  }

  /** Formatea un valor según el tipo que declaró el manifest, con badges estilo PrimeNG para estados. */
  function formatValue(value, field) {
    if (value === null || value === undefined || value === '') return '—';

    // PrimeNG Status Badges para estados y enums
    const isStatusField = ['estado', 'status', 'state', 'tipo_persona', 'tipo', 'active', 'installed'].includes(field.name) || field.options?.length;
    if (isStatusField && typeof value === 'string') {
      const valLower = value.toLowerCase();
      let badgeClass = 'p-badge-info';
      if (['activo', 'installed', 'true', 'done', 'completado', 'confirmado', 'pagado', 'aprobado', 'jurídica', 'juridica'].includes(valLower)) {
        badgeClass = 'p-badge-success';
      } else if (['prospecto', 'pendiente', 'borrador', 'draft', 'en_proceso', 'natural'].includes(valLower)) {
        badgeClass = 'p-badge-warn';
      } else if (['inactivo', 'cancelado', 'rechazado', 'error', 'false'].includes(valLower)) {
        badgeClass = 'p-badge-danger';
      } else if (['sistema', 'nota', 'info'].includes(valLower)) {
        badgeClass = 'p-badge-secondary';
      }
      return `<span class="p-badge ${badgeClass}">${esc(value)}</span>`;
    }

    switch (field.input) {
      case 'checkbox':
        return value
          ? '<span class="p-badge p-badge-success">Sí</span>'
          : '<span class="p-badge p-badge-secondary">No</span>';
      case 'number':
        return typeof value === 'number' ? value.toLocaleString('es-EC') : value;
      case 'date':
        return new Date(value).toLocaleDateString('es-EC');
      case 'datetime-local':
        return new Date(value).toLocaleString('es-EC');
      default:
        return value;
    }
  }

  class FastView {
    constructor(container) {
      this.el = container;
      const [module, model] = (container.dataset.model || '').split('/');
      this.module = module;
      this.model = model;
      this.base = `/api/${module}/${model}`;

      this.meta = null;
      this.state = {
        page: 1,
        // Se ajusta a page_sizes al leer la metadata: un limit que el servidor
        // no tiene en su lista blanca se clampea en silencio y el selector
        // aparece sin nada marcado.
        limit: 10,
        search: '',
        filters: {},   // campo → texto escrito en el encabezado
        orderBy: '',
        orderDir: 'asc',
      };

      // Ids marcados en la tabla. Vive fuera de state porque no viaja en la
      // query: es una decisión del cliente, no un filtro del listado.
      this.selection = new Set();
      // true = "todos los que cumplen el filtro", en todas las páginas. Es un
      // estado aparte del Set porque los ids de las páginas que no están en
      // pantalla no hay cómo conocerlos sin traerlos todos.
      this.selectAllMatching = false;
    }

    /**
     * Cliente HTTP de las peticiones de datos.
     *
     * Antes este objeto montaba sus propias cabeceras y solo con X-Tenant-ID,
     * sin Authorization. Funcionaba contra la capa legacy, que identificaba al
     * usuario por cookie de sesión, pero contra Gin —que solo acepta Bearer— cada
     * listado devolvía 401. Delegar en FastClient además trae el refresco del
     * token, que aquí no existía: al cabo de 15 minutos el CRUD se paraba hasta
     * recargar la página.
     */
    get api() {
      if (typeof FastClient === 'undefined') {
        throw new Error('fast-client.js no está cargado: el CRUD necesita el cliente de auth.');
      }
      return FastClient;
    }

    async init() {
      if (!this.module || !this.model) {
        return this.fail('Falta data-model="modulo/modelo" en el contenedor.');
      }

      try {
        this.meta = await this.api.get(`${this.base}/_meta`);
      } catch (error) {
        return this.fail(`No se pudo leer el modelo: ${error.message}`);
      }

      // El tamaño de página lo fija el servidor: se adopta el suyo para no pedir un
      // limit que él tenga que redondear.
      const sizes = this.pageSizes();
      if (sizes.length && !sizes.includes(this.state.limit)) {
        this.state.limit = sizes[0];
      }

      // El orden inicial es la primera columna: lo más predecible para quien mira.
      this.state.orderBy = this.columns()[0]?.name || '';
      this.renderChrome();
      this.load();
    }

    /** Tamaños de página que el servidor acepta, de la metadata del modelo. */
    pageSizes() {
      return this.meta?.page_sizes?.length ? this.meta.page_sizes : [10, 25, 50, 100];
    }

    /** Columnas a mostrar, resueltas contra la metadata. */
    columns() {
      const wanted = this.meta.views?.list?.columns || [];
      const byName = new Map(this.meta.fields.map((f) => [f.name, f]));
      return wanted.map((name) => byName.get(name)).filter(Boolean);
    }

    /** Estructura fija de la vista; sólo el cuerpo de la tabla se re-renderiza. */
    renderChrome() {
      const sizes = this.pageSizes();

      this.el.innerHTML = `
        <div class="fv">
          <div class="fv-toolbar">
            <input type="search" class="fv-search" placeholder="Buscar en ${esc(this.meta.label)}…">
            <label class="fv-size">
              Mostrar
              <select>${sizes.map((s) => `<option${s === this.state.limit ? ' selected' : ''}>${s}</option>`).join('')}</select>
            </label>
            <div class="fv-toolbar-actions">
              <button class="fv-btn-secondary fv-import-btn" type="button" title="Importar datos masivos">📥 Importar</button>
              <button class="fv-btn-secondary fv-export-btn" type="button" title="Exportar datos">📤 Exportar</button>
              <button class="fv-new" type="button">+ Nuevo</button>
            </div>
          </div>
          <div class="fv-selbar" hidden>
            <span class="fv-selinfo"></span>
            <button type="button" class="fv-clear-sel">Quitar selección</button>
          </div>
          <div class="fv-scroll">
            <table class="fv-table">
              <thead>
                <tr class="fv-labels">
                  <th class="fv-select-col">
                    <input type="checkbox" class="fv-check-all"
                      title="Seleccionar todo lo de esta página" aria-label="Seleccionar todo lo de esta página">
                  </th>
                  ${this.columns().map((f) => `
                    <th data-sort="${esc(f.name)}" title="Ordenar por ${esc(f.label)}">
                      <span>${esc(f.label)}</span><i class="fv-arrow"></i>
                    </th>`).join('')}
                  <th class="fv-actions-col"></th>
                </tr>
                <tr class="fv-filters">
                  <th></th>
                  ${this.columns().map((f) => `
                    <th>${f.options?.length
                        ? `<select data-filter="${esc(f.name)}">
                             <option value="">Todos</option>
                             ${f.options.map((o) => `<option>${esc(o)}</option>`).join('')}
                           </select>`
                        : `<input data-filter="${esc(f.name)}" placeholder="filtrar…">`
                      }</th>`).join('')}
                  <th></th>
                </tr>
              </thead>
              <tbody><tr><td class="fv-empty" colspan="99">Cargando…</td></tr></tbody>
            </table>
          </div>
          <div class="fv-footer">
            <span class="fv-info"></span>
            <div class="fv-pager">
              <button class="fv-step" data-step="-1" type="button"
                title="Página anterior" aria-label="Página anterior">‹</button>
              <span class="fv-pages"></span>
              <button class="fv-step" data-step="1" type="button"
                title="Página siguiente" aria-label="Página siguiente">›</button>
            </div>
          </div>

          <!-- Diálogo Formulario CRUD -->
          <dialog class="fv-dialog fv-crud-dialog">
            <form method="dialog" class="fv-form">
              <header>
                <h2 class="fv-form-title"></h2>
                <button type="button" class="fv-close" aria-label="Cerrar">&times;</button>
              </header>
              <p class="fv-form-error" hidden></p>
              <div class="fv-form-fields">${this.formFields()}</div>
              <div class="fv-chatter" hidden>
                <div class="fv-chatter-divider"></div>
                <div class="fv-chatter-header">
                  <span class="fv-chatter-title">💬 Historial</span>
                  <span class="fv-chatter-record"></span>
                </div>
                <div class="fv-chatter-container"></div>
              </div>
              <footer>
                <button type="button" class="fv-cancel">Cancelar</button>
                <button type="button" class="fv-save">Guardar</button>
              </footer>
            </form>
          </dialog>

          <!-- Diálogo Exportación Masiva -->
          <dialog class="fv-dialog fv-export-dialog">
            <form method="dialog" class="fv-form">
              <header>
                <h2 class="fv-form-title">Exportar ${esc(this.meta.label)}</h2>
                <button type="button" class="fv-close fv-export-close" aria-label="Cerrar">&times;</button>
              </header>
              <div class="fv-form-fields">
                <p class="fv-export-scope"></p>
                <div class="fv-field fv-wide">
                  <label>Formato de Exportación</label>
                  <select class="fv-export-format">
                    <option value="xlsx">Excel (.xlsx)</option>
                    <option value="csv">CSV (.csv)</option>
                  </select>
                </div>
                <div class="fv-field fv-wide">
                  <label>Campos a incluir</label>
                  <div class="fv-export-cols-box">
                    <label class="fv-col-choice">
                      <input type="checkbox" class="fv-export-all-cb" checked> <strong>Todas las columnas</strong>
                    </label>
                    <div class="fv-export-cols-list">
                      ${this.meta.fields.map(f => `
                        <label class="fv-col-choice">
                          <input type="checkbox" value="${esc(f.name)}" class="fv-export-col-cb" checked> ${esc(f.label || f.name)}
                        </label>
                      `).join('')}
                    </div>
                  </div>
                </div>
              </div>
              <footer>
                <button type="button" class="fv-cancel fv-export-cancel">Cancelar</button>
                <button type="button" class="fv-save fv-export-do">📤 Descargar</button>
              </footer>
            </form>
          </dialog>

          <!-- Diálogo Importación Masiva -->
          <dialog class="fv-dialog fv-import-dialog">
            <form method="dialog" class="fv-form">
              <header>
                <h2 class="fv-form-title">Importar ${esc(this.meta.label)}</h2>
                <button type="button" class="fv-close fv-import-close" aria-label="Cerrar">&times;</button>
              </header>
              <div class="fv-form-fields">
                <div class="fv-field fv-wide">
                  <label>1. Obtener Plantilla de Ejemplo</label>
                  <div class="fv-template-actions">
                    <button type="button" class="fv-btn-secondary fv-template-xlsx">📄 Plantilla Excel (.xlsx)</button>
                    <button type="button" class="fv-btn-secondary fv-template-csv">📄 Plantilla CSV (.csv)</button>
                  </div>
                  <small>Descarga un archivo con las cabeceras exactas del modelo para llenarlo con tus datos.</small>
                </div>
                <div class="fv-field fv-wide">
                  <label>2. Seleccionar Archivo de Datos (.xlsx o .csv)</label>
                  <input type="file" class="fv-import-file-input" accept=".xlsx,.csv">
                  <small>Valida primero el archivo: "Importar Registros" sólo se habilita cuando no hay errores.</small>
                </div>
                <div class="fv-field fv-wide fv-import-status-box" hidden>
                  <div class="fv-import-status-msg"></div>
                  <div class="fv-import-warn" hidden></div>
                  <div class="fv-import-errors-wrap" hidden>
                    <table class="fv-import-errors-table">
                      <thead>
                        <tr><th>Fila</th><th>Columna</th><th>Mensaje de Error</th></tr>
                      </thead>
                      <tbody></tbody>
                    </table>
                  </div>
                </div>
              </div>
              <footer>
                <button type="button" class="fv-cancel fv-import-cancel">Cancelar</button>
                <button type="button" class="fv-btn-secondary fv-import-validate-btn">🔍 Validar Archivo</button>
                <button type="button" class="fv-save fv-import-submit-btn" disabled>📥 Importar Registros</button>
              </footer>
            </form>
          </dialog>
        </div>`;

      this.body = this.el.querySelector('tbody');
      this.dialog = this.el.querySelector('.fv-crud-dialog');
      this.exportDialog = this.el.querySelector('.fv-export-dialog');
      this.importDialog = this.el.querySelector('.fv-import-dialog');
      this.bind();
    }

    /** Campos del formulario: los que el manifest declaró editables. */
    formFields() {
      const wanted = this.meta.views?.form?.fields || [];
      const byName = new Map(this.meta.fields.map((f) => [f.name, f]));

      return wanted
        .map((name) => byName.get(name))
        .filter(Boolean)
        .map((f) => `
          <div class="fv-field${f.input === 'textarea' ? ' fv-wide' : ''}">
            <label for="fv-in-${esc(f.name)}">
              ${esc(f.label)}${f.required ? ' <span class="fv-req">*</span>' : ''}
            </label>
            ${this.control(f)}
            ${f.help ? `<small>${esc(f.help)}</small>` : ''}
          </div>`).join('');
    }

    /** El control HTML lo decide el tipo declarado, no el módulo. */
    control(f) {
      const id = `fv-in-${esc(f.name)}`;
      const common = `id="${id}" name="${esc(f.name)}" data-field="${esc(f.name)}"`
        + (f.required ? ' required' : '');

      if (f.input === 'select') {
        return `<select ${common}>
            <option value="">—</option>
            ${(f.options || []).map((o) => `<option>${esc(o)}</option>`).join('')}
          </select>`;
      }
      if (f.input === 'textarea') {
        return `<textarea ${common} rows="3"${f.max_length ? ` maxlength="${f.max_length}"` : ''}></textarea>`;
      }
      if (f.input === 'checkbox') {
        return `<input type="checkbox" ${common}>`;
      }

      const step = f.type === 'money' || f.type === 'decimal' ? ' step="0.01"' : '';
      return `<input type="${esc(f.input)}" ${common}`
        + `${f.max_length ? ` maxlength="${f.max_length}"` : ''}${step}>`;
    }

    bind() {
      // bind() vuelve a ejecutarse si el módulo re-renderiza la vista: sin
      // este remove, cada pasada añade otro keydown y las flechas saltan dos
      // páginas por pulsación.
      if (this.onKeydown) window.removeEventListener('keydown', this.onKeydown);

      const reload = (reset = true) => {
        if (reset) this.state.page = 1;
        // La selección es de una página: si cambian los filtros, lo marcado
        // deja de ser lo que la persona cree que está marcando.
        this.clearSelection();
        this.load();
      };

      this.el.querySelector('.fv-search').addEventListener('input', debounce((e) => {
        this.state.search = e.target.value.trim();
        reload();
      }, SEARCH_DEBOUNCE_MS));

      this.el.querySelector('.fv-size select').addEventListener('change', (e) => {
        this.state.limit = Number(e.target.value);
        reload();
      });

      // Búsqueda por columna: texto usa "contiene", los enums igualdad exacta.
      for (const input of this.el.querySelectorAll('[data-filter]')) {
        const handler = () => {
          const value = input.value.trim();
          if (value) this.state.filters[input.dataset.filter] = value;
          else delete this.state.filters[input.dataset.filter];
          reload();
        };
        input.addEventListener(
          input.tagName === 'SELECT' ? 'change' : 'input',
          input.tagName === 'SELECT' ? handler : debounce(handler, SEARCH_DEBOUNCE_MS),
        );
      }

      for (const th of this.el.querySelectorAll('[data-sort]')) {
        th.addEventListener('click', () => {
          const field = th.dataset.sort;
          if (this.state.orderBy === field) {
            this.state.orderDir = this.state.orderDir === 'asc' ? 'desc' : 'asc';
          } else {
            this.state.orderBy = field;
            this.state.orderDir = 'asc';
          }
          reload();
        });
      }

      // Los números de página se repintan en cada carga, así que no se enlaza cada
      // botón: se delega en el contenedor, que sobrevive al innerHTML.
      this.el.querySelector('.fv-pages').addEventListener('click', (e) => {
        const btn = e.target.closest('[data-goto]');
        if (btn) this.goToPage(Number(btn.dataset.goto));
      });

      // Selección para exportar. Las casillas de fila también se repintan en
      // cada carga, así que van delegadas en el cuerpo.
      this.body.addEventListener('change', (e) => {
        const cb = e.target.closest('[data-select]');
        if (!cb) return;
        const want = cb.checked;
        if (this.selectAllMatching) {
          // Veníamos de "todo el filtro": al desmarcar una fila se fija lo que
          // queda marcado y se abandona el modo, en vez de desmarcarla sola.
          this.selectAllMatching = false;
          for (const row of this.body.querySelectorAll('tr[data-id]')) {
            this.selection.add(row.dataset.id);
          }
        }
        if (want) this.selection.add(cb.dataset.select);
        else this.selection.delete(cb.dataset.select);
        this.syncSelectionUI();
      });

      this.el.querySelector('.fv-check-all').addEventListener('change', (e) => {
        const want = e.target.checked;
        if (this.selectAllMatching) {
          // La cabecera estaba marcada entera: desmarcarla quita todo.
          this.selectAllMatching = false;
          this.selection.clear();
        } else if (want) {
          // "Todos" son todos los que cumplen el filtro, no los de esta página.
          // No hace falta traer sus ids: el export sin ids ya significa "todo
          // lo que filtra", así que no se topa con el límite de ids.
          this.selectAllMatching = true;
          this.selection.clear();
        } else {
          for (const row of this.body.querySelectorAll('tr[data-id]')) {
            this.selection.delete(row.dataset.id);
          }
        }
        this.syncSelectionUI();
      });

      this.el.querySelector('.fv-clear-sel').addEventListener('click', () => this.clearSelection());

      // Las flechas son fijas en el DOM, así que basta con enlazarlas una vez.
      for (const btn of this.el.querySelectorAll('[data-step]')) {
        btn.addEventListener('click', () => {
          this.goToPage(this.state.page + Number(btn.dataset.step));
        });
      }

      // Flechas ← → para recorrer páginas sin volver el ratón al pie. Se
      // atienden en la ventana para no secuestrar las teclas de los diálogos.
      this.onKeydown = (e) => {
        if (this.importDialog?.open || this.exportDialog?.open || this.dialog?.open) return;
        const tag = document.activeElement?.tagName;
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
        if (e.key === 'ArrowLeft') this.goToPage(this.state.page - 1);
        else if (e.key === 'ArrowRight') this.goToPage(this.state.page + 1);
      };
      window.addEventListener('keydown', this.onKeydown);

      this.el.querySelector('.fv-new').addEventListener('click', () => this.openForm());
      this.el.querySelector('.fv-save').addEventListener('click', () => this.save());

      for (const selector of ['.fv-close', '.fv-cancel']) {
        this.el.querySelectorAll(selector).forEach((btn) => {
          btn.addEventListener('click', () => {
            this.dialog?.close();
            this.exportDialog?.close();
            this.importDialog?.close();
          });
        });
      }
      // Clic en el fondo del diálogo también cierra.
      [this.dialog, this.exportDialog, this.importDialog].forEach((d) => {
        d?.addEventListener('click', (e) => {
          if (e.target === d) d.close();
        });
      });

      // --- Modales de Importación / Exportación ---
      const importBtn = this.el.querySelector('.fv-import-btn');
      const exportBtn = this.el.querySelector('.fv-export-btn');
      if (exportBtn) exportBtn.addEventListener('click', () => this.openExportModal());
      if (importBtn) importBtn.addEventListener('click', () => this.openImportModal());

      // Exportar
      const exportAllCb = this.el.querySelector('.fv-export-all-cb');
      if (exportAllCb) {
        exportAllCb.addEventListener('change', (e) => {
          this.el.querySelectorAll('.fv-export-col-cb').forEach((cb) => {
            cb.checked = e.target.checked;
          });
        });
      }
      const exportDoBtn = this.el.querySelector('.fv-export-do');
      if (exportDoBtn) {
        exportDoBtn.addEventListener('click', () => this.triggerExport());
      }

      // Plantillas
      const tmplXlsx = this.el.querySelector('.fv-template-xlsx');
      const tmplCsv = this.el.querySelector('.fv-template-csv');
      if (tmplXlsx) tmplXlsx.addEventListener('click', () => this.downloadExport('xlsx', [], true));
      if (tmplCsv) tmplCsv.addEventListener('click', () => this.downloadExport('csv', [], true));

      // Importar
      const importValidateBtn = this.el.querySelector('.fv-import-validate-btn');
      const importSubmitBtn = this.el.querySelector('.fv-import-submit-btn');
      if (importValidateBtn) importValidateBtn.addEventListener('click', () => this.runImport(true));
      if (importSubmitBtn) importSubmitBtn.addEventListener('click', () => this.runImport(false));

      // Cambiar de archivo invalida la validación anterior: hay que volver a validar.
      const importFileInput = this.el.querySelector('.fv-import-file-input');
      if (importFileInput) {
        importFileInput.addEventListener('change', () => {
          this.importValidated = false;
          if (importSubmitBtn) importSubmitBtn.disabled = true;
          const box = this.importDialog.querySelector('.fv-import-status-box');
          if (box) box.hidden = true;
        });
      }
    }

    openExportModal() {
      if (!this.exportDialog) return;
      // Sin esto el diálogo no deja claro si se baja la selección o todo el
      // filtro, y el archivo sale con más filas de las que la persona marca.
      const note = this.exportDialog.querySelector('.fv-export-scope');
      const n = this.selection.size;
      const total = this.page?.pagination?.total ?? this.page?.total ?? null;
      let text;
      if (this.selectAllMatching) {
        text = total != null
          ? `Se exportarán los ${total} registros que cumplen el filtro, en todas las páginas.`
          : 'Se exportarán todos los registros que cumplen el filtro, en todas las páginas.';
      } else if (n > 0) {
        text = `Se exportarán solo los ${n} registro(s) que marcaste.`;
      } else {
        text = 'No hay selección: se exportará todo lo que cumple el filtro actual.';
      }
      note.textContent = text;
      note.className = `fv-export-scope${n > 0 || this.selectAllMatching ? ' is-scope-selection' : ''}`;
      this.exportDialog.showModal();
    }

    openImportModal() {
      if (!this.importDialog) return;
      const statusBox = this.importDialog.querySelector('.fv-import-status-box');
      if (statusBox) statusBox.hidden = true;
      const errorsWrap = this.importDialog.querySelector('.fv-import-errors-wrap');
      if (errorsWrap) errorsWrap.hidden = true;
      const fileInput = this.importDialog.querySelector('.fv-import-file-input');
      if (fileInput) fileInput.value = '';
      // Importar exige validar antes: si no, se reabre con el botón muerto.
      const submitBtn = this.importDialog.querySelector('.fv-import-submit-btn');
      if (submitBtn) submitBtn.disabled = true;
      this.importValidated = false;
      this.importDialog.showModal();
    }

    async triggerExport() {
      const format = this.exportDialog.querySelector('.fv-export-format').value;
      const allChecked = this.exportDialog.querySelector('.fv-export-all-cb').checked;
      const selectedFields = [];
      if (!allChecked) {
        this.exportDialog.querySelectorAll('.fv-export-col-cb:checked').forEach((cb) => {
          selectedFields.push(cb.value);
        });
      }
      const submitBtn = this.exportDialog.querySelector('.fv-export-do');
      submitBtn.disabled = true;
      try {
        await this.downloadExport(format, selectedFields, false);
        this.exportDialog.close();
      } catch (err) {
        showToast({ severity: 'error', summary: 'Error al exportar', detail: err.message });
      } finally {
        submitBtn.disabled = false;
      }
    }

    async downloadExport(format, fields = [], isTemplate = false) {
      const params = isTemplate ? new URLSearchParams({ format, limit: '0' }) : this.exportQuery();
      params.set('format', format);
      if (fields.length) params.set('fields', fields.join(','));

      // Con registros marcados, el export se limita a esos. En modo "todo el
      // filtro" no se manda ids: el servidor exporta lo que filtra, que ya
      // son todos. La plantilla nunca lleva ids: sale con cabeceras y nada más.
      const only = (isTemplate || this.selectAllMatching) ? [] : [...this.selection];
      if (only.length) params.set('ids', only.join(','));

      const url = `${this.base}/export?${params.toString()}`;
      const clientToken = window.FastClient ? window.FastClient.accessToken() : null;
      const clientTenant = window.FastClient ? window.FastClient.tenant() : null;
      const token = clientToken || localStorage.getItem('access_token') || sessionStorage.getItem('access_token');
      const tenant = clientTenant || localStorage.getItem('tenant_id') || 'default';
      const headers = { 'X-Tenant-ID': tenant };
      if (token) headers['Authorization'] = `Bearer ${token}`;

      const res = await fetch(url, { headers });
      if (!res.ok) {
        const json = await res.json().catch(() => ({ error: 'Error al exportar' }));
        throw new Error(json.error || 'No se pudo descargar el archivo');
      }
      const blob = await res.blob();
      const downloadUrl = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = downloadUrl;
      const prefix = isTemplate ? `plantilla_${this.model}` : `${this.model}_export`;
      a.download = `${prefix}.${format}`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.URL.revokeObjectURL(downloadUrl);

      if (isTemplate) {
        showToast({
          severity: 'info',
          summary: 'Plantilla descargada',
          detail: `Plantilla de ${this.meta.label} en formato .${format.toUpperCase()} descargada.`,
        });
      } else {
        showToast({
          severity: 'success',
          summary: 'Exportación completada',
          detail: only.length
            ? `${only.length} registro(s) seleccionados de ${this.meta.label} guardados como .${format.toUpperCase()}.`
            : `Datos de ${this.meta.label} guardados como .${format.toUpperCase()}.`,
        });
      }
    }

    /**
     * Importa el archivo seleccionado.
     *
     * `validateOnly` distingue los dos botones del diálogo y ya no se consulta
     * ningún "modo de prueba": antes el checkbox venía marcado por defecto, así
     * que pulsar "Importar Registros" repetía la validación y no guardaba nada.
     * Importar sólo se habilita tras una validación limpia del mismo archivo.
     */
    async runImport(validateOnly) {
      const fileInput = this.importDialog.querySelector('.fv-import-file-input');
      const isDryRun = validateOnly === true;

      const file = fileInput?.files[0];
      if (!file) {
        showToast({
          severity: 'warn',
          summary: 'Archivo Requerido',
          detail: 'Por favor selecciona un archivo .xlsx o .csv para importar.',
        });
        return;
      }

      const statusBox = this.importDialog.querySelector('.fv-import-status-box');
      const statusMsg = this.importDialog.querySelector('.fv-import-status-msg');
      const warnBox = this.importDialog.querySelector('.fv-import-warn');
      const errorsWrap = this.importDialog.querySelector('.fv-import-errors-wrap');
      const errorsTbody = this.importDialog.querySelector('.fv-import-errors-table tbody');

      statusBox.hidden = false;
      statusMsg.className = 'fv-import-status-msg p-badge-info';
      statusMsg.textContent = isDryRun ? '⏳ Validando datos del archivo...' : '⏳ Procesando importación de datos...';
      errorsWrap.hidden = true;
      errorsTbody.innerHTML = '';
      warnBox.hidden = true;
      warnBox.innerHTML = '';

      const valBtn = this.importDialog.querySelector('.fv-import-validate-btn');
      const submitBtn = this.importDialog.querySelector('.fv-import-submit-btn');
      valBtn.disabled = true;
      submitBtn.disabled = true;

      try {
        const formData = new FormData();
        formData.append('file', file);

        const clientToken = window.FastClient ? window.FastClient.accessToken() : null;
        const clientTenant = window.FastClient ? window.FastClient.tenant() : null;
        const token = clientToken || localStorage.getItem('access_token') || sessionStorage.getItem('access_token');
        const tenant = clientTenant || localStorage.getItem('tenant_id') || 'default';
        const headers = { 'X-Tenant-ID': tenant };
        if (token) headers['Authorization'] = `Bearer ${token}`;

        const url = `${this.base}/import?dry_run=${isDryRun}`;
        const res = await fetch(url, { method: 'POST', headers, body: formData });
        const data = await res.json().catch(() => ({ error: 'Respuesta inválida del servidor' }));

        // Cabeceras que no corresponden a ningún campo: sus valores se
        // descartarían al guardar. Se avisa antes de que sea una sorpresa.
        if (data.unmatched_columns && data.unmatched_columns.length) {
          warnBox.hidden = false;
          warnBox.innerHTML = `⚠️ ${data.unmatched_columns.length} columna(s) del archivo no coinciden con ningún campo del modelo y se ignorarán: <strong>${esc(data.unmatched_columns.join(', '))}</strong>. Descarga la plantilla y usa sus cabeceras exactas.`;
        }

        if (data.errors && data.errors.length) {
          this.importValidated = false;
          statusMsg.className = 'fv-import-status-msg p-badge-danger';
          statusMsg.textContent = `❌ Se encontraron ${data.errors.length} error(es) en el archivo de importación.`;
          errorsWrap.hidden = false;
          errorsTbody.innerHTML = data.errors.map((e) => `
            <tr>
              <td>Fila ${esc(e.row)}</td>
              <td><strong>${esc(e.column || e.field)}</strong></td>
              <td class="fv-error">${esc(e.error)}</td>
            </tr>
          `).join('');

          showToast({
            severity: 'error',
            summary: 'Errores en archivo',
            detail: `Se detectaron ${data.errors.length} errores. Revisa la lista en el modal.`,
          });
        } else if (res.ok && data.success) {
          statusMsg.className = 'fv-import-status-msg p-badge-success';
          if (data.dry_run) {
            this.importValidated = true;
            statusMsg.textContent = `✅ Validación exitosa: Los ${data.total_rows} registro(s) son válidos. Pulsa "Importar Registros" para guardarlos.`;
            showToast({
              severity: 'success',
              summary: 'Validación Exitosa',
              detail: `${data.total_rows} registros válidos. Ya puedes importarlos.`,
            });
          } else {
            statusMsg.textContent = `🎉 Importación completada: Se crearon ${data.created_count} registro(s) exitosamente.`;
            showToast({
              severity: 'success',
              summary: 'Importación Completada',
              detail: `Se crearon ${data.created_count} registro(s) correctamente.`,
            });
            this.importValidated = false;
            setTimeout(async () => {
              this.importDialog.close();
              // Los registros importados caen en la primera página: recargar sin
              // filtros ni búsqueda para que se vean de verdad.
              await this.resetList();
              this.el.dispatchEvent(new CustomEvent('fast:saved', {
                bubbles: true,
                detail: { imported: data.created_count, creating: true },
              }));
            }, 1400);
          }
        } else {
          this.importValidated = false;
          statusMsg.className = 'fv-import-status-msg p-badge-danger';
          statusMsg.textContent = `❌ Error: ${data.error || 'No se pudo procesar el archivo.'}`;
          showToast({
            severity: 'error',
            summary: 'Error de Importación',
            detail: data.error || 'No se pudo procesar el archivo.',
          });
        }
      } catch (err) {
        this.importValidated = false;
        statusMsg.className = 'fv-import-status-msg p-badge-danger';
        statusMsg.textContent = `❌ Error: ${err.message}`;
        showToast({
          severity: 'error',
          summary: 'Error en Servidor',
          detail: err.message,
        });
      } finally {
        valBtn.disabled = false;
        submitBtn.disabled = !this.importValidated;
      }
    }

    /** Abre el formulario vacío (alta) o con los datos del registro (edición). */
    openForm(record = null) {
      this.editing = record?.id || null;

      this.el.querySelector('.fv-form-title').textContent =
        `${this.editing ? 'Editar' : 'Nuevo'} ${this.meta.label.toLowerCase()}`;

      const error = this.el.querySelector('.fv-form-error');
      error.hidden = true;
      error.textContent = '';

      const byName = new Map(this.meta.fields.map((f) => [f.name, f]));
      for (const input of this.el.querySelectorAll('[data-field]')) {
        const field = byName.get(input.dataset.field);
        const value = record?.[input.dataset.field];

        if (field.input === 'checkbox') {
          input.checked = Boolean(value);
        } else if (field.input === 'datetime-local' && value) {
          input.value = String(value).slice(0, 16); // la API devuelve ISO
        } else {
          input.value = value ?? '';
        }
      }

      // Chatter: mostrar solo al editar, ocultar al crear
      const chatterEl = this.el.querySelector('.fv-chatter');
      if (chatterEl) {
        if (this.editing) {
          chatterEl.hidden = false;
          this.loadChatter(record);
        } else {
          chatterEl.hidden = true;
          this.clearChatter();
        }
      }

      this.dialog.showModal();
      this.el.querySelector('[data-field]')?.focus();
    }

    /** Carga el ChatterWidget para el registro being edited. */
    loadChatter(record) {
      if (!window.ChatterWidget) {
        // Cargar dinámicamente el widget si no está disponible
        this._loadChatterScript().then(() => {
          if (window.ChatterWidget) this._renderChatter(record);
        });
        return;
      }
      this._renderChatter(record);
    }

    _renderChatter(record) {
      const container = this.el.querySelector('.fv-chatter-container');
      const recordLabel = this.el.querySelector('.fv-chatter-record');
      if (!container || !window.ChatterWidget) return;

      const recordName = record?.name || record?.Nombre || record?.titulo || '';
      if (recordLabel) recordLabel.textContent = recordName;

      const recordModel = `${this.module}/${this.model}`;
      window.ChatterWidget.render(container, {
        recordModel: recordModel,
        recordId: this.editing,
        showComposer: true,
        maxHeight: '350px',
      });
    }

    clearChatter() {
      const container = this.el.querySelector('.fv-chatter-container');
      if (container) container.innerHTML = '';
      if (window.ChatterWidget) {
        const recordLabel = this.el.querySelector('.fv-chatter-record');
        if (recordLabel) recordLabel.textContent = '';
      }
    }

    _loadChatterScript() {
      return new Promise((resolve) => {
        if (window.ChatterWidget) { resolve(); return; }
        if (document.querySelector('script[src="/static/js/chatter-widget.js"]')) {
          // Script tag exists but maybe not loaded yet
          const check = setInterval(() => {
            if (window.ChatterWidget) { clearInterval(check); resolve(); }
          }, 50);
          setTimeout(() => { clearInterval(check); resolve(); }, 3000);
          return;
        }
        const s = document.createElement('script');
        s.src = '/static/js/chatter-widget.js';
        s.onload = resolve;
        s.onerror = resolve; // no fallar si no está disponible
        document.body.appendChild(s);
      });
    }

    /**
     * Abre el registro para editar. El listado ya trae todos los campos del
     * manifest (no sólo las columnas visibles), así que no hace falta pedirlo
     * de nuevo; si por algo faltara, se relee con @fast.read().
     */
    async edit(id, record) {
      const complete = this.meta.views.form.fields.every((f) => f in (record || {}));
      if (complete) return this.openForm(record);

      try {
        this.openForm(await this.api.get(`${this.base}/${id}`));
      } catch (error) {
        alert('Error: ' + error.message);
      }
    }

    /** Recolecta el formulario y escribe con @fast.create() o @fast.update(). */
    async save() {
      const form = this.el.querySelector('.fv-form');
      if (!form.reportValidity()) return; // validación del navegador primero

      const byName = new Map(this.meta.fields.map((f) => [f.name, f]));
      const payload = {};
      for (const input of this.el.querySelectorAll('[data-field]')) {
        const field = byName.get(input.dataset.field);
        const raw = field.input === 'checkbox' ? input.checked : input.value.trim();
        const value = coerce(raw, field);

        // Al crear no se mandan los vacíos: así aplican los DEFAULT de la BD.
        if (value === null && !this.editing) continue;
        payload[input.dataset.field] = value;
      }

      const url = this.editing ? `${this.base}/${this.editing}` : this.base;
      const save = this.el.querySelector('.fv-save');
      save.disabled = true;

      try {
        const body = this.editing
          ? await this.api.put(url, payload)
          : await this.api.post(url, payload);

        this.dialog.close();
        // Tras crear, volver a la primera página: ahí aparece el registro nuevo.
        if (!this.editing) this.state.page = 1;
        this.load();
        this.el.dispatchEvent(new CustomEvent('fast:saved', {
          bubbles: true,
          detail: { id: body.id, record: payload, creating: !this.editing },
        }));
      } catch (error) {
        // El SDK explica qué falló ("el campo name es obligatorio"): se muestra
        // dentro del formulario, sin cerrarlo ni perder lo escrito.
        const box = this.el.querySelector('.fv-form-error');
        box.textContent = error.message;
        box.hidden = false;
      } finally {
        save.disabled = false;
      }
    }

    /** Traduce el estado a los parámetros que entiende la API en Go. */
    // Los filtros y la búsqueda, pero sin page ni limit: el export no se pagina
    // y el limit de la tabla (10 por defecto) recortaba el archivo a la página
    // visible. El servidor se queda con su propio tope de filas.
    exportQuery() {
      const params = this.query();
      params.delete('page');
      params.delete('limit');
      return params;
    }

    query() {
      const params = new URLSearchParams({
        page: this.state.page,
        limit: this.state.limit,
      });
      if (this.state.search) params.set('search', this.state.search);
      if (this.state.orderBy) {
        params.set('order', this.state.orderBy);
        params.set('dir', this.state.orderDir);
      }

      for (const [field, value] of Object.entries(this.state.filters)) {
        params.append('filter', `${field}:ILIKE:${value}`);
      }
      return params;
    }

    /**
     * Vuelve al listado sin filtros ni búsqueda y lo recarga.
     *
     * Tras importar, los registros nuevos se insertan al final (created_at desc)
     * y caen en la primera página. Recargar conservando la búsqueda o el filtro
     * activo los deja fuera de la vista y parece que no se guardó nada.
     */
    async resetList() {
      this.state.page = 1;
      this.state.search = '';
      this.state.filters = {};

      const search = this.el.querySelector('.fv-search');
      if (search) search.value = '';
      for (const input of this.el.querySelectorAll('[data-filter]')) {
        input.value = '';
      }

      await this.load();
    }

    async load() {
      try {
        this.page = await this.api.get(`${this.base}?${this.query()}`);
        this.state.page = this.page.pagination?.page || this.page.page || 1;
        this.renderRows();
        this.renderFooter();
        // Las casillas se recrean en cada render: hay que volver a marcar lo
        // que siga seleccionado y recalcular el estado de "seleccionar todo".
        this.syncSelectionUI();
        this.el.dispatchEvent(new CustomEvent('fast:change', {
          bubbles: true, detail: { page: this.page },
        }));
      } catch (error) {
        this.body.innerHTML = `<tr><td class="fv-empty fv-error" colspan="99">${esc(error.message)}</td></tr>`;
      }
    }

    renderRows() {
      const columns = this.columns();
      const rows = this.page.items || this.page.data || [];

      if (!rows.length) {
        const filtering = this.state.search || Object.keys(this.state.filters).length;
        this.body.innerHTML = `<tr><td class="fv-empty" colspan="99">${
          filtering ? 'Sin resultados para esta búsqueda.' : 'Todavía no hay registros.'
        }</td></tr>`;
        return;
      }

      this.body.innerHTML = rows.map((record) => `
        <tr data-id="${esc(record.id)}"${
          this.selectAllMatching || this.selection.has(record.id) ? ' class="is-selected"' : ''}>
          <td class="fv-select-cell">
            <input type="checkbox" data-select="${esc(record.id)}"
              ${this.selectAllMatching || this.selection.has(record.id) ? 'checked' : ''}
              aria-label="Seleccionar ${esc(formatValue(record[this.columns()[0]?.name], this.columns()[0]) || record.id)}">
          </td>
          ${columns.map((f) => `<td>${formatValue(record[f.name], f)}</td>`).join('')}
          <td class="fv-actions">
            ${this.transitionButtons(record)}
            <button type="button" data-action="edit" title="Editar registro">✏️ Editar</button>
            <button type="button" data-action="delete" title="Eliminar registro">🗑️</button>
          </td>
        </tr>`).join('');

      for (const tr of this.body.querySelectorAll('tr[data-id]')) {
        const id = tr.dataset.id;
        const record = rows.find((r) => r.id === id);
        tr.querySelector('[data-action="edit"]').addEventListener('click', () => this.edit(id, record));
        tr.querySelector('[data-action="delete"]').addEventListener('click', () => this.remove(id));
        for (const btn of tr.querySelectorAll('[data-transition]')) {
          btn.addEventListener('click', () => this.transition(id, btn.dataset.transition));
        }
      }

      // Indicador de orden en el encabezado activo.
      for (const th of this.el.querySelectorAll('[data-sort]')) {
        th.classList.toggle('fv-sorted', th.dataset.sort === this.state.orderBy);
        th.classList.toggle('fv-desc', this.state.orderDir === 'desc');
      }
    }

    /**
     * Refleja la selección en la barra de aviso y en la casilla de la cabecera.
     *
     * La casilla de arriba se marca sólo si están marcadas todas las filas
     * visibles: con selección parcial queda sin marcar, que es lo que dice un
     * "seleccionar todo" de verdad. En modo "todo el filtro" sí queda marcada
     * entera, y entonces no hay nada indeterminado que mostrar.
     */
    syncSelectionUI() {
      const boxes = [...this.body.querySelectorAll('[data-select]')];
      const isSelected = (id) => this.selectAllMatching || this.selection.has(id);

      for (const cb of boxes) {
        const want = isSelected(cb.dataset.select);
        if (cb.checked !== want) cb.checked = want;
        cb.closest('tr')?.classList.toggle('is-selected', want);
      }

      // El recuento sale del Set, no de las casillas: al pulsar "seleccionar
      // todo" el DOM todavía va marcando la casilla de cabecera y las de fila
      // siguen sin marcar, así que contar antes de sincronizar daría 0 y la
      // cabecera se quedaría sin marcar con las 3 filas seleccionadas.
      const selected = boxes.filter((cb) => isSelected(cb.dataset.select)).length;

      const checkAll = this.el.querySelector('.fv-check-all');
      const everyVisible = boxes.length > 0 && selected === boxes.length;
      checkAll.checked = this.selectAllMatching || everyVisible;
      checkAll.indeterminate = !this.selectAllMatching && selected > 0 && !everyVisible;

      const bar = this.el.querySelector('.fv-selbar');
      const total = this.page?.pagination?.total ?? this.page?.total ?? null;
      const none = !this.selectAllMatching && this.selection.size === 0;
      bar.hidden = none;

      const info = this.el.querySelector('.fv-selinfo');
      if (this.selectAllMatching) {
        info.textContent = total != null
          ? `Los ${total} registros del filtro están seleccionados, en todas las páginas.`
          : 'Todos los registros del filtro están seleccionados, en todas las páginas.';
      } else {
        info.textContent = this.selection.size === 1
          ? '1 registro seleccionado — se exportará solo ese.'
          : `${this.selection.size} registros seleccionados — se exportarán solo esos.`;
      }
    }

    /** Limpia la selección al cambiar de página o de filtro. */
    clearSelection() {
      if (!this.selectAllMatching && this.selection.size === 0) return;
      this.selection.clear();
      this.selectAllMatching = false;
      this.syncSelectionUI();
    }

    /** Salta a una página. Fuera de rango no hace nada: no inventa páginas. */
    goToPage(target) {
      const { pages } = this.pagination();
      const page = Math.trunc(target);
      if (!Number.isFinite(page) || page < 1 || page > pages || page === this.state.page) return;
      this.state.page = page;
      this.clearSelection();
      this.load();
    }

    /**
     * Paginación normalizada de la respuesta.
     *
     * Gin devuelve {items, pagination:{page,limit,total,pages}} y la capa legacy
     * devuelve los campos en la raíz. Leer this.page.total_pages a pelo daba
     * undefined y toda comparación contra él era falsa.
     */
    pagination() {
      const p = this.page?.pagination || this.page || {};
      const total = p.total ?? 0;
      const limit = p.limit ?? this.state.limit ?? 0;
      const pages = p.pages ?? p.total_pages ?? (limit > 0 ? Math.ceil(total / limit) : 1);
      return {
        total,
        limit,
        page: p.page ?? this.state.page ?? 1,
        pages: Math.max(pages || 1, 1),
      };
    }

    renderFooter() {
      const { total, page, limit, pages } = this.pagination();

      const from = total === 0 ? 0 : (page - 1) * limit + 1;
      const to = Math.min(page * limit, total);

      this.el.querySelector('.fv-info').textContent =
        total === 0 ? 'Sin registros' : `${from}–${to} de ${total} · página ${page} de ${pages}`;

      // Sin páginas que recorrer no hay nada que paginar: se oculta el pie
      // entero en vez de dejar dos flechas muertos.
      const pager = this.el.querySelector('.fv-pager');
      if (pager) pager.hidden = pages <= 1;

      this.el.querySelector('[data-step="-1"]').disabled = page <= 1;
      this.el.querySelector('[data-step="1"]').disabled = page >= pages;

      this.renderPageNumbers(page, pages);
    }

    /**
     * Números de página alrededor de la actual: primero, actual±3, último,
     * con elipsis en los huecos. Al no haber botones de anterior/siguiente,
     * estos números son la única forma de avanzar: por eso la ventana es
     * ancha y el primer y el último siempre están a un clic.
     */
    renderPageNumbers(current, total) {
      const host = this.el.querySelector('.fv-pages');
      if (!host) return;

      if (total <= 1) {
        host.innerHTML = '';
        return;
      }

      const window_ = new Set([1, total, current]);
      for (let d = 1; d <= PAGE_WINDOW; d++) {
        if (current - d > 1) window_.add(current - d);
        if (current + d < total) window_.add(current + d);
      }

      const numbers = [...window_].filter((n) => n >= 1 && n <= total).sort((a, b) => a - b);

      let html = '';
      let previous = 0;
      for (const n of numbers) {
        if (previous && n - previous > 1) html += '<span class="fv-gap">…</span>';
        html += `<button type="button" class="fv-page${n === current ? ' is-current' : ''}"
          data-goto="${n}"${n === current ? ' aria-current="page"' : ''}>${n}</button>`;
        previous = n;
      }
      host.innerHTML = html;
    }

    /**
     * Botones de las transiciones legales desde el estado actual del registro.
     * Sin workflow declarado no devuelve nada — la fila se ve como siempre.
     */
    transitionButtons(record) {
      const wf = this.meta.workflow;
      if (!wf) return '';

      const current = record[wf.field];
      return Object.entries(wf.transitions)
        .filter(([, def]) => def.from.includes(current))
        .map(([action, def]) => `
          <button type="button" class="fv-transition" data-transition="${esc(action)}">
            ${esc(def.label)}
          </button>`)
        .join('');
    }

    /** Ejecuta una transición del flujo: POST .../{id}/transition. */
    async transition(id, action) {
      try {
        const result = await this.api.post(`${this.base}/${id}/transition`, { action });
        this.load();
        this.el.dispatchEvent(new CustomEvent('fast:transitioned', {
          bubbles: true, detail: result,
        }));
      } catch (error) {
        alert('Error: ' + error.message);
      }
    }

    async remove(id) {
      if (!confirm('¿Eliminar este registro?')) return;
      try {
        await this.api.del(`${this.base}/${id}`);

        // Si era el último de la página, retroceder para no quedar en vacío.
        const rows = this.page.items || this.page.data || [];
        if (rows.length === 1 && this.state.page > 1) this.state.page--;
        this.load();
      } catch (error) {
        alert('Error: ' + error.message);
      }
    }

    fail(message) {
      this.el.innerHTML = `<div class="fv-empty fv-error">${esc(message)}</div>`;
    }

    /** Recarga desde afuera, p. ej. tras guardar en un formulario del módulo. */
    refresh() {
      this.load();
    }
  }

  // Registro global: el módulo puede acceder a su vista para refrescarla.
  window.FastViews = new Map();

  function boot() {
    for (const container of document.querySelectorAll('[data-fast-view]')) {
      const view = new FastView(container);
      window.FastViews.set(container.dataset.model, view);
      view.init();
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
