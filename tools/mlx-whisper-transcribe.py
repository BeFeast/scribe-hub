#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.10"
# dependencies = [
#   "mlx-whisper>=0.4.0",
# ]
# ///

import argparse
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

import mlx_whisper


def slugify(value: str) -> str:
    slug = re.sub(r"[^a-zA-Z0-9]+", "-", value.strip().lower()).strip("-")
    return slug or "transcript"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Transcribe an audio file with mlx-whisper (Apple Silicon) and write a transcript markdown file."
    )
    parser.add_argument("--audio-file", required=True, help="Path to normalized input audio file")
    parser.add_argument("--output-dir", required=True, help="Directory where transcript markdown will be written")
    parser.add_argument("--basename", help="Output filename without .md")
    parser.add_argument("--title", help="Human-readable title used as transcript heading")
    parser.add_argument("--source-url", help="Original source URL to include in metadata")
    parser.add_argument(
        "--model-size",
        default="small",
        help="Whisper model size: tiny, base, small, medium, large-v3 (default: small)",
    )
    parser.add_argument(
        "--language",
        default="auto",
        help="Language code to force, or 'auto' for detection (default: auto)",
    )
    parser.add_argument(
        "--beam-size",
        type=int,
        default=5,
        help="Beam size for decoding (default: 5)",
    )
    parser.add_argument(
        "--overwrite",
        action="store_true",
        help="Overwrite transcript if it already exists",
    )
    return parser


def model_path(model_size: str) -> str:
    # mlx-community HF repos for each size
    mapping = {
        "tiny": "mlx-community/whisper-tiny-mlx",
        "base": "mlx-community/whisper-base-mlx",
        "small": "mlx-community/whisper-small-mlx",
        "medium": "mlx-community/whisper-medium-mlx",
        "large-v3": "mlx-community/whisper-large-v3-mlx",
    }
    return mapping.get(model_size, f"mlx-community/whisper-{model_size}-mlx")


def write_transcript(
    output_file: Path,
    *,
    title: str,
    source_url: str | None,
    audio_file: Path,
    model_size: str,
    language: str,
    probability: float | None,
    duration_seconds: float | None,
    transcript_text: str,
) -> None:
    generated_at = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    probability_text = "unknown" if probability is None else f"{probability:.3f}"
    duration_text = "unknown" if duration_seconds is None else f"{duration_seconds:.2f}s"

    metadata_lines = [
        f"- Source URL: {source_url or 'n/a'}",
        f"- Source audio: {audio_file}",
        f"- Transcription model: mlx-whisper ({model_size})",
        f"- Detected language: {language}",
        f"- Language probability: {probability_text}",
        f"- Duration: {duration_text}",
        f"- Generated at: {generated_at}",
    ]

    markdown = (
        f"# {title}\n\n"
        f"## Metadata\n"
        f"{chr(10).join(metadata_lines)}\n\n"
        f"## Transcript\n\n"
        f"{transcript_text}\n"
    )
    output_file.write_text(markdown, encoding="utf-8")


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    audio_file = Path(args.audio_file).expanduser().resolve()
    if not audio_file.is_file():
        parser.error(f"Audio file does not exist: {audio_file}")

    output_dir = Path(args.output_dir).expanduser().resolve()
    if not output_dir.is_dir():
        parser.error(f"Output directory does not exist: {output_dir}")

    title = (args.title or audio_file.stem).strip()
    basename = (args.basename or slugify(title)).strip()
    output_file = output_dir / f"{basename}.md"

    if output_file.exists() and not args.overwrite:
        parser.error(f"Transcript file already exists: {output_file}")

    language = None if args.language == "auto" else args.language

    print(f"Loading mlx-whisper model '{args.model_size}' on Apple Silicon...", file=sys.stderr)
    path = model_path(args.model_size)
    result = mlx_whisper.transcribe(
        str(audio_file),
        path_or_hf_repo=path,
        language=language,
        beam_size=args.beam_size,
    )

    segments = result.get("segments", [])
    transcript_text = " ".join(s["text"].strip() for s in segments if s.get("text", "").strip()).strip()
    detected_language = result.get("language") or "unknown"
    language_probability = None
    duration_seconds = segments[-1]["end"] if segments else None

    write_transcript(
        output_file,
        title=title,
        source_url=args.source_url,
        audio_file=audio_file,
        model_size=args.model_size,
        language=detected_language,
        probability=language_probability,
        duration_seconds=duration_seconds,
        transcript_text=transcript_text,
    )

    print(f"TRANSCRIPT_FILE:{output_file}")
    print(f"TITLE:{title}")
    print(f"DETECTED_LANGUAGE:{detected_language}")
    print(f"TRANSCRIPT_CHARACTERS:{len(transcript_text)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
