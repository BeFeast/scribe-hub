#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.10"
# dependencies = [
#   "google-genai>=1.0",
# ]
# ///

import argparse
import os
import re
import sys
import time
from datetime import date
from pathlib import Path

from google import genai

RETRYABLE_STATUS_CODES = {429, 500, 503}
MAX_RETRIES = 3
BACKOFF_SECONDS = [2, 4, 8]


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Summarize a transcript using the Gemini API and write a summary markdown file."
    )
    parser.add_argument(
        "--transcript-file",
        required=True,
        help="Path to transcript .md file",
    )
    parser.add_argument(
        "--output-file",
        required=True,
        help="Path where summary .md should be written",
    )
    parser.add_argument(
        "--prompt-template",
        required=True,
        help="Path to the prompt template file",
    )
    parser.add_argument(
        "--model",
        default="gemini-2.5-flash-lite",
        help="Gemini model to use (default: gemini-2.5-flash-lite)",
    )
    parser.add_argument(
        "--date",
        default=None,
        help="Date string YYYY-MM-DD for frontmatter (default: today)",
    )
    return parser


def extract_tags(summary_text: str) -> list[str]:
    """Extract tags from YAML frontmatter tags: [tag1, tag2, ...] line."""
    match = re.search(r"^tags:\s*\[([^\]]*)\]", summary_text, re.MULTILINE)
    if match:
        raw = match.group(1)
        return [t.strip().strip('"').strip("'") for t in raw.split(",") if t.strip()]
    return []


def count_key_points(summary_text: str) -> int:
    """Count bullet points under the key points section."""
    in_section = False
    count = 0
    for line in summary_text.splitlines():
        if re.match(r"^##\s+Ключевые моменты", line):
            in_section = True
            continue
        if in_section:
            if re.match(r"^##\s+", line):
                break
            if re.match(r"^\s*-\s+\*\*", line):
                count += 1
    return count


def call_gemini_with_retries(client: genai.Client, model: str, prompt: str) -> str:
    """Call Gemini API with retry logic for transient errors."""
    last_error = None
    for attempt in range(MAX_RETRIES + 1):
        try:
            response = client.models.generate_content(
                model=model,
                contents=prompt,
            )

            # Check for safety filter blocks
            if hasattr(response, "prompt_feedback") and response.prompt_feedback:
                feedback = response.prompt_feedback
                if hasattr(feedback, "block_reason") and feedback.block_reason:
                    print(
                        f"Error: Gemini blocked the request due to safety filter: {feedback.block_reason}",
                        file=sys.stderr,
                    )
                    sys.exit(2)

            text = response.text
            if not text or not text.strip():
                print("Error: Gemini returned empty response text", file=sys.stderr)
                sys.exit(2)

            return text

        except Exception as exc:
            last_error = exc
            # Check if this is a retryable HTTP error
            status_code = getattr(exc, "status_code", None) or getattr(
                getattr(exc, "response", None), "status_code", None
            )
            if status_code in RETRYABLE_STATUS_CODES and attempt < MAX_RETRIES:
                wait = BACKOFF_SECONDS[attempt]
                print(
                    f"Retryable error (HTTP {status_code}), retrying in {wait}s (attempt {attempt + 1}/{MAX_RETRIES})...",
                    file=sys.stderr,
                )
                time.sleep(wait)
                continue

            # Non-retryable or exhausted retries
            if attempt >= MAX_RETRIES:
                print(
                    f"Error: Gemini API failed after {MAX_RETRIES} retries: {exc}",
                    file=sys.stderr,
                )
            else:
                print(f"Error: Gemini API call failed: {exc}", file=sys.stderr)
            sys.exit(1)

    # Should not reach here, but just in case
    print(f"Error: Gemini API failed: {last_error}", file=sys.stderr)
    sys.exit(1)


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    # Validate GEMINI_API_KEY
    api_key = os.environ.get("GEMINI_API_KEY")
    if not api_key:
        print("Error: GEMINI_API_KEY environment variable is not set", file=sys.stderr)
        return 1

    # Resolve paths
    transcript_file = Path(args.transcript_file).expanduser().resolve()
    output_file = Path(args.output_file).expanduser().resolve()
    prompt_template_file = Path(args.prompt_template).expanduser().resolve()
    summary_date = args.date or date.today().isoformat()

    # Validate files exist
    if not transcript_file.is_file():
        print(
            f"Error: Transcript file does not exist: {transcript_file}", file=sys.stderr
        )
        return 1
    if not prompt_template_file.is_file():
        print(
            f"Error: Prompt template file does not exist: {prompt_template_file}",
            file=sys.stderr,
        )
        return 1

    # Read transcript
    print(f"Reading transcript: {transcript_file}", file=sys.stderr)
    transcript_content = transcript_file.read_text(encoding="utf-8")
    if not transcript_content.strip():
        print("Error: Transcript file is empty (0 chars)", file=sys.stderr)
        return 1
    print(f"Transcript length: {len(transcript_content)} chars", file=sys.stderr)

    # Read prompt template
    print(f"Reading prompt template: {prompt_template_file}", file=sys.stderr)
    prompt_template = prompt_template_file.read_text(encoding="utf-8")

    # Derive transcript slug for frontmatter source link
    transcript_slug = transcript_file.stem  # filename without .md

    # Fill template placeholders
    prompt_template = prompt_template.replace("{date}", summary_date)
    prompt_template = prompt_template.replace("{transcript_slug}", transcript_slug)
    # Tags placeholder will be filled by the model
    prompt_template = prompt_template.replace(
        "{tags}", "<auto-generated comma-separated tags>"
    )

    # Build prompt: template + inline transcript content
    prompt = prompt_template + "\n\nTranscript to summarize:\n\n" + transcript_content

    # Call Gemini
    print(f"Calling Gemini model '{args.model}'...", file=sys.stderr)
    client = genai.Client(api_key=api_key)
    summary_text = call_gemini_with_retries(client, args.model, prompt)
    print(f"Received summary: {len(summary_text)} chars", file=sys.stderr)

    # Strip code fences if Gemini wraps output in ```markdown ... ```
    summary_text = summary_text.strip()
    if summary_text.startswith("```"):
        lines = summary_text.splitlines()
        # Remove opening fence (```markdown or ```)
        lines = lines[1:]
        # Remove closing fence
        if lines and lines[-1].strip() == "```":
            lines = lines[:-1]
        summary_text = "\n".join(lines).strip()

    # Strip trailing machine-readable lines that Gemini may echo
    clean_lines = summary_text.splitlines()
    while clean_lines and re.match(
        r"^(SUMMARY_FILE|LANGUAGE|TAGS|KEY_POINTS):", clean_lines[-1]
    ):
        clean_lines.pop()
    summary_text = "\n".join(clean_lines).rstrip() + "\n"

    # Write output
    output_file.parent.mkdir(parents=True, exist_ok=True)
    output_file.write_text(summary_text, encoding="utf-8")
    print(f"Summary written to: {output_file}", file=sys.stderr)

    # Extract structured info
    tags = extract_tags(summary_text)
    key_points = count_key_points(summary_text)

    # Structured output to stdout (contract with shell script)
    print(f"SUMMARY_FILE:{output_file}")
    print("LANGUAGE:ru")
    print(f"TAGS:{','.join(tags)}")
    print(f"KEY_POINTS:{key_points}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
