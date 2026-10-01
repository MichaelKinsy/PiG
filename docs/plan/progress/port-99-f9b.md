# Lane port-99-f9b: interactive content (Phase 2 step 9, second half)

Branch `port-99-f9b` from `porter/pi-0.99.1`. Scope: generic tool-call rendering (key=value titles, expanded key: value lines, MCP titles `server/tool`, five-line MCP result collapse), the interactive side of nested codemode calls, slash commands, tree, export-html toggle. Lane port-99-f9a owns theme, header, footer and selectors.

## Environment

- Every command ran with `HOME`, `PIG_HOME`, `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR` and `TMPDIR` set to scratch directories under `/tmp/f9b` (`/tmp/f9b/env.sh`), and Go's cache paths pointed at the real caches. Nothing was written under `~/.pig`.
- The exact 0.99.1 package is installed in `/tmp/pi99` by an earlier lane (`/tmp/pi99/node_modules/.bin/pi --version` prints 0.99.1). It is the comparator for probes; this branch does not move the pin.

## Upstream changes in scope (0.87.1 to 0.99.1)

| upstream | change | Go |
|---|---|---|
| `core/tools/render-utils.ts:71-96` | `formatToolCallWithArgs` | `tui/tool_call_args.go` |
| `modes/interactive/components/tool-execution.ts:155-157` | `createCallFallback` shows the arguments | `tui/tool_execution_definition.go` |
| `extensions/mcp/tools.ts:276-296` | MCP `renderCall` (label `server/tool`) and `renderResult` (5 lines) | `coding/mcpext` |
| `modes/interactive/interactive-mode.ts:3501-3503` | a nested call gets no card | `internal/codingagent/interactive_events.go` |
| `interactive-mode.ts` `handleSessionCommand` | single-entry cost breakdown only for a model other than the selected one | `internal/codingagent/slash_commands.go` |
| `interactive-mode.ts:640-675` | built-in extension commands untagged; source tags | `internal/codingagent/autocomplete_source_tag.go` |
| `core/keybindings.ts:142-145`, `interactive-mode.ts` hotkeys table | paste action description | `internal/codingagent/keybindings.go`, `interactive_hotkeys.go` |
| `core/export-html/template.{js,css}` | `H` toggle, hidden custom messages, nested calls | `internal/codingagent/export/assets` |

## Red

Commit `test(interactive): port the 0.99.1 interactive-content tests with signature stubs (red)`. Run per test in `docs/plan/evidence/port-99-f9b.red.txt` (each test alone, because a stub panic aborts the test binary).

| upstream test | cases | Go |
|---|---:|---|
| `coding-agent/test/tool-execution-component.test.ts:298` (null offset and limit) | 1 | `internal/codingagent/tool_execution_component_upstream_test.go` (passes at red: the read renderer already treats null as omitted) |
| `coding-agent/test/tool-execution-component.test.ts:451` (arguments in the fallback header) | 1 | same file (fails: the card draws the 0.87.1 structured-args view) |

The citations of the other cases in that file moved from 0.87.1 to 0.99.1 line numbers; their bodies are unchanged upstream.

Own tests, derived from the upstream source because upstream has no unit test for them: `tui/tool_call_args_test.go` (25 cases plus a color test), `coding/mcpext/tools_render_test.go` (5), `internal/codingagent/tool_call_fallback_test.go` (4), `slash_session_cost_breakdown_test.go` (4), `paste_files_description_test.go` (2), `autocomplete_source_tag_test.go` (3 tests), `export/export_0991_test.go` plus the 0.99.1 asset hashes in `export/upstream_test.go`.

Signature-only stubs: `tui.FormatToolCallWithArgs`, `autocompleteSourceTag`, `prefixAutocompleteDescription` (panic "not implemented"), `SlashContext.SelectedModelKey` (field).

## Not ported here, with reason

- `coding-agent/test/codemode-renderer.test.ts` (3 cases) is ported in the second red commit but skipped by name (`codemodeRendererSkip`) until port-99-f8-spec supplies `codemodeRenderers` (lead answer 2). This lane's wiring is the definition card path and the nested-call skip.
- Generated files: `make generate` needs the exact 0.87.1 Pi comparator and npm packages, which this host does not have, so no `chore(port-99): regenerate generated files` commit is made. The lead regenerates centrally. Affected: `internal/pigdocs/content/{divergences,keybindings}.md` (D59 retired, pasteImage and expand descriptions), `test/parity/normalization-inventory.json` (scenario 19 reason text), interface inventories (new exported `tui.FormatToolCallWithArgs`, `mcpext` command/manager types, `extension.CustomFactory`), coverage.
- `TestAppKeybindingDefinitionsMatchUpstreamInventory` fails until the keybinding inventory is regenerated for 0.99.1 (`app.clipboard.pasteImage` description).
- `startup_header.go` paste hint text belongs to lane port-99-f9a.
- Tree family: the only 0.99.1 tree change in scope is `keybindings.ts`, covered by the keybinding descriptions; no other code change.
- `mcpext.Factory` (which now registers `/mcp`) is not linked into the runtime by any lane yet, and `Options.OpenURL` stays host-supplied.
- Parity scenarios (`extensions-runtime/19`, `slash-commands`, `export-html`) were not re-run against a newer Pi: the runner accepts only the pinned comparator. Scenario 19 is now a paired `output_equal` scenario with no `[diverge]` block.

## Second red commit (lead answers)
- `/mcp` command (`coding/mcpext/command.go`), manager view and manager (`manager.go`), in-process `ExtUIContext.Custom` types (`coding/extension/custom.go`), and the codemode renderer stub (`internal/codingagent/codemode_renderer.go`) are signature stubs. Tests: `command_test.go` (9), `manager_test.go` (16), `ext_ui_custom_test.go` (7), `codemode_renderer_test.go` (3, skipped by name until port-99-f8-spec supplies `codemodeRenderers`).
- Red run: 32 of 33 fail, `TestExtUIContextCustomRejectsAValueThatIsNotAFactory` passes (the stub already errors). Details in `docs/plan/evidence/port-99-f9b.red.txt`.

## Green
- `862b2c14b` (green 1): `tui.FormatToolCallWithArgs` and the definition-card fallback (D59 retired), MCP `renderCall`/`renderResult` with `tools.GetTextOutput`, nested tool-call skip, autocomplete source tags, `/session` cost rule, paste description, export viewer assets.
- `c30b55262` (green 2): `/mcp` (`CompleteCommand`, `RunCommand`, pickServer, login/logout/reconnect), `McpManagerView` and `Manage`, registration in `mcpext.Factory`; in-process `ExtUIContext.Custom` (`ext_ui_custom.go`) with `extension.CustomFactory`/`CustomOptions`.
- Tests added or ported: `command_test.go` 11, `manager_test.go` 19, `ext_ui_custom_test.go` 7, `codemode_renderer_test.go` 3 (skipped), plus the green-1 set. All pass; `-race -count=24` on `TestMcp*` and `TestExtUIContextCustom*` passes.
- Mutation checks (revert, see red, restore): args cut length, key order, indent, UTF-16 unit count, nested skip, builtin tag, cost rule, MCP preview and label, definition-path gate; usage arity, single candidate, preferred candidate, failed-first order, empty-value submit, selection kept on rebuild, subscription ended after early finish, message of a failed action, done-before-factory, editor text restore, dispose after resolve, factory-error restore, context-cancel cleanup. Not asserted: `EnsureDiscoveryActive` after reconnect, the redundant width cut in `McpManagerView.Render`, the info level of "Sign-in cancelled." (needs an OAuth server).
- Gates: build, vet, `GOOS=windows go vet`, gofmt, golangci-lint (0 issues), `go fix -diff` empty on touched packages; `-race` on `tui`, `internal/codingagent`, `internal/codingagent/tools`, `internal/codingagent/export`, `coding/mcpext` shows no race. Remaining failures are environmental (oracle tests need `extensions/sdk-ts/node_modules`, tools tests need `rg`, catalog default model for openai-codex) or wait on regeneration.
