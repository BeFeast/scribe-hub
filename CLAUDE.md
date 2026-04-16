# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
# Build both binaries
go build ./cmd/scribe-hub
go build ./cmd/mcp-server

# Run (default port 18810)
go build -o scribe-hub ./cmd/scribe-hub && ./scribe-hub
go build -o scribe-hub ./cmd/scribe-hub && ./scribe-hub -port 8080

# Vet
go vet ./...
```

No tests exist yet. No Makefile or linter configuration is present.

## Architecture

Scribe-hub is an HTTP service (Go, module `ok.labs/scribe-hub`) that queues and runs video transcription jobs. Jobs are processed serially by a single background worker goroutine.

### Layout

- `cmd/scribe-hub/main.go` — entrypoint; parses flags, wires handler, starts HTTP server.
- `cmd/mcp-server/main.go` — MCP stdio server wrapping the HTTP API for Claude Code, Codex CLI, and OpenCode.
- `internal/transcriber/handler.go` — HTTP handlers, in-memory job store, serial job queue, and script execution.
- `scripts/video-transcript-summary-faster-whisper.sh` — orchestrator script: downloads audio, transcribes, summarizes.
- `tools/faster-whisper-transcribe.py` — Python transcription script (run via `uv run`).
- `tools/gemini-summarize.py` — Python summarization script using Gemini API (run via `uv run`).
- `tools/prompts/` — prompt templates for Gemini and OpenCode summarization.

### Key types

- **`Store`** — mutex-protected `map[string]*Job`; all state is in-memory (no persistence).
- **`Handler`** — owns the `Store`, the job queue (`[]*jobRequest`), a single worker channel (`jobsCh`), and all HTTP route handlers.
- **`Job`** — represents a transcription request through its lifecycle: `queued → running → done | failed`.

### API routes (registered in `Handler.ServeHTTP`)

| Method | Path              | Purpose                             |
|--------|-------------------|-------------------------------------|
| POST   | `/transcribe`     | Submit a URL for transcription      |
| GET    | `/status/{id}`    | Poll a single job's status          |
| GET    | `/jobs`           | List jobs (optional `?status=` filter, comma-separated) |
| GET    | `/queue`          | Show currently running + queued jobs |
| GET    | `/health`         | Health check                        |

### MCP server

`cmd/mcp-server/` is a stdio-based MCP server (using `mcp-go`) that proxies requests to the running scribe-hub HTTP API. It exposes four tools: `transcribe`, `job_status`, `list_jobs`, `queue`. Configured via `SCRIBE_HUB_URL` env var (default `http://localhost:18810`).

Config files for each CLI tool:
- `.mcp.json` — Claude Code
- `.codex/config.toml` — Codex CLI
- `opencode.json` — OpenCode

### Configuration

- **`SCRIBE_SCRIPT_PATH`** — override transcription script path (default: auto-resolved from binary or `./scripts/video-transcript-summary-faster-whisper.sh`).
- **`SCRIBE_EXTRA_PATH`** — prepend extra directories to `$PATH` for child processes.
- **`OBSIDIAN_FOLDER`** — output directory passed to the script (default: `$HOME/Documents/Digests`).
- **`GEMINI_API_KEY`** — required for summarization (not needed with `--skip-summary`).

### Runtime prerequisites

`yt-dlp`, `ffmpeg`, `uv`, `python3 >= 3.10`.
