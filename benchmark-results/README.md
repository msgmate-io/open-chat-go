# Free-model sandbox benchmark artifacts

`free-models-<snapshot-date>.json` — machine-readable model × test capability
matrix for the private-sandbox test suite run against the free OpenRouter
models. Rendered on `/evaluations_and_benchmarks` by the
`product_landing_pages` integration (see
`frontend/components/CapabilityMatrix.tsx`).

Schema (version 1):

- `test_groups[]` — column groups: `id`, `label`, `tests[]`.
- `models[]` — one entry per free OpenRouter model:
  - `model`, `slug`, `provider`, `vision`
  - counters: `tests_run`, `pass`, `fail`, `fail_after_retry`,
    `provider_errors`, `timeout`, `not_run`, `pass_rate`
  - `cells[<group-id>][<test-id>]` — per-(test, model) final status:
    `pass`, `fail`, `fail_after_retry`, `timeout`, `provider_<class>` or
    `not_run` (e.g. `image_file_attachment` is `not_run` for text-only
    models).

Statuses are the *final* status after classification: provider-side failures
(`provider_rate_limit`, `provider_error`, ... — ci#479 classifier) and
`timeout` are kept distinct from capability `fail` so a free-tier rate limit is
not counted as a model regression.

See the methodology section on `/evaluations_and_benchmarks` for test
selection, retries, timeouts and caveats.

Regenerate: dispatch `.github/workflows/free-model-benchmark.yaml` (Open-Chat
repo) per free model (`tags=""`) and run the aggregator
(`go run ./cmd/benchmark aggregate`), then commit the resulting
`benchmark-results/free-models-<date>.json`.
