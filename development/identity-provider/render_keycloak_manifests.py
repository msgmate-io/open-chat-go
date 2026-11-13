#!/usr/bin/env python3
"""Render the Kubernetes manifests that deploy the ci#1061 identity provider.

The chart half of ci#1061 points the backend at an external OIDC issuer, the realm
template in `development/identity-provider/realm/` describes the client, and this
module is the missing third piece: the **deployment** of Keycloak itself at the
`id.` host the issuer contract already names.

The manifest is generated from the same source of truth as the contract checker
(`ENVIRONMENTS`, `REALM_NAME` in `development/scripts/identity_provider_realm_contract.py`),
so the deployed host, the issuer in the chart values and the realm cannot drift
apart: changing the environment table changes the rendered deployment too.

The rendered artifacts are checked in and CI re-renders them (`--check`), which is
what makes the reviewable YAML the deployed thing. Nothing here is a credential:
the database, the bootstrap admin and the client secret are referenced through
`secretKeyRef` only, and the committed realm keeps its placeholder secret.

Deliberately stdlib-only, like the rest of this gate.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
SCRIPTS = ROOT / "development" / "scripts"
if str(SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SCRIPTS))

from identity_provider_realm_contract import (  # noqa: E402
    ENVIRONMENTS,
    REALM_NAME,
)

# Shared with `development/sandbox/identity_provider/docker-compose.yaml`; the
# contract checker fails when the two stop matching, so the sandbox flow that
# proves the login also proves the version that is deployed.
KEYCLOAK_IMAGE = "quay.io/keycloak/keycloak:26.0.7"

# The IdP runs in its own namespace: an IdP restart or upgrade never rolls the
# application release, and the application stays a pure OIDC client.
NAMESPACE = "identity-provider"

ARTIFACT_DIR = Path("development") / "identity-provider" / "deploy"


def artifact_path(environment: str) -> Path:
    return ARTIFACT_DIR / f"keycloak-msgmate-io-{environment}.yaml"


def render(environment: str) -> str:
    """Return the complete multi-document manifest for one environment."""

    app_host, idp_host = ENVIRONMENTS[environment]
    return "".join(
        [
            _namespace_doc(),
            _service_account_doc(),
            _deployment_doc(environment, app_host, idp_host),
            _service_doc(),
            _ingress_doc(environment, idp_host),
            _network_policy_doc(),
        ]
    )


def _namespace_doc() -> str:
    return f"""# ci#1061: the identity provider behind https://msgmate.io.
# Rendered by development/identity-provider/render_keycloak_manifests.py -
# edit the renderer, never this file, and re-run it with --write.
apiVersion: v1
kind: Namespace
metadata:
  name: {NAMESPACE}
  labels:
    app.kubernetes.io/part-of: open-chat-identity-provider
"""


def _service_account_doc() -> str:
    return """---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: keycloak
  namespace: identity-provider
automountServiceAccountToken: false
"""


def _deployment_doc(environment: str, app_host: str, idp_host: str) -> str:
    return f"""---
# Keycloak is the OIDC issuer for {app_host}: the discovery document at
# https://{idp_host}/realms/{REALM_NAME}/.well-known/openid-configuration has to
# name exactly that issuer, because the backend compares it to the ID token.
apiVersion: apps/v1
kind: Deployment
metadata:
  name: keycloak
  namespace: {NAMESPACE}
  labels:
    app.kubernetes.io/name: keycloak
    app.kubernetes.io/part-of: open-chat-identity-provider
    app.kubernetes.io/instance: {environment}
spec:
  replicas: 2
  revisionHistoryLimit: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: keycloak
  template:
    metadata:
      labels:
        app.kubernetes.io/name: keycloak
    spec:
      serviceAccountName: keycloak
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: keycloak
      containers:
        - name: keycloak
          image: {KEYCLOAK_IMAGE}
          imagePullPolicy: IfNotPresent
          # --import-realm applies the committed realm template on start; an
          # existing realm is left alone, so this is a no-op after the first boot.
          args: ["start", "--import-realm"]
          securityContext:
            runAsNonRoot: true
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: false
            capabilities:
              drop: ["ALL"]
          ports:
            - name: http
              containerPort: 8080
            - name: management
              containerPort: 9000
          env:
            # The issuer: https://{idp_host}/realms/{REALM_NAME}
            - name: KC_HOSTNAME
              value: "https://{idp_host}"
            # A proxy that rewrites Host would change the issuer and every login
            # would fail an issuer/audience check, so the host is fixed.
            - name: KC_HOSTNAME_STRICT
              value: "true"
            - name: KC_PROXY_HEADERS
              value: "xforwarded"
            - name: KC_HTTP_ENABLED
              value: "true"
            - name: KC_HTTP_MANAGEMENT_PORT
              value: "9000"
            - name: KC_HEALTH_ENABLED
              value: "true"
            - name: KC_METRICS_ENABLED
              value: "true"
            # Sticky sessions on the Ingress keep one login flow on one pod; the
            # local cache is what Keycloak supports without an Infinispan cluster.
            - name: KC_CACHE
              value: "local"
            - name: KC_DB
              value: "postgres"
            - name: KC_DB_URL_HOST
              valueFrom:
                secretKeyRef:
                  name: keycloak-db
                  key: host
            - name: KC_DB_URL_PORT
              valueFrom:
                secretKeyRef:
                  name: keycloak-db
                  key: port
            - name: KC_DB_URL_DATABASE
              valueFrom:
                secretKeyRef:
                  name: keycloak-db
                  key: database
            - name: KC_DB_USERNAME
              valueFrom:
                secretKeyRef:
                  name: keycloak-db
                  key: username
            - name: KC_DB_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: keycloak-db
                  key: password
            # Only needed for the first start / admin recovery; the operator
            # creates this Secret out of band.
            - name: KC_BOOTSTRAP_ADMIN_USERNAME
              valueFrom:
                secretKeyRef:
                  name: keycloak-admin
                  key: username
            - name: KC_BOOTSTRAP_ADMIN_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: keycloak-admin
                  key: password
          startupProbe:
            httpGet:
              path: /health/started
              port: management
            periodSeconds: 5
            failureThreshold: 60
          readinessProbe:
            httpGet:
              path: /health/ready
              port: management
            periodSeconds: 10
            failureThreshold: 3
          livenessProbe:
            httpGet:
              path: /health/live
              port: management
            periodSeconds: 20
            failureThreshold: 3
          resources:
            requests:
              cpu: 250m
              memory: 512Mi
            limits:
              cpu: "1"
              memory: 1Gi
          volumeMounts:
            # The realm template from development/identity-provider/realm/, as a
            # ConfigMap the operator creates out of band.
            - name: realm
              mountPath: /opt/keycloak/data/import
              readOnly: true
            - name: data
              mountPath: /opt/keycloak/data
      volumes:
        - name: realm
          configMap:
            name: keycloak-realm
        - name: data
          emptyDir: {{}}
"""


def _service_doc() -> str:
    return """---
apiVersion: v1
kind: Service
metadata:
  name: keycloak
  namespace: identity-provider
  labels:
    app.kubernetes.io/name: keycloak
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: keycloak
  ports:
    - name: http
      port: 80
      targetPort: http
    - name: management
      port: 9000
      targetPort: management
"""


def _ingress_doc(environment: str, idp_host: str) -> str:
    return f"""---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: keycloak
  namespace: {NAMESPACE}
  labels:
    app.kubernetes.io/name: keycloak
    app.kubernetes.io/instance: {environment}
  annotations:
    # One login flow spans several requests: keep a browser session on one pod
    # while KC_CACHE is local.
    nginx.ingress.kubernetes.io/affinity: cookie
    nginx.ingress.kubernetes.io/affinity-mode: persistent
    nginx.ingress.kubernetes.io/proxy-body-size: "16m"
    # Keycloak reads the scheme from X-Forwarded-* (KC_PROXY_HEADERS).
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - {idp_host}
      secretName: keycloak-tls
  rules:
    - host: {idp_host}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: keycloak
                port:
                  name: http
"""


def _network_policy_doc() -> str:
    return """---
# The IdP only accepts traffic from the ingress controller, only talks to DNS and
# its database, and only exposes its health endpoints inside the cluster.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: keycloak
  namespace: identity-provider
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: keycloak
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx
      ports:
        - port: 8080
          protocol: TCP
    - ports:
        - port: 9000
          protocol: TCP
  egress:
    - ports:
        - port: 5432
          protocol: TCP
        - port: 53
          protocol: UDP
        - port: 53
          protocol: TCP
"""


def write_artifacts(root: Path = ROOT) -> list[Path]:
    written = []
    for environment in sorted(ENVIRONMENTS):
        path = root / artifact_path(environment)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(render(environment), encoding="utf-8")
        written.append(path)
    return written


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--write",
        action="store_true",
        help="rewrite the checked-in artifacts (default: --check)",
    )
    args = parser.parse_args(argv)

    if args.write:
        for path in write_artifacts():
            print(f"wrote {path.relative_to(ROOT)}")
        return 0

    stale = []
    for environment in sorted(ENVIRONMENTS):
        path = ROOT / artifact_path(environment)
        expected = render(environment)
        actual = path.read_text(encoding="utf-8") if path.is_file() else ""
        if actual != expected:
            stale.append(path)
    for path in stale:
        print(
            f"FAIL: {path.relative_to(ROOT)} is not what the renderer produces - "
            "run development/identity-provider/render_keycloak_manifests.py --write",
            file=sys.stderr,
        )
    print("identity-provider deployment manifests: " + ("ok" if not stale else "FAILED"))
    return 0 if not stale else 1


if __name__ == "__main__":
    raise SystemExit(main())
