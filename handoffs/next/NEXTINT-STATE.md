# NextInt integrator state (updated 2026-10-30)

Successor of NEXTPIN-STATE.md (`~/PiG-launch-newer/handoffs/next/`). Branch `fix/follow-pi-next`, worktree `~/wt-follow-next` on the Mac (the main checkout `~/PiG` is on another branch). Push via the `dp` remote. Commits: `git -c user.name="Michael Kinsy" -c user.email=54590707+MichaelKinsy@users.noreply.github.com commit --signoff -S`; merges `--no-ff -S --signoff`.

## Merged on top of 2345dc3fcf (all signed)
1. `origin/fix/follow-iface-close` ec9d6b3dd3: clean merge (20deec845b).
2. `origin/fix/follow-d5a` 0b76ae5e5f: clean merge; then a second merge of `origin/fix/follow-d5a` a422947b5f (documents shared-load race fix) (fe7b6cb5d2). Later D5a SHAs are merged the same way.
3. Integrator commits: D5a dispositions, PORT_MAP rows, test rows and async contracts (697ef331a4); fragment `chord-d5a-host-core.md` renamed `chord-0b76ae5e5f.md`, Photon fix markers, D5a design-out/missing cases resolved to exact upstream ids (bfbf78ea36); `style: apply go fix` (e4f0185a13: coding/cli/diagnose_default_model.go, coding/extension/host/runtimecell/lisp_quicklisp_test.go, coding/extension/host/subprocess/tui_alt_bridge.go, durable/core/session/{held,session}.go, env/connection.go, internal/evals/evalsuites/prompt_templates.go, internal/jsstring/deps_test.go, test/docs-drift/docs_drift_test.go, test/extension-conformance/testfixture/tui_alt.go; mechanical: CutPrefix, maps.Copy, SplitSeq/FieldsSeq, slices.Backward, errors.AsType, strings.Builder, `new(expr)` for the `newPublication` helper, loop-variable copy removed); generate (6f4b255cc4: interface gaps, mapping, bug ledger, D5a bench evidence points at jobs/job.ts).

## Tools (outside the repo, ~/integrator)
ingest.py (patched: comma-separated multi-file evidence tokens), ingest_async.py, asyncrows.py, ingest_tests.py (strings in designedOut/missing must be exact upstream ids), badev.py, settest.py, setdisp.py, gojson.py. `/tmp/d5a_async2.py` mapped the d5a/d5b async tables by file basename.

## Gate recipe (rivendell, tree ~/tern-v5/trees/nextint, one container at a time; never two scripts in the same tree)
Clone from github (`git clone -b fix/follow-pi-next https://github.com/MichaelKinsy/PiG.git`), then `git fetch` a bundle of the local commits. The Pi mirror is gitignored: copy `.upstream/v1.4.0+e7a0df27`, `current`, `README.md` from another tree (`~/tern-v5/trees/d5a-gate`). `npm_config_cache=/home/kinsy/.cache/npm` is required in the container (HOME is not writable). Order: `autobind -update -import-decisions`, `make ledger-update` x2, `make generate` (stops at known-gaps, which is expected red), then `custom-factory-ledger`, `bug-ledger`, `coverage RESULTS=` separately, then the check targets. `make generate` is first-extraction slow (interface extractor, ~25 min); cached afterwards.

## Expected reds (owned elsewhere)
known-gaps: store-storage.test.ts (D-store), harness-registry-reader.test.ts partial (chord-next interface lane), chord sqlite/cloudflare.test.ts. upstream-delta/async/port-map pending: durable store (7) + registry-reader.ts. interface-mapping-strict: chord/next and durable storage.

## Known issues found in the merge
* `make test-expectation-lint TEST_EXPECT_BASE=2345dc3fcf`: 11 test files changed in the D5a feat commit 0b76ae5e5f without a Pi-Behavior-Change trailer or a `test/parity/testprune-reviewed.toml` entry (chord/next/host/{host_activity,host_attempt,host_examples,host_keyed_members,host_keyed_slot_host,host_object,host_reference,host_revocation,host_route,host_routines,remote_host_link}_test.go). Needs the D5a author to review (class CITE with a Pi line) or a trailer.
* `make coverage` was failing on `test/parity/unit-evidence/follow-d5a.json` naming the untracked upstream `packages/chord/test/next/bench/jobs.ts` as `upstream`; changed to `packages/chord/src/next/jobs/job.ts` (the reference stays bench/jobs.ts).

## Ratchet
See `ratchet-breakdown.md`.
