#!/bin/bash

# Script para iniciar Docker compose
# Uso: ./START.sh

set -e

echo "╔════════════════════════════════════════════════════════════╗"
echo "║     🐳 Iniciando Odoo + FastERP con Docker...             ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""

# Verificar Docker
if ! command -v docker &> /dev/null; then
    echo "❌ Docker no está instalado"
    exit 1
fi

if ! command -v docker-compose &> /dev/null; then
    echo "❌ docker-compose no está instalado"
    exit 1
fi

echo "📦 Levantando servicios..."
docker-compose up -d

echo ""
echo "⏳ Esperando que los servicios estén listos (30-60 segundos)..."
sleep 30

# Verificar estado
echo ""
echo "🔍 Verificando estado..."
docker-compose ps

echo ""
echo "╔════════════════════════════════════════════════════════════╗"
echo "║          ✅ Odoo y FastERP DB están corriendo             ║"
echo "╠════════════════════════════════════════════════════════════╣"
echo "║                                                            ║"
echo "║  🌐 Odoo:        http://localhost:8069                    ║"
echo "║     Admin:       admin@example.com / admin                ║"
echo "║                                                            ║"
echo "║  🗄️  FastERP DB:  localhost:5432                          ║"
echo "║     User:        fasterp / fasterp123                     ║"
echo "║                                                            ║"
echo "║  🗄️  Odoo DB:     localhost:5433                          ║"
echo "║     User:        odoo / odoo123                           ║"
echo "║                                                            ║"
echo "║  📖 Próximo paso:                                          ║"
echo "║     Leer: ODOO_CONFIG_STEP_BY_STEP.md                     ║"
echo "║     en la carpeta raíz de FastERP                         ║"
echo "║                                                            ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""
echo "💡 Comandos útiles:"
echo "   docker-compose logs -f         # Ver logs en vivo"
echo "   docker-compose ps              # Estado de servicios"
echo "   docker-compose down            # Parar servicios"
echo "   docker-compose down -v         # Parar y eliminar datos"
echo ""
