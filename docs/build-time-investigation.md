# `Build Backend Image Once` — measured breakdown and ranked plan (ci#513)

Status: **living** investigation for
[ci#513](https://github.com/msgmate-io/open-chat-go-ci/issues/513)
("Drastically reduce 'Build Backend Image Once' time"). Increment 1
(manifest-keyed module graph) has landed in the `Dockerfile`; this document
records the **current** numbers after that change, the costs that are still
unexplained, and the ranked incremental plan.

Everything below is measured — either from the raw job log of a real run
(`gh api repos/msgmate-io/open-chat-go/actions/jobs/<id>/logs`) or from the
`docker buildx` output embedded in it. Numbers are wall-clock seconds for the
stage, taken at the point `BuildKit` reported `DONE`.

## 1. Reference run

Run [`39152867150`](https://github.com/msgmate-io/open-chat-go/actions/runs/39152867150)
(branch `fix/ci-620-provider-admission-cap`), job
`117753876232` (`Build Backend Image Once`, **success**, `INTEGRATION_PROFILE=full`):

| segment | wall |
| --- | --- |
| job total (`18:26:55` → `18:33:30`) | **~395 s** |
| runner setup → buildkit boot (`#1`, `#13` cache manifest import) | ~23 s |
| integration checkout cache restore + `sync --frozen` | ~21 s |
| **build step** (`18:27:17` → `18:32:56`) | **~339 s** |
| post-job `actions/cache` save + teardown | ~34 s |

### Stage breakdown of the build step

| # | stage | wall | share of build step |
| --- | --- | --- | --- |
| 23 | `basebuilder` `apk add` toolchain | CACHED | — |
| 24 | `basebuilder` `go build std` → `/gocache` prewarm | CACHED | — |
| 25 | `basebuilder` `go install swag@latest` → `/swagcache` | CACHED | — |
| 33 | `frontend` `pip install development/build-tools` | 4.8 s | 1.4 % |
| 37 | `frontend` `npm install` | **14.3 s** | 4.2 % |
| **42** | **`basebuilder` `resolve` + `GOMODCACHE=/gomodcache go mod download`** | **31.1 s** | **9.2 %** |
| 43 | `frontend` `npm run build` (Vike prerender, 126 HTML docs) | **18.0 s** | 5.3 % |
| 44 | `frontend` `generate_golang_routes.sh` | 2.0 s | 0.6 % |
| 45 | `basebuilder` `COPY clients/ /clients/` (full sources) | 1.5 s | 0.4 % |
| 46 | `basebuilder` `COPY backend/ ./` | 0.1 s | 0.0 % |
| 49 | `builder` `resolve` + `export` integration pages | 0.4 s | 0.1 % |
| **51** | **`builder` `full_build.sh --no-frontend` (swag + Go compile + link)** | **159.5 s** | **47.0 %** |
| 52 | `builder` copy `little_world_default_bots` | 0.1 s | 0.0 % |
| 58 | export/push prod image | 7.3 s | 2.2 % |
| **60** | **cache export to registry (`cache-to … mode=max`)** | **124.9 s** | **36.8 %** |

Inside stage 51 (159.5 s): `swag init --parseDependency --parseDependencyLevel 3`
finishes at `21.28 s` (≈ **21 s**), and "Full build complete" is printed at
`158.2 s`, so the actual
`go build -trimpath -buildvcs=false -ldflags "-s -w" -o backend .` is
**≈ 137 s**.

Inside stage 60: `preparing build cache for export` **109.3 s** +
`sending cache export` **15.6 s**.

**Two costs dominate: the Go compile+link (137 s) and the registry cache
export (124.9 s) — together ~77 % of the build step.** The whole frontend
(`npm install` + `npm run build` + routes) is ~35 s, and the base image
toolchain layers are fully cached.

## 2. Increment 1 (landed) and what it did / did not fix

The pre-existing fix keys the module graph on integration **manifests only**:

```dockerfile
FROM docker.io/library/alpine:${ALPINE_VERSION} AS manifests
COPY clients/ /clients/
RUN find /clients -type f ! -name go.mod ! -name go.sum -delete
```

so `basebuilder` copies only `go.mod`/`go.sum` before
`openchat-integrations resolve && go mod download`, and the 1.4 GB
`/gomodcache` layer is no longer invalidated by an integration source edit.
Confirmed in the reference run: the integration checkouts are copied *after*
the download (`#45`), so a source-only change cannot invalidate `#42`.

### But stage 42 still runs on every PR (31.1 s)

`#42` was **not** CACHED in the reference run — the module graph was
re-resolved and re-downloaded from scratch. Since the manifest stage content is
now stable across a source-only PR, the layer key must be changing for another
reason. Candidates to check, in order:

1. **The registry cache is shared and mode=max overwrites it.** Every PR CI run
   (and `build_docker.yaml`, which points at `buildcache-core-only`) exports to
   the same `:buildcache-full`/`:buildcache-core-only` refs. Concurrent runs
   race: a run that exports a *superseded* intermediate state replaces the warm
   manifest, so the next run imports a cache that no longer matches. The
   reference run imported `#13 importing cache manifest … buildcache-full` and
   still missed.
2. **A copied input that changes per commit.** `development/` (incl. the private
   `development/ci/openchat` fragment) and `integrations.yaml`/`integrations.lock.json`
   are all copied before `#42`; the private fragment is re-materialised by
   `setup`, so its mtimes/content can differ while `integrations.lock.json`
   (the hash we key on) stays identical.
3. **`openchat-integrations resolve` rewriting `backend/go.work` inside the
   layer** — harmless for the layer key, but it means the *content* hash of
   `go.work` is not part of any key, so a profile/tag change that alters the
   closure cannot be distinguished.

## 3. The host-side Go build cache is still a no-op (≈ 0 bytes)

In the same run:

```
18:27:16  Cache hit for restore-key: docker-go-build-Linux-50082c11cfb3e49…-d9173795a0b4609c…
18:27:16  Cache Size: ~0 MB (220 B)          <- restore key matched an EMPTY entry
…
18:32:59  Cache saved with key: docker-go-build-Linux-50082c11cfb3e49…-e1d317a685d08ad3…
```

The workflow's comment is right that `cache-to=type=registry` never exports the
`type=cache,target=/root/.cache/go-build` mount, and the `Dockerfile` can
therefore only *read* the `gobuildcache` named context:

```dockerfile
--mount=type=bind,from=gobuildcache,target=/gobuildcache,ro
```

…but nothing ever writes the warmed cache back out, so every run starts with an
empty compiler cache and recompiles the whole dependency tree (part of the
137 s). This is the single largest *unrealised* saving on current `main`.

## 4. Ranked plan (each item with an estimated saving)

| rank | change | mechanism | est. saving |
| --- | --- | --- | --- |
| 1 | **Round-trip the Go build cache** | after `full_build.sh`, copy the populated `/root/.cache/go-build` mount into a small **normal layer** inside the builder, and read it back on the host with a second, fully-cached `docker buildx build --target <export-stage> --output type=local,dest=$RUNNER_TEMP/open-chat-go-build-cache` (the same context the workflow already feeds back as `gobuildcache`). Keep the `|| true` best-effort semantics so a failure can never fail the build. | **60–90 s** |
| 2 | **Stop re-uploading the 1.4 GB module graph** | `/gomodcache` is a normal layer precisely so `mode=max` re-exports it — that is the 109 s "preparing build cache for export" tail. Move it to a **hash-keyed base image** (`push: true`, tag `modcache-${hash(go.mod,go.sum,integrations.lock.json)}`) or a second `type=registry` exporter scoped to that stage, so unchanged module graphs are never re-serialised. | **60–110 s** |
| 3 | **Fix the stage-42 cache miss** | see §2: separate the full/core-only registry cache refs, stop copying per-run churn (`development/ci/openchat`) ahead of the download, or key on the resolved `go.work` content. | **25–30 s** |
| 4 | **Take `swag init` off the compile stage** | generate swagger in its own stage keyed on `backend/api/**`+`main.go`+`docs`, and copy only `docs/swagger.json` into the builder; a PR that touches no API package then reuses the generated doc. | **~10–21 s** |
| 5 | **Profile-exact compile graph / per-integration prebuild** | ci#423 (`Host` boundary) + ci#345 (prebuilt integration blocks): today a `full` build compiles all 26 modules in one unit; profile-exact `imports_gen.go`/build tags let a `core-only` or partial profile compile only its own set. | **40–80 s** (structural) |
| 6 | **Path-filter the image build for docs/frontend-only PRs** | the job is label-triggered only; a `paths-ignore`/`docs-only` short-circuit skips a 6-minute build entirely. | whole job |
| 7 | **Publish per-stage timings as a trend** | `--progress=plain` teed to an artifact plus a per-stage table rendered into the PR progress comment; regressions become visible without reading job logs by hand. | 0 s (observability) |
| 8 | **Cache compression** | `cache-to … compression=zstd` on the registry exporter shrinks the (now smaller) export. | few s |

Ranks 1–3 are pure build/CI plumbing and are safe to land incrementally and
independently. Rank 5 is the only *structural* fix and stays gated on ci#423.

## 5. How to reproduce these numbers

```bash
# pick a successful Build Backend Image Once job
gh run list --repo msgmate-io/open-chat-go \
  --workflow run-pr-ci-tests-and-deployments.yaml --limit 20 \
  --json databaseId,conclusion,headBranch
gh api repos/msgmate-io/open-chat-go/actions/runs/<run_id>/jobs \
  --jq '.jobs[] | select(.name=="Build Backend Image Once") | .id'
gh api repos/msgmate-io/open-chat-go/actions/jobs/<job_id>/logs > job.log

# per-stage wall times, biggest first
grep -oE '#[0-9]+(\s+\[[^]]*\])?.*(DONE|CACHED)\s+[0-9.]+s' job.log \
  | sort -t' ' -k1 | awk '{print}' | sort -rnk3 | head -25
```

Job logs are the authoritative source: `docker/build-push-action` prints the
BuildKit node index, the stage/step it belongs to and the wall time for every
step, and the registry cache export (`#NN exporting cache to registry`) is
always the last big node.
