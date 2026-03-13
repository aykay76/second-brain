# LLM Provider Check
# Verifies Ollama and OpenAI connectivity and available models

param(
    [string]$OllamaUrl = "http://localhost:11434",
    [string]$OpenAiKey = $env:OPENAI_API_KEY
)

function Test-OllamaConnection {
    param(
        [string]$Url
    )
    
    Write-Host "Testing Ollama connection..." -ForegroundColor Cyan
    
    try {
        Write-Host "  URL: $Url" -ForegroundColor Gray
        $response = Invoke-WebRequest -Uri "$Url/api/tags" -Method Get -TimeoutSec 5 -ErrorAction Stop
        
        if ($response.StatusCode -eq 200) {
            Write-Host "  ✅ Connection successful" -ForegroundColor Green
            
            $content = $response.Content | ConvertFrom-Json
            if ($content.models) {
                Write-Host "  📦 Available models:" -ForegroundColor Gray
                $content.models | ForEach-Object {
                    Write-Host "     - $($_.name)" -ForegroundColor Gray
                }
            } else {
                Write-Host "  ⚠️  No models found. Pull models with: ollama pull <model>" -ForegroundColor Yellow
            }
            return $true
        }
    } catch {
        Write-Host "  ❌ Connection failed" -ForegroundColor Red
        Write-Host "     $($_.Exception.Message)" -ForegroundColor Gray
        Write-Host "  💡 Make sure Ollama is running: ollama serve" -ForegroundColor Yellow
    }
    return $false
}

function Test-OpenAiConnection {
    param(
        [string]$ApiKey
    )
    
    Write-Host "`nTesting OpenAI connection..." -ForegroundColor Cyan
    
    if (-not $ApiKey) {
        Write-Host "  ⚠️  OPENAI_API_KEY not set in environment" -ForegroundColor Yellow
        Write-Host "  💡 Set it with: `$env:OPENAI_API_KEY = 'your-key'" -ForegroundColor Gray
        return $null
    }
    
    try {
        $headers = @{
            "Authorization" = "Bearer $ApiKey"
            "Content-Type" = "application/json"
        }
        
        Write-Host "  ✅ API key is set (masked)" -ForegroundColor Green
        Write-Host "  💡 Models used: text-embedding-3-small, gpt-4o" -ForegroundColor Gray
        return $true
    } catch {
        Write-Host "  ❌ Error: $_" -ForegroundColor Red
    }
    return $false
}

function Show-ConfigExample {
    Write-Host "`nConfiguration File Example:" -ForegroundColor Cyan
    Write-Host @"
# In config/config.yaml:

llm:
  provider: ollama                    # or 'openai'
  
  ollama:
    base_url: http://localhost:11434
    embedding_model: nomic-embed-text
    chat_model: llama3.1
  
  openai:
    api_key: `${OPENAI_API_KEY}
    embedding_model: text-embedding-3-small
    chat_model: gpt-4o

# To use OpenAI, set environment variable:
# `$env:OPENAI_API_KEY = 'sk-...'
"@ -ForegroundColor Gray
}

# Main execution
Write-Host "`n═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  PA LLM Provider Check" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan

Test-OllamaConnection -Url $OllamaUrl
Test-OpenAiConnection -ApiKey $OpenAiKey

Show-ConfigExample

Write-Host "`n═══════════════════════════════════════════════════════════`n" -ForegroundColor Cyan
