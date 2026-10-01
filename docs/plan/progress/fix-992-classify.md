# fix-992-classify

Scope: `test/parity/upstream-sync/v0.99.2.toml` rows and the unclassified-member gates in `test/upstream-parity`. Base: staging `porter/pi-0.99.1` at 6d3a0f17a. Upstream cites use `.upstream/v0.99.2/...` (the 0.99.1 -> 0.99.2 diff) because every behavior here changed in 0.99.2.

## Red evidence (before any change)

`go test ./test/upstream-parity/` on 6d3a0f17a:

- `TestContext_NoUnclassifiedPiGMembers`: `extension.Context.RefreshTools`, `Signal`, `GetMcpServers` unclassified.
- `TestRunner_NoUnclassifiedPiGMethods`: `inproc.Runner.BindSessionManager`, `Runtime` unclassified (`BeginUIPrompt`, `StaleMessage` were already classified).
- `TestDivergenceDoc_HasExpectedActiveCount`: header 32, 31 `## DN` sections.
- `go run ./test/parity/cmd/upstreamdelta`: 29 rows pending.

No upstream test file belongs to this scope (the rows classify changes other lanes ported with their tests), so there is no signature-stub red commit. The failing gates above are the red.

## B. Classification (commit "fix(upstream-parity): classify ...")

| Member | Pi counterpart | Classification |
|---|---|---|
| `Context.Signal` | `ExtensionContext.signal` (`types.ts:352`, getter `runner.ts:917-920`) | Pi member. The test list wrongly carried it as "translated to context.Context". The Go method existed and cites `runner.ts:917-920`. Removed from `translatedContextMembers`, mapped to `Signal`. |
| `Context.GetMcpServers` | `pi.getMcpServers()` (`types.ts:1845`, `loader.ts:488-491`) | Same category as the existing `SendUserMessage` entry: ExtensionAPI method placed on the Go handler Context. No divergence number. |
| `Context.RefreshTools` | `runtime.refreshTools()` action that `registerTool` runs (`loader.ts:299`, `ExtensionActions.refreshTools` `types.ts:2082,2142`) | Go binding of that action: an in-process extension stores a tool with `Extension.SetRegisteredTool`, so the refresh is a separate Context call. Production callers: `coding/mcpext/builtin.go:130`, `ui_bridge.go:2999`, `cmd/pig/session_extension_actions.go`. |
| `Runner.BindSessionManager` | Upstream runner takes `sessionManager` in its constructor (`runner.ts:394-408`) | SDK-surface/private host injection tied to D61 (in-place `Session.ReplaceInner`). Sole production caller `coding/session.go:1099`. |
| `Runner.Runtime` | Upstream keeps `runtime` private (`runner.ts:358`) | SDK-surface read-only accessor. Caller `internal/codingagent/reload_resources.go:154` hands the subprocess host's registrations to the replacement runner on `/reload`. |

DIVERGENCES.md header: D59 was retired by the 0.99.1 port (it sits in the Retired list) but the 0.3.1 merge kept the release's "32". Active `## DN` sections are 31, so the header says 31. No section was added or removed.

I did not create a divergence for any of these (no member is a Pi-observable behavior difference). The owner may still want a second look at `Runtime` and `RefreshTools`; see the QUESTION.

## A. upstream-sync v0.99.2 rows

27 of 29 rows written. Dispositions: all `ported` with test evidence cited by file and test name; each rationale names the changed behavior and the test that asserts it. The checker's disposition set is ported, unchanged-observable, designed-out, deferred, so no row needed `deferred`: for every changed behavior in the 29 files the Go port exists and a test asserts it. The MCP rows are `ported` because `coding/mcpext` implements each delta item and a named test asserts it (verified by running them; mutation-checks for overflow.ts and mergeModels below).

New test: `TestMergeRemoteCatalogModelsKeysByTypeAndIDInFirstSeenOrder` (`internal/codingagent/remote_catalog_provider_test.go`). It was not red-proven because the Go merge already matched 0.99.2. Mutation: restoring the 0.99.1 `findIndex` loop fails it ("chat/a=base-a" kept, repeated baseline entry not collapsed).

Mutation of `ai/overflow.go` (drop the `prompt exceeds max length` pattern) fails `TestOverflowUpstream` at line 48.

Premise correction (remote-catalog-provider.ts): the task said PiG has no remote catalog refresh. It does: `internal/codingagent/remote_catalog_provider.go` (`WithRemoteCatalog`) is wired by `remote_catalog_registry.go` and tested by `TestRemoteCatalogProviderUpstream`; only the PORT_MAP row text ("not ported" in pi-user-agent.ts / management-http.ts rows, virtual-models.ts "router not ported") is stale. I marked the delta `ported`. PORT_MAP rows are not edited by this lane.

### Rows left pending (owner-gated, not marked approved)

Both rows are still `pending`, so `make upstream-delta` stays red on exactly these two until the owner answers.

1. `packages/coding-agent/src/config.ts` (`resolveCodemodeWorkerSpecifier`, `getCodemodeWorkerSpecifier`; 0.99.2 #10204).
   Proposed: `designed-out`. Rationale: the function selects the worker entry of the codemode sandbox for a Bun compiled executable, the bundled Node module or an unbundled package (Windows Bun cannot map an absolute `B:\~BUN` URL back to an embedded entrypoint). PiG's sandbox runs QuickJS on wazero in process (`codemode/sandbox.go`) and has no worker file or specifier, so the Windows standalone failure cannot occur. The vendored Node runtime's own worker is covered by `coding/extension/host/subprocess/runtime_node_sdk_vendor_test.go`. Same decision already stands in test-mapping-v0.99.2.json for `codemode-worker-config.test.ts` and `sandbox.test.ts` (the two worker cases) and awaits owner approval there.
2. `packages/coding-agent/src/extensions/codemode/worker.ts` (comment-only change: `getCodemodeWorkerUrl()` renamed `getCodemodeWorkerSpecifier()` in the doc comment).
   Proposed: `designed-out` with the same rationale (PORT_MAP already lists the file as n/a: the sandbox runs in process, there is no worker entry); the change has no observable effect.

## Gates

See the READY commit message for commands and counts.

## Generated files

None of my edits changes an exported Go API, a CLI flag, a setting or a scenario. `docs/parity/DIVERGENCES.md` is mirrored by hand in `internal/pigdocs/content/divergences.md` as a table without the count, and `make docs-drift` passes. The lead regenerates centrally; no `chore(port-99): regenerate generated files` commit was needed.

## Stubs other families must fill

None.
