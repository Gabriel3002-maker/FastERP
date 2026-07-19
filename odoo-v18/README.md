# 🐳 Odoo v18 Docker Setup

Configuración completa para levantar Odoo v18 con PostgreSQL.

---

## 📊 Puertos

- **Odoo Web:** `http://localhost:8073`
- **PostgreSQL:** `localhost:5436`

---

## 🚀 Inicio Rápido

```bash
cd odoo-v18
docker-compose up -d
```

Esperar 30-60 segundos...

---

## 📝 Acceso

### Odoo Web
```
URL:      http://localhost:8073
Email:    admin@example.com
Password: admin
```

### PostgreSQL
```
Host:     localhost
Port:     5436
User:     odoo
Password: odoo123
DB:       odoo18
```

---

## 📁 Estructura

```
odoo-v18/
├── docker-compose.yml    (Configuración Docker)
├── config/
│   └── odoo.conf        (Configuración Odoo v18)
└── README.md
```

---

## 🛑 Parar

```bash
docker-compose down
```

Para eliminar datos:
```bash
docker-compose down -v
```

---

## 📊 Ver Logs

```bash
docker-compose logs -f
```

---

**Listo para usar!** 🚀
