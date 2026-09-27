# Google Recorder Studio & Standalone API / MCP Server

A high-performance, full-stack service and automation toolkit for managing, streaming, searching, downloading, and synchronizing recordings and transcripts from Google Recorder (`recorder.google.com`).

Operates in three modes:
1. **Full-Stack Studio**: Interactive Next.js web dashboard + Go backend for browsing, streaming, and full-text searching transcripts.
2. **Standalone REST API**: Headless HTTP daemon designed to run alongside external pipelines, worker jobs, or companion applications.
3. **Model Context Protocol (MCP) Server**: Exposes Google Recorder tools and resources over Streamable HTTP (`/mcp`) and standard I/O (`mcp` subcommand) for direct pairing with Antigravity, Claude, and code agents.

---

## Architecture Overview

```
google-recorder/
├── backend/            # High-performance Go REST API, Sync Engine & MCP Server
│   ├── cmd/server/     # CLI entrypoints (server, mcp, sync, download, list, auth)
│   ├── pkg/recorder/   # Google RPC client, auth tokens, stream & sync engines
│   └── internal/
│       ├── api/        # REST API endpoints & CORS middleware
│       └── mcp/        # Model Context Protocol (JSON-RPC 2.0, HTTP & Stdio)
├── frontend/           # Modern Next.js (React + Tailwind + Lucide) Dashboard
│   ├── src/app/        # Interactive player, transcript viewer, and search UI
│   └── src/lib/        # IndexedDB offline storage & full-text indexing engine
├── run.sh              # Unified CLI launcher for all operational modes
└── .gitignore          # Hygiene configuration
```

---

## Operational Modes

### 1. Launch Full Studio (Backend API + Web Dashboard)
```bash
./run.sh
```
Builds the Go service and starts both the API server and Next.js frontend on `http://localhost:3000`.

### 2. Standalone REST API & MCP Server
Run headless alongside other programs without launching the frontend:
```bash
# Default port 8080 (auto-increments if port is in use):
./run.sh server

# Or bind to a specific port:
./run.sh server --port 8090
```

### 3. Model Context Protocol (MCP) Server
Run as a native stdio MCP server for agentic IDEs, Claude Desktop, or Antigravity:
```bash
./run.sh mcp
```
*(All diagnostic logs are redirected to stderr; stdout is reserved strictly for JSON-RPC 2.0 messages).*

#### Antigravity / Claude MCP Configuration
Add to `~/.gemini/antigravity/mcp_config.json` or `~/.gemini/config/mcp_config.json`:

```json
{
  "mcpServers": {
    "google-recorder": {
      "command": "/home/justin/code/echosh-labs/google-recorder/backend/bin/recorder-server",
      "args": ["mcp"]
    }
  }
}
```

Or connect over Streamable HTTP if the server is already running:
```json
{
  "mcpServers": {
    "google-recorder": {
      "serverUrl": "http://localhost:8080/mcp"
    }
  }
}
```

### 4. Direct CLI Sync & Downloads
Synchronize recordings directly to disk from the command line:
```bash
# Sync entire history to a target folder (skips already downloaded recordings):
./run.sh sync --dir ~/recordings --all

# Sync only 10 most recent recordings with audio and transcripts:
./run.sh sync --dir ~/recordings --limit 10

# Download a single recording by UUID:
./run.sh download <recording-id> --dir ~/recordings

# Quick terminal listing:
./run.sh list --limit 20
```

---

## Synchronization & File Organization

Files are saved **side-by-side in a separate fashion (NOT a zip file)**, making them immediately accessible to audio players, LLM ingestion pipelines, and search indexers:

```
recordings/
├── 2026-09-07_100123_Sep_7_at_1001_AM_96b5fc02.m4a   # Original AAC/MP4 audio stream
├── 2026-09-07_100123_Sep_7_at_1001_AM_96b5fc02.txt   # Speaker-diarized transcript with timestamps
├── 2026-09-07_100123_Sep_7_at_1001_AM_96b5fc02.json  # Complete metadata & word-level segments
└── manifest.json                                      # Incremental sync tracking manifest
```

### Incremental Synchronization
The sync engine maintains a deterministic `manifest.json` in the target directory. On subsequent sync executions, recordings already saved on disk are skipped instantly without re-downloading audio streams. Pass `--force` to re-download.

---

## REST API Reference

### Health & Auth
* `GET  /api/v1/health`: Service health and authentication validity.
* `GET  /api/v1/auth/status`: Inspect active Google account index and session credentials.
* `POST /api/v1/auth/refresh`: Headless auto-refresh from local browser profiles (Firefox, Chrome).
* `POST /api/v1/auth/login`: Launch browser to complete Google account authentication.

### Recordings
* `GET  /api/v1/recordings?all=true&limit=50&query=meeting`: List recordings with reverse-chronological pagination.
* `GET  /api/v1/recordings/{id}`: Single recording metadata.
* `GET  /api/v1/recordings/{id}/transcript?format=text|json`: Speaker-diarized transcript.
* `GET  /api/v1/recordings/{id}/audio`: Zero-allocation audio streaming with HTTP `Range` seek support.

### Downloads & Syncing
* `POST /api/v1/sync`: Trigger library synchronization to local directory.
  ```json
  {
    "output_dir": "/path/to/recordings",
    "download_audio": true,
    "download_transcript": true,
    "transcript_format": "both",
    "download_metadata": true,
    "limit": 0,
    "all": true,
    "force": false,
    "async": false
  }
  ```
* `GET  /api/v1/sync/status?dir=/path/to/recordings`: Inspect active sync progress or manifest details.
* `POST /api/v1/sync/cancel`: Cancel an active background synchronization.
* `POST /api/v1/recordings/{id}/download`: Download an individual recording to a local folder.
* `GET  /api/v1/recordings/{id}/export/audio`: Direct stream download with `Content-Disposition: attachment`.
* `GET  /api/v1/recordings/{id}/export/transcript?format=text|json`: Direct transcript file download.

---

## Model Context Protocol (MCP) Tools

When connected via Streamable HTTP (`/mcp`) or stdio (`mcp`), the following tools are available:

| Tool Name | Parameters | Description |
| :--- | :--- | :--- |
| `list_recordings` | `limit`, `query`, `all`, `oldest_first` | Query recordings with metadata, duration, timestamps, and IDs. |
| `get_recording` | `recording_id` | Retrieve complete metadata for a specific recording. |
| `get_transcript` | `recording_id`, `format` (`text` \| `json`) | Fetch speaker-diarized text or millisecond-timestamped JSON. |
| `download_recording` | `recording_id`, `output_dir`, `download_audio`, `download_transcript`, `transcript_format`, `download_metadata`, `force` | Download an individual recording's files side-by-side to disk. |
| `sync_recordings` | `output_dir`, `download_audio`, `download_transcript`, `transcript_format`, `download_metadata`, `limit`, `all`, `query`, `force` | Synchronize recordings into a local folder, skipping already downloaded items via `manifest.json`. |
| `get_sync_status` | `output_dir` | Check running sync progress or inspect local folder manifest counts. |
| `auth_status` | *(none)* | Inspect session validity, account index, and token state. |
| `auth_refresh` | *(none)* | Auto-extract fresh cookies from local browser profiles. |

---

## Authentication & Self-Healing Sessions

1. **Auto-Extraction from Browser Profiles:**
   * Cookies are automatically extracted from your local browser (Firefox or Chrome), cleaned to eliminate cross-origin conflicts, and tested against Google's RPC service.
   * Probes account indices (`authUser`) automatically to find the active Recorder profile.
   * Stored securely at `~/.config/google-recorder/auth.json` (outside the repo).

2. **Self-Healing Silent Re-Authentication:**
   * If Google returns `401 Unauthorized` (RPC) or `404 Not Found` (expired audio tokens), the Go client triggers a silent background refresh from the local browser profile and retries the request seamlessly.

3. **CLI Auth Commands:**
   ```bash
   # Test existing credentials:
   ./run.sh auth --check

   # Refresh fresh cookies from browser profile:
   ./run.sh auth --refresh
   ```
