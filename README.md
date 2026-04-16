# scribe-hub

HTTP service that queues and runs video transcription jobs using [faster-whisper](https://github.com/SYSTRAN/faster-whisper). Submit a YouTube (or any yt-dlp-supported) URL, and scribe-hub will transcribe it in the background, optionally generating an AI summary via Gemini and saving the result to your Obsidian vault.

Jobs are processed one at a time through a serial queue, so long-running transcriptions don't compete for GPU/CPU resources.

## Prerequisites

- **Go 1.25+**
- **[yt-dlp](https://github.com/yt-dlp/yt-dlp)** -- downloads audio from YouTube and other video sites
- **[ffmpeg](https://ffmpeg.org/)** -- normalizes audio to 16kHz WAV before transcription
- **[uv](https://docs.astral.sh/uv/)** -- runs the Python transcription and summarization scripts
- **Python 3.10+** -- required by the Python scripts (managed by uv)
- **`GEMINI_API_KEY`** env var -- required for AI summarization (not needed with `--skip-summary`)

## Quick start

```bash
# Build
go build -o scribe-hub ./cmd/scribe-hub

# Run on default port 18810
./scribe-hub

# Run on a custom port
./scribe-hub -port 8080
```

## Configuration

| Setting | Source | Default |
|---------|--------|---------|
| HTTP port | `-port` flag | `18810` |
| Transcription script | `SCRIBE_SCRIPT_PATH` env var | auto-resolved from binary dir or `./scripts/video-transcript-summary-faster-whisper.sh` |
| Extra PATH dirs | `SCRIBE_EXTRA_PATH` env var | _(none — inherits system PATH)_ |
| Output folder | `OBSIDIAN_FOLDER` env var | `$HOME/Documents/Digests` |
| Gemini API key | `GEMINI_API_KEY` env var | _(required for summarization)_ |
| Log file | hardcoded | `$HOME/logs/scribe-hub.log` |

## Transcription pipeline

The bundled scripts handle the full transcription workflow:

1. **Download** — `yt-dlp` extracts audio from the video URL
2. **Normalize** — `ffmpeg` converts to 16kHz mono WAV
3. **Transcribe** — `faster-whisper` (via `tools/faster-whisper-transcribe.py`) produces a transcript markdown file
4. **Summarize** — Gemini API (via `tools/gemini-summarize.py`) generates a Russian-language analytical summary (skippable with `--skip-summary`)
5. **Link** — frontmatter is post-processed to cross-link transcript and summary files

## API

All responses are JSON.

### Submit a transcription

```
POST /transcribe
```

```json
{
  "url": "https://www.youtube.com/watch?v=example",
  "skip_summary": false,
  "language": "en",
  "model_size": "large-v3"
}
```

Only `url` is required. Returns `202 Accepted` with the job object including queue position.

### Check job status

```
GET /status/{id}
```

Returns the job with current status (`queued`, `running`, `done`, `failed`), queue position, and output when complete.

### List jobs

```
GET /jobs
GET /jobs?status=queued,running
```

Returns all jobs, optionally filtered by comma-separated status values.

### View queue

```
GET /queue
```

Returns the currently running job and all queued jobs with their positions.

### Health check

```
GET /health
```

Returns `{"status": "ok"}`.

## CLI integration (MCP)

scribe-hub includes an MCP (Model Context Protocol) server so you can submit transcriptions and check status directly from AI coding tools.

### Setup

**Claude Code** — config is already in `.mcp.json`. Run `/mcp` to verify the server is connected.

**Codex CLI** — config is in `.codex/config.toml`. The server registers automatically.

**OpenCode** — config is in `opencode.json`. The server registers automatically.

### MCP tools

| Tool | Description |
|------|-------------|
| `transcribe` | Submit a URL for transcription |
| `job_status` | Check status of a job by ID |
| `list_jobs` | List jobs with optional status filter |
| `queue` | Show running + queued jobs |

The MCP server connects to the running scribe-hub HTTP API. Set `SCRIBE_HUB_URL` env var if scribe-hub runs on a non-default address (default: `http://localhost:18810`).

## Example workflow

```bash
# Submit a video
curl -X POST http://localhost:18810/transcribe \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}'

# Check status (use the id from the response above)
curl http://localhost:18810/status/1713180000000

# See what's in the queue
curl http://localhost:18810/queue
```

## Architecture

```
cmd/scribe-hub/main.go          -- entrypoint, flag parsing, HTTP server
cmd/mcp-server/main.go           -- MCP stdio server (proxies to HTTP API)
internal/transcriber/handler.go  -- HTTP handlers, in-memory job store, serial worker queue
scripts/                         -- bash orchestrator for the transcription pipeline
tools/                           -- Python scripts (transcription + summarization) and prompt templates
```

All state is in-memory. Restarting the service clears all job history. The single background worker goroutine picks jobs off a buffered channel and executes the transcription script via `os/exec`.

## License

Private.
