// ========================================
// Importaciones (con costeo real / landed cost)
// ========================================
// Documento (import_order) + líneas, más los gastos de importación (flete,
// aduana, seguro, otros). El "costo real por unidad" se reparte proporcional
// al valor FOB de cada línea y se muestra en vivo mientras cargás. El cálculo
// definitivo lo hace el backend al "Recibir"
// (/api/_importaciones/import_order/{id}/receive): ahí mueve stock positivo y
// graba el costo real en cada producto. Las etapas (en tránsito / aduana /
// cancelar) son cambios de estado por el endpoint genérico de transición.

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

const money = (n) => '$' + Number(n || 0).toLocaleString('es-EC', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const money4 = (n) => '$' + Number(n || 0).toLocaleString('es-EC', { minimumFractionDigits: 2, maximumFractionDigits: 4 });

const STATUS_LABEL = { draft: 'Borrador', in_transit: 'En tránsito', customs: 'En aduana', received: 'Recibida', cancelled: 'Cancelada' };

let products = [];
let suppliers = [];
let imports = [];
let currentId = null;
let currentStatus = 'draft';
let currentLines = [];

function productName(id) {
  const p = products.find((x) => x.id === id);
  return p ? `${p.name} (${p.sku})` : id;
}

// ========================================
// Cargas de apoyo
// ========================================

async function loadProducts() {
  try {
    const response = await fetch('/api/products/product?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() });
    products = response.ok ? ((await response.json()).data || []) : [];
  } catch {
    products = [];
  }
  $('im-line-product').innerHTML = products.map((p) => `<option value="${esc(p.id)}">${esc(p.name)} (${esc(p.sku)})</option>`).join('');
}

async function loadSuppliers() {
  try {
    const response = await fetch('/api/contacts/contact?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() });
    const all = response.ok ? ((await response.json()).data || []) : [];
    const marked = all.filter((c) => c.is_supplier);
    suppliers = marked.length ? marked : all;
  } catch {
    suppliers = [];
  }
  $('im-supplier').innerHTML = '<option value="">— Elegí un proveedor —</option>'
    + suppliers.map((c) => `<option value="${esc(c.id)}">${esc(c.name)}</option>`).join('');
}

// ========================================
// Lista
// ========================================

async function loadImports() {
  try {
    const response = await fetch('/api/importaciones/import_order?limit=100&order_by=created_at&order_dir=desc', { headers: fastHeaders() });
    imports = response.ok ? ((await response.json()).data || []) : [];
  } catch {
    imports = [];
  }
  renderList();
}

function renderList() {
  const box = $('im-list');
  if (!imports.length) {
    box.innerHTML = '<div class="im-empty">Todavía no hay importaciones.</div>';
    return;
  }
  box.innerHTML = imports.map((o) => `
    <button type="button" class="im-list-item${o.id === currentId ? ' im-list-active' : ''}" data-id="${esc(o.id)}">
      <span class="im-list-title">${esc(o.reference || o.supplier_name || 'Importación')}</span>
      <span class="im-list-sub">
        <span class="im-badge im-status-${esc(o.status)}">${esc(STATUS_LABEL[o.status] || o.status)}</span>
        <span>${money(o.landed_total || o.goods_total)}</span>
      </span>
    </button>`).join('');

  for (const btn of box.querySelectorAll('.im-list-item')) {
    btn.addEventListener('click', () => selectImport(btn.dataset.id));
  }
}

// ========================================
// Editor
// ========================================

function newImport() {
  currentId = null;
  currentStatus = 'draft';
  currentLines = [];
  $('im-empty').hidden = true;
  $('im-form').hidden = false;
  $('im-lines-section').hidden = true;
  $('im-supplier').value = '';
  $('im-reference').value = '';
  $('im-currency').value = 'USD';
  $('im-eta').value = '';
  setCosts({ freight_cost: 0, customs_cost: 0, insurance_cost: 0, other_cost: 0 });
  $('im-error').hidden = true;
  updateActions();
  renderList();
}

async function selectImport(id) {
  const order = imports.find((o) => o.id === id);
  if (!order) return;
  currentId = id;
  currentStatus = order.status || 'draft';

  $('im-empty').hidden = true;
  $('im-form').hidden = false;
  $('im-lines-section').hidden = false;
  $('im-error').hidden = true;
  $('im-supplier').value = order.supplier_id || '';
  $('im-reference').value = order.reference || '';
  $('im-currency').value = order.currency || 'USD';
  $('im-eta').value = order.eta || '';
  setCosts(order);

  updateActions();
  renderList();
  await loadLines(id);
}

async function saveImport() {
  const supplierId = $('im-supplier').value;
  const supplier = suppliers.find((s) => s.id === supplierId);
  const payload = {
    supplier_id: supplierId,
    supplier_name: supplier ? supplier.name : '',
    reference: $('im-reference').value.trim(),
    currency: $('im-currency').value.trim() || 'USD',
    eta: $('im-eta').value || null,
  };
  if (!supplierId) {
    showError('Elegí un proveedor.');
    return;
  }
  try {
    if (currentId) {
      const response = await fetch(`/api/importaciones/import_order/${currentId}`, {
        method: 'PUT', headers: fastHeaders(), body: JSON.stringify(payload),
      });
      if (!response.ok) throw new Error(await errorText(response));
      await loadImports();
      await selectImport(currentId);
    } else {
      const response = await fetch('/api/importaciones/import_order', {
        method: 'POST', headers: fastHeaders(),
        body: JSON.stringify({ ...payload, freight_cost: 0, customs_cost: 0, insurance_cost: 0, other_cost: 0, goods_total: 0, landed_total: 0 }),
      });
      if (!response.ok) throw new Error(await errorText(response));
      currentId = (await response.json()).id;
      await loadImports();
      await selectImport(currentId);
    }
  } catch (error) {
    showError(error.message);
  }
}

async function deleteImport() {
  if (!currentId) return;
  if (!confirm('¿Eliminar esta importación y sus líneas?')) return;
  try {
    for (const line of currentLines) {
      await fetch(`/api/importaciones/import_line/${line.id}`, { method: 'DELETE', headers: fastHeaders() });
    }
    await fetch(`/api/importaciones/import_order/${currentId}`, { method: 'DELETE', headers: fastHeaders() });
    currentId = null;
    currentLines = [];
    $('im-form').hidden = true;
    $('im-empty').hidden = false;
    await loadImports();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

function showError(message) {
  const box = $('im-error');
  box.textContent = message;
  box.hidden = false;
}

// ========================================
// Costos + preview de landed cost
// ========================================

function getCosts() {
  return {
    freight_cost: Number($('im-freight').value) || 0,
    customs_cost: Number($('im-customs-cost').value) || 0,
    insurance_cost: Number($('im-insurance').value) || 0,
    other_cost: Number($('im-other').value) || 0,
  };
}

function setCosts(obj) {
  $('im-freight').value = Number(obj.freight_cost || 0);
  $('im-customs-cost').value = Number(obj.customs_cost || 0);
  $('im-insurance').value = Number(obj.insurance_cost || 0);
  $('im-other').value = Number(obj.other_cost || 0);
}

// Reparte los gastos proporcional al FOB de cada línea. Mismo cálculo que el
// backend hace al recibir, para que lo que ves sea lo que se va a guardar.
function computePreview() {
  const costs = getCosts();
  const extra = costs.freight_cost + costs.customs_cost + costs.insurance_cost + costs.other_cost;
  const goods = currentLines.reduce((sum, l) => sum + Number(l.line_fob || 0), 0);

  for (const line of currentLines) {
    const lineFob = Number(line.line_fob || 0);
    const qty = Number(line.quantity || 0);
    const share = goods > 0 ? extra * (lineFob / goods) : 0;
    const landedUnit = qty > 0 ? Number(line.unit_fob || 0) + share / qty : 0;
    const cell = document.querySelector(`.im-line-landed[data-id="${line.id}"]`);
    if (cell) {
      cell.textContent = (currentStatus === 'received' && line.landed_unit_cost)
        ? money4(line.landed_unit_cost)
        : money4(landedUnit);
    }
  }

  $('im-sum-fob').textContent = money(goods);
  $('im-sum-extra').textContent = money(extra);
  $('im-sum-landed').textContent = money(goods + extra);
}

// Persistir gastos + totales informativos (al salir del campo).
async function syncCosts() {
  if (!currentId) return;
  const costs = getCosts();
  const extra = costs.freight_cost + costs.customs_cost + costs.insurance_cost + costs.other_cost;
  const goods = currentLines.reduce((sum, l) => sum + Number(l.line_fob || 0), 0);
  await fetch(`/api/importaciones/import_order/${currentId}`, {
    method: 'PUT',
    headers: fastHeaders(),
    body: JSON.stringify({ ...costs, goods_total: goods, landed_total: goods + extra }),
  });
  await loadImports();
  renderList();
}

// ========================================
// Líneas
// ========================================

async function loadLines(orderId) {
  try {
    const response = await fetch(`/api/importaciones/import_line?import_order_id__eq=${encodeURIComponent(orderId)}&limit=100`, { headers: fastHeaders() });
    currentLines = response.ok ? ((await response.json()).data || []) : [];
  } catch {
    currentLines = [];
  }
  renderLines();
}

function renderLines() {
  const box = $('im-lines');
  const editable = currentStatus !== 'received' && currentStatus !== 'cancelled';

  box.innerHTML = currentLines.length
    ? currentLines.map((line) => `
      <div class="im-line-row">
        <span class="im-line-name">${esc(line.product_name || productName(line.product_id))}</span>
        <span class="im-line-fob">${esc(line.quantity)} × ${money(line.unit_fob)} FOB</span>
        <span class="im-line-landed" data-id="${esc(line.id)}" title="Costo real por unidad">—</span>
        ${editable ? `<button type="button" class="im-line-delete" data-id="${esc(line.id)}" title="Quitar">&times;</button>` : '<span></span>'}
      </div>`).join('')
    : '<div class="im-empty">Sin productos todavía.</div>';

  for (const btn of box.querySelectorAll('.im-line-delete')) {
    btn.addEventListener('click', () => removeLine(btn.dataset.id));
  }

  $('im-add-line').style.display = editable ? '' : 'none';
  computePreview();
}

async function addLine() {
  if (!currentId) { showError('Guardá primero los datos de la importación.'); return; }
  const productId = $('im-line-product').value;
  const qty = Number($('im-line-qty').value);
  const fob = Number($('im-line-fob').value);
  if (!productId || !qty || qty <= 0) {
    alert('Elegí un producto y una cantidad mayor a 0.');
    return;
  }
  const product = products.find((p) => p.id === productId);
  try {
    const response = await fetch('/api/importaciones/import_line', {
      method: 'POST',
      headers: fastHeaders(),
      body: JSON.stringify({
        import_order_id: currentId,
        product_id: productId,
        product_name: product ? product.name : '',
        quantity: qty,
        unit_fob: fob,
        line_fob: qty * fob,
      }),
    });
    if (!response.ok) throw new Error(await errorText(response));
    $('im-line-qty').value = '';
    $('im-line-fob').value = '';
    await loadLines(currentId);
    await syncCosts();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

async function removeLine(id) {
  try {
    await fetch(`/api/importaciones/import_line/${id}`, { method: 'DELETE', headers: fastHeaders() });
    await loadLines(currentId);
    await syncCosts();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

// ========================================
// Estados / acciones
// ========================================

function updateActions() {
  $('im-status').textContent = STATUS_LABEL[currentStatus] || currentStatus;
  $('im-status').className = 'im-badge im-status-' + currentStatus;
  $('im-send').hidden = currentStatus !== 'draft' || !currentId;
  $('im-customs').hidden = currentStatus !== 'in_transit';
  $('im-receive').hidden = !(currentStatus === 'customs' || currentStatus === 'in_transit');
  $('im-cancel').hidden = !(currentStatus === 'draft' || currentStatus === 'in_transit' || currentStatus === 'customs') || !currentId;
  $('im-delete').hidden = currentStatus === 'received';
}

async function transition(action) {
  if (!currentId) return;
  try {
    const response = await fetch(`/api/importaciones/import_order/${currentId}/transition`, {
      method: 'POST', headers: fastHeaders(), body: JSON.stringify({ action }),
    });
    if (!response.ok) throw new Error(await errorText(response));
    await loadImports();
    await selectImport(currentId);
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

async function receive() {
  if (!currentId) return;
  if (!confirm('¿Recibir la importación? Suma el stock con el costo real calculado.')) return;
  $('im-receive').disabled = true;
  try {
    const response = await fetch(`/api/_importaciones/import_order/${currentId}/receive`, { method: 'POST' });
    if (!response.ok) throw new Error(await errorText(response));
    const body = await response.json();
    const failed = (body.steps || []).filter((s) => !s.ok);
    if (failed.length) {
      alert('Recibida, pero con avisos de stock:\n' + failed.map((s) => s.detail).join('\n'));
    }
    await loadImports();
    await selectImport(currentId);
  } catch (error) {
    alert('Error: ' + error.message);
  } finally {
    $('im-receive').disabled = false;
  }
}

// ========================================
// Arranque
// ========================================

async function init() {
  await loadProducts();
  await loadSuppliers();
  await loadImports();

  $('im-new').addEventListener('click', newImport);
  $('im-save').addEventListener('click', saveImport);
  $('im-delete').addEventListener('click', deleteImport);
  $('im-line-add').addEventListener('click', addLine);
  $('im-send').addEventListener('click', () => transition('enviar'));
  $('im-customs').addEventListener('click', () => transition('aduana'));
  $('im-cancel').addEventListener('click', () => transition('cancelar'));
  $('im-receive').addEventListener('click', receive);

  for (const id of ['im-freight', 'im-customs-cost', 'im-insurance', 'im-other']) {
    $(id).addEventListener('input', computePreview);
    $(id).addEventListener('change', syncCosts);
  }
}

init();
