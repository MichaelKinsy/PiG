# fix-99-j5-rpc: joint run 5 and parity-fast rpc failures

Base `113a10ba9` on `porter/pi-0.99.1`. Oracle for the parity runs: upstream 0.99.1 installed into a scratch directory with `npm install --min-release-age=0` (the mise install holds 0.87.1 only), passed as `PIG_PARITY_PI_BIN`. `extensions/sdk-ts` was installed with `npm ci --min-release-age=0`, which lets the Pi legs of the `cmd/pig` comparison tests run.

## Item 1: four `TestRPCInputEnd*ComparedWithPi` tests

The joint run described the failure as a missing replacement notify. That was a misreading: the notify was present in every failing run. The tests expected the 0.87.1 prompt response `{"type":"response","id":"c","command":"prompt","success":true}`, and 0.99.1 answers `data:{"disposition":"handled"}` (`.upstream/v0.99.1/packages/coding-agent/src/modes/rpc/rpc-mode.ts:403-407`; a consumed extension command reports "handled" at `.../core/agent-session.ts:1893-1897`). With the real upstream 0.99.1 as the oracle, the Pi legs produce that response too, so the wants were a mis-port from 0.87.1. Fix: the wants use `handledPromptResponse`, which the sibling tests in the package already use (`rpc_shutdown_drain_go_test.go`, `rpc_shutdown_newsession_test.go`, `rpc_shutdown_quarantine_test.go`). No production change.

Evidence: all `TestRPCInputEnd*` tests pass with upstream 0.99.1 installed (156 passing subtests, both legs); the quarantine and newSession tests pass under `-race -count=4`, `GOMAXPROCS=4`, `taskset -c 0-3` with four CPU burners.

## Item 2: parity rpc 41 and 26 (`/15/result/structuredContent`)

Cause: `cmd/pig/rpc_events.go` wrote `tool_execution_end.result` with content and details only. Pi emits the finalized AgentToolResult (`packages/agent/src/agent-loop.ts:912-919`), whose `structuredContent` (`packages/agent/src/types.ts:433`) is the bash `BashToolOutput` (`packages/coding-agent/src/core/tools/bash.ts:395-406`). The Go bash tool already returned it (`internal/codingagent/tools/shell_tool.go`); the wire dropped it. Fix: append `structuredContent` after `details` when present. The same serializer writes the JSON mode, so print/json get it too.

Red: `TestRPCToolExecutionEndCarriesStructuredContent`, `TestRPCBashToolExecutionEndCarriesItsStructuredOutput` (`cmd/pig/rpc_tool_structured_test.go`). Green: the same tests plus scenarios `rpc/26` and `rpc/41` against upstream 0.99.1.

Not done (same class, not in the failing scenarios): Pi's `result` also carries `isError`, `usage` and `terminate` when the tool set them. Go's `AgentToolResult.IsError` cannot tell a returned `isError:true` (in the result) from a thrown error (only beside the result), and `TestRPCToolExecutionResultExactWire` pins the current no-injection behavior, so it is left alone.

## Item 3: parity rpc 17 (`/7/message/thinkingLevel`)

Cause: Pi's test-faux twin (`test/parity/testdata/test-faux-provider.ts:527-538`) sets `stopReason:"error"` and `errorMessage` on its output and pushes `start` and `error`. Pi's loop copies the start partial into `message_start` (`packages/agent/src/agent-loop.ts:416-421`) and adds `thinkingLevel` to the final result only (`:409`). Go's `ai/test_faux.go` pushed only the error, so the agent took its "no start event" path (`agent/agent.go` consumeStream `!started`) and built `message_start` from the final message, which carries `thinkingLevel`. The production path already matches Pi; the Go fixture did not match its twin. Fix: the retry branch sets the error state, calls `builder.start()`, then `fail`.

Red: `TestTestFauxRetryableErrorStartsWithTheErrorState` (`ai/test_faux_retry_test.go`). Green: same test and scenario `rpc/17` against upstream 0.99.1.

## Runs

- Red commits: `8b30f6a28` (ai), `19dc37530` (rpc structuredContent). Test fix: `9a20851b0`. Green: see the git log.
- `go build ./...`, `go vet` and `GOOS=windows go vet` on `./ai ./cmd/pig`, gofmt, `go fix -diff`, golangci-lint on `./ai/... ./cmd/pig/...`: clean.
- `go test -race`: `./ai`, `./agent/...` pass. `./coding` and `./cmd/pig` fail only in tests that belong to other lanes (below).
- `make parity-family` equivalents with upstream 0.99.1 as oracle: `rpc` (47), `json`, `print` pass. `providers-faux-streaming`: 4 scenarios fail (`09-orphaned-tool-result-wire`, `10-responses-tool-identity`, `23-metadata-refresh-and-native-result`: the Pi side fails to load its modules from the scratch install layout; `19-http-proxy-connect`: Pi issues two CONNECTs for openai-completions, Go one; none touches this change).
- `-race -count=24`, `GOMAXPROCS=4`, `taskset -c 0-3` with four CPU burners: the new tests in `./ai` and `./cmd/pig`.

## Failures seen that belong to other lanes

`coding`: `TestUpstreamAgentSessionCodemodeTool`, `TestUpstreamCodemodeOptionsAndStore`, `TestUpstreamCodemodeModels` (classifier and codemode), `TestReplacedSession2860New` (f8-spec `SessionManager` binding). `cmd/pig`: `TestRPCPlanModeEmptyToolsSurviveProcessResume`, `TestRPCFreshPromptDeclaresStructuredSystemOnce`, `TestSystemPromptOptionsThroughHeadlessStartup` (codemode and tool_search enabled by default), `TestLinkedPackagesCompileNoRegexpAtInit` (`tool_codemode_renderer.go:34`, `toolsearch.go:53-56`).

## Stubs other families must fill

None.
