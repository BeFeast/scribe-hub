package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"ok.labs/scribe-hub/internal/transcriber"
)

func main() {
	port := flag.String("port", "18810", "HTTP port to listen on")
	flag.Parse()

	home := os.Getenv("HOME")
	scriptPath := filepath.Join(home, "Projects/video-summary/scripts/video-transcript-summary-faster-whisper.sh")
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
