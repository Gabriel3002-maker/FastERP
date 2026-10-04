/**
 * ChatterWidget — Widget embebible de mensajería para FastERP.
 *
 * Soporta: @mentions, notificaciones, hilos, tipos de mensaje.
 *
 * API:
 *   ChatterWidget.render(container, options)
 *   ChatterWidget.destroy(container)
 *   ChatterWidget.getUnreadCount() → Promise<number>
 */
(() => {
  'use strict';

  if (window.ChatterWidget) return;

  const API = '/api/chatter/message';
  const USERS_API = '/api/chatter/users';
  const NOTIF_API = '/api/chatter/notifications';
  const PAGE_SIZE = 20;

  /* ── Helpers ─────────────────────────────────────────── */

  // Tenant, id de usuario y nombre salen de FastClient, que ya sabe decodificar
  // el token. Este fichero tenía su propia copia y pedía el claim "user_id",
  // que el servidor no emite: el campo es "sub". Por eso el chatter siempre
  //Detectaba como "Usuario" y pedía las notificaciones sin usuario.
  function userID() {
    return FastClient.claim('sub');
  }

  function userName() {
    const user = FastClient.user();
    if (user) return user.username || user.email || 'Usuario';
    return FastClient.claim('username') || 'Usuario';
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

  /** Resalta @menciones en el texto del mensaje */
  function highlightMentions(text) {
    return esc(text).replace(/@(\w+)/g, '<span class="ch-mention">@$1</span>');
  }

  /* ── Users cache (para autocomplete) ────────────────── */

  let cachedUsers = null;
  let usersCacheTime = 0;
  const USERS_CACHE_TTL = 60000; // 1 min

  async function fetchUsers() {
    const now = Date.now();
    if (cachedUsers && (now - usersCacheTime) < USERS_CACHE_TTL) {
      return cachedUsers;
    }
    try {
      const json = await FastClient.get(USERS_API);
      if (!json) return [];
      cachedUsers = json.data || [];
      usersCacheTime = now;
      return cachedUsers;
    } catch { return []; }
  }

  /* ── API calls ──────────────────────────────────────── */

  async function fetchMessages(recordModel, recordId, page) {
    const params = new URLSearchParams({
      page: String(page), limit: String(PAGE_SIZE),
      order_by: 'created_at', order_dir: 'asc',
    });
    if (recordModel) params.set('record_model', recordModel);
    if (recordId) params.set('record_id', recordId);
    const json = await FastClient.get(`${API}?${params}`);
    return { data: json.data || [], total: json.total || 0 };
  }

  async function createMessage(data) {
    return FastClient.post(API, data);
  }

  async function createNotification(data) {
    try {
      await FastClient.post(NOTIF_API, data);
    } catch { /* best effort */ }
  }

  /** Extrae menciones del body: busca patrones @Nombre */
  function extractMentions(body, users) {
    const mentions = [];
    const regex = /@(\w+)/g;
    let match;
    while ((match = regex.exec(body)) !== null) {
      const name = match[1];
      const user = users.find(u =>
        u.name.toLowerCase() === name.toLowerCase() ||
        u.id.toLowerCase() === name.toLowerCase()
      );
      if (user) {
        mentions.push({ id: user.id, name: user.name });
      }
    }
    return mentions;
  }

  /** Crea notificaciones para usuarios mencionados */
  async function notifyMentioned(mentions, body, authorName, recordModel, recordId, messageId) {
    const currentUserId = userID();
    for (const m of mentions) {
      if (m.id === currentUserId) continue; // no notificar a uno mismo
      await createNotification({
        user_id: m.id,
        user_name: m.name,
        type: 'mention',
        message: `${authorName} te mencionó: ${body.substring(0, 100)}`,
        message_id: messageId,
        record_model: recordModel,
        record_id: recordId,
        author_name: authorName,
      });
    }
  }

  /* ── Render helpers ─────────────────────────────────── */

  function renderMessage(msg, opts, depth) {
    depth = depth || 0;
    const div = document.createElement('div');
    div.className = 'ch-msg ch-msg--' + esc(msg.message_type || 'comment');
    div.dataset.id = msg.id;

    div.innerHTML = `
      <div class="ch-msg-avatar">${esc(initials(msg.author_name))}</div>
      <div class="ch-msg-body">
        <div class="ch-msg-header">
          <span class="ch-msg-author">${esc(msg.author_name || 'Anónimo')}</span>
          <span class="ch-msg-badge ch-msg-badge--${esc(msg.message_type || 'comment')}">${esc(typeLabel(msg.message_type))}</span>
          <span class="ch-msg-time" title="${esc(formatFullDate(msg.created_at))}">${esc(relativeTime(msg.created_at))}</span>
        </div>
        <div class="ch-msg-text">${highlightMentions(msg.body)}</div>
        ${opts.showComposer && depth < 3 ? `
        <div class="ch-msg-actions">
          <button class="ch-msg-action" data-action="reply" data-parent-id="${esc(msg.id)}">↩ Responder</button>
        </div>` : ''}
      </div>`;

    if (opts.showComposer && depth < 3) {
      div.querySelector('[data-action="reply"]')?.addEventListener('click', () => {
        const existing = div.querySelector('.ch-reply-composer');
        if (existing) { existing.remove(); return; }
        const composer = createReplyComposer(msg.id, opts, div);
        div.querySelector('.ch-msg-body').appendChild(composer);
        composer.querySelector('textarea').focus();
      });
    }

    return div;
  }

  function createReplyComposer(parentId, opts, msgDiv) {
    const div = document.createElement('div');
    div.className = 'ch-reply-composer';
    div.innerHTML = `
      <textarea placeholder="Escribí una respuesta…" rows="2"></textarea>
      <div class="ch-reply-composer-actions">
        <button class="ch-reply-cancel">Cancelar</button>
        <button class="ch-reply-send">Enviar</button>
      </div>`;

    div.querySelector('.ch-reply-cancel').addEventListener('click', () => div.remove());
    div.querySelector('.ch-reply-send').addEventListener('click', async () => {
      const textarea = div.querySelector('textarea');
      const body = textarea.value.trim();
      if (!body) return;
      const btn = div.querySelector('.ch-reply-send');
      btn.disabled = true;
      btn.textContent = 'Enviando…';
      try {
        const users = await fetchUsers();
        const mentions = extractMentions(body, users);
        await createMessage({
          body, message_type: 'comment',
          author_name: opts.authorName || userName(),
          author_id: opts.authorId || userID(),
          record_model: opts.recordModel, record_id: opts.recordId,
          parent_id: parentId, mentions: mentions,
        });
        await notifyMentioned(mentions, body, opts.authorName || userName(),
          opts.recordModel, opts.recordId, null);
        div.remove();
        if (opts._reload) opts._reload();
      } catch (e) {
        btn.disabled = false;
        btn.textContent = 'Enviar';
        alert('Error: ' + e.message);
      }
    });

    return div;
  }

  function renderComposer(opts) {
    const div = document.createElement('div');
    div.className = 'ch-composer';
    let currentType = 'comment';

    div.innerHTML = `
      <div class="ch-composer-header">
        <button class="ch-composer-tab active" data-type="comment">💬 Comentario</button>
        <button class="ch-composer-tab" data-type="note">📝 Nota interna</button>
      </div>
      <div class="ch-composer-input-wrap">
        <textarea placeholder="Escribí un mensaje… (usá @ para mencionar)" rows="3"></textarea>
        <div class="ch-mention-dropdown" hidden></div>
      </div>
      <div class="ch-composer-footer">
        <span class="ch-composer-info">Los comentarios son visibles para todos.</span>
        <button class="ch-composer-submit">Enviar</button>
      </div>`;

    // Tabs
    div.querySelectorAll('.ch-composer-tab').forEach(tab => {
      tab.addEventListener('click', () => {
        div.querySelectorAll('.ch-composer-tab').forEach(t => t.classList.remove('active'));
        tab.classList.add('active');
        currentType = tab.dataset.type;
        const info = div.querySelector('.ch-composer-info');
        const ta = div.querySelector('textarea');
        if (currentType === 'note') {
          info.textContent = 'Las notas son solo visibles para usuarios del sistema.';
          ta.placeholder = 'Escribí una nota interna…';
        } else {
          info.textContent = 'Los comentarios son visibles para todos.';
          ta.placeholder = 'Escribí un mensaje… (usá @ para mencionar)';
        }
      });
    });

    // @mentions autocomplete
    const textarea = div.querySelector('textarea');
    const dropdown = div.querySelector('.ch-mention-dropdown');
    setupMentions(textarea, dropdown);

    // Submit
    div.querySelector('.ch-composer-submit').addEventListener('click', async () => {
      const body = textarea.value.trim();
      if (!body) return;
      const btn = div.querySelector('.ch-composer-submit');
      btn.disabled = true;
      btn.textContent = 'Enviando…';
      try {
        const users = await fetchUsers();
        const mentions = extractMentions(body, users);
        const result = await createMessage({
          body, message_type: currentType,
          author_name: opts.authorName || userName(),
          author_id: opts.authorId || userID(),
          record_model: opts.recordModel, record_id: opts.recordId,
          channel: opts.channel || '', mentions: mentions,
        });
        await notifyMentioned(mentions, body, opts.authorName || userName(),
          opts.recordModel, opts.recordId, result?.id);
        textarea.value = '';
        btn.disabled = false;
        btn.textContent = 'Enviar';
        if (opts._reload) opts._reload();
      } catch (e) {
        btn.disabled = false;
        btn.textContent = 'Enviar';
        alert('Error: ' + e.message);
      }
    });

    return div;
  }

  /* ── @Mentions autocomplete ─────────────────────────── */

  function setupMentions(textarea, dropdown) {
    let activeMention = null;

    textarea.addEventListener('input', async () => {
      const pos = textarea.selectionStart;
      const text = textarea.value.substring(0, pos);
      const atMatch = text.match(/@(\w*)$/);

      if (!atMatch) {
        dropdown.hidden = true;
        activeMention = null;
        return;
      }

      const query = atMatch[1].toLowerCase();
      activeMention = { start: pos - atMatch[0].length, query };

      const users = await fetchUsers();
      const filtered = users.filter(u =>
        u.name.toLowerCase().includes(query) || u.id.toLowerCase().includes(query)
      ).slice(0, 8);

      if (!filtered.length) {
        dropdown.hidden = true;
        return;
      }

      dropdown.innerHTML = filtered.map(u => `
        <div class="ch-mention-item" data-user-id="${esc(u.id)}" data-user-name="${esc(u.name)}">
          <span class="ch-mention-avatar">${esc(initials(u.name))}</span>
          <span class="ch-mention-name">${esc(u.name)}</span>
        </div>
      `).join('');
      dropdown.hidden = false;

      dropdown.querySelectorAll('.ch-mention-item').forEach(item => {
        item.addEventListener('click', () => {
          const userId = item.dataset.userId;
          const userName = item.dataset.userName;
          const before = textarea.value.substring(0, activeMention.start);
          const after = textarea.value.substring(pos);
          textarea.value = before + '@' + userName + ' ' + after;
          textarea.focus();
          dropdown.hidden = true;
          activeMention = null;
        });
      });
    });

    textarea.addEventListener('keydown', (e) => {
      if (dropdown.hidden) return;
      if (e.key === 'Escape') {
        dropdown.hidden = true;
        activeMention = null;
      }
    });

    // Cerrar al hacer click fuera
    textarea.addEventListener('blur', () => {
      setTimeout(() => { dropdown.hidden = true; }, 200);
    });
  }

  /* ── Pagination ─────────────────────────────────────── */

  function renderPagination(container, page, total, onPage) {
    const totalPages = Math.ceil(total / PAGE_SIZE);
    if (totalPages <= 1) { container.innerHTML = ''; return; }
    let html = `<button class="ch-page-btn" data-page="${page - 1}" ${page <= 1 ? 'disabled' : ''}>←</button>`;
    const start = Math.max(1, page - 2);
    const end = Math.min(totalPages, page + 2);
    for (let i = start; i <= end; i++) {
      html += `<button class="ch-page-btn ${i === page ? 'active' : ''}" data-page="${i}">${i}</button>`;
    }
    html += `<button class="ch-page-btn" data-page="${page + 1}" ${page >= totalPages ? 'disabled' : ''}>→</button>`;
    container.innerHTML = html;
    container.querySelectorAll('.ch-page-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        const p = parseInt(btn.dataset.page);
        if (p >= 1 && p <= totalPages) onPage(p);
      });
    });
  }

  /* ── Timeline render ────────────────────────────────── */

  function renderTimeline(container, messages, opts) {
    container.innerHTML = '';
    if (!messages.length) {
      container.innerHTML = '<div class="ch-empty">No hay mensajes aún.</div>';
      return;
    }
    const reversed = [...messages].reverse();
    const groups = [];
    let currentDate = '';
    for (const msg of reversed) {
      const d = new Date(msg.created_at);
      const key = d.toLocaleDateString('es-EC', { day: 'numeric', month: 'long', year: 'numeric' });
      if (key !== currentDate) { currentDate = key; groups.push({ date: key, messages: [] }); }
      groups[groups.length - 1].messages.push(msg);
    }

    for (const group of groups) {
      const dateDiv = document.createElement('div');
      dateDiv.className = 'ch-date-group';
      dateDiv.innerHTML = `<div class="ch-date-label">${esc(group.date)}</div>`;
      for (const msg of group.messages) {
        if (msg.parent_id) continue;
        dateDiv.appendChild(renderMessage(msg, opts, 0));
        const replies = messages.filter(m => m.parent_id === msg.id);
        if (replies.length) {
          const repliesDiv = document.createElement('div');
          repliesDiv.className = 'ch-replies';
          for (const reply of replies) repliesDiv.appendChild(renderMessage(reply, opts, 1));
          dateDiv.appendChild(repliesDiv);
        }
      }
      container.appendChild(dateDiv);
    }
  }

  /* ── Widget instance ────────────────────────────────── */

  class WidgetInstance {
    constructor(container, opts) {
      this.container = container;
      this.opts = Object.assign({
        recordModel: '', recordId: '', showComposer: true,
        maxHeight: '500px', channel: '', authorName: '', authorId: '',
      }, opts);
      this.page = 1;
      this.messages = [];
      this.total = 0;

      this.container.classList.add('ch-widget');
      this.container.style.maxHeight = this.opts.maxHeight;
      this.container.style.overflowY = 'auto';

      this.timeline = document.createElement('div');
      this.timeline.className = 'ch-timeline';
      this.container.appendChild(this.timeline);

      this.paginationEl = document.createElement('div');
      this.paginationEl.className = 'ch-pagination';
      this.container.appendChild(this.paginationEl);

      if (this.opts.showComposer) {
        this.composer = renderComposer(this.opts);
        this.opts._reload = () => this.load();
        this.container.appendChild(this.composer);
      }

      this.load();
    }

    async load() {
      try {
        const result = await fetchMessages(this.opts.recordModel, this.opts.recordId, this.page);
        this.messages = result.data;
        this.total = result.total;
        renderTimeline(this.timeline, this.messages, this.opts);
        renderPagination(this.paginationEl, this.page, this.total, (p) => { this.page = p; this.load(); });
      } catch (e) {
        this.timeline.innerHTML = `<div class="ch-empty">Error: ${esc(e.message)}</div>`;
      }
    }

    destroy() {
      this.container.innerHTML = '';
      this.container.classList.remove('ch-widget');
    }
  }

  /* ── Public API ─────────────────────────────────────── */

  const instances = new Map();

  window.ChatterWidget = {
    render(container, options) {
      if (!container) throw new Error('ChatterWidget: container required');
      const key = container.id || container;
      if (instances.has(key)) instances.get(key).destroy();
      const inst = new WidgetInstance(container, options);
      instances.set(key, inst);
      return inst;
    },

    destroy(container) {
      const key = container.id || container;
      if (instances.has(key)) { instances.get(key).destroy(); instances.delete(key); }
    },

    async getUnreadCount() {
      const uid = userID();
      if (!uid) return 0;
      try {
        // FastClient ya devuelve el cuerpo parseado, así que no hay res.json().
        const data = await FastClient.get(`${NOTIF_API}?user_id=${encodeURIComponent(uid)}&unread=true&limit=1`);
        return (data && data.unread_count) || 0;
      } catch { return 0; }
    },

    async logActivity(recordModel, recordId, body, authorName) {
      return createMessage({
        body, message_type: 'system',
        author_name: authorName || 'Sistema',
        record_model: recordModel, record_id: recordId,
      });
    },
  };
})();
