# Merge check against rev-fix-99-j5-ext a76d999d3 (scratch branch, not pushed)

`git merge a76d999d3` into this branch conflicts in one place, `coding/session_loadout.go`: this branch exports `ToolActions()` (the wiring fix); j5 keeps `toolActions()` and adds `AppendEntry: s.appendExtensionEntry` plus the `appendExtensionEntry` method. Resolution (build and vet of `coding`, `cmd/pig` and `internal/codingagent` pass):

```go
// ToolActions is the ExecuteTool, GetCallableTools and AppendEntry of the extension context actions. ...
// upstream: agent-session.ts:3382-3383, 3339 (getSettings), 3321-3327 (appendEntry)
func (s *Session) ToolActions() extension.ToolActions {
	return extension.ToolActions{ExecuteTool: s.executeNestedToolCall, GetCallableTools: s.getCallableTools, AppendEntry: s.appendExtensionEntry}
}

func (s *Session) appendExtensionEntry(customType string, data any) error { ... } // j5's body unchanged
```

`make parity-family FAMILY=extensions-runtime` on the merge: 22 scenarios fail; the same set as this branch (20) plus `65-dynamic-tools` and `66-session-replacement-lifecycle`, both failing at the base too and flaky here. j5 fixes none of them.

| scenarios | cause | owner |
|---|---|---|
| 17, 22, 24, 25, 27, 29, 30, 33, 34-empty-custom-footer, 35, 35c, 53-node-scrollview-footer, 53-extension-header-spacers, 65-session-before-dialogs | escaped terminal output differs (colors, layout) | TUI/theme families (9a and the render lanes), not the Node runtime |
| 26, 28, 34-extension-process-identity, 55-package-manifest-startup | command listing and process identity differ from the oracle (`.pig` versus `.pi` in `sourceInfo` paths, package identity) | startup/config-directory naming, not this lane |
| 52-interleaved-node-admission | the packed Python cell needs Python; mise shims fail under this host's temporary HOME | environment |
| 65-dynamic-tools | the scratch worktree has no `.upstream/current` example file | environment |
| 66-session-replacement-lifecycle | "did not reach ready", passes on some runs and fails at the base | flaky, extension-host lifecycle |
| 56-node-lazy-imports | the Pi oracle's own output lacks the expected highlight escape (`pi output missing`) | oracle side |
| 36-model-registry-session-manager | failed one run, passed the next | flaky |
