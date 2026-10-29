# a1-tuipage rows (Pi 3bae8527 tui half, #7629)

## (a) Upstream files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/tui/src/keybindings.ts (getSelectPageIndex) | ported | tui/selection_navigation.go | test:tui/select_page_keys_test.go#TestGetSelectPageIndex | `GetSelectPageIndex(kb, data, selectedIndex, itemCount, pageSize) (int, bool)`; replaces PageSelectionIndex |
| packages/tui/src/tui.ts (Component.capturesViewportInput) | ported | tui/selection_navigation.go | test:tui/select_page_keys_test.go#TestContainerDoesNotForwardViewportCapture | `ViewportInputCapturer` + `CapturesViewportInput`; optional property becomes an optional interface; Container no longer forwards |
| packages/tui/src/tui-alt-screen.ts (shouldDeferViewportInput) | ported | tui/tui_alt_screen_input.go | test:tui/select_page_keys_upstream_test.go#TestUpstreamAltScreenDefersViewportKeysToAFocusedList, #TestUpstreamAltScreenGivesViewportKeysBackWhenEditorAutocompleteCloses, #TestUpstreamAltScreenScrollsTheTranscriptForAFocusedContainerWrapper; scenario:model-resolver-selector/24-fullscreen-model-picker-page-keys-reach-selector | D155 special case removed; focused-component check replaces it |
| packages/tui/src/components/select-list.ts | ported | tui/select_list.go | test:tui/select_page_keys_upstream_test.go#TestUpstreamSelectListMovesByOneVisiblePageWithSelectionPageKeys; oracle tui/select_list_oracle_test.go; scenario:tui-components/11-select-list-input-state | page keys, no notify when the index is unchanged, capture flag |
| packages/tui/src/components/settings-list.ts | ported | tui/settings_list.go | test:tui/select_page_keys_upstream_test.go#TestUpstreamSettingsListMovesByOneVisiblePageWithSelectionPageKeys; oracle tui/settings_list_render_oracle_test.go | same |
| packages/tui/src/components/editor.ts | ported | tui/editor.go | test:tui/select_page_keys_upstream_test.go#TestUpstreamEditorMovesTheAutocompleteSelectionWithSelectionPageKeys; oracle tui/editor_autocomplete_render_oracle_test.go | flag true while autocomplete is open; D155 marker removed |
| packages/tui/src/index.ts | designed-out | tui/selection_navigation.go | n/a | Go exports are package-level identifiers |
| packages/coding-agent/.../model-selector.ts | ported | tui/model_select.go | test:tui/select_page_keys_upstream_test.go#TestUpstreamModelSelectorMovesByOnePageWithRemappedSelectionPageKeys; oracle tui/model_selector_oracle_test.go | page keys + flag |
| packages/coding-agent/.../oauth-selector.ts | ported | tui/oauth_selector.go | test:tui/select_page_keys_test.go#TestSelectorComponentsMoveByTheirPage; oracle tui/oauth_selector_oracle_test.go, tui/oauth_selector_input_test.go | page keys + flag |
| packages/coding-agent/.../scoped-models-selector.ts | ported | tui/scoped_models_list.go | test:TestSelectorComponentsMoveByTheirPage, TestUpstreamSelectorsPageWithRemappedKeys | page keys + flag |
| packages/coding-agent/.../user-message-selector.ts | ported | tui/user_message_selector.go | test:TestUserMessageSelectorReportsViewportCaptureOnItsList; oracle tui/user_message_selector_oracle_test.go | flag on UserMessageList only; UserMessageSelectorComponent no longer reports it (host must focus/consult `GetMessageList()`, as upstream's `focus: selector.getMessageList()`) |
| packages/coding-agent/.../tree-selector.ts | ported (tui half) | tui/tree_select.go | test:tui/select_page_keys_test.go#TestTreeSelectorCapturesViewportInputUnlessEditingALabel | `TreeList.CapturesViewportInput` = `labelInput == nil`, promoted to TreeSelectorComponent |
| packages/coding-agent/.../settings-submenu.ts (SelectSubmenu) | ported | tui/select_submenu.go | test:tui/select_page_keys_upstream_test.go#TestUpstreamSelectSubmenuPagesWithSelectionPageKeys | page keys are nav keys; OnSelectionChange fires when a page key moves the highlight |
| coding-agent session-selector.ts, custom-editor.ts, llama/ui.ts, mcp/ui.ts, interactive-mode.ts | not in this slice | internal/codingagent/** | n/a | owned by the coding slices |

## (b) PORT_MAP
Add/replace rows pointing the files above at the PiG files listed in (a). `tui/selection_navigation.go` replaces the PR-7680 mechanism (no `selection-navigation.ts` upstream: `getSelectPageIndex` lives in keybindings.ts).

## (c) Upstream tests
- tui/test/editor.test.ts "moves the autocomplete selection with selection page keys (#7629)" -> TestUpstreamEditorMovesTheAutocompleteSelectionWithSelectionPageKeys
- tui/test/select-list.test.ts "moves by one visible page with selection page keys (#7629)" -> TestUpstreamSelectListMovesByOneVisiblePageWithSelectionPageKeys
- tui/test/settings-list.test.ts same title -> TestUpstreamSettingsListMovesByOneVisiblePageWithSelectionPageKeys
- tui/test/tui-alt-screen.test.ts "defers viewport keys to a focused list ..." -> TestUpstreamAltScreenDefersViewportKeysToAFocusedList; "gives viewport keys back ... autocomplete closes" -> TestUpstreamAltScreenGivesViewportKeysBackWhenEditorAutocompleteCloses
- coding-agent/test/model-selector.test.ts "moves by one page with remapped selection page keys (#7629)" -> TestUpstreamModelSelectorMovesByOnePageWithRemappedSelectionPageKeys
- coding-agent/test/settings-selector.test.ts "pages a searchable submenu with selection page keys (#7629)" -> TestUpstreamSelectSubmenuPagesWithSelectionPageKeys (SelectSubmenu lives in tui/)
- Not in tui/: custom-editor-keybindings, mcp-manager-view (other slices).
- Renamed `tui/select_page_keys_pr7680_test.go` -> `select_page_keys_upstream_test.go` (`TestPR7680*` -> `TestUpstream*`). Removed: `TestPR7680AltScreenRoutesSelectionPageKeysThroughAFocusedContainerWrapper` (asserted Container forwarding that Pi's final design drops) replaced by `...ScrollsTheTranscriptForAFocusedContainerWrapper`; `TestContainerCapturesSelectPageInputFromItsChildren` replaced by `TestContainerDoesNotForwardViewportCapture`. Kept PiG-only: `TestUpstreamAltScreenRoutesAnOverlappingRemappedPageKeyToTheAutocomplete`, `TestUpstreamEditorForwardsRemappedPageKeysToTheAutocompleteMenu` (Pi covers it in coding-agent custom-editor tests; in tui they exercise the same Editor path), `TestUpstreamSelectorsPageWithRemappedKeys`.

## (d) Async contracts
None touched (editor autocomplete async path unchanged).

## (e) New interface IDs
`tui.GetSelectPageIndex`, `tui.ViewportInputCapturer`, `tui.CapturesViewportInput`. Removed: `PageSelectionIndex`, `SelectPageInputCapturer`, `CapturesSelectPageInput`, `Container.CapturesSelectPageInput`.

## D155 rewrite (lane owner)
Narrow D155 to PiG-only pagers/flags that Pi does not mark. Components in tui/ keeping `CapturesViewportInput() bool { return true }` with a D155 marker:
- ExtensionSelectorComponent (Pi has no page keys at all; paging stays PiG-only, `extensionSelectorPageSize`)
- ConfigSelectorComponent (Pi pages but does not flag it)
- FilterableList (PiG list; pages only without search; Pi has no component)
Follow Pi with no marker: SelectList, SettingsList, Editor, ModelSelectorComponent, OAuthSelectorComponent, ScopedModelsList, UserMessageList, TreeSelectorComponent. Remove the "Parity allowance" paragraph (oracle generators now include page keys, except extension selector dialog oracle `tui/extension_dialog_bindings_test.go`/`extension_selector_oracle_test.go`, which still leave them out because Pi's extension-selector.ts ignores them). Remove `tui/tui_alt_screen_input.go (matchViewportAction)` and `tui/selection_navigation.go` from call-site markers (only the components above remain). Update "Locked by": `tui/select_page_keys_test.go` (`TestGetSelectPageIndex`, ...), `tui/select_page_keys_upstream_test.go`.

## Scenarios
- changed: tui-components/11-select-list-input-state (`pageIgnored` -> `pageClamped`; Pi harness `test/parity/testdata/batch2-ui-harness.ts` updated). **Needs a one-line PiG-side change outside this slice**: `internal/codingagent/parity_harness.go` probeSelectList: rename `pageIgnored` to `pageClamped` in the variable and in the format string `select-list:render=%t:pageClamped=%t:wrap=...` (the condition `list.CursorIndex() == 2` stays).
- renamed+changed: model-resolver-selector/24-fullscreen-model-picker-page-down-scrolls-viewport -> 24-fullscreen-model-picker-page-keys-reach-selector (PgUp now moves the picker selection; this is the fullscreen "page key goes to the focused list" scenario). `test/parity/normalization-inventory.json` edited by hand for both; regenerate to confirm.
- comment-only: model-resolver-selector/22-model-picker-unbound-keys-refilter (same expectations).
- Historic `test/parity/upstream-sync/v1.4.0+*.toml` still name the old scenario id and `select_page_keys_pr7680_test.go`; leave (historical) or regenerate.

## Changelog bullets
### Fixed
- PageUp and PageDown (`tui.select.pageUp` / `tui.select.pageDown`) move the selection by one page in the select list, settings list, editor autocomplete and the model, OAuth, scoped-models, user-message and settings-submenu selectors, and fullscreen mode now hands these keys to a focused list instead of scrolling the transcript (Pi #7629).

## Pending / open questions
- Downstream (other slices, `internal/codingagent/**`): every caller of `CapturesSelectPageInput` must move to `tui.CapturesViewportInput`, and the user-message host must consult `sel.GetMessageList()` since the selector no longer reports the flag.
- `docs/bug-ledger.json` / `docs/site/docs/bug-ledger.md` mention the old names; lane owner.
- No upstream tui case left pending.
