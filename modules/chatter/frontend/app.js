/**
 * Chatter Standalone — Página /admin/chatter
 *
 * Timeline global con filtros, paginación y composer.
 * Carga widget.js para las utilidades de render, pero maneja
 * su propia lógica de datos.
 */
(() => {
  'use strict';

  const API = '/api/chatter/message';
  const PAGE_SIZE = 20;

  /* ── Helpers ─────────────────────────────────────────── */

  function tenantID() {
    const token = localStorage.getItem('access_token');
    if (!token) return null;
    try { return JSON.parse(atob(token.split('.')[1])).tenant_id; }
    catch { return null; }
  }

  function userID() {
    const token = localStorage.getItem('access_token');
    if (!token) return null;
    try { return JSON.parse(atob(token.split('.')[1])).user_id || null; }
    catch { return null; }
  }

  function userName() {
    const token = localStorage.getItem('access_token');
    if (!token) return 'Usuario';
    try {
      const p = JSON.parse(atob(token.split('.')[1]));
      return p.name || p.username || p.email || 'Usuario';
    } catch { return 'Usuario'; }
  }

  function headers() {
    return {
      'X-Tenant-ID': tenantID(),
      'Content-Type': 'application/json',
      'Authorization': 'Bearer ' + (localStorage.getItem('access_token') || ''),
    };
  }

  function esc(value) {
    return String(value ?? '').replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  function relativeTime(dateStr) {
    const d = new Date(dateStr);
    const diffMs = Date.now() - d.getTime();
    const diffMin = Math.floor(diffMs / 60000);
    if (diffMin < 1) return 'ahora';
    if (diffMin < 60) return `hace ${diffMin}m`;
    const diffH = Math.floor(diffMin / 60);
    if (diffH < 24) return `hace ${diffH}h`;
    const diffD = Math.floor(diffH / 24);
    if (diffD < 7) return `hace ${diffD}d`;
    return d.toLocaleDateString('es-EC', { day: 'numeric', month: 'short', year: 'numeric' });
  }

  function formatFullDate(dateStr) {
    return new Date(dateStr).toLocaleDateString('es-EC', {
      day: 'numeric', month: 'long', year: 'numeric',
      hour: '2-digit', minute: '2-digit',
    });
  }

  function initials(name) {
    return (name || '?').split(' ').map(w => w[0]).join('').substring(0, 2).toUpperCase();
  }

  function typeLabel(type) {
    return { comment: 'Comentario', note: 'Nota', system: 'Sistema' }[type] || type;
  }

  /* ── Estado ─────────────────────────────────────────── */

  let currentPage = 1;
  let currentFilters = { type: '', channel: '', search: '' };
  let totalMessages = 0;
  let currentType = 'comment';

  /* ── DOM ────────────────────────────────────────────── */

  const $ = (id) => document.getElementById(id);

  /* ── API ────────────────────────────────────────────── */

  async function fetchMessages() {
    const params = new URLSearchParams({
      page: String(currentPage),
      limit: String(PAGE_SIZE),
      order_by: 'created_at',
      order_dir: 'desc',
    });
    if (currentFilters.type) params.set('message_type', currentFilters.type);
    if (currentFilters.channel) params.set('channel', currentFilters.channel);
    if (currentFilters.search) params.set('search', currentFilters.search);

    const res = await fetch(`${API}?${params}`, { headers: headers() });
    if (!res.ok) throw new Error(`Error ${res.status}`);
    const json = await res.json();
    return { data: json.data || [], total: json.total || 0 };
  }

  async function createMessage(data) {
    const res = await fetch(API, {
      method: 'POST',
      headers: headers(),
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(err.error || `Error ${res.status}`);
    }
    return res.json();
  }

  /* ── Render: Timeline ───────────────────────────────── */

  function renderTimeline(messages) {
    const container = $('ch-timeline');
    container.innerHTML = '';

    if (!messages.length) {
      container.innerHTML = '<div class="ch-empty">No hay mensajes. Creá el primero con el formulario de abajo.</div>';
      return;
    }

    for (const msg of messages) {
      container.appendChild(renderMessage(msg));
    }
  }

  function renderMessage(msg) {
    const div = document.createElement('div');
    div.className = 'ch-msg ch-msg--' + esc(msg.message_type || 'comment');
    div.dataset.id = msg.id;

    const badgeClass = 'ch-msg-badge ch-msg-badge--' + esc(msg.message_type || 'comment');
    const recordBadge = msg.record_model
      ? `<span class="ch-msg-badge ch-msg-badge--comment" title="${esc(msg.record_model)}:${esc(msg.record_id)}">${esc(msg.record_model.split('/')[0])}</span>`
      : '';

    div.innerHTML = `
      <div class="ch-msg-avatar">${esc(initials(msg.author_name))}</div>
      <div class="ch-msg-body">
        <div class="ch-msg-header">
          <span class="ch-msg-author">${esc(msg.author_name || 'Anónimo')}</span>
          <span class="${badgeClass}">${esc(typeLabel(msg.message_type))}</span>
          ${recordBadge}
          <span class="ch-msg-time" title="${esc(formatFullDate(msg.created_at))}">${esc(relativeTime(msg.created_at))}</span>
        </div>
        <div class="ch-msg-text">${esc(msg.body)}</div>
      </div>
    `;

    return div;
  }

  /* ── Render: Paginación ─────────────────────────────── */

  function renderPagination() {
    const el = $('ch-pagination');
    const totalPages = Math.ceil(totalMessages / PAGE_SIZE);
    if (totalPages <= 1) { el.innerHTML = ''; return; }

    let html = '';
    html += `<button class="ch-page-btn" data-page="${currentPage - 1}" ${currentPage <= 1 ? 'disabled' : ''}>← Anterior</button>`;

    const start = Math.max(1, currentPage - 2);
    const end = Math.min(totalPages, currentPage + 2);
    for (let i = start; i <= end; i++) {
      html += `<button class="ch-page-btn ${i === currentPage ? 'active' : ''}" data-page="${i}">${i}</button>`;
    }

    html += `<button class="ch-page-btn" data-page="${currentPage + 1}" ${currentPage >= totalPages ? 'disabled' : ''}>Siguiente →</button>`;

    el.innerHTML = html;
    el.querySelectorAll('.ch-page-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        const p = parseInt(btn.dataset.page);
        if (p >= 1 && p <= totalPages) {
          currentPage = p;
          loadMessages();
        }
      });
    });
  }

  function renderCount() {
    const el = $('ch-count');
    if (el) el.textContent = `${totalMessages} mensaje${totalMessages !== 1 ? 's' : ''}`;
  }

  /* ── Carga ──────────────────────────────────────────── */

  async function loadMessages() {
    try {
      const result = await fetchMessages();
      totalMessages = result.total;
      renderTimeline(result.data);
      renderPagination();
      renderCount();
    } catch (e) {
      $('ch-timeline').innerHTML = `<div class="ch-empty">Error: ${esc(e.message)}</div>`;
    }
  }

  async function loadChannels() {
    try {
      const res = await fetch(`${API}?limit=200&order_by=channel&order_dir=asc`, { headers: headers() });
      if (!res.ok) return;
      const json = await res.json();
      const channels = [...new Set((json.data || []).map(m => m.channel).filter(Boolean))];
      const select = $('ch-filter-channel');
      if (!select) return;
      for (const ch of channels) {
        const opt = document.createElement('option');
        opt.value = ch;
        opt.textContent = ch;
        select.appendChild(opt);
      }
    } catch { /* ignore */ }
  }

  /* ── Composer ───────────────────────────────────────── */

  function initComposer() {
    // Tabs de tipo
    document.querySelectorAll('.ch-composer-tab').forEach(tab => {
      tab.addEventListener('click', () => {
        document.querySelectorAll('.ch-composer-tab').forEach(t => t.classList.remove('active'));
        tab.classList.add('active');
        currentType = tab.dataset.type;
        const info = document.querySelector('.ch-composer-info');
        const ta = $('ch-composer-body');
        if (currentType === 'note') {
          info.textContent = 'Las notas son solo visibles para usuarios del sistema.';
          ta.placeholder = 'Escribí una nota interna…';
        } else {
          info.textContent = 'Los comentarios son visibles para todos.';
          ta.placeholder = 'Escribí un mensaje…';
        }
      });
    });

    // Submit
    $('ch-composer-submit').addEventListener('click', async () => {
      const ta = $('ch-composer-body');
      const body = ta.value.trim();
      if (!body) return;

      const btn = $('ch-composer-submit');
      btn.disabled = true;
      btn.textContent = 'Enviando…';

      try {
        await createMessage({
          body: body,
          message_type: currentType,
          author_name: userName(),
          author_id: userID(),
          record_model: '',
          record_id: '',
          channel: '',
        });
        ta.value = '';
        currentPage = 1;
        await loadMessages();
      } catch (e) {
        alert('Error al enviar: ' + e.message);
      } finally {
        btn.disabled = false;
        btn.textContent = 'Enviar';
      }
    });
  }

  /* ── Filtros ────────────────────────────────────────── */

  function initFilters() {
    const fType = $('ch-filter-type');
    const fChannel = $('ch-filter-channel');
    const fSearch = $('ch-search');

    if (fType) {
      fType.addEventListener('change', () => {
        currentFilters.type = fType.value;
        currentPage = 1;
        loadMessages();
      });
    }

    if (fChannel) {
      fChannel.addEventListener('change', () => {
        currentFilters.channel = fChannel.value;
        currentPage = 1;
        loadMessages();
      });
    }

    if (fSearch) {
      let debounce;
      fSearch.addEventListener('input', () => {
        clearTimeout(debounce);
        debounce = setTimeout(() => {
          currentFilters.search = fSearch.value.trim();
          currentPage = 1;
          loadMessages();
        }, 300);
      });
    }
  }

  /* ── Init ───────────────────────────────────────────── */

  function init() {
    initFilters();
    initComposer();
    loadChannels();
    loadMessages();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
