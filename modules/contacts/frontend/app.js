// ========================================
// Módulo Contactos — sólo habla con @fast
// ========================================
// Este módulo no sabe SQL, no sabe de tablas y no toca el core.
// Todo lo que hace es llamar al API @fast que el motor genera a partir de
// modules/contacts/manifest.json:
//
//   @fast.list()   → GET    /api/contacts/contact?page=&limit=&search=&order_by=
//   @fast.create() → POST   /api/contacts/contact
//   @fast.read()   → GET    /api/contacts/contact/{id}
//   @fast.update() → PUT    /api/contacts/contact/{id}
//   @fast.delete() → DELETE /api/contacts/contact/{id}
//
// La paginación (10/20/50/100), la búsqueda y el orden los resuelve el SDK.

const API_BASE = '/api/contacts/contact';

function getTenantID() {
  const token = localStorage.getItem('access_token');
  if (!token) return null;
  try {
    return JSON.parse(atob(token.split('.')[1])).tenant_id;
  } catch {
    return null;
  }
}

const tenantID = getTenantID();
const headers = { 'X-Tenant-ID': tenantID, 'Content-Type': 'application/json' };

// Estado de la vista. El SDK manda: limit y page_sizes salen de su respuesta.
const state = {
  contacts: [],
  page: 1,
  limit: 20,
  total: 0,
  totalPages: 0,
  search: '',
  editingId: null,
};

const $ = (id) => document.getElementById(id);

// --- Arranque ---
$('addContactBtn').addEventListener('click', openModal);
document.querySelector('.btn-close').addEventListener('click', closeModal);
$('searchInput').addEventListener('input', debounce(onSearch, 300));
$('pageSizeSelect').addEventListener('change', onPageSizeChange);
$('prevPage').addEventListener('click', () => goToPage(state.page - 1));
$('nextPage').addEventListener('click', () => goToPage(state.page + 1));
$('contactModal').addEventListener('click', (e) => {
  if (e.target.id === 'contactModal') closeModal();
});

loadContacts();

// ========================================
// @fast.list() — página de contactos
// ========================================
async function loadContacts() {
  const params = new URLSearchParams({
    page: state.page,
    limit: state.limit,
    order_by: 'name',
    order_dir: 'asc',
  });
  if (state.search) params.set('search', state.search);

  try {
    const response = await fetch(`${API_BASE}?${params}`, { headers });
    if (!response.ok) throw new Error(await errorMessage(response));

    const page = await response.json();

    state.contacts = page.data || [];
    state.page = page.page;
    state.limit = page.limit;
    state.total = page.total;
    state.totalPages = page.total_pages;

    renderPageSizes(page.page_sizes);
    renderContacts();
    renderPagination();
  } catch (error) {
    $('contactsList').innerHTML = `<div class="loading">Error: ${escapeHtml(error.message)}</div>`;
  }
}

// El selector se llena con lo que el SDK declara como tamaños válidos.
function renderPageSizes(sizes) {
  const select = $('pageSizeSelect');
  if (!sizes || select.options.length === sizes.length) return;

  select.innerHTML = sizes
    .map((size) => `<option value="${size}">${size}</option>`)
    .join('');
  select.value = state.limit;
}

function renderContacts() {
  const container = $('contactsList');

  if (!state.contacts.length) {
    container.innerHTML = state.search
      ? '<div class="loading">Sin resultados para esta búsqueda.</div>'
      : '<div class="loading">No hay contactos. ¡Crea el primero!</div>';
    return;
  }

  container.innerHTML = state.contacts.map(contactCard).join('');
}

function contactCard(contact) {
  const line = (icon, value) =>
    value ? `<div class="contact-info">${icon} ${escapeHtml(value)}</div>` : '';

  const name = contact.name || 'Sin nombre';
  return `
    <div class="contact-card">
      <div class="contact-avatar">${escapeHtml(name.charAt(0).toUpperCase())}</div>
      <div class="contact-name">${escapeHtml(name)}</div>
      ${line('📌', contact.job_title)}
      ${line('📧', contact.email)}
      ${line('📱', contact.phone)}
      ${line('🏢', contact.company)}
      <div class="contact-actions">
        <button class="btn-edit" onclick="editContact('${contact.id}')">Editar</button>
        <button class="btn-delete" onclick="deleteContact('${contact.id}')">Eliminar</button>
      </div>
    </div>`;
}

function renderPagination() {
  const nav = $('pagination');
  nav.hidden = state.totalPages <= 1;

  const from = state.total === 0 ? 0 : (state.page - 1) * state.limit + 1;
  const to = Math.min(state.page * state.limit, state.total);

  $('pageInfo').textContent =
    `${from}–${to} de ${state.total} · página ${state.page} de ${state.totalPages}`;
  $('prevPage').disabled = state.page <= 1;
  $('nextPage').disabled = state.page >= state.totalPages;
}

function goToPage(page) {
  if (page < 1 || page > state.totalPages) return;
  state.page = page;
  loadContacts();
}

function onPageSizeChange(event) {
  state.limit = Number(event.target.value);
  state.page = 1; // cambiar el tamaño invalida la página actual
  loadContacts();
}

// La búsqueda la resuelve el SDK sobre los campos de texto del manifest.
function onSearch(event) {
  state.search = event.target.value.trim();
  state.page = 1;
  loadContacts();
}

// --- Modal ---
function openModal() {
  state.editingId = null;
  $('modalTitle').textContent = 'Nuevo Contacto';
  $('contactForm').reset();
  $('contactModal').style.display = 'flex';
}

function closeModal() {
  $('contactModal').style.display = 'none';
}

function editContact(id) {
  const contact = state.contacts.find((c) => c.id === id);
  if (!contact) return;

  state.editingId = id;
  $('modalTitle').textContent = 'Editar Contacto';
  $('formName').value = contact.name || '';
  $('formEmail').value = contact.email || '';
  $('formPhone').value = contact.phone || '';
  $('formCompany').value = contact.company || '';
  $('formJobTitle').value = contact.job_title || '';
  $('contactModal').style.display = 'flex';
}

// ========================================
// @fast.create() / @fast.update()
// ========================================
async function saveContact() {
  const name = $('formName').value.trim();
  if (!name) {
    alert('El nombre es requerido');
    return;
  }

  const payload = {
    name,
    email: $('formEmail').value.trim(),
    phone: $('formPhone').value.trim(),
    company: $('formCompany').value.trim(),
    job_title: $('formJobTitle').value.trim(),
  };

  const editing = Boolean(state.editingId);
  const url = editing ? `${API_BASE}/${state.editingId}` : API_BASE;

  try {
    const response = await fetch(url, {
      method: editing ? 'PUT' : 'POST',
      headers,
      body: JSON.stringify(payload),
    });
    if (!response.ok) throw new Error(await errorMessage(response));

    closeModal();
    if (!editing) state.page = 1; // el nuevo registro va al inicio
    await loadContacts();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

// ========================================
// @fast.delete()
// ========================================
async function deleteContact(id) {
  if (!confirm('¿Eliminar este contacto?')) return;

  try {
    const response = await fetch(`${API_BASE}/${id}`, { method: 'DELETE', headers });
    if (!response.ok) throw new Error(await errorMessage(response));

    // Si era el último de la página, retroceder para no quedar en una vacía.
    if (state.contacts.length === 1 && state.page > 1) state.page -= 1;
    await loadContacts();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}

// --- Utilidades ---

// El SDK responde {"error": "..."} con el motivo real (campo obligatorio,
// filtro inválido, etc.). Mostrarlo es más útil que un código HTTP suelto.
async function errorMessage(response) {
  try {
    const body = await response.json();
    if (body.error) return body.error;
  } catch {
    // respuesta sin JSON
  }
  return `Error ${response.status}`;
}

function escapeHtml(value) {
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
