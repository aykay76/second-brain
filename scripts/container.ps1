# Docker/Podman Container Management Helper
# Manage PostgreSQL container for local development

param(
    [ValidateSet('up', 'down', 'restart', 'logs', 'ps', 'shell', 'help')]
    [string]$Command = "help"
)

# Detect if podman or docker is available
$containerCmd = $null
if (Get-Command podman -ErrorAction SilentlyContinue) {
    $containerCmd = "podman"
} elseif (Get-Command docker -ErrorAction SilentlyContinue) {
    $containerCmd = "docker"
} else {
    Write-Host "❌ Neither docker nor podman found. Please install one of them." -ForegroundColor Red
    exit 1
}

Write-Host "Using: $containerCmd" -ForegroundColor Gray

# Get the workspace root (parent of scripts directory)
$workspaceRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

function Show-Help {
    Write-Host "`n═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
    Write-Host "  Container Management Helper" -ForegroundColor Cyan
    Write-Host "═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan
    
    Write-Host "Usage: ./container.ps1 -Command <command>" -ForegroundColor Green
    Write-Host ""
    Write-Host "Commands:" -ForegroundColor Cyan
    Write-Host "  up        Start PostgreSQL container and services" -ForegroundColor Gray
    Write-Host "  down      Stop and remove containers" -ForegroundColor Gray
    Write-Host "  restart   Restart the services" -ForegroundColor Gray
    Write-Host "  logs      Show container logs" -ForegroundColor Gray
    Write-Host "  ps        List running containers" -ForegroundColor Gray
    Write-Host "  shell     Open psql shell in database" -ForegroundColor Gray
    Write-Host "  help      Show this help message" -ForegroundColor Gray
    Write-Host ""
    Write-Host "Examples:" -ForegroundColor Cyan
    Write-Host "  ./container.ps1 -Command up" -ForegroundColor Green
    Write-Host "  ./container.ps1 -Command logs" -ForegroundColor Green
    Write-Host "  ./container.ps1 -Command shell" -ForegroundColor Green
    Write-Host ""
}

switch ($Command) {
    "up" {
        Write-Host "Starting services..." -ForegroundColor Cyan
        Push-Location $workspaceRoot
        & $containerCmd compose up -d
        Write-Host "Waiting for database to be ready..." -ForegroundColor Yellow
        Start-Sleep -Seconds 3
        & $containerCmd compose ps
        Pop-Location
    }
    
    "down" {
        Write-Host "Stopping services..." -ForegroundColor Cyan
        Push-Location $workspaceRoot
        & $containerCmd compose down
        Pop-Location
    }
    
    "restart" {
        Write-Host "Restarting services..." -ForegroundColor Cyan
        Push-Location $workspaceRoot
        & $containerCmd compose restart
        Pop-Location
    }
    
    "logs" {
        Write-Host "Showing logs (Ctrl+C to exit)..." -ForegroundColor Cyan
        Push-Location $workspaceRoot
        & $containerCmd compose logs -f
        Pop-Location
    }
    
    "ps" {
        Write-Host "Running containers:" -ForegroundColor Cyan
        Push-Location $workspaceRoot
        & $containerCmd compose ps
        Pop-Location
    }
    
    "shell" {
        Write-Host "Opening psql shell..." -ForegroundColor Cyan
        Write-Host "Connection details:" -ForegroundColor Gray
        Write-Host "  Host: localhost" -ForegroundColor Gray
        Write-Host "  Port: 5433" -ForegroundColor Gray
        Write-Host "  Database: pa" -ForegroundColor Gray
        Write-Host "  User: pa" -ForegroundColor Gray
        Write-Host ""
        
        $env:PGPASSWORD = "pa"
        Push-Location $workspaceRoot
        & psql -h localhost -p 5433 -U pa -d pa
        Pop-Location
    }
    
    "help" {
        Show-Help
    }
    
    default {
        Show-Help
    }
}

Write-Host ""
