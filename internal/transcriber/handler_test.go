package transcriber

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleTranscribeReturnsJSONErrorForInvalidRequest(t *testing.T) {
	h := newHandler("", "", handlerOptions{
		startWorker: false,
		fetchTitle: func(context.Context, string) (string, error) {
			return "", nil
		},
	})
	mux := http.NewServeMux()
	h.ServeHTTP(mux)

	req := httptest.NewRequest(http.MethodPost, "/transcribe", strings.NewReader(`{"url":`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST code, got %q", resp.Code)
	}
	if resp.Retryable {
		t.Fatal("invalid request should not be retryable")
	}
}

func TestHandleStatusReturnsJSONNotFound(t *testing.T) {
	h := newHandler("", "", handlerOptions{startWorker: false})
	mux := http.NewServeMux()
	h.ServeHTTP(mux)

	req := httptest.NewRequest(http.MethodGet, "/status/missing", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != "JOB_NOT_FOUND" {
		t.Fatalf("expected JOB_NOT_FOUND code, got %q", resp.Code)
	}
}

func TestHandleTranscribeFallsBackWhenTitleFetchTimesOut(t *testing.T) {
	timeoutObserved := make(chan struct{}, 1)
	h := newHandler("", "", handlerOptions{
		startWorker:       false,
		maxQueuedJobs:     2,
		titleFetchTimeout: 5 * time.Millisecond,
		fetchTitle: func(ctx context.Context, url string) (string, error) {
			<-ctx.Done()
			timeoutObserved <- struct{}{}
			return "", ctx.Err()
		},
	})
	mux := http.NewServeMux()
	h.ServeHTTP(mux)

	req := httptest.NewRequest(http.MethodPost, "/transcribe", strings.NewReader(`{"url":"https://example.com/video"}`))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}

	var job Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if job.Title != job.URL {
		t.Fatalf("expected title fallback to URL %q, got %q", job.URL, job.Title)
	}
	if job.Status != "queued" {
		t.Fatalf("expected queued status, got %q", job.Status)
	}

	select {
	case <-timeoutObserved:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected title fetcher to observe context timeout")
	}
}

func TestHandleTranscribeRejectsWhenQueueIsFull(t *testing.T) {
	h := newHandler("", "", handlerOptions{
		startWorker:   false,
		maxQueuedJobs: 1,
		fetchTitle: func(context.Context, string) (string, error) {
			return "", nil
		},
	})
	mux := http.NewServeMux()
	h.ServeHTTP(mux)

	firstReq := httptest.NewRequest(http.MethodPost, "/transcribe", strings.NewReader(`{"url":"https://example.com/one"}`))
	firstRec := httptest.NewRecorder()
	mux.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("expected first request status %d, got %d", http.StatusAccepted, firstRec.Code)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/transcribe", strings.NewReader(`{"url":"https://example.com/two"}`))
	secondRec := httptest.NewRecorder()
	mux.ServeHTTP(secondRec, secondReq)

	if secondRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected second request status %d, got %d", http.StatusServiceUnavailable, secondRec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(secondRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != "QUEUE_FULL" {
		t.Fatalf("expected QUEUE_FULL code, got %q", resp.Code)
	}
	if !resp.Retryable {
		t.Fatal("queue full response should be retryable")
	}

	jobs := h.store.snapshot()
	if len(jobs) != 1 {
		t.Fatalf("expected only accepted jobs to remain stored, got %d", len(jobs))
	}
}

func TestNextJobIDIsUniqueWithinProcess(t *testing.T) {
	h := newHandler("", "", handlerOptions{startWorker: false})
	h.nextID.Store(41)

	first := h.nextJobID()
	second := h.nextJobID()

	if first == second {
		t.Fatal("expected unique job ids")
	}
	if first != "42" || second != "43" {
		t.Fatalf("expected sequential ids 42 and 43, got %q and %q", first, second)
	}
}
