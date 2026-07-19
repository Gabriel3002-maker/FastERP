// ========================================
// Studio-Flujo
// ========================================
// Dos formas de definir el flujo: dibujarlo (cajas que se arrastran y se
// conectan, con Drawflow) o escribirlo en Mermaid. Cualquiera de las dos
// termina en la misma estructura — { states, initial, transitions } — que
// alimenta el mismo payload hacia /api/_studio/generate. El motor no sabe ni
// le importa de cuál de las dos vino.
//
// Endpoints propios (no son @fast, son de este módulo):
//   POST /api/_studio/parse-mermaid   → interpreta el texto (modo texto)
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

const DEBOUNCE_MS = 400;

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

// Los <script>/<link> insertados con innerHTML no se ejecutan (el módulo ya
// llegó así, vía module-loader.html); las librerías vendorizadas se cargan a
// mano, igual que ahí.
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

function loadStyle(href) {
  if (document.querySelector(`link[href="${href}"]`)) return;
  const link = document.createElement('link');
  link.rel = 'stylesheet';
  link.href = href;
  document.head.appendChild(link);
}

// Réplica liviana, del lado del cliente, del slugifyAction() del motor
// (core/sdk/mermaid.go): minúsculas, separadores → "_", sin dígito inicial.
// No hace normalización Unicode, igual que el original — una tilde se vuelve
// separador, no la letra sin tilde.
function slugifyAction(label) {
  let slug = String(label || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
  if (!slug) slug = 'accion';
  if (/^[0-9]/.test(slug)) slug = 't_' + slug;
  if (slug.length > 63) slug = slug.replace(/_+$/, '').slice(0, 63);
  return slug;
}

// --- Estado del lienzo ---

let activeTab = 'canvas';
let parsedFlow = null; // resultado de parse-mermaid (modo texto)
let drawflow = null; // instancia de Drawflow (modo lienzo)
let connectionLabels = {}; // "outId->inId" → etiqueta escrita al conectar
let fields = [{ name: '', type: 'string', label: '', required: false, options: '' }];

// --- Arranque ---

init();

async function init() {
  bindModeToggle();
  bindTabs();
  bindDiagramInput();
  bindFieldEditor();
  $('sf-generate').addEventListener('click', generate);
  $('sf-add-state').addEventListener('click', () => addStateNode());

  loadCatalog();
  await initCanvas();
  renderFields();
}

function bindModeToggle() {
  for (const radio of document.querySelectorAll('input[name="sf-mode"]')) {
    radio.addEventListener('change', () => {
      const extending = radio.value === 'extend' && radio.checked;
      $('sf-create-fields').hidden = extending;
      $('sf-extend-fields').hidden = !extending;
    });
  }
}

function bindTabs() {
  for (const tab of document.querySelectorAll('.sf-tab')) {
    tab.addEventListener('click', () => {
      activeTab = tab.dataset.tab;
      document.querySelectorAll('.sf-tab').forEach((t) => t.classList.toggle('sf-tab-active', t === tab));
      $('sf-panel-canvas').hidden = activeTab !== 'canvas';
      $('sf-panel-text').hidden = activeTab !== 'text';
      $('sf-flow-result').hidden = true;
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

// ========================================
// Modo lienzo (Drawflow)
// ========================================

async function initCanvas() {
  loadStyle('/static/vendor/drawflow/drawflow.min.css');

  try {
    await loadScript('/static/vendor/drawflow/drawflow.min.js');
  } catch {
    $('sf-drawflow').innerHTML = '<div class="sf-empty">No se pudo cargar el lienzo. Usá la pestaña de texto.</div>';
    return;
  }

  drawflow = new window.Drawflow($('sf-drawflow'));
  drawflow.reroute = true;
  drawflow.start();

  drawflow.on('connectionCreated', onConnectionCreated);
  drawflow.on('connectionRemoved', onConnectionRemoved);

  // Dos estados de arranque, ya conectados: se ve de entrada qué hacer en
  // vez de un lienzo vacío sin pistas.
  const a = addStateNode('prospecto', 60, 80);
  const b = addStateNode('activo', 380, 80);
  markInitial(a);
  drawflow.addConnection(a, b, 'output_1', 'input_1');
  connectionLabels[`${a}->${b}`] = 'Activar';
  renderTransitionsPanel();
}

function nodeHTML(label) {
  return `
    <div class="sf-node-box">
      <span class="sf-node-label" contenteditable="true" df-label spellcheck="false">${esc(label)}</span>
      <div class="sf-node-actions">
        <button type="button" class="sf-node-star" title="Marcar como estado inicial">☆</button>
        <button type="button" class="sf-node-delete" title="Eliminar estado">&times;</button>
      </div>
    </div>`;
}

function addStateNode(label, x, y) {
  const name = (label || `estado_${Object.keys(drawflow.export().drawflow.Home.data).length + 1}`);
  const posX = x ?? 60 + Math.random() * 300;
  const posY = y ?? 200 + Math.random() * 150;

  const id = drawflow.addNode('estado', 1, 1, posX, posY, 'sf-node', { label: name, initial: false }, nodeHTML(name));

  const el = document.getElementById(`node-${id}`);
  el.querySelector('.sf-node-star').addEventListener('click', () => markInitial(id));
  el.querySelector('.sf-node-delete').addEventListener('click', () => removeStateNode(id));
  el.querySelector('.sf-node-label').addEventListener('input', () => renderTransitionsPanel());

  return id;
}

function removeStateNode(id) {
  drawflow.removeNodeId(`node-${id}`);
  // Drawflow ya dispara connectionRemoved por cada conexión que colgaba de
  // este nodo, así que connectionLabels se limpia solo vía esos eventos.
  renderTransitionsPanel();
}

// Sólo puede haber un estado inicial: se limpia el resto y se marca este.
function markInitial(id) {
  const data = drawflow.export().drawflow.Home.data;
  for (const nodeId in data) {
    const isInitial = String(nodeId) === String(id);
    drawflow.updateNodeDataFromId(nodeId, { ...data[nodeId].data, initial: isInitial });
    const star = document.querySelector(`#node-${nodeId} .sf-node-star`);
    if (star) {
      star.textContent = isInitial ? '⭐' : '☆';
      star.closest('.sf-node-box')?.classList.toggle('sf-node-initial', isInitial);
    }
  }
}

function onConnectionCreated({ output_id, input_id }) {
  const label = prompt('¿Cómo se llama esta acción? (ej: "Activar", "Confirmar pedido")');
  const clean = (label || '').trim();

  if (!clean) {
    // Sin etiqueta la transición no sirve — el motor la rechazaría igual
    // (toda transición necesita un nombre de acción) — así que ni se ofrece.
    drawflow.removeSingleConnection(output_id, input_id, 'output_1', 'input_1');
    return;
  }

  connectionLabels[`${output_id}->${input_id}`] = clean;
  renderTransitionsPanel();
}

function onConnectionRemoved({ output_id, input_id }) {
  delete connectionLabels[`${output_id}->${input_id}`];
  renderTransitionsPanel();
}

// El panel lateral es la vista editable de las transiciones: renombrar acá
// no toca el lienzo, sólo la etiqueta guardada en connectionLabels.
function renderTransitionsPanel() {
  const list = $('sf-transitions-list');
  if (!drawflow) return;

  const data = drawflow.export().drawflow.Home.data;
  const labelOf = (id) => data[id]?.data?.label || '(sin nombre)';

  const rows = Object.entries(connectionLabels);
  if (rows.length === 0) {
    list.innerHTML = '<div class="sf-empty">Conectá dos estados para crear una.</div>';
    return;
  }

  list.innerHTML = rows.map(([key, label]) => {
    const [outId, inId] = key.split('->');
    return `
      <div class="sf-transition-row" data-key="${esc(key)}">
        <span class="sf-transition-path">${esc(labelOf(outId))} → ${esc(labelOf(inId))}</span>
        <input class="sf-transition-label" value="${esc(label)}">
        <button type="button" class="sf-transition-remove" title="Quitar">&times;</button>
      </div>`;
  }).join('');

  list.querySelectorAll('.sf-transition-row').forEach((row) => {
    const key = row.dataset.key;
    row.querySelector('.sf-transition-label').addEventListener('input', (e) => {
      connectionLabels[key] = e.target.value;
    });
    row.querySelector('.sf-transition-remove').addEventListener('click', () => {
      const [outId, inId] = key.split('->');
      drawflow.removeSingleConnection(outId, inId, 'output_1', 'input_1'); // dispara connectionRemoved
    });
  });
}

// Arma { states, initial, transitions } directo del grafo, o { error } si
// falta algo — la misma forma que produce el parser de Mermaid, así
// buildPayload() no necesita saber de cuál de los dos modos vino.
function graphToWorkflow() {
  if (!drawflow) return { error: 'El lienzo no cargó.' };

  const data = drawflow.export().drawflow.Home.data;
  const nodes = Object.values(data);
  if (nodes.length === 0) return null; // sin diagrama: el modelo no lleva flujo, y no es un error

  const idToLabel = {};
  const states = [];
  const seen = new Set();
  let initial = null;

  for (const node of nodes) {
    const label = (node.data.label || '').trim();
    idToLabel[node.id] = label;
    if (label && !seen.has(label)) {
      seen.add(label);
      states.push(label);
    }
    if (node.data.initial) initial = label;
  }

  if (!initial) {
    return { error: 'Marcá qué estado es el inicial (la ⭐ en una de las cajas).' };
  }

  const transitions = {};
  for (const node of nodes) {
    const fromLabel = idToLabel[node.id];
    for (const outKey in node.outputs || {}) {
      for (const conn of node.outputs[outKey].connections || []) {
        const rawLabel = connectionLabels[`${node.id}->${conn.node}`];
        if (!rawLabel) continue; // conexión sin etiqueta: no debería poder pasar, pero por las dudas
        const toLabel = idToLabel[conn.node];
        const action = slugifyAction(rawLabel);

        if (transitions[action]) {
          if (transitions[action].to !== toLabel) {
            return {
              error: `la acción "${rawLabel}" ya lleva a "${transitions[action].to}"; `
                + `no puede llevar también a "${toLabel}" — usá un nombre distinto para esa transición`,
            };
          }
          if (!transitions[action].from.includes(fromLabel)) transitions[action].from.push(fromLabel);
        } else {
          transitions[action] = { from: [fromLabel], to: toLabel, label: rawLabel };
        }
      }
    }
  }

  if (Object.keys(transitions).length === 0) {
    return { error: 'Conectá al menos dos estados para tener una transición.' };
  }

  return { states, initial, transitions };
}

// ========================================
// Modo texto (Mermaid)
// ========================================

function bindDiagramInput() {
  $('sf-diagram').addEventListener('input', debounce(onDiagramChange, DEBOUNCE_MS));
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

let mermaidReady = false;

async function renderMermaidPreview(source) {
  const previewBox = $('sf-mermaid-preview');
  if (!mermaidReady) {
    try {
      await loadScript('/static/vendor/mermaid/mermaid.min.js');
      window.mermaid.initialize({ startOnLoad: false, theme: 'neutral' });
      mermaidReady = true;
    } catch {
      return;
    }
  }

  try {
    const id = 'sf-diagram-' + Date.now();
    const { svg } = await window.mermaid.render(id, source);
    previewBox.innerHTML = svg;
  } catch {
    previewBox.innerHTML = '<div class="sf-empty">El diagrama todavía no se puede dibujar.</div>';
  }
}

// ========================================
// Paso 3: campos
// ========================================

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

// ========================================
// Paso 4: generar
// ========================================

// Resuelve el flujo activo sin importar de qué pestaña vino.
function resolveFlow() {
  if (activeTab === 'canvas') return graphToWorkflow();
  return parsedFlow; // el modo texto ya validó contra el motor al escribir
}

function buildPayload() {
  const extending = currentMode() === 'extend';
  const module = extending ? $('sf-existing-module').value.trim() : $('sf-module').value.trim();
  const model = $('sf-model').value.trim();
  const flow = resolveFlow();

  const payloadFields = {};
  let sequence = 20;

  const stateField = $('sf-state-field').value.trim();
  if (flow && !flow.error && stateField) {
    payloadFields[stateField] = {
      type: 'string',
      label: 'Estado',
      options: flow.states,
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

  if (flow && !flow.error && stateField) {
    payload.workflow = { field: stateField, initial: flow.initial, transitions: flow.transitions };
  }

  return payload;
}

async function generate() {
  const flow = resolveFlow();
  const box = $('sf-generate-result');
  const button = $('sf-generate');

  if (flow?.error) {
    box.hidden = false;
    box.className = 'sf-result sf-result-error';
    box.textContent = flow.error;
    return;
  }

  const payload = buildPayload();

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
