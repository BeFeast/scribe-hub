package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var baseURL string

func init() {
	baseURL = os.Getenv("SCRIBE_HUB_URL")
	if baseURL == "" {
		baseURL = "http://localhost:18810"
	}
}

func main() {
	s := server.NewMCPServer(
		"scribe-hub",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	s.AddTool(
		mcp.NewTool("transcribe",
			mcp.WithDescription("Submit a video URL for transcription. Returns the queued job with its ID and queue position."),
			mcp.WithString("url", mcp.Required(), mcp.Description("Video URL to transcribe (YouTube or any yt-dlp-supported URL)")),
			mcp.WithBoolean("skip_summary", mcp.Description("Skip AI summary generation after transcription")),
			mcp.WithString("language", mcp.Description("Force language code (e.g. en, ru) or omit for auto-detection")),
			mcp.WithString("model_size", mcp.Description("Whisper model size (e.g. small, medium, large-v3)")),
		),
		handleTranscribe,
	)

	s.AddTool(
		mcp.NewTool("job_status",
			mcp.WithDescription("Check the status of a transcription job by its ID. Returns the job with current status, queue position, and output when complete."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Job ID (returned from transcribe)")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		handleJobStatus,
	)

	s.AddTool(
		mcp.NewTool("list_jobs",
			mcp.WithDescription("List transcription jobs, optionally filtered by status."),
			mcp.WithString("status", mcp.Description("Comma-separated status filter (e.g. queued,running,done,failed)")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		handleListJobs,
	)

	s.AddTool(
		mcp.NewTool("queue",
			mcp.WithDescription("Show the currently running job and all queued jobs with their positions."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		handleQueue,
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func handleTranscribe(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, err := req.RequireString("url")
	if err != nil {
		return errorResult(err.Error()), nil
	}

	body := map[string]any{"url": url}
	if v := req.GetBool("skip_summary", false); v {
		body["skip_summary"] = true
	}
	if v := req.GetString("language", ""); v != "" {
		body["language"] = v
	}
	if v := req.GetString("model_size", ""); v != "" {
		body["model_size"] = v
	}

	return doPost("/transcribe", body)
}

func handleJobStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return errorResult(err.Error()), nil
	}
	return doGet("/status/" + id)
}

func handleListJobs(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := "/jobs"
	if status := req.GetString("status", ""); status != "" {
		path += "?status=" + status
	}
	return doGet(path)
}

func handleQueue(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return doGet("/queue")
}

func doGet(path string) (*mcp.CallToolResult, error) {
	resp, err := http.Get(baseURL + path)
	if err != nil {
		return errorResult(fmt.Sprintf("HTTP request failed: %v", err)), nil
	}
	defer resp.Body.Close()
	return readResponse(resp)
}

func doPost(path string, body map[string]any) (*mcp.CallToolResult, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to marshal request: %v", err)), nil
	}

	resp, err := http.Post(baseURL+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return errorResult(fmt.Sprintf("HTTP request failed: %v", err)), nil
	}
	defer resp.Body.Close()
	return readResponse(resp)
}

func readResponse(resp *http.Response) (*mcp.CallToolResult, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read response: %v", err)), nil
	}

	// Pretty-print the JSON for readability
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		data = pretty.Bytes()
	}

	if resp.StatusCode >= 400 {
		return errorResult(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(data))), nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(string(data))},
	}, nil
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(msg)},
		IsError: true,
	}
}
