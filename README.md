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

## CLI

The `pa` CLI gives you terminal access to your entire knowledge base.
Build it with:

```bash
go build -o bin/pa ./cmd/pa-cli
```

### Commands

```
pa ask "what's new in RAG research?"          Ask a question (RAG pipeline)
pa search "event sourcing"                     Search your knowledge base
pa search --semantic "consensus algorithms"    Semantic-only search
pa search --tags architecture,database "..."   Filter search by tags
pa ingest                                      Sync all sources
pa ingest github                               Sync a specific source
pa trending                                    Show trending repos
pa papers                                      Show recent arXiv papers
pa papers --source springer                    Show Springer papers
pa related <artifact-id>                       Show graph neighbourhood
pa status                                      Knowledge base stats
pa tag <artifact-id> "architecture"            Add a personal tag
pa discover                                    Run relationship discovery
pa enrich                                      Auto-tag and summarise artifacts
```

### Configuration

| Variable | Default | Description |
|---|---|---|
| `--server` | `http://localhost:8080` | PA server URL |
| `PA_SERVER_URL` | `http://localhost:8080` | PA server URL (env var) |

### Shell Completion

```bash
# zsh
pa completion zsh > "${fpath[1]}/_pa"

# bash
pa completion bash > /etc/bash_completion.d/pa

# fish
pa completion fish > ~/.config/fish/completions/pa.fish
```

## API Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check (database connectivity) |
| `GET` | `/status` | Knowledge base statistics and sync status |
| `GET` | `/search?q=...` | Hybrid semantic + full-text search |
| `GET` | `/search?q=...&mode=semantic` | Semantic (vector-only) search |
| `GET` | `/search?q=...&limit=10` | Limit result count (default 20) |
| `GET` | `/search?q=...&tags=a,b` | Filter results by tags (all must match) |
| `GET` | `/artifacts?source=X&type=Y` | List/filter artifacts |
| `GET` | `/artifacts/{id}/related` | Artifact graph neighbourhood |
| `POST` | `/artifacts/{id}/tags` | Add a tag to an artifact |
| `POST` | `/ask` | Ask a question, get a grounded answer with citations |
| `POST` | `/ingest/filesystem` | Trigger filesystem scan and ingestion |
| `POST` | `/ingest/github` | Trigger GitHub sync (repos, PRs, commits, stars, gists) |
| `POST` | `/ingest/arxiv` | Trigger arXiv paper sync (categories + keywords) |
| `POST` | `/ingest/trending` | Trigger GitHub Trending scrape + sync |
| `POST` | `/ingest/youtube` | Trigger YouTube video + transcript sync |
| `POST` | `/ingest/onedrive` | Trigger OneDrive document sync |
| `POST` | `/discover` | Run cross-source relationship discovery |
| `POST` | `/enrich` | Auto-tag and summarise unprocessed artifacts |

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

### arXiv Ingestion

The `/ingest/arxiv` endpoint syncs research papers from arXiv based on
configured categories and keyword watchlists.

```bash
curl -X POST http://localhost:8080/ingest/arxiv
```

Response:

```json
{
  "source": "arxiv",
  "ingested": 47,
  "skipped": 3,
  "errors": 0
}
```

Features:

- **Category filtering** — watch specific arXiv categories (e.g. `cs.AI`, `cs.SE`, `cs.DC`)
- **Keyword watchlists** — filter papers matching terms like "RAG", "knowledge graph"
- **Incremental sync** — cursor tracks the latest paper date; subsequent syncs only fetch newer papers
- **Configurable lookback** — `initial_lookback` controls how far back the first sync reaches (e.g. `30d`, `2w`)
- **Content hash deduplication** — skips unchanged papers on re-sync
- **arXiv ID parsing** — extracts and normalises arXiv IDs (handles versioned IDs like `2403.12345v1`)
- **Citation extraction** — scans abstracts for arXiv ID references and creates `CITES` relationships
- **Automatic embedding** — generates vector embeddings from title + abstract
- **Rate limiting** — respects arXiv's recommended 3-second delay between requests
- **Rich metadata** — stores authors, categories, arXiv ID, PDF URL in JSONB

Configure in `config/config.yaml`:

```yaml
sources:
  arxiv:
    enabled: true
    categories: ["cs.AI", "cs.SE", "cs.DC", "cs.CR"]
    keywords: ["knowledge graph", "RAG", "embeddings", "LLM agents"]
    max_results: 200
    initial_lookback: "30d"
```

### GitHub Trending Ingestion

The `/ingest/trending` endpoint scrapes GitHub's trending page for repos
across your configured languages and ingests them as artifacts.

```bash
curl -X POST http://localhost:8080/ingest/trending
```

Response:

```json
{
  "source": "trending",
  "ingested": 38,
  "skipped": 12,
  "errors": 0
}
```

Features:

- **HTML scraping** — uses [colly](https://github.com/gocolly/colly) to scrape github.com/trending
- **Per-language pages** — scrapes the "all languages" page plus each configured language
- **Deduplication** — repos appearing on multiple language pages are ingested once
- **Content hash deduplication** — includes the sync date, so repos are refreshed daily
- **README fetching** — optionally fetches README via GitHub API for richer embedding
- **Topic enrichment** — fetches repo topics via GitHub API for metadata
- **Automatic embedding** — generates vector embeddings from name + description + README
- **IMPLEMENTS detection** — finds arXiv papers semantically similar to trending repos, or explicitly referenced by arXiv ID in descriptions
- **SIMILAR_TOPIC detection** — finds your own GitHub repos that are topically related to trending repos via embedding similarity
- **Rich metadata** — stores language, stars, star velocity, topics, trending date in JSONB

The scraper reuses the GitHub token from the `github` source config for API
calls (README and topic fetching). Scraping the trending page itself does not
require authentication.

Configure in `config/config.yaml`:

```yaml
sources:
  trending:
    enabled: true
    languages: ["Go", "Python", "Rust", "TypeScript"]
    fetch_readme: true
```

### YouTube Ingestion

The `/ingest/youtube` endpoint syncs videos from configured YouTube channels
and search terms, including auto-generated transcripts.

```bash
curl -X POST http://localhost:8080/ingest/youtube
```

Response:

```json
{
  "source": "youtube",
  "ingested": 15,
  "skipped": 5,
  "errors": 0
}
```

Features:

- **Channel tracking** — syncs latest videos from configured YouTube channels
- **Search discovery** — finds videos matching configured search terms
- **Transcript extraction** — fetches auto-generated captions from YouTube's timedtext API
- **Incremental sync** — cursor per channel tracks latest video date
- **Deduplication** — videos discovered via both channel and search are ingested once
- **Video enrichment** — fetches full details (duration, view count, like count, tags)
- **Automatic embedding** — generates vector embeddings from title + description + transcript
- **Relationship detection** — links videos to semantically similar papers, repos, and trending repos
- **Rich metadata** — stores channel, duration, view/like counts, tags, thumbnail, transcript flag

Requires a YouTube Data API v3 key. Set the `YOUTUBE_API_KEY` environment
variable before starting the server:

```bash
export YOUTUBE_API_KEY=your_api_key_here
```

Configure in `config/config.yaml`:

```yaml
sources:
  youtube:
    enabled: true
    api_key: ${YOUTUBE_API_KEY}
    channels: ["UCVHFbqXqoYvEWM1Ddxl0QDg"]
    search_terms: ["Go programming", "distributed systems", "AI agents"]
    max_results: 50
```

### OneDrive Ingestion

The `/ingest/onedrive` endpoint syncs documents from Microsoft OneDrive
using the Graph API with delta queries for efficient incremental sync.

```bash
curl -X POST http://localhost:8080/ingest/onedrive
```

Response:

```json
{
  "source": "onedrive",
  "ingested": 8,
  "skipped": 2,
  "errors": 0
}
```

Features:

- **OAuth2 device code flow** — authenticate with your personal Microsoft account
  (the server prints a URL and code to visit on first run)
- **Token persistence** — access and refresh tokens are saved to a JSON file and
  reused across restarts; expired tokens are refreshed automatically
- **Delta sync** — uses Graph API delta queries so subsequent syncs only fetch
  changed or new files, not the entire folder
- **Deletion handling** — files removed from OneDrive are also removed from the
  knowledge base via delta events
- **Text extraction** for multiple formats:
  - `.md` / `.txt` — direct content
  - `.docx` — parses Office Open XML (word/document.xml) to extract paragraph text
  - `.pdf` — basic text extraction from PDF text objects
- **Configurable extension filter** — choose which file types to ingest
- **Content hash deduplication** — skips unchanged files via SHA-256
- **Automatic embedding** — generates vector embeddings on ingest
- **Rich metadata** — stores folder, filename, size, MIME type, web URL, parent path,
  last modified date in JSONB

Requires a Microsoft Entra (Azure AD) app registration with `Files.Read` and
`offline_access` permissions, configured for "Mobile and desktop applications"
with device code flow enabled. Set the `ONEDRIVE_CLIENT_ID` environment
variable before starting the server:

```bash
export ONEDRIVE_CLIENT_ID=your_client_id_here
```

Configure in `config/config.yaml`:

```yaml
sources:
  onedrive:
    enabled: false
    client_id: ${ONEDRIVE_CLIENT_ID}
    tenant_id: "consumers"
    folders: ["/Documents/Engineering"]
    extensions: [".md", ".txt", ".docx", ".pdf"]
    token_file: "config/onedrive_tokens.json"
```

| Setting | Default | Description |
|---|---|---|
| `client_id` | (required) | Microsoft Entra app registration client ID |
| `tenant_id` | `consumers` | `consumers` for personal accounts, or your tenant ID |
| `folders` | `[]` | OneDrive folder paths to sync |
| `extensions` | `.md,.txt,.docx,.pdf` | File extensions to ingest |
| `token_file` | `config/onedrive_tokens.json` | Path to persist OAuth2 tokens |

### Discovery

The `/discover` endpoint runs the cross-source discovery engine, which
automatically detects relationships between artifacts from different sources.

```bash
curl -X POST http://localhost:8080/discover
```

Response:

```json
{
  "cross_source_related": 12,
  "tag_co_occurrence": 3,
  "author_matches": 2,
  "citation_matches": 5,
  "trending_research": 4,
  "total": 26
}
```

The engine runs five discovery strategies:

- **Cross-source embedding similarity** — finds semantically similar artifacts
  across different sources (e.g. a note about "event sourcing" matched to a
  related arXiv paper) and creates `RELATED_TO` relationships
- **Tag co-occurrence** — artifacts sharing 2+ auto-generated tags from
  different sources get `SIMILAR_TOPIC` edges
- **Author matching** — matches author names across GitHub owners, arXiv
  paper authors, and YouTube channels to create `AUTHORED_BY_SAME` relationships
- **Citation matching** — scans arXiv paper content for GitHub repository URLs
  and creates `IMPLEMENTS` relationships
- **Trending + research** — finds trending repos whose embeddings are
  semantically similar to arXiv papers, suggesting they implement research ideas

All relationships include confidence scores and metadata about how they were
detected. The engine is idempotent — running it multiple times only creates
new relationships, never duplicates.

Configure in `config/config.yaml`:

```yaml
discovery:
  enabled: true
  similarity_threshold: 0.80
  max_candidates: 10
  batch_size: 50
```

| Setting | Default | Description |
|---|---|---|
| `similarity_threshold` | `0.80` | Minimum cosine similarity for `RELATED_TO` edges |
| `max_candidates` | `10` | Maximum neighbor candidates per artifact |
| `batch_size` | `50` | Artifacts processed per batch in similarity scan |

### Enrichment (Auto-Tagging & Summaries)

The `/enrich` endpoint triggers automatic tagging and summary generation for
artifacts that are missing them. It uses the configured chat model (Ollama or
OpenAI) to:

1. **Auto-tag** — suggest 3-5 concise topic tags per artifact
2. **Summarise** — generate a one-paragraph summary per artifact

```bash
curl -X POST http://localhost:8080/enrich
```

Response:

```json
{
  "tagged": 15,
  "summarised": 12,
  "errors": 0
}
```

Tags are stored with `auto_generated = true` in the `tags` table. Summaries
are written to the `summary` column on `artifacts` and appear in search
results for quick scanning.

Use the `tags` query parameter on `/search` to filter results:

```bash
curl "http://localhost:8080/search?q=patterns&tags=architecture,database"
```

This returns only artifacts that have **all** specified tags and match the query.

Configure in `config/config.yaml`:

```yaml
enrichment:
  enabled: true
  batch_size: 20
  max_tags: 5
```

| Setting | Default | Description |
|---|---|---|
| `batch_size` | `20` | Artifacts to process per enrichment run |
| `max_tags` | `5` | Maximum auto-generated tags per artifact |

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
go build -o bin/pa-server ./cmd/pa
go build -o bin/pa ./cmd/pa-cli
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
├── cmd/
│   ├── pa/main.go              # Server entrypoint
│   └── pa-cli/main.go          # CLI tool entrypoint
├── internal/
│   ├── api/                    # HTTP handlers (health, search, ask, ingest, discover, status, artifacts)
│   ├── cli/                    # CLI commands, HTTP client, terminal formatting
│   ├── config/                 # Configuration loading
│   ├── database/               # PostgreSQL connection & migrations
│   ├── discovery/              # Cross-source relationship detection engine
│   ├── ingestion/              # Source syncers
│   │   ├── syncer.go           # Common Syncer interface
│   │   ├── arxiv/              # arXiv paper syncer
│   │   │   ├── client.go       # Atom XML API client with rate limiting
│   │   │   └── syncer.go       # Category/keyword sync, citations, cursors
│   │   ├── filesystem/         # Local filesystem scanner + watcher
│   │   │   ├── scanner.go      # Directory walking, hashing, upsert
│   │   │   ├── frontmatter.go  # YAML frontmatter parser
│   │   │   ├── wikilinks.go    # Obsidian-style wikilink parser
│   │   │   └── watcher.go      # fsnotify real-time file watcher
│   │   ├── github/             # Personal GitHub syncer
│   │   │   ├── client.go       # HTTP client with auth, pagination, rate limiting
│   │   │   └── syncer.go       # Repo, PR, commit, star, gist sync + cross-refs
│   │   ├── onedrive/           # Microsoft OneDrive syncer
│   │   │   ├── client.go       # Graph API client, OAuth2 device code flow
│   │   │   ├── extract.go      # Text extraction (.md, .txt, .docx, .pdf)
│   │   │   └── syncer.go       # Delta sync, dedup, deletion handling
│   │   ├── trending/           # GitHub Trending scraper
│   │   │   ├── scraper.go      # Colly HTML scraper for trending page
│   │   │   └── syncer.go       # Sync, API enrichment, relationship detection
│   │   └── youtube/            # YouTube video + transcript syncer
│   │       ├── client.go       # YouTube Data API v3 client
│   │       ├── transcript.go   # Auto-caption transcript extraction
│   │       └── syncer.go       # Channel/search sync, relationship detection
│   ├── tagging/                # Auto-tagging & summary generation service
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
