// ========================================
// Módulo Contactos
// ========================================
// El listado (tabla, paginación, búsqueda global y por columna, orden) lo
// resuelve el core: index.html sólo declara
//
//   <div data-fast-view data-model="contacts/contact"></div>
//
// y el motor lo arma leyendo manifest.json. Este archivo sólo conserva el
// formulario de alta/edición, hasta que el core lo genere desde el manifest.
//
// Escribe con @fast:
//   @fast.create() → POST /api/contacts/contact
//   @fast.update() → PUT  /api/contacts/contact/{id}

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

const headers = { 'X-Tenant-ID': getTenantID(), 'Content-Type': 'application/json' };
const view = () => window.FastViews.get('contacts/contact');

let editingId = null;

// El motor avisa cuando piden crear o editar; el módulo pone el formulario.
document.addEventListener('fast:new', openModal);
document.addEventListener('fast:edit', (e) => fillModal(e.detail.record));

document.querySelector('.btn-close').addEventListener('click', closeModal);
document.getElementById('contactModal').addEventListener('click', (e) => {
  if (e.target.id === 'contactModal') closeModal();
});

function openModal() {
  editingId = null;
  document.getElementById('modalTitle').textContent = 'Nuevo Contacto';
  document.getElementById('contactForm').reset();
  document.getElementById('contactModal').style.display = 'flex';
}

function fillModal(contact) {
  editingId = contact.id;
  document.getElementById('modalTitle').textContent = 'Editar Contacto';
  document.getElementById('formName').value = contact.name || '';
  document.getElementById('formEmail').value = contact.email || '';
  document.getElementById('formPhone').value = contact.phone || '';
  document.getElementById('formCompany').value = contact.company || '';
  document.getElementById('formJobTitle').value = contact.job_title || '';
  document.getElementById('contactModal').style.display = 'flex';
}

function closeModal() {
  document.getElementById('contactModal').style.display = 'none';
}

// @fast.create() / @fast.update()
async function saveContact() {
  const name = document.getElementById('formName').value.trim();
  if (!name) {
    alert('El nombre es requerido');
    return;
  }

  const payload = {
    name,
    email: document.getElementById('formEmail').value.trim(),
    phone: document.getElementById('formPhone').value.trim(),
    company: document.getElementById('formCompany').value.trim(),
    job_title: document.getElementById('formJobTitle').value.trim(),
  };

  const url = editingId ? `${API_BASE}/${editingId}` : API_BASE;

  try {
    const response = await fetch(url, {
      method: editingId ? 'PUT' : 'POST',
      headers,
      body: JSON.stringify(payload),
    });

    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      throw new Error(body.error || `Error ${response.status}`);
    }

    closeModal();
    view()?.refresh();
  } catch (error) {
    alert('Error: ' + error.message);
  }
}
