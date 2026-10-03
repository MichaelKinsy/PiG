# fix-992-cmdpig-timing progress

## Finding 1: "RPC dump notification timed out after 1.4s"
Source: `testbudget.Wait(t)` caps its budget at `time.Until(t.Deadline()) - 10s`. The 1.4s was the remaining `go test -timeout 10m` budget when the test started; the package was out of time (finding 2), not a short fixed deadline in `rpc_tool_flags_test.go`. The helper (`rpcProcess.awaitProgress`) already uses the standard bounded helper.
Same pattern found elsewhere: 14 literal >=1s `time.After` / `context.WithTimeout` hang bounds in cmd/pig tests (list in the red run below).

## Red (part 1)
`go test -run TestTestWaitsUseTheTestBudgetNotLiteralDeadlines ./cmd/pig` fails with 14 sites: config_exit_pty, extensions_cache_command, mcp_builtin_binary, model_oauth_refresh, rpc_azure_observation, rpc_bedrock_observation, rpc_observation, rpc_output (2), rpc_settle_gate, rpc_ui, startup_session_name_upstream (2), stdout_queue.

## Baseline (finding 2)
`go test -json -count=1 ./cmd/pig`, taskset 4 cores + 4 burners: 1525.96s PASS with the pinned Pi installed (1269.9s FAIL without Pi: the /pi oracle subtests fail fast). Serial phase (tests without t.Parallel) ~1131s, parallel phase ~395s. Top: Bedrock 240s (2400 pig spawns), RPCInputEndAfterExtensionCommandComparedWithPi 171s, PigletExtensionToolsScope 108s, Azure 102s, StartupMalformedPackageManifests 94s.

## Green (part 1)
The 14 literal hang bounds now use `testbudget.Wait(t)`. Kept literal (allowlisted in the guard): the three `go build` bounds and the 1.5s negative window in interactive_signal_lifetime_test.go. Pacing waits (50ms poll, Mistral/Pi-messages 150ms gaps) are not hang bounds. Guard test passes; touched tests pass (bedrock/azure at 5 runs); lint 0 issues; GOOS=windows vet clean.
