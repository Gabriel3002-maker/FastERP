# 🔗 Guía de Integración: FastERP ↔ Odoo

## 📋 Resumen

Este documento explica cómo configurar la sincronización entre **FastERP** (tu plataforma solar) y **Odoo** (tu ERP).

---

## 1️⃣ Configuración Inicial en Odoo

### Paso 1: Habilitar REST API

En Odoo (v14+):

```
Configuración → Técnico → Parámetros del Sistema
```

Crear estos parámetros (si no existen):

| Parámetro | Valor | Propósito |
|-----------|-------|----------|
| `web.base.url` | `https://odoo.ejemplo.com` | URL base |
| `ir.config_parameter/rest_api/enabled` | `True` | Habilitar API REST |

### Paso 2: Crear Usuario API en Odoo

```
Configuración → Usuarios → Nuevo Usuario
```

**Datos:**
- **Nombre:** `fasterp_api`
- **Email:** `fasterp@ejemplo.com`
- **Contraseña:** `[contraseña segura]`
- **Roles:** Selecciona:
  - `Sales / User`
  - `Inventory / User`
  - `CRM / User`
  - (IMPORTANTE: NO admin)

**Permisos específicos:**
- ✅ Leer: `product.product`, `product.template`
- ✅ Leer: `product.pricelist`
- ✅ Crear: `crm.lead`
- ✅ Crear: `sale.order`
- ✅ Leer/Escribir: `sale.order.line`

### Paso 3: Obtener API Key (si Odoo soporta)

```
Mi Perfil → Seguridad
```

Generar **API Token** (algunas versiones de Odoo)

---

## 2️⃣ Configuración en FastERP

### Paso 1: Acceder al Módulo `odoo_sync`

Desde el admin de FastERP:

```
FastERP Admin → Modules → Odoo Sync → Configuration
```

### Paso 2: Llenar Datos de Odoo

**Formulario:**

```
Odoo URL:          https://odoo.ejemplo.com
Database Name:     tu_base_datos
Username:          fasterp_api
Password:          [contraseña]
API Key:           [si Odoo lo genera]
Enable Auto-sync:  ✅ ON
Sync Interval:     30 minutos
```

### Paso 3: Probar Conexión

Botón: **"Test Connection"**

Si dice ✅ **"Connected successfully!"**, está bien. Si no, verificar:
- URL correcta (sin slash final)
- Usuario y contraseña correctos
- Permisos del usuario en Odoo

---

## 3️⃣ ¿Qué se Sincroniza?

### Datos que TRAE FastERP desde Odoo:

#### **Productos:**
```
De Odoo → FastERP
- ID del producto
- Nombre (product_name)
- Categoría (product.categ_id)
- Precio de venta (list_price)
- Costo (standard_price)
- Stock disponible (qty_available)
- SKU (default_code)
```

**Ejemplo de sincronización:**

```
Odoo: Paneles Jinko 550W
  ID: 12345
  list_price: $400
  qty_available: 150

↓ Sincroniza ↓

FastERP Products:
  {
    "odoo_product_id": 12345,
    "name": "Paneles Jinko 550W",
    "category": "panel",
    "price": 400,
    "stock": 150
  }
```

#### **Precios:**
- Se actualizan cada 30 minutos (configurable)
- Histórico en tabla `mod_products_product_price`

#### **Inventario:**
- Stock actualizado en tiempo real
- Alerta si stock < 10 unidades

### Datos que ENVÍA FastERP hacia Odoo:

#### **Leads (Oportunidades):**
```
Cuando cliente genera cotización en portal:

FastERP → Odoo
{
  "name": "María García - Cotización Solar",
  "email": "maria@example.com",
  "phone": "+521234567890",
  "address": "Calle Principal 123, Monterrey",
  "expected_revenue": 45000,
  "probability": 50,
  "description": "Cotización de 12 paneles Jinko 550W"
}

↓ Crea en Odoo ↓

CRM → Leads → Nueva Oportunidad
```

#### **Quotes (Cotizaciones):**
```
Cuando cliente acepta la cotización:

FastERP → Odoo
{
  "client_name": "María García",
  "client_email": "maria@example.com",
  "order_date": "2026-07-11",
  "order_lines": [
    {
      "product_id": 12345,
      "name": "Paneles Jinko 550W x12",
      "quantity": 12,
      "price_unit": 400
    },
    {
      "product_id": 67890,
      "name": "Inversor Fronius 5kW",
      "quantity": 1,
      "price_unit": 2500
    }
  ],
  "payment_term": "50% Advance / 50% Upon Delivery"
}

↓ Crea en Odoo ↓

Sales → Quotations → New Sale Order
```

---

## 4️⃣ Estructura de Productos en Odoo

### Para que funcione bien, organiza tus productos así:

#### **Paneles Solares:**

```
Categoría: Productos → Energía Solar → Paneles
SKU: PANEL-JINKO-550-BLK
Nombre: Paneles Jinko 550W Black Frame
Precio venta: $400
Costo: $250
Descripción:
  550W capacity
  21.5% efficiency
  25-year warranty
```

#### **Inversores:**

```
Categoría: Productos → Energía Solar → Inversores
SKU: INV-FRONIUS-5K
Nombre: Inversor Fronius Symo 5.0-3-S
Precio venta: $2,500
Costo: $1,500
Descripción:
  5 kW capacity
  3-phase
  WiFi monitoring
```

#### **Estructuras:**

```
Categoría: Productos → Energía Solar → Estructuras
SKU: STRUCT-FLAT-ROOF
Nombre: Estructura para Techo Plano (12 paneles)
Precio venta: $800
Costo: $400
```

### Categorías Recomendadas en Odoo:

```
Energía Solar/
├── Paneles
│   ├── Jinko
│   ├── Canadian Solar
│   ├── JA Solar
│   └── ...
├── Inversores
│   ├── Fronius
│   ├── SMA
│   ├── Solis
│   └── ...
├── Estructuras
│   ├── Techo Plano
│   ├── Techo Inclinado
│   ├── Lámina
│   └── ...
└── Protecciones
    ├── Breakers
    ├── Disyuntores DC
    └── ...
```

---

## 5️⃣ Condiciones de Pago (Terms) en Odoo

Para que el financiamiento funcione, crea estos términos:

| Nombre | Descripción | Condición |
|--------|-------------|----------|
| **Contado** | Pago al contado | Inmediato |
| **Crédito Bancario 36m** | 10% anticipo, 36 cuotas mensuales | 36 Net Days |
| **Financiamiento Interno** | 0% interés, 12 meses | 12 Net Days |
| **PPA 25 años** | Venta de energía | Special |

### Cómo crear en Odoo:

```
Accounting → Configuration → Payment Terms → New
```

**Ejemplo - Crédito Bancario:**

```
Name: Credit 36 months
Lines:
  - Discount: 10% (down payment)
  - Remaining: Net 1080 days (36 meses)
```

---

## 6️⃣ Webhooks (Opcional pero Recomendado)

Para actualizaciones en TIEMPO REAL:

### En Odoo:

```
Configuración → Técnico → Automation → Webhooks
```

**Crear webhook para:**

1. **Cuando cambia precio de producto:**
   ```
   Evento: sale.order_line - Update
   Trigger: product.price changed
   URL: https://fasterp.ejemplo.com/api/modules/odoo_sync/webhook/price-change
   Método: POST
   ```

2. **Cuando cambia inventario:**
   ```
   Evento: stock.move - Confirm
   Trigger: qty_available changed
   URL: https://fasterp.ejemplo.com/api/modules/odoo_sync/webhook/inventory-update
   Método: POST
   ```

3. **Cuando se crea una orden:**
   ```
   Evento: sale.order - Create
   Trigger: New SO created
   URL: https://fasterp.ejemplo.com/api/modules/odoo_sync/webhook/order-created
   Método: POST
   ```

---

## 7️⃣ Pruebas de Sincronización

### Test 1: Sincronizar Productos

En FastERP → Odoo Sync:

```
Botón: "Sync Products Now"
```

Resultado esperado:
```
✅ Synced 45 products
   - 20 panels
   - 8 inverters
   - 10 structures
   - 7 protections

Duration: 3.5 seconds
```

### Test 2: Verificar en BD Local

```bash
psql -U odoo17 -d fasterp -c "SELECT COUNT(*) FROM mod_products_product;"
```

Debería retornar: `45`

### Test 3: Generar Cotización de Prueba

En Portal Solar:
1. Llenar datos de cliente
2. Subir facturas
3. Generar cotización
4. Ver en Odoo que se creó el Lead

```
Odoo → CRM → Leads → [Nueva oportunidad]
```

---

## 8️⃣ Troubleshooting

### ❌ "Connection refused"
- Verificar que Odoo está corriendo
- Verificar URL (sin https en desarrollo)
- Verificar firewall

### ❌ "Invalid credentials"
- Verificar usuario/contraseña en Odoo
- Asegurar que usuario está activo
- Verificar permisos

### ❌ "No products synced"
- Verificar que productos existen en Odoo
- Verificar que no están archivados
- Ver logs de sincronización en FastERP

### ❌ "Quotation no se crea en Odoo"
- Verificar que producto existe en Odoo
- Verificar límite de precios
- Ver error en logs de FastERP

---

## 9️⃣ Logs de Sincronización

En FastERP → Odoo Sync → View Logs:

```
2026-07-11 14:03:45 | SUCCESS | Sync Products | 45 records | 3.5s
2026-07-11 14:33:12 | SUCCESS | Sync Prices | 45 records | 1.2s
2026-07-11 15:03:27 | ERROR | Sync Inventory | Connection timeout | 30.0s
2026-07-11 15:15:03 | SUCCESS | Create Lead | 1 record | 0.8s
```

---

## 🔟 Próximos Pasos

1. ✅ Configurar conexión a Odoo
2. ✅ Sincronizar productos iniciales
3. ✅ Crear términos de pago
4. ✅ Probar flujo completo (producto → cotización → Odoo)
5. ✅ Configurar webhooks (opcional)
6. ✅ Capacitar equipo de ventas

---

**¿Dudas? Revisar logs de sincronización o contactar soporte.** 🚀
