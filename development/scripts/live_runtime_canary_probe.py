# Live Runtime Canary probe: resolve a bot and create an interaction against a
# live Open-Chat deployment, so the post-merge `live-canary` check can tell a
# server/create-interaction failure apart from a canary-script parsing bug
# (open-chat-go-ci#854, open-chat-go-ci#865).
#
# The canary, not the bot, is under test here: the probe performs the exact
# request the Live Runtime Canary makes and prints a self-contained verdict
# (HTTP status, method/path, and the JSON envelope if the body parses) instead
# of dying in json.loads() on an empty or plain-text body.
# Environment (never secrets, credentials or user data):
#   OC_PROBE_BASE_URL    e.g. https://ci.msgmate.io   (required)
#   OC_PROBE_BOT_IDENTIFIER  e.g. msgmate-coder-bot   (required; the bot's UUID
#                          or owner-scoped name, as bot-identity resolution
#                          uses it)
#   OC_PROBE_OWNER_USER   the owner user the identifier is scoped to, when the
#                          identifier is an owner-scoped name (required)
#   OC_PROBE_DRY_RUN      set to "1" to only resolve and check HTTP reachability
#                          (no interaction is created)
# Exit codes: 0 = interaction created (or dry run resolved), 2 = probe misuse,
# 3 = bot/path-level failure, 4 = create-interaction failure.

import json
import os
import sys
import urllib.error
import urllib.request

# The live-runtime permissions in effect (theme: observe-and-report only), so
# an unexpected environment is impossible to miss:
#   OAUTH_TOKEN, CORESO_TOKEN, BFB GROUP, LAUNCHD_PAYLOADS — not used (unset).


def _fail(code: int, message: str) -> int:
    print(message)
    return code


def _request(url: str) -> tuple:
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            body = resp.read().decode("utf-8", "replace")
            return resp.status, dict(resp.headers), body
    except urllib.error.HTTPError as err:
        return err.code, dict(err.headers), err.read().decode("utf-8", "replace")
    except urllib.error.URLError as err:
        return 0, {}, f"urlopen error: {err.reason}"


def _envelope(status: int, headers: dict, body: str):
    if status == 0:
        # A transport-level failure (connection refused, DNS, TLS) is not an
        # HTTP answer at all, so it is reported as a reachability failure.
        return None
    if status >= 400 or not body:
        # The API always emits a JSON envelope on failure, so a missing/empty
        # body or a plain-text one is itself a server-side bug to report in the
        # verdict instead of crashing the canary (open-chat-go-ci#865).
        return None
    try:
        return json.loads(body)
    except (ValueError, json.JSONDecodeError):
        return None


def main(argv) -> int:
    base_url = os.environ.get("OC_PROBE_BASE_URL", "").strip().rstrip("/")
    bot = os.environ.get("OC_PROBE_BOT_IDENTIFIER", "").strip()
    owner = os.environ.get("OC_PROBE_OWNER_USER", "").strip()
    dry_run = os.environ.get("OC_PROBE_DRY_RUN") == "1"
    if not base_url or not bot:
        return _fail(2, "usage: OC_PROBE_BASE_URL and OC_PROBE_BOT_IDENTIFIER are required")
    if not dry_run and not owner:
        return _fail(2, "usage: OC_PROBE_OWNER_USER is required when not a dry run")

    # --- step 1: resolve the bot (bot-identity probe from ci#854) -------------
    identifier = f"{owner}/{bot}" if not dry_run and owner else bot
    resolve_url = f"{base_url}/api/v1/bots/{identifier}"
    status, headers, body = _request(resolve_url)
    envelope = _envelope(status, headers, body)
    if status == 0 or status >= 400 or envelope is None:
        return _fail(
            3,
            f"[probe] bot resolve FAILED status={status} url={resolve_url} "
            f"body={body!r} (bot identity, not create-interaction)",
        )
    print(f"[probe] bot resolve OK status={status} url={resolve_url} body={body[:300]!r}")
    if dry_run:
        print("[probe] dry run: not creating an interaction")
        return 0

    # --- step 2: create the interaction (ci#865 interaction/backend path) -----
    create_url = f"{base_url}/api/v1/bots/{identifier}/interactions"
    payload = json.dumps(
        {
            "message": "[live-canary] runtime probe: verify the interaction/backend path (ci#865)",
            "tags": ["live-canary", "ci#865"],
        }
    ).encode("utf-8")
    req = urllib.request.Request(
        create_url, data=payload, method="POST",
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            status = resp.status
            headers = dict(resp.headers)
            body = resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as err:
        status = err.code
        headers = dict(err.headers)
        body = err.read().decode("utf-8", "replace")
    except urllib.error.URLError as err:
        status = 0
        headers = {}
        body = f"urlopen error: {err.reason}"

    envelope = _envelope(status, headers, body)
    if status >= 400 or envelope is None:
        return _fail(
            4,
            f"[probe] create-interaction FAILED status={status} url={create_url} "
            f"body={body!r} "
            f"(interaction/backend path, not bot identity; this is the real 865 failure)",
        )
    print(
        f"[probe] create-interaction OK status={status} url={create_url} "
        f"envelope={envelope}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))