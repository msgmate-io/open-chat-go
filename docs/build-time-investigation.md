# `Build Backend Image Once` — measured breakdown and ranked plan (ci#513)

Status: **living** investigation for
[ci#513](https://github.com/msgmate-io/open-chat-go-ci/issues/513)
("Drastically reduce 'Build Backend Image Once' time").

Everything below is measured from the raw job logs of real runs
(`gh api repos/msgmate-io/open-chat-go/actions/jobs/<id>/logs`). Numbers are
wall-clock seconds BuildKit printed for each node at `DONE`/`CACHED`.

## 1. Warm vs. cold module graph (the dominant variance)

Three real `Build Backend Image Once` jobs, all `INTEGRATION_PROFILE=full`,
success:

| job (run, started) | branch | job wall | `basebuilder` module graph | `#NN` cache export |
| --- | --- | --- | --- | --- |
| `117743255998` (`39150552200`, 17:39Z) | `fix/670-repetition-loop-guard` | 293 s | **CACHED** | **9.2 s** (5.2 prep + 4.0 send) |
| `117734283687` (`39147037361`, 17:00Z) | `fix/ci-merged-reuse-pr-image` | 325 s | **CACHED** | **9.8 s** (5.3 + 4.4) |
| `117753876232` (`39152867150`, 18:26Z) | `fix/ci-620-provider-admission-cap` | **395 s** | **31.1 s re-download** | **124.9 s** (109.3 + 15.6) |

Measured per-stage verdicts (`CACHED` vs `DONE`):

| stage | warm | cold |
| --- | --- | --- |
| `manifests` `COPY clients/` → strip to `go.mod`/`go.sum` | CACHED | **DONE 0.3 s** |
| `basebuilder` `COPY integrations.lock.json` | CACHED | **DONE 10.3 s** |
| `basebuilder` `COPY development/` | CACHED | **DONE 0.2 s** |
| `basebuilder` `COPY --from=manifests /clients` | CACHED | **DONE 0.1 s** |
| `basebuilder` `COPY backend/go.mod backend/go.sum` | CACHED | **DONE 0.0 s** |
| `basebuilder` `resolve && GOMODCACHE=/gomodcache go mod download` | **CACHED** | **DONE 31.1 s** |
| `basebuilder` `COPY clients/ /clients/` (full sources) | DONE 54.2 s | DONE 1.5 s |
| `builder` `full_build.sh --no-frontend` | 166.7 s | 159.5 s |
| `#60` cache export to registry (`mode=max`) | 9.2 s | **124.9 s** |

**The cold run costs ~+102 s end-to-end, all of it cache invalidation**, not
compilation: +31 s of module re-download and +109 s of registry cache
re-serialisation. The Go compile itself is remarkably stable (159–179 s in every
sample).

### Why the cold run was cold

`117753876232` started at **18:26:55Z** — seven seconds after the private `ci`
repo landed a pin refresh
(`b2ef035dc fix(lock): msgmate_coder websearch disable build fix`, 18:26:48Z),
and the same window also carries `78cefbfad chore(integrations): disable
coder_websearch` (18:03Z). The pin move changes the integration checkouts
(`clients/integrations/**`), and in the same build:

- the `manifests` stage's own `COPY clients/ /clients/` re-ran (its layer key is
  the **content of every copied file**, i.e. all integration sources — the
  `find … -delete` that follows only shapes the *result*, it cannot shrink the
  key), so `COPY --from=manifests` re-ran too;
- `COPY integrations.lock.json` re-ran (its key did move);
- `COPY development/` re-ran — the private manifest fragment
  `development/ci/openchat/*` is materialised per run and its pins moved.

All three sit **in front of** the module download, so the download re-ran, which
rewrote the 1.4 GB `/gomodcache` normal layer, which `cache-to mode=max` then
re-serialised into GHCR — 109.3 s of "preparing build cache for export".

## 2. What increment 1 did and did not fix

The landed change keys the module graph on integration **manifests only**:

```dockerfile
FROM docker.io/library/alpine:${ALPINE_VERSION} AS manifests
COPY clients/ /clients/
RUN find /clients -type f ! -name go.mod ! -name go.sum -delete
```

and `basebuilder` copies only that before `resolve && go mod download`, with
`COPY clients/ /clients/` moved after the download. Confirmed in all three runs:
the full integration sources land at step 13/14, *after* the module graph, so a
backend-only edit no longer touches the download.

But it does **not** protect against integration-pin churn: the `manifests`
stage's own `COPY clients/` is still keyed on the full sources, so an
integration edit invalidates the whole chain. That is the observed cold run.

## 3. The host-side Go build cache is still a no-op (≈ 0 bytes)

In `117753876232`:

```
18:27:16  Cache hit for restore-key: docker-go-build-Linux-50082c11cfb3e49…-d9173795a0b4609c…
18:27:16  Cache Size: ~0 MB (220 B)          <- restore key matched an EMPTY entry
18:32:59  Cache saved with key: docker-go-build-Linux-50082c11cfb3e49…-e1d317a685d08ad3…
```

`cache-to=type=registry` never exports the `type=cache,target=/root/.cache/go-build`
mount, and the Dockerfile only *consumes* the `gobuildcache` named context:

```dockerfile
--mount=type=bind,from=gobuildcache,target=/gobuildcache,ro
```

Nothing writes the warmed compiler cache back out, so every run recompiles the
whole dependency tree inside the 159–179 s `full_build.sh`. The `#13 [context
gobuildcache] load from client` step transfers **2 B**.

## 4. Ranked plan (each item with an estimated saving)

| rank | change | mechanism | est. saving |
| --- | --- | --- | --- |
| 1 | **Key `manifests` on manifests only** | give the stage a context that contains nothing but `go.mod`/`go.sum` (a host-side manifest-only tree passed as a named build context, e.g. `--build-context manifests=<dir>` generated by `openchat-integrations`, or a tarball). Then an integration *source* edit cannot invalidate the module graph; only a real manifest change can. Keep the current copy+strip as the local fallback. | **31 s + 109 s on every integration-pin move** |
| 2 | **Take pin churn out of the pre-download layers** | copy only `development/build-tools` + `development/scripts` before the download and move `development/ci/openchat/*` (and the private lock) *after* it, so the private fragment — refreshed several times a day — cannot re-key the graph. | removes the 10.3 s lock copy + its knock-on |
| 3 | **Round-trip the Go build cache** | after `full_build.sh`, stash the populated `/root/.cache/go-build` into a small normal layer and read it back on the host with a second, fully cached `docker buildx build --target <export-stage> --output type=local,dest=$RUNNER_TEMP/open-chat-go-build-cache`. Keep `\|\| true` so it can never fail the build. | **60–90 s** |
| 4 | **Stop re-exporting the 1.4 GB `/gomodcache` layer** | `/gomodcache` is a normal layer precisely so `mode=max` re-exports it (the 109 s tail). Move it to a hash-keyed base image (`modcache-<sha256(go.mod,go.sum,lock)>`) or a second `type=registry` exporter scoped to that stage. | **60–110 s** |
| 5 | **Avoid materialising `COPY clients/` as a layer** | the full integration sources are a 1.52 GB layer that costs **54–57 s** on warm runs (and 1.5 s cold); a tarball/streamed context or `--mount=type=bind` for the compile step avoids writing it as an image layer at all. | **~50 s** |
| 6 | **Take `swag init` off the compile stage** | generate swagger in its own stage keyed on `backend/api/**`+`main.go`+`docs`; a PR touching no API package reuses the generated doc. | **~10–21 s** |
| 7 | **Profile-exact compile graph / per-integration prebuild** | ci#423 (`Host` boundary) + ci#345 (prebuilt integration blocks): a `full` build compiles 26 modules in one unit; profile-exact `imports_gen.go`/build tags compile only the profile's set. | **40–80 s** (structural; unchanged — this is the ~160 s compile) |
| 8 | **Path-filter the image build for docs/frontend-only PRs** | the job is label-triggered only; a docs-only short-circuit skips a 5–7 minute build entirely. | whole job |
| 9 | **Publish per-stage timings as a trend** | `--progress=plain` teed to an artifact plus a per-stage table in the PR progress comment; the warm/cold jump above would have been visible without reading job logs. | 0 s (observability) |
| 10 | **Cache compression** | `cache-to … compression=zstd` on the registry exporter shrinks the export further. | few s |

Ranks 1–4 are pure build/CI plumbing, independently landable, and together
remove the entire cold-run penalty (~140 s). Rank 7 is the only structural fix.

## 5. How to reproduce these numbers

```bash
# pick the Build Backend Image Once job of any recent run
gh run list --repo msgmate-io/open-chat-go \
  --workflow run-pr-ci-tests-and-deployments.yaml --limit 20 \
  --json databaseId,conclusion,headBranch
gh api repos/msgmate-io/open-chat-go/actions/runs/<run_id>/jobs \
  --jq '.jobs[] | select(.name=="Build Backend Image Once") | .id'
gh api repos/msgmate-io/open-chat-go/actions/jobs/<job_id>/logs > job.log

# per-node verdict and wall time (CACHED vs DONE), biggest first
grep -oE '#[0-9]+ \[[a-z_]+ +[0-9]+/[0-9]+\][^#]*?(CACHED|DONE [0-9.]+s)' job.log
grep -oE 'preparing build cache for export [0-9.]+s done' job.log
grep -oE 'Cache (Size|hit|saved)[^"]*' job.log
```

`docker/build-push-action` prints the BuildKit node index, stage/step and wall
time for every step; `#NN exporting cache to registry` is always the last big
node when the module graph changed.
