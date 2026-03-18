VISION INGESTION ARCHITECTURE DIAGRAM
=====================================

┌─────────────────────────────────────────────────────────────────────────────────┐
│                                  EXTERNAL SYSTEMS                               │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────────────────┐   │
│  │   Vision LLM     │  │  Embedding Model │  │      PostgreSQL DB           │   │
│  │   (OpenAI/Ollama)│  │   (Embeddings)   │  │  • artifacts table           │   │
│  │                  │  │                  │  │  • artifact_embeddings       │   │
│  └────────┬─────────┘  └────────┬─────────┘  │  • relationships             │   │
│           │                     │            └───────────────┬──────────────┘   │
└───────────┼─────────────────────┼────────────────────────────┼──────────────────┘
            │                     │                            │
            ▲                     ▲                            ▲
            │                     │                            │
        Caption                Embeddings                    Store/Query
        Request                Request                       Artifacts
            │                     │                            │
            │                     │                            │
┌───────────┴─────────────────────┴────────────────────────────┴──────────────────┐
│                          VISION INGESTION LAYER                                 │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────────┐ │
│  │                         FilesystemSyncer                                   │ │
│  │  • Scans configured directories recursively                                │ │
│  │  • Filters by extensions (.jpg, .png, etc.)                                │ │
│  │  • Coordinates overall sync orchestration                                  │ │
│  │                                                                            │ │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │ │
│  │  │ Sync() → scanDirectory() → processAndCount() → processImage()       │   │ │
│  │  │                                                                     │   │ │
│  │  │  processImage() flow:                                               │   │ │
│  │  │    1. extractMetadata()   ─┐                                        │   │ │
│  │  │    2. generateCaption()  ──┼─→ generate AI caption                  │   │ │
│  │  │    3. checkDeduplication() │   (via VisionService)                  │   │ │
│  │  │    4. storeArtifact()      │   (if not already indexed)             │   │ │
│  │  │    5. generateEmbedding()  │   (via EmbeddingService)               │   │ │
│  │  │    6. updateMetrics()      └─                                       │   │ │
│  │  └─────────────────────────────────────────────────────────────────────┘   │ │
│  │                                                                            │ │
│  └────────────────────────────────────────────────────────────────────────────┘ │
│                                   ▲                                             │
│                                   │                                             │
│  ┌────────────────────────────────┴──────────────────────────────────────────┐  │
│  │                        VisionService                                      │  │
│  │                                                                           │  │
│  │  Caption(imagePath, prompt)                                               │  │
│  │    └─ reads image → base64 encode → POST to VisionProvider                │  │
│  │                                                                           │  │
│  │  ExtractMetadata(imagePath)                                               │  │
│  │    ├─ dimensions: image.Decode () → Width, Height                         │  │
│  │    └─ EXIF: goexif library → camera, lens, ISO, date, etc.                │  │
│  │                                                                           │  │
│  └───────────────────────────────────────────────────────────────────────────┘  │
│                                                                                 │
│  ┌────────────────────────────────────────────────────────────────────────────┐ │
│  │                    EmbeddingService                                        │ │
│  │   (from internal/retrieval)                                                │ │
│  │                                                                            │ │
│  │  GenerateEmbeddings(artifactID, content)                                   │ │
│  │    └─ calls EmbeddingProvider.Embed() → vector embeddings                  │ │
│  │       stores in artifact_embeddings table                                  │ │
│  │                                                                            │ │
│  └────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
            │                                                  └─────────────────┐
            │                                                                    │
            ▼                                                                    ▼
    ┌──────────────────────┐                      ┌───────────────────────────────┐
    │    JobManager        │                      │    HTTP API Layer             │
    │  (async processor)   │                      │ (internal/api/vision.go)      │
    │                      │                      │                               │
    │  • Job submission    │◄─────────────────────┤ POST /vision/ingest           │
    │  • Worker pool       │                      │                               │
    │  • Status tracking   │                      │ Delegates to FilesystemSyncer │
    │  • Result retrieval  │                      │                               │
    │                      │                      └───────────────────────────────┘
    └──────────────────────┘
            │
            │ processes jobs in queue
            │
            └──────────────────────────────────────────────────────┐
                                                                   │
                                                                   ▼
                                               FilesystemSyncer.Sync(ctx)
