#!/bin/bash

# System Health Check (Bash/Shell version)
# Tests database, server, and LLM provider connectivity

SERVER_URL="${1:-http://localhost:8080}"
OLLAMA_URL="${2:-http://localhost:11434}"
DB_HOST="${3:-localhost}"
DB_PORT="${4:-5433}"
DB_NAME="${5:-pa}"
DB_USER="${6:-pa}"
DB_PASSWORD="${7:-pa}"

test_service() {
    local name=$1
    local url=$2
    
    if curl -sf "$url" > /dev/null 2>&1; then
        echo "✅ $name is healthy"
        return 0
    else
        echo "❌ $name is down or unreachable"
        echo "   URL: $url"
        return 1
    fi
}

test_database() {
    local host=$1
    local port=$2
    local database=$3
    local user=$4
    local password=$5
    
    export PGPASSWORD="$password"
    
    if timeout 5 psql -h "$host" -p "$port" -U "$user" -d "$database" -c "SELECT 1;" > /dev/null 2>&1; then
        echo "✅ Database is healthy"
        return 0
    else
        echo "❌ Database is unreachable"
        echo "   Host: $host:$port"
        return 1
    fi
}

# Main execution
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  PA System Health Check"
echo "═══════════════════════════════════════════════════════════"
echo ""

echo "Services:"
test_database "$DB_HOST" "$DB_PORT" "$DB_NAME" "$DB_USER" "$DB_PASSWORD"
test_service "PA Server" "$SERVER_URL/health"
test_service "Ollama" "$OLLAMA_URL/api/tags"

echo ""
echo "Endpoints:"
echo "  Server:  $SERVER_URL"
echo "  Ollama:  $OLLAMA_URL"
echo "  Database: $DB_HOST:$DB_PORT/$DB_NAME"

echo ""
echo "═══════════════════════════════════════════════════════════"
echo ""
