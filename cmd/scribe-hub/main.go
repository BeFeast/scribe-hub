package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"ok.labs/scribe-hub/internal/transcriber"
)

func defaultScriptPath() string {
	if v := os.Getenv("SCRIBE_SCRIPT_PATH"); v != "" {
		return v
	}
	// Try to resolve relative to the executable location first.
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "..", "scripts", "video-transcript-summary-faster-whisper.sh")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	// Fallback: relative to working directory.
	return "scripts/video-transcript-summary-faster-whisper.sh"
}

func main() {
	port := flag.String("port", "18810", "HTTP port to listen on")
	flag.Parse()

	home := os.Getenv("HOME")
	scriptPath := defaultScriptPath()
	logPath := filepath.Join(home, "logs/scribe-hub.log")

	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		log.Fatalf("failed to create log dir: %v", err)
	}

	mux := http.NewServeMux()
	h := transcriber.NewHandler(scriptPath, logPath)
	h.ServeHTTP(mux)

	log.Printf("scribe-hub listening on :%s", *port)
	if err := http.ListenAndServe(":"+*port, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
