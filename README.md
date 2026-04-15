# scribe-hub

HTTP service that queues and runs video transcription jobs using [faster-whisper](https://github.com/SYSTRAN/faster-whisper). Submit a YouTube (or any yt-dlp-supported) URL, and scribe-hub will transcribe it in the background, optionally generating an AI summary and saving the result to your Obsidian vault.

Jobs are processed one at a time through a serial queue, so long-running transcriptions don't compete for GPU/CPU resources.

## Prerequisites

- **Go 1.25+**
- **[yt-dlp](https://github.com/yt-dlp/yt-dlp)** -- used to fetch video titles and by the transcription script to download audio
- **Google Chrome** -- yt-dlp reads cookies from Chrome to authenticate title fetches; title falls back to the URL if Chrome is unavailable
- **Transcription script** -- [`video-transcript-summary-faster-whisper.sh`](https://github.com/user/video-summary) (default location: `$HOME/Projects/video-summary/scripts/video-transcript-summary-faster-whisper.sh`)
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
| HTTP port | `-port` flag | `18810` |
| Transcription script | hardcoded | `$HOME/Projects/video-summary/scripts/video-transcript-summary-faster-whisper.sh` |
| Output folder | `OBSIDIAN_FOLDER` env var | `$HOME/Documents/Digests` |
| Log file | hardcoded | `$HOME/logs/scribe-hub.log` |
| `PATH` (script env) | hardcoded | macOS-specific paths; **macOS only until P0#3 is resolved** |

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
internal/transcriber/handler.go  -- HTTP handlers, in-memory job store, serial worker queue
```

All state is in-memory. Restarting the service clears all job history. The single background worker goroutine picks jobs off a buffered channel and executes the transcription script via `os/exec`.

## License

Private.
