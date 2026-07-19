#!/bin/bash

# Script para iniciar Odoo v18

echo "🐳 Levantando Odoo v18..."
docker-compose up -d

echo ""
echo "⏳ Esperando servicios (30-60 segundos)..."
sleep 30

echo ""
echo "✅ Odoo v18 está corriendo!"
echo ""
echo "🌐 Acceso: http://localhost:8073"
echo "📧 Email:  admin@example.com"
echo "🔑 Password: admin"
echo ""
echo "🗄️  PostgreSQL: localhost:5436"
echo ""
