# Database Query Helper
# Prints useful SQL queries for inspecting ingestion status and database contents

param(
    [string]$Host = "localhost",
    [int]$Port = 5433,
    [string]$Database = "pa",
    [string]$User = "pa",
    [string]$Password = "pa",
    [string]$Query = "schema",
    [switch]$Execute = $false
)

$queries = @{
    "schema" = @"
-- Show all tables and their row counts
SELECT 
    schemaname,
    tablename,
    pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) as size
FROM pg_tables 
WHERE schemaname = 'public' 
ORDER BY tablename;
"@
    
    "tables" = @"
-- List all columns in public tables
SELECT 
    table_name,
    column_name,
    data_type,
    is_nullable
FROM information_schema.columns
WHERE table_schema = 'public'
ORDER BY table_name, ordinal_position;
"@
    
    "migrations" = @"
-- Show migration history
SELECT version, dirty, installed_on 
FROM schema_migrations 
ORDER BY version DESC;
"@
    
    "indexes" = @"
-- Show all indexes
SELECT schemaname, tablename, indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public'
ORDER BY tablename, indexname;
"@
    
    "stats" = @"
-- Database size and row counts
SELECT 
    schemaname,
    COUNT(*) as num_tables,
    pg_size_pretty(SUM(pg_total_relation_size(schemaname||'.'||tablename))) as total_size,
    SUM(n_live_tup) as total_rows
FROM pg_stat_user_tables
GROUP BY schemaname;
"@
    
    "connections" = @"
-- Active connections
SELECT 
    datname,
    usename,
    application_name,
    client_addr,
    state,
    query,
    query_start
FROM pg_stat_activity
WHERE datname IS NOT NULL
ORDER BY query_start DESC;
"@
    
    "performance" = @"
-- Table and index performance insights
SELECT 
    schemaname,
    tablename,
    seq_scan,
    seq_tup_read,
    idx_scan,
    idx_tup_fetch,
    n_tup_ins,
    n_tup_upd,
    n_tup_del,
    n_live_tup
FROM pg_stat_user_tables
ORDER BY seq_scan DESC;
"@
}

function Show-AvailableQueries {
    Write-Host "Available queries:" -ForegroundColor Cyan
    Write-Host ""
    $queries.Keys | Sort-Object | ForEach-Object {
        Write-Host "  $_" -ForegroundColor Green
    }
    Write-Host ""
    Write-Host "Usage:" -ForegroundColor Cyan
    Write-Host '  ./db-query.ps1 -Query "schema"' -ForegroundColor Gray
    Write-Host '  ./db-query.ps1 -Query "stats" -Execute' -ForegroundColor Gray
}

function Run-Query {
    param(
        [string]$Query,
        [string]$Host,
        [int]$Port,
        [string]$Database,
        [string]$User,
        [string]$Password
    )
    
    try {
        $psqlPath = Get-Command psql -ErrorAction SilentlyContinue
        if (-not $psqlPath) {
            Write-Host "❌ psql not found. Install PostgreSQL client tools." -ForegroundColor Red
            return
        }
        
        $env:PGPASSWORD = $Password
        
        Write-Host "Executing query on $Host`:$Port/$Database..." -ForegroundColor Cyan
        Write-Host ""
        
        $result = $Query | & psql -h $Host -p $Port -U $User -d $Database
        
        if ($LASTEXITCODE -eq 0) {
            Write-Host $result -ForegroundColor Gray
        } else {
            Write-Host "❌ Query failed" -ForegroundColor Red
            Write-Host $result -ForegroundColor Gray
        }
    } catch {
        Write-Host "❌ Error: $_" -ForegroundColor Red
    }
}

# Main
Write-Host "`n═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  PA Database Query Helper" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan

if ($Query -eq "help" -or $Query -eq "list") {
    Show-AvailableQueries
} elseif ($queries.ContainsKey($Query)) {
    $sql = $queries[$Query]
    
    if ($Execute) {
        Run-Query -Query $sql -Host $Host -Port $Port -Database $Database -User $User -Password $Password
    } else {
        Write-Host "Query: $Query" -ForegroundColor Cyan
        Write-Host ""
        Write-Host $sql -ForegroundColor Gray
        Write-Host ""
        Write-Host "Use -Execute flag to run this query:" -ForegroundColor Yellow
        Write-Host "  ./db-query.ps1 -Query ""$Query"" -Execute" -ForegroundColor Green
    }
} else {
    Write-Host "Unknown query: $Query" -ForegroundColor Red
    Write-Host ""
    Show-AvailableQueries
}

Write-Host ""
