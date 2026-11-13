# partial-chord-edge (resume note)

Rows: chord link/websocket.ts, node/http/node.ts, node/surfaces/published.ts, surfaces/format.ts, tui/terminal.ts. Stopped at the request budget.

Done (tests + mutation proof, run on xd670 in tern-ci:v10 (xd6702 is integrator-only)):
- websocket.ts: tests for send/close/readyState; ledger rows readyState, close(+call:0), send(+call:0) now `ported` in mapping-v1.5.1.json. Mutants WS1-WS5 killed.
- published.ts: new surfaces_published_test.go (retention of 4 sets, failed-build carry-over, digests/names, source maps, assets, rollout Step). One production fix: the Step lists removed plugins in UTF-16 order (compareUTF16), as Pi's sorted set. Mutants PS1-PS14 killed.
- node.ts: URL parser port in url.go checked against Node's URL by testdata/url_corpus.jsonl (url_probe.cjs, 3931 rows incl. IPv6 compression ties); wireTarget in node.go (Node's parser takes 0x21-0x7e only) checked by target_corpus.jsonl; D218 text extended. Mutants U1-U11 (U9 needed the IPv6 tie rows), N1 killed.
- format.ts: ledger rows formatNumber/formatTime (+call:0) closed `ported` (public-api-tested; D464/D465/D466). Mutants F1-F4 killed.
- terminal.ts: tui_terminal_test.go + tui_terminal_pty_linux_test.go (real pty; new test for size from stdout and TTY answer from stdin). Mutants T1-T11 killed (T8 equivalent: dropped).

Not done:
- `go run ./test/parity/cmd/portmapcheck` for the five rows (derives the tick; it runs the whole tree's evidence, ~20+ min; kill it by running with a shard or after setting the rows to ✅ and comparing). Rows stay 🟡 with a Missing line until it proves them.
- Regenerators not run: make divergences (D218.md edited), interface-gaps-update, interface-go, bug-ledger, known-gaps, test-inventory-generate, docs-bundle, coverage RESULTS= . lane-precheck.sh not run. changelog fragment not written (name it chordedge-<sha of the code commit>.md; Fixed bullets need `<!-- fix: <sha> -->`).
- Note: tui_terminal.go is Pi's launcher-side process terminal, not a port of terminal.ts (BridgeTerminal, the vendored sandbox copy); the row cites both.
- Windows vet of tui_terminal_test.go (GOOS=windows go vet ./chord/nextnode/tui/) not run.
