# System Health Check
# Tests database, server, and LLM provider connectivity

param(
    [string]$ServerUrl = "http://localhost:8080",
    [string]$OllamaUrl = "http://localhost:11434",
    [string]$DbHost = "localhost",
    [int]$DbPort = 5433,
    [string]$DbName = "pa",
    [string]$DbUser = "pa",
    [string]$DbPassword = "pa"
)

function Test-Service {
    param(
        [string]$Name,
        [string]$Url,
        [int]$TimeoutSeconds = 5
    )
    
    try {
        $response = Invoke-WebRequest -Uri $Url -Method Get -TimeoutSec $TimeoutSeconds -ErrorAction Stop
        if ($response.StatusCode -eq 200) {
            Write-Host "✅ $Name is healthy" -ForegroundColor Green
            return $true
        }
    } catch {
        Write-Host "❌ $Name is down or unreachable" -ForegroundColor Red
        Write-Host "   URL: $Url" -ForegroundColor Gray
        Write-Host "   Error: $($_.Exception.Message)" -ForegroundColor Gray
    }
    return $false
}

function Test-Database {
    param(
        [string]$Host,
        [int]$Port,
        [string]$Database,
        [string]$User,
        [string]$Password
    )
    
    try {
        $psqlPath = Get-Command psql -ErrorAction SilentlyContinue
        if (-not $psqlPath) {
            Write-Host "⚠️  PostgreSQL client not installed, skipping database test" -ForegroundColor Yellow
            return $null
        }
        
        $env:PGPASSWORD = $Password
        $result = & psql -h $Host -p $Port -U $User -d $Database -c "SELECT 1;" -q 2>&1
        
        if ($LASTEXITCODE -eq 0) {
            Write-Host "✅ Database is healthy" -ForegroundColor Green
            return $true
        } else {
            Write-Host "❌ Database is unreachable" -ForegroundColor Red
            Write-Host "   Host: $Host`:$Port" -ForegroundColor Gray
            Write-Host "   Error: $result" -ForegroundColor Gray
            return $false
        }
    } catch {
        Write-Host "❌ Database test failed: $_" -ForegroundColor Red
        return $false
    }
}

# Main execution
Write-Host "`n═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  PA System Health Check" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan

Write-Host "Services:" -ForegroundColor Cyan
Test-Database -Host $DbHost -Port $DbPort -Database $DbName -User $DbUser -Password $DbPassword
Test-Service -Name "PA Server" -Url "$ServerUrl/health"
Test-Service -Name "Ollama" -Url "$OllamaUrl/api/tags"

Write-Host "`nEndpoints:" -ForegroundColor Cyan
Write-Host "  Server:  $ServerUrl" -ForegroundColor Gray
Write-Host "  Ollama:  $OllamaUrl" -ForegroundColor Gray
Write-Host "  Database: $DbHost`:$DbPort/$DbName" -ForegroundColor Gray

Write-Host "`n═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan
