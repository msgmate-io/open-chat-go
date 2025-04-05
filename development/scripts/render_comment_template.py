#!/usr/bin/env python3

from pathlib import Path
import os
import sys


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: render_comment_template.py <template-path>", file=sys.stderr)
        return 2

    template_path = Path(sys.argv[1])
    if not template_path.exists():
        print(f"template not found: {template_path}", file=sys.stderr)
        return 1

    text = template_path.read_text()
    for key, value in os.environ.items():
        if key.startswith("TPL_"):
            text = text.replace("{{" + key[4:] + "}}", value)

    sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
