# fix-992-prompt-options: one run-prompt builder for every mode (audit-992-coding F2 + F7)

Upstream: Pi 0.99.2 (`.upstream/v0.99.2`; `.upstream/current` is a 0.87.1 mirror and was not used). Source: `docs/plan/progress/audit-992-coding.md` on `audit-992-coding`.

## Root cause

Two prompt builders. The Session (print, JSON, RPC) rebuilt the tool and rule sections from its registry and kept only `run.Sections` of the before_agent_start result, so edits to `toolSnippets`, `toolGuidelines`, `appendSystemPrompt` and the other options never reached the request (F2). Interactive mode rendered the run prompt from the edited options but never consulted the Session's hidden declarations (F7). Pi has one path: `_preparePromptAndToolLoadout` (agent-session.ts:1669-1683, called at 1747 and 707) builds the sections from the run's options, after removing hidden tools' snippets.

## Fix

- `internal/codingagent/before_agent_start_run.go`: `BeforeAgentStartRun.BaseSections` / `PromptSections` (the one builder), `WithoutHiddenSnippets`, `NextTurnOptions` (agent-session.ts:697-706).
- Session (`coding/session_transcript.go`, `session_preflight.go`): keeps the resolved run (`runPrompt`), builds the effective sections from its options and the live tools, refreshes the run's options before each later turn, exposes `HiddenDeclarations()`.
- Interactive (`interactive_run_prompt.go`, `interactive.go`): `renderRunPrompt` and the base prompt use the same builder and the Session's hidden set.
- In-process runner (`runner.go`): returns the shared options whenever any field was edited (runner.ts:1462), not only sections/selectedTools.
- Subprocess wire: the response carries the handler's whole options object as `_pigPromptOptions`; the host applies every field except sections and selectedTools (`prompt_options.go`). All four SDKs (Node `runtime.mjs`, Go, Python, Rust) send it and present every option collection (`toolSnippets`, `toolGuidelines`, `promptGuidelines`, `contextFiles`, `skills`) as present, as Pi's normalized options do.
- `docs/extension-api-parity.md` row `before_agent_start` and the async-contract paragraph: the "other structured option mutations ... remain unresolved" clause is gone.

## Red run (commit 982f352c3)

- `go test ./coding -run TestAuditBeforeAgentStartToolSnippetEditReachesTheRunPrompt`: FAIL, prompt lists `- read: Read file contents`.
- `go test ./cmd/pig -run TestAuditCodemodeOnlyPromptOmitsHiddenToolsInEveryMode`: `interactive_tools_flag` FAIL (prompt lists hidden `read`, `bash`); `print_defaultTools` FAIL (declares `[read bash edit write]`, F6, see below); `print_tools_flag` passes (control).

## Green

- Both coding and interactive subtests pass. `print_defaultTools` stays red: it is F6 (`defaultTools` activating extension tools at startup, owner `fix-992-default-tools-reload`), not this lane's code. Its failure message is unchanged by this lane.
- New regression tests (not red-proven before the fix, except where noted): `coding.TestBeforeAgentStartOptionEditsReachEveryTurnOfTheRunPrompt`, `coding.TestRunPromptRefreshesBaseSnippetsBeforeLaterTurns`, `icodingagent.TestBeforeAgentStartRunPromptSectionsOmitHiddenSnippets`, `icodingagent.TestBeforeAgentStartRunNextTurnOptionsMergeBaseUnderRunEdits`, `extensionconformance.TestPromptOptionEditsAcrossSDKs` (native, fused Go, isolated/packed Go/Node/Python/Rust; the Node row failed with a null result before the runtime change).
- Mutations: replacing the run-built sections with the base sections fails the audit test and the every-turn test; dropping `NextTurnOptions` in `prepareNextTurn` fails `TestRunPromptRefreshesBaseSnippetsBeforeLaterTurns`; the hidden filter is guarded by the interactive binary test and the builder unit test.
- Load: `-race -count=24` on the prompt tests (coding, internal/codingagent), then the same with `GOMAXPROCS=4 taskset -c 0-3` against 4 CPU burners (`timeout 150 sh -c 'while :; do :; done'`, self-expiring): pass.

## Gates

`go build ./...`, `go vet` (+ `GOOS=windows` for coding, subprocess, internal/codingagent, extensions/sdk), gofmt, `go fix -diff`, golangci-lint over the touched packages (0 issues), `check-public-claims.py`: clean. `go test ./coding ./coding/extension/host/inproc ./test/extension-conformance` (the three prompt conformance tests): pass.

Not clean here, identical on the red base commit (checked by running the failing names in a base worktree): the pinned-Pi comparison tests and `make parity-family FAMILY=extensions-runtime` (builtin command order `llama`/`mcp`, `0.87.1` identity pin, missing `node_modules` for the pinned Pi). `make generate` stops at `known-gaps` with an `images-models.test.ts` release-policy mismatch owned by another lane.

## Deferred / for other lanes

- F6 `print_defaultTools`: `fix-992-default-tools-reload`.
- Interactive mode keeps its own base-options object (`baseSystemPromptOptions` in `InteractiveMode`) and prompt text on the agent; only the run-prompt build and the hidden filter converge. Moving interactive onto `Session.PreparePrompt` is a larger change to its turn flow and is not needed for the contract above.
- Non-default structured Session prompts (caller-supplied sections) take only the `tools` and `rules` sections from the run's options; `appendSystemPrompt`, `customPrompt` edits do not apply to a caller-owned prompt (PiG-only concept, no Pi counterpart).
- Rust whole-options reassignment and live getters during a handler stay as the parity row records them (not re-probed).
- No stubs for other families.
