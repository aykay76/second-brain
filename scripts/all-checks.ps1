# Full System Check
# Runs all diagnostic checks: database, server, LLM providers

param(
    [string]$ServerUrl = "http://localhost:8080",
    [string]$OllamaUrl = "http://localhost:11434",
    [string]$DbHost = "localhost",
    [int]$DbPort = 5433
)

Write-Host "`n╔═══════════════════════════════════════════════════════════╗" -ForegroundColor Cyan
Write-Host "║          PA - Full System Diagnostic Check               ║" -ForegroundColor Cyan
Write-Host "╚═══════════════════════════════════════════════════════════╝`n" -ForegroundColor Cyan

# Check script directory
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

# Run health check
Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Cyan
Write-Host "Service Health Check" -ForegroundColor Cyan
Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Cyan
& "$scriptDir\health-check.ps1" -ServerUrl $ServerUrl -OllamaUrl $OllamaUrl -DbHost $DbHost -DbPort $DbPort

# Run database health
Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Cyan
Write-Host "Database Details" -ForegroundColor Cyan
Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Cyan
& "$scriptDir\db-health.ps1" -Host $DbHost -Port $DbPort

# Run LLM check
Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Cyan
Write-Host "LLM Providers" -ForegroundColor Cyan
Write-Host "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" -ForegroundColor Cyan
& "$scriptDir\llm-check.ps1" -OllamaUrl $OllamaUrl

Write-Host "╔═══════════════════════════════════════════════════════════╗" -ForegroundColor Cyan
Write-Host "║              Diagnostic Check Complete                   ║" -ForegroundColor Cyan
Write-Host "╚═══════════════════════════════════════════════════════════╝`n" -ForegroundColor Cyan
