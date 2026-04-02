# Second Brain Web Interface

A modern, clean web UI for the Second Brain knowledge base system. Built with native HTML, CSS, and JavaScript—no frameworks, no build tools.

## Features

### 💬 Chat Interface (Main Feature)
- AI-powered chat window similar to GPT
- Ask questions about your knowledge base
- Get AI-generated answers with source citations
- Adjust search depth with configurable Top-K results
- Real-time streaming responses

### 📊 Status Dashboard
- **Artifacts**: Total count and breakdown by source (filesystem, GitHub, ArXiv, YouTube, OneDrive, etc.)
- **Embeddings**: Coverage percentage with visual progress bar
- **Relationships**: Total count and breakdown by relationship type
- **By Type**: Distribution of artifacts by type (documents, commits, papers, notes, photos, etc.)
- **Last Sync**: Recent sync status for each ingestion source
- Auto-refreshes every 30 seconds when viewing the status tab

### 📥 Ingestion Control
- One-click ingestion triggers for each source:
  - **Filesystem**: Local files and directories
  - **GitHub**: Repositories, commits, pull requests
  - **ArXiv**: Research papers
  - **YouTube**: Videos and transcripts
  - **Trending**: GitHub trending repositories
  - **OneDrive**: Cloud documents
  - **The New Stack**: Tech news articles
  - **Vision**: Image analysis and captions
- Visual feedback on ingestion status
- Background processing—ingestion continues while you work

### 🔍 Search & Discovery
- Full-text and semantic search across your knowledge base
- Browse search results with source information
- View artifact types and metadata

## Architecture

### Directory Structure
```
cmd/pa/web/
├── index.html          # Main HTML entry point
├── css/
│   └── styles.css      # All styling (dark/light theme)
└── js/
    ├── theme.js        # Theme toggle & localStorage persistence
    ├── app.js          # Main app logic & tab management
    ├── chat.js         # Chat interface & AI interaction
    └── status.js       # Status dashboard, ingest, search
```

### Design Principles
- **Pure HTML/CSS/JS**: No React, Vue, Angular, or build tools
- **Self-contained**: All files are static and deployable to any CDN
- **Responsive**: Works on desktop, tablet, and mobile
- **Accessible**: Semantic HTML, keyboard navigation
- **Dark/Light Theme**: Automatic detection with manual toggle in localStorage

## Usage

### Starting the Server
```bash
cd cmd/pa
go run main.go
```

The web interface will be available at `http://localhost:8080` (or configured port).

### API Endpoints

The backend exposes a RESTful API with both versioned and legacy endpoints:

#### Versioned API (v1) - Recommended
```
GET  /api/v1/health                    # Server health check
GET  /api/v1/status                    # KB statistics & sync status
GET  /api/v1/search?q=...              # Search artifacts
POST /api/v1/ask                       # RAG ask (legacy format)
POST /api/v1/chat/ask                  # Modern chat endpoint
POST /api/v1/ingest/{source}           # Trigger ingestion
GET  /api/v1/artifacts                 # List artifacts
GET  /api/v1/artifacts/{id}/related    # Get related artifacts
POST /api/v1/artifacts/{id}/tags       # Add tags
GET  /api/v1/digest                    # Get periodic digest
GET  /api/v1/insights/*                # Various insights
```

#### Chat Endpoint (Recommended)
```bash
curl -X POST http://localhost:8080/api/v1/chat/ask \
  -H "Content-Type: application/json" \
  -d '{
    "question": "What projects am I working on?",
    "top_k": 5
  }'
```

Response:
```json
{
  "answer": "Based on your knowledge base, you are working on several projects...",
  "sources": [
    "kwatch - Kubernetes watcher",
    "tempam - Temperature monitoring",
    "second-brain - Knowledge management"
  ]
}
```

#### Legacy Endpoints (Still Supported)
All endpoints work without the `/api/v1` prefix for backward compatibility with existing clients.

## Theme System

The UI includes automatic dark/light mode detection with manual toggle:

1. **Detection Order**:
   - First: Check localStorage for saved preference
   - Second: Check system `prefers-color-scheme`
   - Default: Light mode

2. **CSS Variables**:
   - Primary/secondary/tertiary backgrounds
   - Text colors (primary/secondary/tertiary)
   - Accent colors with hover states
   - Border and shadow definitions

3. **Toggle Button**: Click the sun/moon icon in the header to switch

## Keyboard Shortcuts

- **Ctrl/Cmd + Enter**: Send chat message
- **Tab**: Navigate between elements
- **Enter**: Activate buttons/submit forms

## Performance Notes

- **Static Assets**: Cached for 24 hours after first load
- **HTML**: Cached for 1 hour (allows updates without redeploy)
- **Status Auto-Refresh**: Only active when viewing the Status tab (30-second interval)
- **Responsive Images**: Uses CSS media queries, no JS required

## Browser Support

- Modern browsers with ES6 support:
  - Chrome 51+
  - Firefox 54+
  - Safari 10+
  - Edge 15+

## Development

### No Build Step Required
Just edit the files directly:
- `cmd/pa/web/index.html` - Structure
- `cmd/pa/web/css/styles.css` - Styling
- `cmd/pa/web/js/*.js` - Interactions

Changes are immediately available when you run the server.

### Adding New Pages
1. Create a new section in `index.html` with `id="{name}-tab"`
2. Add a navigation button with `data-tab="{name}"`
3. Create corresponding JS file in `js/` directory
4. The TabManager will automatically wire it up

### ThemeJS
All custom styles use CSS variables defined in `css/styles.css`:
```css
--bg-primary: color for main background
--text-primary: color for main text
--accent: primary action color
--border-color: for borders and dividers
```

## API Integration Details

### Status Response Format
```json
{
  "artifacts": {
    "total": 5658,
    "by_source": {
      "filesystem": 3499,
      "github": 1412,
      "arxiv": 400,
      ...
    },
    "by_type": {
      "document": 3258,
      "commit": 1288,
      ...
    }
  },
  "embeddings": {
    "total": 4802,
    "coverage": 84.9
  },
  "relationships": {
    "total": 76,
    "by_type": {
      "REFERENCES": 76
    }
  },
  "sync_cursors": [
    {
      "source_name": "arxiv:sync",
      "cursor_value": "...",
      "updated_at": "2026-03-31T17:26:23Z"
    }
  ]
}
```

### Ingest Response
```json
{
  "status": "started",
  "message": "Ingestion started in background"
}
```

## Future Enhancements

- Conversation history with persistence
- Advanced filtering and faceted search
- Custom tags and organization
- Batch operations
- Real-time notifications
- Export/import functionality
- Multi-user support
