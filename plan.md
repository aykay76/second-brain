# Personal AI Memory Agent — Implementation Plan

A personal "second brain" that ingests, links, and recalls knowledge from your
digital life — code, documents, notes, research papers, trending projects, and
videos — and surfaces connections between them.

---

## Vision

You accumulate knowledge across dozens of sources: code you write, repos you
star, notes you take, papers you read, videos you watch, trends you follow.
Most of it becomes unfindable within weeks. This agent gives you perfect recall
and connects the dots across sources:

- "What was that approach I used for database migrations in my side project last year?"
- "What CLI tool did I star for working with JSON?"
- "What did I write about event sourcing in my notes?"
- "Are there any recent arXiv papers related to what I'm working on?"
- "What's trending in Go this week that relates to my interests?"
- "What YouTube talks have I watched about distributed systems?"
- "Show me the connection between that Springer paper and the repo I starred"

It's a personal engineering memory + research intelligence system — not a task
manager, not a note-taking app, but a retrieval and discovery layer that sits
on top of everything you already have and follow.

---

## Technology Stack

Minimal, consolidated, runs entirely on your local machine.

| Concern | Technology | Notes |
|---|---|---|
| **Backend** | Go | Single binary, easy to run anywhere |
| **Database** | PostgreSQL 16 + pgvector | All storage in one place |
| **LLM provider** | **Ollama** (primary) / OpenAI (optional) | Provider interface — swap freely |
| **Embedding model** | Ollama `nomic-embed-text` (768d) or OpenAI `text-embedding-3-small` (1536d) | Configurable dimension |
| **Chat model** | Ollama `llama3` / `mistral` / `phi-3` or OpenAI `gpt-4o` | For RAG, summarisation, tagging |
| **Web scraping** | colly (Go) | For GitHub Trending, content extraction |
| **Migrations** | golang-migrate | SQL migration files |
| **Config** | Native YAML | YAML + env vars |
| **Local dev** | Docker Compose | PostgreSQL + pgvector (Ollama runs natively) |
| **Testing** | testify + testcontainers-go | |

The entire stack is one Go binary + one PostgreSQL container + Ollama (which
you already have). No cloud dependencies required — OpenAI is an optional
upgrade, not a requirement.

### LLM Provider Architecture

The embedding and chat services use a provider interface so you can switch
between backends without changing any calling code:

```go
type EmbeddingProvider interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}

type ChatProvider interface {
    Complete(ctx context.Context, messages []Message) (string, error)
}
```

Implementations: `OllamaProvider`, `OpenAIProvider`. Configured via YAML:

```yaml
llm:
  provider: ollama          # or "openai"
  ollama:
    base_url: http://localhost:11434
    embedding_model: nomic-embed-text
    chat_model: llama3.1
  openai:
    api_key: ${OPENAI_API_KEY}
    embedding_model: text-embedding-3-small
    chat_model: gpt-4o
```

---

## Knowledge Sources

### Core — Personal Knowledge

| Source | What to ingest | Sync method |
|---|---|---|
| **Local filesystem** | Markdown files, text files, PDFs in configured directories | fsnotify watcher + periodic scan |
| **GitHub (personal)** | Your repos (PRs, commits, issues, READMEs), starred repos, gists | GitHub API, cursor-based polling |
| **OneDrive** | Documents, notes, spreadsheets from configured folders | Microsoft Graph API, delta sync |

### Research & Trends

| Source | What to ingest | Sync method |
|---|---|---|
| **arXiv** | Papers in configured categories (title, authors, abstract, categories) | arXiv API (Atom search) + RSS feeds per category |
| **Springer** | Journal articles / conference papers (title, authors, abstract, DOI) | Springer Nature API (free metadata tier) |
| **GitHub Trending** | Trending repos by language (name, description, README, stars, velocity) | Scrape github-trending.today or GitHub search API |
| **YouTube** | Videos from configured channels / search terms (title, description, transcript) | YouTube Data API v3 + transcript extraction |

### Extensions (add later if useful)

| Source | What to ingest | Sync method |
|---|---|---|
| **Browser bookmarks** | Title, URL, folder/tags from Chrome/Edge/Firefox | Import HTML/JSON export file |
| **RSS / blog feeds** | Articles from blogs you follow | OPML import + feed polling |
| **Obsidian vault** | Notes with wikilinks, tags, YAML frontmatter | Filesystem watcher with Obsidian-aware parser |
| **Stack Overflow** | Your own questions and answers | Stack Exchange API |
| **Hacker News** | Top/best stories matching your interests | HN Algolia API |

---

## Data Model

### artifacts

The universal record for any piece of knowledge.

| Column | Type | Description |
|---|---|---|
| id | UUID | Primary key |
| source | TEXT | `github`, `onedrive`, `filesystem`, `arxiv`, `springer`, `github_trending`, `youtube`, `bookmark`, `rss` |
| artifact_type | TEXT | `repo`, `pr`, `commit`, `issue`, `gist`, `star`, `document`, `note`, `paper`, `trending_repo`, `video`, `bookmark`, `article` |
| external_id | TEXT | Source-specific unique ID (URL, path, SHA, arXiv ID, DOI) |
| title | TEXT | Human-readable title |
| content | TEXT | Full text content (abstract for papers, transcript for videos, etc.) |
| summary | TEXT | LLM-generated summary (nullable, populated async) |
| metadata | JSONB | Source-specific fields (see below) |
| content_hash | TEXT | SHA-256 of content for change detection |
| source_url | TEXT | Link back to original |
| created_at | TIMESTAMPTZ | When the artifact was created at source |
| ingested_at | TIMESTAMPTZ | When we ingested it |
| updated_at | TIMESTAMPTZ | Last update |

**Metadata JSONB examples by source:**

```jsonc
// arXiv paper
{ "authors": ["..."], "categories": ["cs.AI", "cs.SE"], "arxiv_id": "2403.12345", "pdf_url": "..." }

// Springer article
{ "authors": ["..."], "doi": "10.1007/...", "journal": "...", "published_date": "..." }

// GitHub trending repo
{ "language": "Go", "stars": 1234, "stars_today": 89, "topics": ["..."] }

// YouTube video
{ "channel": "...", "duration": "PT15M", "view_count": 50000, "tags": ["..."], "has_transcript": true }
```

### artifact_embeddings

| Column | Type | Description |
|---|---|---|
| id | UUID | Primary key |
| artifact_id | UUID | FK to artifacts |
| embedding | vector(768) | pgvector column (dimension matches configured model) |
| model_name | TEXT | e.g. `nomic-embed-text` or `text-embedding-3-small` |
| created_at | TIMESTAMPTZ | |

*Note: vector dimension is set at migration time based on configured embedding model. If you switch models, re-embed and recreate the column.*

### relationships

| Column | Type | Description |
|---|---|---|
| id | UUID | Primary key |
| source_id | UUID | FK to artifacts |
| target_id | UUID | FK to artifacts |
| relation_type | TEXT | See relation types below |
| confidence | REAL | 0.0-1.0, for auto-detected relationships |
| metadata | JSONB | Additional context |
| created_at | TIMESTAMPTZ | |

**Relation types:**

| Type | Example |
|---|---|
| `REFERENCES` | Commit message mentions issue number |
| `BELONGS_TO` | Commit belongs to a repo |
| `RELATED_TO` | General semantic similarity (auto-detected) |
| `LINKS_TO` | Obsidian wikilink, markdown link, URL reference |
| `STARRED` | You starred this repo |
| `BOOKMARKED` | You bookmarked this URL |
| `CITES` | Paper cites another paper |
| `IMPLEMENTS` | Repo implements ideas from a paper |
| `SIMILAR_TOPIC` | Artifacts share topic/category (auto-detected) |
| `TRENDING_WITH` | Repos trending at the same time |
| `AUTHORED_BY_SAME` | Same author across different sources |

### sync_cursors

| Column | Type | Description |
|---|---|---|
| source_name | TEXT | PK (e.g. `github:repos`, `arxiv:cs.AI`, `youtube:channel:xyz`) |
| cursor_value | TEXT | Last-synced marker |
| updated_at | TIMESTAMPTZ | |

### tags

| Column | Type | Description |
|---|---|---|
| id | UUID | Primary key |
| artifact_id | UUID | FK to artifacts |
| tag | TEXT | User-defined or auto-generated tag |
| auto_generated | BOOLEAN | True if suggested by LLM |

---

## Project Structure

```
pa/
├── cmd/
│   ├── pa/main.go                   # Server entrypoint
│   └── pa-cli/main.go               # CLI tool
├── internal/
│   ├── api/                          # HTTP handlers, OpenAPI spec
│   ├── artifact/                     # Domain: artifact CRUD, repository
│   ├── llm/                          # Provider interface + implementations
│   │   ├── provider.go               # EmbeddingProvider + ChatProvider interfaces
│   │   ├── ollama.go                 # Ollama implementation
│   │   └── openai.go                 # OpenAI implementation
│   ├── ingestion/                    # Source syncers
│   │   ├── filesystem/               # Local file watcher + scanner
│   │   ├── github/                   # Personal GitHub syncer
│   │   ├── onedrive/                 # Microsoft Graph syncer
│   │   ├── arxiv/                    # arXiv paper syncer
│   │   ├── springer/                 # Springer metadata syncer
│   │   ├── trending/                 # GitHub Trending scraper
│   │   ├── youtube/                  # YouTube video + transcript syncer
│   │   ├── bookmarks/                # Bookmark file importer
│   │   ├── rss/                      # RSS feed poller
│   │   └── syncer.go                 # Common Syncer interface
│   ├── relationship/                 # Edge management + graph queries
│   ├── retrieval/                    # Search, enrichment, RAG pipeline
│   ├── tagging/                      # Auto-tagging service
│   ├── discovery/                    # Cross-source relationship detection
│   └── config/                       # Configuration
├── migrations/                       # SQL migration files
├── config/
│   └── config.yaml                   # Default configuration
├── compose.yaml                      # PostgreSQL + pgvector
├── go.mod
└── README.md
```

---

## Implementation Plan

### Phase 1: Foundation & Personal Knowledge (sessions 1-5)

#### Session 1 — Project Scaffolding & Schema

**Goal:** Bootable Go service with PostgreSQL, pgvector, and the core schema.

- [x] Initialise Go module (`pa`)
- [x] Create `cmd/pa/main.go` with graceful shutdown
- [x] Set up configuration (YAML + env vars)
- [x] Create `compose.yaml` (PostgreSQL 16 + pgvector)
- [x] Database connection with health check
- [x] Migrations:
  - [x] Enable pgvector extension
  - [x] Create `artifacts` table
  - [x] Create `artifact_embeddings` table with HNSW index
  - [x] Create `relationships` table
  - [x] Create `sync_cursors` table
  - [x] Create `tags` table
  - [x] Add full-text search (`tsvector`) column + GIN index on artifacts
- [x] `/health` endpoint
- [x] Verify: compose up, migrate, health check passes

#### Session 2 — LLM Provider Layer & Embedding Pipeline

**Goal:** Abstracted LLM layer with Ollama as primary, plus embedding generation and search.

- [x] Define `EmbeddingProvider` and `ChatProvider` interfaces
- [x] Implement `OllamaProvider` (HTTP client to local Ollama instance)
  - [x] Embedding via `POST /api/embed` (nomic-embed-text, batch support)
  - [x] Chat completion via `POST /api/chat` (llama3 / mistral)
- [x] Implement `OpenAIProvider` (HTTP client, optional fallback)
- [x] Provider factory: instantiate based on config
- [x] Embedding service with batching support
- [x] Store embeddings via pgvector (using pgvector-go)
- [x] Semantic search function (embed query -> cosine similarity -> top-k)
- [x] Hybrid search: combine pgvector similarity + `ts_rank` full-text score
- [x] `/search` API endpoint (query string -> ranked results with scores)
- [ ] Integration tests with testcontainers-go (use mock provider for embedding)
- [ ] Manual test: verify Ollama connectivity, generate test embeddings

#### Session 3 — Local Filesystem Syncer

**Goal:** Scan configured directories, ingest markdown/text files as artifacts.

- [x] Define `Syncer` interface (shared across all sources)
- [x] Implement filesystem scanner:
  - [x] Walk configured directories recursively
  - [x] Filter by extension (`.md`, `.txt`, `.pdf`, configurable)
  - [x] Compute content hash for change detection
  - [x] Upsert artifacts (skip unchanged files based on hash)
- [x] Extract basic metadata: file path, size, modification date, parent directory
- [x] Parse markdown frontmatter (YAML) into metadata JSONB if present
- [x] Parse Obsidian-style wikilinks (`[[page]]`) and create `LINKS_TO` relationships
- [x] Implement `fsnotify` file watcher for real-time updates (optional, can start with polling)
- [x] Generate embeddings on ingest via configured provider
- [x] Create `/ingest/filesystem` trigger endpoint
- [x] Tests: create temp directory with test files, verify ingestion and dedup

#### Session 4 — GitHub Syncer

**Goal:** Ingest personal GitHub activity.

- [x] GitHub API client (personal access token auth)
- [x] Sync your repos: name, description, README content, language, topics
- [x] Sync PRs from your repos: title, description, comments
- [x] Sync commits from your repos: message, files changed
- [x] Sync starred repos: name, description, README
- [x] Sync gists: description, content
- [x] Incremental sync with cursor (last-synced-at per resource type)
- [x] Extract cross-references: commit mentions issue -> relationship
- [x] Generate embeddings on ingest
- [x] `/ingest/github` trigger endpoint
- [x] Tests with mock GitHub API

#### Session 5 — RAG Query Endpoint

**Goal:** Ask questions, get grounded answers from your knowledge base.

- [x] RAG pipeline:
  - [x] Embed question via configured provider
  - [x] Hybrid search for top-k artifacts
  - [x] Enrich with 1-hop related artifacts
  - [x] Assemble context prompt
  - [x] Chat completion via configured provider (Ollama or OpenAI)
  - [x] Extract citations, format response
- [x] `/ask` endpoint (question -> answer + sources)
- [x] RAG prompt template for personal knowledge:
  - [x] "You are a personal knowledge assistant. Answer based only on the provided context."
  - [x] Include source type and URL in context for citation
- [x] Tests: mock provider, verify context assembly
- [x] End-to-end: ingest notes + GitHub, ask questions, verify answers

**Phase 1 milestone:** A working personal knowledge agent that indexes your files and GitHub, answers questions using Ollama locally, with zero cloud dependency.

---

### Phase 2: Research & Trend Intelligence (sessions 6-8)

#### Session 6 — arXiv Syncer

**Goal:** Track new research papers in categories you care about.

- [x] arXiv API client (Atom-based search API)
- [x] Configure watched categories (e.g. `cs.AI`, `cs.SE`, `cs.DC`, `cs.CR`)
- [x] Configure keyword watchlists (e.g. "knowledge graph", "RAG", "embeddings")
- [x] Sync papers: title, authors, abstract, categories, submission date, PDF URL
- [x] Incremental sync (query by date range, store cursor)
- [x] Parse arXiv IDs for cross-referencing
- [x] Extract citations from abstracts where mentioned (create `CITES` relationships)
- [x] Generate embeddings from title + abstract
- [x] `/ingest/arxiv` trigger endpoint
- [x] Tests with mock arXiv API responses

#### Session 7 — GitHub Trending Scraper

**Goal:** Track trending repos across languages you follow.

- [x] Trending scraper using colly (scrape github.com/trending)
- [x] Alternative: GitHub search API (`created:>DATE sort:stars`) for similar results
- [x] Configure watched languages (e.g. Go, Python, Rust, TypeScript)
- [x] Sync trending repos: name, description, language, stars, star velocity, topics
- [x] Fetch README content for richer embedding
- [x] Detect if trending repo implements an arXiv paper (title/description match -> `IMPLEMENTS`)
- [x] Detect repos related to your own repos (`SIMILAR_TOPIC` based on embedding similarity)
- [x] Daily sync (trending changes daily)
- [x] `/ingest/trending` trigger endpoint
- [x] Tests with fixture HTML / mock API

#### Session 8 — YouTube Syncer

**Goal:** Index tech talks, conference presentations, and tutorials.

- [x] YouTube Data API v3 client
- [x] Configure watched channels (e.g. CNCF, GopherCon, Strange Loop, Computerphile)
- [x] Configure search terms for discovery
- [x] Sync videos: title, description, channel, tags, duration, publish date, view count
- [x] Transcript extraction (YouTube auto-captions via timedtext API or library)
- [x] Generate embeddings from title + description + transcript (chunked if long)
- [x] Link videos to related papers/repos when topics overlap (`RELATED_TO`)
- [x] Incremental sync per channel (latest video date as cursor)
- [x] `/ingest/youtube` trigger endpoint
- [x] Tests with mock YouTube API

**Phase 2 milestone:** The agent tracks research, trends, and videos alongside your personal knowledge, and finds connections between them.

---

### Phase 3: Cross-Source Intelligence (sessions 9-11)

#### Session 9 — Discovery Engine

**Goal:** Automatically detect relationships across sources.

- [x] Embedding similarity scan: find pairs of artifacts with high cosine similarity across different sources
- [x] Topic co-occurrence: artifacts sharing auto-generated tags get `SIMILAR_TOPIC` edges
- [x] Author matching: same name/handle across GitHub, arXiv, Springer -> `AUTHORED_BY_SAME`
- [x] Citation matching: arXiv paper mentions a GitHub repo URL -> `IMPLEMENTS`
- [x] Trending + research: trending repo description matches paper abstract -> `IMPLEMENTS`
- [x] Run discovery as a periodic background job
- [x] `/discover` trigger endpoint to run on demand
- [x] Configurable similarity thresholds and confidence scoring
- [x] Tests with known cross-source relationships

#### Session 10 — OneDrive Syncer

**Goal:** Ingest documents from OneDrive.

- [x] Microsoft Graph API client (OAuth2 device code flow for personal accounts)
- [x] List files in configured OneDrive folders
- [x] Delta sync (Graph API delta queries)
- [x] Text extraction:
  - [x] `.md`, `.txt` files (direct read)
  - [x] `.docx` files (extract text)
  - [x] `.pdf` files (extract text)
- [x] Map to artifacts with OneDrive metadata
- [x] Generate embeddings on ingest
- [x] `/ingest/onedrive` trigger endpoint
- [x] Tests with mock Graph API

#### Session 11 — CLI Tool

**Goal:** Quick terminal access to your entire knowledge base.

- [x] `cmd/pa-cli/main.go` (cobra)
- [x] `pa ask "what's new in RAG research?"` -> calls `/ask`
- [x] `pa search "event sourcing"` -> calls `/search`
- [x] `pa ingest [source]` -> trigger one or all syncers
- [x] `pa trending` -> show this week's trending repos matching your interests
- [x] `pa papers --recent` -> show recently ingested papers
- [x] `pa related <artifact-id>` -> show graph neighbourhood
- [x] `pa status` -> sync status, artifact counts by source, embedding coverage
- [x] `pa tag <artifact-id> "architecture"` -> add personal tag
- [x] Pretty terminal output with source links and colour
- [x] Shell completion (cobra built-in: bash, zsh, fish, powershell)
- [x] `pa discover` -> trigger cross-source relationship discovery
- [x] New server endpoints: `GET /status`, `GET /artifacts`, `GET /artifacts/{id}/related`, `POST /artifacts/{id}/tags`
- [x] HTTP client with error handling and configurable server URL (`--server` flag, `PA_SERVER_URL` env)
- [x] Tests with mock HTTP server (18 tests)

**Phase 3 milestone:** Full cross-source intelligence — the agent connects your notes to papers to trending repos to videos and surfaces insights you'd never find manually.

---

### Phase 4: Enrichment & Polish (sessions 13+)

#### Session 12 — Auto-Tagging & Summaries
- [x] Auto-tagging service: given artifact content, suggest 3-5 tags (via Ollama)
- [x] Summary generation: one-paragraph summary per artifact
- [x] Run as on-demand enrichment job for newly ingested artifacts (`POST /enrich`)
- [x] Use summaries in search results for quick scanning
- [x] Filtered search by tags (`/search?tags=architecture,database`)
- [x] Batch processing for untagged/unsummarised artifacts
- [x] `pa enrich` CLI command
- [x] `pa search --tags` flag for tag-filtered search
- [x] Tests (10 tagging unit tests + 3 CLI tests)

#### Session 13 — Digest Engine & Temporal Queries

**Goal:** Generate periodic knowledge digests and support time-based queries over
your artifact history. This is the foundation that shifts the system from
"store and retrieve" to "synthesize and reflect."

- [x] **Temporal query infrastructure:**
  - [x] Date-range filtering on artifact queries (ingested_at / created_at windows)
  - [x] Natural language temporal parsing — support phrases like "last week",
        "in January", "the past 3 months", "before Christmas" (regex-based
        parser with support for relative keywords, named months, durations,
        exact dates, and "before" expressions)
  - [ ] `pa ask "what was I working on in March 2025?"` routes through temporal
        filter before RAG context assembly
  - [x] `/artifacts?from=&to=` query parameter support
- [x] **Digest generation service:**
  - [x] Accept a time window (day / week / month / custom range)
  - [x] Aggregate artifacts ingested in the window, grouped by source
  - [x] Summarise activity per source via LLM ("You ingested 8 papers, 3 repos,
        12 notes…")
  - [x] Collect cross-source relationships discovered in the window
  - [x] Compose a structured digest (sections: activity summary, new connections,
        top artifacts by source)
  - [x] LLM-generated narrative intro: "Here's what you worked on, learned, and
        saved this week"
- [x] **Configurable cadence:**
  - [x] Support daily, weekly, and monthly digest periods
  - [x] Config option for default cadence + day-of-week for weekly
  - [x] `pa digest` CLI command (defaults to weekly)
  - [x] `pa digest --period daily` / `pa digest --period monthly`
  - [x] `pa digest --from 2025-03-01 --to 2025-03-31` for custom ranges
  - [x] `pa digest --natural "last month"` (natural language)
- [x] **Output formats:**
  - [x] Pretty terminal report (default for CLI)
  - [x] Markdown file output (`pa digest --output weekly-2025-03-10.md`)
  - [x] JSON response from API (`GET /digest?period=weekly`)
- [x] **API endpoints:**
  - [x] `GET /digest?period=weekly|daily|monthly&from=&to=&natural=` — generate digest
  - [x] `GET /artifacts?from=&to=&source=` — temporal artifact listing
- [x] Tests: temporal parsing (42 test cases), digest service unit tests (10),
      CLI client tests (3), output format validation

#### Session 14 — Knowledge Insights

**Goal:** Surface non-obvious insights from your knowledge base — the features
that turn a digest from "here's a list of stuff" into "here's what you should
pay attention to." These are the building blocks for a future web UI / mobile
app dashboard.

- [x] **Forgotten gems resurfacing:**
  - [x] Find artifacts from 1-3 months ago that are semantically similar to
        this week's ingested artifacts but haven't been recalled/searched
  - [x] Temporal-filtered similarity search (recent embeddings vs older ones,
        excluding already-surfaced results)
  - [x] Include in digest as "You might want to revisit…" section
  - [x] `pa gems` CLI command for on-demand resurfacing
  - [x] `GET /insights/gems?lookback=90d` API endpoint
- [x] **Serendipity highlights:**
  - [x] Rank the most surprising cross-source connections discovered this period
  - [x] Score by: confidence × source-type diversity (a paper↔repo link scores
        higher than repo↔repo)
  - [x] Include top 3-5 in digest as "Unexpected connections" section
  - [x] `pa serendipity` CLI command
  - [x] `GET /insights/serendipity?period=weekly` API endpoint
- [x] **Interest momentum / topic drift:**
  - [x] Tag frequency histogram bucketed by week/month
  - [x] Compute momentum: compare this period's tag counts to previous period
  - [x] Surface "Topics gaining momentum" and "Topics cooling off" in digest
  - [x] `pa topics --trending` CLI command
  - [x] `GET /insights/topics?trending=true` API endpoint
- [x] **Knowledge depth map:**
  - [x] Per-topic depth score: count of artifacts × source diversity
  - [x] Classify as deep (many artifacts, 3+ sources), moderate, or shallow
  - [x] Surface in digest: "Deep coverage on X, only surface-level on Y"
  - [x] `pa depth` CLI command (table of topics with depth scores)
  - [x] `GET /insights/depth` API endpoint
- [x] **Learning velocity metrics:**
  - [x] Count artifacts by source and type per period
  - [x] Compare to previous period and rolling average
  - [x] One-liner in digest: "Heavy research week: 14 papers vs your usual 4"
  - [x] Include in `pa status` output as a velocity section
- [x] **"This time last month/year" memories:**
  - [x] Query artifacts from same calendar period in previous months/years
  - [x] Include in digest as "One month ago you were deep into X" section
  - [x] `pa memories` CLI command
  - [x] `GET /insights/memories?lookback=1month` API endpoint
- [x] **Integrate all insights into digest:**
  - [x] Digest service calls each insight provider and assembles sections
  - [x] Each insight is independently toggleable in config
  - [x] LLM generates a cohesive narrative weaving the insights together
- [x] Tests: insight scoring logic, edge cases (empty weeks, single-source
      topics), integration with digest service (14 tests)

#### Session 15 — Browser Bookmarks & RSS
- [ ] Chrome/Edge bookmarks importer
- [ ] OPML import for RSS feeds
- [ ] Feed polling on configurable schedule
- [ ] Readability extraction for article content

#### Session 16+ — Future Ideas
- [ ] **Research radar:** "Based on your interests, here are papers/repos you should look at"
- [ ] **Obsidian plugin:** bidirectional sync with Obsidian vault
- [ ] **Web UI:** simple local dashboard (htmx or similar) for browsing the knowledge graph
- [ ] **Mobile app:** digest + insights as a daily briefing on your phone
- [ ] **Hacker News integration:** track top stories matching your topics
- [ ] **Export:** generate a knowledge report / mind map from your graph
- [ ] **Notification hooks:** alert when something trending relates to your active projects
- [ ] **Additional scrapers:** scrape additional websites like https://thenewstack.io/, https://dev.to/, etc.

---

## Cross-Cutting Concerns

### Observability (lightweight for personal use)
- [ ] slog structured logging
- [ ] `/status` endpoint: artifact counts, sync timestamps, embedding coverage
- [ ] CLI `pa status` command

### Privacy
- [ ] All data stays local (PostgreSQL on your machine)
- [ ] Ollama as primary LLM — nothing leaves your machine by default
- [ ] OpenAI is opt-in; only sends text for embedding/RAG context
- [ ] No telemetry, no analytics

### Rate Limiting & Politeness
- [ ] Respect rate limits on all APIs (GitHub, arXiv, Springer, YouTube)
- [ ] Configurable polling intervals per source
- [ ] Exponential backoff on failures
- [ ] User-Agent headers identifying the tool

### Configuration
```yaml
sources:
  filesystem:
    enabled: true
    paths:
      - ~/notes
      - ~/Documents/tech
    extensions: [".md", ".txt"]

  github:
    enabled: true
    token: ${GITHUB_TOKEN}
    sync_starred: true
    sync_gists: true

  onedrive:
    enabled: false
    folders: ["/Documents/Engineering"]

  arxiv:
    enabled: true
    categories: ["cs.AI", "cs.SE", "cs.DC"]
    keywords: ["knowledge graph", "RAG", "embeddings", "LLM agents"]
    poll_interval: 24h

  springer:
    enabled: true
    api_key: ${SPRINGER_API_KEY}
    subjects: ["Computer Science"]
    keywords: ["software architecture", "microservices"]
    poll_interval: 24h

  trending:
    enabled: true
    languages: ["Go", "Python", "Rust", "TypeScript"]
    poll_interval: 12h

  youtube:
    enabled: true
    api_key: ${YOUTUBE_API_KEY}
    channels: ["UCVHFbqXqoYvEWM1Ddxl0QDg"]  # example: CNCF
    search_terms: ["Go programming", "distributed systems", "AI agents"]
    poll_interval: 24h

llm:
  provider: ollama
  ollama:
    base_url: http://localhost:11434
    embedding_model: nomic-embed-text
    chat_model: llama3.1
  openai:
    api_key: ${OPENAI_API_KEY}
    embedding_model: text-embedding-3-small
    chat_model: gpt-4o
```

---

## Progress Tracker

| Phase | Session | Focus | Status | Date |
|---|---|---|---|---|
| 1 | 1 | Scaffolding & Schema | Done | 2026-03-13 |
| 1 | 2 | LLM Provider Layer & Embeddings | Done | 2026-03-13 |
| 1 | 3 | Filesystem Syncer | Done | 2026-03-13 |
| 1 | 4 | GitHub Syncer | Done | 2026-03-13 |
| 1 | 5 | RAG Query Endpoint | Done | 2026-03-13 |
| 2 | 6 | arXiv Syncer | Done | 2026-03-13 |
| 2 | 7 | GitHub Trending Scraper | Done | 2026-03-13 |
| 2 | 8 | YouTube Syncer | Done | 2026-03-13 |
| 3 | 9 | Discovery Engine | Done | 2026-03-13 |
| 3 | 10 | OneDrive Syncer | Done | 2026-03-13 |
| 3 | 11 | CLI Tool | Done | 2026-03-13 |
| 4 | 12 | Auto-Tagging & Summaries | Done | 2026-03-13 |
| 4 | 13 | Digest Engine & Temporal Queries | Done | 2026-03-13 |
| 4 | 14 | Knowledge Insights | Done | 2026-03-13 |
| 4 | 15 | Bookmarks & RSS | Not started | |
| 4 | 16+ | Future Ideas | Not started | |