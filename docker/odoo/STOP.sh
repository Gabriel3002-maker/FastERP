#!/bin/bash

# Script para parar Docker compose
# Uso: ./STOP.sh

echo "🛑 Parando servicios Docker..."
docker-compose down

echo ""
echo "✅ Servicios detenidos"
echo ""
echo "Para eliminar datos: docker-compose down -v"
