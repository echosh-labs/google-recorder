# 🎙️ Google Recorder Studio & Standalone API / MCP Server
> A high-performance Go REST API, Next.js web studio, and Model Context Protocol (MCP) bridge for streaming, searching, downloading, and synchronizing voice recordings from Google Recorder (`recorder.google.com`).

---

## 🌟 The Vibe
`google-recorder` unlocks your mobile voice memos. If you use a Google Pixel phone or the Google Recorder app to capture spoken thoughts, meetings, or creative narrations, this service turns that raw audio into a fully indexed, diarized local audio vault. It features full-text search across spoken transcripts, streaming audio playback, automatic synchronization, and an MCP server that lets AI assistants listen to or read your voice notes directly.

---

## 🚀 60-Second Quickstart

```bash
# 1. Clone the repository
git clone https://github.com/echosh-labs/google-recorder.git
cd google-recorder

# 2. Extract your Google Recorder session cookies (One-time setup)
./run.sh auth --chrome

# 3. Launch the full interactive Studio (Backend API + Next.js UI)
./run.sh
```

Once running:
- **Interactive Web Studio**: Visit [http://localhost:3000](http://localhost:3000)
- **Headless REST API**: Available on Port 8091 (or 8080)
- **MCP Endpoint**: `http://localhost:8091/mcp`

---

## 🎮 Operational Modes

### Mode 1: Full-Stack Interactive Studio
```bash
./run.sh
```
Compiles the Go backend and launches the Next.js visual dashboard on [http://localhost:3000](http://localhost:3000) with waveform playback, speaker labeling, and instant search.

### Mode 2: Headless Microservice Daemon (Port 8091)
Run as a background REST API service alongside companion engines or systemd:
```bash
./run.sh server --port 8091
```

### Mode 3: Model Context Protocol (MCP) for AI Agents
Connect Antigravity, Claude Desktop, or VS Code Copilot to query your voice memos directly:
```bash
# Run stdio MCP server:
./run.sh mcp
```

#### MCP Client Configuration
Add to your client configuration (e.g. `~/.gemini/antigravity/mcp_config.json` or `claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "google-recorder": {
      "command": "/path/to/google-recorder/backend/bin/recorder-server",
      "args": ["mcp"]
    }
  }
}
```

Or connect over Streamable HTTP if the server daemon is active:
```json
{
  "mcpServers": {
    "google-recorder": {
      "serverUrl": "http://localhost:8091/mcp"
    }
  }
}
```

### Mode 4: Automated CLI Backup & Sync
Download audio (`.m4a`) and diarized transcripts (`.json`, `.txt`) directly to your filesystem:
```bash
# Synchronize all recordings to local folder:
./run.sh sync --dir ~/recordings --all

# Download only the 10 most recent voice notes:
./run.sh sync --dir ~/recordings --limit 10

# Download a specific recording by ID:
./run.sh download <recording-id> --dir ~/recordings
```

---

## 🔐 Authentication & Session Setup

Google Recorder uses cookie-based session verification (`SAPISIDHASH` via SHA-1 over origin `https://recorder.google.com`). 

### Easy One-Click Setup
1. Open Google Chrome and make sure you are logged into [recorder.google.com](https://recorder.google.com).
2. In your terminal, run:
   ```bash
   ./run.sh auth --chrome
   ```
3. The script extracts your session cookies and securely saves them to `~/.config/google-recorder/auth.json` (chmod `0600`).
4. To test authentication status:
   ```bash
   ./run.sh list --limit 5
   ```

---

## 📡 REST API Reference

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/v1/recordings` | `GET` | List available recordings with duration, date, and title. |
| `/api/v1/recordings/{id}` | `GET` | Retrieve full recording details including diarized speaker segments. |
| `/api/v1/recordings/{id}/audio` | `GET` | Stream recording audio with HTTP 206 partial range seeking. |
| `/api/v1/recordings/{id}/transcript` | `GET` | Get clean text transcript of the recording. |
| `/api/v1/sync` | `POST` | Trigger background synchronization of new recordings to disk. |
| `/mcp` | `POST` | Streamable Model Context Protocol JSON-RPC 2.0 endpoint. |

---

## 🏗️ Architecture

```
google-recorder/
├── backend/                  # Go REST API, Sync Engine & MCP Server
│   ├── cmd/server/           # CLI entrypoints (server, mcp, sync, download, auth)
│   ├── pkg/recorder/         # Google RPC protocol client, SAPISID auth, stream pipe
│   └── internal/
│       ├── api/              # HTTP router, CORS middleware, and audio streaming
│       └── mcp/              # MCP protocol handler for autonomous agent pairing
├── frontend/                 # Next.js 15 Web Dashboard
│   ├── src/app/              # Audio player, transcript viewer, and keyword search UI
│   └── src/lib/              # IndexedDB cache & offline full-text search
└── run.sh                    # Unified launcher script for all operational modes
```

---

## 📄 License

This software is dual-licensed:
- **Open Source Edition**: Governed by the [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE.md) for individual, educational, and open-source usage.
- **Commercial & Enterprise Edition**: Requires a commercial license from echoSH labs for proprietary integration, corporate deployment, or advanced enterprise features. See [COMMERCIAL.md](COMMERCIAL.md) or visit [echosh-labs.com](https://echosh-labs.com).

For commercial licensing inquiries, contact [justin@echosh-labs.com](mailto:justin@echosh-labs.com).
