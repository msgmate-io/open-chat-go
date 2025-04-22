#!/usr/bin/env python3
"""Append/update Python client function docs in the docs MDX page.

This script reads method docstrings from `OpenChatPythonClient` and writes an
auto-generated API reference section into:
`frontend/pages/docs/content/python-client.mdx`.
"""

from __future__ import annotations

import ast
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CLIENT_FILE = ROOT / "clients" / "oc_python_client" / "client" / "client.py"
DOC_FILE = ROOT / "frontend" / "pages" / "docs" / "content" / "python-client.mdx"

START_MARKER = "{/* PYTHON_CLIENT_FUNCTIONS:START */}"
END_MARKER = "{/* PYTHON_CLIENT_FUNCTIONS:END */}"
LEGACY_START_MARKER = "<!-- PYTHON_CLIENT_FUNCTIONS:START -->"
LEGACY_END_MARKER = "<!-- PYTHON_CLIENT_FUNCTIONS:END -->"


def _src_segment(source: str, node: ast.AST | None) -> str:
    if node is None:
        return ""
    segment = ast.get_source_segment(source, node)
    if segment:
        return " ".join(segment.strip().split())
    return ""


def _format_signature(source: str, fn: ast.FunctionDef) -> str:
    args = fn.args
    parts: list[str] = []

    pos_args = list(args.posonlyargs) + list(args.args)
    if pos_args and pos_args[0].arg == "self":
        pos_args = pos_args[1:]

    positional_defaults = list(args.defaults)
    default_offset = len(pos_args) - len(positional_defaults)

    def format_arg(arg: ast.arg, default: ast.AST | None = None) -> str:
        text = arg.arg
        ann = _src_segment(source, arg.annotation)
        if ann:
            text = f"{text}: {ann}"
        if default is not None:
            default_text = _src_segment(source, default)
            if default_text:
                text = f"{text} = {default_text}"
        return text

    for i, arg in enumerate(pos_args):
        default_node = positional_defaults[i - default_offset] if i >= default_offset else None
        parts.append(format_arg(arg, default_node))

    if args.vararg is not None:
        text = f"*{args.vararg.arg}"
        ann = _src_segment(source, args.vararg.annotation)
        if ann:
            text = f"{text}: {ann}"
        parts.append(text)
    elif args.kwonlyargs:
        parts.append("*")

    for kwarg, kwdefault in zip(args.kwonlyargs, args.kw_defaults):
        parts.append(format_arg(kwarg, kwdefault))

    if args.kwarg is not None:
        text = f"**{args.kwarg.arg}"
        ann = _src_segment(source, args.kwarg.annotation)
        if ann:
            text = f"{text}: {ann}"
        parts.append(text)

    signature = f"{fn.name}({', '.join(parts)})"
    returns = _src_segment(source, fn.returns)
    if returns:
        signature = f"{signature} -> {returns}"
    return signature


def _extract_public_methods(source: str) -> list[tuple[str, str]]:
    tree = ast.parse(source)
    target_class: ast.ClassDef | None = None
    for node in tree.body:
        if isinstance(node, ast.ClassDef) and node.name == "OpenChatPythonClient":
            target_class = node
            break

    if target_class is None:
        raise RuntimeError("OpenChatPythonClient class not found")

    methods: list[tuple[str, str]] = []
    for node in target_class.body:
        if not isinstance(node, ast.FunctionDef):
            continue
        if node.name.startswith("_") and node.name != "__init__":
            continue
        signature = _format_signature(source, node)
        docstring = ast.get_docstring(node) or "No docstring provided."
        methods.append((signature, docstring.strip()))
    return methods


def _build_generated_block(methods: list[tuple[str, str]]) -> str:
    lines = [
        "## Python Client API Reference",
        "",
        "The list below is auto-generated from `OpenChatPythonClient` docstrings in `clients/oc_python_client/client/client.py`.",
        "",
        START_MARKER,
    ]

    for signature, docstring in methods:
        lines.append("")
        lines.append(f"### `{signature}`")
        lines.append("")
        lines.extend(docstring.splitlines())

    lines.append("")
    lines.append(END_MARKER)
    lines.append("")
    return "\n".join(lines)


def _update_docs_page(existing: str, generated: str) -> str:
    marker_pairs = [
        (START_MARKER, END_MARKER),
        (LEGACY_START_MARKER, LEGACY_END_MARKER),
    ]
    for start_marker, end_marker in marker_pairs:
        if start_marker in existing and end_marker in existing:
            start = existing.index(start_marker)
            end = existing.index(end_marker) + len(end_marker)
            if end < len(existing) and existing[end] == "\n":
                end += 1
            return existing[:start] + generated[generated.index(START_MARKER):] + existing[end:]

    return existing.rstrip() + "\n\n" + generated


def main() -> int:
    client_source = CLIENT_FILE.read_text(encoding="utf-8")
    methods = _extract_public_methods(client_source)
    if not methods:
        raise RuntimeError("No public methods found in OpenChatPythonClient")

    docs_source = DOC_FILE.read_text(encoding="utf-8")
    generated = _build_generated_block(methods)
    updated = _update_docs_page(docs_source, generated)
    DOC_FILE.write_text(updated, encoding="utf-8")

    print(f"Updated {DOC_FILE.relative_to(ROOT)} with {len(methods)} methods.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
