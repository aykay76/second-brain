# pa — Personal AI Memory Agent

A personal "second brain" that ingests, links, and recalls knowledge from your
digital life — code, documents, notes, research papers, trending projects, and
videos — and surfaces connections between them.

## Prerequisites

- **Go 1.22+**
- **Podman** (or Docker) with Compose support
- **Ollama** running locally with models pulled:
  ```bash
  ollama pull nomic-embed-text
  ollama pull llama3.1
  ```

## Quick Start

### 1. Start PostgreSQL (with pgvector)

```bash
cd pa
podman compose up -d
```

This starts PostgreSQL 16 with the pgvector extension on **port 5433**
(mapped to avoid conflicts with any existing local PostgreSQL on 5432).

Verify it's ready:

```bash
podman exec pa_postgres_1 pg_isready -U pa -d pa
```

### 2. Run the server

```bash
go run ./cmd/pa
```

The server will:
- Connect to PostgreSQL
- Run database migrations automatically
- Start listening on **http://localhost:8080**

### 3. Check health

```bash
curl http://localhost:8080/health
```

Expected response:

```json
{"status": "ok", "database": "up"}
```

## Configuration

Configuration lives in `config/config.yaml`. Values containing `${VAR}` are
expanded from environment variables at load time.

Override the config file path with:

```bash
export PA_CONFIG_PATH=/path/to/your/config.yaml
```

### Key settings

| Setting | Default | Description |
|---|---|---|
| `server.port` | `8080` | HTTP server port |
| `db.host` | `localhost` | PostgreSQL host |
| `db.port` | `5433` | PostgreSQL port |
| `db.name` | `pa` | Database name |
| `db.user` | `pa` | Database user |
| `db.password` | `pa` | Database password |
| `llm.provider` | `ollama` | LLM backend: `ollama` or `openai` |
| `llm.ollama.base_url` | `http://localhost:11434` | Ollama API URL |
| `llm.ollama.embedding_model` | `nomic-embed-text` | Embedding model (768d) |
| `llm.ollama.chat_model` | `llama3.1` | Chat model for RAG/summarisation |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check (database connectivity) |
| `GET` | `/search?q=...` | Hybrid semantic + full-text search |
| `GET` | `/search?q=...&mode=semantic` | Semantic (vector-only) search |
| `GET` | `/search?q=...&limit=10` | Limit result count (default 20) |

### Search

The `/search` endpoint embeds your query via the configured LLM provider
(Ollama by default) and performs a hybrid search combining:

- **Semantic similarity** (pgvector cosine distance, weight 0.7)
- **Full-text search** (PostgreSQL `ts_rank`, weight 0.3)

Requires Ollama to be running with the embedding model loaded. If Ollama
is not reachable, the endpoint returns an error with details.

Example:

```bash
curl "http://localhost:8080/search?q=database+migrations&limit=5"
```

Response:

```json
{
  "query": "database migrations",
  "count": 2,
  "results": [
    {
      "id": "...",
      "source": "filesystem",
      "artifact_type": "note",
      "title": "Migration Strategies",
      "score": 0.82,
      "metadata": {}
    }
  ]
}
```

## Database

The schema is managed via [golang-migrate](https://github.com/golang-migrate/migrate).
Migrations run automatically on startup. The schema includes:

- **artifacts** — universal knowledge records with JSONB metadata and full-text search
- **artifact_embeddings** — pgvector embeddings (768d) with HNSW index
- **relationships** — typed edges between artifacts with confidence scores
- **sync_cursors** — incremental sync state per source
- **tags** — user-defined and auto-generated labels

### Reset the database

```bash
podman compose down -v   # removes volumes (all data)
podman compose up -d     # fresh start
```

## Development

### Build

```bash
go build -o bin/pa ./cmd/pa
```

### Run tests

```bash
go test ./...
```

### Stop infrastructure

```bash
podman compose down      # stop containers, keep data
podman compose down -v   # stop containers, delete data
```

## LLM Providers

The LLM layer is abstracted behind `EmbeddingProvider` and `ChatProvider`
interfaces. Switch providers by changing `llm.provider` in the config.

| Provider | Embedding Model | Dimension | Chat Model | Notes |
|---|---|---|---|---|
| `ollama` | `nomic-embed-text` | 768 | `llama3.1` | Default, fully local |
| `openai` | `text-embedding-3-small` | 1536 | `gpt-4o` | Requires API key |

To use OpenAI instead of Ollama:

```yaml
llm:
  provider: openai
  openai:
    api_key: ${OPENAI_API_KEY}
```

## Project Structure

```
pa/
├── cmd/pa/main.go              # Server entrypoint
├── internal/
│   ├── api/                    # HTTP handlers (health, search)
│   ├── config/                 # Configuration loading
│   ├── database/               # PostgreSQL connection & migrations
│   ├── llm/                    # LLM provider interfaces & implementations
│   │   ├── provider.go         # EmbeddingProvider + ChatProvider interfaces
│   │   ├── ollama.go           # Ollama implementation
│   │   ├── openai.go           # OpenAI implementation
│   │   └── factory.go          # Provider factory
│   └── retrieval/              # Embedding service & search
│       ├── embedding.go        # Batch embedding + pgvector storage
│       └── search.go           # Semantic & hybrid search
├── migrations/                 # SQL migration files (embedded)
├── config/config.yaml          # Default configuration
└── compose.yaml                # PostgreSQL + pgvector
```
