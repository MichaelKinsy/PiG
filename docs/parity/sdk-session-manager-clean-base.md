# SDK SessionManager clean-base evidence

This dependency implements construction and identity from the four original `packages/coding-agent/test/sdk-session-manager.test.ts` cases. It does not close the complete SDK or SessionManager source files.

## Contract

`coding.SessionManager` aliases the real append-only log. `ServicesOptions.SessionManager` supplies the CWD default only when explicit CWD is absent. `SessionOptions.SessionManager` and `SessionStartOptions.SessionManager` select the actual log without copying it. Its persistence mode and configured storage directory survive wrapping, opening and branching. A new persisted Session uses the selected Services agent directory rather than another ambient configuration root.

The constructor retains the stable scoped-model, prompt, tool registry and branch-based `BuildSessionContext` representations. Supplied explicit models bypass saved-model selection. Explicit thinking wins over configured or restored preferences. Missing initial thinking metadata is appended to supplied history. The stable direct-tool context implementation is unchanged.

## Tests and oracle

`coding/sdk_session_manager_upstream_test.go` preserves the four original inputs and assertions: selected agent-directory storage, exact memory-manager identity, derived CWD with real `bash pwd`, and current Session identity/model/thinking in the built-in bash environment. The tests reuse helpers in `coding/session_test.go`. They use isolated homes and directories, no network and no sleep-based synchronization.

`coding/sdk_session_manager_boundaries_test.go` covers two-way log visibility, explicit CWD precedence, configured directory versus opened file path, Runtime option forwarding, poisoned history, explicit model/thinking and missing metadata. `coding/session_context_settings_upstream_test.go` retains its original selected-branch/assistant-model assertions and adds explicit-versus-restored model selection.

Session22 executes the original four TypeScript test bodies against published Pi0.87.1 and compares exact observation records. Supplemental construction tests remain native-only and do not inflate the scenario's coverage. Session24/25 re-probe the factory Runtime and persistence callers. All three scenarios pass their declared three-pair durability on the adapted source.

Eight compiling mutations fail the native guards. Three also fail the Session22 pair: ambient storage-root substitution, ignored supplied-manager identity and ignored manager-derived CWD. Five are native-only: dropped Runtime manager forwarding, ignored explicit model-selection flag, missing thinking metadata, lost opened-directory override and full-entry restoration instead of selected-branch restoration.

An initial caller-side saved-model-lookup mutation was not discriminating: the stable model lookup does not execute the configured credential command, and the constructor separately retains the explicit model. That attempt is not counted as proof. The helper's explicit-selection guard is independently mutation-proven; the unchanged constructor guard still checks that credential work does not occur and poisoned history remains intact.

## Integration checks

The first Runtime restoration test failed because its generic scripted reply incorrectly labelled a requested `faux-2` reply as `faux-1`. The stable branch projection correctly restores the last assistant's model. The fixture now tags the reply with the requested model, like upstream's faux provider. The expected restored model stays `faux-2`; no production restoration rule or test assertion is weakened.

The shared `Session.BindExtensions` retains stable dynamic tool admission after `session_start` and accepts optional mode bindings. It does not replace the tool-registry method with a second binder. The existing live UI/mode getter representation and scoped model identity remain intact.

Reproduce in an isolated environment:

```sh
go test ./coding -run 'TestUpstreamSDKSessionManager|TestSDKManager|TestSDKExplicitThinking|TestSDKInjectedHistory|TestRuntimeKeepsSuppliedSessionManager|TestSessionManagerCWDDefault|TestRestoreSessionRuntimeState' -count=1
go test ./coding ./internal/codingagent
node --experimental-import-meta-resolve test/parity/testdata/sdk-session-manager-pi.mjs
```

Source intake does not import rejected author ancestry. Additional Runtime callback, mode factory and extension-lifetime work remains separately incomplete.
