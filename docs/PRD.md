# Product Requirements Document: scribe-hub improvements

## Current state

scribe-hub is a functional single-user transcription queue service. It accepts URLs, fetches titles via yt-dlp, queues jobs, runs them serially through an external script, and reports status via a REST API. All state lives in memory with no persistence, no authentication, no cancellation, and a single hardcoded worker.

This document outlines improvements organized by priority.

---

## P0 -- Reliability & data safety

### 1. Job persistence

**Problem:** All jobs are lost on restart. A crash mid-transcription loses the entire history and the running job silently disappears.

**Requirements:**
- Persist jobs to a local SQLite (or BoltDB) database on every state transition.
- On startup, reload incomplete jobs (`queued`, `running`) back into the queue.
- Jobs that were `running` at crash time should be re-queued, not silently marked failed.

### 2. Graceful shutdown

**Problem:** `SIGTERM`/`SIGINT` kills the process immediately. A running transcription (which can take 10+ minutes) is lost with no cleanup.

**Requirements:**
- Intercept OS signals, stop accepting new HTTP requests, and wait for the current job to finish (with a configurable timeout).
- Drain the queue to the persistence layer before exiting.

### 3. Script execution hardening

**Problem:** The `PATH` env var is hardcoded to macOS-specific paths (`/Users/god/.local/bin`). The script path is also hardcoded to a specific `$HOME` layout.

**Requirements:**
- Make the script path configurable via flag or env var (`SCRIBE_SCRIPT_PATH`).
- Inherit the system `PATH` by default instead of overriding it. Allow `SCRIBE_PATH` override if needed.
- Add a startup check that the script exists and is executable; fail fast with a clear error if not.

---

## P1 -- Usability

### API contract baseline (applies to `POST /transcribe`, `DELETE /jobs/{id}`, `GET /status/{id}`, `GET /jobs`)

**Problem:** API behavior is currently implied by implementation details and ad-hoc responses, making client integration brittle across CLI tools, web UIs, and accessibility tooling.

**Requirements:**
- All non-2xx responses MUST return a stable JSON error object:
  - `code` (string, machine-readable, stable across releases; e.g. `TITLE_FETCH_TIMEOUT`, `JOB_NOT_FOUND`)
  - `message` (string, human-readable summary)
  - `hint` (string, optional actionable next step)
  - `retryable` (boolean; `true` only when a retry is expected to succeed)
- `message` content MUST be understandable when read aloud by screen readers and CLI narration:
  - No internal jargon or stack traces.
  - Prefer plain language with concrete guidance (for example: "Job is already running; use force=true to submit another").
  - Keep to one to two short sentences.
- Status endpoints MUST use a consistent vocabulary everywhere (`/status/{id}` and `/jobs`):
  - Allowed statuses: `queued`, `running`, `done`, `failed`, `cancelled`.
  - Disallow synonyms (`complete`, `success`, `error`, etc.) in API payloads.
- Lifecycle transitions MUST be explicit and enforced:
  - `queued -> running`
  - `running -> done | failed | cancelled`
  - `queued -> cancelled`
  - No other transitions are valid (terminal states are `done`, `failed`, `cancelled`).
- Publish JSON Schemas for request/response payloads and version them with the API:
  - `schemas/transcribe-request.schema.json`
  - `schemas/job-response.schema.json`
  - `schemas/error-response.schema.json`
  - Schemas MUST be linked from README/API docs and included in release artifacts.

**Required error/edge-case examples (docs + tests):**
- Title fetch timeout during `POST /transcribe` (fallback title used; request still succeeds when possible, otherwise explicit timeout error with `retryable: true`).
- Cancellation via `DELETE /jobs/{id}` for both queued and running jobs (`cancelled` terminal state).
- Duplicate URL detection on `POST /transcribe` (existing active job returned or conflict response, with clear `hint` about `force=true`).
- Rate limit exceedance (`429`) with a retry-oriented hint and `retryable: true`.

### 4. Job cancellation

**Problem:** Once submitted, a job cannot be cancelled. If a wrong URL is submitted, it blocks the queue until the script finishes or fails.

**Requirements:**
- `DELETE /jobs/{id}` -- cancel a queued job (remove from queue) or kill a running job (send `SIGTERM` to the child process).
- Cancelled jobs transition to a `cancelled` status.

### 5. Webhook / callback notifications

**Problem:** Clients must poll `/status/{id}` to know when a job completes.

**Requirements:**
- Optional `callback_url` field on `POST /transcribe`.
- On job completion (done/failed), POST the job JSON to the callback URL.
- Fire-and-forget with a single retry on failure.

### 6. Duplicate URL detection

**Problem:** The same URL can be submitted multiple times, wasting resources on redundant transcriptions.

**Requirements:**
- On submit, check if a job with the same URL is already `queued` or `running`. If so, return the existing job instead of creating a new one.
- Add a `force` parameter to bypass this check when re-transcription is intentional.

### 7. Request timeout for title fetching

**Problem:** `fetchYouTubeTitle` calls `yt-dlp` synchronously during the HTTP request with no timeout. If yt-dlp hangs (network issue, rate limit), the `POST /transcribe` request hangs indefinitely.

**Requirements:**
- Add a context with timeout (e.g. 15s) to the yt-dlp title fetch.
- On timeout, proceed with the URL as the title (current fallback behavior) rather than blocking.

---

## P2 -- Observability

### 8. Structured logging

**Problem:** Logs are unstructured `log.Printf` calls mixed with raw script output in a single file.

**Requirements:**
- Use structured logging (`slog` from stdlib) with JSON output.
- Separate application logs from script output. Script output should go to per-job log files or a dedicated directory.
- Include job ID, URL, duration, and status in log entries.

### 9. Metrics endpoint

**Problem:** No visibility into queue depth, job throughput, or error rates.

**Requirements:**
- `GET /metrics` endpoint exposing:
  - `jobs_total` (counter by status)
  - `jobs_queue_depth` (gauge)
  - `job_duration_seconds` (histogram)
  - `title_fetch_duration_seconds` (histogram)
- Prometheus-compatible format.

### 10. Job history & cleanup

**Problem:** The in-memory store grows without bound. After persistence is added, the database will also grow indefinitely.

**Requirements:**
- Add a `GET /jobs` pagination parameter (`limit`, `offset` or cursor-based).
- Auto-purge completed/failed jobs older than a configurable retention period (default 30 days).

---

## P3 -- Scalability & features

### 11. Configurable worker concurrency

**Problem:** Only one job runs at a time. On machines with sufficient resources, this underutilizes capacity.

**Requirements:**
- Add a `-workers N` flag (default 1) controlling how many jobs run in parallel.
- Queue position calculations should account for the worker pool size.

### 12. Job priority

**Problem:** All jobs are FIFO. There's no way to expedite an urgent transcription.

**Requirements:**
- Optional `priority` field on submit (`low`, `normal`, `high`; default `normal`).
- Higher-priority jobs are inserted ahead of lower-priority ones in the queue.

### 13. Rate limiting

**Problem:** No protection against excessive submissions flooding the queue.

**Requirements:**
- Configurable rate limit per IP or globally (e.g. 10 submissions per minute).
- Return `429 Too Many Requests` when exceeded.

### 14. Docker packaging

**Problem:** Deployment requires manually installing Go, yt-dlp, faster-whisper, and the transcription script.

**Requirements:**
- Provide a `Dockerfile` and `docker-compose.yml` that bundles all dependencies.
- Support GPU passthrough for faster-whisper acceleration.
- Volume mount for the output directory.

---

## P4 -- Nice to have

### 15. WebSocket live output

**Problem:** For long-running jobs, there's no way to see progress. You only get output after the job finishes.

**Requirements:**
- `GET /ws/jobs/{id}/output` -- WebSocket endpoint streaming script stdout/stderr in real time.
- Fallback: `GET /jobs/{id}/output?follow=true` using SSE (Server-Sent Events).

### 16. Web UI

**Problem:** The service is API-only, requiring curl or a custom client.

**Requirements:**
- Minimal embedded web UI (single HTML page served at `/`) showing:
  - Submit form (URL + options)
  - Live queue view
  - Job history table with status, title, duration
  - Click-to-expand job output

### 17. Multi-source support

**Problem:** Title fetching assumes YouTube. The service could handle podcasts, local files, or direct audio URLs.

**Requirements:**
- Detect URL type and use appropriate title-fetching strategy (yt-dlp for video sites, HTTP HEAD for direct files, filename for local paths).
- Support `file://` paths or multipart upload for local audio/video files.
