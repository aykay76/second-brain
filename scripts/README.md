# PA Scripts Directory

Utility scripts for managing and debugging the PA personal knowledge agent system.

## Prerequisites

- **PowerShell 5.1+** (Windows)
- **PostgreSQL client tools** (`psql`) - required for database scripts
  - Windows: [PostgreSQL Downloads](https://www.postgresql.org/download/windows/)
  - After installation, add `C:\Program Files\PostgreSQL\<version>\bin` to your PATH
- **Docker** or **Podman** - required for container management
- **Go 1.25+** - for running the application

## Quick Start

```powershell
# Check if everything is healthy
./all-checks.ps1

# Start the database
./container.ps1 -Command up

# Check database status
./db-health.ps1

# View available database queries
./db-query.ps1 -Query help
```

## Scripts

### `all-checks.ps1` — Complete System Diagnostic

Runs all diagnostic checks in sequence. Perfect for verifying your entire setup.

```powershell
./all-checks.ps1
```

**Checks:**
- Service health (database, server, Ollama)
- Database details (tables, sizes, migrations)
- LLM provider connectivity

---

### `health-check.ps1` — Service Health Check

Tests connectivity to database, PA server, and Ollama.

```powershell
./health-check.ps1 [options]
```

**Options:**
- `-ServerUrl` — PA server URL (default: `http://localhost:8080`)
- `-OllamaUrl` — Ollama URL (default: `http://localhost:11434`)
- `-DbHost` — Database host (default: `localhost`)
- `-DbPort` — Database port (default: `5433`)

**Output:**
```
✅ Database is healthy
✅ PA Server is healthy
❌ Ollama is down or unreachable
```

---

### `db-health.ps1` — Database Details & Statistics

Inspects database schema, sizes, migrations, and connection info.

```powershell
./db-health.ps1 [options]
```

**Options:**
- `-Host` — Database host (default: `localhost`)
- `-Port` — Database port (default: `5433`)
- `-Database` — Database name (default: `pa`)
- `-User` — Database user (default: `pa`)
- `-Password` — Database password (default: `pa`)

**Displays:**
- PostgreSQL version
- Migration history
- Table sizes
- Active connections
- Available tables for ingestion monitoring

---

### `db-query.ps1` — Database Query Helper

Pre-built SQL queries for common diagnostic tasks. No need to write SQL!

```powershell
# List available queries
./db-query.ps1 -Query help

# View a query before running it
./db-query.ps1 -Query schema

# Execute a query
./db-query.ps1 -Query schema -Execute
```

**Available Queries:**

| Query | Purpose |
|-------|---------|
| `schema` | Tables and their sizes |
| `tables` | Column definitions for all tables |
| `migrations` | Migration history |
| `indexes` | Index definitions |
| `stats` | Database size and row counts |
| `connections` | Active database connections |
| `performance` | Scan and update statistics |

**Examples:**

```powershell
# Check table row counts
./db-query.ps1 -Query stats -Execute

# Monitor active queries
./db-query.ps1 -Query connections -Execute

# Check index usage
./db-query.ps1 -Query performance -Execute
```

---

### `llm-check.ps1` — LLM Provider Validation

Verifies Ollama and OpenAI connectivity and lists available models.

```powershell
./llm-check.ps1 [options]
```

**Options:**
- `-OllamaUrl` — Ollama URL (default: `http://localhost:11434`)

**Output:**
```
Testing Ollama connection...
  ✅ Connection successful
  📦 Available models:
     - nomic-embed-text:latest
     - llama3.1:latest
     - mistral:latest

Testing OpenAI connection...
  ⚠️  OPENAI_API_KEY not set in environment
```

---

### `container.ps1` — Container Management

Manage PostgreSQL container via Docker Compose or Podman.

```powershell
./container.ps1 -Command <command>
```

**Commands:**

| Command | Purpose |
|---------|---------|
| `up` | Start PostgreSQL container |
| `down` | Stop and remove containers |
| `restart` | Restart services |
| `logs` | Show container logs (live) |
| `ps` | List running containers |
| `shell` | Open interactive `psql` shell |
| `help` | Show help message |

**Examples:**

```powershell
# Start the database
./container.ps1 -Command up

# Check container status
./container.ps1 -Command ps

# Open database shell
./container.ps1 -Command shell

# View logs
./container.ps1 -Command logs

# Stop everything
./container.ps1 -Command down
```

---

## Common Workflows

### 1. Verify Installation

After your first setup, verify everything works:

```powershell
# Start the database
./container.ps1 -Command up

# Run all health checks
./all-checks.ps1

# Check LLM provider
./llm-check.ps1
```

### 2. Debug Ingestion Issues

When ingestion isn't working as expected:

```powershell
# Check database connectivity
./db-health.ps1

# See what tables have data
./db-query.ps1 -Query stats -Execute

# Check table structure
./db-query.ps1 -Query schema -Execute

# Monitor active connections
./db-query.ps1 -Query connections -Execute
```

### 3. Database Investigation

Opening the psql shell directly:

```powershell
./container.ps1 -Command shell

# Now in psql:
pa=> SELECT * FROM schema_migrations;
pa=> SELECT count(*) FROM your_table;
pa=> \dt -- list all tables
pa=> \d table_name -- describe a table
pa=> \q -- quit
```

### 4. Troubleshoot Connectivity Issues

```powershell
# Quick health check
./health-check.ps1

# Detailed database diagnostics
./db-health.ps1

# Check Ollama/LLM setup
./llm-check.ps1

# View container logs
./container.ps1 -Command logs
```

---

## Environment Configuration

Database connection defaults (can be overridden):

```powershell
# Override database host
./db-health.ps1 -Host 192.168.1.100 -Port 5433

# Use custom user
./db-query.ps1 -User myuser -Password mypass -Query schema -Execute
```

LLM provider setup:

```powershell
# Set OpenAI API key for this session
$env:OPENAI_API_KEY = "sk-..."

# Run LLM check with custom Ollama URL
./llm-check.ps1 -OllamaUrl "http://192.168.1.100:11434"
```

---

## Troubleshooting

### "psql not found"

Install PostgreSQL client tools or add to PATH:

```powershell
# Download: https://www.postgresql.org/download/windows/
# Add to PATH: C:\Program Files\PostgreSQL\<version>\bin

# Verify installation
psql --version
```

### "Cannot connect to database"

```powershell
# Check if container is running
./container.ps1 -Command ps

# Start the database
./container.ps1 -Command up

# Check logs
./container.ps1 -Command logs
```

### Database connection timeout

The database may need time to initialize:

```powershell
# Check health status
./health-check.ps1

# Wait and retry
Start-Sleep -Seconds 5
./db-health.ps1
```

### Ollama not found

```powershell
# Check if Ollama is running
./llm-check.ps1

# Make sure Ollama is started: ollama serve
# Or use OpenAI instead in config/config.yaml
```

---

## Creating Custom Queries

To make `db-query.ps1` even more useful, edit the file and add your own SQL queries in the `$queries` hashtable:

```powershell
$queries = @{
    # ... existing queries ...
    
    "my-custom-query" = @"
-- Your custom SQL here
SELECT * FROM your_table WHERE condition;
"@
}
```

Then use it:

```powershell
./db-query.ps1 -Query my-custom-query -Execute
```

---

## Tips & Best Practices

1. **Run `all-checks.ps1` regularly** — Quick health check of everything
2. **Keep logs open** — `./container.ps1 -Command logs` in a separate terminal
3. **Use `db-query.ps1`** — Avoid writing SQL by hand for common tasks
4. **Check migrations** — Before running the server: `./db-query.ps1 -Query migrations -Execute`
5. **Monitor connections** — Track active queries: `./db-query.ps1 -Query connections -Execute`

---

## Related Resources

- [PostgreSQL Documentation](https://www.postgresql.org/docs/)
- [pgvector](https://github.com/pgvector/pgvector) — Vector storage for embeddings
- [Ollama](https://ollama.ai/) — Local LLM provider
- [golang-migrate](https://github.com/golang-migrate/migrate) — Database migrations

---
