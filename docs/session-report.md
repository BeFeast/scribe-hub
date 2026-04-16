# scribe-hub — Session Report

## What was done

### 1. Repository initialization
- Created `CLAUDE.md` with build commands, architecture overview, key types, API routes, and configuration reference.

### 2. Documentation
- **`README.md`** — full project README covering prerequisites, quick start, configuration table, transcription pipeline, API reference, MCP integration (Claude Code, Codex CLI, OpenCode, OpenClaw), example workflows, and architecture overview.
- **`docs/PRD.md`** — product requirements document with 18 improvements across P0–P4 priorities (persistence, graceful shutdown, cancellation, webhooks, structured logging, metrics, Docker, WebSocket live output, web UI, etc.).
- **`docs/quickstart.md`** — quickstart guide covering local dev, remote deployment (systemd + launchd), and MCP/OpenClaw setup for all supported CLI tools.

### 3. Vendored transcription engine
Moved the transcription pipeline from the external `video-summary` repo into scribe-hub:

| File | Purpose |
|------|---------|
| `scripts/video-transcript-summary-faster-whisper.sh` | Bash orchestrator: yt-dlp download → ffmpeg normalize → faster-whisper transcribe → gemini summarize |
| `tools/faster-whisper-transcribe.py` | Python transcription via faster-whisper (run with `uv run`) |
| `tools/gemini-summarize.py` | Python summarization via Gemini API (run with `uv run`) |
| `tools/prompts/transcript-summary-gemini.md` | Gemini prompt template |
| `tools/prompts/transcript-summary-opencode.md` | OpenCode prompt template |

### 4. Hardcoded path removal
- **`cmd/scribe-hub/main.go`** — replaced hardcoded `$HOME/Projects/video-summary/scripts/...` with `defaultScriptPath()` that checks `SCRIBE_SCRIPT_PATH` env var → binary-relative path → `./scripts/...` fallback.
- **`internal/transcriber/handler.go`** — removed macOS-specific `PATH` override (`/Users/god/.local/bin` etc.). Now inherits system `PATH`, with optional `SCRIBE_EXTRA_PATH` prepend.

### 5. MCP server
Created `cmd/mcp-server/main.go` — a stdio-based MCP server using `github.com/mark3labs/mcp-go` that proxies to the scribe-hub HTTP API.

**Tools exposed:**

| Tool | HTTP call |
|------|-----------|
| `transcribe` | `POST /transcribe` |
| `job_status` | `GET /status/{id}` |
| `list_jobs` | `GET /jobs?status=...` |
| `queue` | `GET /queue` |

**Config files created:**
- `.mcp.json` — Claude Code
- `.codex/config.toml` — Codex CLI
- `opencode.json` — OpenCode

### 6. OpenClaw integration
- **Skill** (`docs/openclaw-skill/SKILL.md`) — markdown skill that teaches OpenClaw's agent to call scribe-hub's HTTP API via `web_fetch`. No code needed.
- **MCP config** documented in quickstart for `~/.openclaw/openclaw.json`.

### 7. Merge & conflict resolution
- Merged `origin/main` (31 commits ahead) into the feature branch.
- Resolved README.md conflict in the prerequisites section (kept comprehensive list, incorporated Chrome cookies note from main).

---

## Current branch

```
claude/init-project-wJEyZ
```

### Commit history (feature branch)

```
1e439f4 Add OpenClaw integration (skill + MCP config)
42c0e33 Add quickstart guide with local dev, deployment, and MCP setup
5d75885 Merge origin/main into feature branch, resolve README conflict
fecbaf7 Add .gitignore for compiled binaries
a2b974b Vendor transcription engine and add MCP server for CLI integration
```

---

## What to test locally

### Prerequisites

```bash
brew install yt-dlp ffmpeg python@3.12
curl -LsSf https://astral.sh/uv/install.sh | sh
```

### Build

```bash
go build ./cmd/scribe-hub
go build ./cmd/mcp-server
go vet ./...
```

### Run scribe-hub

```bash
# Without summarization
./scribe-hub -port 8080

# With summarization
GEMINI_API_KEY=your-key ./scribe-hub -port 8080
```

### Smoke test

```bash
# Health check
curl http://localhost:8080/health

# Submit a short video (transcript only)
curl -X POST http://localhost:8080/transcribe \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://www.youtube.com/watch?v=jNQXAC9IVRw", "skip_summary": true}'

# Poll status (replace <id> with the returned job ID)
curl http://localhost:8080/status/<id> | jq

# Check queue
curl http://localhost:8080/queue | jq
```

### Test MCP server

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | SCRIBE_HUB_URL=http://localhost:8080 go run ./cmd/mcp-server
```

### Test from Claude Code

1. Open the scribe-hub project directory
2. Run `/mcp` to verify the server is connected
3. Ask Claude to "transcribe https://www.youtube.com/watch?v=jNQXAC9IVRw with skip_summary"
4. Ask Claude to "show the queue" or "check status of job <id>"

### Test OpenClaw skill

```bash
mkdir -p ~/.openclaw/workspace/skills/scribe-hub
cp docs/openclaw-skill/SKILL.md ~/.openclaw/workspace/skills/scribe-hub/SKILL.md
```

---

## What's next (from PRD.md)

**P0 — Reliability:**
1. Job persistence (SQLite/BoltDB)
2. Graceful shutdown (drain queue on SIGTERM)

**P1 — Usability:**
3. Structured error responses (code/message/hint/retryable)
4. Job cancellation (`DELETE /jobs/{id}`)
5. Webhook callbacks
6. Duplicate URL detection
7. Title fetch timeout

**P2 — Observability:**
8. Structured logging (`slog`)
9. Prometheus metrics endpoint
10. Job history pagination + cleanup

See `docs/PRD.md` for the full list through P4.
