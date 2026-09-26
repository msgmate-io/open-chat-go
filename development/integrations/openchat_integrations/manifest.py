"""Manifest loading and profile resolution for Open-Chat integrations.

The manifest (`integrations.yaml` at the repository root) is the single source
of truth for integration source locations, profile membership, and the Go /
frontend contributions of each integration.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional

import yaml

MANIFEST_NAME = "integrations.yaml"
LOCAL_OVERLAY_NAME = "integrations.local.yaml"

VALID_SOURCES = ("git", "local", "submodule")


class ManifestError(RuntimeError):
    pass


@dataclass
class FrontendPage:
    source: str
    asset: str
    route: Optional[str] = None
    public: bool = False


@dataclass
class Frontend:
    name: str
    package: Optional[str] = None
    path: str = "frontend"
    pages: List[FrontendPage] = field(default_factory=list)


@dataclass
class Integration:
    id: str
    module: str
    import_path: str
    source: str
    repo: Optional[str]
    ref: str
    path: str
    private: bool
    tags: List[str]
    bootstrap: Optional[str]
    depends_on: List[str]
    frontend: Optional[Frontend]

    @property
    def dir_name(self) -> str:
        return Path(self.path).name


@dataclass
class Manifest:
    version: int
    default_profile: str
    cache_dir: str
    profiles: Dict[str, List[str]]
    integrations: Dict[str, Integration]
    repo_root: Path

    @classmethod
    def load(cls, repo_root: Path, apply_local_overlay: bool = True) -> "Manifest":
        repo_root = repo_root.resolve()
        manifest_path = repo_root / MANIFEST_NAME
        if not manifest_path.exists():
            raise ManifestError(f"manifest not found: {manifest_path}")

        raw = yaml.safe_load(manifest_path.read_text(encoding="utf-8")) or {}
        if not isinstance(raw, dict):
            raise ManifestError("manifest root must be a mapping")

        overlay: Dict[str, Any] = {}
        overlay_path = repo_root / LOCAL_OVERLAY_NAME
        if apply_local_overlay and overlay_path.exists():
            loaded = yaml.safe_load(overlay_path.read_text(encoding="utf-8")) or {}
            if isinstance(loaded, dict):
                overlay = loaded

        version = int(raw.get("version", 1))
        default_profile = str(raw.get("default_profile", "default"))
        cache_dir = str(raw.get("cache_dir", ".integrations"))

        profiles_raw = raw.get("profiles") or {}
        if not isinstance(profiles_raw, dict):
            raise ManifestError("profiles must be a mapping")
        profiles: Dict[str, List[str]] = {}
        for name, ids in profiles_raw.items():
            if not isinstance(ids, list):
                raise ManifestError(f"profile {name!r} must be a list")
            profiles[str(name)] = [str(i) for i in ids]

        integrations_raw = raw.get("integrations") or {}
        if not isinstance(integrations_raw, dict):
            raise ManifestError("integrations must be a mapping")

        overrides_raw = overlay.get("overrides") or {}
        if not isinstance(overrides_raw, dict):
            raise ManifestError("integrations.local.yaml overrides must be a mapping")

        integrations: Dict[str, Integration] = {}
        for integ_id, entry in integrations_raw.items():
            integ_id = str(integ_id)
            if not isinstance(entry, dict):
                raise ManifestError(f"integration {integ_id!r} must be a mapping")

            merged = dict(entry)
            override = overrides_raw.get(integ_id)
            if isinstance(override, dict):
                merged.update(override)

            integrations[integ_id] = _parse_integration(integ_id, merged)

        manifest = cls(
            version=version,
            default_profile=default_profile,
            cache_dir=cache_dir,
            profiles=profiles,
            integrations=integrations,
            repo_root=repo_root,
        )
        manifest.validate()
        return manifest

    def validate(self) -> None:
        for profile, ids in self.profiles.items():
            for integ_id in ids:
                if integ_id == "*":
                    continue
                if integ_id not in self.integrations:
                    raise ManifestError(
                        f"profile {profile!r} references unknown integration {integ_id!r}"
                    )
        for integ in self.integrations.values():
            for dep in integ.depends_on:
                if dep not in self.integrations:
                    raise ManifestError(
                        f"integration {integ.id!r} depends on unknown integration {dep!r}"
                    )

    def resolve_profile(self, profile: Optional[str]) -> str:
        if not profile:
            profile = os.environ.get("INTEGRATION_PROFILE") or self.default_profile
        if profile not in self.profiles:
            known = ", ".join(sorted(self.profiles))
            raise ManifestError(f"unknown profile {profile!r} (known: {known})")
        return profile

    def profile_ids(self, profile: str) -> List[str]:
        ids = self.profiles[profile]
        if "*" in ids:
            return list(self.integrations.keys())
        # Preserve manifest declaration order.
        return [i for i in self.integrations if i in set(ids)]

    def closure(self, ids: List[str]) -> List[str]:
        """Return ids plus every transitively required integration."""
        seen: List[str] = []
        pending = list(ids)
        known = set(ids)
        while pending:
            current = pending.pop(0)
            if current in seen:
                continue
            seen.append(current)
            integ = self.integrations.get(current)
            if integ is None:
                continue
            for dep in integ.depends_on:
                if dep not in known:
                    known.add(dep)
                    pending.append(dep)
        # Keep deterministic manifest order.
        return [i for i in self.integrations if i in set(seen)]

    def path_for(self, integ: Integration) -> Path:
        return (self.repo_root / integ.path).resolve()


def _parse_frontend(raw: Any, integ_id: str) -> Optional[Frontend]:
    if raw is None:
        return None
    if isinstance(raw, str):
        return Frontend(name=raw)
    if not isinstance(raw, dict):
        raise ManifestError(f"integration {integ_id!r} frontend must be a mapping")
    name = str(raw.get("name") or integ_id)
    pages: List[FrontendPage] = []
    for page in raw.get("pages") or []:
        if not isinstance(page, dict):
            raise ManifestError(f"integration {integ_id!r} frontend page must be a mapping")
        source = str(page.get("source", "")).strip()
        asset = str(page.get("asset", "")).strip()
        if not source or not asset:
            raise ManifestError(
                f"integration {integ_id!r} frontend page needs source and asset"
            )
        pages.append(
            FrontendPage(
                source=source,
                asset=asset,
                route=str(page["route"]) if page.get("route") else None,
                public=bool(page.get("public", False)),
            )
        )
    return Frontend(
        name=name,
        package=str(raw["package"]) if raw.get("package") else None,
        path=str(raw.get("path", "frontend")),
        pages=pages,
    )


def _parse_integration(integ_id: str, entry: Dict[str, Any]) -> Integration:
    module = str(entry.get("module", "")).strip()
    import_path = str(entry.get("import", "") or module).strip()
    if not module:
        raise ManifestError(f"integration {integ_id!r} is missing module")
    if not import_path:
        raise ManifestError(f"integration {integ_id!r} is missing import")

    source = str(entry.get("source", "git")).strip() or "git"
    if source not in VALID_SOURCES:
        raise ManifestError(
            f"integration {integ_id!r} has invalid source {source!r} "
            f"(valid: {', '.join(VALID_SOURCES)})"
        )

    repo = entry.get("repo")
    repo = str(repo).strip() if repo else None
    if source == "git" and not repo:
        raise ManifestError(f"integration {integ_id!r} with source git needs repo")

    path = str(entry.get("path", "")).strip()
    if not path:
        raise ManifestError(f"integration {integ_id!r} is missing path")

    ref = str(entry.get("ref", "main")).strip() or "main"

    tags = [str(t) for t in (entry.get("tags") or [])]
    depends_on = [str(d) for d in (entry.get("depends_on") or [])]

    bootstrap = entry.get("bootstrap")
    bootstrap = str(bootstrap) if bootstrap else None

    # Developer override: OPENCHAT_INTEGRATION_<ID>_PATH points an integration
    # at a local checkout without editing the manifest.
    env_path = os.environ.get(f"OPENCHAT_INTEGRATION_{integ_id.upper()}_PATH")
    if env_path:
        source = "local"
        path = env_path
        repo = None

    return Integration(
        id=integ_id,
        module=module,
        import_path=import_path,
        source=source,
        repo=repo,
        ref=ref,
        path=path,
        private=bool(entry.get("private", False)),
        tags=tags,
        bootstrap=bootstrap,
        depends_on=depends_on,
        frontend=_parse_frontend(entry.get("frontend"), integ_id),
    )
