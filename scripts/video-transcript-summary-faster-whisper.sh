#!/usr/bin/env bash

set -euo pipefail
export PATH="/usr/local/bin:$HOME/.bun/bin:$HOME/.local/bin:$PATH"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
TRANSCRIBE_SCRIPT="$REPO_ROOT/tools/transcribe.py"
SUMMARIZE_SCRIPT="$REPO_ROOT/tools/gemini-summarize.py"
PROMPT_TEMPLATE_FILE="$REPO_ROOT/tools/prompts/transcript-summary-gemini.md"

SOURCE_URL=""
AUDIO_FILE=""
OUTPUT_DIR=""
BASENAME=""
TITLE=""
SUMMARY_BASENAME=""
MODEL_SIZE="${VIDEO_SUMMARY_MODEL_SIZE:-small}"
COMPUTE_TYPE="${VIDEO_SUMMARY_COMPUTE_TYPE:-int8}"
LANGUAGE="${VIDEO_SUMMARY_LANGUAGE:-auto}"
BEAM_SIZE="${VIDEO_SUMMARY_BEAM_SIZE:-5}"
SKIP_SUMMARY=0
OVERWRITE=0

usage() {
    cat <<'EOF'
Usage:
  video-transcript-summary-faster-whisper.sh [<youtube_url>] [options]
  video-transcript-summary-faster-whisper.sh (--url <youtube_url> | --audio-file <path>) [options]

Options:
  --basename <name>       Override transcript filename without .md
  --title <title>         Override transcript title
  --model-size <name>     faster-whisper model size (default: small)
  --compute-type <type>   faster-whisper compute type (default: int8)
  --language <code>       Force language code or use auto (default: auto)
  --beam-size <n>         Decoding beam size (default: 5)
  --output-dir <dir>      Output directory. If omitted, uses $OBSIDIAN_FOLDER/YYYY-MM-DD
  --skip-summary          Stop after transcript generation
  --overwrite             Overwrite an existing transcript file
  --help                  Show this message

Examples:
  video-transcript-summary-faster-whisper.sh 'https://www.youtube.com/watch?v=...'
  video-transcript-summary-faster-whisper.sh --audio-file '/path/to/audio.mp3' --skip-summary
EOF
}

require_command() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "Missing required command: $cmd" >&2
        exit 1
    fi
}

trim() {
    local value="$1"
    value="${value#${value%%[![:space:]]*}}"
    value="${value%${value##*[![:space:]]}}"
    printf '%s' "$value"
}

sanitize_title_for_filename() {
    python3 - "$1" <<'PY'
import re
import sys

value = sys.argv[1].strip()
value = re.sub(r'[\\/:*?"<>|]', ' ', value)
value = re.sub(r'\s+', ' ', value).strip().rstrip('.')
print(value or 'Untitled Video')
PY
}

slugify_title() {
    python3 - "$1" <<'PY'
import re
import sys
import unicodedata

value = unicodedata.normalize('NFKD', sys.argv[1]).encode('ascii', 'ignore').decode('ascii')
value = re.sub(r'[^a-zA-Z0-9]+', '-', value.strip().lower()).strip('-')
print(value or 'transcript')
PY
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --url)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            SOURCE_URL="$2"
            shift 2
            ;;
        --audio-file)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            AUDIO_FILE="$2"
            shift 2
            ;;
        --output-dir)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --basename)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            BASENAME="$2"
            shift 2
            ;;
        --title)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            TITLE="$2"
            shift 2
            ;;
        --model-size)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            MODEL_SIZE="$2"
            shift 2
            ;;
        --compute-type)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            COMPUTE_TYPE="$2"
            shift 2
            ;;
        --language)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            LANGUAGE="$2"
            shift 2
            ;;
        --beam-size)
            [[ $# -ge 2 ]] || { echo "Missing value for $1" >&2; exit 1; }
            BEAM_SIZE="$2"
            shift 2
            ;;
        --skip-summary)
            SKIP_SUMMARY=1
            shift
            ;;
        --overwrite)
            OVERWRITE=1
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            if [[ -z "$SOURCE_URL" && -z "$AUDIO_FILE" ]]; then
                SOURCE_URL="$1"
                shift
            else
                echo "Unknown argument: $1" >&2
                usage >&2
                exit 1
            fi
            ;;
    esac
done

if [[ -n "$SOURCE_URL" && -n "$AUDIO_FILE" ]]; then
    echo "Use either --url or --audio-file, not both." >&2
    exit 1
fi

if [[ -z "$SOURCE_URL" && -z "$AUDIO_FILE" ]]; then
    echo "One of --url or --audio-file is required." >&2
    exit 1
fi

if [[ -z "$OUTPUT_DIR" ]]; then
    if [[ -z "${OBSIDIAN_FOLDER:-}" ]]; then
        echo "--output-dir is required unless OBSIDIAN_FOLDER is set." >&2
        exit 1
    fi
    OUTPUT_DIR="${OBSIDIAN_FOLDER%/}/$(date +%F)"
fi

if [[ ! -f "$TRANSCRIBE_SCRIPT" ]]; then
    echo "Transcription script not found: $TRANSCRIBE_SCRIPT" >&2
    exit 1
fi

if [[ ! -f "$PROMPT_TEMPLATE_FILE" ]]; then
    echo "Prompt template not found: $PROMPT_TEMPLATE_FILE" >&2
    exit 1
fi

require_command uv
require_command ffmpeg

if [[ -n "$SOURCE_URL" ]]; then
    require_command yt-dlp
fi

if [[ "$SKIP_SUMMARY" -eq 0 && -z "${GEMINI_API_KEY:-}" ]]; then
    echo "GEMINI_API_KEY is required for summarization (use --skip-summary to skip)" >&2
    exit 1
fi

mkdir -p "$OUTPUT_DIR"

WORK_DIR="$(mktemp -d)"
cleanup() {
    rm -rf "$WORK_DIR"
}
trap cleanup EXIT

LOCAL_AUDIO_FILE="$AUDIO_FILE"

if [[ -n "$SOURCE_URL" ]]; then
    echo "Fetching YouTube title..." >&2
    if [[ -z "$TITLE" ]]; then
        TITLE="$(yt-dlp --get-title --no-playlist "$SOURCE_URL" | tail -n 1)"
    fi
    TITLE="$(trim "$TITLE")"

    echo "Downloading audio..." >&2
    LOCAL_AUDIO_FILE="$(yt-dlp \
        -x \
        --audio-format mp3 \
        --restrict-filenames \
        --no-playlist \
        -o "$WORK_DIR/%(title)s.%(ext)s" \
        --print after_move:filepath \
        "$SOURCE_URL" | tail -n 1)"
fi

if [[ ! -f "$LOCAL_AUDIO_FILE" ]]; then
    echo "Audio file was not found after preparation: $LOCAL_AUDIO_FILE" >&2
    exit 1
fi

if [[ -z "$TITLE" ]]; then
    TITLE="$(basename "$LOCAL_AUDIO_FILE")"
    TITLE="${TITLE%.*}"
fi
TITLE="$(sanitize_title_for_filename "$(trim "$TITLE")")"
if [[ -z "$BASENAME" ]]; then
    BASENAME="$(slugify_title "$TITLE")"
fi
SUMMARY_BASENAME="$TITLE"
TRANSCRIPTS_DIR="$OUTPUT_DIR/_transcripts"
mkdir -p "$TRANSCRIPTS_DIR"

NORMALIZED_AUDIO_FILE="$WORK_DIR/input-16k.wav"
echo "Normalizing audio with ffmpeg..." >&2
ffmpeg -hide_banner -loglevel error -y -i "$LOCAL_AUDIO_FILE" -ar 16000 -ac 1 "$NORMALIZED_AUDIO_FILE"

TRANSCRIBE_OUTPUT_FILE="$WORK_DIR/transcribe-output.txt"

TRANSCRIBE_ARGS=(
    "$TRANSCRIBE_SCRIPT"
    --audio-file "$NORMALIZED_AUDIO_FILE"
    --output-dir "$TRANSCRIPTS_DIR"
    --model-size "$MODEL_SIZE"
    --compute-type "$COMPUTE_TYPE"
    --language "$LANGUAGE"
    --beam-size "$BEAM_SIZE"
)

if [[ -n "$BASENAME" ]]; then
    TRANSCRIBE_ARGS+=(--basename "$BASENAME")
fi

if [[ -n "$TITLE" ]]; then
    TRANSCRIBE_ARGS+=(--title "$TITLE")
fi

if [[ -n "$SOURCE_URL" ]]; then
    TRANSCRIBE_ARGS+=(--source-url "$SOURCE_URL")
fi

if [[ "$OVERWRITE" -eq 1 ]]; then
    TRANSCRIBE_ARGS+=(--overwrite)
fi

echo "Transcribing audio with whisper (${MODEL_SIZE})..." >&2
uv run "${TRANSCRIBE_ARGS[@]}" >"$TRANSCRIBE_OUTPUT_FILE"

TRANSCRIPT_FILE="$(awk -F: '/^TRANSCRIPT_FILE:/{sub(/^TRANSCRIPT_FILE:/,""); print; exit}' "$TRANSCRIBE_OUTPUT_FILE")"

if [[ -z "$TRANSCRIPT_FILE" || ! -f "$TRANSCRIPT_FILE" ]]; then
    echo "Failed to determine transcript file path." >&2
    cat "$TRANSCRIBE_OUTPUT_FILE" >&2
    exit 1
fi

cat "$TRANSCRIBE_OUTPUT_FILE"

TRANSCRIPT_CHARS="$(awk -F: '/^TRANSCRIPT_CHARACTERS:/{print $2; exit}' "$TRANSCRIBE_OUTPUT_FILE")"
if [[ "${TRANSCRIPT_CHARS:-0}" -eq 0 ]]; then
    echo "Empty transcript (no speech detected) — skipping summary" >&2
    exit 1
fi

if [[ "$SKIP_SUMMARY" -eq 1 ]]; then
    exit 0
fi

TRANSCRIPT_FILENAME="$(basename "$TRANSCRIPT_FILE")"
TRANSCRIPT_STEM="${TRANSCRIPT_FILENAME%.md}"
SUMMARY_FILE="$OUTPUT_DIR/${SUMMARY_BASENAME}.md"

echo "Generating summary via Gemini API..." >&2
GEMINI_OUTPUT="$(
    uv run "$SUMMARIZE_SCRIPT" \
        --transcript-file "$TRANSCRIPT_FILE" \
        --output-file "$SUMMARY_FILE" \
        --prompt-template "$PROMPT_TEMPLATE_FILE" \
        --date "$(date +%F)"
)"
printf '%s\n' "$GEMINI_OUTPUT"

if [[ -f "$SUMMARY_FILE" ]]; then
    find "$TRANSCRIPTS_DIR" -maxdepth 1 -type f -name "* — Summary.md" -delete
fi

python3 - "$SUMMARY_FILE" "$TRANSCRIPT_FILE" "$TRANSCRIPT_STEM" <<'PY'
from pathlib import Path
import sys


def split_frontmatter(text: str):
    if not text.startswith('---\n'):
        return None, text
    lines = text.splitlines(keepends=True)
    if not lines or lines[0] != '---\n':
        return None, text
    for idx in range(1, len(lines)):
        if lines[idx].strip() == '---':
            frontmatter = ''.join(lines[:idx + 1])
            body = ''.join(lines[idx + 1:])
            return frontmatter, body
    return None, text



def extract_or_build_summary_frontmatter(content: str):
    if content.startswith('---\n'):
        frontmatter, body = split_frontmatter(content)
        if frontmatter is not None:
            return frontmatter, body

    marker = '\n---\n'
    marker_idx = content.find(marker)
    if marker_idx != -1:
        end_idx = content.find('\n---\n', marker_idx + len(marker))
        if end_idx != -1:
            frontmatter_start = marker_idx + 1
            frontmatter_end = end_idx + len('\n---\n')
            frontmatter = content[frontmatter_start:frontmatter_end]
            prefix = content[:frontmatter_start]
            suffix = content[frontmatter_end:]
            body = prefix + suffix
            return frontmatter, body

    return '---\ntype: summary\n---\n', content.lstrip('\n')



def ensure_summary_format(summary_path: Path, transcript_slug: str):
    link_line = f"> Transcript: [[_transcripts/{transcript_slug}]]"
    content = summary_path.read_text(encoding='utf-8')
    frontmatter, body = extract_or_build_summary_frontmatter(content)

    body_lines = body.splitlines()
    filtered_lines = []
    for line in body_lines:
        if line.strip().startswith('> Transcript: [['):
            continue
        filtered_lines.append(line)

    while filtered_lines and filtered_lines[0].strip() == '':
        filtered_lines.pop(0)

    rebuilt_body = '\n'.join(filtered_lines)
    updated = f"{frontmatter}\n{link_line}\n"
    if rebuilt_body:
        updated += f"\n{rebuilt_body}"
    if not updated.endswith('\n'):
        updated += '\n'
    summary_path.write_text(updated, encoding='utf-8')


summary_path = Path(sys.argv[1])
transcript_path = Path(sys.argv[2])
transcript_slug = sys.argv[3]
ensure_summary_format(summary_path, transcript_slug)

transcript_content = transcript_path.read_text(encoding='utf-8')
transcript_frontmatter, transcript_body = split_frontmatter(transcript_content)
if transcript_frontmatter is None and transcript_content.startswith('> Transcript: [['):
    lines = transcript_content.splitlines()
    non_link_lines = [line for line in lines if not line.strip().startswith('> Transcript: [[')]
    while non_link_lines and non_link_lines[0].strip() == '':
        non_link_lines.pop(0)
    updated_transcript = '---\ntype: transcript\n---\n'
    if non_link_lines:
        updated_transcript += '\n' + '\n'.join(non_link_lines)
    if not updated_transcript.endswith('\n'):
        updated_transcript += '\n'
    transcript_path.write_text(updated_transcript, encoding='utf-8')
PY

echo "SUMMARY_FILE:$SUMMARY_FILE"
