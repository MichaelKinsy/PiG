# plugfull: closing the plugnode differences (branch fix/follow-plugfull on dp)

Designs: docs/plan/chord-plugins-node-evaluator.md section 10. Status per difference with evidence: plugnode-differences.md; per-slice rows: plugfull-{store,callbacks,lifecycle,bridge,buildwin}.md.

Closed: #1, #2 (except keyed members of a bridged Go core facet), #3, #4, #7, #8 (additive isolation stated), #9, #11, #13 (Windows runner). Open: #10 (owner call: who ships Node), #12 (Pi's location is a line of esbuild's JS library; not producible without inventing it), keyed members of a bridged Go core facet (Pi's KeyedSlot is owned by an object's own Documents).

Gates on a fresh clone (xd670, tern-ci:v6, merged with next e6a2a2123b): `go test -race -count=3 ./chord/nextnode/... ./chord/next/...` green; `go test ./chord/host/... ./test/ci-images/...` green (package boundary, version literals); GOOS=windows vet clean; the Windows runner passed `go test ./chord/nextnode/... ./internal/chord/winpipe/` 3 of 3 (before the merge of next).

Still to do by the integrator: `make test-expectation-lint` reports commit 0ad6843579 (lazy attach, #11) changing an expected value in plugins_node_test.go without a `Pi-Behavior-Change:` trailer: reword it with `Pi-Behavior-Change: packages/chord/src/next-node/workers/main.ts:114` (Pi's heartbeat starts when the app connects the parent link). I did not rewrite history (it holds next's commits). `make lint-changed` (LINT_BASE=3bae268a96) was 0 issues before the merge of next and the NodeOptions.Command commit; re-run it on the merge.
