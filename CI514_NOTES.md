# ci#514 — bash retirement (drive notes)

## Status
- Phase 1 exit criterion 4 (write verification) evidence surface: **PR #1308** open against `dev` (label `test`), re-landing unmerged `b73a697b` content.
- Phase 1 criteria 1–3 and S1 coverage payload already on `dev` via earlier merges.
- Remaining closed-out items from earlier runs: bash_retirement_report script, drive record `CI514_DRIVE.md` — no longer needed as separate artifacts (consolidated).

## Verification (local, current branch)
```
cd backend && PATH=/usr/local/go/bin:$PATH
go build ./nativecoder/   # passes
go test ./nativecoder/ -count=1   # passes (targeted criterion tests)
gofmt -l api/msgmate/bash_coverage.go   # clean
go build/vet ./api/msgmate/ # clean apart from pre-existing mailru/easyjson go.sum noise in sandbox
```

## env quirks
- Go toolchain at /usr/local/go/bin (not on PATH by default): export PATH=/usr/local/go/bin:$PATH.
- msgmate package build prints missing-go.sum noise for mailru/easyjson etc. (pre-existing; CI resolves).

## PRs / branches
- ci/514-bash-retirement-criterion4-reland  ->  PR #1308 (base dev)
