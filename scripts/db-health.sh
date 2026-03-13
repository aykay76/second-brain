#!/bin/bash

# Database Health Check (Bash/Shell version)
# Verifies PostgreSQL connection and checks critical system status

HOST="${1:-localhost}"
PORT="${2:-5433}"
DATABASE="${3:-pa}"
USER="${4:-pa}"
PASSWORD="${5:-pa}"

test_postgres_connection() {
    local host=$1
    local port=$2
    local database=$3
    local user=$4
    local password=$5
    
    echo "🔍 Checking PostgreSQL on $host:$port..."
    
    export PGPASSWORD="$password"
    
    if ! command -v psql &> /dev/null; then
        echo "⚠️  psql not found. Install PostgreSQL client tools for full diagnostics."
        echo "   https://www.postgresql.org/download/"
        return 1
    fi
    
    version=$(psql -h "$host" -p "$port" -U "$user" -d "$database" -c "SELECT version();" 2>&1)
    
    if [ $? -eq 0 ]; then
        echo "✅ Database connection successful"
        echo "   $version"
        return 0
    else
        echo "❌ Failed to connect: $version"
        return 1
    fi
}

get_database_stats() {
    local host=$1
    local port=$2
    local database=$3
    local user=$4
    local password=$5
    
    echo ""
    echo "📊 Database Statistics"
    
    export PGPASSWORD="$password"
    
    # Check migrations table
    echo ""
    echo "  Migrations:"
    psql -h "$host" -p "$port" -U "$user" -d "$database" -t -c \
        "SELECT version, dirty FROM schema_migrations ORDER BY version DESC;" 2>&1 | \
        while read line; do
            [ -n "$line" ] && echo "    $line"
        done
    
    # Check table sizes
    echo ""
    echo "  Table Sizes:"
    psql -h "$host" -p "$port" -U "$user" -d "$database" -t -c \
        "SELECT tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) as size
         FROM pg_tables WHERE schemaname='public' ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;" 2>&1 | \
        while read line; do
            [ -n "$line" ] && echo "    $line"
        done
    
    # Check connection info
    echo ""
    echo "  Connection Info:"
    psql -h "$host" -p "$port" -U "$user" -d "$database" -t -c \
        "SELECT 'Active connections: ' || count(*) FROM pg_stat_activity WHERE datname = current_database();" 2>&1 | \
        while read line; do
            [ -n "$line" ] && echo "    $line"
        done
}

get_ingestion_status() {
    local host=$1
    local port=$2
    local database=$3
    local user=$4
    local password=$5
    
    echo ""
    echo "📥 Ingestion Status"
    
    export PGPASSWORD="$password"
    
    # List all tables to understand schema
    echo "  Available tables:"
    psql -h "$host" -p "$port" -U "$user" -d "$database" -t -c \
        "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename;" 2>&1 | \
        grep -v '^[[:space:]]*$' | \
        while read line; do
            [ -n "$line" ] && echo "    - $line"
        done
}

# Main execution
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  PA Database Health Check"
echo "═══════════════════════════════════════════════════════════"
echo ""

if test_postgres_connection "$HOST" "$PORT" "$DATABASE" "$USER" "$PASSWORD"; then
    get_database_stats "$HOST" "$PORT" "$DATABASE" "$USER" "$PASSWORD"
    get_ingestion_status "$HOST" "$PORT" "$DATABASE" "$USER" "$PASSWORD"
fi

echo ""
echo "═══════════════════════════════════════════════════════════"
echo ""
