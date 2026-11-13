#!/usr/bin/env python3
"""ci#1044 - prove a deployment-profile BOT_TOKEN carries the capabilities it needs.

Why this exists
---------------
ci#1043 taught the CI-side deploy to *survive* a stale per-deployment vault
`BOT_TOKEN`: `scripts/resolve-github-bot-token.sh` probes every candidate before
`actions/checkout` runs, names the rejected one and continues on the CI-secret
fallback. That keeps deployments green but cannot say *why* the vault item is
rejected, and it only reports the first missing capability (`contents:read`) -
the token may still be missing `packages:*` or `pull_requests:write`, which fail
one deploy step later, in the middle of a run.

This script is the issuance-time counterpart: point it at a **candidate** token
(the one about to be stored as the profile's `BOT_TOKEN`) and it prints one
verdict per required capability, naming what is missing and what the deploy step
that needs it does. Run it before writing the item to the vault, and again after
a rotation; it needs no cluster, no Helm and no deployment run.

Required capability set (ci#1044)
---------------------------------
| capability            | used by                                            |
| --------------------- | -------------------------------------------------- |
| `contents:read`       | `actions/checkout` of the deploy target            |
| `pull_requests:write` | posting the preview-URL comment back to the PR     |
| `packages:read`       | pulling `pr-<n>-<sha>` images from `ghcr.io`       |
| `packages:write`      | pushing/manifesting those images during the build  |

all on `msgmate-io/open-chat-go` / `ghcr.io/msgmate-io/open-chat-go`, and the
token must be a **fine-grained** PAT (`github_pat_...`) whose selected-repository
list includes `msgmate-io/open-chat-go`: a token scoped to a different repository
of the same organisation reproduces exactly the `git` exit 128 seen in run
`42959618854`, because GitHub answers the read with HTTP 404 instead of 403.

Honest verdicts
---------------
Three verdicts, and only one of them is fatal:

* `OK`          - probed successfully.
* `MISSING`     - proven absent; ``main()`` exits non-zero.
* `UNVERIFIED`  - cannot be probed with a read-only request. A fine-grained PAT
                  exposes no "can I write?" endpoint, so `pull_requests:write`
                  is only proven functionally, by the deploy commenting the
                  preview URL back to the source PR. Reported, never fatal, so
                  the check cannot pretend to more certainty than it has.

Usage
-----
    BOT_TOKEN_CANDIDATE=... python3 development/scripts/check_bot_token_capabilities.py
    python3 development/scripts/check_bot_token_capabilities.py \
        --observations-json '{"contents_status": 200, ...}'   # offline

The candidate is read from `--token-file`, `BOT_TOKEN_CANDIDATE` or
`BOT_TOKEN` (in that order) and is **never** printed: only status codes,
capability names and the token *shape* reach stdout. `/dev/null`-style masking is
not enough for a log, so the value is never formatted into an f-string at all.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any, Dict, List, Optional, Tuple

API_HOST = "https://api.github.com"
GHCR_HOST = "https://ghcr.io"
DEPLOY_REPO = "msgmate-io/open-chat-go"
DEPLOY_PACKAGE = "msgmate-io/open-chat-go"
DEPLOY_REF = "dev"
DEPLOY_PROFILES = ("msgmate-io-pr-deployment", "msgmate-io-staging")

OK = "OK"
MISSING = "MISSING"
UNVERIFIED = "UNVERIFIED"

FINE_GRAINED = "fine-grained"
CLASSIC = "classic"
GITHUB_APP = "github-app"
UNKNOWN_SHAPE = "unknown"

# The exact capability list ci#1044 requires of the profile's BOT_TOKEN.
CAPABILITIES = (
    "token shape",
    "contents:read",
    "pull_requests:read",
    "pull_requests:write",
    "packages:read",
    "packages:write",
)


class Finding:
    """One capability verdict. Deliberately tiny: no credential ever enters it."""

    __slots__ = ("capability", "verdict", "detail")

    def __init__(self, capability: str, verdict: str, detail: str) -> None:
        self.capability = capability
        self.verdict = verdict
        self.detail = detail

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return "Finding(%r, %r, %r)" % (self.capability, self.verdict, self.detail)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Finding):
            return NotImplemented
        return (self.capability, self.verdict, self.detail) == (
            other.capability,
            other.verdict,
            other.detail,
        )


def strip_credential(value: Optional[str]) -> str:
    """Drop CR/LF and surrounding whitespace from a credential (copy/paste hygiene)."""
    v = (value or "").replace("\r", "").replace("\n", "")
    return v.strip()


def token_shape(token: str) -> str:
    """Classify the credential by its prefix, without revealing any of it."""
    v = strip_credential(token)
    if v.startswith("github_pat_"):
        return FINE_GRAINED
    if v.startswith("ghp_"):
        return CLASSIC
    if v.startswith(("ghs_", "ghu_")):
        return GITHUB_APP
    return UNKNOWN_SHAPE


def explain_status(status: Any) -> str:
    """The named, actionable classification of a GitHub HTTP status."""
    try:
        code = int(status)
    except (TypeError, ValueError):
        code = 0
    if code == 200:
        return "HTTP 200"
    if code == 401:
        return "HTTP 401 - the credential is invalid, expired or revoked"
    if code == 403:
        return ("HTTP 403 - the credential lacks the scope, or the API rate limit "
                "is exhausted")
    if code == 404:
        return ("HTTP 404 - the credential cannot see the repository/package "
                "(scope missing, or the fine-grained token does not select it)")
    if code == 0:
        return "no response from the registry/API"
    return "HTTP %s" % code


def _access_actions(access: Any, package: str) -> Tuple[Optional[set], str]:
    """Actions GHCR granted for `repository:<package>` in a registry token response."""
    if not isinstance(access, list):
        return None, "the registry answered without a scope list (unexpected payload)"
    for entry in access:
        if not isinstance(entry, dict):
            continue
        if (entry.get("type") == "repository"
                and entry.get("name") == package):
            actions = entry.get("actions")
            if isinstance(actions, list):
                return {str(a) for a in actions}, ""
            return set(), "the registry granted the repository with no actions"
    return None, "the registry granted no scope for repository:%s" % package


def evaluate(observations: Dict[str, Any]) -> List[Finding]:
    """Pure verdict function over the probe results (no network, no credential).

    Recognised keys: `token_shape`, `contents_status`, `pulls_status`,
    `registry_status`, `registry_access` (the `access` list of a GHCR token
    response) and `package`.
    """
    package = str(observations.get("package") or DEPLOY_PACKAGE)
    findings: List[Finding] = []

    shape = str(observations.get("token_shape") or UNKNOWN_SHAPE)
    if shape == FINE_GRAINED:
        findings.append(Finding(
            "token shape", OK,
            "fine-grained PAT; its selected-repository list must include %s" % DEPLOY_REPO,
        ))
    else:
        findings.append(Finding(
            "token shape", MISSING,
            "%s token - ci#1044 requires a fine-grained `github_pat_` token whose "
            "selected repositories include %s; a token scoped to another repository "
            "of the organisation reproduces the git exit 128 of run 42959618854"
            % (shape, DEPLOY_REPO),
        ))

    contents_status = observations.get("contents_status")
    if str(contents_status) == "200":
        findings.append(Finding("contents:read", OK,
                                "%s; enough for `actions/checkout` of the deploy target"
                                % explain_status(contents_status)))
    else:
        findings.append(Finding(
            "contents:read", MISSING,
            "%s - `git` then fails with exit 128 "
            "(\"could not read Username for 'https://github.com'\")"
            % explain_status(contents_status),
        ))

    pulls_status = observations.get("pulls_status")
    if str(pulls_status) == "200":
        findings.append(Finding("pull_requests:read", OK, explain_status(pulls_status)))
    else:
        findings.append(Finding(
            "pull_requests:read", MISSING,
            "%s - the deploy reads the source PR before commenting the preview URL"
            % explain_status(pulls_status),
        ))

    # No read-only endpoint can prove a write capability on a fine-grained PAT:
    # say so instead of guessing. The deploy's preview-URL comment is the probe.
    findings.append(Finding(
        "pull_requests:write", UNVERIFIED,
        "fine-grained PATs expose no read-only write probe; proven by the deploy "
        "commenting the preview URL back to the source PR",
    ))

    registry_status = observations.get("registry_status")
    if str(registry_status) != "200":
        reason = explain_status(registry_status)
        findings.append(Finding("packages:read", MISSING,
                                "GHCR token exchange: %s" % reason))
        findings.append(Finding("packages:write", MISSING,
                                "GHCR token exchange: %s" % reason))
    else:
        actions, note = _access_actions(observations.get("registry_access"), package)
        if actions is None:
            findings.append(Finding(
                "packages:read", MISSING,
                "%s; packages:read/write missing, or the token does not select "
                "ghcr.io/%s" % (note, package),
            ))
            findings.append(Finding(
                "packages:write", MISSING,
                "%s; the deploy pulls pr-<n>-<sha> images from ghcr.io/%s"
                % (note, package),
            ))
        else:
            for capability, action, use in (
                ("packages:read", "pull", "pulling pr-<n>-<sha> images"),
                ("packages:write", "push", "pushing/manifesting those images"),
            ):
                if action in actions:
                    findings.append(Finding(
                        capability, OK,
                        "ghcr.io/%s grants %s (%s)"
                        % (package, ", ".join(sorted(actions)), use),
                    ))
                else:
                    findings.append(Finding(
                        capability, MISSING,
                        "ghcr.io/%s grants '%s' but not '%s' (%s)"
                        % (package, ", ".join(sorted(actions)), action, use),
                    ))
    return findings


def problems(findings: List[Finding]) -> List[Finding]:
    """The fatal subset: proven-missing capabilities only."""
    return [f for f in findings if f.verdict == MISSING]


def failed(findings: List[Finding]) -> bool:
    return bool(problems(findings))


def render(findings: List[Finding], token_shape_hint: str = "") -> str:
    """Human/greppable report; contains no credential, only verdicts."""
    lines = ["BOT_TOKEN capability report for %s (ref %s)" % (DEPLOY_REPO, DEPLOY_REF)]
    if token_shape_hint:
        lines.append("credential shape: %s" % token_shape_hint)
    width = max(len(c) for c in CAPABILITIES)
    for finding in findings:
        lines.append("  %-*s  %-10s  %s"
                     % (width, finding.capability, finding.verdict, finding.detail))
    return "\n".join(lines)


class Probe:
    """The network half. Everything it learns is a status code or a public list."""

    def __init__(self, token: str, repo: str = DEPLOY_REPO,
                 package: str = DEPLOY_PACKAGE, ref: str = DEPLOY_REF,
                 login: str = "") -> None:
        self._token = strip_credential(token)
        self.repo = repo
        self.package = package
        self.ref = ref
        self.login = login or "x-access-token"

    def _headers(self) -> Dict[str, str]:
        return {
            "Authorization": "Bearer %s" % self._token,
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            "User-Agent": "open-chat-go-ci#1044-capability-check",
        }

    def github_status(self, path: str) -> int:
        url = API_HOST + path
        request = urllib.request.Request(url, headers=self._headers(), method="GET")
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                return int(getattr(response, "status", response.getcode()))
        except urllib.error.HTTPError as err:
            return int(err.code)
        except (urllib.error.URLError, OSError):
            return 0

    def registry_scope(self) -> Tuple[int, Any]:
        """Ask GHCR for a `pull,push` token for the deploy package.

        GitHub's container registry answers with a JSON document whose `access`
        list is the granted scope; a PAT without `packages:*` gets a document
        with no (or partial) access - that list is the only read-only proof of
        the packages capabilities.
        """
        basic = base64.b64encode(
            ("%s:%s" % (self.login, self._token)).encode("utf-8")
        ).decode("ascii")
        url = ("%s/token?service=ghcr.io&scope=repository:%s:pull,push"
               % (GHCR_HOST, self.package))
        request = urllib.request.Request(url, headers={
            "Authorization": "Basic %s" % basic,
            "User-Agent": "open-chat-go-ci#1044-capability-check",
        }, method="GET")
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                status = int(getattr(response, "status", response.getcode()))
                body = response.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as err:
            return int(err.code), None
        except (urllib.error.URLError, OSError):
            return 0, None
        try:
            payload = json.loads(body)
        except ValueError:
            return status, None
        return status, payload.get("access")

    def observations(self) -> Dict[str, Any]:
        status, access = self.registry_scope()
        return {
            "token_shape": token_shape(self._token),
            "contents_status": self.github_status(
                "/repos/%s/commits/%s" % (self.repo, self.ref)),
            "pulls_status": self.github_status(
                "/repos/%s/pulls?state=all&per_page=1" % self.repo),
            "registry_status": status,
            "registry_access": access,
            "package": self.package,
        }


def _load_token(args: argparse.Namespace) -> str:
    if args.token_file:
        try:
            with open(args.token_file, "r", encoding="utf-8") as handle:
                return strip_credential(handle.read())
        except OSError as err:
            raise SystemExit("could not read --token-file: %s" % err.strerror)
    return strip_credential(os.environ.get("BOT_TOKEN_CANDIDATE")
                            or os.environ.get("BOT_TOKEN"))


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        description="Verify that a candidate deployment-profile BOT_TOKEN carries "
                    "the ci#1044 capability set (%s). The credential is never printed."
                    % ", ".join(CAPABILITIES))
    parser.add_argument("--token-file",
                        help="file holding the candidate token (0600 recommended)")
    parser.add_argument("--repo", default=DEPLOY_REPO,
                        help="repository the deploy checks out (default: %s)" % DEPLOY_REPO)
    parser.add_argument("--package", default=DEPLOY_PACKAGE,
                        help="GHCR package the deploy pulls (default: %s)" % DEPLOY_PACKAGE)
    parser.add_argument("--ref", default=DEPLOY_REF,
                        help="ref proven readable (default: %s)" % DEPLOY_REF)
    parser.add_argument("--login", default=os.environ.get("BOT_LOGIN", ""),
                        help="username for the GHCR basic-auth exchange")
    parser.add_argument("--observations-json", default="",
                        help="offline mode: evaluate these probe results instead of "
                             "touching the network")
    parser.add_argument("--json", action="store_true",
                        help="also print the findings as JSON")
    args = parser.parse_args(argv)

    if args.observations_json.strip():
        try:
            observations = json.loads(args.observations_json)
        except ValueError as err:
            print("::error::could not parse --observations-json: %s" % err)
            return 1
        if not isinstance(observations, dict):
            print("::error::--observations-json must be a JSON object")
            return 1
        token_shape_hint = str(observations.get("token_shape") or "")
    else:
        token = _load_token(args)
        if not token:
            print("::error::no candidate token: pass --token-file or set "
                  "BOT_TOKEN_CANDIDATE. Nothing was probed.")
            return 1
        observations = Probe(token, repo=args.repo, package=args.package,
                             ref=args.ref, login=args.login).observations()
        token_shape_hint = str(observations.get("token_shape") or "")
        # Belt and braces: the credential value must not survive into any output.
        observations.pop("token", None)

    findings = evaluate(observations)
    print(render(findings, token_shape_hint))
    if args.json:
        print(json.dumps([{"capability": f.capability, "verdict": f.verdict,
                           "detail": f.detail} for f in findings]))

    missing = problems(findings)
    for finding in missing:
        print("::error::ci#1044: BOT_TOKEN is missing %s - %s"
              % (finding.capability, finding.detail))
    if missing:
        print("::error::ci#1044: do not store this token as the %s profile "
              "BOT_TOKEN; re-issue it with the capability set above."
              % "/".join(DEPLOY_PROFILES))
        return 1

    print("ci#1044: the candidate token carries every probeable capability the "
          "%s deploy needs; only the functional pull_requests:write probe "
          "(preview-URL comment) remains." % "/".join(DEPLOY_PROFILES))
    return 0


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
