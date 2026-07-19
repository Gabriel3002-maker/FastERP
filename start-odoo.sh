#!/bin/bash

# Script para iniciar Odoo con Docker
# Uso: ./start-odoo.sh

set -e

echo "╔════════════════════════════════════════════════════════════╗"
echo "║          Starting Odoo for FastERP Integration             ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""

# Check if Docker is installed
if ! command -v docker &> /dev/null; then
    echo "❌ Docker is not installed. Please install Docker first."
    echo "   Download: https://www.docker.com/products/docker-desktop"
    exit 1
fi

# Check if docker-compose is available
if ! command -v docker-compose &> /dev/null; then
    echo "❌ docker-compose is not installed."
    exit 1
fi

echo "📦 Starting Odoo + PostgreSQL..."
echo ""

# Start Odoo
docker-compose -f docker-compose.odoo.yml up -d

echo ""
echo "⏳ Waiting for services to be ready..."
sleep 10

# Check if Odoo is running
if docker ps | grep -q odoo-server; then
    echo ""
    echo "✅ Odoo is running!"
    echo ""
    echo "╔════════════════════════════════════════════════════════════╗"
    echo "║                   🎉 ODOO READY                            ║"
    echo "╠════════════════════════════════════════════════════════════╣"
    echo "║                                                            ║"
    echo "║  🌐 Web Interface:    http://localhost:8069               ║"
    echo "║  📊 Initial Login:                                         ║"
    echo "║     Email:    admin@example.com                           ║"
    echo "║     Password: admin                                        ║"
    echo "║                                                            ║"
    echo "║  🔌 Database:         odoo (PostgreSQL)                   ║"
    echo "║     Connection:       localhost:5433                      ║"
    echo "║     User:             odoo                                ║"
    echo "║     Password:         odoo123                             ║"
    echo "║                                                            ║"
    echo "║  📝 Next Steps:                                            ║"
    echo "║     1. Go to http://localhost:8069                        ║"
    echo "║     2. Login with admin/admin                             ║"
    echo "║     3. Follow ODOO_SETUP.md for FastERP integration       ║"
    echo "║                                                            ║"
    echo "╚════════════════════════════════════════════════════════════╝"
    echo ""
    echo "📋 Useful Commands:"
    echo "   docker-compose -f docker-compose.odoo.yml logs -f      # View logs"
    echo "   docker-compose -f docker-compose.odoo.yml stop         # Stop services"
    echo "   docker-compose -f docker-compose.odoo.yml down         # Stop & remove"
    echo ""
else
    echo ""
    echo "❌ Odoo failed to start. Check logs:"
    docker-compose -f docker-compose.odoo.yml logs
    exit 1
fi
