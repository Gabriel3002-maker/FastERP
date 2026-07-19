#!/bin/bash

# FastERP Core - Database Setup Script

echo "🔧 FastERP Core Database Setup"
echo "================================"
echo ""

# Verificar si PostgreSQL está corriendo
if ! pg_isready -h localhost -p 5432 > /dev/null 2>&1; then
    echo "❌ PostgreSQL no está corriendo en localhost:5432"
    echo "Inicia PostgreSQL primero:"
    echo "  sudo service postgresql start"
    exit 1
fi

echo "✓ PostgreSQL is running"
echo ""

# Crear base de datos
echo "📦 Creating database 'fasterp'..."
psql -h localhost -U postgres -c "CREATE DATABASE fasterp;" 2>/dev/null || echo "  (database already exists)"

echo "✓ Database ready"
echo ""
echo "================================"
echo "✅ Database setup complete!"
echo ""
echo "Run the app:"
echo "  make run"
echo ""
echo "Login with:"
echo "  User: admin"
echo "  Pass: admin123"
