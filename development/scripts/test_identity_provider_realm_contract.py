#!/usr/bin/env python3
"""Unit tests for the ci#1061 identity-provider realm/chart contract (stdlib only)."""

from __future__ import annotations

import contextlib
import io
import json
import os
import sys
import unittest
from contextlib import redirect_stdout
from pathlib import Path

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import identity_provider_realm_contract as contract  # noqa: E402

ROOT = Path(HERE).resolve().parents[1]

REALM_PATH = ROOT / contract.REALM_FILE
PATCH_PATH = ROOT / contract.CHART_PATCH

PATCH_HEADER = (
    "diff --git a/backend/values.msgmate-io-production.yaml b/backend/values.msgmate-io-production.yaml\n"
    "--- a/backend/values.msgmate-io-production.yaml\n"
    "+++ b/backend/values.msgmate-io-production.yaml\n"
    "@@ -1,1 +1,9 @@\n"
)


def production_patch(*lines: str) -> str:
    return PATCH_HEADER + "".join(f"+{line}\n" for line in lines)


GOOD_PRODUCTION_LINES = (
    "identityProvider:",
    "  enabled: true",
    "  preset: keycloak",
    "  issuer: https://id.msgmate.io/realms/main",
    "  clientID: open-chat",
    "  redirectURL: https://msgmate.io/api/v1/integrations/identity_provider/login/callback",
    "  existingSecret: open-chat-identity-provider",
    "  allowUnmapped: false",
)


def load_realm() -> dict:
    return json.loads(REALM_PATH.read_text(encoding="utf-8"))


class PreservedSecretsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.realm = load_realm()

    def test_committed_realm_has_a_real_client_secret_placeholder(self):
        client = self.realm["clients"][0]
        self.assertEqual(client["secret"], contract.PLACEHOLDER_SECRET)

    def test_a_committed_client_secret_fails_the_contract(self):
        self.realm["clients"][0]["secret"] = "s3cr3t-value"
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("placeholder" in problem for problem in findings), findings)

    def test_seeded_users_fail_the_contract(self):
        self.realm["users"] = [{"username": "someone", "enabled": True}]
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("seed users" in problem for problem in findings), findings)

    def test_public_or_implicit_clients_fail(self):
        self.realm["clients"][0]["publicClient"] = True
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("confidential" in problem for problem in findings), findings)

    def test_direct_grants_fail(self):
        self.realm["clients"][0]["directAccessGrantsEnabled"] = True
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("direct grants" in problem for problem in findings), findings)

    def test_pkce_is_required(self):
        self.realm["clients"][0]["attributes"] = {}
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("PKCE" in problem for problem in findings), findings)

    def test_self_registration_fails(self):
        self.realm["registrationAllowed"] = True
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("registrationAllowed" in problem for problem in findings), findings)

    def test_realm_name_is_part_of_the_issuer_contract(self):
        self.realm["realm"] = "master"
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("realm name" in problem for problem in findings), findings)

    def test_tls_is_required(self):
        self.realm["sslRequired"] = "none"
        findings = contract.evaluate(self.realm, {})
        self.assertTrue(any("sslRequired" in problem for problem in findings), findings)


class ChartAgreementTests(unittest.TestCase):
    def setUp(self) -> None:
        self.realm = load_realm()

    def evaluate(self, overrides: dict | None = None, drop: tuple[str, ...] = ()):
        values = {
            "enabled": "true",
            "preset": "keycloak",
            "issuer": "https://id.msgmate.io/realms/main",
            "clientID": "open-chat",
            "redirectURL": "https://msgmate.io/api/v1/integrations/identity_provider/login/callback",
            "existingSecret": "open-chat-identity-provider",
            "allowUnmapped": "false",
        }
        values.update(overrides or {})
        for key in drop:
            values.pop(key, None)
        return contract.evaluate(self.realm, {"production": values})

    def test_the_committed_tree_satisfies_the_contract(self):
        findings = contract.check_repository(ROOT)
        self.assertEqual(list(findings), [], findings)
        # Three realm/chart notes, plus the deployment notes the ci#1061 deployment
        # half adds: both environments' Secret references and the served issuer.
        self.assertEqual(len(findings.notes), 9, findings.notes)

    def test_issuer_for_the_wrong_host_fails(self):
        findings = self.evaluate({"issuer": "https://id.stage.msgmate.io/realms/main"})
        self.assertTrue(any("issuer" in problem for problem in findings), findings)

    def test_http_issuer_fails(self):
        findings = self.evaluate({"issuer": "http://id.msgmate.io/realms/main"})
        self.assertTrue(any("https" in problem for problem in findings), findings)

    def test_client_id_must_exist_in_the_realm(self):
        findings = self.evaluate({"clientID": "another-client"})
        self.assertTrue(any("clientID" in problem for problem in findings), findings)

    def test_unregistered_redirect_uri_fails(self):
        findings = self.evaluate(
            {"redirectURL": "https://msgmate.io/api/v1/integrations/identity_provider/login/callback2"}
        )
        self.assertTrue(any("not registered" in problem for problem in findings), findings)

    def test_redirect_uri_must_be_absolute_for_the_environment(self):
        findings = self.evaluate(
            {"redirectURL": "/api/v1/integrations/identity_provider/login/callback"}
        )
        problems = [problem for problem in findings if "redirectURL" in problem]
        self.assertEqual(len(problems), 2, findings)

    def test_missing_redirect_uri_fails(self):
        findings = self.evaluate(drop=("redirectURL",))
        self.assertTrue(any("redirectURL must be set" in problem for problem in findings), findings)

    def test_preset_must_match_the_keycloak_realm(self):
        findings = self.evaluate({"preset": "authentik"})
        self.assertTrue(any("preset" in problem for problem in findings), findings)

    def test_existing_secret_is_required(self):
        findings = self.evaluate(drop=("existingSecret",))
        self.assertTrue(any("existingSecret" in problem for problem in findings), findings)

    def test_allow_unmapped_must_stay_false(self):
        findings = self.evaluate({"allowUnmapped": "true"})
        self.assertTrue(any("allowUnmapped" in problem for problem in findings), findings)

    def test_inline_credentials_in_values_are_rejected(self):
        findings = self.evaluate({"clientSecret": "s3cr3t"})
        self.assertTrue(any("clientSecret" in problem for problem in findings), findings)

    def test_disabled_environments_are_not_checked(self):
        findings = contract.evaluate(self.realm, {
            "production": {
                "enabled": "false",
                "issuer": "",
                "clientID": "",
                "redirectURL": "",
                "existingSecret": "",
            }
        })
        self.assertTrue(any("enables the identity provider for no environment" in problem for problem in findings), findings)

    def test_staging_is_checked_independently(self):
        findings = contract.evaluate(self.realm, {
            "staging": {
                "enabled": "true",
                "preset": "keycloak",
                "issuer": "https://id.msgmate.io/realms/main",
                "clientID": "open-chat",
                "redirectURL": "https://stage.msgmate.io/api/v1/integrations/identity_provider/login/callback",
                "existingSecret": "open-chat-identity-provider",
                "allowUnmapped": "false",
            }
        })
        self.assertTrue(any(problem.startswith("staging: issuer") for problem in findings), findings)


class PatchParsingTests(unittest.TestCase):
    def test_added_block_is_parsed_per_environment(self):
        parsed = contract.parse_chart_patch(
            production_patch(*GOOD_PRODUCTION_LINES)
        )
        self.assertEqual(set(parsed), {"production"})
        self.assertEqual(parsed["production"]["issuer"], "https://id.msgmate.io/realms/main")
        self.assertEqual(parsed["production"]["enabled"], "true")

    def test_quoted_values_are_unquoted(self):
        parsed = contract.parse_chart_patch(production_patch(
            "identityProvider:",
            '  enabled: "true"',
            "  clientID: 'open-chat'",
        ))
        self.assertEqual(parsed["production"], {"enabled": "true", "clientID": "open-chat"})

    def test_removed_lines_are_ignored(self):
        text = PATCH_HEADER + "-identityProvider:\n-  enabled: true\n+identityProvider:\n+  enabled: false\n"
        parsed = contract.parse_chart_patch(text)
        self.assertEqual(parsed["production"], {"enabled": "false"})

    def test_comment_lines_do_not_close_the_block(self):
        parsed = contract.parse_chart_patch(production_patch(
            "identityProvider:",
            "  enabled: true",
            "  # a comment inside the block",
            "  clientID: open-chat",
        ))
        self.assertEqual(parsed["production"], {"enabled": "true", "clientID": "open-chat"})

    def test_the_next_top_level_key_closes_the_block(self):
        parsed = contract.parse_chart_patch(production_patch(
            "identityProvider:",
            "  enabled: true",
            "otherBlock:",
            "  issuer: https://example.com",
        ))
        self.assertEqual(parsed["production"], {"enabled": "true"})

    def test_the_real_patch_parses_both_environment_overlays(self):
        parsed = contract.parse_chart_patch(PATCH_PATH.read_text(encoding="utf-8"))
        self.assertEqual(set(parsed), {"production", "staging", "default"})
        self.assertEqual(parsed["production"]["enabled"], "true")
        self.assertEqual(parsed["staging"]["enabled"], "true")
        self.assertEqual(parsed["default"]["enabled"], "false")
        self.assertEqual(parsed["default"]["clientSecretKey"], "client-secret")
        self.assertEqual(parsed["default"]["bootstrapJSONKey"], "bootstrap-json")


class MainTests(unittest.TestCase):
    def test_main_reports_ok(self):
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = contract.main([])
        self.assertEqual(code, 0)
        self.assertIn("contract: ok", buffer.getvalue())

    def test_json_mode_is_machine_readable_and_hides_no_failures(self):
        findings = contract.check_repository(ROOT)
        payload = {"ok": not findings, "problems": list(findings), "notes": findings.notes}
        rendered = json.dumps(payload)
        self.assertTrue(json.loads(rendered)["ok"])


class RunbookTests(unittest.TestCase):
    def test_runbook_documents_the_operator_steps(self):
        text = (ROOT / contract.RUNBOOK).read_text(encoding="utf-8")
        for needle in (
            "id.msgmate.io/realms/main",
            "id.stage.msgmate.io/realms/main",
            "open-chat-identity-provider",
            "client-secret",
            "bootstrap-json",
            "redirectURL",
            "Rollback",
        ):
            self.assertIn(needle, text)

    def test_runbook_contains_no_credential_shaped_values(self):
        text = (ROOT / contract.RUNBOOK).read_text(encoding="utf-8")
        self.assertNotIn("BEGIN PRIVATE KEY", text)
        self.assertNotIn("kubeconfig", text)


class NoSecretTests(unittest.TestCase):
    def test_the_patch_never_carries_a_secret_value(self):
        text = PATCH_PATH.read_text(encoding="utf-8")
        for added in text.splitlines():
            if not added.startswith("+") or added.startswith("+++"):
                continue
            for line in (added,):
                self.assertNotIn("clientSecret:", line)
                self.assertNotIn("bootstrapJSON:", line)
                self.assertNotIn("BEGIN PRIVATE KEY", line)

    def test_realm_file_is_json_without_extra_users(self):
        realm = load_realm()
        self.assertEqual(realm["users"], [])
        self.assertEqual(realm["clients"][0]["redirectUris"], [
            "https://msgmate.io/api/v1/integrations/identity_provider/login/callback",
            "https://stage.msgmate.io/api/v1/integrations/identity_provider/login/callback",
        ])


class DeploymentManifestTests(unittest.TestCase):
    """The rendered IdP deployments are what actually serves id.msgmate.io."""

    def setUp(self) -> None:
        self.manifests = {
            environment: (ROOT / contract.DEPLOYMENT_DIR / f"keycloak-msgmate-io-{environment}.yaml").read_text(
                encoding="utf-8"
            )
            for environment in contract.ENVIRONMENTS
        }
        self.compose = (ROOT / contract.SANDBOX_COMPOSE).read_text(encoding="utf-8")

    def evaluate(self, **overrides: str) -> contract.Findings:
        manifests = dict(self.manifests)
        manifests.update(overrides)
        return contract.evaluate_deployment(manifests, self.compose)

    def test_the_checked_in_manifests_satisfy_the_contract(self):
        findings = self.evaluate()
        self.assertEqual(list(findings), [])

    def test_missing_manifest_is_reported_not_raised(self):
        findings = self.evaluate(production="")
        self.assertTrue(any("missing rendered identity provider deployment" in p for p in findings))

    def test_issuer_host_must_match_the_chart_issuer(self):
        broken = self.manifests["production"].replace("https://id.msgmate.io", "https://msgmate.io")
        findings = self.evaluate(production=broken)
        self.assertTrue(any("KC_HOSTNAME" in p for p in findings))

    def test_realm_import_must_stay_enabled(self):
        broken = self.manifests["production"].replace('args: ["start", "--import-realm"]', 'args: ["start"]')
        findings = self.evaluate(production=broken)
        self.assertTrue(any("--import-realm" in p for p in findings))

    def test_an_inline_database_password_fails(self):
        broken = self.manifests["production"].replace(
            """            - name: KC_DB_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: keycloak-db
                  key: password""",
            """            - name: KC_DB_PASSWORD
              value: hunter2""",
        )
        findings = self.evaluate(production=broken)
        self.assertTrue(any("KC_DB_PASSWORD" in p for p in findings))

    def test_a_committed_secret_manifest_fails(self):
        findings = self.evaluate(production=self.manifests["production"] + "---\nkind: Secret\n")
        self.assertTrue(any("must not contain a Secret manifest" in p for p in findings))

    def test_the_client_secret_placeholder_must_not_be_baked_in(self):
        findings = self.evaluate(
            production=self.manifests["production"] + f"# {contract.PLACEHOLDER_SECRET}\n"
        )
        self.assertTrue(any("client-secret placeholder" in p for p in findings))

    def test_staging_and_production_must_run_the_same_image(self):
        findings = self.evaluate(
            staging=self.manifests["staging"].replace(
                "quay.io/keycloak/keycloak:26.0.7", "quay.io/keycloak/keycloak:25.0.6"
            )
        )
        self.assertTrue(any("same identity provider image" in p for p in findings))

    def test_the_deployed_image_must_be_the_sandbox_image(self):
        findings = contract.evaluate_deployment(
            self.manifests,
            self.compose.replace("quay.io/keycloak/keycloak:26.0.7", "quay.io/keycloak/keycloak:25.0.6"),
        )
        self.assertTrue(any("differs from the sandbox" in p for p in findings))

    def test_the_issuer_host_is_tls_terminated(self):
        broken = "\n".join(
            line for line in self.manifests["production"].splitlines() if "- id.msgmate.io" not in line
        )
        findings = self.evaluate(production=broken)
        self.assertTrue(any("over TLS" in p for p in findings))


class DeploymentRendererTests(unittest.TestCase):
    """The checked-in YAML is a rendered artifact, so it must never be hand-edited."""

    def test_the_renderer_reproduces_the_checked_in_manifests(self):
        sys.path.insert(0, str(ROOT / "development" / "identity-provider"))
        import render_keycloak_manifests as renderer  # noqa: E402

        for environment in contract.ENVIRONMENTS:
            path = ROOT / contract.DEPLOYMENT_DIR / f"keycloak-msgmate-io-{environment}.yaml"
            self.assertEqual(path.read_text(encoding="utf-8"), renderer.render(environment))

    def test_check_mode_passes_and_is_quiet_about_staleness(self):
        sys.path.insert(0, str(ROOT / "development" / "identity-provider"))
        import render_keycloak_manifests as renderer  # noqa: E402

        stderr = io.StringIO()
        with redirect_stdout(io.StringIO()), contextlib.redirect_stderr(stderr):
            self.assertEqual(renderer.main([]), 0)
        self.assertEqual(stderr.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
