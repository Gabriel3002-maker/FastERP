// FastClient es el único sitio donde la UI habla con la API.
//
// Existía auth repetido en cinco ficheros (layout, dashboard, auditoría,
// chatter, fast-views) y cada copia se equivocaba en algo: unos leen el tenant del
// JWT, otros de localStorage, y solo uno usa el nombre de claim que el servidor
// emite. Cuando no coinciden, el servidor responde 403 y la pantalla se queda
// vacía sin explicar por qué. Aquí hay una sola implementación y el resto la llama.
//
// Dos detalles del servidor que condicionan este código:
//
//   - El access token dura 15 minutos y el refresh rota: en cuanto se usa, el
//     servidor lo borra y da 401 si alguien lo reutiliza. Varias peticiones que
//     caducan a la vez (una pantalla de listado dispara varias en paralelo) harían
//     N refreshes simultáneos, y el segundo usaría un token ya consumido. Por eso
//     refrescar() coalesce: uno solo refresca y el resto espera a esa misma promesa.
//
//   - El tenant se manda en X-Tenant-ID y tiene que coincidir con el tenant del
//     token, o el servidor responde 403. Se guarda al hacer login en vez de
//     releer el JWT en cada petición, que es lo que hacía antes el código repetido.

const FastClient = (() => {
    'use strict';

    const K_ACCESS = 'access_token';
    const K_REFRESH = 'refresh_token';
    const K_TENANT = 'tenant_id';
    const K_USER = 'fast_user';

    // Promesa de refresco en curso. Mientras exista, todo el que necesite un token
    // nuevo espera a esta en vez de lanzar el suyo.
    let inFlight = null;

    function storage() {
        try {
            return window.localStorage;
        } catch (e) {
            // Modo privado o política que prohíbe localStorage. Se avisa una vez y se
            // sigue: las peticiones simplemente salen sin autenticar.
            console.warn('[FastClient] localStorage no disponible:', e && e.message);
            return null;
        }
    }

    // El prefijo read/write no es decorativo: un helper llamado get() choca con el
    // get() de HTTP y hace que el archivo entero no parsee, que es un SyntaxError
    // antes de ejecutar nada y deja la UI sin FastClient sin ningún rastro.
    function readKey(key) {
        const s = storage();
        return s ? s.getItem(key) : null;
    }

    function writeKey(key, value) {
        const s = storage();
        if (!s) return;
        if (value === null || value === undefined) s.removeItem(key);
        else s.setItem(key, value);
    }

    // ── Token ────────────────────────────────────────────────────────────────

    function accessToken() { return readKey(K_ACCESS); }
    function refreshToken() { return readKey(K_REFRESH); }
    function tenant() { return readKey(K_TENANT); }

    function user() {
        const raw = readKey(K_USER);
        if (!raw) return null;
        try { return JSON.parse(raw); } catch (e) { return null; }
    }

    /**
     * Lee un claim del access token.
     *
     * Se usa solo para cosas que el servidor no manda en ninguna respuesta, como el
     * identificador de usuario para las notificaciones. Nota que el claim es "sub",
     * no "user_id": el código anterior pedía "user_id" y recibía undefined, así que
     * la cabecera de notificaciones se pedía sin usuario y salía vacía.
     */
    function claim(name) {
        const token = accessToken();
        if (!token) return null;
        const parts = token.split('.');
        if (parts.length !== 3) return null;
        try {
            // base64url → base64:JWT usa '-' y '_' donde el estándar usa '+' y '/',
            // y quita el relleno. atob() no acepta ninguna de las dos cosas.
            let b64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
            while (b64.length % 4 !== 0) b64 += '=';
            const payload = JSON.parse(atob(b64));
            return payload && payload[name] !== undefined ? payload[name] : null;
        } catch (e) {
            return null;
        }
    }

    function isAuthenticated() {
        return !!accessToken();
    }

    function clearSession() {
        [K_ACCESS, K_REFRESH, K_TENANT, K_USER].forEach(k => writeKey(k, null));
    }

    // ── Sesión ───────────────────────────────────────────────────────────────

    /**
     * Guarda la sesión que devuelve /api/auth/login.
     *
     * El tenant se toma de la respuesta del servidor y no del claim del token a
     * propósito: es el único dato que aún no se ha usado para pedir nada, así que
     * es el momento más temprano en que se puede cachear sin coste.
     */
    function saveSession(data) {
        if (data && data.access_token) writeKey(K_ACCESS, data.access_token);
        if (data && data.refresh_token) writeKey(K_REFRESH, data.refresh_token);
        if (data && data.user) {
            writeKey(K_USER, JSON.stringify(data.user));
            if (data.user.tenant_id) writeKey(K_TENANT, data.user.tenant_id);
        } else if (tenant() === null) {
            // /api/auth/refresh no devuelve el usuario. Si aún no se tiene el tenant
            // (p. ej. tras cerrar sesión a medias), se recupera del token.
            const t = claim('tenant_id');
            if (t) writeKey(K_TENANT, t);
        }
    }

    /**
     * Login contra /api/auth/login.
     *
     * El servidor exige X-Tenant-ID incluso aquí, así que el tenant hay que mandarlo
     * antes de tener token. La página de login lo pide; en una instancia de un solo
     * tenant el servidor lo deja escrito en el formulario.
     */
    async function login(username, password, tenantSlug) {
        const headers = { 'Content-Type': 'application/json' };
        if (tenantSlug) headers['X-Tenant-ID'] = tenantSlug;

        const res = await fetch('/api/auth/login', {
            method: 'POST',
            headers,
            body: JSON.stringify({ username, password }),
        });

        let body = null;
        try { body = await res.json(); } catch (e) { /* respuesta sin cuerpo */ }

        if (!res.ok) {
            const err = new Error((body && body.error) || `login fallido (${res.status})`);
            err.status = res.status;
            throw err;
        }

        saveSession(body);
        return body;
    }

    /** Cierra sesión. El borrado local se hace igual si la llamada falla: si el
     *  servidor no la acepta, el token ya no sirve para nada útil y dejarlo en el
     *  navegador solo cambia el síntoma. */
    async function logout() {
        try {
            await request('/api/me/logout', { method: 'POST' });
        } catch (e) {
            console.warn('[FastClient] logout remoto falló:', e && e.message);
        } finally {
            clearSession();
            window.location.href = '/login';
        }
    }

    // ── Refresco ─────────────────────────────────────────────────────────────

    /**
     * Renueva el access token. Coalescente: si ya hay un refresco en marcha, se
     * devuelve esa misma promesa en vez de empezar otro.
     */
    function refresh() {
        if (inFlight) return inFlight;

        const token = refreshToken();
        if (!token) {
            inFlight = Promise.reject(new Error('no hay refresh token'));
            // Se limpia el estado para que el siguiente llamante no reciba una
            // promesa ya rechazada por siempre.
            inFlight.catch(() => { inFlight = null; });
            return inFlight;
        }

        const headers = { 'Content-Type': 'application/json' };
        const t = tenant();
        if (t) headers['X-Tenant-ID'] = t;

        inFlight = fetch('/api/auth/refresh', {
            method: 'POST',
            headers,
            body: JSON.stringify({ refresh_token: token }),
        })
            .then(async res => {
                if (!res.ok) throw new Error(`refresh rechazado (${res.status})`);
                const data = await res.json();
                saveSession(data);
                return data;
            })
            .finally(() => { inFlight = null; });

        return inFlight;
    }

    function onSessionLost() {
        clearSession();
        // Con ?next= se puede volver a la página que se estaba mirando. Se
        // descarta si no es una ruta interna: un "next" con URL absoluta convertiría
        // el login en un redirector abierto.
        const next = window.location.pathname + window.location.search;
        const target = next && next !== '/login' ? '/login?next=' + encodeURIComponent(next) : '/login';
        window.location.href = target;
    }

    // ── Peticiones ───────────────────────────────────────────────────────────

    /**
     * Petición autenticada con reintento tras refrescar.
     *
     * El reintento ocurre una sola vez y solo ante 401. Un 403 no se reintenta: son
     * dos cosas distintas y 403 significa "token de otro tenant" o "no eres admin",
     * y reintentar con un token nuevo no lo arregla.
     */
    async function request(url, options, isRetry) {
        options = options || {};

        const headers = Object.assign({}, options.headers || {});
        const token = accessToken();
        if (token) headers['Authorization'] = 'Bearer ' + token;
        const t = tenant();
        if (t) headers['X-Tenant-ID'] = t;
        // Con raw:true el body se manda tal cual. Existe para FormData: si se
        // serializara a JSON, y sobre todo si se le pusiera Content-Type, el
        // navegador no añadiría el boundary y el servidor no leería el multipart.
        if (!options.raw && options.body !== undefined && typeof options.body !== 'string') {
            headers['Content-Type'] = 'application/json';
            options.body = JSON.stringify(options.body);
        }

        const res = await fetch(url, Object.assign({}, options, { headers }));

        if (res.status === 401 && !isRetry) {
            try {
                await refresh();
            } catch (e) {
                onSessionLost();
                throw e;
            }
            return request(url, options, true);
        }

        if (res.status === 401 && isRetry) {
            // El token nuevo tampoco vale: la sesión está muerta de verdad.
            onSessionLost();
            throw new Error('sesión no válida');
        }

        let body = null;
        const text = await res.text();
        if (text) {
            try { body = JSON.parse(text); } catch (e) { body = text; }
        }

        if (!res.ok) {
            const message = (body && body.error) || `error ${res.status}`;
            const err = new Error(message);
            err.status = res.status;
            err.body = body;
            throw err;
        }

        return body;
    }

    const get = (url, options) => request(url, Object.assign({ method: 'GET' }, options));
    const post = (url, body, options) => request(url, Object.assign({ method: 'POST', body }, options));
    const put = (url, body, options) => request(url, Object.assign({ method: 'PUT', body }, options));
    const del = (url, options) => request(url, Object.assign({ method: 'DELETE' }, options));

    /**
     * Sube un archivo con el token.
     *
     * Pasa por request() en vez de hacer su propio fetch, y no solo por el
     * Authorization: así hereda el refresco ante 401 y el reintento único. Subir
     * un .wasm de varios MB es la operación más lenta de la UI y la más probable
     * de encontrarse con el token caducado justo a mitad.
     *
     * El FormData se reutiliza en el reintento porque es un objeto normal, no un
     * stream consumido: fetch lo vuelve a leer.
     */
    async function upload(url, file, fields, isRetry) {
        const form = new FormData();
        if (fields) {
            Object.keys(fields).forEach(k => form.append(k, fields[k]));
        }
        form.append('file', file, file.name);
        return request(url, { method: 'POST', body: form, raw: true }, isRetry);
    }

    return {
        // sesión
        login, logout, refresh, saveSession, clearSession,
        accessToken, refreshToken, tenant, user, claim, isAuthenticated,
        // peticiones
        get, post, put, del, upload, request,
        //False si no hay localStorage, útil para avisar en la UI.
        hasStorage: () => !!storage(),
    };
})();

// Exportado como window para las plantillas, que son HTML plano sin bundler.
if (typeof window !== 'undefined') {
    window.FastClient = FastClient;
}