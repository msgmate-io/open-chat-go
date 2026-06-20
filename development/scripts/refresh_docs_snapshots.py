#!/usr/bin/env python3
"""
Refresh docs static snapshots defined in docs MDX frontmatter.

Reads `frontend/pages/docs/content/*.mdx`, extracts:
- snapshotApis
- snapshotOutputPath
- snapshotScript (optional local script returning JSON payload on stdout)

Then calls each configured API and writes a deterministic JSON snapshot.

Auth uses OPEN_CHAT_SESSION_ID if provided.
Otherwise it performs login with hardcoded defaults:
- ADMIN_USER=admin
- ADMIN_PASSWORD=password
"""

from __future__ import annotations

import glob
import json
import os
import re
import subprocess
import sys
from datetime import datetime, timezone
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
DOCS_GLOB = ROOT / "frontend" / "pages" / "docs" / "content" / "*.mdx"
ADMIN_USER = "admin"
ADMIN_PASSWORD = "password"


@dataclass
class SnapshotDoc:
    mdx_path: Path
    snapshot_apis: list[str]
    output_path: str
    snapshot_key: str
    snapshot_script: str


def parse_frontmatter(text: str) -> dict[str, object]:
    if not text.startswith("---\n"):
        return {}
    end = text.find("\n---\n", 4)
    if end == -1:
        return {}
    block = text[4:end]
    out: dict[str, object] = {}
    current_list_key: str | None = None
    for raw_line in block.splitlines():
        line = raw_line.rstrip()
        list_item = re.match(r"^\s*-\s*(.+)$", line)
        if list_item and current_list_key:
            out.setdefault(current_list_key, [])
            cast_list = out[current_list_key]
            if isinstance(cast_list, list):
                cast_list.append(list_item.group(1).strip())
            continue

        m = re.match(r"^([A-Za-z0-9_]+):\s*(.*)$", line)
        if not m:
            current_list_key = None
            continue
        key, value = m.group(1), m.group(2).strip()
        if value == "":
            out[key] = []
            current_list_key = key
        else:
            out[key] = value
            current_list_key = None
    return out


def discover_snapshot_docs() -> list[SnapshotDoc]:
    docs: list[SnapshotDoc] = []
    for file_str in glob.glob(str(DOCS_GLOB)):
        path = Path(file_str)
        text = path.read_text(encoding="utf-8")
        fm = parse_frontmatter(text)
        apis_raw = fm.get("snapshotApis", [])
        apis = [str(x).strip() for x in apis_raw] if isinstance(apis_raw, list) else []
        out = str(fm.get("snapshotOutputPath", "")).strip()
        snapshot_key = str(fm.get("snapshotKey", "")).strip() or path.stem
        snapshot_script = str(fm.get("snapshotScript", "")).strip()
        if out and (apis or snapshot_script):
            docs.append(
                SnapshotDoc(
                    mdx_path=path,
                    snapshot_apis=apis,
                    output_path=out,
                    snapshot_key=snapshot_key,
                    snapshot_script=snapshot_script,
                )
            )
    return docs


def api_request(method: str, url: str, session_id: str) -> dict:
    req = urllib.request.Request(url=url, method=method)
    req.add_header("Cookie", f"session_id={session_id}")
    req.add_header("Accept", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read().decode("utf-8")
            return json.loads(body) if body else {}
    except urllib.error.HTTPError as err:
        body = err.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {url} -> {err.code}: {body}") from err


def build_snapshot_payload(snapshot_doc: SnapshotDoc, host: str, session_id: str) -> dict:
    if snapshot_doc.snapshot_script:
        script_path = ROOT / snapshot_doc.snapshot_script
        if not script_path.exists():
            raise RuntimeError(f"snapshotScript does not exist: {script_path}")
        completed = subprocess.run(
            [sys.executable, str(script_path), "--stdout"],
            cwd=str(ROOT),
            check=True,
            capture_output=True,
            text=True,
        )
        stdout = completed.stdout.strip()
        if not stdout:
            raise RuntimeError(f"snapshotScript returned empty output: {snapshot_doc.snapshot_script}")
        return json.loads(stdout)

    responses = []
    for api in snapshot_doc.snapshot_apis:
        url = api if api.startswith("http://") or api.startswith("https://") else f"{host}{api}"
        responses.append((api, api_request("GET", url, session_id)))

    response_map = {api: payload for api, payload in responses}

    if snapshot_doc.snapshot_key == "models-overview":
        table_entries = response_map.get("/api/v1/admin/tables", [])
        table_names = [str(entry.get("name", "")).strip() for entry in table_entries if isinstance(entry, dict)]
        table_payloads = []
        for table_name in sorted([name for name in table_names if name]):
            table_url = f"{host}/api/v1/admin/table/{table_name}?full=1"
            table_payloads.append(api_request("GET", table_url, session_id))

        sql_payload = response_map.get("/api/v1/admin/schema/sql", {})
        relations = []
        if isinstance(sql_payload, dict):
            for rel in sql_payload.get("relations", []):
                if not isinstance(rel, dict):
                    continue
                relations.append(
                    {
                        "fromTable": str(rel.get("from_table", "")),
                        "fromField": str(rel.get("from_field", "")),
                        "toTable": str(rel.get("to_table", "")),
                        "toField": str(rel.get("to_field", "")),
                    }
                )

            return {
                "tables": table_payloads,
                "relations": relations,
                "sql": str(sql_payload.get("sql", "")),
                "generated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
            }

    if snapshot_doc.snapshot_key == "server-command":
        return {
            "server_doc": response_map.get("/api/v1/admin/docs/tag/open-chat-server-command-options"),
            "provider_doc": response_map.get("/api/v1/admin/docs/tag/open-chat-provider-env-vars"),
            "generated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        }

    if len(responses) == 1:
        return responses[0][1]

    return {
        "generated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "apis": {api: payload for api, payload in responses},
    }


def login_and_get_session(host: str, username: str, password: str) -> str:
    url = f"{host}/api/v1/user/login"
    payload = json.dumps({"email": username, "password": password}).encode("utf-8")
    req = urllib.request.Request(url=url, method="POST", data=payload)
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "application/json")

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            set_cookie_headers = resp.headers.get_all("Set-Cookie") or []
            for header in set_cookie_headers:
                if header.startswith("session_id="):
                    return header.split(";", 1)[0].split("=", 1)[1]
    except urllib.error.HTTPError as err:
        body = err.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"POST {url} -> {err.code}: {body}") from err

    raise RuntimeError("Login succeeded but no session_id cookie was returned")


def main() -> int:
    host = os.environ.get("OPEN_CHAT_HOST", "http://127.0.0.1:1984").rstrip("/")
    session_id = os.environ.get("OPEN_CHAT_SESSION_ID", "").strip()
    if not session_id:
        print(f"OPEN_CHAT_SESSION_ID not set. Logging in with ADMIN_USER='{ADMIN_USER}'.")
        session_id = login_and_get_session(host, ADMIN_USER, ADMIN_PASSWORD)
        print("Login successful, acquired temporary session.")

    docs = discover_snapshot_docs()
    if not docs:
        print("No snapshot-enabled docs pages found.")
        return 0

    print(f"Found {len(docs)} snapshot-enabled docs page(s).")
    failures = 0

    for doc in docs:
        print(f"\n- {doc.mdx_path.relative_to(ROOT)}")
        print(f"\n- {doc.mdx_path.relative_to(ROOT)}")
        if doc.snapshot_script:
            print(f"  snapshotScript: {doc.snapshot_script}")
        if doc.snapshot_apis:
            print(f"  snapshotApis: {len(doc.snapshot_apis)} endpoint(s)")
            for api in doc.snapshot_apis:
                print(f"    - {api}")
        print(f"  output:      {doc.output_path}")

        try:
            payload = build_snapshot_payload(doc, host, session_id)
            payload["generated_at"] = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")

            out_abs = ROOT / doc.output_path
            out_abs.parent.mkdir(parents=True, exist_ok=True)
            out_abs.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
            print(f"  refreshed:   wrote {out_abs.stat().st_size} bytes")
            print(f"  file:        {out_abs}")
        except Exception as exc:  # noqa: BLE001
            failures += 1
            print(f"  FAILED: {exc}")

    if failures:
        print(f"\nCompleted with {failures} failure(s).")
        return 1

    print("\nAll snapshots refreshed successfully.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
