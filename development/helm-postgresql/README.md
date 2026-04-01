# ci#667 - Helm chart PostgreSQL support

The backend Helm chart knows two datastores: SQLite (default, PVC-backed) and
PostgreSQL. PostgreSQL support was added as a single `postgresql.mode` switch with
three values (`off` | `internal` | `external`) instead of a subchart dependency, so
an existing release can switch datastore without a chart-repo dependency tree and
without a second Postgres operator being required.

* `off` (default) - unchanged behaviour: `DB_BACKEND=sqlite`, PVC-backed SQLite.
* `internal` - the chart deploys its own single-writer PostgreSQL instance
  (`backend/templates/postgresql.yaml`: StatefulSet + Service + PVC) and points the
  backend at it (`DB_BACKEND=postgres` plus host/port/db/user).
* `external` - no Postgres workload is created; the backend is pointed at a
  PostgreSQL the operator runs elsewhere (managed database, another release, ...).

In both non-off modes the SQLite env (`DB_PATH`) and the SQLite PVC are left out of
the rendered manifest, and the statement-size/cache env for Postgres is wired
instead.

## Where this lives

The chart itself lives in the private repository
`msgmate-io/open-chat-go-helm-chart` (`development/helm/**` is git-ignored in this
monorepo, see `.gitignore`). The durable, reviewable record of the chart-side change
is therefore carried here as a patch, plus the values overlays it adds.

## Files

* `ci-667-postgresql.patch` - the complete chart change, relative to the chart root
  (paths are `backend/...`, so it applies at the chart root, not in this directory).
  Touched: `backend/templates/backend.yaml` (mode switch, env wiring, config
  secret), new `backend/templates/postgresql.yaml`, new
  `backend/values.postgresql-internal.example.yaml`, new
  `backend/values.postgresql-external.example.yaml` and `backend/values.yaml`
  (documented `postgresql` block with `mode: off` default).
* The two example overlays are the documentation of the external mode: they carry
  host, port, database and user only - never a password. The password is read from
  an existing Secret (or an out-of-band managed credential), so no secret value is
  committed here or rendered into the chart.

## Applying / verifying

From a checkout of `open-chat-go-helm-chart`:

```sh
git apply /path/to/development/helm-postgresql/ci-667-postgresql.patch
helm lint backend
helm template t backend                                                   # still sqlite
helm template t backend -f backend/values.postgresql-internal.example.yaml
helm template t backend -f backend/values.postgresql-external.example.yaml
```

Regression check used for this change: every existing overlay
(`values.yaml`, `values.msgmate-io-{staging,ci,ci2,pr-deployment,production}.yaml`)
renders **byte-identical** to before the patch, i.e. Postgres stays opt-in and
nothing changes for the deployments that select SQLite. `helm lint backend` passes
for all variants, and an invalid `postgresql.mode` fails the render with a clear
message.

## Not part of this change

* No credential or connection string is committed anywhere.
* Migration of existing data from SQLite into PostgreSQL is a data operation, not a
  chart feature; the chart only switches the backend datastore.
* Applying the patch to the private chart repository and rolling a release is done
  by the deployment flow, not from this PR.
