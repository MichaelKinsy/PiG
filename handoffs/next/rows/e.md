# Lane E rows (env daemon + evals). Branch fix/follow-e

## (a) upstream files

path | disposition | PiG files | evidence | rationale
---|---|---|---|---
packages/evals/src/report.ts | ported | internal/evals/report.go | test:internal/evals/report_upstream_test.go#TestReportUpstream | 9628c848: classifyCaseStatus stops being exported (Go `classifyCaseStatus`); readTaskObservation filters assertions and cases by the requested fullName, so skipped siblings no longer make the observation errored (test "selects the requested case when a sibling case is skipped"); the classify tests are replaced by the skipped/todo/disabled readTaskObservation cases, as upstream.
packages/env/daemon/src/exec.rs, pty.rs (92c1da1f spawn_error) | designed-out | (none) | n/a | A Go goroutine cannot fail to start: Go has no `thread::spawn` panic path, and the runtime treats thread exhaustion as fatal. The failure `spawn_error` guards (exec reader/waiter and pty reader/writer/waiter threads failing under a task limit) has no Go form; the Go exec/pty already return spawn_error when the child itself cannot start. Upstream has no regression test for it either (PR text: "no automated regression test"). The Rust-only reorder (waiter receives the Child through a channel) carries no behavior beyond that case.
packages/env/daemon/src/main.rs, sys/unix.rs (b6d5c2ec) | ported | env/daemon/server.go (Serve, Options.OnExitSignal), env/daemon/sys_unix.go (notifyExitSignals) | test:env/remote_daemon_signal_test.go#TestConnectionCloseKillsRunningCommands, test:env/remote_daemon_signal_test.go#TestDaemonKillsItsCommandsOnTerminationSignals | Serve takes SIGTERM/SIGINT/SIGHUP through signal.Notify (the Rust sigmask+sigwait thread), kills every command and terminal (killAll) and exits 128+signal. Tests send the real signals to the real daemon process (test binary as `serve`) and assert the command and the daemon die. Red: sha e75044fafc.
packages/env/daemon/src/sys/windows.rs (b6d5c2ec on_exit_signal, a87b742f wording) | ported | env/daemon/sys_windows.go (notifyExitSignals) | test:env/daemon/sys_windows_test.go#TestWindowsDaemonHandlesNoExitSignal (windows only, vet-compiled on linux with GOOS=windows) | no-op with a87b742f's wording (job object ends each command's own process only; descendants break away and keep running).
packages/env/docs/protocol.md (b6d5c2ec, a87b742f) | designed-out | changelog.d/env-daemon-signals-b6d5c2ec.md | n/a | Pi's protocol doc is not shipped; the user-visible statement lives in the changelog fragment.
packages/evals/evals/configured-runtime.ts | ported | internal/evals/configured_runtime.go (unchanged) | test:internal/evals/configured_runtime_upstream_test.go | Pi moved loadConfiguredModelRuntime into its test because its evals read session.modelRuntime. A PiG Session lives in a pig process, so evalsuites (another package) still need the loader: it stays exported as the Go form of `session.modelRuntime`.
packages/evals/evals/documentation-audit.eval.ts | ported | internal/evals/evalsuites/audit.go, internal/evals/suite.go (EvalProjectAudit, AuditEvalFile) | test:internal/evals/evalsuites/suites_test.go#TestSuitesMatchPisEvalInventory | audit asserts the page and an implementation file were read (implementation = repo files outside the audited docs; Pi: under packages/coding-agent/src).
packages/evals/evals/extensions.docs.eval.ts | ported | internal/evals/evalsuites/extensions.go | test:internal/evals/evalsuites/suites_test.go#TestSuitesMatchPisEvalInventory | `toolLoaded` dropped from output and expected.
packages/evals/evals/prompt-templates.docs.eval.ts | ported | internal/evals/evalsuites/prompt_templates.go, internal/evals/harness_run.go (AgentRun.Extensions) | test:internal/evals/evalsuites/prompt_templates_test.go#TestPromptTemplateOutputReportsTheScopeAndTheExpandedPrompt | output loads templates/skills with a DefaultResourceLoader (NoExtensions) over run workspace and agent dir; extensions come from the pig process's get_extensions.
packages/evals/vitest.evals.config.ts | ported | cmd/pig-evals/main.go (`audit` command), internal/evals/suite.go, docs/site/docs/evals.md | test:internal/evals/evalsuites/suites_test.go#TestSuitesMatchPisEvalInventory | host project excludes the audit; audit is its own project (`pig-evals audit`).

## (b) PORT_MAP
No new rows. Rows to update (text only): `packages/env/daemon/src/main.rs` and `sys/unix.rs` rows add "SIGTERM/SIGINT/SIGHUP handling: env/remote_daemon_signal_test.go"; `sys/windows.rs` row stays 🟡; `packages/evals/src/report.ts` stays ✅. Add rows for `packages/evals/evals/prompt-templates.docs.eval.ts` only if the evals/evals files are tracked (they were not in the earlier PORT_MAP scope).

## (c) tests
upstream test | disposition | PiG test
---|---|---
env/test/remote.test.ts "kills running commands when the connection closes" | ported | env/remote_daemon_signal_test.go#TestConnectionCloseKillsRunningCommands
(no upstream test) SIGINT/SIGHUP/SIGTERM sent to the daemon | added | env/remote_daemon_signal_test.go#TestDaemonKillsItsCommandsOnTerminationSignals
evals/test/report.test.ts (changed cases, new sibling case; classifyCaseStatus cases removed) | ported | internal/evals/report_upstream_test.go#TestReportUpstream
evals/test/configured-runtime.test.ts (loader moved into the test) | designed-out | Go keeps the exported loader (see (a)); tests unchanged
evals/test/comparison.test.ts (totalMs, estimatedCostUsd) | ported before | internal/evals/comparison_upstream_test.go already asserts both

## (d) async contracts
env/daemon Serve: new signal goroutine. Contract: first of SIGTERM/SIGINT/SIGHUP -> killAll then OnExitSignal (default exit 128+signal); the goroutine ends when Serve returns (stop channel) and signal delivery is stopped on return. Evidence: env/remote_daemon_signal_test.go.

## (e) interface IDs
none (Options.OnExitSignal is a daemon-package option; internal/evals additions are internal; AgentRun.Extensions and EvalProjectAudit are internal/evals API).
