#!/usr/bin/env python3
"""Validate the SKILL.md files shipped by the plugins and seeded into a vault.

Checks, for every `plugins/*/skills/*/SKILL.md` and `template/.agents/skills/*/SKILL.md` on disk:

- YAML frontmatter parses.
- `name`: present, 1-64 chars, lowercase [a-z0-9-], matches the directory name.
- `description`: present, non-empty, max 1024 UTF-8 bytes.
- No two skills share a `name`.

Exits non-zero and prints a human-readable report on any failure.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("error: pyyaml is required. Install with `pip install pyyaml`.")


REPO_ROOT = Path(__file__).resolve().parent.parent
PLUGINS_DIR = REPO_ROOT / "plugins"
# The vault's own skills (compile-inbox, synthesize, …), seeded into a new vault by `kt init`. Only
# the repo-root template is linted — the embedded copy under cli/ is a byte-for-byte mirror (CI's
# drift guard keeps them identical), so globbing it too would just trip the duplicate-name check.
TEMPLATE_SKILLS_DIR = REPO_ROOT / "template" / ".agents" / "skills"

NAME_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,63}$")
NAME_MAX = 64
DESCRIPTION_MAX = 1024
FRONTMATTER_RE = re.compile(r"\A---\r?\n(.*?)\r?\n---\r?\n", re.DOTALL)


class Report:
    def __init__(self) -> None:
        self.errors: list[str] = []

    def error(self, scope: str, msg: str) -> None:
        self.errors.append(f"{scope}: {msg}")

    def ok(self) -> bool:
        return not self.errors


def parse_frontmatter(path: Path) -> dict | None:
    text = path.read_text(encoding="utf-8")
    match = FRONTMATTER_RE.match(text)
    if not match:
        return None
    try:
        data = yaml.safe_load(match.group(1))
    except yaml.YAMLError as exc:
        raise ValueError(f"invalid YAML: {exc}") from exc
    if not isinstance(data, dict):
        raise ValueError("frontmatter must be a YAML mapping")
    return data


def validate_skill(skill_md: Path, report: Report, expected_name: str) -> str | None:
    scope = str(skill_md.relative_to(REPO_ROOT))

    try:
        frontmatter = parse_frontmatter(skill_md)
    except ValueError as exc:
        report.error(scope, str(exc))
        return None

    if frontmatter is None:
        report.error(scope, "missing YAML frontmatter (must start with `---`)")
        return None

    name = frontmatter.get("name")
    description = frontmatter.get("description")

    if not isinstance(name, str) or not name:
        report.error(scope, "`name` is required and must be a non-empty string")
    else:
        if len(name) > NAME_MAX:
            report.error(scope, f"`name` is {len(name)} chars, max is {NAME_MAX}")
        if not NAME_RE.match(name):
            report.error(
                scope,
                "`name` must be lowercase letters, digits, and hyphens "
                "(starting with a letter or digit)",
            )
        if name != expected_name:
            report.error(
                scope,
                f"`name` is '{name}' but parent directory is '{expected_name}' — they must match",
            )

    if not isinstance(description, str) or not description.strip():
        report.error(scope, "`description` is required and must be a non-empty string")
    else:
        # Measure UTF-8 bytes, not code points: claude.ai's directory rejects a
        # skill whose description exceeds the byte limit, which silently drops the
        # whole plugin from the marketplace. A description full of em-dashes can
        # pass a code-point check while busting the byte budget.
        description_bytes = len(description.encode("utf-8"))
        if description_bytes > DESCRIPTION_MAX:
            report.error(
                scope,
                f"`description` is {description_bytes} bytes, max is {DESCRIPTION_MAX}",
            )

    return name if isinstance(name, str) else None


def main() -> int:
    report = Report()

    skill_files = sorted(PLUGINS_DIR.glob("*/skills/*/SKILL.md")) if PLUGINS_DIR.is_dir() else []
    if not skill_files:
        report.error("plugins/", "no SKILL.md files found under plugins/*/skills/*/")
    if TEMPLATE_SKILLS_DIR.is_dir():
        skill_files += sorted(TEMPLATE_SKILLS_DIR.glob("*/SKILL.md"))

    seen_names: set[str] = set()
    for skill_md in skill_files:
        dir_name = skill_md.parent.name
        name = validate_skill(skill_md, report, expected_name=dir_name)
        if name:
            if name in seen_names:
                report.error(str(skill_md.relative_to(REPO_ROOT)), f"duplicate skill name '{name}'")
            seen_names.add(name)

    if report.ok():
        print(f"ok — validated {len(skill_files)} skill(s)")
        return 0

    print("Skill validation failed:\n", file=sys.stderr)
    for err in report.errors:
        print(f"  - {err}", file=sys.stderr)
    print(f"\n{len(report.errors)} error(s)", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
