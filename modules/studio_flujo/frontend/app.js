// ========================================
// Studio-Flujo
// ========================================
// Dibujás un diagrama de estados en Mermaid, agregás los campos que
// necesita tu modelo, y "Generar" arma un manifest.json (más el frontend
// mínimo, si es un módulo nuevo) usando exactamente el mismo motor que ya
// hace correr a cualquier otro módulo — no hay un camino especial para lo
// generado.
//
// Endpoints propios (no son @fast, son de este módulo):
//   POST /api/_studio/parse-mermaid   → interpreta el diagrama
//   POST /api/_studio/generate        → escribe el manifest (admin)
//   GET  /api/_catalog                → módulos instalados, para "extender"

const FIELD_TYPES = [
  ['string', 'Texto corto'],
  ['text', 'Texto largo'],
  ['email', 'Correo'],
  ['phone', 'Teléfono'],
  ['url', 'URL'],
  ['integer', 'Entero'],
  ['decimal', 'Decimal'],
  ['money', 'Dinero'],
  ['boolean', 'Sí / No'],
  ['date', 'Fecha'],
  ['datetime', 'Fecha y hora'],
];

const DIAGRAM_DEBOUNCE_MS = 500;

const $ = (id) => document.getElementById(id);

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

// Los <script> insertados con innerHTML no se ejecutan (el módulo ya llegó
// así, vía module-loader.html); mermaid.js se carga a mano, igual que ahí.
function loadScript(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[src="${src}"]`)) return resolve();
    const script = document.createElement('script');
    script.src = src;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`no se pudo cargar ${src}`));
    document.body.appendChild(script);
  });
}

// --- Estado del lienzo ---

let parsedFlow = null; // último resultado válido de parse-mermaid
let fields = [{ name: '', type: 'string', label: '', required: false, options: '' }];
let mermaidReady = false;

// --- Arranque ---

init();

async function init() {
  bindModeToggle();
  bindDiagramInput();
  bindFieldEditor();
  $('sf-generate').addEventListener('click', generate);

  loadCatalog();

  try {
    await loadScript('/static/vendor/mermaid/mermaid.min.js');
    window.mermaid.initialize({ startOnLoad: false, theme: 'neutral' });
    mermaidReady = true;
  } catch {
    // Sin mermaid.js no hay dibujo, pero /api/_studio/parse-mermaid sigue
    // funcionando igual: el motor no depende del renderer visual.
  }

  renderFields();
}

// --- Paso 1: destino ---

function bindModeToggle() {
  for (const radio of document.querySelectorAll('input[name="sf-mode"]')) {
    radio.addEventListener('change', () => {
      const extending = radio.value === 'extend' && radio.checked;
      $('sf-create-fields').hidden = extending;
      $('sf-extend-fields').hidden = !extending;
    });
  }
}

async function loadCatalog() {
  const select = $('sf-existing-module');
  try {
    const response = await fetch('/api/_catalog');
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const { modules } = await response.json();

    if (!modules.length) {
      select.innerHTML = '<option value="">No hay módulos con modelos todavía</option>';
      return;
    }

    select.innerHTML = modules
      .map((m) => `<option value="${esc(m.name)}">${esc(m.icon || '')} ${esc(m.label)}</option>`)
      .join('');

    const showInfo = () => {
      const mod = modules.find((m) => m.name === select.value);
      const names = (mod?.models || []).map((model) => model.name).join(', ') || '(sin modelos)';
      $('sf-existing-info').textContent = `Modelos actuales: ${names}`;
    };
    select.addEventListener('change', showInfo);
    showInfo();
  } catch (error) {
    select.innerHTML = '<option value="">Error cargando el catálogo</option>';
  }
}

function currentMode() {
  return document.querySelector('input[name="sf-mode"]:checked').value;
}

// --- Paso 2: diagrama ---

function bindDiagramInput() {
  $('sf-diagram').addEventListener('input', debounce(onDiagramChange, DIAGRAM_DEBOUNCE_MS));
}

async function onDiagramChange() {
  const source = $('sf-diagram').value.trim();
  const resultBox = $('sf-flow-result');
  const previewBox = $('sf-mermaid-preview');

  if (!source) {
    parsedFlow = null;
    resultBox.hidden = true;
    previewBox.innerHTML = '<div class="sf-empty">El dibujo del diagrama aparece acá.</div>';
    return;
  }

  // El dibujo (mermaid.js) y la validación del motor (nuestro parser) son dos
  // cosas separadas: una es cosmética, la otra es la que de verdad importa.
  renderMermaidPreview(source);

  try {
    const response = await fetch('/api/_studio/parse-mermaid', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ diagram: source }),
    });
    const body = await response.json();

    if (!response.ok) {
      parsedFlow = null;
      resultBox.hidden = false;
      resultBox.className = 'sf-result sf-result-error';
      resultBox.textContent = body.error;
      return;
    }

    parsedFlow = body;
    resultBox.hidden = false;
    resultBox.className = 'sf-result sf-result-ok';
    resultBox.innerHTML = `✓ ${body.states.length} estados, `
      + `${Object.keys(body.transitions).length} acciones. `
      + `Inicial: <strong>${esc(body.initial)}</strong>`;
  } catch (error) {
    parsedFlow = null;
    resultBox.hidden = false;
    resultBox.className = 'sf-result sf-result-error';
    resultBox.textContent = 'No se pudo validar el diagrama: ' + error.message;
  }
}

async function renderMermaidPreview(source) {
  const previewBox = $('sf-mermaid-preview');
  if (!mermaidReady) return;

  try {
    const id = 'sf-diagram-' + Date.now();
    const { svg } = await window.mermaid.render(id, source);
    previewBox.innerHTML = svg;
  } catch {
    previewBox.innerHTML = '<div class="sf-empty">El diagrama todavía no se puede dibujar.</div>';
  }
}

// --- Paso 3: campos ---

function bindFieldEditor() {
  $('sf-add-field').addEventListener('click', () => {
    fields.push({ name: '', type: 'string', label: '', required: false, options: '' });
    renderFields();
  });
}

function renderFields() {
  const list = $('sf-fields-list');
  list.innerHTML = fields.map((f, i) => `
    <div class="sf-field-row" data-index="${i}">
      <input class="sf-f-name" placeholder="nombre_campo" value="${esc(f.name)}" spellcheck="false">
      <select class="sf-f-type">
        ${FIELD_TYPES.map(([v, l]) => `<option value="${v}"${f.type === v ? ' selected' : ''}>${l}</option>`).join('')}
      </select>
      <input class="sf-f-label" placeholder="Etiqueta" value="${esc(f.label)}">
      <label class="sf-f-required">
        <input type="checkbox" class="sf-f-req"${f.required ? ' checked' : ''}> requerido
      </label>
      <input class="sf-f-options" placeholder="opciones, separadas, por coma" value="${esc(f.options)}">
      <button type="button" class="sf-f-remove" title="Quitar campo">&times;</button>
    </div>`).join('');

  list.querySelectorAll('.sf-field-row').forEach((row) => {
    const i = Number(row.dataset.index);
    const sync = () => {
      fields[i] = {
        name: row.querySelector('.sf-f-name').value.trim(),
        type: row.querySelector('.sf-f-type').value,
        label: row.querySelector('.sf-f-label').value.trim(),
        required: row.querySelector('.sf-f-req').checked,
        options: row.querySelector('.sf-f-options').value.trim(),
      };
    };
    row.querySelectorAll('input, select').forEach((el) => el.addEventListener('input', sync));
    row.querySelector('.sf-f-remove').addEventListener('click', () => {
      fields.splice(i, 1);
      if (fields.length === 0) fields.push({ name: '', type: 'string', label: '', required: false, options: '' });
      renderFields();
    });
  });
}

// --- Paso 4: generar ---

function buildPayload() {
  const extending = currentMode() === 'extend';
  const module = extending ? $('sf-existing-module').value.trim() : $('sf-module').value.trim();
  const model = $('sf-model').value.trim();

  const payloadFields = {};
  let sequence = 20;

  const stateField = $('sf-state-field').value.trim();
  if (parsedFlow && stateField) {
    payloadFields[stateField] = {
      type: 'string',
      label: 'Estado',
      options: parsedFlow.states,
      sequence: 5,
    };
  }

  for (const f of fields) {
    if (!f.name) continue;
    payloadFields[f.name] = {
      type: f.type,
      label: f.label || undefined,
      required: f.required || undefined,
      options: f.options ? f.options.split(',').map((o) => o.trim()).filter(Boolean) : undefined,
      sequence: (sequence += 10),
    };
  }

  const payload = {
    module,
    model,
    model_label: $('sf-model-label').value.trim() || undefined,
    fields: payloadFields,
  };

  if (!extending) {
    payload.module_label = $('sf-module-label').value.trim() || undefined;
    payload.module_icon = $('sf-module-icon').value.trim() || undefined;
  }

  if (parsedFlow && stateField) {
    payload.workflow = {
      field: stateField,
      initial: parsedFlow.initial,
      transitions: parsedFlow.transitions,
    };
  }

  return payload;
}

async function generate() {
  const payload = buildPayload();
  const box = $('sf-generate-result');
  const button = $('sf-generate');

  if (!payload.module || !payload.model) {
    box.hidden = false;
    box.className = 'sf-result sf-result-error';
    box.textContent = 'Falta el nombre del módulo o del modelo.';
    return;
  }
  if (Object.keys(payload.fields).length === 0) {
    box.hidden = false;
    box.className = 'sf-result sf-result-error';
    box.textContent = 'Agregá al menos un campo (o dibujá el flujo, que también agrega el campo de estado).';
    return;
  }

  button.disabled = true;
  box.hidden = false;
  box.className = 'sf-result';
  box.textContent = 'Generando…';

  try {
    const response = await fetch('/api/_studio/generate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const body = await response.json();

    if (!response.ok) {
      box.className = 'sf-result sf-result-error';
      box.textContent = body.error;
      return;
    }

    const action = body.mode === 'created' ? 'creado' : 'extendido';
    box.className = 'sf-result sf-result-ok';
    box.innerHTML = `✓ Módulo <strong>${esc(body.module)}</strong> ${action} con el modelo `
      + `<strong>${esc(body.model)}</strong>. Instalalo desde <em>Módulos</em> para usarlo.`;
  } catch (error) {
    box.className = 'sf-result sf-result-error';
    box.textContent = 'Error: ' + error.message;
  } finally {
    button.disabled = false;
  }
}
