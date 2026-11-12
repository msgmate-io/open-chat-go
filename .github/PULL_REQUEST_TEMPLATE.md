## Summary

<!-- What changes, and why. One or two sentences. -->

<!--
Open this pull request against `dev` (never `main`), as a ready-for-review pull
request (not a draft), with the label for its class: `test` for a
feature/bugfix/docs/chore PR, `test` and `e2e` for a `chore/consolidate-*` PR.
See AGENTS.md ("Pull request conventions") for the full list.
-->

## Tracker

Closes msgmate-io/open-chat-go-ci#<n>

<!--
Replace `<n>` with the leaf issue number. Keep the reference on a line of its
own - that is what `PR Test Merge Requirement` reads (a `- ` bullet, `**bold**`
or a table cell around it is fine, other prose on the same line is not).
GitHub cannot auto-close a cross-repository issue, so this line is what the gate
requires and what closes the tracker issue after merge.
-->

## Verification

<!-- Commands you ran, or the CI job that covers this change. State truthfully
     whether anything ran locally - the coder sandbox has a Go toolchain with a
     warmed module cache, so targeted `go test ./<pkg>/...` normally can run, and
     the full suite/E2E remain PR CI's job. If the toolchain is missing, run
     development/scripts/check_coder_sandbox_readiness.sh and write `go: MISSING`
     instead of claiming a suite ran. -->

## Notes for reviewers

<!-- Optional: rollout, follow-ups, anything that is deliberately out of scope. -->
