#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.10"
# dependencies = [
#   "faster-whisper>=1.1.1",
# ]
# ///
# On Apple Silicon, mlx-whisper is attempted first (installed on demand via uv).
# Falls back to faster-whisper (CPU/CUDA) on any other platform or import error.

import argparse
import platform
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path


def slugify(value: str) -> str:
    slug = re.sub(r"[^a-zA-Z0-9]+", "-", value.strip().lower()).strip("-")
    return slug or "transcript"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Transcribe audio with mlx-whisper (Apple Silicon) or faster-whisper (CPU/CUDA)."
    )
    parser.add_argument("--audio-file", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--basename")
    parser.add_argument("--title")
    parser.add_argument("--source-url")
    parser.add_argument("--model-size", default="small",
                        help="Model size: tiny, base, small, medium, large-v3 (default: small)")
    parser.add_argument("--compute-type", default="int8",
                        help="faster-whisper compute type, ignored for mlx (default: int8)")
    parser.add_argument("--language", default="auto")
    parser.add_argument("--beam-size", type=int, default=5,
                        help="Beam size for faster-whisper, ignored for mlx (default: 5)")
    parser.add_argument("--overwrite", action="store_true")
    return parser


def _is_apple_silicon() -> bool:
    return platform.system() == "Darwin" and platform.machine() == "arm64"


def _mlx_model_path(model_size: str) -> str:
    mapping = {
        "tiny":    "mlx-community/whisper-tiny-mlx",
        "base":    "mlx-community/whisper-base-mlx",
        "small":   "mlx-community/whisper-small-mlx",
        "medium":  "mlx-community/whisper-medium-mlx",
        "large-v3": "mlx-community/whisper-large-v3-mlx",
    }
    return mapping.get(model_size, f"mlx-community/whisper-{model_size}-mlx")


def transcribe_mlx(audio_file: Path, model_size: str, language: str | None) -> dict:
    # Install mlx-whisper into the uv script env on demand
    subprocess.run(
        [sys.executable, "-m", "pip", "install", "--quiet", "mlx-whisper>=0.4.0"],
        check=True,
    )
    import mlx_whisper  # noqa: PLC0415
    print(f"Loading mlx-whisper model '{model_size}' on Apple Silicon...", file=sys.stderr)
    result = mlx_whisper.transcribe(str(audio_file), path_or_hf_repo=_mlx_model_path(model_size), language=language)
    segments = result.get("segments", [])
    return {
        "text": " ".join(s["text"].strip() for s in segments if s.get("text", "").strip()).strip(),
        "language": result.get("language") or "unknown",
        "language_probability": None,
        "duration_seconds": segments[-1]["end"] if segments else None,
        "backend": f"mlx-whisper ({model_size})",
    }


def transcribe_faster_whisper(audio_file: Path, model_size: str, compute_type: str,
                               language: str | None, beam_size: int) -> dict:
    from faster_whisper import WhisperModel  # noqa: PLC0415
    print(f"Loading faster-whisper model '{model_size}' on CPU ({compute_type})...", file=sys.stderr)
    model = WhisperModel(model_size, device="cpu", compute_type=compute_type)
    print("Running transcription...", file=sys.stderr)
    segments, info = model.transcribe(str(audio_file), language=language, beam_size=beam_size, vad_filter=True)
    collected = list(segments)
    text = " ".join(s.text.strip() for s in collected if s.text.strip()).strip()
    duration = max(s.end for s in collected) if collected else None
    return {
        "text": text,
        "language": getattr(info, "language", None) or "unknown",
        "language_probability": getattr(info, "language_probability", None),
        "duration_seconds": duration,
        "backend": f"faster-whisper ({model_size}, {compute_type})",
    }


def write_transcript(output_file: Path, *, title: str, source_url: str | None, audio_file: Path,
                     backend: str, language: str, probability: float | None,
                     duration_seconds: float | None, transcript_text: str) -> None:
    generated_at = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    probability_text = "unknown" if probability is None else f"{probability:.3f}"
    duration_text = "unknown" if duration_seconds is None else f"{duration_seconds:.2f}s"
    metadata_lines = [
        f"- Source URL: {source_url or 'n/a'}",
        f"- Source audio: {audio_file}",
        f"- Transcription model: {backend}",
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

    result = None
    if _is_apple_silicon():
        try:
            result = transcribe_mlx(audio_file, args.model_size, language)
        except Exception as e:
            print(f"mlx-whisper failed ({e}), falling back to faster-whisper...", file=sys.stderr)

    if result is None:
        result = transcribe_faster_whisper(audio_file, args.model_size, args.compute_type, language, args.beam_size)

    write_transcript(
        output_file,
        title=title,
        source_url=args.source_url,
        audio_file=audio_file,
        backend=result["backend"],
        language=result["language"],
        probability=result["language_probability"],
        duration_seconds=result["duration_seconds"],
        transcript_text=result["text"],
    )

    print(f"TRANSCRIPT_FILE:{output_file}")
    print(f"TITLE:{title}")
    print(f"DETECTED_LANGUAGE:{result['language']}")
    print(f"TRANSCRIPT_CHARACTERS:{len(result['text'])}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
