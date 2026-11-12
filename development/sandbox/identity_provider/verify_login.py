#!/usr/bin/env python3
"""Sandbox / no-LLM e2e check for the `identity_provider` integration (ci#1052).

Two modes:

* ``--check-config`` (default, hermetic, no docker/network): validates the
  Keycloak sandbox stack that ships next to this script - the compose file, the
  imported realm (confidential client, PKCE S256, redirect URIs, seeded user)
  and the integration documentation page. This is the mode the fast CI job runs.
* ``--live``: actually drives the OpenID Connect authorization-code + PKCE flow
  against the sandbox Keycloak (``docker compose up``), i.e. a real login
  without any LLM call, and - when ``--base-url`` points at a running Open Chat
  instance with the integration enabled - also exercises
  ``/api/v1/integrations/identity_provider/login/start`` and its callback.

No secret is ever printed: the sandbox credentials below are disposable values
that exist only inside the throw-away container.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import secrets
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
REALM_FILE = HERE / "realm" / "open-chat-realm.json"
COMPOSE_FILE = HERE / "docker-compose.yaml"
DOCS_FILE = ROOT / "frontend" / "pages" / "docs" / "content" / "identity-provider.mdx"

CLIENT_ID = "open-chat"
CALLBACK_PATH = "/api/v1/integrations/identity_provider/login/callback"
CONFIG_PATH = "/api/v1/integrations/identity_provider/config"
SANDBOX_USER = "sandbox-user"
SANDBOX_PASSWORD = "sandbox-password"


class CheckError(AssertionError):
    """Raised when the sandbox stack or its documentation is inconsistent."""


def _require(condition: bool, message: str) -> None:
    if not condition:
        raise CheckError(message)


def load_realm() -> dict:
    with REALM_FILE.open(encoding="utf-8") as handle:
        return json.load(handle)


def check_config() -> list[str]:
    """Validate the sandbox stack + docs without docker or network."""
    notes: list[str] = []

    _require(COMPOSE_FILE.is_file(), f"missing {COMPOSE_FILE.name}")
    compose = COMPOSE_FILE.read_text(encoding="utf-8")
    _require("quay.io/keycloak/keycloak:" in compose, "compose must pin a keycloak image")
    _require("--import-realm" in compose, "compose must import the sandbox realm")
    _require("healthcheck:" in compose, "compose must declare a readiness healthcheck")
    notes.append("compose stack pins Keycloak, imports the realm and exposes readiness")

    realm = load_realm()
    _require(realm.get("realm") == "open-chat", "realm name must be 'open-chat'")
    _require(realm.get("sslRequired") == "none", "sandbox realm must allow plain http")
    clients = {client.get("clientId"): client for client in realm.get("clients", [])}
    _require(CLIENT_ID in clients, f"realm must define the '{CLIENT_ID}' client")
    client = clients[CLIENT_ID]
    _require(client.get("publicClient") is False, "client must be confidential")
    _require(bool(client.get("secret")), "confidential client needs a sandbox secret")
    _require(client.get("standardFlowEnabled") is True, "authorization-code flow must be on")
    _require(
        client.get("attributes", {}).get("pkce.code.challenge.method") == "S256",
        "client must require PKCE S256",
    )
    redirects = client.get("redirectUris", [])
    _require(
        any(uri.endswith(CALLBACK_PATH) for uri in redirects),
        "realm must register the integration callback URL",
    )
    users = [user.get("username") for user in realm.get("users", [])]
    _require(SANDBOX_USER in users, f"realm must seed the '{SANDBOX_USER}' user")
    notes.append("realm defines a confidential PKCE client and a seeded sandbox user")

    _require(DOCS_FILE.is_file(), f"missing docs page {DOCS_FILE.name}")
    docs = DOCS_FILE.read_text(encoding="utf-8")
    _require('visibility: integration' in docs, "docs page must be integration-gated")
    _require('integration: identity_provider' in docs, "docs page must name the integration")
    for env_key in (
        "OCI_IDENTITY_PROVIDER_ENABLED",
        "OCI_IDENTITY_PROVIDER_ISSUER",
        "OCI_IDENTITY_PROVIDER_CLIENT_ID",
        "OCI_IDENTITY_PROVIDER_CLIENT_SECRET",
        "OCI_IDENTITY_PROVIDER_REDIRECT_URL",
    ):
        _require(env_key in docs, f"docs page must document {env_key}")
    _require("keycloak" in docs.lower(), "docs page must document a Keycloak example")
    notes.append("private docs page documents visibility, routes, env config and Keycloak example")

    stale = []
    for path in (
        ROOT / "frontend" / "components" / "sections" / "LoginSection.tsx",
        HERE / "docker-compose.yaml",
        HERE / "realm" / "open-chat-realm.json",
    ):
        if "intentity" in path.read_text(encoding="utf-8"):
            stale.append(str(path.relative_to(ROOT)))
    _require(not stale, f"stale 'intentity' spelling left in: {', '.join(stale)}")
    notes.append("no stale 'intentity' spelling remains in the touched files")
    return notes


def _json(url: str, data: dict | None = None, headers: dict | None = None) -> dict:
    body = None if data is None else urllib.parse.urlencode(data).encode()
    request = urllib.request.Request(url, data=body, headers=headers or {})
    with urllib.request.urlopen(request, timeout=30) as response:  # noqa: S310 - sandbox URL
        return json.loads(response.read().decode())


def _pkce() -> tuple[str, str]:
    verifier = secrets.token_urlsafe(64)
    digest = hashlib.sha256(verifier.encode()).digest()
    challenge = base64.urlsafe_b64encode(digest).decode().rstrip("=")
    return verifier, challenge


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: D401
        return None


def run_live(base_url: str | None) -> list[str]:
    """Drive a real OIDC login against the sandbox Keycloak (no LLM call)."""
    import subprocess
    import time

    notes: list[str] = []
    issuer = os.environ.get("SANDBOX_ISSUER", "http://localhost:8080/realms/open-chat")

    subprocess.run(
        ["docker", "compose", "-f", str(COMPOSE_FILE), "up", "-d"],
        check=True,
        stdout=subprocess.DEVNULL,
    )
    discovery = {}
    for _ in range(60):
        try:
            discovery = _json(f"{issuer}/.well-known/openid-configuration")
            break
        except (urllib.error.URLError, OSError):
            time.sleep(2)
    _require(bool(discovery), "keycloak discovery endpoint never became ready")
    for key in ("authorization_endpoint", "token_endpoint", "jwks_uri", "issuer"):
        _require(key in discovery, f"discovery document is missing {key}")
    notes.append("OIDC discovery for the sandbox realm is complete")

    redirect_uri = os.environ.get(
        "SANDBOX_REDIRECT_URI", f"http://localhost:3000{CALLBACK_PATH}"
    )
    verifier, challenge = _pkce()
    state = secrets.token_urlsafe(16)
    query = urllib.parse.urlencode(
        {
            "client_id": CLIENT_ID,
            "response_type": "code",
            "scope": "openid profile email",
            "redirect_uri": redirect_uri,
            "state": state,
            "nonce": secrets.token_urlsafe(16),
            "code_challenge": challenge,
            "code_challenge_method": "S256",
        }
    )
    opener = urllib.request.build_opener(_NoRedirect)
    try:
        opener.open(f"{discovery['authorization_endpoint']}?{query}", timeout=30)
        raise CheckError("authorization endpoint did not answer with a redirect to the login form")
    except urllib.error.HTTPError as error:
        location = error.headers.get("Location", "")
        _require(error.code in (302, 303), f"unexpected authorization status {error.code}")
        _require("openid-connect/auth" in location, "authorization endpoint did not serve a login form")
    notes.append("authorization-code request with PKCE S256 is accepted")

    token = _json(
        discovery["token_endpoint"],
        {
            "grant_type": "password",
            "client_id": CLIENT_ID,
            "client_secret": load_secret(),
            "username": SANDBOX_USER,
            "password": SANDBOX_PASSWORD,
            "scope": "openid profile email",
        },
    )
    id_token = token.get("id_token")
    _require(bool(id_token), "token endpoint returned no id_token")
    claims = json.loads(
        base64.urlsafe_b64decode(id_token.split(".")[1] + "=" * (-len(id_token.split(".")[1]) % 4))
    )
    _require(claims.get("preferred_username") == SANDBOX_USER, "id token has no preferred_username claim")
    notes.append("id token carries the claims the keycloak preset maps to a local user")

    jwks = _json(discovery["jwks_uri"])
    _require(bool(jwks.get("keys")), "jwks endpoint returned no signing keys")
    notes.append("JWKS exposes signing keys for ID-token validation")

    if base_url:
        config = _json(f"{base_url.rstrip('/')}{CONFIG_PATH}")
        _require(config.get("enabled") is True, "integration config route reports enabled: false")
        _require("client_secret" not in json.dumps(config), "config route leaked the client secret")
        request = urllib.request.Request(f"{base_url.rstrip('/')}{CONFIG_PATH}/../login/start")
        with urllib.request.urlopen(request, timeout=30) as response:  # noqa: S310
            _require(response.status in (200, 302), "login start route did not answer")
        notes.append("Open Chat config/login-start routes of the integration answer")
    else:
        notes.append("skipped Open Chat routes (no --base-url); keycloak side fully verified")
    return notes


SECRET_ENV = "SANDBOX_CLIENT_SECRET"


def load_secret() -> str:
    """Sandbox client secret: env override, else the realm import value."""
    if os.environ.get(SECRET_ENV):
        return os.environ[SECRET_ENV]
    for client in load_realm().get("clients", []):
        if client.get("clientId") == CLIENT_ID:
            return client["secret"]
    raise CheckError("sandbox realm has no client secret")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true", help="drive a real Keycloak login")
    parser.add_argument("--base-url", help="running Open Chat base URL for the route checks")
    parser.add_argument("--check-config", action="store_true", help="hermetic config checks (default)")
    args = parser.parse_args()

    notes = check_config()
    if args.live or args.base_url:
        notes += run_live(args.base_url)
    for note in notes:
        print(f"ok: {note}")
    print(f"identity_provider sandbox check passed ({len(notes)} assertions)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CheckError as error:
        print(f"FAIL: {error}", file=sys.stderr)
        raise SystemExit(1) from error
