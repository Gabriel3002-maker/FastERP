// ========================================
// Ventas (POS)
// ========================================
// Una pantalla: grilla de productos (con su stock a la vista) + carrito. Al
// "Cobrar" se llama al endpoint propio /api/_ventas/checkout (con sesión),
// que crea la venta, sus líneas y los movimientos de stock NEGATIVOS en el
// módulo products — el stock nunca se toca desde acá directamente.
//
// El precio sale del catálogo (sale_price) pero se puede ajustar por línea
// (descuentos). Las cantidades también.

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

let products = [];
const stockByProduct = new Map();
const cart = new Map(); // product_id -> { product, qty, price }

// ========================================
// Catálogo
// ========================================

async function loadProducts() {
  const box = $('vt-products');
  try {
    const [prodRes, moveRes] = await Promise.all([
      fetch('/api/products/product?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() }),
      fetch('/api/products/stock_move?limit=100&order_by=created_at&order_dir=desc', { headers: fastHeaders() }),
    ]);
    if (!prodRes.ok) throw new Error(await errorText(prodRes));
    products = ((await prodRes.json()).data || []).filter((p) => p.active !== false);

    stockByProduct.clear();
    for (const m of (moveRes.ok ? (await moveRes.json()).data : []) || []) {
      stockByProduct.set(m.product_id, (stockByProduct.get(m.product_id) || 0) + Number(m.quantity || 0));
    }
    renderProducts();
  } catch (error) {
    box.innerHTML = `<div class="vt-empty vt-error">${esc(error.message)}</div>`;
  }
}

function renderProducts() {
  const box = $('vt-products');
  const term = $('vt-search').value.trim().toLowerCase();
  const visible = products.filter((p) => !term
    || (p.name || '').toLowerCase().includes(term)
    || (p.sku || '').toLowerCase().includes(term));

  if (!products.length) {
    box.innerHTML = `
      <div class="vt-blank">
        <div class="vt-blank-icon">🛒</div>
        <p>No hay productos todavía. Cargalos en Operaciones para poder vender.</p>
        <a class="vt-btn-primary" href="/admin/products">Ir a Operaciones</a>
      </div>`;
    return;
  }
  if (!visible.length) {
    box.innerHTML = '<div class="vt-empty">Sin resultados.</div>';
    return;
  }

  box.innerHTML = visible.map((p) => {
    const stock = stockByProduct.get(p.id) || 0;
    const price = Number(p.sale_price ?? p.cost ?? 0);
    return `
      <button type="button" class="vt-product" data-id="${esc(p.id)}">
        <span class="vt-product-name">${esc(p.name)}</span>
        <span class="vt-product-meta">${esc(p.sku || '')}</span>
        <span class="vt-product-foot">
          <span class="vt-product-price">${money(price)}</span>
          <span class="vt-product-stock${stock <= 0 ? ' vt-stock-zero' : ''}">stock ${stock}</span>
        </span>
      </button>`;
  }).join('');

  for (const btn of box.querySelectorAll('.vt-product')) {
    btn.addEventListener('click', () => addToCart(btn.dataset.id));
  }
}

// ========================================
// Carrito
// ========================================

function addToCart(productId) {
  const product = products.find((p) => p.id === productId);
  if (!product) return;
  const existing = cart.get(productId);
  if (existing) {
    existing.qty += 1;
  } else {
    cart.set(productId, { product, qty: 1, price: Number(product.sale_price ?? product.cost ?? 0) });
  }
  renderCart();
}

function renderCart() {
  const box = $('vt-cart-lines');
  if (!cart.size) {
    box.innerHTML = '<div class="vt-empty">El carrito está vacío. Tocá un producto para agregarlo.</div>';
    $('vt-total').textContent = money(0);
    $('vt-checkout').disabled = true;
    return;
  }

  box.innerHTML = [...cart.values()].map(({ product, qty, price }) => `
    <div class="vt-line" data-id="${esc(product.id)}">
      <div class="vt-line-head">
        <span class="vt-line-name">${esc(product.name)}</span>
        <button type="button" class="vt-line-del" title="Quitar">&times;</button>
      </div>
      <div class="vt-line-controls">
        <input type="number" class="vt-line-qty" min="0" step="any" value="${esc(qty)}" title="Cantidad">
        <span class="vt-line-x">×</span>
        <input type="number" class="vt-line-price" min="0" step="any" value="${esc(price)}" title="Precio unitario">
        <span class="vt-line-total">${money(qty * price)}</span>
      </div>
    </div>`).join('');

  for (const line of box.querySelectorAll('.vt-line')) {
    const id = line.dataset.id;
    line.querySelector('.vt-line-del').addEventListener('click', () => { cart.delete(id); renderCart(); });
    line.querySelector('.vt-line-qty').addEventListener('input', (e) => {
      const item = cart.get(id);
      item.qty = Number(e.target.value) || 0;
      line.querySelector('.vt-line-total').textContent = money(item.qty * item.price);
      updateTotal();
    });
    line.querySelector('.vt-line-price').addEventListener('input', (e) => {
      const item = cart.get(id);
      item.price = Number(e.target.value) || 0;
      line.querySelector('.vt-line-total').textContent = money(item.qty * item.price);
      updateTotal();
    });
  }
  updateTotal();
}

function cartTotal() {
  let total = 0;
  for (const { qty, price } of cart.values()) total += qty * price;
  return total;
}

function updateTotal() {
  const total = cartTotal();
  $('vt-total').textContent = money(total);
  $('vt-checkout').disabled = total <= 0;
}

async function checkout() {
  const lines = [...cart.values()]
    .filter((i) => i.qty > 0)
    .map((i) => ({ product_id: i.product.id, product_name: i.product.name, quantity: i.qty, unit_price: i.price }));
  if (!lines.length) return;

  const btn = $('vt-checkout');
  btn.disabled = true;
  btn.textContent = 'Cobrando…';
  try {
    const response = await fetch('/api/_ventas/checkout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        partner_id: $('vt-customer').value || '',
        customer_name: $('vt-customer').selectedOptions[0]?.dataset.name || '',
        paid_method: $('vt-pay').value,
        lines,
      }),
    });
    if (!response.ok) throw new Error(await errorText(response));
    const body = await response.json();
    const failed = (body.steps || []).filter((s) => !s.ok);
    cart.clear();
    renderCart();
    await loadProducts();
    if (failed.length) {
      alert('Venta cobrada, pero con avisos de stock:\n' + failed.map((s) => s.detail).join('\n'));
    } else {
      flash('Venta cobrada por ' + money(body.total));
    }
  } catch (error) {
    alert('Error: ' + error.message);
  } finally {
    btn.textContent = 'Cobrar';
    updateTotal();
  }
}

let flashTimer = null;
function flash(message) {
  let el = $('vt-flash');
  if (!el) {
    el = document.createElement('div');
    el.id = 'vt-flash';
    el.className = 'vt-flash';
    document.body.appendChild(el);
  }
  el.textContent = '✓ ' + message;
  el.classList.add('vt-flash-show');
  clearTimeout(flashTimer);
  flashTimer = setTimeout(() => el.classList.remove('vt-flash-show'), 2600);
}

// ========================================
// Clientes (de Contactos)
// ========================================

async function loadCustomers() {
  const select = $('vt-customer');
  try {
    const response = await fetch('/api/contacts/contact?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() });
    if (!response.ok) throw new Error(await errorText(response));
    const all = (await response.json()).data || [];
    // Preferimos los marcados como cliente; si nadie tiene rol, mostramos todos.
    const marked = all.filter((c) => c.is_customer);
    const list = marked.length ? marked : all;
    select.innerHTML = '<option value="">— Cliente ocasional —</option>'
      + list.map((c) => `<option value="${esc(c.id)}" data-name="${esc(c.name)}">${esc(c.name)}</option>`).join('');
  } catch {
    select.innerHTML = '<option value="">— Cliente ocasional —</option>';
  }
}

// ========================================
// Historial
// ========================================

const PAY_LABEL = { efectivo: 'Efectivo', transferencia: 'Transferencia', tarjeta: 'Tarjeta' };

async function loadHistory() {
  const box = $('vt-history');
  try {
    const response = await fetch('/api/ventas/sale?limit=100&order_by=created_at&order_dir=desc', { headers: fastHeaders() });
    if (!response.ok) throw new Error(await errorText(response));
    const sales = (await response.json()).data || [];
    if (!sales.length) {
      box.innerHTML = '<div class="vt-empty">Todavía no hay ventas.</div>';
      return;
    }
    box.innerHTML = sales.map((s) => {
      const when = s.created_at ? new Date(s.created_at).toLocaleString('es-EC') : '';
      const cancelled = s.status === 'cancelled';
      return `
        <div class="vt-sale-row${cancelled ? ' vt-sale-cancelled' : ''}">
          <span class="vt-sale-when">${esc(when)}</span>
          <span class="vt-sale-cust">${esc(s.customer_name || 'Cliente ocasional')}</span>
          <span class="vt-sale-pay">${esc(PAY_LABEL[s.paid_method] || s.paid_method || '')}</span>
          <span class="vt-sale-total">${money(s.total)}</span>
          <span class="vt-sale-actions">
            ${cancelled
              ? '<span class="vt-badge">Anulada</span>'
              : `<button type="button" class="vt-btn-danger" data-cancel="${esc(s.id)}">Anular</button>`}
          </span>
        </div>`;
    }).join('');

    for (const btn of box.querySelectorAll('[data-cancel]')) {
      btn.addEventListener('click', () => cancelSale(btn.dataset.cancel, btn));
    }
  } catch (error) {
    box.innerHTML = `<div class="vt-empty vt-error">${esc(error.message)}</div>`;
  }
}

async function cancelSale(id, btn) {
  if (!confirm('¿Anular esta venta? Se repone el stock de sus productos.')) return;
  btn.disabled = true;
  try {
    const response = await fetch(`/api/_ventas/sale/${id}/cancel`, { method: 'POST' });
    if (!response.ok) throw new Error(await errorText(response));
    await loadHistory();
    loadProducts();
  } catch (error) {
    alert('Error: ' + error.message);
    btn.disabled = false;
  }
}

// ========================================
// Arranque
// ========================================

loadProducts();
loadCustomers();

$('vt-search').addEventListener('input', renderProducts);
$('vt-checkout').addEventListener('click', checkout);

document.addEventListener('fast:tab', (e) => {
  if (e.detail.tab === 'historial') loadHistory();
  if (e.detail.tab === 'vender') loadProducts();
});
