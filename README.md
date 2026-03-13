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
| `POST` | `/ask` | Ask a question, get a grounded answer with citations |
| `POST` | `/ingest/filesystem` | Trigger filesystem scan and ingestion |
| `POST` | `/ingest/github` | Trigger GitHub sync (repos, PRs, commits, stars, gists) |

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

### Ask (RAG)

The `/ask` endpoint is a retrieval-augmented generation (RAG) pipeline that
answers questions grounded in your personal knowledge base. It:

1. Embeds your question via the configured LLM provider
2. Performs hybrid search (semantic + full-text) for top-k relevant artifacts
3. Enriches results with 1-hop related artifacts from the knowledge graph
4. Assembles a context prompt with source metadata for citation
5. Sends the context + question to the chat model
6. Extracts `[N]` citation markers and maps them back to sources

**JSON body (recommended):**

```bash
curl -X POST http://localhost:8080/ask \
  -H "Content-Type: application/json" \
  -d '{"question": "What approach did I use for database migrations?", "top_k": 10}'
```

**Query parameters (alternative):**

```bash
curl -X POST "http://localhost:8080/ask?q=What+approach+did+I+use+for+database+migrations&top_k=10"
```

Response:

```json
{
  "question": "What approach did I use for database migrations?",
  "answer": "Based on [1], you used golang-migrate with SQL migration files...",
  "sources": [
    {
      "index": 1,
      "artifact_id": "...",
      "title": "Migration Strategies",
      "source": "filesystem",
      "artifact_type": "note",
      "source_url": "/Users/you/notes/migrations.md",
      "score": 0.85,
      "cited": true
    },
    {
      "index": 2,
      "artifact_id": "...",
      "title": "database-tools",
      "source": "github",
      "artifact_type": "repo",
      "source_url": "https://github.com/you/database-tools",
      "score": 0.72,
      "cited": false
    }
  ]
}
```

Parameters:

| Parameter | Type | Default | Description |
|---|---|---|---|
| `question` / `q` | string | (required) | The question to answer |
| `top_k` | int | 10 | Number of artifacts to retrieve for context |

The system prompt instructs the model to answer only from provided context
and cite sources using `[N]` notation. Sources marked `"cited": true` were
explicitly referenced in the answer.

### Filesystem Ingestion

The `/ingest/filesystem` endpoint scans all configured directories for
matching files and ingests them as artifacts. Files are deduplicated by
content hash — unchanged files are skipped on subsequent syncs.

```bash
curl -X POST http://localhost:8080/ingest/filesystem
```

Response:

```json
{
  "source": "filesystem",
  "ingested": 12,
  "skipped": 3,
  "errors": 0
}
```

Features:

- **Recursive scanning** of configured directories
- **Extension filtering** (default: `.md`, `.txt`)
- **Content hash deduplication** — skips unchanged files via SHA-256
- **Markdown frontmatter parsing** — YAML frontmatter extracted into metadata
- **Wikilink resolution** — Obsidian-style `[[page]]` links create `LINKS_TO` relationships
- **Real-time updates** — `fsnotify` file watcher processes changes as they happen
- **Automatic embedding** — generates vector embeddings on ingest

Configure watched directories in `config/config.yaml`:

```yaml
sources:
  filesystem:
    enabled: true
    paths:
      - ~/notes
      - ~/Documents/tech
    extensions: [".md", ".txt"]
```

### GitHub Ingestion

The `/ingest/github` endpoint syncs your personal GitHub activity into
the knowledge base. It ingests owned repos, pull requests with comments,
commits with file changes, starred repos, and gists.

```bash
curl -X POST http://localhost:8080/ingest/github
```

Response:

```json
{
  "source": "github",
  "ingested": 45,
  "skipped": 12,
  "errors": 0
}
```

Features:

- **Owned repos** — name, description, README content, language, topics
- **Configurable include list** — sync additional repos (e.g. key dependencies)
- **Pull requests** — title, description, and all comments concatenated
- **Commits** — message, files changed with additions/deletions
- **Starred repos** — name, description, README content
- **Gists** — description and file contents
- **Incremental sync** — cursor per resource type, only fetches new/updated items
- **Cross-references** — commit messages mentioning `#N` create `REFERENCES` relationships to PRs
- **Automatic embedding** — generates vector embeddings on ingest
- **Rate limit handling** — respects GitHub API rate limits with automatic backoff
- **Pagination** — follows `Link` headers to fetch all results

Requires a GitHub personal access token. Set the `GITHUB_TOKEN` environment
variable before starting the server:

```bash
export GITHUB_TOKEN=ghp_your_token_here
```

Configure in `config/config.yaml`:

```yaml
sources:
  github:
    enabled: true
    token: ${GITHUB_TOKEN}
    sync_starred: true
    sync_gists: true
    include_repos:
      - golang/go
      - kubernetes/kubernetes
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
│   ├── api/                    # HTTP handlers (health, search, ask, ingest)
│   ├── config/                 # Configuration loading
│   ├── database/               # PostgreSQL connection & migrations
│   ├── ingestion/              # Source syncers
│   │   ├── syncer.go           # Common Syncer interface
│   │   ├── filesystem/         # Local filesystem scanner + watcher
│   │   │   ├── scanner.go      # Directory walking, hashing, upsert
│   │   │   ├── frontmatter.go  # YAML frontmatter parser
│   │   │   ├── wikilinks.go    # Obsidian-style wikilink parser
│   │   │   └── watcher.go      # fsnotify real-time file watcher
│   │   └── github/             # Personal GitHub syncer
│   │       ├── client.go       # HTTP client with auth, pagination, rate limiting
│   │       └── syncer.go       # Repo, PR, commit, star, gist sync + cross-refs
│   ├── llm/                    # LLM provider interfaces & implementations
│   │   ├── provider.go         # EmbeddingProvider + ChatProvider interfaces
│   │   ├── ollama.go           # Ollama implementation
│   │   ├── openai.go           # OpenAI implementation
│   │   └── factory.go          # Provider factory
│   └── retrieval/              # Embedding service, search & RAG
│       ├── embedding.go        # Batch embedding + pgvector storage
│       ├── search.go           # Semantic & hybrid search
│       └── rag.go              # RAG pipeline (search → enrich → chat → cite)
├── migrations/                 # SQL migration files (embedded)
├── config/config.yaml          # Default configuration
└── compose.yaml                # PostgreSQL + pgvector
```
