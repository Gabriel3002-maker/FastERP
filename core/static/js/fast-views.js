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
 * Eventos que emite el contenedor, para que el módulo reaccione si quiere:
 *   fast:change — la vista se recargó       (detail: { page })
 *   fast:saved  — se creó o editó un registro (detail: { id, record, creating })
 */
(() => {
  'use strict';

  const SEARCH_DEBOUNCE_MS = 300;

  function tenantID() {
    const token = localStorage.getItem('access_token');
    if (!token) return null;
    try {
      return JSON.parse(atob(token.split('.')[1])).tenant_id;
    } catch {
      return null;
    }
  }

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

  /** Formatea un valor según el tipo que declaró el manifest. */
  function formatValue(value, field) {
    if (value === null || value === undefined || value === '') return '—';

    switch (field.input) {
      case 'checkbox':
        return value ? 'Sí' : 'No';
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
        limit: 20,
        search: '',
        filters: {},   // campo → texto escrito en el encabezado
        orderBy: '',
        orderDir: 'asc',
      };
    }

    get headers() {
      return { 'X-Tenant-ID': tenantID(), 'Content-Type': 'application/json' };
    }

    async init() {
      if (!this.module || !this.model) {
        return this.fail('Falta data-model="modulo/modelo" en el contenedor.');
      }

      try {
        const response = await fetch(`${this.base}/_meta`, { headers: this.headers });
        if (!response.ok) throw new Error(await this.errorText(response));
        this.meta = await response.json();
      } catch (error) {
        return this.fail(`No se pudo leer el modelo: ${error.message}`);
      }

      // El orden inicial es la primera columna: lo más predecible para quien mira.
      this.state.orderBy = this.columns()[0]?.name || '';
      this.renderChrome();
      this.load();
    }

    /** Columnas a mostrar, resueltas contra la metadata. */
    columns() {
      const wanted = this.meta.views?.list?.columns || [];
      const byName = new Map(this.meta.fields.map((f) => [f.name, f]));
      return wanted.map((name) => byName.get(name)).filter(Boolean);
    }

    /** Estructura fija de la vista; sólo el cuerpo de la tabla se re-renderiza. */
    renderChrome() {
      const sizes = this.meta.page_sizes || [10, 20, 50, 100];

      this.el.innerHTML = `
        <div class="fv">
          <div class="fv-toolbar">
            <input type="search" class="fv-search" placeholder="Buscar en ${esc(this.meta.label)}…">
            <label class="fv-size">
              Mostrar
              <select>${sizes.map((s) => `<option${s === this.state.limit ? ' selected' : ''}>${s}</option>`).join('')}</select>
            </label>
            <button class="fv-new" type="button">+ Nuevo</button>
          </div>
          <div class="fv-scroll">
            <table class="fv-table">
              <thead>
                <tr class="fv-labels">
                  ${this.columns().map((f) => `
                    <th data-sort="${esc(f.name)}" title="Ordenar por ${esc(f.label)}">
                      <span>${esc(f.label)}</span><i class="fv-arrow"></i>
                    </th>`).join('')}
                  <th class="fv-actions-col"></th>
                </tr>
                <tr class="fv-filters">
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
              <button class="fv-prev" type="button">‹ Anterior</button>
              <button class="fv-next" type="button">Siguiente ›</button>
            </div>
          </div>

          <dialog class="fv-dialog">
            <form method="dialog" class="fv-form">
              <header>
                <h2 class="fv-form-title"></h2>
                <button type="button" class="fv-close" aria-label="Cerrar">&times;</button>
              </header>
              <p class="fv-form-error" hidden></p>
              <div class="fv-form-fields">${this.formFields()}</div>
              <footer>
                <button type="button" class="fv-cancel">Cancelar</button>
                <button type="button" class="fv-save">Guardar</button>
              </footer>
            </form>
          </dialog>
        </div>`;

      this.body = this.el.querySelector('tbody');
      this.dialog = this.el.querySelector('.fv-dialog');
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
      const reload = (reset = true) => {
        if (reset) this.state.page = 1;
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

      this.el.querySelector('.fv-prev').addEventListener('click', () => {
        if (this.state.page > 1) { this.state.page--; this.load(); }
      });
      this.el.querySelector('.fv-next').addEventListener('click', () => {
        if (this.state.page < this.page.total_pages) { this.state.page++; this.load(); }
      });

      this.el.querySelector('.fv-new').addEventListener('click', () => this.openForm());
      this.el.querySelector('.fv-save').addEventListener('click', () => this.save());

      for (const selector of ['.fv-close', '.fv-cancel']) {
        this.el.querySelector(selector).addEventListener('click', () => this.dialog.close());
      }
      // Clic en el fondo del diálogo (fuera del formulario) también cierra.
      this.dialog.addEventListener('click', (e) => {
        if (e.target === this.dialog) this.dialog.close();
      });
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

      this.dialog.showModal();
      this.el.querySelector('[data-field]')?.focus();
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
        const response = await fetch(`${this.base}/${id}`, { headers: this.headers });
        if (!response.ok) throw new Error(await this.errorText(response));
        this.openForm(await response.json());
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
        const response = await fetch(url, {
          method: this.editing ? 'PUT' : 'POST',
          headers: this.headers,
          body: JSON.stringify(payload),
        });
        if (!response.ok) throw new Error(await this.errorText(response));

        const body = await response.json();
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

    /** Traduce el estado a los parámetros que entiende el SDK. */
    query() {
      const params = new URLSearchParams({
        page: this.state.page,
        limit: this.state.limit,
      });
      if (this.state.search) params.set('search', this.state.search);
      if (this.state.orderBy) {
        params.set('order_by', this.state.orderBy);
        params.set('order_dir', this.state.orderDir);
      }

      const byName = new Map(this.meta.fields.map((f) => [f.name, f]));
      for (const [field, value] of Object.entries(this.state.filters)) {
        const meta = byName.get(field);
        // Un campo con opciones se filtra exacto; el texto libre, por "contiene".
        const key = meta?.options?.length ? field : `${field}__contains`;
        params.set(key, value);
      }
      return params;
    }

    async load() {
      try {
        const response = await fetch(`${this.base}?${this.query()}`, { headers: this.headers });
        if (!response.ok) throw new Error(await this.errorText(response));

        this.page = await response.json();
        this.state.page = this.page.page;
        this.renderRows();
        this.renderFooter();
        this.el.dispatchEvent(new CustomEvent('fast:change', {
          bubbles: true, detail: { page: this.page },
        }));
      } catch (error) {
        this.body.innerHTML = `<tr><td class="fv-empty fv-error" colspan="99">${esc(error.message)}</td></tr>`;
      }
    }

    renderRows() {
      const columns = this.columns();
      const rows = this.page.data || [];

      if (!rows.length) {
        const filtering = this.state.search || Object.keys(this.state.filters).length;
        this.body.innerHTML = `<tr><td class="fv-empty" colspan="99">${
          filtering ? 'Sin resultados para esta búsqueda.' : 'Todavía no hay registros.'
        }</td></tr>`;
        return;
      }

      this.body.innerHTML = rows.map((record) => `
        <tr data-id="${esc(record.id)}">
          ${columns.map((f) => `<td>${esc(formatValue(record[f.name], f))}</td>`).join('')}
          <td class="fv-actions">
            <button type="button" data-action="edit">Editar</button>
            <button type="button" data-action="delete">Eliminar</button>
          </td>
        </tr>`).join('');

      for (const tr of this.body.querySelectorAll('tr[data-id]')) {
        const id = tr.dataset.id;
        const record = rows.find((r) => r.id === id);
        tr.querySelector('[data-action="edit"]').addEventListener('click', () => this.edit(id, record));
        tr.querySelector('[data-action="delete"]').addEventListener('click', () => this.remove(id));
      }

      // Indicador de orden en el encabezado activo.
      for (const th of this.el.querySelectorAll('[data-sort]')) {
        th.classList.toggle('fv-sorted', th.dataset.sort === this.state.orderBy);
        th.classList.toggle('fv-desc', this.state.orderDir === 'desc');
      }
    }

    renderFooter() {
      const { total, page, limit, total_pages: pages } = this.page;
      const from = total === 0 ? 0 : (page - 1) * limit + 1;
      const to = Math.min(page * limit, total);

      this.el.querySelector('.fv-info').textContent =
        total === 0 ? 'Sin registros' : `${from}–${to} de ${total} · página ${page} de ${pages}`;
      this.el.querySelector('.fv-prev').disabled = page <= 1;
      this.el.querySelector('.fv-next').disabled = page >= pages;
    }

    async remove(id) {
      if (!confirm('¿Eliminar este registro?')) return;
      try {
        const response = await fetch(`${this.base}/${id}`, {
          method: 'DELETE', headers: this.headers,
        });
        if (!response.ok) throw new Error(await this.errorText(response));

        // Si era el último de la página, retroceder para no quedar en vacío.
        if (this.page.data.length === 1 && this.state.page > 1) this.state.page--;
        this.load();
      } catch (error) {
        alert('Error: ' + error.message);
      }
    }

    /** El SDK responde {"error": "..."} con el motivo real. */
    async errorText(response) {
      try {
        const body = await response.json();
        if (body.error) return body.error;
      } catch { /* respuesta sin JSON */ }
      return `Error ${response.status}`;
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
