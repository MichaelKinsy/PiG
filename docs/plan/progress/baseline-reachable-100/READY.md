# READY: baseline-reachable-100

Branch: team/smc1/baseline-reachable-100 (base agg-100).

## Cause

`test-porting-policy-v1.0.0.json` anchored `baselineCommit` on 18cdea972, a staging-only commit. The release is one squash on public main, so `git ls-tree` exits 128 in `test-porting-release` and `known-gaps-drift`. Nothing checked that the anchor is on public main.

## Fix

- `test/parity/cmd/testinventorycheck/release_policy.go`: `requireReachableFromPublicMain` runs `git merge-base --is-ancestor <baseline> origin/main` and fails with a specific error when the commit is not an ancestor or `origin/main` is not fetched. `automation/gen/test-ported-baseline.py` has the same guard.
- Baseline anchored on public main 0e6ed0048 (its mapping is 0.87.1, so the gate carries that floor across the leap). Carrying the floor required 15 paths ported on public main that the stored list lacked; they are added (488 to 503 paths). Nothing is lowered. `experimental-remote-runtime.test.ts` is among them and is `partial` now because its upstream hash changed in 1.0.0; the gate checks the count (574 ported, 503 baseline), not each path's current disposition.
- `docs/parity/pending-tests-triage.md` states the rule.

## Evidence

- Red: `TestReleasePolicyRequiresBaselineReachableFromPublicMain` ("no origin/main ref error=<nil>") before the guard; green after. The four existing git-backed tests now publish their anchor to `refs/remotes/origin/main` and failed until they did.
- `python3 automation/gen/test-ported-baseline.py --commit 18cdea972` now refuses (not reachable); `--commit origin/main` passes and rewrites the policy unchanged.
- `go test ./test/parity/cmd/testinventorycheck`, `go vet`, gofmt, golangci-lint (0 issues).

## Not green, by cause (not this lane)

- `make test-porting-release` and `known-gaps-drift` now get past the baseline and stop on five `designedOutCases` rows whose rationale says `SCRUTINIZED:proposed` (chord-100 blocker, owner approval needed). With those markers flipped in a temporary copy, the gate still reports open hot-path pending/partial rows (durable/harness, others). Both are inherited and unchanged here.
- The release PR check needs `origin/main` fetched; the public CI jobs use `fetch-depth: 0`.
