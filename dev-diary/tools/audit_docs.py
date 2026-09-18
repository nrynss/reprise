#!/usr/bin/env python3
"""Audit the planning tree and exit nonzero on findings.

The loop cannot see what no task owns, and a plan that contradicts itself
hides work. This script lints the dev-diary tree so a broken reference or a
stale status is a failed check rather than a surprise.

Checks:
- every task id referenced in a requires line exists as a task
- no duplicate task ids across phase files
- status lines take only the documented values
- every phase file carries the three handoff headings
- relative markdown links inside the tree resolve on disk
- task ids referenced in README.md, project.md and libraries.md exist

Usage: audit_docs.py [--root DEV_DIARY_DIR]
The root defaults to the directory beside this script's parent.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

TASK_RE = re.compile(r"\bT(\d+)\.(\d+)([a-z]?)\b")
REQUIRES_RE = re.compile(r"^requires:\s*(.+)$", re.MULTILINE)
STATUS_RE = re.compile(r"^status:\s*(.+)$", re.MULTILINE)
HEADING_RE = re.compile(r"^###\s+(T\d+\.\d+[a-z]?)[:(]", re.MULTILINE)
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)]+)\)")

STATUS_VALUES = re.compile(
    r"^(not-started"
    r"|in-progress:implement:\S+"
    r"|in-progress:review-r\d+:\S+@\w+"
    r"|in-progress:remediate-r\d+:\S+"
    r"|in-progress:land:\S+@\w+"
    r"|done:\w+"
    r"|blocked:.+)$"
)

HANDOFF_HEADINGS = [
    "what exists now",
    "what surprised us",
    "notes for the next developer",
]

README_FILES = ("README.md", "project.md", "libraries.md")


def find_files(root: Path) -> tuple[list[Path], list[Path]]:
    """Return (phase_files, other_markdown_files) under root."""
    phase: list[Path] = []
    other: list[Path] = []
    for path in sorted(root.rglob("*.md")):
        if path.name.startswith("PHASE-"):
            phase.append(path)
        else:
            other.append(path)
    return phase, other


def defined_tasks(phase_files: list[Path]) -> dict[str, Path]:
    """Map each task id to the file that defines it."""
    tasks: dict[str, Path] = {}
    for path in phase_files:
        for match in HEADING_RE.finditer(path.read_text(encoding="utf-8")):
            tasks[match.group(1)] = path
    return tasks


def check_duplicate_ids(phase_files: list[Path], findings: list[str]) -> dict[str, Path]:
    seen: dict[str, Path] = {}
    for path in phase_files:
        for match in HEADING_RE.finditer(path.read_text(encoding="utf-8")):
            task_id = match.group(1)
            if task_id in seen:
                findings.append(
                    f"{path.name}: duplicate task id {task_id}, "
                    f"already defined in {seen[task_id].name}"
                )
            else:
                seen[task_id] = path
    return seen


def check_requires(phase_files: list[Path], tasks: dict[str, Path], findings: list[str]) -> None:
    for path in phase_files:
        text = path.read_text(encoding="utf-8")
        for match in REQUIRES_RE.finditer(text):
            value = match.group(1).strip()
            for ref in TASK_RE.findall(value):
                task_id = f"T{ref[0]}.{ref[1]}{ref[2]}"
                if task_id not in tasks:
                    findings.append(f"{path.name}: requires line cites unknown task {task_id}")


def check_statuses(phase_files: list[Path], findings: list[str]) -> None:
    for path in phase_files:
        text = path.read_text(encoding="utf-8")
        for match in STATUS_RE.finditer(text):
            value = match.group(1).strip()
            if not STATUS_VALUES.match(value):
                findings.append(f"{path.name}: status line takes an undocumented value: {value}")


def check_handoff_headings(phase_files: list[Path], findings: list[str]) -> None:
    for path in phase_files:
        text = path.read_text(encoding="utf-8").lower()
        for heading in HANDOFF_HEADINGS:
            if not any(
                line.strip().startswith("###") and heading in line
                for line in text.splitlines()
            ):
                findings.append(f"{path.name}: missing handoff heading {heading!r}")


def check_links(root: Path, files: list[Path], findings: list[str]) -> None:
    for path in files:
        text = path.read_text(encoding="utf-8")
        for match in LINK_RE.finditer(text):
            target = match.group(1).strip()
            if target.startswith(("http://", "https://", "mailto:", "#")):
                continue
            resolved = (path.parent / target.split("#")[0]).resolve()
            if not resolved.exists():
                findings.append(f"{path.name}: relative link does not resolve: {target}")


def check_named_references(root: Path, tasks: dict[str, Path], findings: list[str]) -> None:
    for name in README_FILES:
        path = root / name
        if not path.exists():
            findings.append(f"missing expected planning file {name}")
            continue
        for ref in TASK_RE.findall(path.read_text(encoding="utf-8")):
            task_id = f"T{ref[0]}.{ref[1]}{ref[2]}"
            if task_id not in tasks:
                findings.append(f"{name}: cites unknown task {task_id}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    default_root = Path(__file__).resolve().parent.parent
    parser.add_argument("--root", type=Path, default=default_root, help="dev-diary directory")
    args = parser.parse_args()

    root: Path = args.root.resolve()
    if not root.is_dir():
        print(f"error: root {root} is not a directory", file=sys.stderr)
        return 2

    findings: list[str] = []
    phase_files, other_files = find_files(root)
    if not phase_files:
        findings.append("no PHASE-*.md files found under root")

    tasks = check_duplicate_ids(phase_files, findings)
    check_requires(phase_files, tasks, findings)
    check_statuses(phase_files, findings)
    check_handoff_headings(phase_files, findings)
    check_links(root, phase_files + other_files, findings)
    check_named_references(root, tasks, findings)

    if findings:
        for finding in findings:
            print(f"FINDING: {finding}")
        print(f"{len(findings)} finding(s)")
        return 1
    print("OK planning tree is consistent")
    return 0


if __name__ == "__main__":
    sys.exit(main())
