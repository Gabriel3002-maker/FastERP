// ========================================
// Automatizaciones
// ========================================
// Editor visual (Drawflow) de un pipeline de pasos tipados — no es una
// máquina de estados (para eso está Studio-Flujo): acá cada caja hace algo
// de verdad (crear/actualizar/buscar un registro) y una Condición bifurca
// el camino. Los valores de cada campo pueden ser fijos o variables
// ({{trigger.campo}}, {{nodo.campo}}), resueltas del lado del servidor.
//
// El grafo se guarda como texto plano en el campo "definition" del modelo
// @fast "automation" (guardar/listar/borrar salen gratis de ahí, cero
// código propio) y se ejecuta con el handler nativo:
//   POST /api/_automation/run              → corre un pipeline (a mano, "Probar")
//   POST /api/_automation/webhook/{id}     → lo mismo, disparado por un sistema externo
// El disparador "schedule" corre solo en el servidor (automation_scheduler.go
// en el core) — esta página no participa de esa ejecución, sólo la configura.

const $ = (id) => document.getElementById(id);

// El dispatcher @fast genérico (/api/{modulo}/{modelo}) no exige sesión,
// pero sí necesita saber a qué tenant pertenece la petición — mismo patrón
// que ya usa core/static/js/fast-views.js: el tenant sale del JWT que
// login.html guardó en localStorage al iniciar sesión, no hace falta
// pedírselo aparte al usuario. /api/_automation/run es distinto: ese sí
// exige sesión, y la sesión viaja sola en la cookie httpOnly que puso el
// login — no necesita este header.
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

const NODE_META = {
  update_trigger: { icon: '🔧', label: 'Actualizar disparador', outputs: 1 },
  create: { icon: '➕', label: 'Crear registro', outputs: 1 },
  search: { icon: '🔎', label: 'Buscar registro', outputs: 1 },
  condition: { icon: '❓', label: 'Condición', outputs: 2 },
};

// --- Estado ---

let catalog = []; // [{name, label, icon, models: [{name, label, fields:[...]}]}]
let drawflow = null;
let selectedNodeId = null;
let startNodeId = null;
let currentAutomationId = null; // null mientras es nueva / sin guardar
let webhookToken = null; // token del webhook — se genera al elegir ese disparador, o se carga con la automatización guardada

init();

async function init() {
  bindPalette();
  bindTopFields();
  bindRunAndSave();
  bindTriggerType();
  bindZoomControls();
  await loadCatalog();
  await loadSavedList();
  await initCanvas();
}

// ========================================
// Catálogo (módulos/modelos/campos instalados)
// ========================================

async function loadCatalog() {
  try {
    const response = await fetch('/api/_catalog');
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const body = await response.json();
    catalog = body.modules || [];
  } catch {
    catalog = [];
  }
  populateModuleSelect($('am-trigger-module'));
  $('am-trigger-module').addEventListener('change', () => {
    populateModelSelect($('am-trigger-model'), $('am-trigger-module').value);
    renderNodeConfig(); // el campo elegido en una Condición puede depender del disparador
  });
  populateModelSelect($('am-trigger-model'), $('am-trigger-module').value);
}

// Nunca ofrece "automatizaciones" (este mismo módulo) como disparador ni
// como destino de "Crear registro" — usarse a sí mismo como disparador no
// tiene un caso de uso real en v1 y sólo confunde el ejemplo de arranque
// (además, al ser el primer nombre en orden alfabético, quedaba
// seleccionado por default sin que nadie lo eligiera).
function populateModuleSelect(select) {
  const options = catalog.filter((m) => m.name !== 'automatizaciones');
  select.innerHTML = options
    .map((m) => `<option value="${esc(m.name)}">${esc(m.icon || '')} ${esc(m.label)}</option>`)
    .join('') || '<option value="">Sin módulos</option>';
}

function populateModelSelect(select, moduleName) {
  const mod = catalog.find((m) => m.name === moduleName);
  const models = mod ? mod.models : [];
  select.innerHTML = models
    .map((mo) => `<option value="${esc(mo.name)}">${esc(mo.label)}</option>`)
    .join('') || '<option value="">Sin modelos</option>';
}

function fieldsOf(moduleName, modelName) {
  const mod = catalog.find((m) => m.name === moduleName);
  const model = mod && mod.models.find((mo) => mo.name === modelName);
  return model ? model.fields : [];
}

function bindTopFields() {
  // nada más que atar acá — populateModuleSelect/populateModelSelect ya
  // quedan atados en loadCatalog(), que es quien tiene los datos.
}

// ========================================
// Lienzo (Drawflow) — paleta de nodos tipados
// ========================================

async function initCanvas() {
  loadStyle('/static/vendor/drawflow/drawflow.min.css');

  try {
    await loadScript('/static/vendor/drawflow/drawflow.min.js');
  } catch {
    $('am-drawflow').innerHTML = '<div class="am-empty">No se pudo cargar el lienzo.</div>';
    return;
  }

  drawflow = new window.Drawflow($('am-drawflow'));
  drawflow.reroute = true;
  drawflow.start();

  drawflow.on('nodeRemoved', (id) => {
    if (String(startNodeId) === String(id)) startNodeId = null;
    if (String(selectedNodeId) === String(id)) { selectedNodeId = null; renderNodeConfig(); }
  });

  // Un ejemplo de arranque: Condición conectada a un Actualizar en la rama
  // verdadera, para que se vea de entrada cómo se usan las dos salidas.
  const cond = addNode('condition');
  const upd = addNode('update_trigger', 380, 60);
  drawflow.addConnection(cond, upd, 'output_1', 'input_1');
  markStart(cond);
}

// drawflow puede no haber cargado (CDN caído, etc.) — el ?. cubre ese caso
// sin que los botones de zoom rompan la página.
function bindZoomControls() {
  $('am-zoom-in').addEventListener('click', () => drawflow?.zoom_in());
  $('am-zoom-out').addEventListener('click', () => drawflow?.zoom_out());
  $('am-zoom-reset').addEventListener('click', () => drawflow?.zoom_reset());
}

function bindPalette() {
  $('am-add-update').addEventListener('click', () => addNode('update_trigger'));
  $('am-add-create').addEventListener('click', () => addNode('create'));
  $('am-add-search').addEventListener('click', () => addNode('search'));
  $('am-add-condition').addEventListener('click', () => addNode('condition'));
}

function addNode(type, x, y) {
  const meta = NODE_META[type];
  const posX = x ?? 60 + Math.random() * 260;
  const posY = y ?? 200 + Math.random() * 150;

  const data = { type, fields: {}, module: '', model: '', field: '', operator: 'eq', value: '' };
  const id = drawflow.addNode(type, 1, meta.outputs, posX, posY, `am-node am-node-${type}`, data, nodeHTML(type));

  const el = document.getElementById(`node-${id}`);
  el.addEventListener('click', (e) => {
    e.stopPropagation();
    selectNode(id);
  });
  el.querySelector('.am-node-star').addEventListener('click', (e) => {
    e.stopPropagation();
    markStart(id);
  });
  el.querySelector('.am-node-delete').addEventListener('click', (e) => {
    e.stopPropagation();
    drawflow.removeNodeId(`node-${id}`);
  });

  return id;
}

function nodeHTML(type) {
  const meta = NODE_META[type];
  return `
    <div class="am-node-box">
      <span class="am-node-icon">${meta.icon}</span>
      <span class="am-node-label">${esc(meta.label)}</span>
      <div class="am-node-actions">
        <button type="button" class="am-node-star" title="Marcar como nodo inicial">☆</button>
        <button type="button" class="am-node-delete" title="Eliminar">&times;</button>
      </div>
    </div>`;
}

function markStart(id) {
  startNodeId = id;
  document.querySelectorAll('.am-node-box').forEach((box) => {
    const nodeEl = box.closest('[id^="node-"]');
    const isStart = nodeEl && nodeEl.id === `node-${id}`;
    box.classList.toggle('am-node-start', isStart);
    const star = box.querySelector('.am-node-star');
    if (star) star.textContent = isStart ? '⭐' : '☆';
  });
}

function selectNode(id) {
  selectedNodeId = id;
  document.querySelectorAll('.am-node-box').forEach((box) => {
    const nodeEl = box.closest('[id^="node-"]');
    box.classList.toggle('am-node-box-selected', nodeEl && nodeEl.id === `node-${id}`);
  });
  renderNodeConfig();
}

function currentNodeData() {
  if (selectedNodeId == null || !drawflow) return null;
  const data = drawflow.export().drawflow.Home.data;
  return data[selectedNodeId] ? data[selectedNodeId].data : null;
}

function updateSelectedNodeData(patch) {
  if (selectedNodeId == null) return;
  const current = currentNodeData() || {};
  drawflow.updateNodeDataFromId(selectedNodeId, { ...current, ...patch });
}

// ========================================
// Panel de configuración del nodo seleccionado
// ========================================

function renderNodeConfig() {
  const box = $('am-node-config');
  const data = currentNodeData();
  if (!data) {
    box.innerHTML = '<div class="am-empty">Tocá una caja para configurarla.</div>';
    return;
  }

  if (data.type === 'condition') {
    renderConditionConfig(box, data);
  } else if (data.type === 'create') {
    renderCreateConfig(box, data);
  } else if (data.type === 'search') {
    renderSearchConfig(box, data);
  } else {
    renderUpdateTriggerConfig(box, data);
  }
}

// Encabezado de color por tipo arriba de cada formulario del panel — así se
// ve de un vistazo qué caja se está configurando, mismo color que usa esa
// caja en el lienzo y en la paleta.
function configHeader(type) {
  const meta = NODE_META[type];
  return `
    <div class="am-config-header am-config-header-${type}">
      <span class="am-node-icon">${meta.icon}</span>
      <span>${esc(meta.label)}</span>
    </div>`;
}

function renderUpdateTriggerConfig(box, data) {
  const fields = fieldsOf($('am-trigger-module').value, $('am-trigger-model').value);
  box.innerHTML = `
    ${configHeader('update_trigger')}
    <div class="am-node-config-form">
      <p class="am-hint" style="margin:0;">
        Campos a escribir en el registro disparador. El valor puede ser fijo
        o una variable, ej: <code>{{n1.email}}</code>.
      </p>
      <div id="am-field-rows" class="am-field-rows"></div>
      <button type="button" id="am-field-add" class="am-btn-secondary am-field-row-add">+ Campo</button>
    </div>`;
  renderFieldRows($('am-field-rows'), data.fields || {}, fields, (newFields) => updateSelectedNodeData({ fields: newFields }));
  $('am-field-add').addEventListener('click', () => {
    const rows = { ...(data.fields || {}), '': '' };
    updateSelectedNodeData({ fields: rows });
    renderNodeConfig();
  });
}

function renderCreateConfig(box, data) {
  box.innerHTML = `
    ${configHeader('create')}
    <div class="am-node-config-form">
      <div class="am-field">
        <label>Módulo destino</label>
        <select id="am-create-module"></select>
      </div>
      <div class="am-field">
        <label>Modelo destino</label>
        <select id="am-create-model"></select>
      </div>
      <p class="am-hint" style="margin:0;">
        Campos del registro nuevo. El valor puede ser fijo o una variable,
        ej: <code>{{trigger.nombre}}</code>.
      </p>
      <div id="am-field-rows" class="am-field-rows"></div>
      <button type="button" id="am-field-add" class="am-btn-secondary am-field-row-add">+ Campo</button>
    </div>`;

  const moduleSelect = $('am-create-module');
  const modelSelect = $('am-create-model');
  populateModuleSelect(moduleSelect);
  if (data.module) moduleSelect.value = data.module;
  populateModelSelect(modelSelect, moduleSelect.value);
  if (data.model) modelSelect.value = data.model;

  const rerenderFieldRows = () => {
    const fields = fieldsOf(moduleSelect.value, modelSelect.value);
    renderFieldRows($('am-field-rows'), data.fields || {}, fields, (newFields) => updateSelectedNodeData({ fields: newFields }));
  };

  moduleSelect.addEventListener('change', () => {
    populateModelSelect(modelSelect, moduleSelect.value);
    updateSelectedNodeData({ module: moduleSelect.value, model: modelSelect.value });
    rerenderFieldRows();
  });
  modelSelect.addEventListener('change', () => {
    updateSelectedNodeData({ model: modelSelect.value });
    rerenderFieldRows();
  });

  updateSelectedNodeData({ module: moduleSelect.value, model: modelSelect.value });
  rerenderFieldRows();

  $('am-field-add').addEventListener('click', () => {
    const rows = { ...(data.fields || {}), '': '' };
    updateSelectedNodeData({ fields: rows });
    renderNodeConfig();
  });
}

// Busca el primer registro que matchee un único filtro "campo = valor" y lo
// deja disponible como {{esteNodo.campo}} para los pasos siguientes — sin
// esto, las variables entre nodos no tendrían de dónde sacar datos más
// allá del disparador.
function renderSearchConfig(box, data) {
  box.innerHTML = `
    ${configHeader('search')}
    <div class="am-node-config-form">
      <div class="am-field">
        <label>Módulo destino</label>
        <select id="am-search-module"></select>
      </div>
      <div class="am-field">
        <label>Modelo destino</label>
        <select id="am-search-model"></select>
      </div>
      <div class="am-field">
        <label>Buscar donde el campo</label>
        <select id="am-search-field"></select>
      </div>
      <div class="am-field">
        <label>sea igual a</label>
        <input id="am-search-value" value="${esc(data.value)}" spellcheck="false" placeholder="valor o {{trigger.campo}}">
      </div>
      <p class="am-hint" style="margin:0;">Se queda con el primero que encuentre.</p>
    </div>`;

  const moduleSelect = $('am-search-module');
  const modelSelect = $('am-search-model');
  const fieldSelect = $('am-search-field');
  populateModuleSelect(moduleSelect);
  if (data.module) moduleSelect.value = data.module;
  populateModelSelect(modelSelect, moduleSelect.value);
  if (data.model) modelSelect.value = data.model;

  const rerenderFieldOptions = () => {
    const fields = fieldsOf(moduleSelect.value, modelSelect.value);
    fieldSelect.innerHTML = '<option value="">— elegir —</option>' + fields
      .map((f) => `<option value="${esc(f.name)}"${f.name === data.field ? ' selected' : ''}>${esc(f.label || f.name)}</option>`)
      .join('');
  };

  moduleSelect.addEventListener('change', () => {
    populateModelSelect(modelSelect, moduleSelect.value);
    updateSelectedNodeData({ module: moduleSelect.value, model: modelSelect.value });
    rerenderFieldOptions();
  });
  modelSelect.addEventListener('change', () => {
    updateSelectedNodeData({ model: modelSelect.value });
    rerenderFieldOptions();
  });
  fieldSelect.addEventListener('change', (e) => updateSelectedNodeData({ field: e.target.value }));
  $('am-search-value').addEventListener('input', (e) => updateSelectedNodeData({ value: e.target.value }));

  updateSelectedNodeData({ module: moduleSelect.value, model: modelSelect.value });
  rerenderFieldOptions();
}

function renderConditionConfig(box, data) {
  const fields = fieldsOf($('am-trigger-module').value, $('am-trigger-model').value);
  const fieldOptions = fields
    .map((f) => `<option value="${esc(f.name)}"${f.name === data.field ? ' selected' : ''}>${esc(f.label || f.name)}</option>`)
    .join('');

  box.innerHTML = `
    ${configHeader('condition')}
    <div class="am-node-config-form">
      <div class="am-field">
        <label>Campo del disparador</label>
        <select id="am-cond-field"><option value="">— elegir —</option>${fieldOptions}</select>
      </div>
      <div class="am-field">
        <label>Operador</label>
        <select id="am-cond-operator">
          <option value="eq"${data.operator === 'eq' ? ' selected' : ''}>Igual a</option>
          <option value="neq"${data.operator === 'neq' ? ' selected' : ''}>Distinto de</option>
          <option value="gt"${data.operator === 'gt' ? ' selected' : ''}>Mayor que (número)</option>
          <option value="lt"${data.operator === 'lt' ? ' selected' : ''}>Menor que (número)</option>
          <option value="contains"${data.operator === 'contains' ? ' selected' : ''}>Contiene</option>
        </select>
      </div>
      <div class="am-field">
        <label>Valor</label>
        <input id="am-cond-value" value="${esc(data.value)}" spellcheck="false" placeholder="valor o {{n1.campo}}">
      </div>
      <p class="am-hint" style="margin:0;">
        La salida ✓ (arriba) sigue si es verdadero; la salida ✗ (abajo) si es falso.
      </p>
    </div>`;

  $('am-cond-field').addEventListener('change', (e) => updateSelectedNodeData({ field: e.target.value }));
  $('am-cond-operator').addEventListener('change', (e) => updateSelectedNodeData({ operator: e.target.value }));
  $('am-cond-value').addEventListener('input', (e) => updateSelectedNodeData({ value: e.target.value }));
}

// Editor genérico de "campo = valor": lo usan tanto Actualizar disparador
// como Crear registro, sólo cambia de qué modelo salen las opciones de
// "campo". `rows` es el objeto {campo: valor} tal como se guarda en
// node.data.fields — un objeto, no un array, así que cada fila necesita
// poder cambiar de nombre de campo sin perder su posición visual.
function renderFieldRows(container, rows, fieldOptions, onChange) {
  const entries = Object.entries(rows);
  if (entries.length === 0) {
    container.innerHTML = '<div class="am-empty">Sin campos — agregá uno.</div>';
    return;
  }

  container.innerHTML = entries.map(([name, value], i) => {
    const options = fieldOptions
      .map((f) => `<option value="${esc(f.name)}"${f.name === name ? ' selected' : ''}>${esc(f.label || f.name)}</option>`)
      .join('');
    return `
      <div class="am-field-row" data-index="${i}">
        <select class="am-fr-name"><option value="">— campo —</option>${options}</select>
        <input class="am-fr-value" value="${esc(value)}" spellcheck="false" placeholder="valor">
        <button type="button" class="am-f-remove" title="Quitar">&times;</button>
      </div>`;
  }).join('');

  container.querySelectorAll('.am-field-row').forEach((row) => {
    const i = Number(row.dataset.index);
    const nameSelect = row.querySelector('.am-fr-name');
    const valueInput = row.querySelector('.am-fr-value');

    const sync = () => {
      const next = {};
      entries.forEach(([n, v], j) => {
        if (j === i) {
          if (nameSelect.value) next[nameSelect.value] = valueInput.value;
        } else {
          next[n] = v;
        }
      });
      onChange(next);
    };

    nameSelect.addEventListener('change', sync);
    valueInput.addEventListener('input', sync);
    row.querySelector('.am-f-remove').addEventListener('click', () => {
      const next = {};
      entries.forEach(([n, v], j) => { if (j !== i) next[n] = v; });
      onChange(next);
      renderNodeConfig();
    });
  });
}

// ========================================
// Lienzo → definición JSON (lo que entiende el motor)
// ========================================

function firstConnection(nodeData, outputKey) {
  const conns = (nodeData.outputs && nodeData.outputs[outputKey] && nodeData.outputs[outputKey].connections) || [];
  return conns.length ? String(conns[0].node) : '';
}

function graphToDefinition() {
  if (!drawflow) return { error: 'El lienzo no cargó.' };

  const data = drawflow.export().drawflow.Home.data;
  const ids = Object.keys(data);
  if (ids.length === 0) return { error: 'Agregá al menos un nodo.' };

  if (startNodeId == null || !data[startNodeId]) {
    return { error: 'Marcá qué nodo es el inicial (la ⭐ en una de las cajas).' };
  }

  const nodes = {};
  for (const id of ids) {
    const d = data[id].data;
    const node = { type: d.type };

    if (d.type === 'update_trigger') {
      node.fields = d.fields || {};
      node.next = firstConnection(data[id], 'output_1');
    } else if (d.type === 'create') {
      if (!d.module || !d.model) return { error: `A un nodo "Crear registro" le falta el módulo/modelo destino.` };
      node.module = d.module;
      node.model = d.model;
      node.fields = d.fields || {};
      node.next = firstConnection(data[id], 'output_1');
    } else if (d.type === 'search') {
      if (!d.module || !d.model) return { error: `A un nodo "Buscar registro" le falta el módulo/modelo destino.` };
      if (!d.field) return { error: `A un nodo "Buscar registro" le falta el campo del filtro.` };
      node.module = d.module;
      node.model = d.model;
      node.field = d.field;
      node.value = d.value ?? '';
      node.next = firstConnection(data[id], 'output_1');
    } else if (d.type === 'condition') {
      if (!d.field) return { error: 'A un nodo "Condición" le falta el campo a comparar.' };
      node.field = d.field;
      node.operator = d.operator || 'eq';
      node.value = d.value ?? '';
      node.on_true = firstConnection(data[id], 'output_1');
      node.on_false = firstConnection(data[id], 'output_2');
    } else {
      return { error: `Nodo con tipo desconocido: ${d.type}` };
    }

    nodes[id] = node;
  }

  return { start: String(startNodeId), nodes };
}

// ========================================
// Guardar / cargar / probar
// ========================================

function bindRunAndSave() {
  $('am-save').addEventListener('click', save);
  $('am-run').addEventListener('click', run);
  $('am-load').addEventListener('change', () => loadSaved($('am-load').value));
}

async function loadSavedList() {
  const select = $('am-load');
  try {
    const response = await fetch('/api/automatizaciones/automation', { headers: fastHeaders() });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const body = await response.json();
    const items = body.data || [];
    select.innerHTML = '<option value="">— nueva —</option>' +
      items.map((it) => `<option value="${esc(it.id)}">${esc(it.name)}</option>`).join('');
  } catch {
    // El módulo puede ser la primera vez que corre (todavía sin tabla real
    // hasta el primer Guardar) — no es un error para el usuario.
    select.innerHTML = '<option value="">— nueva —</option>';
  }
}

async function loadSaved(id) {
  if (!id) {
    currentAutomationId = null;
    webhookToken = null;
    $('am-trigger-type').value = 'manual';
    $('am-schedule-id').value = '';
    updateTriggerTypeUI();
    $('am-history-section').hidden = true;
    return;
  }
  try {
    const response = await fetch(`/api/automatizaciones/automation/${id}`, { headers: fastHeaders() });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const record = await response.json();
    currentAutomationId = id;
    $('am-name').value = record.name || '';
    if (record.target_module) {
      $('am-trigger-module').value = record.target_module;
      populateModelSelect($('am-trigger-model'), record.target_module);
    }
    if (record.target_model) $('am-trigger-model').value = record.target_model;
    loadGraphIntoCanvas(JSON.parse(record.definition || '{}'));

    webhookToken = record.webhook_token || null;
    $('am-trigger-type').value = record.trigger_type || 'manual';
    if (record.schedule_minutes) $('am-schedule-minutes').value = String(record.schedule_minutes);
    if (record.cron_trigger_module) {
      $('am-schedule-module').value = record.cron_trigger_module;
      populateModelSelect($('am-schedule-model'), record.cron_trigger_module);
    }
    if (record.cron_trigger_model) $('am-schedule-model').value = record.cron_trigger_model;
    $('am-schedule-id').value = record.cron_trigger_id || '';
    updateTriggerTypeUI();

    await loadHistory(id);
  } catch (error) {
    showFlowResult(false, 'No se pudo cargar: ' + error.message);
  }
}

// ========================================
// Disparador automático (webhook / programado)
// ========================================

function bindTriggerType() {
  $('am-trigger-type').addEventListener('change', updateTriggerTypeUI);
  $('am-webhook-copy').addEventListener('click', () => {
    $('am-webhook-url').select();
    navigator.clipboard?.writeText($('am-webhook-url').value).catch(() => {});
  });

  populateModuleSelect($('am-schedule-module'));
  $('am-schedule-module').addEventListener('change', () => populateModelSelect($('am-schedule-model'), $('am-schedule-module').value));
  populateModelSelect($('am-schedule-model'), $('am-schedule-module').value);

  updateTriggerTypeUI();
}

function updateTriggerTypeUI() {
  const type = $('am-trigger-type').value;
  $('am-trigger-webhook').hidden = type !== 'webhook';
  $('am-trigger-schedule').hidden = type !== 'schedule';

  if (type === 'webhook') {
    if (!webhookToken) webhookToken = crypto.randomUUID();
    $('am-webhook-url').value = currentAutomationId
      ? `${location.origin}/api/_automation/webhook/${currentAutomationId}?token=${webhookToken}`
      : 'Guardá la automatización para ver la URL completa.';
  }
}

// ========================================
// Historial de corridas
// ========================================

async function loadHistory(automationId) {
  const section = $('am-history-section');
  const list = $('am-history-list');
  if (!automationId) {
    section.hidden = true;
    return;
  }
  section.hidden = false;
  list.innerHTML = '<div class="am-empty">Cargando…</div>';

  try {
    const response = await fetch(
      `/api/automatizaciones/automation_run?automation_id__eq=${encodeURIComponent(automationId)}&order_by=created_at&order_dir=desc&limit=10`,
      { headers: fastHeaders() },
    );
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const body = await response.json();
    const runs = body.data || [];

    if (runs.length === 0) {
      list.innerHTML = '<div class="am-empty">Todavía no corrió.</div>';
      return;
    }

    list.innerHTML = runs.map(renderHistoryRow).join('');
    list.querySelectorAll('.am-history-summary').forEach((row) => {
      row.addEventListener('click', () => {
        const detail = row.nextElementSibling;
        if (detail) detail.hidden = !detail.hidden;
      });
    });
  } catch (error) {
    list.innerHTML = `<div class="am-empty">No se pudo cargar el historial: ${esc(error.message)}</div>`;
  }
}

function renderHistoryRow(r) {
  const when = r.created_at ? new Date(r.created_at).toLocaleString() : '';
  let steps = [];
  try { steps = JSON.parse(r.steps || '[]'); } catch { steps = []; }

  const stepLines = steps.map((s) => {
    const icon = s.ok ? '✓' : '✗';
    return `<div>${icon} <strong>${esc(s.type)}</strong> — ${esc(s.detail)}</div>`;
  }).join('') || '<div class="am-empty">Sin pasos.</div>';
  const errorLine = r.error ? `<div class="am-run-step-fail">Error: ${esc(r.error)}</div>` : '';

  return `
    <div class="am-history-row">
      <div class="am-history-summary ${r.ok ? 'am-history-summary-ok' : 'am-history-summary-fail'}">
        <span>${r.ok ? '✓' : '✗'}</span>
        <span class="am-history-source">${esc(r.source || 'manual')}</span>
        <span class="am-history-when">${esc(when)}</span>
      </div>
      <div class="am-history-detail" hidden>${stepLines}${errorLine}</div>
    </div>`;
}

// Reconstruye el lienzo a partir de una definición guardada — layout simple
// por niveles (recorrido desde "start"), no hace falta que sea bonito, sólo
// editable.
function loadGraphIntoCanvas(graph) {
  if (!drawflow || !graph.nodes) return;
  drawflow.clear();
  startNodeId = null;
  selectedNodeId = null;

  const oldToNew = {};
  const depthOf = {};
  const levels = {};
  const queue = graph.start ? [graph.start] : [];
  depthOf[graph.start] = 0;
  const seen = new Set();

  while (queue.length) {
    const oldId = queue.shift();
    if (seen.has(oldId) || !graph.nodes[oldId]) continue;
    seen.add(oldId);
    const depth = depthOf[oldId] || 0;
    levels[depth] = (levels[depth] || 0) + 1;

    const node = graph.nodes[oldId];
    const x = 60 + depth * 280;
    const y = 60 + (levels[depth] - 1) * 140;
    const newId = addNode(node.type, x, y);
    oldToNew[oldId] = newId;
    drawflow.updateNodeDataFromId(newId, {
      type: node.type,
      fields: node.fields || {},
      module: node.module || '',
      model: node.model || '',
      field: node.field || '',
      operator: node.operator || 'eq',
      value: node.value ?? '',
    });

    const nexts = node.type === 'condition' ? [node.on_true, node.on_false] : [node.next];
    nexts.forEach((nextId) => {
      if (nextId && graph.nodes[nextId]) {
        depthOf[nextId] = depth + 1;
        queue.push(nextId);
      }
    });
  }

  for (const [oldId, node] of Object.entries(graph.nodes)) {
    const fromNew = oldToNew[oldId];
    if (!fromNew) continue;
    if (node.type === 'condition') {
      if (node.on_true && oldToNew[node.on_true]) drawflow.addConnection(fromNew, oldToNew[node.on_true], 'output_1', 'input_1');
      if (node.on_false && oldToNew[node.on_false]) drawflow.addConnection(fromNew, oldToNew[node.on_false], 'output_2', 'input_1');
    } else if (node.next && oldToNew[node.next]) {
      drawflow.addConnection(fromNew, oldToNew[node.next], 'output_1', 'input_1');
    }
  }

  if (graph.start && oldToNew[graph.start] != null) markStart(oldToNew[graph.start]);
  renderNodeConfig();
}

function showFlowResult(ok, message) {
  const box = $('am-flow-result');
  box.hidden = false;
  box.className = 'am-result ' + (ok ? 'am-result-ok' : 'am-result-error');
  box.textContent = message;
}

async function save() {
  const graph = graphToDefinition();
  if (graph.error) return showFlowResult(false, graph.error);

  const name = $('am-name').value.trim();
  if (!name) return showFlowResult(false, 'Falta el nombre.');

  const triggerType = $('am-trigger-type').value;
  if (triggerType === 'schedule' && !$('am-schedule-id').value.trim()) {
    return showFlowResult(false, 'El disparador programado necesita el ID de un registro fijo contra el cual correr.');
  }

  const button = $('am-save');
  button.disabled = true;
  try {
    const payload = {
      name,
      target_module: $('am-trigger-module').value,
      target_model: $('am-trigger-model').value,
      definition: JSON.stringify(graph),
      active: true,
      trigger_type: triggerType,
    };
    if (triggerType === 'webhook') {
      if (!webhookToken) webhookToken = crypto.randomUUID();
      payload.webhook_token = webhookToken;
    }
    if (triggerType === 'schedule') {
      payload.schedule_minutes = Number($('am-schedule-minutes').value) || 60;
      payload.cron_trigger_module = $('am-schedule-module').value;
      payload.cron_trigger_model = $('am-schedule-model').value;
      payload.cron_trigger_id = $('am-schedule-id').value.trim();
    }

    const isUpdate = Boolean(currentAutomationId);
    const url = isUpdate ? `/api/automatizaciones/automation/${currentAutomationId}` : '/api/automatizaciones/automation';
    const response = await fetch(url, {
      method: isUpdate ? 'PUT' : 'POST',
      headers: fastHeaders(),
      body: JSON.stringify(payload),
    });
    const body = await response.json();
    if (!response.ok) return showFlowResult(false, body.error || 'No se pudo guardar.');

    if (!isUpdate) currentAutomationId = body.id || body.data?.id || null;
    showFlowResult(true, `✓ Guardada como "${name}".`);
    await loadSavedList();
    if (currentAutomationId) $('am-load').value = currentAutomationId;
    updateTriggerTypeUI(); // ahora currentAutomationId ya existe: completa la URL del webhook
    await loadHistory(currentAutomationId);
  } catch (error) {
    showFlowResult(false, 'Error: ' + error.message);
  } finally {
    button.disabled = false;
  }
}

async function run() {
  const graph = graphToDefinition();
  if (graph.error) return showFlowResult(false, graph.error);

  const triggerModule = $('am-trigger-module').value;
  const triggerModel = $('am-trigger-model').value;
  const triggerID = $('am-run-id').value.trim();
  const resultBox = $('am-run-result');

  if (!triggerModule || !triggerModel || !triggerID) {
    resultBox.hidden = false;
    resultBox.className = 'am-result am-result-error';
    resultBox.textContent = 'Elegí módulo/modelo disparador y escribí el ID del registro a probar.';
    return;
  }

  const button = $('am-run');
  button.disabled = true;
  resultBox.hidden = false;
  resultBox.className = 'am-result';
  resultBox.textContent = 'Ejecutando…';

  try {
    const response = await fetch('/api/_automation/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        // automation_id (si ya está guardada) es sólo para que el historial
        // quede asociado — la definición inline es la que manda, así
        // "Probar" siempre corre el lienzo tal como está, con ediciones
        // todavía sin guardar (ver resolveGraph en automation.go).
        automation_id: currentAutomationId || undefined,
        definition: graph,
        trigger: { module: triggerModule, model: triggerModel, id: triggerID },
      }),
    });
    const body = await response.json();

    if (!response.ok) {
      resultBox.className = 'am-result am-result-error';
      resultBox.textContent = body.error || 'No se pudo ejecutar.';
      return;
    }

    const steps = body.steps || [];
    const lines = steps.map((s) => {
      const icon = s.ok ? '✓' : '✗';
      const cls = s.ok ? '' : ' am-run-step-fail';
      return `<div class="am-run-step${cls}">${icon} <strong>${esc(s.type)}</strong> — ${esc(s.detail)}</div>`;
    }).join('');

    resultBox.className = 'am-result ' + (body.error ? 'am-result-error' : 'am-result-ok');
    resultBox.innerHTML = (lines || '<div class="am-empty">Sin pasos ejecutados.</div>')
      + (body.error ? `<div class="am-run-step-fail">Terminó con error: ${esc(body.error)}</div>` : '');

    if (currentAutomationId) await loadHistory(currentAutomationId);
  } catch (error) {
    resultBox.className = 'am-result am-result-error';
    resultBox.textContent = 'Error: ' + error.message;
  } finally {
    button.disabled = false;
  }
}
