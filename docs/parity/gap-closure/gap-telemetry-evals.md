# gap-telemetry-evals: Pi 1.0.4 `packages/telemetry` and `packages/evals`

Base `12429f15b`, upstream mirror `.upstream/v1.0.4`. `docs/parity/PORT_MAP.md` lists only `packages/{agent,ai,coding-agent,tui}/src`, so the headline porting figure does not count these two packages. This ledger applies the PORT_MAP file rule to them: one row per tracked upstream source file, status `✅`/`🟡`/`⬜`/`n/a`, and percentage = `✅ / (✅ + 🟡 + ⬜)` with `n/a` (designed out) excluded. Integration adds the PORT_MAP rows; this lane does not edit PORT_MAP.md.

Delta from the 1.0.0 port: `telemetry` and `evals` changed between v1.0.0 and v1.0.4 only in `packages/*/package.json`, `telemetry/CHANGELOG.md` (version headings) and `evals/docker/entrypoint.ts` (the `npm-shrinkwrap.json` presence check was dropped; Go images have no npm install tree, see the `entrypoint.ts` row).

## File map (source files, the PORT_MAP rule)

| Upstream file | Go file(s) | Status before | Evidence |
|---|---|---|---|
| `telemetry/src/index.ts` | `telemetry/context.go`, `telemetry/schema.go` | ✅ | Carriers `context.go:5-45`; schema data, `DefineTelemetrySchema`, `CreateTypedSpanStarter` `schema.go:40-143`. The conditional/mapped types have no Go form (`stubgen:omit` `schema.go:5-25`, mechanics, no divergence). `startSpan<T>` returns `Promise<T>`; a Go interface method cannot be generic, so `StartSpan` returns `error` and callbacks close over results (`TelemetryContext`, `context.go:30`). |
| `telemetry/src/noop.ts` | `telemetry/context.go` | ✅ | `noopTelemetrySpan` and `NoopTelemetryContext` `context.go:41-57`. `Object.freeze` has no Go form; the span is a stateless value. |
| `telemetry/src/memory.ts` | `telemetry/memory.go` | ✅ | Recorder `memory.go:25-183`; nil attribute values omitted as `undefined` (`memory.go:42`); settle ordering and `endSequence` `memory.go:96-108`. |
| `telemetry/src/testing/types.ts` | `telemetry/telemetrytest/types.go` | ✅ | `types.go:14-30`. |
| `telemetry/src/testing/conformance.ts` | `telemetry/telemetrytest/conformance.go` | ✅ | 8 of the 9 cases of `conformance.ts:60-315` have the same group and name (`conformance.go:119-371`). The other 3 (`ignores failed attribute calls atomically`, `suppresses unreadable telemetry payload failures`, `ignores failed status calls atomically`) build a `Proxy` whose traps throw. A Go `SpanOptions`/`SpanAttributes` value cannot fail on read, but a payload whose `String`/`MarshalJSON` methods panic can: the first two are now ported in that Go form (`telemetrytest/conformance.go`; they assert what survives in Go, and a recorder that marshals attribute values fails them). `ignores failed status calls atomically` has no Go form (a `SpanStatus` is two strings) and stays designed out in the test-mapping rationale. It cannot go in `designedOutCases`: the case is generated in `src/testing/conformance.ts`, so `make test-inventory` rejects its id as not an upstream case of the file.  |
| `telemetry/src/testing/index.ts` | `telemetry/telemetrytest/{conformance,types}.go` | ✅ | Re-export only; `CreateTelemetryAdapterConformance`. |
| `evals/src/plan.ts` | `internal/evals/plan.go` | ✅ | `plan.go:29-110`. |
| `evals/src/report.ts` | `internal/evals/report.go`, `internal/evals/vitest_report.go` | ✅ | `report.go:1-783`; byte-for-byte against upstream on Node v24 through `testdata/oracle.json` (`evals_oracle_test.go`). |
| `evals/src/harness.ts` | `internal/evals/harness.go` | 🟡 | Helpers ported: `ResolveModelSelection`, `ApplyIsolatedEnvironment`, `resolveSandboxIdentity`, `VerifySystemPrompt`, `ExcludePiDocumentation`, `CreatePiDocumentationEvalHarness` (`harness.go:53-240`). Not ported: `createPiCodingAgentHarness`/`runPiCodingAgent`, `enterToolSandbox`, `protectTransformedModules`, `seedWorkspace`, `promptAgent`, `toTranscriptEvents`. |
| `evals/src/cli.ts` | none | ⬜ | `plan.go:6-15` omitted it ("no Go host"). |
| `evals/src/docker.ts` | none | ⬜ | same. |

Combined PORT_MAP-rule figure: before **8 / 11 files ✅ = 72.7%** (telemetry 6/6 = 100%; evals 2/5 = 40%, 1 🟡, 2 ⬜). The headline 76.9% (379/493) would become 387/504 = 76.8% if these 11 rows were added as they stand.

## File map (the remaining tracked files of `packages/evals`, outside the PORT_MAP `src/` rule)

| Upstream file | Go file(s) | Status before | Evidence |
|---|---|---|---|
| `evals/evals/acme-server.ts` | `internal/evals/acme_server.go` | ✅ | `acme_server.go:1-262`; bytes pinned by `TestAcmeServerStreamsUpstreamBytes`. |
| `evals/evals/configured-runtime.ts` | `internal/evals/configured_runtime.go` | ✅ | `configured_runtime.go:1-178`. |
| `evals/evals/smoke.eval.ts`, `models.docs.eval.ts`, `documentation-audit.eval.ts`, `extensions.docs.eval.ts`, `custom-provider.docs.eval.ts`, `openai-provider.docs.eval.ts`, `tui.docs.eval.ts` | none | ⬜ x7 | Vitest-evals suites; they run the agent Session through `createPiCodingAgentHarness`. |
| `evals/docker/entrypoint.ts` | none | ⬜ | container entry point. |
| `evals/docker/install-runtime.mjs`, `docker/Dockerfile`, `docker/Dockerfile.dockerignore` | none | ⬜ x3 | image build. |
| `evals/vitest.evals.config.ts`, `vitest.test.config.ts`, `tsconfig.json`, `.gitignore`, `package.json`, `README.md` | none | n/a | Vitest and TypeScript tooling configuration; Go uses `go test` and the Go toolchain. |

Extended before-figure over all 23 non-config source files of the two packages (telemetry 6, evals `src/` 5, `evals/` 9, `docker/` 3 counted, `install-runtime.mjs` n/a): **10 / 23 = 43.5%** (6 telemetry + plan, report, acme-server, configured-runtime). Telemetry-only: 6/6; evals-only: 4/17. (An earlier draft of this ledger said 10/18; that miscounted the denominator.)

## Test map

All 8 upstream test files are `ported` in `test/parity/interfaces/test-mapping-v1.0.4.json` and in the Go tests below. Every upstream case has a Go subtest named after it with an `// upstream: file:line` marker. No upstream test touches `cli.ts`, `docker.ts` or `runPiCodingAgent`.

| Upstream test | Cases | Go test |
|---|---:|---|
| `telemetry/test/conformance.test.ts` | 9 conformance (8 with Go forms, 1 designed out: failed status calls) + 1 snapshot | `telemetry/conformance_upstream_test.go` |
| `telemetry/test/telemetry.test.ts` | 5 | `telemetry/telemetry_upstream_test.go`. `expectTypeOf` and `@ts-expect-error` assertions check TypeScript inference and have no Go form; `Object.isFrozen` has no Go form. |
| `evals/test/acme-server.test.ts` | 5 x 2 fixtures | `internal/evals/acme_server_upstream_test.go` |
| `evals/test/comparison.test.ts` | 4 | `internal/evals/comparison_upstream_test.go` |
| `evals/test/configured-runtime.test.ts` | 8 | `internal/evals/configured_runtime_upstream_test.go` |
| `evals/test/harness.test.ts` | 10 | `internal/evals/harness_upstream_test.go` |
| `evals/test/plan.test.ts` | 4 | `internal/evals/plan_upstream_test.go` |
| `evals/test/report.test.ts` | 12 | `internal/evals/report_upstream_test.go`, `report_color_test.go`, `evals_oracle_test.go` |

## Go feasibility (re-examined)

The 1.0.0 port recorded "no Go host" for the container runner and agent harness. That no longer holds:

- `cli.ts`/`docker.ts` are argument parsing, a plan, `docker build/run/inspect` subprocesses and file writes. Go ports them directly; the Docker calls go through one injectable command runner so tests do not need Docker.
- `runPiCodingAgent` needs a Session with an isolated agent dir, a transformed system prompt, tools and a session snapshot. `coding.NewServices`, `Runtime.New`, `Session.Send` and the extension `before_agent_start` hook provide each.
- The container's Vitest and `vitest-evals` are third-party npm packages, not Pi sources. The Go equivalent is a Go eval-suite runner that lists cases and writes the Vitest JSON shape `report.go` already reads (`vitest_report.go`).

## After

| Upstream file | Go file(s) | Status after | Evidence |
|---|---|---|---|
| `evals/src/cli.ts` | `internal/evals/cli.go`, `cmd/pig-evals/main.go` (`docs`) | ✅ | `ParseEvalCli` matches upstream on 42 recorded vectors (`testdata/cli_oracle.json`, regenerated by `testdata/cli_oracle.ts` on Node 24); `EvalCli.RunEvalCli` runs the plan, the per-task loop, `protocol.json`, `expected-runs.json`, `observations.jsonl`, `report.json`, `report.txt` and the exit code (`cli_test.go`). |
| `evals/src/docker.ts` | `internal/evals/docker.go` | ✅ | `BuildImages`, `RequireEvalAuthFile`, `DiscoverCases`, `RunTask` and the `docker run` vector pinned in `cli_test.go`; Docker calls go through `Command`. Not carried over: the `--tmpfs /repo/node_modules/.vite-temp` mount (Vite's cache). `RequireEvalAuthFile` reads PiG's agent directory (D2). |
| `evals/src/harness.ts` | `internal/evals/harness.go`, `harness_run.go`, `agent_process.go`, `sandbox_*.go`, `bridge/`, `cmd/pig-eval-extension` | ✅ | `RunPiCodingAgent` follows `runPiCodingAgent` step for step (`harness_run.go`): isolated root/home/agent dir/workspace, credential hand-off, model and auth resolution, the `before_agent_start` transform and custom tools, prompt/reload steps, `promptAgent` checks, diagnostics, `verifySystemPrompt`, output, session snapshot artifact, `AggregateError` cleanup, partial run on failure (`HarnessRunError`). `harness_run_test.go` runs it against a scripted OpenAI-compatible server through the real pig binary: the recorded system prompt is the one the provider received, the transform reaches the provider, a terminating custom tool ends the run, reload continues one Session, provider/abort/output failures, cancellation aborts the agent. 11 compiling mutations killed (2 survivors led to the empty-answer and cancellation cases). The sandbox ran as root in a golang container. |
| `evals/docker/entrypoint.ts` | `internal/evals/entrypoint.go`, `cmd/pig-evals/entrypoint_unix.go` | ✅ | `RunEntrypoint` keeps the sandbox id checks, the binary and documentation image checks, root-only evaluator and the sandbox read probe, the agent dir and artifact ownership, and runs the suites (`entrypoint_test.go`). Run in the built `with_docs` and `without_docs` images: discovery wrote `discovered-tests.json`, a task run wrote `vitest.json` and chowned `/artifacts` to the caller. The 1.0.4 change (no `npm-shrinkwrap.json` check) has no counterpart: the image has no npm tree. |
| `evals/docker/Dockerfile`, `Dockerfile.dockerignore` | `internal/evals/docker/Dockerfile`, `Dockerfile.dockerignore` | ✅ | Builder stage builds `pig`, `pig-eval-extension` and `pig-evals`; `without_docs` and `with_docs` targets; built and run with Docker 29.7.2. |
| `evals/docker/install-runtime.mjs` | none | n/a | It packs the workspace's npm packages and installs them with npm. The Go image has no npm install tree: the Dockerfile builder stage compiles the three binaries. |
| `evals/evals/*.eval.ts` (7 suites) | `internal/evals/evalsuites/*.go` | ✅ | Same suites, case names and projects (`suites_test.go`), prompts say PiG. Judges `StructuredOutputJudge` and `ToolCallJudge` follow vitest-evals 0.15.0 (`judges.go`, `suite_test.go`). The footer oracle of `tui.docs.eval.ts` renders pig in tmux (`tui_test.go` runs it over the real binary). |
| vitest-evals, Vitest runner and configs | `internal/evals/suite.go`, `judges.go` | n/a (third-party) | Not Pi source. `Runner.List` and `Runner.Run` write the `vitest list --json` and JSON-reporter shapes `report.go` reads (`suite_test.go` round-trips them through `readVitestJSONReportFile`). |

Differences from Pi that a user or maintainer can observe, none in Pi's documented behaviour:

- **File suffix.** Documentation eval files end in `.docs.eval.go`, not `.docs.eval.ts`. This is a naming detail of the Go port, not observable behaviour, and no divergence (lead decision).
- **Session host.** Pi drives an in-process `AgentSession`; PiG's extension host, reload and tool loadout live in the pig binary, so the harness drives a pig process in RPC mode. The inline extension and the custom tools of Pi's harness reach that process through `bridge` and the `pig-eval-extension` binary. `reload` restarts the process on the same session file, which re-reads every resource and extension.
- **Extension observations.** Pi's `extensions.docs` output reads `session.resourceLoader.getExtensions()`. PiG reports `extension_error` events and counts a tool as loaded when it returned a successful result.
- **Credentials.** Pi keeps the credential in the model runtime's memory and deletes the runner's `auth.json` before the sandbox drop (`harness.ts:328-329`); PiG deletes it at the same point. An API key stays in the harness: the isolated `auth.json` holds a `!` command that fetches it from the bridge socket, which the agent's file tools cannot open. An OAuth credential has to be in the `auth.json` the pig process reads and refreshes, owned by the sandbox user, so the agent's file tools can read it. Recorded as D98 in `docs/parity/DIVERGENCES.md`, owner sign-off pending (the run's root is removed afterwards and PI_EVAL_* variables are not passed to the agent).
- **Unexpected extensions.** Pi fails a run whose new Session loaded an extension other than its inline transform (`harness.ts:357-363`). PiG makes the same check when the first pig process starts, before any prompt, through the additive `get_extensions` RPC command (D101). It accepts the bridge extension and PiG's stock `builtin:` extensions, which Pi does not have.
- **Image documentation checks.** Pi's entry point checks that the `without_docs` image has no coding-agent README, CHANGELOG, docs or examples and that the `with_docs` image has README.md, CHANGELOG.md, docs/models.md and examples/README.md, non-empty (`entrypoint.ts:48-62`). PiG's agent sees only the documentation it materializes from its binary under its config root, so `assertDocumentation` materializes a fresh root, applies the variant's removal (`removePigDocumentation`, the same function the harness uses in `without_docs` runs) and asserts the result: `without_docs` leaves no README.md, CHANGELOG.md, docs or examples; `with_docs` leaves README.md and models.md plus every embedded page, non-empty. PiG's bundle has no CHANGELOG.md or examples directory (examples are linked on GitHub, D22), so `with_docs` cannot require them. Mutations (removal skips docs, removal skips examples, with_docs requires CHANGELOG.md) fail `entrypoint_test.go`.
- **without_docs.** Pi removes README.md, CHANGELOG.md, docs/ and examples/ from the installed package. PiG materializes its embedded docs into each run's home when pig starts, so the without_docs run removes that directory once startup has finished, in addition to the prompt transform. The binary itself still embeds the docs.
- **Footer oracle.** The built-in status text for the comparison comes from the same render with extensions disabled; Pi reads it from the first render of the extended session. Pi's oracle Session also holds a 717,800-token, $1.008 usage entry and runs at thinking level `high` (`tui.docs.eval.ts:122-159`); PiG's session fixture has neither, so the token, cost and thinking fields of the rendered footer, and with them `retainedPercent`, differ from Pi's.
- **Audit pages.** The audit suite audits `internal/pigdocs/content`, the documentation PiG ships to the agent, as Pi audits `packages/coding-agent/docs`.
- **TUI/judge subset.** Only the judge configurations Pi's suites use exist.

Percentages by the PORT_MAP file rule:

| Scope | Before | After |
|---|---:|---:|
| telemetry `src/` (6 files) | 6/6 = 100% | 6/6 = 100% |
| evals `src/` (5 files) | 2/5 = 40.0% | 5/5 = 100% |
| both packages `src/` (11 files) | 8/11 = 72.7% | 11/11 = 100% |
| all non-config source files (23; `install-runtime.mjs` n/a) | 10/23 = 43.5% | 23/23 = 100% |

If integration adds the 11 `src/` rows as ✅, the PORT_MAP headline moves from 379/493 = 76.9% to 390/504 = 77.4%.

Gates run on this lane: `go vet` (linux, windows, darwin) on the touched packages, `golangci-lint` (0 issues), `go test -race` on `./internal/evals/...` and `./telemetry/...`, `divergence-guard` (50 baselined hits, unchanged), `source-hygiene` (clean). Not run: `make check`, parity scenarios.
