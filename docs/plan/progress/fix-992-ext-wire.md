# fix-992-ext-wire progress

Base: staging porter/pi-0.99.1. Behavior source: Pi 0.99.2 (`.upstream/v0.99.2`); cited lines are identical in `.upstream/v0.99.1` where the file did not change. Findings F1 and F3 of `docs/plan/progress/audit-992-coding.md` (staging `audit-992-coding`, e3898ddb4).

## Red

Cherry-picked the audit's red tests (620e13f84), reworded as `test(extension): port Pi 0.99.2 getMcpServers alias and registerCommand tests with signature stubs (red)`. Both compile against the current tip, so no stubs were needed.

- `go test ./coding/extension -run TestAuditRegisteredMcpServerReportsResolvedExposureAliases`: FAIL, `got ..."exposure":"codemode-deferred","toolExposure":{"a":"codemode-deferred"}... want ..."exposure":"codemode","toolExposure":{"a":"codemode"}...`.
- `go test ./coding/extension/host/subprocess -run TestAuditNodeRunnerRejectsObjectFormCommandRegistration`: FAIL, `loaded=[{object-command ...}] failures=[]` (the extension loaded).

Upstream 0.99.2 tests for this scope: none beyond the audit's two. `mcp-extension.test.ts:97-98` (alias in a configured server) is the already-ported `coding/mcpext/config_test.go:115`.

## Green

- F1 `b1582af4d`: `RegisterMcpServer` canonicalizes the alias-resolved copy (`mcp-servers.ts:151-166,192-196`, `loader.ts:479`). `resolveExposureAliases` re-marshalled a `map`, which sorts keys, so it now edits an ordered object in place (Pi: `{ ...value }` plus in-place assignment). New unexported `resolveMcpExposureAliases` (the one caller is `RegisterMcpServer`). The wire (`getMcpServers`, `mcp_servers_change`, `mcp_servers` frame, state push) carries `Declared`, so all four SDKs get the resolved copy without an SDK change.
- F3 `3d52886e8`: deleted the object-form shim (`runtime.mjs`, was 1752-1755; `loader.ts:304-308`). The host (`host.go:2886`), Go, Python and Rust SDKs already rejected it.

Object-form `registerCommand` callers in PiG (grep over the whole repo, excluding `.upstream` and `node_modules`; Pigpen is not in this repository): only `test/extension-conformance/testdata/node-ts-resolution-fixture/main.ts`, now `registerCommand("ts-resolution", {...})`. No example, piglet or doc used the object form.

## Added after green (not red-proven before the fix)

- `coding/extension/mcp_alias_resolution_test.go`: order, unknown members, no alias, non-object, invalid exposure.
- Conformance rows in `test/extension-conformance/mcp_server_order_test.go` (Node, Python; isolated and packed) and an alias assertion in `TestRustSDKMcpServerRegistration` (Rust fixture `late` server registers `codemode-deferred`). The Go SDK has a typed `McpServerConfig`; its read path is the same host reply and `TestAuditRegisteredMcpServerReportsResolvedExposureAliases` covers the value it decodes.
- Mutation check: restoring `Canonicalize(config)` in `RegisterMcpServer` fails the audit test and the Node, Python and Rust rows; restored afterwards.
- `docs/extension-api-parity.md` `getMcpServers` row states the resolved copy.

## Verification

- `go build ./...`, `go vet` (coding/extension, coding/mcpext, test/extension-conformance), `GOOS=windows go vet` (coding/extension, subprocess), gofmt, `go fix -diff`: clean. `golangci-lint` (coding/extension, subprocess, test/extension-conformance, build tags integration,live,parity): 0 issues.
- `go test -race -timeout 60m` coding/extension: ok. subprocess: only `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` fails (needs Xvfb, not installed on this host). coding/mcpext, extensions/sdk, extensions/sdk/json, installresolver, pigsdk, source: ok. The `go test` default 600s timeout is too short for the subprocess package under `-race`.
- Load: `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners, `-race -count=24`: `TestAuditNode*` and the command-validation tests in subprocess ok (648s); coding/extension `Mcp|Audit` ok. `-count=6`: conformance `GetMcpServers*` and `TestNodeTypeScriptSourceResolution*` ok (195s).
- Incident: a first load attempt used `pkill -f "while :; do :; done"` to stop my burners, which matches every lane's burners. I did not record PIDs then. The rerun records PIDs in `/tmp/fix-992-ext-wire-burners.pids` and stops them with `kill <pids>`.
- `make parity-family FAMILY=extensions-runtime`: not green on this host, for reasons outside the slice. (a) `67-reload-module-state` fails `capture-pane` (tmux sessions killed, the shared-uid pkill issue). (b) Pi's reference binary is `extensions/sdk-ts/node_modules/.bin/pi`, which I symlinked from `wt/agg-992` because npm Pi 0.99.2 is not installed in this worktree (gitignored symlink, removed before READY); its `get_commands` lists a builtin `llama` where PiG lists `mcp` (`26-extension-load-order`, `55-package-manifest-startup`), and other scenarios in the family differ for the same reason or on rendering. None touches `registerCommand` object form or `getMcpServers`. Listed as deferred; the lead's central run decides.

## Generated files

`coding/extension/host/subprocess/runtime-node.zip` and `runtime_node_digest_generated.go` embed `runtime.mjs`; regenerated with `go generate ./coding/extension/host/subprocess/` in the final `chore(port-99): regenerate generated files` commit. `TestAuditNodeRunnerRejectsObjectFormCommandRegistration` passes only with that commit applied (the F3 green commit alone leaves the old embedded runtime). The interface inventory and PORT_MAP drift gates were not run (`make generate` not run: no exported API changed).

## Stubs other families must fill

None.
