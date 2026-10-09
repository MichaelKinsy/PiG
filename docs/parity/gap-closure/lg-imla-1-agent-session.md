# lg-imla-1: coding-agent agent-session / session options / session manager (59 open rows, integrate-042 detector)

Probed against Pi 1.0.4 `core/{sdk.ts,agent-session.ts,agent-session-runtime.ts,session-manager.ts}` and the Go `coding/{session.go,services.go,runtime_replacement.go}`. Per LEAD-RULINGS-1520 unblock-types 1: reviewed placement onto `coding.SessionOptions` + `AgentSessionServices`; port only members Pi behaviour reads and a production path can reach.

## Placement (rule requests for lg-rules; no Go change closes these)
| Pi member(s) | Go placement that already carries the behaviour |
|---|---|
| `AgentSessionConfig`/`CreateAgentSessionOptions` `cwd`, `agentDir`, `settingsManager`, `modelRuntime` (11 rows) | `AgentSessionServices` (`CWD()`, `AgentDir()`, `SettingsManager()`, `ModelRuntime()`), built by `CreateAgentSessionServices` (services.go:147) and passed as `NewSession(svcs, opts)` (session.go:329). Rule: a member is satisfied when `SessionOptions` or `AgentSessionServices` carries it (two Go types realise Pi's one options object). |
| `allowedToolNames`, `excludedToolNames`, `initialActiveToolNames`, `excludeTools` | `SessionOptions.AllowedTools`, `.ExcludedTools`, `.ActiveBuiltinTools` (session.go:245,263 comments cite sdk.ts:246, agent-session.ts:2288). Rename rows. |
| `sessionStartEvent` | `CreateAgentSessionRuntimeOptions.SessionStartEvent` (runtime_replacement.go:27), applied by `applyRuntime`; `Session.sessionStartEvent` is set at startup. Rename row. |
| `CreateAgentSessionResult` (`session`, `extensionsResult`, `modelFallbackMessage`) | `CreateAgentSessionRuntimeResult` (runtime_replacement.go); `extensionsResult` is the load result the mode keeps. Placement; `extensionsResult` needs the rule too. |
| `AgentSession.reload`, `AgentSessionRuntime.construct` | Go splits Pi's reload by mode: `Runtime.SetReload(func(ctx, *Session) error)` (runtime_replacement.go:133) plus `Session.ReloadExtensions` and `RefreshToolsAfterReload` (session_extension_hooks.go:507, session_tool_registry.go:399). Placement. |
| `AgentSession.state` | `agent.Agent` state is read through `Session.Agent()`-style accessors; placement. |
| `createAgentSession`, `createAgentSessionFromServices`, `AgentSession.construct` | `CreateAgentSessionServices` + `NewSession` (S4 two functions for one). Placement. |
| `SessionManager._persist`, `appendMessage` | private in Pi (`_persist`: designed-out, Go has no private-member API); `appendMessage` -> `Session.AppendMessage` (internal/codingagent/session.go), reviewed-gap in shard 4. |

## Missing members with no production caller (not ported: caller-free, no stubs)
`cacheWarmer` (injection point; Go builds its warmer in `installCacheWarmer`, session.go:606), `baseToolsOverride` (Go's `Tools`/`SkipBuiltinTools` override base tools), `extensionRunnerRef` (Go's `Runner`), `agent` (external-agent constructor: ruled out by unblock-types 1), `usesDefaultTools` (derived from `ActiveBuiltinTools == nil` in Go). Pi's only caller of each is `createAgentSession` in sdk.ts, which Go's `NewSession` replaces.

## Representation rows
`tools` (`string[]` vs `[]agent.AgentTool`): Go `Tools` carries tool objects; Pi's name allowlist is `AllowedTools`. `extensionFlagValues` record -> `[]ExtensionFlagValue` (T6 ordered slice). `bindExtensions` parameter in a variadic tail (S3). `sendCustomMessage.message` boolean vs `any` (unvalidated JS value). `SwitchSessionOptions.cwdOverride` and `CreateAgentSessionRuntimeOptions.projectTrustContext`: real members; the former is `SessionOptions.CWDOverride`.
