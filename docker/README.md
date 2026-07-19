# 🐳 Docker Configuration for Solar ERP

Esta carpeta contiene toda la configuración de Docker para levantar Odoo + FastERP.

---

## 📁 Estructura

```
docker/
└── odoo/                      ← Aquí está todo lo que necesitas
    ├── docker-compose.yml     (Configuración de servicios)
    ├── .env                   (Variables de entorno)
    ├── START.sh              (Script para iniciar)
    ├── STOP.sh               (Script para parar)
    └── README.md             (Instrucciones)
```

---

## 🚀 Inicio Rápido

```bash
cd docker/odoo
./START.sh
```

---

## 📊 Servicios que Levanta

1. **fasterp-postgres** - Base de datos FastERP
   - Puerto: 5432
   - Usuario: fasterp
   - Contraseña: fasterp123

2. **odoo-postgres** - Base de datos Odoo
   - Puerto: 5433
   - Usuario: odoo
   - Contraseña: odoo123

3. **odoo-server** - Servidor Odoo
   - URL: http://localhost:8069
   - Usuario: admin@example.com
   - Contraseña: admin

---

## ✨ Características

- ✅ Toda la configuración en un archivo (`docker-compose.yml`)
- ✅ Variables de entorno en `.env`
- ✅ Scripts de inicio/parada automáticos
- ✅ Redes Docker configuradas
- ✅ Health checks automáticos
- ✅ Volúmenes persistentes
- ✅ Puertos mapeados

---

## 🔧 Comandos

**Ver logs en vivo:**
```bash
cd docker/odoo
docker-compose logs -f
```

**Parar servicios:**
```bash
./STOP.sh
```

**Parar y eliminar datos:**
```bash
cd docker/odoo
docker-compose down -v
```

---

## 📖 Próximo Paso

Después de iniciar con `./START.sh`:

1. Ir a http://localhost:8069
2. Leer: `/ODOO_CONFIG_STEP_BY_STEP.md` en raíz de FastERP
3. Seguir paso a paso para:
   - Crear usuario API
   - Agregar productos solares
   - Conectar FastERP

---

**¡Listo para levantar tu stack!** 🚀
