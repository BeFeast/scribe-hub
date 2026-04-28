package transcriber

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"
)

type Job struct {
    ID          string     `json:"id"`
    URL         string     `json:"url"`
    Status      string     `json:"status"`
    CreatedAt   time.Time  `json:"created_at"`
    UpdatedAt   time.Time  `json:"updated_at"`
    QueuedAt    time.Time  `json:"queued_at"`
    StartedAt   *time.Time `json:"started_at"`
    CompletedAt *time.Time `json:"completed_at"`
    Title       string     `json:"title,omitempty"`
    Position    *int       `json:"position,omitempty"`
    Output      string     `json:"output,omitempty"`
    Error       string     `json:"error,omitempty"`
}

type Store struct {
    mu   sync.RWMutex
    jobs map[string]*Job
}

func NewStore() *Store {
    return &Store{jobs: make(map[string]*Job)}
}

func (s *Store) Add(job *Job) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.jobs[job.ID] = job
}

func (s *Store) Get(id string) (*Job, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    j, ok := s.jobs[id]
    if !ok {
        return nil, false
    }
    return cloneJob(j), true
}

func (s *Store) Update(id string, fn func(*Job)) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if j, ok := s.jobs[id]; ok {
        fn(j)
        j.UpdatedAt = time.Now()
    }
}

func (s *Store) snapshot() []*Job {
    s.mu.RLock()
    defer s.mu.RUnlock()
    out := make([]*Job, 0, len(s.jobs))
    for _, j := range s.jobs {
        out = append(out, cloneJob(j))
    }
    return out
}

type Handler struct {
    store      *Store
    scriptPath string
    logPath    string

    queueMu sync.Mutex
    queue   []*jobRequest
    running *jobRequest
    jobsCh  chan *jobRequest
}

type jobRequest struct {
    job         *Job
    skipSummary bool
    overwrite   bool
    language    string
    modelSize   string
}

type queueResponse struct {
    Running *Job  `json:"running"`
    Queued  []*Job `json:"queued"`
    Total   int   `json:"total"`
}

func NewHandler(scriptPath, logPath string) *Handler {
    h := &Handler{
        store:      NewStore(),
        scriptPath: scriptPath,
        logPath:    logPath,
        jobsCh:     make(chan *jobRequest, 128),
    }
    go h.worker()
    return h
}

func (h *Handler) ServeHTTP(mux *http.ServeMux) {
    mux.HandleFunc("POST /transcribe", h.handleTranscribe)
    mux.HandleFunc("GET /status/{id}", h.handleStatus)
    mux.HandleFunc("GET /jobs", h.handleJobs)
    mux.HandleFunc("GET /queue", h.handleQueue)
    mux.HandleFunc("GET /health", h.handleHealth)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) handleTranscribe(w http.ResponseWriter, r *http.Request) {
    var req struct {
        URL         string `json:"url"`
        SkipSummary bool   `json:"skip_summary"`
        Overwrite   bool   `json:"overwrite"`
        Language    string `json:"language"`
        ModelSize   string `json:"model_size"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
        http.Error(w, `{"error":"url is required"}`, http.StatusBadRequest)
        return
    }

    title := req.URL
    if fetchedTitle, err := fetchYouTubeTitle(req.URL); err != nil {
        log.Printf("failed to prefetch YouTube title for %s: %v", req.URL, err)
    } else if fetchedTitle != "" {
        title = fetchedTitle
    }

    now := time.Now()
    id := fmt.Sprintf("%d", now.UnixMilli())
    job := &Job{
        ID:        id,
        URL:       req.URL,
        Status:    "queued",
        CreatedAt: now,
        UpdatedAt: now,
        QueuedAt:  now,
        Title:     title,
    }
    h.store.Add(job)

    jr := &jobRequest{
        job:         job,
        skipSummary: req.SkipSummary,
        overwrite:   req.Overwrite,
        language:    req.Language,
        modelSize:   req.ModelSize,
    }
    h.enqueue(jr)

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusAccepted)
    json.NewEncoder(w).Encode(h.jobWithPosition(job.ID))
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    job, ok := h.store.Get(id)
    if !ok {
        http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
        return
    }
    pos := h.positionFor(id)
    job.Position = pos
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(job)
}

func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request) {
    jobs := h.listJobs(r.URL.Query().Get("status"))
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(jobs)
}

func (h *Handler) handleQueue(w http.ResponseWriter, r *http.Request) {
    running, queued := h.activeQueue()
    resp := queueResponse{Running: running, Queued: queued, Total: len(queued)}
    if running != nil {
        resp.Total++
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}

func (h *Handler) enqueue(jr *jobRequest) {
    h.queueMu.Lock()
    h.queue = append(h.queue, jr)
    h.queueMu.Unlock()
    h.jobsCh <- jr
}

func (h *Handler) worker() {
    for jr := range h.jobsCh {
        h.startJob(jr)
        h.run(jr)
        h.finishJob(jr)
    }
}

func (h *Handler) startJob(jr *jobRequest) {
    now := time.Now()
    h.queueMu.Lock()
    if len(h.queue) > 0 && h.queue[0].job.ID == jr.job.ID {
        h.queue = h.queue[1:]
    } else {
        for i, queued := range h.queue {
            if queued.job.ID == jr.job.ID {
                h.queue = append(h.queue[:i], h.queue[i+1:]...)
                break
            }
        }
    }
    h.running = jr
    h.queueMu.Unlock()

    h.store.Update(jr.job.ID, func(j *Job) {
        j.Status = "running"
        j.StartedAt = &now
    })
}

func (h *Handler) finishJob(jr *jobRequest) {
    h.queueMu.Lock()
    if h.running != nil && h.running.job.ID == jr.job.ID {
        h.running = nil
    }
    h.queueMu.Unlock()
}

func (h *Handler) listJobs(statusFilter string) []*Job {
    jobs := h.store.snapshot()
    allowed := parseStatusFilter(statusFilter)
    out := make([]*Job, 0, len(jobs))
    for _, job := range jobs {
        if len(allowed) > 0 && !allowed[job.Status] {
            continue
        }
        job.Position = h.positionFor(job.ID)
        out = append(out, job)
    }
    sort.Slice(out, func(i, j int) bool {
        if out[i].QueuedAt.Equal(out[j].QueuedAt) {
            return out[i].CreatedAt.Before(out[j].CreatedAt)
        }
        return out[i].QueuedAt.Before(out[j].QueuedAt)
    })
    return out
}

func (h *Handler) activeQueue() (*Job, []*Job) {
    h.queueMu.Lock()
    defer h.queueMu.Unlock()

    var running *Job
    if h.running != nil {
        running = cloneJob(h.running.job)
        pos := 0
        running.Position = &pos
    }

    queued := make([]*Job, 0, len(h.queue))
    for i, jr := range h.queue {
        job := cloneJob(jr.job)
        pos := i + 1
        job.Position = &pos
        queued = append(queued, job)
    }
    return running, queued
}

func (h *Handler) positionFor(id string) *int {
    h.queueMu.Lock()
    defer h.queueMu.Unlock()

    if h.running != nil && h.running.job.ID == id {
        pos := 0
        return &pos
    }
    for i, jr := range h.queue {
        if jr.job.ID == id {
            pos := i + 1
            return &pos
        }
    }
    return nil
}

func (h *Handler) jobWithPosition(id string) *Job {
    job, ok := h.store.Get(id)
    if !ok {
        return nil
    }
    job.Position = h.positionFor(id)
    return job
}

func (h *Handler) run(jr *jobRequest) {
    job := jr.job

    args := []string{h.scriptPath, job.URL}
    if jr.skipSummary {
        args = append(args, "--skip-summary")
    }
    if jr.overwrite {
        args = append(args, "--overwrite")
    }
    if jr.language != "" {
        args = append(args, "--language", jr.language)
    }
    if jr.modelSize != "" {
        args = append(args, "--model-size", jr.modelSize)
    }
    if job.Title != "" && job.Title != job.URL {
        args = append(args, "--title", job.Title)
    }

    cmd := exec.Command(args[0], args[1:]...)
    env := os.Environ()
    if extra := os.Getenv("SCRIBE_EXTRA_PATH"); extra != "" {
        env = append(env, "PATH="+extra+":"+os.Getenv("PATH"))
    }
    env = append(env,
        "OBSIDIAN_FOLDER="+getEnv("OBSIDIAN_FOLDER", filepath.Join(os.Getenv("HOME"), "Documents/Digests")),
    )
    cmd.Env = env

    var buf bytes.Buffer
    logFile, err := os.OpenFile(h.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        log.Printf("failed to open log: %v", err)
        cmd.Stdout = &buf
        cmd.Stderr = &buf
    } else {
        defer logFile.Close()
        fmt.Fprintf(logFile, "\n[%s] job %s: %s\n", time.Now().Format(time.RFC3339), job.ID, job.URL)
        multi := io.MultiWriter(&buf, logFile)
        cmd.Stdout = multi
        cmd.Stderr = multi
    }

    err = cmd.Run()
    output := buf.String()
    completedAt := time.Now()

    if title := parseOutputField(output, "TITLE"); title != "" {
        h.store.Update(job.ID, func(j *Job) {
            j.Title = title
        })
    }

    if err != nil {
        h.store.Update(job.ID, func(j *Job) {
            j.Status = "failed"
            j.Error = err.Error()
            j.Output = output
            j.CompletedAt = &completedAt
        })
        log.Printf("job %s failed: %v", job.ID, err)
        return
    }

    h.store.Update(job.ID, func(j *Job) {
        j.Status = "done"
        j.Output = output
        j.CompletedAt = &completedAt
    })
    log.Printf("job %s done", job.ID)
}

func parseStatusFilter(raw string) map[string]bool {
    raw = strings.TrimSpace(raw)
    if raw == "" {
        return nil
    }
    out := make(map[string]bool)
    for _, part := range strings.Split(raw, ",") {
        part = strings.TrimSpace(part)
        if part == "" {
            continue
        }
        out[part] = true
    }
    return out
}

func cloneJob(j *Job) *Job {
    if j == nil {
        return nil
    }
    cp := *j
    if j.StartedAt != nil {
        t := *j.StartedAt
        cp.StartedAt = &t
    }
    if j.CompletedAt != nil {
        t := *j.CompletedAt
        cp.CompletedAt = &t
    }
    if j.Position != nil {
        p := *j.Position
        cp.Position = &p
    }
    return &cp
}

func getEnv(key, def string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return def
}

func fetchYouTubeTitle(url string) (string, error) {
    cmd := exec.Command("yt-dlp", "--cookies-from-browser", "chrome", "--get-title", "--no-playlist", url)
    out, err := cmd.Output()
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(out)), nil
}

func parseOutputField(output, key string) string {
    prefix := key + ":"
    for _, line := range strings.Split(output, "\n") {
        if strings.HasPrefix(line, prefix) {
            return strings.TrimSpace(strings.TrimPrefix(line, prefix))
        }
    }
    return ""
}
