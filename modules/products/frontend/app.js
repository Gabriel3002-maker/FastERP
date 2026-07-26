// ========================================
// Productos e Inventario
// ========================================
// Pestaña "Catálogo": el CRUD sale gratis de fast-views.js (ver
// <div data-fast-view> en index.html).
//
// Pestaña "Stock": lo que fast-views no puede dar. Muestra TODOS los
// productos con su stock a la vista (antes había que elegir de a uno en un
// desplegable para enterarte de cuánto tenías), y las entradas/salidas se
// cargan con dos botones — el signo lo pone este código, no la persona.
// Guardar "-5" a mano para decir "salieron 5" es justo el tipo de detalle
// que hace que un sistema se sienta "para expertos".
//
// El stock es siempre la suma de los movimientos, nunca un contador
// guardado aparte que se pueda desincronizar.

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

const fmt = (n) => Number(n || 0).toLocaleString('es-EC', { maximumFractionDigits: 2 });

// Carga perezosa de librerías vendorizadas (mismo patrón que automatizaciones
// con Drawflow): el .js vive en el core, se trae recién cuando hace falta.
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

let products = [];
const stockByProduct = new Map(); // product_id -> stock actual (suma de movimientos)
let currentProductId = null;

// ========================================
// Lista de existencias
// ========================================

// Se traen todos los movimientos de una y se suman por producto, en vez de
// pedir los de cada producto por separado: una consulta en lugar de N.
async function loadStock() {
  const box = $('pr-stock-list');
  try {
    const [productsRes, movesRes] = await Promise.all([
      fetch('/api/products/product?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() }),
      fetch('/api/products/stock_move?limit=100&order_by=created_at&order_dir=desc', { headers: fastHeaders() }),
    ]);
    if (!productsRes.ok) throw new Error(await errorText(productsRes));
    if (!movesRes.ok) throw new Error(await errorText(movesRes));

    products = (await productsRes.json()).data || [];
    const moves = (await movesRes.json()).data || [];

    stockByProduct.clear();
    for (const move of moves) {
      const current = stockByProduct.get(move.product_id) || 0;
      stockByProduct.set(move.product_id, current + Number(move.quantity || 0));
    }

    renderStockList();
    renderStockChart();
  } catch (error) {
    box.innerHTML = `<div class="pr-empty pr-error">${esc(error.message)}</div>`;
  }
}

// ========================================
// Gráfico de stock (echarts, barras horizontales)
// ========================================
// echarts se vendoriza en el core (/static/vendor/echarts) y se carga la
// primera vez que se ve la pestaña Stock. Las barras salen de stockByProduct
// (la misma suma de movimientos), así el gráfico refleja al instante lo que
// se vendió/compró. Los colores se toman de las variables del tema del core.

let stockChart = null;
let echartsLoading = null;

function ensureECharts() {
  if (window.echarts) return Promise.resolve();
  if (!echartsLoading) {
    echartsLoading = loadScript('/static/vendor/echarts/echarts.min.js')
      .catch(() => { echartsLoading = null; });
  }
  return echartsLoading;
}

async function renderStockChart() {
  const box = $('pr-stock-chart');
  if (!box) return;
  // Si la pestaña todavía está oculta, el contenedor mide 0 y echarts no puede
  // dibujar: se difiere hasta que la pestaña Stock se muestre (fast:tab).
  if (box.offsetWidth === 0) return;

  await ensureECharts();
  if (!window.echarts) {
    box.innerHTML = '<div class="pr-empty">No se pudo cargar el gráfico.</div>';
    return;
  }

  const rows = products
    .map((p) => ({ name: p.name || '(sin nombre)', stock: stockByProduct.get(p.id) || 0, reorder: Number(p.reorder_point || 0) }))
    .sort((a, b) => b.stock - a.stock)
    .slice(0, 20);

  if (!rows.length) {
    box.style.height = '';
    box.innerHTML = '<div class="pr-empty">Cargá productos para ver el gráfico.</div>';
    return;
  }

  const css = getComputedStyle(document.documentElement);
  const cvar = (name, fallback) => (css.getPropertyValue(name).trim() || fallback);
  const accent = cvar('--primary', '#2563eb');
  const danger = cvar('--error', '#dc2626');
  const warn = cvar('--warning', '#d97706');
  const textCol = cvar('--text-secondary', '#6b7280');
  const gridCol = cvar('--border', '#e5e7eb');

  // Altura según cantidad de barras (horizontales).
  box.style.height = Math.max(240, rows.length * 30 + 30) + 'px';

  if (!stockChart) stockChart = window.echarts.init(box);

  const categories = rows.map((r) => r.name).reverse();
  const values = rows.map((r) => {
    let color = accent;
    if (r.stock <= 0) color = danger;
    else if (r.reorder > 0 && r.stock <= r.reorder) color = warn;
    return { value: r.stock, itemStyle: { color, borderRadius: [0, 4, 4, 0] } };
  }).reverse();

  stockChart.setOption({
    grid: { left: 8, right: 40, top: 10, bottom: 8, containLabel: true },
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    xAxis: {
      type: 'value',
      axisLabel: { color: textCol },
      splitLine: { lineStyle: { color: gridCol } },
    },
    yAxis: {
      type: 'category',
      data: categories,
      axisLabel: { color: textCol },
      axisLine: { lineStyle: { color: gridCol } },
      axisTick: { show: false },
    },
    series: [{
      type: 'bar',
      data: values,
      barMaxWidth: 22,
      label: { show: true, position: 'right', color: textCol, formatter: (p) => fmt(p.value) },
    }],
  });
  stockChart.resize();
}

function renderStockList() {
  const box = $('pr-stock-list');
  const term = $('pr-stock-search').value.trim().toLowerCase();

  const visible = products.filter((p) => !term
    || (p.name || '').toLowerCase().includes(term)
    || (p.sku || '').toLowerCase().includes(term));

  if (!visible.length) {
    box.innerHTML = `<div class="pr-empty">${
      term ? 'Sin resultados.' : 'Todavía no hay productos. Cargalos en la pestaña Catálogo.'
    }</div>`;
    return;
  }

  box.innerHTML = visible.map((p) => {
    const stock = stockByProduct.get(p.id) || 0;
    // El punto de reorden es opcional: sin él no hay nada que avisar.
    const reorder = Number(p.reorder_point || 0);
    const low = reorder > 0 && stock <= reorder;
    return `
      <button type="button" class="pr-stock-item${p.id === currentProductId ? ' pr-stock-active' : ''}" data-id="${esc(p.id)}">
        <span class="pr-stock-name">${esc(p.name)}</span>
        <span class="pr-stock-sku">${esc(p.sku)}</span>
        <span class="pr-stock-qty${low ? ' pr-stock-low' : ''}" ${low ? 'title="Por debajo del punto de reorden"' : ''}>
          ${low ? '⚠ ' : ''}${fmt(stock)}
        </span>
      </button>`;
  }).join('');

  for (const btn of box.querySelectorAll('.pr-stock-item')) {
    btn.addEventListener('click', () => selectProduct(btn.dataset.id));
  }
}

// ========================================
// Detalle de un producto
// ========================================

function selectProduct(id) {
  const product = products.find((p) => p.id === id);
  if (!product) return;

  currentProductId = id;
  $('pr-detail-empty').hidden = true;
  $('pr-detail').hidden = false;
  $('pr-detail-name').textContent = product.name || '';
  $('pr-detail-sku').textContent = product.sku || '';
  $('pr-detail-total').textContent = fmt(stockByProduct.get(id) || 0);

  renderStockList(); // refresca el resaltado del elegido
  loadMoves();
}

async function loadMoves() {
  const list = $('pr-moves-list');
  if (!currentProductId) return;
  list.innerHTML = '<div class="pr-empty">Cargando…</div>';

  try {
    const response = await fetch(
      `/api/products/stock_move?product_id__eq=${encodeURIComponent(currentProductId)}&order_by=created_at&order_dir=desc&limit=100`,
      { headers: fastHeaders() },
    );
    if (!response.ok) throw new Error(await errorText(response));
    const moves = (await response.json()).data || [];

    list.innerHTML = moves.length
      ? moves.map((m) => {
        const qty = Number(m.quantity || 0);
        const entered = qty >= 0;
        const when = m.created_at ? new Date(m.created_at).toLocaleString('es-EC') : '';
        return `
          <div class="pr-move-row">
            <span class="pr-move-dir ${entered ? 'pr-move-in-tag' : 'pr-move-out-tag'}">
              ${entered ? '↓ Entró' : '↑ Salió'}
            </span>
            <span class="pr-move-qty">${fmt(Math.abs(qty))}</span>
            <span class="pr-move-reason">${esc(REASON_LABEL[m.reason] || m.reason || '')}</span>
            <span class="pr-move-ref">${esc(m.reference || '')}</span>
            <span class="pr-move-when">${esc(when)}</span>
            <button type="button" class="pr-move-delete" data-id="${esc(m.id)}" title="Borrar">&times;</button>
          </div>`;
      }).join('')
      : '<div class="pr-empty">Sin movimientos todavía.</div>';

    for (const btn of list.querySelectorAll('.pr-move-delete')) {
      btn.addEventListener('click', () => deleteMove(btn.dataset.id));
    }
  } catch (error) {
    list.innerHTML = `<div class="pr-empty pr-error">${esc(error.message)}</div>`;
  }
}

const REASON_LABEL = {
  compra: 'Compra',
  venta: 'Venta',
  ajuste: 'Ajuste de inventario',
  devolucion: 'Devolución',
  produccion_consumo: 'Consumo de producción',
  produccion_entrada: 'Entrada de producción',
};

// direction: +1 entró, -1 salió. El signo lo decide el botón que se tocó,
// nunca lo escribe la persona.
async function registerMove(direction) {
  if (!currentProductId) return;

  const qtyInput = $('pr-move-qty');
  const amount = Number(qtyInput.value);
  if (!amount || amount <= 0) {
    alert('Escribí una cantidad mayor a 0.');
    qtyInput.focus();
    return;
  }

  const buttons = [$('pr-move-in'), $('pr-move-out')];
  buttons.forEach((b) => { b.disabled = true; });

  try {
    const response = await fetch('/api/products/stock_move', {
      method: 'POST',
      headers: fastHeaders(),
      body: JSON.stringify({
        product_id: currentProductId,
        quantity: amount * direction,
        reason: $('pr-move-reason').value,
        reference: $('pr-move-ref').value.trim(),
      }),
    });
    if (!response.ok) throw new Error(await errorText(response));

    qtyInput.value = '';
    $('pr-move-ref').value = '';
    await loadStock();
    selectProduct(currentProductId);
  } catch (error) {
    alert('Error: ' + error.message);
  } finally {
    buttons.forEach((b) => { b.disabled = false; });
  }
}

async function deleteMove(id) {
  if (!confirm('¿Borrar este movimiento? El stock se recalcula sin él.')) return;
  try {
    const response = await fetch(`/api/products/stock_move/${id}`, { method: 'DELETE', headers: fastHeaders() });
    if (!response.ok) throw new Error(await errorText(response));
    await loadStock();
    selectProduct(currentProductId);
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

// ========================================
// Arranque
// ========================================

function init() {
  loadStock();

  $('pr-stock-search').addEventListener('input', renderStockList);
  $('pr-move-in').addEventListener('click', () => registerMove(1));
  $('pr-move-out').addEventListener('click', () => registerMove(-1));

  // Alta/baja/edición en la grilla del catálogo cambia lo que hay que
  // mostrar en Stock — se recarga al volver a esa pestaña.
  const productView = document.querySelector('[data-model="products/product"]');
  if (productView) productView.addEventListener('fast:saved', loadStock);

  document.addEventListener('fast:tab', (e) => {
    if (e.detail.tab === 'stock') loadStock();
  });

  window.addEventListener('resize', () => stockChart && stockChart.resize());
}

init();

// ========================================
// Recetas + Producción (antes módulo "manufactura")
// ========================================
// Va aislado en su propio IIFE: reusa los mismos helpers ($/esc/fastHeaders)
// pero declarados acá adentro, así no chocan con los de arriba. Sus modelos
// (bom, bom_line, production_order) ahora viven en el módulo "products", por
// eso las rutas son /api/products/... y el endpoint de "completar" es
// /api/_products/production_order/{id}/complete.
(function () {
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

  let products = [];
  let boms = [];
  let currentBomId = null;
  let currentBomLines = [];

  function productName(id) {
    const p = products.find((x) => x.id === id);
    return p ? `${p.name} (${p.sku})` : id;
  }

  function fillProductSelect(select) {
    select.innerHTML = products.map((p) => `<option value="${esc(p.id)}">${esc(p.name)} (${esc(p.sku)})</option>`).join('');
  }

  async function loadProducts() {
    try {
      const response = await fetch('/api/products/product?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() });
      if (!response.ok) throw new Error(await errorText(response));
      const body = await response.json();
      products = body.data || [];
    } catch (error) {
      products = [];
    }
    for (const select of [$('mf-bom-product'), $('mf-line-component'), $('mf-order-product')]) {
      fillProductSelect(select);
    }
  }

  // ---- Recetas (BOM) ----

  async function loadBoms() {
    try {
      const response = await fetch('/api/products/bom?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() });
      if (!response.ok) throw new Error(await errorText(response));
      const body = await response.json();
      boms = body.data || [];
    } catch (error) {
      boms = [];
    }
    renderBomList();
    populateOrderBoms();
  }

  function renderBomList() {
    const box = $('mf-bom-list');
    if (!boms.length) {
      box.innerHTML = '<div class="mf-empty">Todavía no hay recetas.</div>';
      return;
    }
    box.innerHTML = boms.map((b) => `
      <button type="button" class="mf-list-item${b.id === currentBomId ? ' mf-list-active' : ''}" data-id="${esc(b.id)}">
        <span class="mf-list-title">${esc(b.name || 'Receta sin nombre')}</span>
        <span class="mf-list-sub">${esc(productName(b.product_id))}</span>
        ${b.active ? '' : '<span class="mf-badge">Inactiva</span>'}
      </button>`).join('');

    for (const btn of box.querySelectorAll('.mf-list-item')) {
      btn.addEventListener('click', () => selectBom(btn.dataset.id));
    }
  }

  function newBom() {
    currentBomId = null;
    currentBomLines = [];
    $('mf-bom-empty').hidden = true;
    $('mf-bom-form').hidden = false;
    $('mf-bom-lines-section').hidden = true;
    $('mf-bom-product').value = '';
    $('mf-bom-name').value = '';
    $('mf-bom-active').checked = true;
    $('mf-bom-delete').hidden = true;
    $('mf-bom-error').hidden = true;
    renderBomList();
  }

  async function selectBom(id) {
    const bom = boms.find((b) => b.id === id);
    if (!bom) return;
    currentBomId = id;

    $('mf-bom-empty').hidden = true;
    $('mf-bom-form').hidden = false;
    $('mf-bom-lines-section').hidden = false;
    $('mf-bom-error').hidden = true;
    $('mf-bom-product').value = bom.product_id;
    $('mf-bom-name').value = bom.name || '';
    $('mf-bom-active').checked = Boolean(bom.active);
    $('mf-bom-delete').hidden = false;

    renderBomList();
    await loadBomLines(id);
  }

  async function saveBom() {
    const payload = {
      product_id: $('mf-bom-product').value,
      name: $('mf-bom-name').value.trim(),
      active: $('mf-bom-active').checked,
    };
    if (!payload.product_id) {
      showBomError('Elegí el producto que se fabrica.');
      return;
    }

    try {
      const url = currentBomId ? `/api/products/bom/${currentBomId}` : '/api/products/bom';
      const response = await fetch(url, {
        method: currentBomId ? 'PUT' : 'POST',
        headers: fastHeaders(),
        body: JSON.stringify(payload),
      });
      if (!response.ok) throw new Error(await errorText(response));
      const body = await response.json();
      const savedId = currentBomId || body.id;
      await loadBoms();
      await selectBom(savedId);
    } catch (error) {
      showBomError(error.message);
    }
  }

  async function deleteBom() {
    if (!currentBomId) return;
    if (!confirm('¿Eliminar esta receta y sus materiales?')) return;

    try {
      for (const line of currentBomLines) {
        await fetch(`/api/products/bom_line/${line.id}`, { method: 'DELETE', headers: fastHeaders() });
      }
      const response = await fetch(`/api/products/bom/${currentBomId}`, { method: 'DELETE', headers: fastHeaders() });
      if (!response.ok) throw new Error(await errorText(response));

      currentBomId = null;
      currentBomLines = [];
      await loadBoms();
      $('mf-bom-form').hidden = true;
      $('mf-bom-empty').hidden = false;
    } catch (error) {
      alert('Error: ' + error.message);
    }
  }

  function showBomError(message) {
    const box = $('mf-bom-error');
    box.textContent = message;
    box.hidden = false;
  }

  async function loadBomLines(bomId) {
    try {
      const response = await fetch(`/api/products/bom_line?bom_id__eq=${encodeURIComponent(bomId)}&limit=100`, {
        headers: fastHeaders(),
      });
      if (!response.ok) throw new Error(await errorText(response));
      const body = await response.json();
      currentBomLines = body.data || [];
      renderBomLines();
    } catch (error) {
      $('mf-bom-lines').innerHTML = `<div class="mf-empty mf-error">${esc(error.message)}</div>`;
    }
  }

  function renderBomLines() {
    const box = $('mf-bom-lines');
    box.innerHTML = currentBomLines.length
      ? currentBomLines.map((line) => `
        <div class="mf-line-row">
          <span>${esc(productName(line.component_product_id))}</span>
          <span class="mf-line-qty">${esc(line.quantity)}</span>
          <button type="button" class="mf-line-delete" data-id="${esc(line.id)}" title="Quitar">&times;</button>
        </div>`).join('')
      : '<div class="mf-empty">Sin materiales todavía.</div>';

    for (const btn of box.querySelectorAll('.mf-line-delete')) {
      btn.addEventListener('click', () => removeLine(btn.dataset.id));
    }
  }

  async function addLine() {
    if (!currentBomId) return;
    const componentId = $('mf-line-component').value;
    const qty = Number($('mf-line-qty').value);
    if (!componentId || !qty) {
      alert('Elegí un material y una cantidad mayor a 0.');
      return;
    }

    try {
      const response = await fetch('/api/products/bom_line', {
        method: 'POST',
        headers: fastHeaders(),
        body: JSON.stringify({ bom_id: currentBomId, component_product_id: componentId, quantity: qty }),
      });
      if (!response.ok) throw new Error(await errorText(response));
      $('mf-line-qty').value = '';
      await loadBomLines(currentBomId);
    } catch (error) {
      alert('Error: ' + error.message);
    }
  }

  async function removeLine(id) {
    try {
      const response = await fetch(`/api/products/bom_line/${id}`, { method: 'DELETE', headers: fastHeaders() });
      if (!response.ok) throw new Error(await errorText(response));
      await loadBomLines(currentBomId);
    } catch (error) {
      alert('Error: ' + error.message);
    }
  }

  // ---- Órdenes de producción ----

  const STATUS_LABEL = { draft: 'Borrador', in_progress: 'En curso', done: 'Completada', cancelled: 'Cancelada' };

  async function loadOrders() {
    const box = $('mf-orders-list');
    try {
      const response = await fetch('/api/products/production_order?limit=100&order_by=created_at&order_dir=desc', {
        headers: fastHeaders(),
      });
      if (!response.ok) throw new Error(await errorText(response));
      const body = await response.json();
      renderOrders(body.data || []);
    } catch (error) {
      box.innerHTML = `<div class="mf-empty mf-error">${esc(error.message)}</div>`;
    }
  }

  function renderOrders(orders) {
    const box = $('mf-orders-list');
    if (!orders.length) {
      box.innerHTML = '<div class="mf-empty">Todavía no hay órdenes.</div>';
      return;
    }

    box.innerHTML = orders.map((o) => {
      const buttons = [];
      if (o.status === 'draft') {
        buttons.push(`<button type="button" class="mf-btn-secondary" data-action="iniciar" data-id="${esc(o.id)}">Iniciar</button>`);
        buttons.push(`<button type="button" class="mf-btn-danger" data-action="cancelar" data-id="${esc(o.id)}">Cancelar</button>`);
      } else if (o.status === 'in_progress') {
        buttons.push(`<button type="button" class="mf-btn-primary" data-action="completar" data-id="${esc(o.id)}">Completar</button>`);
        buttons.push(`<button type="button" class="mf-btn-danger" data-action="cancelar" data-id="${esc(o.id)}">Cancelar</button>`);
      }
      return `
        <div class="mf-order-row">
          <span class="mf-order-badge mf-status-${esc(o.status)}">${esc(STATUS_LABEL[o.status] || o.status)}</span>
          <span class="mf-order-product">${esc(productName(o.product_id))}</span>
          <span class="mf-order-qty">${esc(o.quantity)}</span>
          <span class="mf-order-date">${esc(o.planned_date || '')}</span>
          <span class="mf-order-actions">${buttons.join('')}</span>
        </div>`;
    }).join('');

    for (const btn of box.querySelectorAll('[data-action]')) {
      btn.addEventListener('click', () => handleOrderAction(btn.dataset.id, btn.dataset.action, btn));
    }
  }

  async function handleOrderAction(id, action, btn) {
    if (action === 'cancelar' && !confirm('¿Cancelar esta orden?')) return;
    btn.disabled = true;

    try {
      if (action === 'completar') {
        // Endpoint propio: sesión (cookie), sin X-Tenant-ID — mueve stock de
        // verdad, no es sólo cambiar el estado.
        const response = await fetch(`/api/_products/production_order/${id}/complete`, { method: 'POST' });
        if (!response.ok) throw new Error(await errorText(response));
        const body = await response.json();
        const failed = (body.steps || []).filter((s) => !s.ok);
        if (failed.length) {
          alert('Se completó, pero con problemas moviendo stock:\n' + failed.map((s) => s.detail).join('\n'));
        }
      } else {
        const response = await fetch(`/api/products/production_order/${id}/transition`, {
          method: 'POST', headers: fastHeaders(), body: JSON.stringify({ action }),
        });
        if (!response.ok) throw new Error(await errorText(response));
      }
      loadOrders();
    } catch (error) {
      alert('Error: ' + error.message);
      btn.disabled = false;
    }
  }

  function populateOrderBoms() {
    const productId = $('mf-order-product').value;
    const select = $('mf-order-bom');
    const relevant = boms.filter((b) => b.product_id === productId);
    select.innerHTML = relevant.length
      ? relevant.map((b) => `<option value="${esc(b.id)}">${esc(b.name || 'Receta sin nombre')}</option>`).join('')
      : '<option value="">— este producto todavía no tiene receta —</option>';
  }

  async function createOrder(e) {
    e.preventDefault();
    const payload = {
      product_id: $('mf-order-product').value,
      bom_id: $('mf-order-bom').value,
      quantity: Number($('mf-order-qty').value),
      planned_date: $('mf-order-date').value || null,
    };
    if (!payload.product_id || !payload.bom_id || !payload.quantity) {
      alert('Elegí qué producir, con qué receta, y cuántas unidades.');
      return;
    }

    const submitBtn = e.target.querySelector('button[type="submit"]');
    submitBtn.disabled = true;
    try {
      const response = await fetch('/api/products/production_order', {
        method: 'POST', headers: fastHeaders(), body: JSON.stringify(payload),
      });
      if (!response.ok) throw new Error(await errorText(response));
      $('mf-order-qty').value = '';
      $('mf-order-date').value = '';
      showOrderForm(false); // creada: se guarda sola y vuelve a la lista
      loadOrders();
    } catch (error) {
      alert('Error: ' + error.message);
    } finally {
      submitBtn.disabled = false;
    }
  }

  // El formulario de alta arranca cerrado: quien entra a esta pestaña casi
  // siempre viene a mirar cómo va la producción, no a cargar una orden nueva.
  function showOrderForm(show) {
    $('mf-order-form').hidden = !show;
    $('mf-toggle-order-form').hidden = show;
    if (show) populateOrderBoms();
  }

  async function initMfg() {
    await loadProducts();
    await loadBoms();
    await loadOrders();

    $('mf-new-bom').addEventListener('click', newBom);
    $('mf-bom-save').addEventListener('click', saveBom);
    $('mf-bom-delete').addEventListener('click', deleteBom);
    $('mf-line-add').addEventListener('click', addLine);

    $('mf-order-product').addEventListener('change', populateOrderBoms);
    $('mf-order-form').addEventListener('submit', createOrder);
    $('mf-toggle-order-form').addEventListener('click', () => showOrderForm(true));
    $('mf-cancel-order').addEventListener('click', () => showOrderForm(false));
  }

  initMfg();
})();

// ========================================
// Lotes (vencimientos)
// ========================================
// Registro simple de lotes de productos perecederos para avisar vencimientos
// (el Dashboard usa los mismos datos). La cantidad del lote es informativa
// para el seguimiento del vencimiento — NO se suma al stock (eso ya lo hacen
// Compras/Importaciones), para no duplicar. Aislado en su IIFE.
(function () {
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

  // Días enteros entre hoy y la fecha (en UTC, para no correrse por zona
  // horaria). Positivo = faltan; negativo = ya venció.
  function daysLeft(dateStr) {
    if (!dateStr) return null;
    const [y, m, d] = dateStr.split('-').map(Number);
    if (!y || !m || !d) return null;
    const exp = Date.UTC(y, m - 1, d);
    const now = new Date();
    const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
    return Math.round((exp - today) / 86400000);
  }

  let perishables = [];
  let lots = [];

  async function loadPerishables() {
    try {
      const response = await fetch('/api/products/product?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() });
      const all = response.ok ? ((await response.json()).data || []) : [];
      perishables = all.filter((p) => p.perishable);
    } catch {
      perishables = [];
    }
    const select = $('lt-product');
    if (!perishables.length) {
      select.innerHTML = '<option value="">— No hay productos perecederos —</option>';
    } else {
      select.innerHTML = perishables.map((p) => `<option value="${esc(p.id)}">${esc(p.name)} (${esc(p.sku)})</option>`).join('');
    }
  }

  async function loadLots() {
    const box = $('lt-list');
    try {
      const response = await fetch('/api/products/product_lot?limit=100&order_by=expiry_date&order_dir=asc', { headers: fastHeaders() });
      if (!response.ok) throw new Error(await errorText(response));
      lots = (await response.json()).data || [];
      renderLots();
    } catch (error) {
      box.innerHTML = `<div class="lt-empty lt-error">${esc(error.message)}</div>`;
    }
  }

  function statusOf(d) {
    if (d === null) return { cls: '', text: 'sin fecha' };
    if (d < 0) return { cls: 'lt-expired', text: `vencido hace ${Math.abs(d)} día${Math.abs(d) === 1 ? '' : 's'}` };
    if (d === 0) return { cls: 'lt-soon', text: 'vence hoy' };
    if (d <= 15) return { cls: 'lt-soon', text: `vence en ${d} día${d === 1 ? '' : 's'}` };
    return { cls: 'lt-ok', text: `vence en ${d} días` };
  }

  function renderLots() {
    const box = $('lt-list');
    if (!perishables.length && !lots.length) {
      box.innerHTML = `
        <div class="lt-blank">
          <div class="lt-blank-icon">📅</div>
          <p>Para llevar vencimientos, marcá un producto como <strong>Perecedero</strong> en la pestaña Catálogo (en "Opciones avanzadas") y volvé acá.</p>
        </div>`;
      return;
    }
    if (!lots.length) {
      box.innerHTML = '<div class="lt-empty">Todavía no registraste lotes.</div>';
      return;
    }

    box.innerHTML = lots.map((lot) => {
      const d = daysLeft(lot.expiry_date);
      const st = statusOf(d);
      return `
        <div class="lt-row ${st.cls}">
          <div class="lt-row-main">
            <span class="lt-row-name">${esc(lot.product_name || lot.product_id)}</span>
            <span class="lt-row-sub">${lot.lot_code ? esc(lot.lot_code) + ' · ' : ''}${esc(lot.quantity ?? 0)} u${lot.purchase_date ? ' · comprado ' + esc(lot.purchase_date) : ''}</span>
          </div>
          <span class="lt-row-status">${esc(st.text)}</span>
          <span class="lt-row-date">${esc(lot.expiry_date || '')}</span>
          <button type="button" class="lt-del" data-id="${esc(lot.id)}" title="Borrar">&times;</button>
        </div>`;
    }).join('');

    for (const btn of box.querySelectorAll('.lt-del')) {
      btn.addEventListener('click', () => deleteLot(btn.dataset.id));
    }
  }

  async function addLot(e) {
    e.preventDefault();
    const productId = $('lt-product').value;
    const product = perishables.find((p) => p.id === productId);
    const expiry = $('lt-expiry').value;
    if (!productId) { showError('Elegí un producto perecedero.'); return; }
    if (!expiry) { showError('Poné la fecha de vencimiento.'); return; }

    try {
      const response = await fetch('/api/products/product_lot', {
        method: 'POST',
        headers: fastHeaders(),
        body: JSON.stringify({
          product_id: productId,
          product_name: product ? product.name : '',
          lot_code: $('lt-code').value.trim(),
          quantity: Number($('lt-qty').value) || 0,
          purchase_date: $('lt-purchase').value || null,
          expiry_date: expiry,
        }),
      });
      if (!response.ok) throw new Error(await errorText(response));
      $('lt-code').value = '';
      $('lt-qty').value = '';
      $('lt-error').hidden = true;
      await loadLots();
    } catch (error) {
      showError(error.message);
    }
  }

  async function deleteLot(id) {
    if (!confirm('¿Borrar este lote?')) return;
    try {
      const response = await fetch(`/api/products/product_lot/${id}`, { method: 'DELETE', headers: fastHeaders() });
      if (!response.ok) throw new Error(await errorText(response));
      await loadLots();
    } catch (error) {
      alert('Error: ' + error.message);
    }
  }

  function showError(message) {
    const box = $('lt-error');
    box.textContent = message;
    box.hidden = false;
  }

  async function initLotes() {
    await loadPerishables();
    await loadLots();
    $('lt-form').addEventListener('submit', addLot);

    // Al volver a la pestaña, refrescar por si se marcó un perecedero nuevo.
    document.addEventListener('fast:tab', (ev) => {
      if (ev.detail.tab === 'lotes') { loadPerishables(); loadLots(); }
    });
  }

  initLotes();
})();
