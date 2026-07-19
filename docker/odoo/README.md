# 🐳 Docker Setup para Odoo + FastERP

Carpeta con toda la configuración de Docker para levantar Odoo + FastERP.

---

## 🚀 Cómo Usar

### Prerequisitos
- Docker instalado
- docker-compose v2+
- ~3GB de espacio en disco

### Iniciar Servicios

Desde esta carpeta (`docker/odoo/`):

```bash
docker-compose up -d
```

Esperar 30-60 segundos...

### Verificar que Está Corriendo

```bash
docker-compose ps
```

Deberías ver:
```
NAME                 STATUS
fasterp-postgres     Up (healthy)
odoo-postgres        Up (healthy)
odoo-server          Up (healthy)
```

---

## 📍 Acceso

### Odoo
```
URL:      http://localhost:8069
Email:    admin@example.com
Password: admin
```

### FastERP (después de levantarlo)
```
URL:      http://localhost:5173
Admin:    http://localhost:7071/admin
```

### Bases de Datos

**FastERP PostgreSQL:**
```
Host:     localhost
Port:     5432
User:     fasterp
Password: fasterp123
DB:       fasterp
```

**Odoo PostgreSQL:**
```
Host:     localhost
Port:     5433
User:     odoo
Password: odoo123
DB:       odoo
```

---

## 📝 Variables de Entorno

Están en `.env`:
- Cambiar puertos si es necesario
- Cambiar contraseñas (recomendado para producción)
- Cambiar COMPOSE_PROJECT_NAME si necesitas múltiples instancias

---

## 🛑 Parar Servicios

```bash
docker-compose down
```

Para eliminar datos:
```bash
docker-compose down -v
```

---

## 📊 Ver Logs

Todos:
```bash
docker-compose logs -f
```

Solo Odoo:
```bash
docker-compose logs -f odoo
```

Solo PostgreSQL FastERP:
```bash
docker-compose logs -f fasterp-db
```

---

## 🔧 Troubleshooting

### Puertos ya en uso
```bash
# Ver qué usa el puerto
lsof -i :8069

# Cambiar puerto en docker-compose.yml:
# Cambiar "8069:8069" a "8070:8069"
```

### Servicio no inicia
```bash
# Ver error
docker-compose logs odoo

# Reintentar
docker-compose restart odoo
```

### Limpiar y reiniciar
```bash
docker-compose down -v
docker-compose up -d
```

---

## 📚 Siguiente Paso

Una vez que Odoo esté corriendo:

1. Leer: `/home/sucode/Documents/ecuabyte/fasterp/ODOO_CONFIG_STEP_BY_STEP.md`
2. Crear usuario API en Odoo
3. Agregar productos solares
4. Conectar FastERP

---

**¡Listo!** 🚀
