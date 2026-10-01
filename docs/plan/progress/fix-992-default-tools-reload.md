# fix-992-default-tools-reload

Lane for audit-992-coding F6 (defaultTools cannot activate extension tools at startup) and F4 (ctx.reload() in print/JSON/RPC). Base: staging porter/pi-0.99.1 @ 936e62509. Oracle: Pi 0.99.2 source (`.upstream/v0.99.2`).

## F4: already closed at the porter tip

fix-99-j13 (merged 94f45865e, `cmd/pig/headless_reload.go`) binds `ctx.reload()` in every mode (print-mode.ts:97-99, rpc-mode.ts:341-343) and activates the tools a reload newly adds to `defaultTools` (agent-session.ts:3587-3609). The audit's red `TestAuditPrintModeExtensionCommandReloadReloadsLikePi` passes on the tip, so it was not cherry-picked. What remained was the extension-tool case: `cmd/pig/default_tools_extension_test.go::TestReloadActivatesExtensionToolsAddedToDefaultTools` (print, JSON, RPC; a reload that adds `+codemode` activates `codemode`, observed from the new instance's `session_start`) passes on the tip as well and stays as a guard. Nothing to implement for F4.

## F6: root cause and fix

The CLI resolves `defaultTools` (`settings.GetDefaultTools()`, cli_runtime_build.go:454) and passes the names as `SessionOptions.ActiveBuiltinTools`. `newSessionToolRegistry` (coding/session_tool_registry.go) turned that map into active names only for the built-in tools, so a name such as `codemode` was dropped. Pi passes the whole list as `initialActiveToolNames` (sdk.ts:264-269) and `_refreshToolRegistry` activates every registered tool it names (agent-session.ts:3487-3506). Fix: names in `ActiveBuiltinTools` that are not built-ins are appended to the active names; `selectTools` keeps those the registry admits (an unregistered name activates nothing). Print, JSON and interactive start through that path; RPC already selected its startup loadout itself (`rpc_initial_tools.go`) and passed before the fix.

## Commits (red/green order)

1. `72e3f683f` test(cmd): port Pi 0.99.2 defaultTools extension-tool activation tests (red) — startup (print/JSON/RPC/interactive x 3 settings) fails for print, JSON, interactive; `+codemode` reload test.
2. `test(cmd): keep the built-in extensions in the reload defaultTools test` — correction of my own test: `--no-extensions` also drops the built-in extensions (resource-loader.ts:569-578), so `codemode` was never registered. Red proof of the reload test is not possible: the tip already passes it (F4 above).
3. `424eb0b6c` test(coding): ActiveBuiltinTools naming extension tools activates them (red) — 2 of 4 subtests fail (`active = [bash edit read write]`, `active = []`).
4. `b8a8a648c` fix(coding): defaultTools activates extension tools at startup (green).
5. READY commit, and `chore(port-99): regenerate generated files`.

The audit's `TestAuditCodemodeOnlyPromptOmitsHiddenToolsInEveryMode` was not cherry-picked: its `print defaultTools` case is covered by the new startup test (same declarations), and its `interactive tools flag` case is F7, owned by fix-992-prompt-options (already holds that file).

## Evidence

- Red: `go test ./cmd/pig -run TestDefaultToolsActivatesExtensionToolsAtStartup` on the pre-fix registry: `print mode declares [read bash edit write], want [... codemode]`, `print mode declares [], want [codemode]` (also json, interactive).
- Green: same test, 12 subtests pass; `go test -race ./coding/ ./coding/extension/ ./internal/codingagent/... ./cmd/pig` pass.
- Load: `GOMAXPROCS=4 taskset -c 0-3` with 4 burner PIDs I started (stopped with `kill <pid>`): `go test -race -count=24 ./coding -run 'TestNewSessionActiveBuiltinToolsNameExtensionTools|TestDefaultTools'` ok; `-count=3` of both cmd/pig tests ok.
- Mutation: restoring the pre-fix registry fails the coding and cmd/pig tests (above).
- Gates: build, vet, `GOOS=windows go vet ./coding ./cmd/pig`, gofmt, `go fix -diff`, golangci-lint (0 issues) pass.

## Environment notes

- The worktree had no `extensions/sdk-ts/node_modules`; I symlinked agg-992's (untracked, not committed) to run the Node-backed tests. The Pi oracle in it is 0.99.2.
- `make parity-family FAMILY=settings` passes (14 scenarios). `tools` and `startup` fail on scenarios pinned to Pi 0.87.1 / 0.99.1 (`tool-rpc-wire.py` asserts `--version == 0.87.1`; the startup banner scenarios expect `v0.99.1`) against the 0.99.2 oracle. They are oracle-version fixtures, unrelated to this change (fix-992-parity-fixtures owns them).

## Deferred / notes for other lanes

- Order: closed in review (rev-fix-992-default-tools-reload). The registry orders the `ActiveBuiltinTools` set by the resolved `defaultTools` list (sdk.ts:268-270, agent-session.ts:1508-1511), so `["codemode","read"]` declares `codemode, read` as Pi 0.99.2 does. `--tools` keeps registry order (`["bash","read"]` declares `read, bash`; Pi declares `bash, read`); that predates this lane and is left to the owner.
- No divergence recorded; no stubs for other families.
