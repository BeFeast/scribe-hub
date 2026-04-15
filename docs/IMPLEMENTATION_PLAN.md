# Scribe Hub Implementation Plan

This plan translates `docs/PRD.md` into an execution roadmap with phased milestones, architecture changes, and acceptance gates.

## 1) Goals, sequencing, and delivery strategy

### Primary goals
- Eliminate data loss and make job lifecycle crash-safe.
- Stabilize API contracts so clients can integrate reliably.
- Add operational visibility (logs + metrics).
- Prepare for safe scale-up (concurrency, priority, rate limiting).

### Sequencing principles
- Implement in strict priority order (P0 → P1 → P2 → P3 → P4).
- Ship thin vertical slices with migration-safe defaults.
- Keep behavior backward compatible where possible; document breaking changes explicitly.

### Proposed release train
- **Release A (P0):** persistence, graceful shutdown, script hardening.
- **Release B (P1 core):** API contract baseline, cancellation, duplicate detection, title timeout.
- **Release C (P1 integration):** callbacks + client compatibility docs/schemas.
- **Release D (P2):** structured logging, metrics, retention + pagination.
- **Release E (P3):** workers, priority, rate limiting, Docker.
- **Release F (P4):** live output streaming + minimal UI accessibility baseline.

## 2) Target architecture changes

### Storage layer
- Add a persistence abstraction (e.g., `JobRepository`) with SQLite-backed implementation.
- Persist every lifecycle mutation transactionally.
- On startup, restore queue state:
  - `queued` jobs reinserted in queue order.
  - `running` jobs moved back to `queued` and reprocessed.

### Job orchestration layer
- Replace single `jobsCh` worker with a worker-pool scheduler (`workers=N`, default 1).
- Centralize transition validation via a state machine helper.
- Track active process handles to enable cancellation (`SIGTERM`) and shutdown control.

### API layer
- Introduce typed request/response DTOs and shared error response builder.
- Enforce consistent status vocabulary: `queued`, `running`, `done`, `failed`, `cancelled`.
- Add schema files under `schemas/` and document versioning.

### Runtime/config layer
- Add config struct with env/flag parsing:
  - `SCRIBE_SCRIPT_PATH`, optional `SCRIBE_PATH`, `-workers`, retention, shutdown timeout, rate limits.
- Startup validation for script existence + executable permissions.

### Observability layer
- Migrate to `slog` JSON with consistent keys (`job_id`, `url`, `status`, `duration_ms`).
- Emit Prometheus metrics for queue depth, totals, and latency histograms.
- Split script output into per-job output files (and expose references via API if needed).

## 3) Workstreams and subagent-style parallelization plan

If working with a subagent team, split by bounded interfaces:

1. **Platform/Core subagent**
   - Persistence, migrations, startup recovery, shutdown.
2. **API/Contract subagent**
   - Error schema, JSON schema files, endpoint consistency, cancellation/duplicate behavior.
3. **Execution/Runtime subagent**
   - Worker pool, priority queueing, process lifecycle, script/config hardening.
4. **Observability/Docs subagent**
   - `slog`, metrics endpoint, client compatibility docs, troubleshooting/quickstarts.

Integration cadence:
- Daily contract sync on shared DTOs/state transitions.
- Feature flags for partially completed workstreams.
- Merge order: storage/state-machine first, then API/runtime features, then docs/UI.

## 4) Detailed phase plan

## Phase 0 — Foundation (pre-P0 hardening)

### Deliverables
- Refactor `internal/transcriber/handler.go` into packages/modules:
  - `internal/api`
  - `internal/core` (state machine)
  - `internal/store`
  - `internal/runner`
  - `internal/config`
- Add integration test harness with temporary SQLite DB and fake script runner.

### Exit criteria
- Existing endpoints still pass baseline smoke tests.
- New module boundaries in place for parallel work.

---

## Phase 1 — P0 Reliability & data safety

### 1. Job persistence
Tasks:
- Create DB schema (`jobs`, optional `job_events`, `job_callbacks`).
- Persist creates + all transitions in one write path.
- Startup rehydration logic for queued/running states.

Acceptance:
- Restart during active run preserves all jobs and requeues interrupted work.

### 2. Graceful shutdown
Tasks:
- Signal handling for `SIGINT`/`SIGTERM`.
- HTTP server shutdown (`Server.Shutdown`) to stop new requests.
- Wait for in-flight jobs until timeout; persist queue state on exit.

Acceptance:
- Controlled shutdown leaves no in-memory-only state.

### 3. Script execution hardening
Tasks:
- Add `SCRIBE_SCRIPT_PATH` and optional `SCRIBE_PATH` support.
- Default to inherited `PATH` when override not supplied.
- Fail fast on startup if script missing/non-executable.

Acceptance:
- No hardcoded home-directory paths remain.

---

## Phase 2 — P1 API contract + core usability

### 4. API contract baseline
Tasks:
- Add canonical error object `{code,message,hint,retryable}` for all non-2xx.
- Enforce transition/state vocabulary via shared validator.
- Add schema files:
  - `schemas/transcribe-request.schema.json`
  - `schemas/job-response.schema.json`
  - `schemas/error-response.schema.json`

Acceptance:
- Endpoint behavior consistent across `/transcribe`, `/status/{id}`, `/jobs`, `/jobs/{id}` cancellation.

### 5. Job cancellation
Tasks:
- `DELETE /jobs/{id}`:
  - queued: remove from queue and mark cancelled.
  - running: send `SIGTERM` and mark cancelled when process exits.
- Ensure terminal-state invariants.

Acceptance:
- Cancellation works for queued and running jobs with deterministic final state.

### 6. Duplicate URL detection
Tasks:
- Active-job lookup by URL for `queued|running`.
- Return existing job unless `force=true` provided.

Acceptance:
- Duplicate submits no longer create redundant work by default.

### 7. Title fetch timeout
Tasks:
- Context timeout (default 15s) for `yt-dlp --get-title`.
- On timeout, fallback to URL title and proceed.

Acceptance:
- Submit endpoint remains responsive during upstream tool hangs.

---

## Phase 3 — P1 integrations/docs (client compatibility + callbacks)

### 8. Callback notifications
Tasks:
- Add optional `callback_url` to submit payload.
- On `done|failed`, POST job payload with one retry.
- Capture callback attempts/result in logs and metrics.

Acceptance:
- Clients can receive completion signals without polling.

### 9. API client compatibility package
Tasks:
- Add compatibility contract section to README/docs.
- Provide parity examples for curl + JS fetch + Python requests.
- Add troubleshooting matrix and schema compatibility guidance.

Acceptance:
- Same fixtures produce equivalent outcomes across documented clients.

---

## Phase 4 — P2 Observability + retention

### 10. Structured logging
Tasks:
- Replace ad-hoc `log.Printf` with `slog` JSON.
- Write script output to per-job files in dedicated directory.

### 11. Metrics endpoint
Tasks:
- Implement Prometheus exposition at `/metrics`.
- Counters/gauges/histograms per PRD.

### 12. History cleanup
Tasks:
- Add pagination (`limit`, `offset` or cursor) to `/jobs`.
- Background retention cleanup for terminal jobs older than configurable days.

Acceptance:
- Operators can observe throughput/errors and keep storage bounded.

---

## Phase 5 — P3 Scale and deployment

### 13. Configurable concurrency
Tasks:
- Worker pool `-workers N` with default 1.
- Queue position semantics updated for multiple workers.

### 14. Job priority
Tasks:
- Add priority field (`low|normal|high`) and priority-aware queue insertion.

### 15. Rate limiting
Tasks:
- Add global or per-IP token bucket.
- Return stable 429 error object with retry hint.

### 16. Docker packaging
Tasks:
- Add `Dockerfile` + `docker-compose.yml` including dependencies and output volume.
- Document GPU passthrough option for faster-whisper.

Acceptance:
- Reproducible deployment path with bounded queue abuse risk.

---

## Phase 6 — P4 UX/live output

### 17. Live output stream
Tasks:
- Add WebSocket endpoint for stdout/stderr stream.
- Add SSE fallback (`follow=true`).
- Implement WCAG 2.2 AA controls and live-region announcements.

### 18. Minimal web UI
Tasks:
- Single-page app at `/` with submit form, queue, history, expandable output.
- Keyboard navigation, semantic labels, contrast/focus-visible, reduced-motion behavior.

Acceptance:
- Accessibility acceptance checklist passes for core live-output workflows.

## 5) Data model and migration sketch

### Initial SQLite tables
- `jobs`:
  - ids/timestamps/status/url/title/output/error/priority/callback metadata.
- `job_events` (optional but recommended):
  - append-only lifecycle events for audit/debug.
- `schema_migrations`:
  - migration version tracking.

Migration policy:
- Forward-only SQL migrations with startup apply step.
- Backward compatibility window for one previous schema version when practical.

## 6) Testing strategy

### Automated test layers
- **Unit tests:** transition validator, duplicate detection, priority ordering, rate limiter.
- **Integration tests:** API + SQLite + fake runner; crash/restart requeue behavior.
- **E2E smoke tests:** submit → status → cancel/history, callbacks, metrics exposure.

### Required PRD scenario tests
- Title fetch timeout behavior and retryable error semantics where applicable.
- Cancellation for queued and running jobs.
- Duplicate URL handling with and without `force=true`.
- Rate-limit `429` response correctness.

### Non-functional verification
- Graceful shutdown under in-flight workload.
- Retention cleanup impact under large historical dataset.
- Accessibility checks for P4 workflows.

## 7) Risks and mitigations

- **Risk:** Refactor destabilizes existing behavior.
  - **Mitigation:** Phase 0 harness + incremental merges behind feature flags.
- **Risk:** Process cancellation edge cases across platforms.
  - **Mitigation:** Use process groups where needed; test Linux/macOS signal semantics.
- **Risk:** Callback endpoint failures create noisy retries.
  - **Mitigation:** one retry max + explicit metrics and logs.
- **Risk:** Multi-worker scheduling introduces race conditions.
  - **Mitigation:** single scheduler lock + deterministic state transition checks.

## 8) Definition of done (program level)

This plan is considered complete when:
- All P0 and P1 requirements are shipped with tests and docs.
- Schemas are versioned and referenced in README/API docs.
- Metrics/logging support operational debugging without raw process logs.
- Scale features (P3) have documented defaults and safe limits.
- P4 accessibility acceptance tests pass for delivered UI/streaming features.
