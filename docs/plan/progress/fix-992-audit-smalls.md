# fix-992-audit-smalls: audit-992-codemode-mcp CM4, CM5, CM6 and the WHATWG host note

Base `porter/pi-0.99.1` (299ad70c3), upstream 0.99.2. Source: `docs/plan/progress/audit-992-codemode-mcp.md` on `audit-992-codemode-mcp`.

## CM4: edit diff context colour (one diff renderer)

- Upstream: `.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/components/diff.ts:89,141` draws context rows (and rows that do not parse) with `theme.fg("toolDiffContext", …)`, removed rows with `toolDiffRemoved`, added rows with `toolDiffAdded`.
- Red: `5375482cc` ports `TestAuditEditDiffContextLinesUseTheToolDiffContextColor`. Pre-fix failure: `line 0 = "\x1b[2m 1 before line\x1b[22m", want "\x1b[38;2;157;165;169m 1 before line\x1b[39m"` (also lines 3 and 4).
- Green: `internal/codingagent/tool_render.go` lost its second diff renderer (`renderDiffString` body, `parseDiffLine`, `diffLineRe`). `renderDiffString` is now `tui.NewDiffComponent(diff, "").Render(width)`. `tui.RenderDiff` now uses `Theme.FgText` (closes with `\x1b[39m`, not `\x1b[0m`; no `th.Muted`/`th.Error`/`th.Success` fallbacks that Pi does not have) and Pi's exact `parseDiffLine` pattern `^([-+\s])(\s*\d*)\s(.*)$`. `TestParseDiffLine` moved to `tui/diff_render_test.go` with the same cases.
- Scenario `interactive-rendering/10-edit-tool-diff`: `output_normalized_equal` → `escaped_output_equal`. Pi and PiG match byte for byte with SGR kept (`make parity-family FAMILY=interactive-rendering`, all pairs pass except `40-suspend-resume-session`, which needs a native Pi 0.87.1 binary and fails on this host regardless of this lane). Mutation: drawing context rows with SGR dim fails scenario 10 (`escaped output mismatch`).

## CM5: streamable-http "calls fetch without a receiver"

- The audit's test is moved into `mcp/streamable_http_test.go` as `TestStreamableHTTPTransportRoutesEveryRequestThroughTheFetchInUse`, now with both halves of the upstream case (injected fetch, and the default client via a stubbed `http.DefaultClient.Transport`, the analogue of `vi.stubGlobal("fetch")`).
- Mutations: `UnauthorizedContext.Fetch = http.DefaultClient` fails the first half; `t.fetch = &http.Client{}` for the default fails the second.
- Ledger: `test-mapping-v0.99.2.json` row `packages/mcp/test/streamable-http.test.ts` cites the test and states that only the receiver assertion is designed out. `designedOutCases` is unchanged (owner queue).

## CM6: upstream-delta and async-contract gates

- `test/parity/cmd/upstreamdelta` tracked roots and `test/parity/cmd/asynccheck` tracked packages now include `packages/codemode` and `packages/mcp`. Tests: `TestComputeDelta`, `TestDiscoverAsyncSources`, `TestDiscoverAsyncSourcesRejectsMisleadingMirror` (red in `5375482cc`).
- `upstream-sync/v0.99.2.toml`: five rows (`prelude-source.ts` ported, `streamable-http.ts` ported, `host.ts`, `worker.ts`, `types.ts` designed-out because PiG has no worker).
- `async-contracts.toml`: 16 rows (6 codemode, 10 mcp; two `no-runtime-async` per package). `make upstream-delta` now reports only the two pre-existing `coding-agent` pending rows (`config.ts`, `extensions/codemode/worker.ts`), owned by other lanes.
- Finding, closed (owner answer a: match Pi): `packages/mcp/src/oauth/provider.ts` wedges after one failed `store.save` or an update-time `store.load` (rejected `writes` chain; Node 24 run). Red `TestMcpOAuthProviderKeepsFailingAfterAFailedWrite` (4 subtests); green: `McpOAuthProvider.writeErr` in `mcp/oauth/provider.go`. A cancelled caller context does not poison the provider (store calls take no signal upstream); `TestMcpOAuthProviderIsNotPoisonedByACancelledCall`, mutation-checked. The async row is `translated`.
- `codemode/assets/PROVENANCE.md` nit fixed (0.99.2 pins `quickjs-wasi@3.6.2`).

## WHATWG hosts (reviewer note of rev-audit-992-coding)

- Upstream: `.upstream/v0.99.2/packages/coding-agent/src/core/mcp-servers.ts:85-86` (`isLoopbackRedirectUri`), `:135` (callbackUrl port), `:217-218` (`URL.canParse(value.url)` and `/^https?:$/` on the protocol), `:229-231` (auth host is `new URL(value.url).hostname`).
- Merged the porter tip first (it carries b25633c6e's `urlHostname`); this lane replaces that helper.
- Red `e1c0e8626` (`TestParseHTTPURLMatchesNodeURL`, `TestValidateMcpServerConfigParsesTheURLAsTheWHATWGParserDoes`, expected values from a Node 24 probe). Pre-fix: `http://%6cocalhost/`, `http:example.com` and `http:\\example.com\mcp` were rejected as not http(s) URLs, `http://host:99999/` was accepted, and `http://%6cocalhost:8080/cb` was not a loopback redirect URI.
- Green: `internal/nodeurl/httpurl.go` `ParseHTTPURL` (protocol, hostname, port with default-port elision, search, hash); `coding/extension/mcp_servers.go` validation, the auth-host check, `IsLoopbackRedirectURI` and the callbackUrl port use it.
- Not covered: connecting to a percent-encoded host URL still goes through Go's `url.Parse` in `mcp.NewStreamableHTTPTransport` and `coding/mcpext` (`oauth.go`, `runtime.go`), which needs a full WHATWG href serializer. Validation now accepts what Pi accepts; the transport reports `Invalid URL` for it.

## Environment notes

- `make parity-deps` installed the upstream 0.99.2 comparator in this worktree.
- `internal/codingagent`, `tui` and `coding/extension` tests pass. `coding/extension/host/subprocess` fails `TestNodeThemeAppearanceColorsAndStyle` and `TestNodeVendoredTuiUpstreamTests` on the merged porter tip (a Node theme runtime assertion, `undefined !== {}`); neither touches this lane's files.
- `interactive-rendering/40-suspend-resume-session` needs a native Pi 0.87.1 binary and fails here regardless of this lane.
