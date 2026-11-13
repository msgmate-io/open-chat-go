#!/usr/bin/env python3
"""Contract check for a *real* identity provider deployment behind msgmate.io (ci#1061).

ci#1061 has two halves:

* the Helm chart side - the private chart's `identityProvider:` block, carried in
  this repository as the reviewable patch
  `development/helm-identity-provider/ci-1061-identity-provider.patch`. It points
  the backend at an external OIDC issuer and reads the client secret and the
  identity map from the `open-chat-identity-provider` Secret.
* the identity provider itself - the Keycloak realm template in
  `development/identity-provider/realm/msgmate-io-main-realm.json`, applied out of
  band to `id.msgmate.io` (production) and `id.stage.msgmate.io` (staging).

The two halves are edited in different repositories at different times, and every
mismatch between them only shows up as a browser-side `redirect_uri_mismatch` or
`invalid_client` during a live login. This checker pins the agreement statically:

  * the realm is `main` and defines the confidential `open-chat` client with the
    authorization-code flow, PKCE S256, no direct grants and no self-registration;
  * for every environment whose chart values enable the integration, the issuer
    is `https://id.<env host>/realms/main`, the client id matches the realm client
    and the exact `redirectURL` is registered in the realm;
  * nothing secret is committed: the realm only carries the documented
    placeholder secret, the values carry no credential at all (they reference the
    `existingSecret`, whose keys are wired with `secretKeyRef`), and the realm
    seeds no users.

Deliberately stdlib-only (no PyYAML): the Pin Freshness Gate CI job runs this with
the system `python3`, before the build-tools venv is guaranteed. Values are read
from the patch's added lines, which is exactly the reviewable surface.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]

# Repository-relative, so the checker can be pointed at a fixture tree in tests.
REALM_FILE = Path("development") / "identity-provider" / "realm" / "msgmate-io-main-realm.json"
CHART_PATCH = Path("development") / "helm-identity-provider" / "ci-1061-identity-provider.patch"
RUNBOOK = Path("development") / "identity-provider" / "README.md"

# The realm both environments authenticate against. Keycloak issuers end in
# /realms/<realm>, so the realm name is part of the issuer contract.
REALM_NAME = "main"
CLIENT_ID = "open-chat"

# The placeholder that must be replaced out of band. Committing anything else in
# the realm template means a credential is in the repository.
PLACEHOLDER_SECRET = "REPLACE_WITH_CLIENT_SECRET"

# environment -> (application host, identity provider host)
ENVIRONMENTS = {
    "production": ("msgmate.io", "id.msgmate.io"),
    "staging": ("stage.msgmate.io", "id.stage.msgmate.io"),
}

CALLBACK_PATH = "/api/v1/integrations/identity_provider/login/callback"

# Secret-bearing keys that must never appear in a values file.
FORBIDDEN_VALUE_KEYS = ("clientSecret", "clientSecretValue", "bootstrapJSON", "bootstrapJson")


class Findings(list):
    """A list of problems that also carries informational notes."""

    def __init__(self) -> None:
        super().__init__()
        self.notes: list[str] = []

    def note(self, message: str) -> None:
        self.notes.append(message)

    def fail(self, message: str) -> None:
        self.append(message)


def load_realm(path: Path) -> dict:
    with path.open(encoding="utf-8") as handle:
        return json.load(handle)


def parse_chart_patch(text: str) -> dict[str, dict[str, str]]:
    """Return ``{environment: {identityProvider key: value}}`` from the patch.

    Only *added* lines are considered (the patch is the record of the change), and
    only the values files one environment per file: the file name decides the
    environment, so a patch that forgets staging cannot silently pass.
    """
    environments: dict[str, dict[str, str]] = {}
    current: str | None = None
    block: dict[str, str] | None = None

    for line in text.splitlines():
        if line.startswith("+++ "):
            target = line[4:].strip()
            if target == "/dev/null":
                current, block = None, None
                continue
            path = target[2:] if target.startswith("b/") else target
            match = re.search(r"values\.([A-Za-z0-9._-]+)\.yaml$", path)
            if match:
                current = match.group(1)
                block = None
            elif re.search(r"values\.yaml$", path):
                # The chart default is `values.yaml`; it gates every environment.
                current = "default"
                block = None
            else:
                current, block = None, None
            continue

        if current is None:
            continue
        if not line.startswith("+") or line.startswith("+++"):
            continue
        added = line[1:]

        starter = re.match(r"^identityProvider:\s*$", added)
        if starter:
            block = {}
            environments[current] = block
            continue
        if block is None:
            continue

        entry = re.match(r"^  ([A-Za-z0-9_]+):\s*(.*?)\s*$", added)
        if entry:
            value = entry.group(2)
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            block[entry.group(1)] = value
            continue
        if not added.strip() or added.strip().startswith("#"):
            # Blank/comment lines inside the block keep it open.
            continue
        # Any other content ends the block (next top-level key).
        block = None

    # `values.msgmate-io-production.yaml` names the `production` environment, so the
    # normalized key is what ENVIRONMENTS is written against.
    return {_environment_name(name): values for name, values in environments.items()}


def _environment_name(value_file_suffix: str) -> str:
    prefix = "msgmate-io-"
    if value_file_suffix.startswith(prefix):
        return value_file_suffix[len(prefix):]
    return value_file_suffix


def _truthy(value: str | None) -> bool:
    return str(value).strip().lower() in {"true", "yes", "on", "1"}


def evaluate(realm: dict, chart_environments: dict[str, dict[str, str]]) -> Findings:
    findings = Findings()

    if realm.get("realm") != REALM_NAME:
        findings.fail(
            f"realm name is {realm.get('realm')!r}, expected {REALM_NAME!r} "
            "(the issuers end in /realms/{0})".format(REALM_NAME)
        )
    if realm.get("enabled") is not True:
        findings.fail("realm must be enabled")
    if realm.get("registrationAllowed") is not False:
        findings.fail("realm must keep registrationAllowed false (accounts live in the IdP, not self-service)")
    if realm.get("sslRequired") != "external":
        findings.fail("realm must require TLS for external requests (sslRequired: external)")
    if realm.get("bruteForceProtected") is not True:
        findings.fail("realm must enable bruteForceProtected - this is now a login credential surface")

    clients = {client.get("clientId"): client for client in realm.get("clients", [])}
    client = clients.get(CLIENT_ID)
    if client is None:
        findings.fail(f"realm must define the {CLIENT_ID!r} client")
        return findings

    if client.get("publicClient") is not False:
        findings.fail(f"{CLIENT_ID!r} must stay a confidential client (the backend keeps the secret)")
    if client.get("standardFlowEnabled") is not True:
        findings.fail(f"{CLIENT_ID!r} must enable the authorization-code flow")
    if client.get("implicitFlowEnabled") is not False:
        findings.fail(f"{CLIENT_ID!r} must keep the implicit flow disabled")
    if client.get("directAccessGrantsEnabled") is not False:
        findings.fail(f"{CLIENT_ID!r} must keep direct grants disabled (no password grant against the IdP)")
    if (client.get("attributes") or {}).get("pkce.code.challenge.method") != "S256":
        findings.fail(f"{CLIENT_ID!r} must require PKCE S256")

    if client.get("secret") != PLACEHOLDER_SECRET:
        findings.fail(
            f"{CLIENT_ID!r} client secret must stay the {PLACEHOLDER_SECRET!r} placeholder - "
            "a real secret in this file means a credential is committed"
        )

    if realm.get("users"):
        findings.fail("the committed realm template must not seed users - real accounts come from the IdP")
    findings.note(f"realm {REALM_NAME!r}: confidential client {CLIENT_ID!r}, PKCE S256, no seeded users")

    redirects = client.get("redirectUris") or []
    origins = client.get("webOrigins") or []

    # The chart default must stay disabled: only the environment overlays may turn
    # the integration on, otherwise every render (including local ones) points at
    # production's id. host.
    default_block = chart_environments.get("default")
    if default_block is not None and _truthy(default_block.get("enabled")):
        findings.fail(
            "the chart default values.yaml must keep identityProvider.enabled: false - "
            "only the msgmate.io overlays may enable it"
        )

    enabled = {
        name: values
        for name, values in chart_environments.items()
        if name != "default" and _truthy(values.get("enabled"))
    }
    if not enabled:
        findings.fail(
            "the chart patch enables the identity provider for no environment - ci#1061 is about "
            "wiring a real IdP to msgmate.io"
        )
        return findings

    for environment in sorted(enabled):
        values = enabled[environment]
        if environment not in ENVIRONMENTS:
            findings.fail(f"unknown environment {environment!r} in the chart patch")
            continue
        app_host, idp_host = ENVIRONMENTS[environment]

        issuer = values.get("issuer", "")
        expected_issuer = f"https://{idp_host}/realms/{REALM_NAME}"
        if issuer != expected_issuer:
            findings.fail(
                f"{environment}: issuer is {issuer!r}, expected {expected_issuer!r} "
                f"(id. must be its own host) - fix the values or the realm"
            )
        if not issuer.startswith("https://"):
            findings.fail(f"{environment}: issuer must be https, got {issuer!r}")

        if values.get("clientID") != CLIENT_ID:
            findings.fail(
                f"{environment}: clientID is {values.get('clientID')!r}, "
                f"the realm only defines {CLIENT_ID!r}"
            )

        if values.get("preset") not in (None, "", "keycloak"):
            findings.fail(
                f"{environment}: preset is {values.get('preset')!r} but the realm is a Keycloak realm"
            )

        redirect = values.get("redirectURL", "")
        if not redirect:
            findings.fail(f"{environment}: redirectURL must be set explicitly (request-base detection is the usual redirect_uri_mismatch)")
        else:
            if not redirect.startswith(f"https://{app_host}/"):
                findings.fail(f"{environment}: redirectURL {redirect!r} must point at https://{app_host}/")
            if not redirect.endswith(CALLBACK_PATH):
                findings.fail(f"{environment}: redirectURL {redirect!r} must end in {CALLBACK_PATH}")
            if redirect not in redirects:
                findings.fail(
                    f"{environment}: redirectURL {redirect!r} is not registered in the realm "
                    f"(registered: {redirects})"
                )
            origin = f"https://{app_host}"
            if origin not in origins:
                findings.fail(f"{environment}: webOrigins must include {origin!r}")

        secret_ref = values.get("existingSecret", "")
        if not secret_ref:
            findings.fail(f"{environment}: existingSecret must name the Secret holding client-secret/bootstrap-json")
        if _truthy(values.get("allowUnmapped")):
            findings.fail(
                f"{environment}: allowUnmapped must stay false - an unmapped identity would get an account "
                "on the public deployment"
            )
        for key in FORBIDDEN_VALUE_KEYS:
            if key in values:
                findings.fail(f"{environment}: {key!r} must never be set in a values file - keep it in the Secret")

        findings.note(
            f"{environment}: issuer {issuer} -> {CLIENT_ID} -> {redirect} via Secret {secret_ref}"
        )

    return findings


def check_repository(root: Path = ROOT) -> Findings:
    realm = load_realm(root / REALM_FILE)
    patch = (root / CHART_PATCH).read_text(encoding="utf-8")
    findings = evaluate(realm, parse_chart_patch(patch))
    if not (root / RUNBOOK).is_file():
        findings.fail(f"missing deployment runbook {RUNBOOK.name}")
    return findings


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--json", action="store_true", help="emit the report as JSON")
    args = parser.parse_args(argv)

    findings = check_repository()
    if args.json:
        print(json.dumps({"ok": not findings, "problems": list(findings), "notes": findings.notes}, indent=2))
    else:
        for note in findings.notes:
            print(f"ok: {note}")
        for problem in findings:
            print(f"FAIL: {problem}", file=sys.stderr)
        print("identity-provider realm/chart contract: " + ("ok" if not findings else "FAILED"))
    return 0 if not findings else 1


if __name__ == "__main__":
    raise SystemExit(main())
