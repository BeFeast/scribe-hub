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

### 4. API contract baseline (applies to `POST /transcribe`, `DELETE /jobs/{id}`, `GET /status/{id}`, `GET /jobs`)

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

### 8. CLI Counterpart Compatibility

**Problem:** scribe-hub behavior is currently defined around its own API patterns. Teams using OpenClaw, Claude, and Codex CLIs need a shared command contract and output compatibility to avoid provider-specific wrappers and brittle glue code.

**Requirements:**
- Define a canonical command surface (subcommands, required/optional flags, and JSON input/output modes) that all supported CLIs can implement or adapt to.
- Specify a behavior parity matrix for OpenClaw CLI, Claude CLI, and Codex CLI covering core workflows (submit, status, cancel, list/history, output retrieval, and machine-readable mode).
- Define normalized exit codes and a structured error schema, including transport errors, validation errors, auth/config errors, timeout/interruption cases, and provider/runtime failures.
- Define transcript and job schema compatibility, including field-level mapping rules, type normalization, nullability/default handling, timestamp conventions, and forward-compatible handling for unknown fields.
- Add explicit non-goals for provider-specific features that may remain optional (e.g., proprietary flags, provider-native streaming/event formats, and advanced diagnostics not expressible in the canonical schema).
- Add acceptance criteria requiring the same input fixtures to produce functionally equivalent outputs across OpenClaw CLI, Claude CLI, and Codex CLI, allowing only documented non-goal deviations.

---

### 8. Documentation deliverables

**Requirements:**
- Provide quickstart examples for OpenClaw, Claude CLI, and Codex CLI that demonstrate equivalent end-to-end workflows (submit, monitor, and retrieve results) using the same sample job.
- Include copy-paste command snippets for each CLI covering `submit`, `cancel`, `status`, and `history` operations.
- Add a troubleshooting matrix that maps common authentication, environment-variable, and path-resolution issues to CLI-specific symptoms and fixes.
- Define documentation versioning rules and parity checks so CLI examples are updated whenever API contracts, flags, or response schemas change.

---

## P2 -- Observability

### 9. Structured logging

**Problem:** Logs are unstructured `log.Printf` calls mixed with raw script output in a single file.

**Requirements:**
- Use structured logging (`slog` from stdlib) with JSON output.
- Separate application logs from script output. Script output should go to per-job log files or a dedicated directory.
- Include job ID, URL, duration, and status in log entries.

### 10. Metrics endpoint

**Problem:** No visibility into queue depth, job throughput, or error rates.

**Requirements:**
- `GET /metrics` endpoint exposing:
  - `jobs_total` (counter by status)
  - `jobs_queue_depth` (gauge)
  - `job_duration_seconds` (histogram)
  - `title_fetch_duration_seconds` (histogram)
- Prometheus-compatible format.

### 11. Job history & cleanup

**Problem:** The in-memory store grows without bound. After persistence is added, the database will also grow indefinitely.

**Requirements:**
- Add a `GET /jobs` pagination parameter (`limit`, `offset` or cursor-based).
- Auto-purge completed/failed jobs older than a configurable retention period (default 30 days).

---

## P3 -- Scalability & features

### 12. Configurable worker concurrency

**Problem:** Only one job runs at a time. On machines with sufficient resources, this underutilizes capacity.

**Requirements:**
- Add a `-workers N` flag (default 1) controlling how many jobs run in parallel.
- Queue position calculations should account for the worker pool size.

### 13. Job priority

**Problem:** All jobs are FIFO. There's no way to expedite an urgent transcription.

**Requirements:**
- Optional `priority` field on submit (`low`, `normal`, `high`; default `normal`).
- Higher-priority jobs are inserted ahead of lower-priority ones in the queue.

### 14. Rate limiting

**Problem:** No protection against excessive submissions flooding the queue.

**Requirements:**
- Configurable rate limit per IP or globally (e.g. 10 submissions per minute).
- Return `429 Too Many Requests` when exceeded.

### 15. Docker packaging

**Problem:** Deployment requires manually installing Go, yt-dlp, faster-whisper, and the transcription script.

**Requirements:**
- Provide a `Dockerfile` and `docker-compose.yml` that bundles all dependencies.
- Support GPU passthrough for faster-whisper acceleration.
- Volume mount for the output directory.

---

## P4 -- Nice to have

### 16. WebSocket live output

**Problem:** For long-running jobs, there's no way to see progress. You only get output after the job finishes.

**Requirements:**
- `GET /ws/jobs/{id}/output` -- WebSocket endpoint streaming script stdout/stderr in real time.
- Fallback: `GET /jobs/{id}/output?follow=true` using SSE (Server-Sent Events).
- Baseline accessibility conformance target: WCAG 2.2 AA for all live output interactions.
- Live output stream and related controls are fully operable with keyboard only (focus order, open/close stream, scroll, pause/resume).
- Live output panel uses semantic labeling and a dedicated live region that announces job state transitions (`queued -> running -> completed/failed`) to screen readers.
- All interactive controls (connect/disconnect, follow mode, pause/resume, expand/collapse output) meet contrast requirements and always show a visible focus indicator.
- Auto-scrolling live output supports reduced-motion preferences and includes pause/stop controls that halt automatic movement/announcement updates on user request.

**Acceptance tests:**
- **Conformance target:** Accessibility checklist and automated scans report WCAG 2.2 AA compliance for the live output workflow, with any exceptions documented and approved.
- **Keyboard-only operation:** Starting a stream, toggling follow mode, pausing/resuming output, and expanding/collapsing output can be completed without a mouse using only keyboard controls.
- **Semantic labeling + live announcements:** Screen reader testing confirms labeled controls/regions and audible announcements for each job state transition.
- **Contrast + focus-visible:** Visual QA verifies minimum contrast and clearly visible focus styles for every interactive element in live output views.
- **Reduced motion + pause/stop:** With reduced-motion enabled, auto-scrolling/animation is minimized; users can pause/stop auto-scroll and resume manually, and announcement updates from the live region are also halted when scrolling is paused.

### 17. Web UI

**Problem:** The service is API-only, requiring curl or a custom client.

**Requirements:**
- Minimal embedded web UI (single HTML page served at `/`) showing:
  - Submit form (URL + options)
  - Live queue view
  - Job history table with status, title, duration
  - Click-to-expand job output
- Baseline accessibility conformance target: WCAG 2.2 AA for the full UI experience.
- Submit form, history table interactions, and output expansion are fully operable with keyboard only.
- Form fields, status badges, queue/history controls, and output regions have semantic labels/roles; job state changes are exposed through screen-reader announcements.
- All interactive elements in the UI meet contrast requirements and provide clear `:focus-visible` styling.
- Motion-heavy behaviors (including auto-scrolling output) honor reduced-motion preferences and provide pause/stop controls.

**Acceptance tests:**
- **Conformance target:** End-to-end UI accessibility review confirms WCAG 2.2 AA target coverage for submit, queue, history, and output flows.
- **Keyboard-only operation:** A keyboard-only test pass completes submit, history navigation, row expansion, and output interaction without pointer input.
- **Semantic labeling + announcements:** Accessibility tree/screen reader checks confirm form labels, status badge semantics, and announcements for job lifecycle changes.
- **Contrast + focus-visible:** Automated and manual checks confirm compliant color contrast and visible focus state on all interactive elements.
- **Reduced motion + pause/stop:** When `prefers-reduced-motion` is active, all motion-heavy behaviors (including auto-scrolling output and any animated indicators) are reduced; users can pause/stop/resume auto-scrolling output from UI controls.

### 18. Multi-source support

**Problem:** Title fetching assumes YouTube. The service could handle podcasts, local files, or direct audio URLs.

**Requirements:**
- Detect URL type and use appropriate title-fetching strategy (yt-dlp for video sites, HTTP HEAD for direct files, filename for local paths).
- Support `file://` paths or multipart upload for local audio/video files.

---

## Test strategy

### 1. Golden fixtures

**Requirements:**
- Define a shared set of golden fixtures that include input URL and job options.
- For each fixture, define the expected normalized output JSON used for parity checks.

### 2. CLI parity verification

**Requirements:**
- Run every golden fixture against all three CLIs.
- Normalize each CLI output to the shared JSON contract and diff results against expected golden outputs.
- Treat any non-whitespace JSON diff as a parity regression.

### 3. Accessibility validation for UI

**Requirements:**
- Add accessibility checks for keyboard traversal across all interactive components.
- Verify ARIA announcements for status updates and asynchronous job state changes.
- Enforce contrast checks for text and actionable UI elements.

### 4. Release gate

**Requirements:**
- Block release if any CLI parity regression is detected from the golden fixture suite.
- Block release if any critical accessibility regression is detected.
