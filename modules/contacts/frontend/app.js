// Obtener tenant ID del JWT token
function getTenantID() {
  const token = localStorage.getItem('access_token');
  if (!token) return null;
  try {
    const parts = token.split('.');
    const decoded = JSON.parse(atob(parts[1]));
    return decoded.tenant_id;
  } catch {
    return null;
  }
}

const API_BASE = '/api/contacts';
const tenantID = getTenantID();

// Estado
let contacts = [];
let editingId = null;

// Event listeners
document.getElementById('addContactBtn').addEventListener('click', openModal);
document.querySelector('.btn-close').addEventListener('click', closeModal);
document.getElementById('searchInput').addEventListener('input', filterContacts);

// Cargar contactos al iniciar
loadContacts();

// Funciones
async function loadContacts() {
  try {
    const response = await fetch(`${API_BASE}/list`, {
      headers: { 'X-Tenant-ID': tenantID }
    });

    if (!response.ok) {
      // Si no hay contactos, mostrar lista vacía
      contacts = [];
      renderContacts();
      return;
    }

    const data = await response.json();
    contacts = data.contacts || [];
    renderContacts();
  } catch (error) {
    console.error('Error cargando contactos:', error);
    document.getElementById('contactsList').innerHTML = `
      <div class="loading">Error cargando contactos: ${error.message}</div>
    `;
  }
}

function renderContacts() {
  const container = document.getElementById('contactsList');

  if (contacts.length === 0) {
    container.innerHTML = '<div class="loading">No hay contactos. ¡Crea el primero!</div>';
    return;
  }

  container.innerHTML = contacts.map(contact => `
    <div class="contact-card">
      <div class="contact-avatar">${contact.name?.charAt(0).toUpperCase()}</div>
      <div class="contact-name">${contact.name || 'Sin nombre'}</div>
      ${contact.job_title ? `<div class="contact-info">📌 ${contact.job_title}</div>` : ''}
      ${contact.email ? `<div class="contact-info">📧 ${contact.email}</div>` : ''}
      ${contact.phone ? `<div class="contact-info">📱 ${contact.phone}</div>` : ''}
      ${contact.company ? `<div class="contact-info">🏢 ${contact.company}</div>` : ''}
      <div class="contact-actions">
        <button class="btn-edit" onclick="editContact('${contact.id}')">Editar</button>
        <button class="btn-delete" onclick="deleteContact('${contact.id}')">Eliminar</button>
      </div>
    </div>
  `).join('');
}

function filterContacts() {
  const search = document.getElementById('searchInput').value.toLowerCase();
  const filtered = contacts.filter(c =>
    (c.name || '').toLowerCase().includes(search) ||
    (c.email || '').toLowerCase().includes(search) ||
    (c.company || '').toLowerCase().includes(search)
  );

  const container = document.getElementById('contactsList');
  if (filtered.length === 0) {
    container.innerHTML = '<div class="loading">No hay resultados</div>';
    return;
  }

  container.innerHTML = filtered.map(contact => `
    <div class="contact-card">
      <div class="contact-avatar">${contact.name?.charAt(0).toUpperCase()}</div>
      <div class="contact-name">${contact.name || 'Sin nombre'}</div>
      ${contact.job_title ? `<div class="contact-info">📌 ${contact.job_title}</div>` : ''}
      ${contact.email ? `<div class="contact-info">📧 ${contact.email}</div>` : ''}
      ${contact.phone ? `<div class="contact-info">📱 ${contact.phone}</div>` : ''}
      ${contact.company ? `<div class="contact-info">🏢 ${contact.company}</div>` : ''}
      <div class="contact-actions">
        <button class="btn-edit" onclick="editContact('${contact.id}')">Editar</button>
        <button class="btn-delete" onclick="deleteContact('${contact.id}')">Eliminar</button>
      </div>
    </div>
  `).join('');
}

function openModal() {
  editingId = null;
  document.getElementById('modalTitle').textContent = 'Nuevo Contacto';
  document.getElementById('contactForm').reset();
  document.getElementById('contactModal').style.display = 'flex';
}

function closeModal() {
  document.getElementById('contactModal').style.display = 'none';
}

function editContact(id) {
  const contact = contacts.find(c => c.id === id);
  if (!contact) return;

  editingId = id;
  document.getElementById('modalTitle').textContent = 'Editar Contacto';
  document.getElementById('formName').value = contact.name || '';
  document.getElementById('formEmail').value = contact.email || '';
  document.getElementById('formPhone').value = contact.phone || '';
  document.getElementById('formCompany').value = contact.company || '';
  document.getElementById('formJobTitle').value = contact.job_title || '';
  document.getElementById('contactModal').style.display = 'flex';
}

async function saveContact() {
  const name = document.getElementById('formName').value.trim();
  if (!name) {
    alert('El nombre es requerido');
    return;
  }

  const payload = {
    name: name,
    email: document.getElementById('formEmail').value,
    phone: document.getElementById('formPhone').value,
    company: document.getElementById('formCompany').value,
    job_title: document.getElementById('formJobTitle').value
  };

  try {
    const url = editingId ? `${API_BASE}/${editingId}` : `${API_BASE}`;
    const method = editingId ? 'PUT' : 'POST';

    const response = await fetch(url, {
      method: method,
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-ID': tenantID
      },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      throw new Error(`Error ${response.status}: ${response.statusText}`);
    }

    closeModal();
    await loadContacts();
    alert(editingId ? 'Contacto actualizado' : 'Contacto creado');
  } catch (error) {
    alert('Error guardando contacto: ' + error.message);
  }
}

async function deleteContact(id) {
  if (!confirm('¿Eliminar este contacto?')) return;

  try {
    const response = await fetch(`${API_BASE}/${id}`, {
      method: 'DELETE',
      headers: { 'X-Tenant-ID': tenantID }
    });

    if (!response.ok) {
      throw new Error(`Error ${response.status}`);
    }

    await loadContacts();
    alert('Contacto eliminado');
  } catch (error) {
    alert('Error eliminando contacto: ' + error.message);
  }
}

// Cerrar modal al hacer clic fuera
document.getElementById('contactModal').addEventListener('click', (e) => {
  if (e.target.id === 'contactModal') closeModal();
});
