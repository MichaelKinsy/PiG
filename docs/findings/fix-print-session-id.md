# Session identity in headless modes (#83)

## Finding

The issue's reported revision, `f5c98329d959932142ecfe2a819ab589c4ecb865`, creates a valid Session but leaves print and JSON subprocess session-read callbacks unbound. Node's `ctx.sessionManager.getSessionId()` consequently returns an empty string. RPC and interactive already bind the callback. This is not a UUID-generation or persistence failure.

Stable tip 4 (`636bab50a9872bd92ec0d2dd4e62bf8669eec62e`) already includes the root host-binding fix from `828ce33067a78c3f871cfbc9fdc0c6605bea763a`. It calls `bindSessionReadActions` before print/JSON `session_start`. This change preserves that implementation and adds regression coverage instead of generating a second identity.

The accessor audit finds a second bug on tip 4. Go, Python, and Rust context convenience methods decode `id` and `path`, but the host returns `sessionId`, `sessionFile`, and `leafId`. Their SessionManager facades and Node's replicated accessors already work. The convenience methods now decode the actual host fields. Go and Python also stop returning a cached file path that survives replacement with an in-memory Session. No wire shape or public method signature changes.

## Upstream contract

Pi 0.87.1 is the oracle:

- `packages/coding-agent/src/core/session-manager.ts:264-266` uses the shared UUIDv7 generator.
- `session-manager.ts:1057-1087` creates the ID and header before selecting the optional persistence path.
- `session-manager.ts:1752-1755,1801-1802` uses the same constructor for `create` and `inMemory`.
- `packages/coding-agent/src/main.ts:363-447` selects the manager independently of mode; `--no-session` selects `inMemory`.
- `packages/coding-agent/src/modes/print-mode.ts:76-104,129-140` binds extensions before prompting and uses the same Session in print and JSON modes.

Identity creation and reads are synchronous. Native SDK convenience reads await the existing host call on the extension worker. Node reads the existing pre-dispatch state. There is no new task, timer, buffer, persistence operation, or TUI-loop work. Session/Host shutdown retains its existing ownership. Provider credentials, API kinds, and poisoned transcript conversion do not participate in this identity lookup; the CLI probes use only the hermetic faux provider.

## Reproduction

The public GitHub issue was read through its HTML endpoint because the requested `gh` profile lacks credentials and the unauthenticated API returns HTTP 403. Nothing was posted.

Build both historical commits from disposable `git archive` snapshots. The v0.2.0 annotated tag resolves to `c318b7716369e00310092c987a9243c9a8d4a1a3`. Each probe uses a fresh HOME, working directory, and agent/session directories. A minimal extension records the actual ID before and after a faux-provider turn. Two independent runs per mode give these results:

| Mode | Pi 0.87.1 | Stable tip 4 | v0.2.0 |
|---|---|---|---|
| `-p` | Fresh UUIDv7; stable through shutdown | Same | Extension ID empty; internal header has UUIDv7 |
| `-p --no-session` | Fresh UUIDv7; no Session file | Same | Extension ID empty; no Session file |
| `--mode json`, persisted and in-memory | Fresh UUIDv7; header ID matches extension ID | Same | Extension ID empty despite valid emitted header |
| `--mode rpc`, persisted and in-memory | Fresh UUIDv7 at startup | Same | Fresh UUIDv7 at startup |

UUIDs use canonical lower-case `8-4-4-4-12` spelling, version nibble `7`, and RFC 4122 variant bits. Every new run has a distinct ID. Persistent print/JSON runs write their selected file after the assistant response. In-memory runs write no Session file. An unprompted persisted RPC Session has a selected path but does not yet create the file. The CLI regression additionally completes a tool turn in RPC and checks the persisted header.

## Pinned pi-warden replay

The npm compatibility corpus now includes `pi-warden@0.73.0`, its complete dependency lock, and npm integrity. The ordinary corpus runner checks package loading and the read-only `/warden loops` command. This row declares its command and empty expected stdout because warden's consent notice is outside the faux provider's scripted model inputs; no comparator is weakened. The dedicated replay calls the package's unchanged `warden_loops` tool definition from a print command, with a real extension context and no live model. It compares complete tool results and stdout/stderr without normalization:

```sh
python3 test/parity/corpus/session-id/replay.py --pig "$PIG_BIN" --output "$EVIDENCE/warden"
```

Use the actual Node executable directory on PATH when changing HOME; a user-configured tool-manager shim cannot resolve its installation from a fresh HOME.

On the issue's revision, both persisted and in-memory calls return `not available: this session has no id to keep loops under`. On tip 4 and Pi, both return `added #1: trial check`, followed by `Open loops:\n- #1 trial check`. The empty-host-ID mutation reproduces the exact package failure. The package cannot load on v0.2.0 because that release lacks the unrelated `parseFrontmatter` export; the minimal accessor probe, not package loading, establishes the v0.2.0 identity defect.

## Regression evidence

- `TestSessionReadBindingsKeepIdentityWithoutPersistence` checks fresh UUIDv7/header identity, both native host-call spellings, the Node snapshot, and live Session replacement through the shared binding.
- `TestHeadlessExtensionSessionIdentity` drives the actual print, JSON, and RPC CLI with a Node tool, two runs per persistence choice, stable event-time IDs, saved header IDs, and no files for `--no-session`.
- `print/05-print-extension-session-id` compares three real Pi/PiG pairs. It uses exact stdout and artifact equality. The fixture validates raw UUID spelling, equality with the header, and stability before projecting random identity into booleans. The artifact retains event order and persistence/file-presence values. Returning an empty string from the shared `getSessionID` callback compiles, fails both Go guards, and fails the scenario.
- `TestContextSessionIdentityAcrossSDKs` is red before the SDK fix: seven native-SDK realizations return empty IDs/leaves; Rust also returns empty file paths, while Go/Python retain a stale path after an in-memory replacement. It passes after the fix for the native reference, Go/Node/Rust/Python isolated, Go/Node/Rust/Python packed, and fused Go.
- `TestRegistrySessionFacadesAcrossSDKs` and `TestConformance_TransportsMatch` pass alongside the new row. This distinguishes the existing working facade from the broken convenience accessors.

The original print-mode scenario and CLI guards pass on tip 4 before this change; they are mutation-proven guards for an already-integrated fix, not evidence of a new host fix. The SDK regression is genuinely red before the production edits. An initial SDK test run failed because a fresh HOME hid the installed `uv` tool from its shim; resolving the installed executable on PATH fixes the test environment without changing or skipping coverage.

`BenchmarkRuntimeNewSession` retains CPU/allocation profiles and three samples: 0.71–0.87 ms/op, approximately 181–185 KB/op and 2,610–2,664 allocations/op on Linux amd64, Go 1.27.1, Intel Xeon 6746E. This is a baseline, not a performance-improvement claim. The benchmark joins its event consumer before the next Session. The changed accessors allocate only their existing small host-call payloads; they do not scan or retain Session history.

## Qualification

Passing checks:

- `make generate` and `make normalization-inventory`.
- `make set-version VERSION=0.3.0` to refresh the three local SDK checksums without changing the release version.
- `go test ./cmd/pig ./test/extension-conformance -count=1`, plus race-enabled runs of the three new regression tests.
- `go test ./...` in `extensions/sdk`, and `cargo test --quiet` in `extensions/sdk-rs`.
- `make parity-family FAMILY=print` with the corrected installed tmux on PATH.
- Three-pair cross-family probes: `03-json-extension-no-ui`, `16-resumed-session-identity`, `18-rpc-session-replacement-cancel`, `28-rpc-extension-has-ui`, and `36-model-registry-session-manager`.
- The pinned pi-warden load/command corpus row and dedicated add/list replay.
- `go vet ./...`, plus Windows-targeted vet for the touched Go packages.
- `go tool golangci-lint config verify`, `make lint-changed`, and `make lint`.
- Scenario lint, port-map drift, coverage drift, divergence consistency/guard, source hygiene, docs drift, format-version inventory, factory-ledger drift, and SDK-surface drift.

Whole-repository gates remain blocked by baseline obligations, not accepted skips:

- `go test ./...` fails four `test/parity/closure` tests. An untouched tip-4 snapshot reproduces the same failures: provisional-count/D78 dashboard expectations and a provider-wire closure record exceeding 64 MiB. All other packages pass after regenerating the SDK sums.
- `make ci-contracts` stops at `test-porting-release` on existing partial/pending hot-path upstream test ports.
- `make ci-drift` stops at D78's missing `SCRUTINIZED:approved`. This slice neither grants approval nor changes the divergence.
- `go fix -diff ./...` reports existing modernization suggestions in `telemetry` and `agent/harness/runtime`; it proposes no change to this slice's files.
- The standalone Python test suite requires pytest >=9.1.1, which is not installed in the selected Python environment. No tool was installed without approval. Python's real isolated and packed host/SDK conformance passes; that does not claim the standalone pytest suite passed.

The unprompted RPC probe also observes the existing EOF shutdown-event difference: Pi records `session_shutdown`, while PiG does not deliver it to the subprocess extension. The identity remains correct. This observation is handed to the lead separately; this slice does not claim RPC EOF lifecycle closure.
