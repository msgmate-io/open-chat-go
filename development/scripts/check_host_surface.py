#!/usr/bin/env python3
"""Pin the stable Host surface of the integration interface (ci#423).

``integrationinterface`` is the compile input every integration shares, so its
Host capability surface is guarded: ``fs.FS`` / ``http.ServeMux`` (route and
asset registration stays on ``Definition``) and ``gorm.DB`` / ``gorm.io/gorm``
(the persistence handle must stay opaque) may not grow. The leaks that remain
today are the *migration backlog* and are pinned in
``integrations.host-surface.json`` so they can only shrink. The guard also pins
the interface module's direct requirements (compile-input stability) and the
``HostVersion`` constant.

  python3 development/scripts/check_host_surface.py --baseline integrations.host-surface.json
  python3 development/scripts/check_host_surface.py --write-baseline integrations.host-surface.json
  python3 development/scripts/check_host_surface.py --check-baseline integrations.host-surface.json
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys

TOKENS = ("fs.FS", "http.ServeMux", "gorm.DB", "gorm.io/gorm")
SURFACE = os.path.join("clients", "go_integration_interface", "integrationinterface")
GOMOD = os.path.join("clients", "go_integration_interface", "go.mod")
BASELINE = "integrations.host-surface.json"
HOST_VERSION = re.compile(r"^const\s+HostVersion\s*=\s*(\d+)", re.M)


def repo_root():
    """Walk up from this file until the checkout root is found."""
    here = os.path.dirname(os.path.abspath(__file__))
    while True:
        if os.path.isdir(os.path.join(here, ".git")) or os.path.exists(os.path.join(here, "DEVELOPMENT.md")):
            return here
        parent = os.path.dirname(here)
        if parent == here:
            return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        here = parent


def scan(root):
    directory = os.path.join(root, SURFACE)
    files, version = {}, None
    for name in sorted(os.listdir(directory)) if os.path.isdir(directory) else []:
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        text = open(os.path.join(directory, name)).read()
        if version is None:
            found = HOST_VERSION.search(text)
            if found:
                version = int(found.group(1))
        code = "\n".join(line.split("//")[0] for line in text.splitlines())
        leaks = {token: code.count(token) for token in TOKENS if code.count(token)}
        if leaks:
            files[name] = leaks
    requirements = []
    gomod = os.path.join(root, GOMOD)
    if os.path.exists(gomod):
        for raw in open(gomod):
            if "// indirect" in raw:
                continue
            parts = raw.split("//")[0].strip().removeprefix("require ").strip("()").split()
            if len(parts) == 2 and parts[1].startswith("v"):
                requirements.append(f"{parts[0]} {parts[1]}")
    return {
        "version": 1,
        "host_version": version,
        "files": files,
        "requirements": sorted(requirements),
    }


def gate(current, baseline):
    errors, notes = [], []
    for name in sorted(current["files"]):
        leaks = current["files"][name]
        pinned = baseline["files"].get(name)
        if pinned is None:
            errors.append(f"{name}: unpinned surface leak {leaks}")
            continue
        for token, count in sorted(leaks.items()):
            allowed = pinned.get(token, 0)
            if count > allowed:
                errors.append(f"{name}: {token} grew {allowed} -> {count}")
            elif count < allowed:
                notes.append(f"{name}: {token} shrank {allowed} -> {count}")
        for token, allowed in sorted(pinned.items()):
            if token not in leaks:
                notes.append(f"{name}: {token} shrank {allowed} -> 0")
    for name in sorted(baseline["files"]):
        if name not in current["files"]:
            errors.append(f"{name}: pinned leak is gone; refresh the baseline")
    if current["host_version"] != baseline["host_version"]:
        errors.append(f"HostVersion {baseline['host_version']} -> {current['host_version']}")
    added = [r for r in current["requirements"] if r not in baseline["requirements"]]
    dropped = [r for r in baseline["requirements"] if r not in current["requirements"]]
    if added:
        errors.append("interface module gained direct requirements: " + ", ".join(added))
    if dropped:
        notes.append("interface module dropped direct requirements: " + ", ".join(dropped))
    return errors, notes


def validate(baseline):
    errors = []
    if baseline.get("version") != 1:
        errors.append("version must be 1")
    if not isinstance(baseline.get("host_version"), int) or baseline["host_version"] <= 0:
        errors.append("host_version must be a positive int")
    files = baseline.get("files")
    if not isinstance(files, dict):
        errors.append("files must be an object")
    else:
        for name, leaks in files.items():
            if not name.endswith(".go") or name.endswith("_test.go"):
                errors.append(f"{name}: not a surface file")
            if not isinstance(leaks, dict) or not leaks:
                errors.append(f"{name}: leaks must be a non-empty object")
                continue
            for token, count in leaks.items():
                if token not in TOKENS:
                    errors.append(f"{name}: unknown token {token}")
                if not isinstance(count, int) or count <= 0:
                    errors.append(f"{name}: {token} count must be a positive int")
    requirements = baseline.get("requirements")
    if not isinstance(requirements, list) or not requirements:
        errors.append("requirements must be a non-empty list")
    else:
        for entry in requirements:
            if not isinstance(entry, str) or len(entry.split()) != 2:
                errors.append(f"requirements: malformed entry {entry!r}")
    return errors


def load(path):
    with open(path) as handle:
        return json.load(handle)


def report(errors, notes):
    for note in notes:
        print(f"note: {note}")
    ci = os.environ.get("GITHUB_ACTIONS") == "true"
    for error in errors:
        print(f"::error::{error}" if ci else f"error: {error}")
    return 1 if errors else 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Guard the stable Host surface (ci#423).")
    parser.add_argument("--root", default=repo_root())
    parser.add_argument("--baseline", nargs="?", const=BASELINE)
    parser.add_argument("--write-baseline", metavar="FILE")
    parser.add_argument("--check-baseline", metavar="FILE")
    args = parser.parse_args(argv)

    if args.check_baseline:
        print(f"checking baseline {args.check_baseline}")
        return report(validate(load(args.check_baseline)), [])

    if not os.path.isdir(os.path.join(args.root, SURFACE)):
        print(f"error: {SURFACE} is missing (interface submodule not checked out)")
        return 1

    current = scan(args.root)
    leaks = sum(sum(entry.values()) for entry in current["files"].values())
    print(
        f"host surface: {len(current['files'])} file(s) with {leaks} pinned leak(s), "
        f"HostVersion={current['host_version']}, {len(current['requirements'])} direct requirement(s)"
    )

    if args.write_baseline:
        with open(args.write_baseline, "w") as handle:
            json.dump(current, handle, indent=2, sort_keys=True)
            handle.write("\n")
        print(f"wrote {args.write_baseline}")
        return 0

    path = args.baseline if args.baseline is not None else (BASELINE if os.path.exists(BASELINE) else None)
    if path is None:
        print("no baseline committed yet; report only")
        return 0
    baseline = load(path)
    errors = validate(baseline)
    if errors:
        print(f"::error::malformed baseline {path}" if os.environ.get("GITHUB_ACTIONS") == "true" else f"error: malformed baseline {path}")
        return report(errors, [])
    return report(*gate(current, baseline))


if __name__ == "__main__":
    sys.exit(main())
