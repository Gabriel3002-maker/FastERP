# 🔧 Configuración Odoo Step-by-Step para FastERP

**Objetivo:** Tener Odoo listo con productos solares sincronizados en FastERP.

**Tiempo estimado:** 30-45 minutos

---

## PASO 1: Iniciar Odoo

### Opción A: Con Docker (Recomendado - 5 minutos)

```bash
cd /home/sucode/Documents/ecuabyte/fasterp
chmod +x start-odoo.sh
./start-odoo.sh
```

**Resultado esperado:**
```
✅ Odoo is running!
🌐 Web Interface: http://localhost:8069
Email: admin@example.com
Password: admin
```

### Opción B: Odoo Existente

Si ya tienes Odoo:
- Usa la URL actual (ej: `https://tu-odoo.com`)
- Sigue directamente al PASO 2

---

## PASO 2: Crear Usuario API en Odoo

Accede a Odoo:

```
1. Ir a: http://localhost:8069
2. Email: admin@example.com
3. Contraseña: admin
```

### Crear Usuario para FastERP

```
Configuración → Usuarios → Crear
```

**Formulario:**

```
Login:          fasterp_api
Email:          fasterp@tu-empresa.com
Nombre:         FastERP API User
Contraseña:     [generar contraseña segura]
                ej: FastERP2024@Sync!
```

**Guardar y tomar nota de la contraseña.**

### Asignar Roles Mínimos Necesarios

En el usuario recién creado:

```
Pestaña: "Roles"
Selecciona:
  ✅ Sales / User
  ✅ Inventory / User  
  ✅ CRM / User
  
NO selecciones: Admin (reduce riesgo de seguridad)
```

**Guardar cambios.**

---

## PASO 3: Crear Productos Solares en Odoo

Vamos a agregar el catálogo de productos.

### Crear Categoría

```
Inventario → Configuración → Categorías de Productos → Crear
```

**Categoría 1: Paneles**

```
Nombre:        Paneles Solares
Nombre Padre:  [dejar vacío]
Código:        PANEL
Guardar
```

**Categoría 2: Inversores**

```
Nombre:        Inversores
Código:        INV
Guardar
```

**Categoría 3: Estructuras**

```
Nombre:        Estructuras de Soporte
Código:        STRUCT
Guardar
```

### Crear Productos

#### Producto 1: Panel Jinko 550W

```
Inventario → Productos → Crear

Nombre del Producto:    Paneles Jinko 550W Black Frame
Código (SKU):           PANEL-JINKO-550-BLK
Tipo:                   Producto comercializable
Categoría:              Paneles Solares

Pestaña "Información General":
  Descripción:          
    Especificaciones:
    - Potencia: 550W
    - Eficiencia: 21.5%
    - Garantía: 25 años en producción
    - Dimensiones: 2.172 x 1.303 x 35mm
    
Pestaña "Precios":
  Precio de venta (lista):    $400
  Costo:                      $250
  
Pestaña "Inventario":
  Cantidad en existencia:     50
  
Guardar
```

#### Producto 2: Panel Canadian Solar 545W

```
Nombre del Producto:    Panel Canadian Solar 545W Bifacial
Código (SKU):           PANEL-CAN-545-BIF
Tipo:                   Producto comercializable
Categoría:              Paneles Solares

Descripción:
  - Potencia: 545W
  - Bifacial (produce por ambos lados)
  - Garantía: 25 años
  
Precio de venta:        $420
Costo:                  $270
Cantidad:               30

Guardar
```

#### Producto 3: Inversor Fronius Symo 5kW

```
Nombre del Producto:    Inversor Fronius Symo 5.0-3-S
Código (SKU):           INV-FRONIUS-5K
Tipo:                   Producto comercializable
Categoría:              Inversores

Descripción:
  - Potencia: 5 kW
  - Trifásico
  - WiFi + Monitoreo online
  - Garantía: 5 años
  
Precio de venta:        $2,500
Costo:                  $1,500
Cantidad:               15

Guardar
```

#### Producto 4: Inversor SMA 6kW

```
Nombre del Producto:    Inversor SMA Sunny Tripower 6.0 kW
Código (SKU):           INV-SMA-6K
Categoría:              Inversores

Precio de venta:        $2,800
Costo:                  $1,700
Cantidad:               10

Guardar
```

#### Producto 5: Estructura Techo Plano (12 paneles)

```
Nombre del Producto:    Estructura Soporte Techo Plano - 12 Paneles
Código (SKU):           STRUCT-FLAT-12
Categoría:              Estructuras de Soporte

Descripción:
  - Para techos planos
  - Incluye: Rails, abrazaderas, pernos
  - Ajustable: 0-45 grados de inclinación
  - Material: Aluminio anodizado
  
Precio de venta:        $800
Costo:                  $450
Cantidad:               25

Guardar
```

#### Producto 6: Protección - Breaker DC 40A

```
Nombre del Producto:    Breaker DC 40A Daytona
Código (SKU):           PROTECT-BREAK-40A
Categoría:              Estructuras de Soporte  [O crear Protecciones]

Precio de venta:        $150
Costo:                  $80
Cantidad:               100

Guardar
```

### Verificar Productos

```
Inventario → Productos
```

Deberías ver 6 productos listados. ✅

---

## PASO 4: Crear Listas de Precios (Opcional pero Recomendado)

Para poder tener diferentes precios por tipo de cliente:

```
Ventas → Configuración → Listas de Precios → Crear
```

**Lista 1: Precio Base (Contado)**

```
Nombre:         Base Prices (Cash)
Tipo:           Basada en otra lista de precios
Guardar
```

**Lista 2: Precio Financiamiento (con margen)**

```
Nombre:         Financing Prices (5% markup)
Tipo:           Basada en lista de precios
Base:           "Base Prices (Cash)"
```

Para agregar markup:
- Abrir cada línea de producto
- Agregar 5% al precio

---

## PASO 5: Términos de Pago

Los términos de pago en Odoo mapean a las opciones de financiamiento en FastERP.

```
Contabilidad → Configuración → Términos de Pago → Crear
```

### Término 1: Contado Inmediato

```
Nombre:             Payment on Delivery
Descripción:        Pago completo al momento de entrega
Línea 1:
  Type:             Porcentaje
  Porcentaje:       100%
  Días:             0
  Final:            ✅ (marcar)
Guardar
```

### Término 2: Crédito Bancario 36 meses

```
Nombre:             Bank Credit 36 Months
Descripción:        10% advance, 36 monthly payments
Línea 1:
  Type:             Porcentaje
  Porcentaje:       10%
  Días:             0
  Final:            (sin marcar)
Línea 2:
  Type:             Porcentaje
  Porcentaje:       90%
  Días:             1080  (36 meses × 30 días)
  Final:            ✅ (marcar)
Guardar
```

### Término 3: Financiamiento Interno 12 meses

```
Nombre:             Internal Finance 12 Months
Descripción:        0% interest, 12 monthly payments
Línea 1:
  Type:             Porcentaje
  Porcentaje:       100%
  Días:             360  (12 meses × 30 días)
  Final:            ✅ (marcar)
Guardar
```

---

## PASO 6: Habilitar REST API

```
Configuración → Técnico → Parámetros del Sistema
```

Buscar o crear:

| Parámetro | Valor |
|-----------|-------|
| `web.base.url` | `http://localhost:8069` (para dev) |
| `ir.config_parameter/rest_api/enabled` | `True` |

**Guardar.**

---

## PASO 7: Configurar FastERP para Conectar a Odoo

Regresa a FastERP:

```
Admin → Modules → Odoo Sync → Configuration
```

**Llenar el formulario:**

```
Odoo URL:           http://localhost:8069
Database Name:      odoo
Username:           fasterp_api
Password:           [la que creaste en Paso 2]
API Key:            [dejar vacío si Odoo no soporta]
Enable Auto-sync:   ✅ ON
Sync Interval:      30 (minutos)
```

**Guardar.**

---

## PASO 8: Probar Conexión

En FastERP → Odoo Sync Configuration:

**Botón: "Test Connection"**

Esperar...

### ✅ Si Dice: "Connected Successfully!"

¡Excelente! La conexión funciona.

### ❌ Si Da Error

Verificar:
- URL es correcta (sin trailing slash)
- Usuario y contraseña correctos en Odoo
- Usuario tiene permisos mínimos
- Odoo está corriendo

Revisar logs:
```bash
docker-compose -f docker-compose.odoo.yml logs odoo
```

---

## PASO 9: Primera Sincronización

En FastERP → Odoo Sync:

**Botón: "Sync Products Now"**

Esperar 10-15 segundos...

### ✅ Resultado Esperado

```
Status:         ✅ SUCCESS
Records Synced: 6 products
  - 2 panels
  - 2 inverters
  - 1 structure
  - 1 protection
Duration:       3.5 seconds
```

### Verificar en la BD

```bash
psql -U odoo17 -d fasterp -c "SELECT COUNT(*) FROM mod_products_product;"
```

Debería retornar: `6`

---

## PASO 10: Verificar en Web Portal

Accede al portal solar:

```
http://localhost:5173/portal
```

(O la ruta que tengas configurada)

**Deberías ver:**
- ✅ Lista de productos de Odoo
- ✅ Precios sincronizados
- ✅ Stock disponible
- ✅ Fotos/descripciones

---

## 🧪 PRUEBA FINAL: Generar Cotización

1. En el portal, llenar formulario de cliente
2. Seleccionar productos (ej: 12 paneles Jinko + 1 Fronius 5kW)
3. Generar cotización
4. Ir a Odoo CRM

```
Odoo → CRM → Leads
```

Deberías ver una nueva oportunidad creada con:
- Nombre del cliente
- Productos seleccionados
- Monto de la cotización

---

## ✅ Checklist de Configuración

- [ ] Odoo corriendo (http://localhost:8069)
- [ ] Usuario `fasterp_api` creado
- [ ] 6 productos solares agregados a Odoo
- [ ] 3 términos de pago configurados
- [ ] REST API habilitada
- [ ] FastERP conectado a Odoo
- [ ] Prueba de conexión ✅ SUCCESS
- [ ] Primer sync completado (6 productos)
- [ ] Productos visibles en portal web
- [ ] Lead creado en Odoo desde portal

---

## 📞 Troubleshooting

### "Connection refused"
```bash
# Verificar que Odoo está corriendo
docker ps | grep odoo

# Si no está, iniciar
./start-odoo.sh
```

### "Invalid credentials"
```
Verificar en Odoo:
Configuración → Usuarios → fasterp_api
  - ¿Usuario activo?
  - ¿Contraseña correcta?
  - ¿Tiene roles mínimos?
```

### "No products synced"
```
Verificar en Odoo:
Inventario → Productos
  - ¿Existen los 6 productos?
  - ¿No están archivados?
  - ¿Tienen precios?
```

### "Lead no se crea en Odoo"
```
FastERP:
Modules → odoo_sync → View Logs

Buscar último error y revisar mensajes
```

---

## 🎓 Próximos Pasos

Una vez que TODO esté funcionando:

1. **Agregar más productos** a Odoo
2. **Configurar webhooks** para sync en tiempo real
3. **Crear clientes de prueba** en Odoo
4. **Probar todo el flujo** de cotización a orden

---

**¡Listo para sincronizar! 🚀**

¿Necesitas ayuda en algún paso? Pregunta.
