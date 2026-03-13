# Database Health Check
# Verifies PostgreSQL connection and checks critical system status

param(
    [string]$Host = "localhost",
    [int]$Port = 5433,
    [string]$Database = "pa",
    [string]$User = "pa",
    [string]$Password = "pa"
)

function Test-PostgresConnection {
    param(
        [string]$Host,
        [int]$Port,
        [string]$Database,
        [string]$User,
        [string]$Password
    )
    
    $connString = "Server=$Host;Port=$Port;Database=$Database;User Id=$User;Password=$Password;"
    
    try {
        $connection = New-Object System.Data.SqlClient.SqlConnection
        # For PostgreSQL, we need to use psql or a different approach
        # Let's use a simple test via ping first
        
        Write-Host "🔍 Checking PostgreSQL on $Host`:$Port..." -ForegroundColor Cyan
        
        # Try to use psql if available
        $psqlPath = Get-Command psql -ErrorAction SilentlyContinue
        if ($psqlPath) {
            $env:PGPASSWORD = $Password
            $result = & psql -h $Host -p $Port -U $User -d $Database -c "SELECT version();" 2>&1
            if ($LASTEXITCODE -eq 0) {
                Write-Host "✅ Database connection successful" -ForegroundColor Green
                Write-Host "   $($result[0])" -ForegroundColor Gray
                return $true
            } else {
                Write-Host "❌ Failed to connect: $result" -ForegroundColor Red
                return $false
            }
        } else {
            Write-Host "⚠️  psql not found. Install PostgreSQL client tools for full diagnostics." -ForegroundColor Yellow
            Write-Host "   https://www.postgresql.org/download/" -ForegroundColor Gray
            return $null
        }
    } catch {
        Write-Host "❌ Error: $_" -ForegroundColor Red
        return $false
    }
}

function Get-DatabaseStats {
    param(
        [string]$Host,
        [int]$Port,
        [string]$Database,
        [string]$User,
        [string]$Password
    )
    
    Write-Host "`n📊 Database Statistics" -ForegroundColor Cyan
    
    try {
        $env:PGPASSWORD = $Password
        
        # Check migrations table
        Write-Host "`n  Migrations:" -ForegroundColor Gray
        $migrations = & psql -h $Host -p $Port -U $User -d $Database -t -c `
            "SELECT version, dirty FROM schema_migrations ORDER BY version DESC;" 2>&1
        if ($LASTEXITCODE -eq 0) {
            if ($migrations) {
                $migrations | ForEach-Object { Write-Host "    $_" -ForegroundColor Gray }
            } else {
                Write-Host "    (no migrations)" -ForegroundColor Gray
            }
        }
        
        # Check table sizes
        Write-Host "`n  Table Sizes:" -ForegroundColor Gray
        $tables = & psql -h $Host -p $Port -U $User -d $Database -t -c `
            "SELECT tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) as size 
             FROM pg_tables WHERE schemaname='public' ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;" 2>&1
        if ($LASTEXITCODE -eq 0 -and $tables) {
            $tables | ForEach-Object { Write-Host "    $_" -ForegroundColor Gray }
        } else {
            Write-Host "    (no tables or error)" -ForegroundColor Gray
        }
        
        # Check key metrics
        Write-Host "`n  Connection Info:" -ForegroundColor Gray
        $connInfo = & psql -h $Host -p $Port -U $User -d $Database -t -c `
            "SELECT 'Active connections: ' || count(*) FROM pg_stat_activity WHERE datname = current_database();" 2>&1
        if ($LASTEXITCODE -eq 0) {
            $connInfo | ForEach-Object { Write-Host "    $_" -ForegroundColor Gray }
        }
        
    } catch {
        Write-Host "❌ Error fetching statistics: $_" -ForegroundColor Red
    }
}

function Get-IngestionStatus {
    param(
        [string]$Host,
        [int]$Port,
        [string]$Database,
        [string]$User,
        [string]$Password
    )
    
    Write-Host "`n📥 Ingestion Status" -ForegroundColor Cyan
    
    try {
        $env:PGPASSWORD = $Password
        
        # List all tables to understand schema
        $tables = & psql -h $Host -p $Port -U $User -d $Database -t -c `
            "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename;" 2>&1
        
        if ($LASTEXITCODE -eq 0 -and $tables) {
            Write-Host "  Available tables:" -ForegroundColor Gray
            $tables | Where-Object { $_ -match '\w+' } | ForEach-Object { 
                Write-Host "    - $_" -ForegroundColor Gray
            }
        }
        
    } catch {
        Write-Host "❌ Error: $_" -ForegroundColor Red
    }
}

# Main execution
Write-Host "`n═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  PA Database Health Check" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan

$connected = Test-PostgresConnection -Host $Host -Port $Port -Database $Database -User $User -Password $Password

if ($connected -ne $false) {
    Get-DatabaseStats -Host $Host -Port $Port -Database $Database -User $User -Password $Password
    Get-IngestionStatus -Host $Host -Port $Port -Database $Database -User $User -Password $Password
}

Write-Host "`n═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan
