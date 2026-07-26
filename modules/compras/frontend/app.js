// ========================================
// Compras
// ========================================
// Documento (compra) + líneas, calcado del editor de Recetas de Operaciones.
// El stock NO se toca desde acá: sube recién al "Recibir", que pega contra el
// endpoint propio /api/_compras/purchase/{id}/receive (con sesión), que mueve
// stock positivo en products y actualiza el costo del producto.
// "Pedir" y "Cancelar" son sólo cambios de estado → endpoint genérico de
// transición.

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

const STATUS_LABEL = { draft: 'Borrador', ordered: 'Pedida', received: 'Recibida', cancelled: 'Cancelada' };

let products = [];
let suppliers = [];
let purchases = [];
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
  $('cp-line-product').innerHTML = products.map((p) => `<option value="${esc(p.id)}">${esc(p.name)} (${esc(p.sku)})</option>`).join('');
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
  $('cp-supplier').innerHTML = '<option value="">— Elegí un proveedor —</option>'
    + suppliers.map((c) => `<option value="${esc(c.id)}">${esc(c.name)}</option>`).join('');
}

// ========================================
// Lista
// ========================================

async function loadPurchases() {
  try {
    const response = await fetch('/api/compras/purchase?limit=100&order_by=created_at&order_dir=desc', { headers: fastHeaders() });
    purchases = response.ok ? ((await response.json()).data || []) : [];
  } catch {
    purchases = [];
  }
  renderList();
}

function renderList() {
  const box = $('cp-list');
  if (!purchases.length) {
    box.innerHTML = '<div class="cp-empty">Todavía no hay compras.</div>';
    return;
  }
  box.innerHTML = purchases.map((p) => `
    <button type="button" class="cp-list-item${p.id === currentId ? ' cp-list-active' : ''}" data-id="${esc(p.id)}">
      <span class="cp-list-title">${esc(p.supplier_name || 'Sin proveedor')}</span>
      <span class="cp-list-sub">
        <span class="cp-badge cp-status-${esc(p.status)}">${esc(STATUS_LABEL[p.status] || p.status)}</span>
        <span>${money(p.total)}</span>
      </span>
    </button>`).join('');

  for (const btn of box.querySelectorAll('.cp-list-item')) {
    btn.addEventListener('click', () => selectPurchase(btn.dataset.id));
  }
}

// ========================================
// Editor
// ========================================

function newPurchase() {
  currentId = null;
  currentStatus = 'draft';
  currentLines = [];
  $('cp-empty').hidden = true;
  $('cp-form').hidden = false;
  $('cp-lines-section').hidden = true;
  $('cp-supplier').value = '';
  $('cp-date').value = '';
  $('cp-error').hidden = true;
  updateActions();
  renderList();
}

async function selectPurchase(id) {
  const purchase = purchases.find((p) => p.id === id);
  if (!purchase) return;
  currentId = id;
  currentStatus = purchase.status || 'draft';

  $('cp-empty').hidden = true;
  $('cp-form').hidden = false;
  $('cp-lines-section').hidden = false;
  $('cp-error').hidden = true;
  $('cp-supplier').value = purchase.supplier_id || '';
  $('cp-date').value = purchase.expected_date || '';

  updateActions();
  renderList();
  await loadLines(id);
}

async function savePurchase() {
  const supplierId = $('cp-supplier').value;
  const supplier = suppliers.find((s) => s.id === supplierId);
  const payload = {
    supplier_id: supplierId,
    supplier_name: supplier ? supplier.name : '',
    expected_date: $('cp-date').value || null,
  };
  if (!supplierId) {
    showError('Elegí un proveedor.');
    return;
  }
  try {
    if (currentId) {
      const response = await fetch(`/api/compras/purchase/${currentId}`, {
        method: 'PUT', headers: fastHeaders(), body: JSON.stringify(payload),
      });
      if (!response.ok) throw new Error(await errorText(response));
      await loadPurchases();
      await selectPurchase(currentId);
    } else {
      const response = await fetch('/api/compras/purchase', {
        method: 'POST', headers: fastHeaders(), body: JSON.stringify({ ...payload, total: 0 }),
      });
      if (!response.ok) throw new Error(await errorText(response));
      currentId = (await response.json()).id;
      await loadPurchases();
      await selectPurchase(currentId);
    }
  } catch (error) {
    showError(error.message);
  }
}

async function deletePurchase() {
  if (!currentId) return;
  if (!confirm('¿Eliminar esta compra y sus líneas?')) return;
  try {
    for (const line of currentLines) {
      await fetch(`/api/compras/purchase_line/${line.id}`, { method: 'DELETE', headers: fastHeaders() });
    }
    await fetch(`/api/compras/purchase/${currentId}`, { method: 'DELETE', headers: fastHeaders() });
    currentId = null;
    currentLines = [];
    $('cp-form').hidden = true;
    $('cp-empty').hidden = false;
    await loadPurchases();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

function showError(message) {
  const box = $('cp-error');
  box.textContent = message;
  box.hidden = false;
}

// ========================================
// Líneas
// ========================================

async function loadLines(purchaseId) {
  try {
    const response = await fetch(`/api/compras/purchase_line?purchase_id__eq=${encodeURIComponent(purchaseId)}&limit=100`, { headers: fastHeaders() });
    currentLines = response.ok ? ((await response.json()).data || []) : [];
  } catch {
    currentLines = [];
  }
  renderLines();
}

function renderLines() {
  const box = $('cp-lines');
  const editable = currentStatus === 'draft' || currentStatus === 'ordered';

  box.innerHTML = currentLines.length
    ? currentLines.map((line) => `
      <div class="cp-line-row">
        <span class="cp-line-name">${esc(line.product_name || productName(line.product_id))}</span>
        <span class="cp-line-qty">${esc(line.quantity)} × ${money(line.unit_cost)}</span>
        <span class="cp-line-total">${money(line.line_total)}</span>
        ${editable ? `<button type="button" class="cp-line-delete" data-id="${esc(line.id)}" title="Quitar">&times;</button>` : '<span></span>'}
      </div>`).join('')
    : '<div class="cp-empty">Sin productos todavía.</div>';

  for (const btn of box.querySelectorAll('.cp-line-delete')) {
    btn.addEventListener('click', () => removeLine(btn.dataset.id));
  }

  $('cp-add-line').style.display = editable ? '' : 'none';
  $('cp-total').textContent = money(currentLines.reduce((sum, l) => sum + Number(l.line_total || 0), 0));
}

async function addLine() {
  if (!currentId) { showError('Guardá primero los datos de la compra.'); return; }
  const productId = $('cp-line-product').value;
  const qty = Number($('cp-line-qty').value);
  const cost = Number($('cp-line-cost').value);
  if (!productId || !qty || qty <= 0) {
    alert('Elegí un producto y una cantidad mayor a 0.');
    return;
  }
  const product = products.find((p) => p.id === productId);
  try {
    const response = await fetch('/api/compras/purchase_line', {
      method: 'POST',
      headers: fastHeaders(),
      body: JSON.stringify({
        purchase_id: currentId,
        product_id: productId,
        product_name: product ? product.name : '',
        quantity: qty,
        unit_cost: cost,
        line_total: qty * cost,
      }),
    });
    if (!response.ok) throw new Error(await errorText(response));
    $('cp-line-qty').value = '';
    $('cp-line-cost').value = '';
    await loadLines(currentId);
    await syncTotal();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

async function removeLine(id) {
  try {
    await fetch(`/api/compras/purchase_line/${id}`, { method: 'DELETE', headers: fastHeaders() });
    await loadLines(currentId);
    await syncTotal();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

// Mantener purchase.total al día para que la lista muestre el monto correcto.
async function syncTotal() {
  if (!currentId) return;
  const total = currentLines.reduce((sum, l) => sum + Number(l.line_total || 0), 0);
  await fetch(`/api/compras/purchase/${currentId}`, {
    method: 'PUT', headers: fastHeaders(), body: JSON.stringify({ total }),
  });
  await loadPurchases();
  renderList();
}

// ========================================
// Estados / acciones
// ========================================

function updateActions() {
  $('cp-status').textContent = STATUS_LABEL[currentStatus] || currentStatus;
  $('cp-status').className = 'cp-badge cp-status-' + currentStatus;
  $('cp-order').hidden = currentStatus !== 'draft' || !currentId;
  $('cp-receive').hidden = currentStatus !== 'ordered';
  $('cp-cancel').hidden = !(currentStatus === 'draft' || currentStatus === 'ordered') || !currentId;
  $('cp-delete').hidden = currentStatus === 'received';
}

async function transition(action) {
  if (!currentId) return;
  try {
    const response = await fetch(`/api/compras/purchase/${currentId}/transition`, {
      method: 'POST', headers: fastHeaders(), body: JSON.stringify({ action }),
    });
    if (!response.ok) throw new Error(await errorText(response));
    await loadPurchases();
    await selectPurchase(currentId);
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

async function receive() {
  if (!currentId) return;
  if (!confirm('¿Marcar como recibida? Esto suma el stock de los productos.')) return;
  $('cp-receive').disabled = true;
  try {
    const response = await fetch(`/api/_compras/purchase/${currentId}/receive`, { method: 'POST' });
    if (!response.ok) throw new Error(await errorText(response));
    const body = await response.json();
    const failed = (body.steps || []).filter((s) => !s.ok);
    if (failed.length) {
      alert('Recibida, pero con avisos de stock:\n' + failed.map((s) => s.detail).join('\n'));
    }
    await loadPurchases();
    await selectPurchase(currentId);
  } catch (error) {
    alert('Error: ' + error.message);
  } finally {
    $('cp-receive').disabled = false;
  }
}

// ========================================
// Arranque
// ========================================

async function init() {
  await loadProducts();
  await loadSuppliers();
  await loadPurchases();

  $('cp-new').addEventListener('click', newPurchase);
  $('cp-save').addEventListener('click', savePurchase);
  $('cp-delete').addEventListener('click', deletePurchase);
  $('cp-line-add').addEventListener('click', addLine);
  $('cp-order').addEventListener('click', () => transition('pedir'));
  $('cp-cancel').addEventListener('click', () => transition('cancelar'));
  $('cp-receive').addEventListener('click', receive);
}

init();
