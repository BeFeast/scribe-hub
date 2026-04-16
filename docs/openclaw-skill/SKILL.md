---
name: scribe-hub
description: Submit YouTube videos for transcription and summarization via scribe-hub
config_types:
  scribe_hub_url:
    type: string
    description: Base URL of the scribe-hub server
    default: "http://localhost:18810"
---

# scribe-hub transcription skill

You have access to a scribe-hub transcription server at `{{config.scribe_hub_url}}`.

## Capabilities

- Submit YouTube (or any yt-dlp-supported) URLs for transcription
- Check job status and queue position
- List all jobs with optional status filtering
- View the current queue

## How to use

### Submit a transcription

Use `web_fetch` to POST to the server:

```
POST {{config.scribe_hub_url}}/transcribe
Content-Type: application/json

{
  "url": "<video-url>",
  "skip_summary": false,
  "language": "en",
  "model_size": "large-v3"
}
```

Only `url` is required. The response includes a job ID and queue position.

Set `skip_summary` to `true` if the user only wants the transcript without an AI summary.

### Check job status

```
GET {{config.scribe_hub_url}}/status/<job-id>
```

Returns the job with its current status: `queued`, `running`, `done`, or `failed`.

### List jobs

```
GET {{config.scribe_hub_url}}/jobs
GET {{config.scribe_hub_url}}/jobs?status=queued,running
```

### View queue

```
GET {{config.scribe_hub_url}}/queue
```

Shows the currently running job and all queued jobs.

### Health check

```
GET {{config.scribe_hub_url}}/health
```

## Workflow

When a user asks to transcribe a video:

1. Submit the URL via `POST /transcribe`
2. Report the job ID and queue position
3. If the user wants to wait, poll `GET /status/<id>` every 10-15 seconds until the status is `done` or `failed`
4. Report the result including the output file paths
