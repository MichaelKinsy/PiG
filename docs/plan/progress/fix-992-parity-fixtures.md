# fix-992-parity-fixtures: P1 and P2 from the upstream 0.99.2 parity aggregate

Base: porter/pi-0.99.1 tip 6d3a0f17a. Oracle: upstream 0.99.2 (`.upstream/v0.99.2`, the locked package under `extensions/sdk-ts/node_modules`). Every scenario was run against it with the parity runner, serially first and then in parallel.

## P1: stale probe fixtures (all pass against upstream 0.99.2 on both sides)

| Scenario | Stale expectation | upstream 0.99.2 behavior | Fix |
|---|---|---|---|
| 17-tool-rpc-error-and-bash-wire | `--version == "0.87.1"` | pin is 0.99.2 | `tool-rpc-wire.py` reads `UpstreamVersion` from `internal/coding/pigversion/pigversion.go` |
| 40-suspend-resume-session | `expected pinned Pi 0.87.1` | same | `suspend-session-native.sh` reads the pin the same way |
| 00-startup-banner | `v0.3.0+0.99.1`, `v0.99.1` | banner carries the pins | `v{{VERSION}}`, `v{{UPSTREAM_VERSION}}` tokens |
| 34-extension-process-identity | regex pinned `0\.87\.1` | relaunch prints 0.99.2 | `{{UPSTREAM_VERSION}}` inside `both_match_regex` (runner change below) |
| 01-ai-sdk-helpers, 18-images-runtime-auth | `builtinImagesModels()` | removed (`packages/ai/CHANGELOG.md:26`); image models live in `builtinModels()` / `Models.generateImages` | fixtures use `builtinModels()` (image providers = providers with `generateImages`; `type: "image"` on the model) |
| 13-compaction-completion-auth-errors | no-auth compaction on an empty session rejects with "No API key found" | `compact()` checks "nothing to compact" first and resolves auth when summarizing (`agent-session.ts:2699-2715`, `agent-session-compaction.test.ts:282-288`) | the no-auth case seeds a compactable session, as the upstream test does |
| 11-session-fork-label-boundaries | a fork of an unanswered user message is not written | `_hasConversation()` persists once the path holds a user or assistant message (`session-manager.ts:1166-1170,1717-1724`; `tree-traversal.test.ts:483-585`) | block mirrors the two upstream tests |
| 28-session-reload-ui, 29-session-rebind-ui | `#5943` sites 418/451/475 and 276/313/368 | the same cases sit at 380/413/437 and 238/275/330 (helper import moved, bodies unchanged) | scenario scripts and the Go probe sites (`internal/codingagent/re*_ui_upstream_test.go`) use the 0.99.2 lines |
| 56-node-lazy-imports | `38;2;181;189;104` | Pi's default theme in print mode is the system theme: `38;5;2` | expectation updated; **red on PiG, finding F1** |

AGG 4 additions (fail on both sides serially): 01-config-empty (the empty home lists the built-in extensions), 21-post-login-default-model (opencode-go default is kimi-k3, 1.0M window, level max), 06-oauth-callback-page (`oauth-harness.ts` imported `dist/auth/oauth/oauth-page.js`; upstream 0.99.2 has `dist/utils/oauth-page.js`). All three pass on both sides.

Runner changes: `both_match_regex` expands `{{VERSION}}` / `{{UPSTREAM_VERSION}}` with the pins quoted as literals. New guards: `TestScenarioAssertionsNameThePinThroughTokens`, `TestProbeFixturesReadThePinFromItsSource`.

## P2: tmux `capture-pane` exit 1

Root cause (not harness concurrency): another process kills the isolated tmux server mid-run. Evidence:

- The AGG artifacts (`wt/agg-992/test/parity/artifacts`) show two instants, 21:09:20-24 (02, 03, 04, 05) and 21:13:47 (09, 10, 11, within 0.5 s), where BOTH binaries fail `capture-pane` and the cleanup `list-panes` exit 1; scenarios started later pass. 01-config-empty, 21 and 06 did not fail this way: they failed on their stale content (above).
- The same scenarios passed serially, all seven at once, in a 254-scenario interactive-tmux run at 24 and at 120 concurrent pairs, and in five full 32-way runs. Only the first full run showed the failure (22:58:35-39 and 22:58:54-59, while I was running no command); a sixth run also showed it because I killed its runner myself.
- Every lane task template tells the lane to finish with `pkill -f "[t]mux -L pig-parity"`. All lanes share one uid, and the isolated server's argv is `tmux -L pig-parity-<runid> -f /dev/null new-session ...`, so that pattern kills the parity server of every lane's in-flight run. `/tmp/tmux-UID` holds 2300+ `pig-parity-*` sockets whose servers were killed.
- Reproduced the exact signature: starting 02-05 in parallel and running that pkill against the run's socket 9 s in gives `capture-pane: exit status 1` and `inspect panes for <session>: exit status 1` for every in-flight session on both sides, while a scenario started afterwards passes (log `tmp/demo.log`).

Harness changes, both red-first:

1. A failed capture or pane inspection reports the server's state: recorded server pid (running, exited, or lost and replaced), socket path present or missing, remaining sessions, whether the scenario's session is among them (`tmuxServerReport`).
2. `ensureTmuxServer` checked a `sync.Once`, so after the server was killed the replacement (created implicitly by the next `new-session`) had tmux's defaults and no keeper: every later scenario ran without `extended-keys`. It now checks the keeper on each call (exact-name `has-session`, which avoids tmux's prefix matching) and starts the server again.

Not added: a retry of the lost pair. `RunScenario` states that a retry must not turn an earlier failure into a pass (AC61), and the fix for this fault is the cleanup instruction, not the harness. Not added either: serialization.

## Findings for other owners

- F1 (PiG-only, real): print/json/rpc and `--export` never run the equivalent of Pi's `initTheme(settingsManager.getTheme())` (main.ts:898) / default to the system theme (`getResolvedThemeColors`). Pi renders palette entries as `\x1b[38;5;N` and export CSS as ANSI-16 (`--accent: #800080`); PiG keeps tui's builtin dark theme. Verified with identical env and COLORTERM variants. Red: 56-node-lazy-imports, 01/02/03/04/06-export-cli-*, 29-export-tool-renderers.
- Remaining full-run failures (19 of ~620), none in this lane: P3 builtin order (26, 28, 55), P4 01-slash-popup, P5 export (above), P6 05/07/09-startup, 10-startup-trust-prompt-wording, 24/25-tool-renderers, 10-login-subscription-providers, 19-http-proxy-connect.

## Red-green record

| Commit | Kind |
|---|---|
| a4592d7ab test(parity): both_match_regex expands the version pins as literals (red) | red: `TestBothMatchRegexExpandsVersionTokensAsLiterals` failed (`{{UPSTREAM_VERSION}}` not expanded) |
| 4dd2fcf18 fix(parity): both_match_regex expands the version pins; 34 reads the pin (green) | mutation: removing `QuoteMeta` fails the same test (`"0099.2"` matched) |
| 7bdf4f44b fix(parity): re-derive the stale probe fixtures from upstream 0.99.2 (green) | scenarios failed before (baseline run `tmp/p1-base.log`), pass after |
| fc22fef44 test(parity): guard probe fixtures and scenario expectations against hard-coded pins | mutation: restoring the 0.87.1 literals in 17 and 40 fails `TestProbeFixturesReadThePinFromItsSource` |
| def72fe6c test(parity): 56 expects upstream 0.99.2's system-theme highlight | red on PiG by design (F1) |
| 6326d6c28, 969d66d56, a7ba7de69 | 21, 06, 01-config-empty fixes (green) |
| 5784eb99a test(parity): report the tmux server's state when a capture fails (red) | red: report was empty |
| 1ec47259a fix(parity): a failed tmux capture reports the server's state (green) | |
| (next) test(parity): ensureTmuxServer configures the replacement for a lost server (red) | red: replacement pid equals the lost pid |
| fix(parity): ensureTmuxServer notices a lost server ... (green); fix(parity): the tmux report names a replaced server (green) | |

Upstream tests: this lane changes probe fixtures and harness code, not Pi-ported tests; the only Go probe edits are re-citations of line sites in `internal/codingagent/reload_ui_upstream_test.go` and `rebind_ui_upstream_test.go` (cited above).

## Commands run

`go build ./...`, `go vet ./test/parity/... ./internal/codingagent/`, `GOOS=windows go vet -tags=parity ./test/parity/runner ./internal/codingagent/`, `gofmt -l`, `go fix -diff`, `go tool golangci-lint run --build-tags=integration,live,parity ./test/parity/runner/ ./internal/codingagent/` (0 issues), `go test -race -tags=parity ./test/parity/runner` (all but TestParity, which needs the binaries; TestParity checked per scenario), `go test -race ./internal/codingagent -run 'TestSessionStartNotifyOriginal|TestStartupRebindOriginal'`. Parity: every scenario above, serially and in parallel; `make parity-family` for compaction, selectors, model-resolver-selector and oauth (only 10-login-subscription-providers fails, P6); five 32-way full runs.

## Deferred

- F1 (above) and the P3-P6 rows: other owners.
- The lane task templates' pkill instruction: lead (QUESTION posted).
- `make generate` outputs (normalization inventory, coverage) are regenerated in the last commit.
