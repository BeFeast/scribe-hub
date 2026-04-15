# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
# Build
go build ./cmd/scribe-hub

# Run (default port 18810)
go build -o scribe-hub ./cmd/scribe-hub && ./scribe-hub
go build -o scribe-hub ./cmd/scribe-hub && ./scribe-hub -port 8080

# Vet
go vet ./...
```

No tests exist yet. No Makefile or linter configuration is present.

## Architecture

Scribe-hub is an HTTP service (Go, module `ok.labs/scribe-hub`) that queues and runs video transcription jobs by shelling out to an external script (`video-transcript-summary-faster-whisper.sh`). Jobs are processed serially by a single background worker goroutine.

### Layout

- `cmd/scribe-hub/main.go` — entrypoint; parses flags, wires handler, starts HTTP server.
- `internal/transcriber/handler.go` — all application logic in one file: HTTP handlers, in-memory job store, serial job queue, and script execution.

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

### External dependencies

- **`yt-dlp`** — used at submit time to pre-fetch YouTube video titles.
- **Transcription script** — path defaults to `$HOME/Projects/video-summary/scripts/video-transcript-summary-faster-whisper.sh`. The script receives the URL plus optional flags (`--skip-summary`, `--language`, `--model-size`, `--title`).
- **`OBSIDIAN_FOLDER`** env var — passed to the script; defaults to `$HOME/Documents/Digests`.
