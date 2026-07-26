// ========================================
// Tienda Web
// ========================================
// Una sola vista: cada producto del catálogo (módulo "products") es una
// tarjeta donde, en un solo lugar, se ve la foto, el precio y el stock
// reales, y se decide si se publica, si es destacado, su texto de tienda y
// sus fotos. Antes esto estaba partido en dos pestañas (catálogo + galería)
// y había que re-elegir el producto para tocarle las imágenes.
//
// Todo es @fast + /api/_uploads; no hay backend propio de este módulo.

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

const money = (n) => '$' + Number(n || 0).toLocaleString('es-EC', { maximumFractionDigits: 2 });

let products = [];
const storeByProduct = new Map();      // product_id -> fila store_product
const imagesByProduct = new Map();     // product_id -> [store_image, ...]
const stockByProduct = new Map();      // product_id -> stock actual

// Una sola carga trae todo lo que las tarjetas necesitan (productos, su
// capa de tienda, sus imágenes y su stock), agrupando del lado del cliente
// — cuatro consultas fijas en vez de una por producto.
async function loadCatalog() {
  const box = $('tw-catalog');
  try {
    const [prodRes, storeRes, imgRes, moveRes] = await Promise.all([
      fetch('/api/products/product?limit=100&order_by=name&order_dir=asc', { headers: fastHeaders() }),
      fetch('/api/tienda_web/store_product?limit=100', { headers: fastHeaders() }),
      fetch('/api/tienda_web/store_image?limit=100&order_by=position&order_dir=asc', { headers: fastHeaders() }),
      fetch('/api/products/stock_move?limit=100&order_by=created_at&order_dir=desc', { headers: fastHeaders() }),
    ]);
    if (!prodRes.ok) throw new Error(await errorText(prodRes));
    if (!storeRes.ok) throw new Error(await errorText(storeRes));

    products = (await prodRes.json()).data || [];

    storeByProduct.clear();
    for (const row of (storeRes.ok ? (await storeRes.json()).data : []) || []) {
      storeByProduct.set(row.product_id, row);
    }

    imagesByProduct.clear();
    for (const img of (imgRes.ok ? (await imgRes.json()).data : []) || []) {
      if (!imagesByProduct.has(img.product_id)) imagesByProduct.set(img.product_id, []);
      imagesByProduct.get(img.product_id).push(img);
    }

    stockByProduct.clear();
    for (const m of (moveRes.ok ? (await moveRes.json()).data : []) || []) {
      stockByProduct.set(m.product_id, (stockByProduct.get(m.product_id) || 0) + Number(m.quantity || 0));
    }

    renderCatalog();
  } catch (error) {
    box.innerHTML = `<div class="tw-empty tw-error">${esc(error.message)}</div>`;
  }
}

function renderCatalog() {
  const box = $('tw-catalog');

  // Estado vacío que explica de dónde salen los productos, en el momento
  // justo — en vez de una lista en blanco con un hint.
  if (!products.length) {
    box.innerHTML = `
      <div class="tw-blank">
        <div class="tw-blank-icon">🛍️</div>
        <h2>Tu tienda muestra los productos de tu catálogo</h2>
        <p>Todavía no cargaste ninguno. Creá tus productos y volvé acá para elegir cuáles publicar.</p>
        <a class="tw-btn-primary" href="/admin/products">Ir a Productos e Inventario</a>
      </div>`;
    return;
  }

  box.innerHTML = products.map((p) => {
    const sp = storeByProduct.get(p.id) || {};
    const imgs = imagesByProduct.get(p.id) || [];
    const stock = stockByProduct.get(p.id) || 0;
    const cover = imgs[0]
      ? `<img src="${esc(imgs[0].url)}" alt="${esc(p.name)}" loading="lazy">`
      : '<span class="tw-cover-empty">Sin foto</span>';

    const thumbs = imgs.map((img) => `
      <div class="tw-thumb">
        <img src="${esc(img.url)}" alt="" loading="lazy">
        <button type="button" class="tw-thumb-delete" data-img="${esc(img.id)}" title="Borrar">&times;</button>
      </div>`).join('');

    return `
      <div class="tw-card${sp.published ? ' tw-card-published' : ''}" data-product="${esc(p.id)}">
        <div class="tw-cover">${cover}</div>
        <div class="tw-card-body">
          <div class="tw-card-head">
            <div>
              <strong class="tw-card-name">${esc(p.name)}</strong>
              <span class="tw-card-meta">${esc(p.sku)} · ${money(p.sale_price ?? p.cost)} · stock ${stock}</span>
            </div>
          </div>

          <label class="tw-switch">
            <input type="checkbox" class="tw-pub" ${sp.published ? 'checked' : ''}>
            <span class="tw-switch-slider"></span>
            <span class="tw-switch-label">Publicar en la tienda</span>
          </label>
          <label class="tw-switch tw-switch-sm">
            <input type="checkbox" class="tw-feat" ${sp.featured ? 'checked' : ''} ${sp.published ? '' : 'disabled'}>
            <span class="tw-switch-slider"></span>
            <span class="tw-switch-label">Destacado</span>
          </label>

          <input type="text" class="tw-desc" placeholder="Descripción para la tienda (opcional)" value="${esc(sp.display_description || '')}">

          <div class="tw-photos">
            ${thumbs}
            <button type="button" class="tw-add-photo" title="Agregar foto">+ Foto</button>
            <input type="file" class="tw-photo-input" accept="image/*" hidden>
          </div>
        </div>
      </div>`;
  }).join('');

  for (const card of box.querySelectorAll('.tw-card')) {
    const productId = card.dataset.product;

    card.querySelector('.tw-pub').addEventListener('change', (e) => {
      upsertStoreProduct(productId, { published: e.target.checked }).then(loadCatalog);
    });
    card.querySelector('.tw-feat').addEventListener('change', (e) => {
      upsertStoreProduct(productId, { featured: e.target.checked }).then(loadCatalog);
    });
    card.querySelector('.tw-desc').addEventListener('change', (e) => {
      upsertStoreProduct(productId, { display_description: e.target.value.trim() });
    });

    const fileInput = card.querySelector('.tw-photo-input');
    card.querySelector('.tw-add-photo').addEventListener('click', () => fileInput.click());
    fileInput.addEventListener('change', (e) => {
      const file = e.target.files[0];
      if (file) uploadImage(productId, file);
      e.target.value = '';
    });
    for (const btn of card.querySelectorAll('.tw-thumb-delete')) {
      btn.addEventListener('click', () => deleteImage(btn.dataset.img));
    }
  }
}

async function upsertStoreProduct(productId, patch) {
  const existing = storeByProduct.get(productId);
  try {
    if (existing) {
      const response = await fetch(`/api/tienda_web/store_product/${existing.id}`, {
        method: 'PUT', headers: fastHeaders(), body: JSON.stringify(patch),
      });
      if (!response.ok) throw new Error(await errorText(response));
    } else {
      const response = await fetch('/api/tienda_web/store_product', {
        method: 'POST', headers: fastHeaders(),
        body: JSON.stringify({ product_id: productId, published: false, featured: false, display_description: '', ...patch }),
      });
      if (!response.ok) throw new Error(await errorText(response));
    }
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

async function uploadImage(productId, file) {
  try {
    const form = new FormData();
    form.append('file', file);
    const uploadResponse = await fetch('/api/_uploads?kind=products', { method: 'POST', body: form });
    if (!uploadResponse.ok) throw new Error(await errorText(uploadResponse));
    const { url } = await uploadResponse.json();

    const nextPosition = (imagesByProduct.get(productId) || []).length;
    const createResponse = await fetch('/api/tienda_web/store_image', {
      method: 'POST',
      headers: fastHeaders(),
      body: JSON.stringify({ product_id: productId, url, position: nextPosition, alt: '' }),
    });
    if (!createResponse.ok) throw new Error(await errorText(createResponse));

    loadCatalog();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

async function deleteImage(id) {
  if (!confirm('¿Borrar esta foto?')) return;
  try {
    const response = await fetch(`/api/tienda_web/store_image/${id}`, {
      method: 'DELETE', headers: fastHeaders(),
    });
    if (!response.ok) throw new Error(await errorText(response));
    loadCatalog();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

loadCatalog();
