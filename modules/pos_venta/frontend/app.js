// POS Venta - Terminal de punto de venta

const API = {
  async get(url) {
    const res = await fetch(url, { headers: { 'X-Tenant-ID': getTenantID() } });
    if (!res.ok) throw new Error(`API error: ${res.status}`);
    return res.json();
  },
  async post(url, data) {
    const res = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Tenant-ID': getTenantID()
      },
      body: JSON.stringify(data)
    });
    if (!res.ok) throw new Error(`API error: ${res.status}`);
    return res.json();
  }
};

function getTenantID() {
  return localStorage.getItem('tenant_id') || 'default';
}

const POS = {
  carrito: [],
  iva: 0.19,

  async init() {
    await this.cargarProductos();
    await this.cargarPuntosVenta();
    this.setupEventos();
  },

  async cargarProductos() {
    try {
      const data = await API.get('/api/products/product');
      const productos = data.data || [];

      const html = productos
        .filter(p => p.active)
        .map(p => `
          <div class="pos-producto" data-id="${p.id}">
            <div class="pos-prod-nombre">${p.name}</div>
            <div class="pos-prod-sku">${p.sku || ''}</div>
            <div class="pos-prod-precio">$${parseFloat(p.sale_price || 0).toFixed(2)}</div>
            <div class="pos-prod-stock">Stock: ${p.stock_quantity || 0}</div>
            <button class="pos-btn-agregar" data-id="${p.id}" data-nombre="${p.name}" data-precio="${p.sale_price}">+ Agregar</button>
          </div>
        `).join('');

      document.getElementById('pos-lista-productos').innerHTML = html || '<p>No hay productos activos</p>';
      this.setupBotonesAgregar();
    } catch (err) {
      console.error('Error cargando productos:', err);
      document.getElementById('pos-lista-productos').innerHTML = '<p>Error cargando productos</p>';
    }
  },

  async cargarPuntosVenta() {
    try {
      const data = await API.get('/api/pos_venta/punto_venta');
      const puntos = data.data || [];

      const select = document.getElementById('pos-punto-venta');
      select.innerHTML = '<option value="">Seleccionar...</option>' +
        puntos.map(p => `<option value="${p.id}">${p.nombre}</option>`).join('');
    } catch (err) {
      console.error('Error cargando puntos de venta:', err);
    }
  },

  setupBotonesAgregar() {
    document.querySelectorAll('.pos-btn-agregar').forEach(btn => {
      btn.addEventListener('click', (e) => {
        const id = e.target.dataset.id;
        const nombre = e.target.dataset.nombre;
        const precio = parseFloat(e.target.dataset.precio) || 0;

        this.agregarAlCarrito(id, nombre, precio);
      });
    });
  },

  agregarAlCarrito(id, nombre, precio) {
    const existente = this.carrito.find(item => item.id === id);

    if (existente) {
      existente.cantidad++;
    } else {
      this.carrito.push({ id, nombre, precio, cantidad: 1 });
    }

    this.renderizarCarrito();
  },

  renderizarCarrito() {
    const container = document.getElementById('pos-items');

    if (this.carrito.length === 0) {
      container.innerHTML = '<div class="pos-empty">Sin productos</div>';
    } else {
      container.innerHTML = this.carrito.map((item, idx) => `
        <div class="pos-item">
          <div class="pos-item-info">
            <div class="pos-item-nombre">${item.nombre}</div>
            <div class="pos-item-precio">$${item.precio.toFixed(2)} c/u</div>
          </div>
          <div class="pos-item-cantidad">
            <button class="pos-btn-menos" data-idx="${idx}">−</button>
            <input type="number" value="${item.cantidad}" min="1" data-idx="${idx}" class="pos-cantidad">
            <button class="pos-btn-mas" data-idx="${idx}">+</button>
          </div>
          <div class="pos-item-subtotal">$${(item.precio * item.cantidad).toFixed(2)}</div>
          <button class="pos-btn-eliminar" data-idx="${idx}">🗑</button>
        </div>
      `).join('');

      // Eventos de cantidad
      container.querySelectorAll('.pos-btn-menos').forEach(btn => {
        btn.addEventListener('click', (e) => {
          const idx = parseInt(e.target.dataset.idx);
          if (this.carrito[idx].cantidad > 1) this.carrito[idx].cantidad--;
          this.renderizarCarrito();
        });
      });

      container.querySelectorAll('.pos-btn-mas').forEach(btn => {
        btn.addEventListener('click', (e) => {
          const idx = parseInt(e.target.dataset.idx);
          this.carrito[idx].cantidad++;
          this.renderizarCarrito();
        });
      });

      container.querySelectorAll('.pos-cantidad').forEach(input => {
        input.addEventListener('change', (e) => {
          const idx = parseInt(e.target.dataset.idx);
          const cant = parseInt(e.target.value) || 1;
          if (cant > 0) this.carrito[idx].cantidad = cant;
          this.renderizarCarrito();
        });
      });

      container.querySelectorAll('.pos-btn-eliminar').forEach(btn => {
        btn.addEventListener('click', (e) => {
          const idx = parseInt(e.target.dataset.idx);
          this.carrito.splice(idx, 1);
          this.renderizarCarrito();
        });
      });
    }

    this.calcularTotales();
  },

  calcularTotales() {
    const subtotal = this.carrito.reduce((sum, item) => sum + (item.precio * item.cantidad), 0);
    const impuesto = subtotal * this.iva;
    const total = subtotal + impuesto;

    document.getElementById('pos-subtotal').textContent = `$${subtotal.toFixed(2)}`;
    document.getElementById('pos-impuesto').textContent = `$${impuesto.toFixed(2)}`;
    document.getElementById('pos-total').textContent = `$${total.toFixed(2)}`;
  },

  setupEventos() {
    document.getElementById('pos-cancelar').addEventListener('click', () => {
      if (confirm('¿Cancelar venta?')) {
        this.carrito = [];
        this.renderizarCarrito();
        document.getElementById('pos-cliente-nombre').value = '';
        document.getElementById('pos-punto-venta').value = '';
      }
    });

    document.getElementById('pos-guardar').addEventListener('click', () => this.guardarVenta());

    document.getElementById('pos-buscar').addEventListener('input', (e) => {
      const texto = e.target.value.toLowerCase();
      document.querySelectorAll('.pos-producto').forEach(prod => {
        const nombre = prod.querySelector('.pos-prod-nombre').textContent.toLowerCase();
        const sku = prod.querySelector('.pos-prod-sku').textContent.toLowerCase();
        prod.style.display = nombre.includes(texto) || sku.includes(texto) ? '' : 'none';
      });
    });
  },

  async guardarVenta() {
    if (this.carrito.length === 0) {
      alert('Agregar productos al carrito');
      return;
    }

    const puntoVentaId = document.getElementById('pos-punto-venta').value;
    if (!puntoVentaId) {
      alert('Seleccionar punto de venta');
      return;
    }

    const clienteNombre = document.getElementById('pos-cliente-nombre').value || 'Consumidor Final';
    const tipoDoc = document.getElementById('pos-tipo-doc').value;
    const metodoPago = document.getElementById('pos-metodo-pago').value;
    const referencia = document.getElementById('pos-referencia').value;

    const subtotal = this.carrito.reduce((sum, item) => sum + (item.precio * item.cantidad), 0);
    const impuesto = subtotal * this.iva;
    const total = subtotal + impuesto;

    try {
      // Crear venta
      const ventaResp = await API.post('/api/pos_venta/venta', {
        numero_documento: `POS-${Date.now()}`,
        tipo_documento: tipoDoc,
        punto_venta_id: puntoVentaId,
        cliente_nombre: clienteNombre,
        fecha_venta: new Date().toISOString(),
        subtotal: subtotal,
        impuesto: impuesto,
        total: total,
        estado: 'borrador'
      });

      const ventaID = ventaResp.id;

      // Crear líneas de venta
      for (let i = 0; i < this.carrito.length; i++) {
        const item = this.carrito[i];
        await API.post('/api/pos_venta/linea_venta', {
          venta_id: ventaID,
          producto_id: item.id,
          producto_nombre: item.nombre,
          cantidad: item.cantidad,
          precio_unitario: item.precio,
          subtotal: item.precio * item.cantidad,
          secuencia: i + 1
        });
      }

      // Registrar pago
      await API.post('/api/pos_venta/pago_venta', {
        venta_id: ventaID,
        metodo_pago: metodoPago,
        monto: total,
        referencia: referencia,
        fecha_pago: new Date().toISOString()
      });

      alert('✓ Venta registrada: ' + ventaID);

      // Limpiar
      this.carrito = [];
      this.renderizarCarrito();
      document.getElementById('pos-cliente-nombre').value = '';
      document.getElementById('pos-referencia').value = '';

    } catch (err) {
      alert('Error: ' + err.message);
      console.error(err);
    }
  }
};

// Inicializar al cargar
document.addEventListener('DOMContentLoaded', () => POS.init());
