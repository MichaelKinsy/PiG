# Separating coding/extension from tui

Status: proposal, no code. Context: issue #214. Package `coding/mcpext` was split so that it imports no terminal UI itself, but `go list -deps ./coding/mcpext` still contains `tui`, `tui/widthx`, `tui/internal/hljs` and `internal/wordsegmenter` (the roughly 6 MiB of word-segmentation dictionaries). The only path is `coding/extension`, and, through it, `internal/codingagent/tools` (which imports `coding/extension` and `tui`).

## Where coding/extension touches tui

Six non-test files import `tui` or `tui/widthx`.

| File | Symbols | Use |
|---|---|---|
| `opaque_types.go` | `Component = tui.Component`, `TUI = tui.TUI`, `EditorComponent = tui.EditorComponent`, `EditorTheme = tui.EditorTheme`, `KeybindingsManager = *tui.TUIKeybindingsManager` | Aliases. `Theme` is already `any`. These are the root coupling: every API that mentions a component inherits it. |
| `custom.go` | `CustomFactory` (params `*tui.Theme`), `OverlayOptionsValue.Options tui.OverlayOptions`, `OverlayOptionsFunc`, `OnHandle func(*tui.OverlayHandle)` | `ui.Custom` and overlay options. |
| `ui_factories.go` | `DisposableComponent` (embeds `tui.Component`), `HeaderFactory`, `FooterFactory`, `EditorFactory`, `WidgetFactory`, `ThemeInstance{Theme *tui.Theme}`, `RemoteEditorComponent`, `RemoteEditorOf` | Header, footer, editor and widget factories. |
| `view_surface.go` | `ViewHeader`, `ViewFooter`, `FramedView.Render/Invalidate/Dispose` | Build the factories above from a `ViewSurface`. |
| `login.go` | `widthx.VisibleWidth` (lines 211, 339) | Width measure for the login list. Pure function. |
| `mcp_servers.go` | `widthx.JSTrim` (line 412) | JS-style trim in `ValidateMcpServerConfig`. Pure function. |

Indirect: `ui.go` (`UIContext`), `renderer.go` and `tool.go` (`ToolRenderCallFunc` returns `Component`; `ToolRenderers`), `remote_editor.go` and `remote_overlay.go` (use the factory types), `events.go`. They mention the aliases, not `tui`, so they change only if the aliases move.

Separate from the above, `coding/mcpext` needs `internal/codingagent/tools` (`TruncateMiddle`, `FormatSize`, `NodeFSError`, `RegisterExitCleanup`), which imports `tui` itself. That is a second edge to cut.

## Proposed boundary

1. `coding/extension` (core): everything that does not draw. Tools, events, commands, MCP server registration, the runner, `ExtensionFactory`, `UIContext`'s non-visual methods (`Notify`, `Select`, `Input`, ...). `Component`, `TUI`, `Theme`, `KeybindingsManager`, `EditorComponent` become small interfaces defined here, with no `tui` import:

   ```go
   type Component interface { Render(width int) []string; Invalidate() }  // what tui.Component requires, checked at the tui side
   ```
   Opaque ones (`TUI`, `KeybindingsManager`, `EditorTheme`) become `any` like `Theme` already is, with typed accessors in the tui-side package.
2. `coding/extension/exttui` (new): the terminal-typed surface. `CustomFactory`, the header, footer, editor and widget factories, `OverlayOptions` and handles, `RemoteEditor*`, `ViewHeader`, `ViewFooter`, `FramedView`. It imports both `coding/extension` and `tui`.
3. `widthx.VisibleWidth` and `widthx.JSTrim` move to a leaf package with no dictionaries (`internal/jsstring` already has JS string helpers; the width tables live in `tui/widthx`). Check that `tui/widthx` itself does not import `wordsegmenter` before reusing it; if it does, split the width function out.
4. `internal/codingagent/tools`: either move the four helpers mcpext uses into a leaf package, or split tools into `tools` (no tui) and the TUI renderers.

Because the aliases in (1) can stay for one release (`type Component = interface{...}` is structurally satisfied by `tui.Component`), `tui.Component` values keep working wherever a `extension.Component` is expected.

## Compatibility for extension authors

- In-process Go factories (the compiled-in form of `docs/specs/extension-factory-trust.md`) that call `ui.Custom` with a `CustomFactory`, or set headers, footers, editors and widgets, change their import to `exttui` for those types. Code that only registers tools, commands, events and MCP servers does not change.
- Type aliases make `extension.Component` interchangeable with the new interface, but `extension.CustomFactory(func(host extension.TUI, theme *tui.Theme, ...)` has a concrete `*tui.Theme` parameter today. Keeping `Theme` as `any` and passing it unchanged keeps the value; authors then assert `theme.(*tui.Theme)` (the pattern `mcptui.themeOf` already uses). This is a source break for factories that name `*tui.Theme` in the signature.
- Subprocess SDK extensions (Node, Python, Rust, Go SDK) use the wire protocol and are not affected. The remote editor and overlay bridges keep their wire shape.
- Per AGENTS.md there is no Pig-owned compatibility reader; this is an atomic change of the Go API, done with `make generate` for the interface ledgers.

## Risks

- Reach: 1230 files import `coding/extension`; most use only tools and events, but each alias change is checked by the compiler everywhere. The interface ledger (`test/parity/interfaces`) and PORT_MAP entries for the moved symbols must be regenerated and reviewed.
- Parity: the types mirror upstream `types.ts`; the move must not change observable behavior of `ctx.ui.custom`, `setHeader`, `setFooter`, `setEditorComponent`. The existing oracle tests must pass unchanged, with no new divergences.
- Cycles: `exttui` imports `tui`; `tui` must not import `coding/extension`. Today it does not (the `go list` result above), but `internal/codingagent` code that mixes both needs checking.
- Hidden edges: dependencies of `coding/extension` other than `tui` (the `agent` and `ai` packages) should be checked with `go list -deps` after each step, because a leaf import of `tui/internal/hljs` through another path would keep the dictionaries linked. The test `coding/mcpext/headless_deps_test.go` should then be tightened to forbid `tui` for all of `coding/mcpext`.
- Dead-code elimination does not help: `wordsegmenter` dictionaries are package-level data reached from `tui` init, so any link of `tui` pays for them.
