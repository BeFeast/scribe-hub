# Quickstart Guide

## Local development

### 1. Build and run

```bash
go build -o scribe-hub ./cmd/scribe-hub
./scribe-hub -port 8080
```

### 2. Verify it works

```bash
curl http://localhost:8080/health
# → {"status":"ok"}
```

### 3. Submit a test transcription

```bash
# Transcription only (no GEMINI_API_KEY needed)
curl -X POST http://localhost:8080/transcribe \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://www.youtube.com/watch?v=jNQXAC9IVRw", "skip_summary": true}'
```

Poll the returned job ID:

```bash
curl -s http://localhost:8080/status/<id> | jq '.status'
```

Or watch the queue in real time:

```bash
watch -n2 'curl -s http://localhost:8080/queue | jq'
```

### 4. Full pipeline with summarization

```bash
GEMINI_API_KEY=your-key ./scribe-hub -port 8080
```

```bash
curl -X POST http://localhost:8080/transcribe \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://www.youtube.com/watch?v=jNQXAC9IVRw"}'
```

Output goes to `$OBSIDIAN_FOLDER` (default `~/Documents/Digests/`): a transcript in `_transcripts/` and a summary markdown file alongside it.

---

## Deploying to a remote server

### 1. Build for the target platform

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o scribe-hub ./cmd/scribe-hub

# Linux ARM64 (e.g. Raspberry Pi, AWS Graviton)
GOOS=linux GOARCH=arm64 go build -o scribe-hub ./cmd/scribe-hub
```

### 2. Copy files to the server

```bash
rsync -av scribe-hub scripts/ tools/ user@server:/opt/scribe-hub/
```

The layout on the server:

```
/opt/scribe-hub/
  scribe-hub          # binary
  scripts/            # bash orchestrator
  tools/              # Python scripts + prompt templates
```

### 3. Install runtime dependencies

```bash
apt install ffmpeg python3
pip install yt-dlp
curl -LsSf https://astral.sh/uv/install.sh | sh
```

### 4. Run manually

```bash
export OBSIDIAN_FOLDER=/path/to/output
export GEMINI_API_KEY=your-key
export SCRIBE_SCRIPT_PATH=/opt/scribe-hub/scripts/video-transcript-summary-faster-whisper.sh

cd /opt/scribe-hub
./scribe-hub -port 18810
```

### 5. Run as a systemd service

Create `/etc/systemd/system/scribe-hub.service`:

```ini
[Unit]
Description=scribe-hub transcription service
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/scribe-hub
ExecStart=/opt/scribe-hub/scribe-hub -port 18810
Environment=OBSIDIAN_FOLDER=/path/to/output
Environment=GEMINI_API_KEY=your-key
Environment=SCRIBE_SCRIPT_PATH=/opt/scribe-hub/scripts/video-transcript-summary-faster-whisper.sh
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now scribe-hub
sudo systemctl status scribe-hub
```

### 6. Run as a macOS launchd service

Create `~/Library/LaunchAgents/com.scribe-hub.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.scribe-hub</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/scribe-hub/scribe-hub</string>
        <string>-port</string>
        <string>18810</string>
    </array>
    <key>WorkingDirectory</key>
    <string>/opt/scribe-hub</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>OBSIDIAN_FOLDER</key>
        <string>/Users/you/Documents/Digests</string>
        <key>GEMINI_API_KEY</key>
        <string>your-key</string>
        <key>SCRIBE_SCRIPT_PATH</key>
        <string>/opt/scribe-hub/scripts/video-transcript-summary-faster-whisper.sh</string>
    </dict>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/scribe-hub.out.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/scribe-hub.err.log</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.scribe-hub.plist
launchctl start com.scribe-hub
```

---

## Using from AI coding tools (MCP)

The MCP server runs locally on your machine and connects to scribe-hub over HTTP. All three config files are already in the repo — just set `SCRIBE_HUB_URL` to point at your server.

### Claude Code — `.mcp.json`

```json
{
  "mcpServers": {
    "scribe-hub": {
      "type": "stdio",
      "command": "go",
      "args": ["run", "./cmd/mcp-server"],
      "env": {
        "SCRIBE_HUB_URL": "http://your-server:18810"
      }
    }
  }
}
```

Run `/mcp` in Claude Code to verify the server is connected.

### Codex CLI — `.codex/config.toml`

```toml
[mcp_servers.scribe-hub]
command = "go"
args = ["run", "./cmd/mcp-server"]
enabled = true

[mcp_servers.scribe-hub.env]
SCRIBE_HUB_URL = "http://your-server:18810"
```

### OpenCode — `opencode.json`

```json
{
  "$schema": "https://opencode.ai/config.schema.json",
  "mcp": {
    "scribe-hub": {
      "command": "go",
      "args": ["run", "./cmd/mcp-server"],
      "env": {
        "SCRIBE_HUB_URL": "http://your-server:18810"
      }
    }
  }
}
```

### How it works

```
Claude Code / Codex / OpenCode
  → (stdio) → mcp-server (runs locally)
    → (HTTP) → scribe-hub (your server:18810)
```

Each tool picks up its config automatically when you open a project containing these files.

### Testing the MCP server manually

```bash
# Initialize + call the queue tool
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"queue","arguments":{}}}' \
  | SCRIBE_HUB_URL=http://your-server:18810 go run ./cmd/mcp-server
```

---

## Using from OpenClaw

OpenClaw supports two integration paths: a **Skill** (no code, uses the HTTP API directly) or **MCP** (reuses the Go MCP server).

### Option A: Skill (recommended)

Copy the skill into your OpenClaw workspace:

```bash
mkdir -p ~/.openclaw/workspace/skills/scribe-hub
cp docs/openclaw-skill/SKILL.md ~/.openclaw/workspace/skills/scribe-hub/SKILL.md
```

Then set the server URL in OpenClaw's config (`~/.openclaw/openclaw.json`):

```json
{
  "skills": {
    "scribe-hub": {
      "enabled": true,
      "config": {
        "scribe_hub_url": "http://your-server:18810"
      }
    }
  }
}
```

The skill teaches the agent to call scribe-hub's HTTP API using the built-in `web_fetch` tool — no extra binaries needed.

### Option B: MCP server

Add to `~/.openclaw/openclaw.json`:

```json
{
  "mcp": {
    "servers": {
      "scribe-hub": {
        "command": "go",
        "args": ["run", "./cmd/mcp-server"],
        "cwd": "/path/to/scribe-hub",
        "env": {
          "SCRIBE_HUB_URL": "http://your-server:18810"
        }
      }
    }
  }
}
```

This exposes the same four MCP tools (`transcribe`, `job_status`, `list_jobs`, `queue`) as Claude Code and Codex.
