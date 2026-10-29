# a1-codingpage rows (Pi 3bae8527, coding-agent half)

## (a) upstream files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| coding-agent/src/modes/interactive/components/custom-editor.ts | ported | internal/codingagent/custom_editor.go | test:internal/codingagent/custom_editor_keybindings_test.go#TestPR7680AutocompletePageKeysWinOverTheImagePasteShortcut | page keys reach an open autocomplete before app actions; D155 marker removed (Pi does it) |
| .../components/session-selector.ts | ported | internal/codingagent/session_selector.go | test:internal/codingagent/select_page_keys_test.go#TestSessionSelectorCapturesSelectionPageKeys; oracle TestSessionSelectorMatchesPi (page keys back in generator) | `CapturesViewportInput() = !renameMode`; paging over an empty list now leaves -1 as Pi does, so the D155 empty-list note and TestPR7680SessionSelectionStaysValidWhenPagingBeforeTheLoad are removed (the Pi 7629 regression test is not in the final upstream tree) |
| .../components/tree-selector.ts | pending-tuipage | tui/tree_select.go (no coding-agent wrapper: internal/codingagent/tree_selector.go returns *tui.TreeSelectorComponent) | - | owned by slice tuipage: must report false only while editing a label |
| .../components/{model-selector,oauth-selector,scoped-models-selector,user-message-selector,settings-submenu}.ts | pending-tuipage | tui/{model_select,oauth_selector,scoped_models_list,user_message_selector,select_submenu}.go | oracle scripts here (settings_submenu_oracle_test.go) now send PageDown/PageUp | owned by tuipage |
| coding-agent/src/extensions/llama/ui.ts | ported | internal/codingagent/llama/ui.go | test:internal/codingagent/llama/ui_test.go#TestHuggingFaceSearchPageKeysMoveTenResults; TestLlamaViewMatchesPi (PageUp/PageDown in generator keys) | SEARCH_MAX_VISIBLE=searchMaxVisible, HuggingFaceSearch and list views capture; LlamaView delegates; D155 marker removed |
| coding-agent/src/extensions/mcp/ui.ts | ported | coding/mcpext/mcptui/manager.go | test:coding/mcpext/manager_test.go#TestMcpManagerMenuCapturesViewportKeysOnlyWhileAMenuListIsShown; TestMcpManagerViewMatchesPi | menu handler captures, status/input/empty-menu screens do not |
| (PiG-only, D155) trust_selector.go, first_time_setup.go | unchanged-observable | those files | select_page_keys_test.go | Pi has no page handling there; keep `CapturesViewportInput() bool {return true}` with D155 marker |
| interactive_theme_modal.go consumeModalHostInputFor | ported | internal/codingagent/interactive_theme_modal.go | scenarios model-resolver-selector 22, 24 | a focused component capturing viewport input gets ALL viewport keys (tui-alt-screen.ts shouldDeferViewportInput), not only page keys; D155 marker removed |

## (b) PORT_MAP
No new rows; existing rows for the files above stay.

## (c) tests
- custom-editor-keybindings.test.ts "gives an explicit history binding..." -> custom_editor_keybindings_test.go#TestCustomEditorExplicitHistoryBindingBeatsModelCycling (moved from custom_editor_test.go; behavior-contracts.toml path lines 960/1592 updated in the red commit; ledgers/pin-ledgers/coverage.md/bug-ledger still name custom_editor_test.go for the history test: integrator regenerates)
- custom-editor-keybindings.test.ts "pages an open autocomplete menu before pasting an image..." -> custom_editor_keybindings_test.go#TestPR7680AutocompletePageKeysWinOverTheImagePasteShortcut (moved from select_page_keys_pr7680_test.go, now with the closing paste step)
- custom-editor-history-keybindings.test.ts: deleted upstream (renamed); mapping row follows the rename
- mcp-manager-view.test.ts "captures fullscreen viewport keys only while a menu list is shown" -> coding/mcpext/manager_test.go#TestMcpManagerMenuCapturesViewportKeysOnlyWhileAMenuListIsShown
- settings-selector.test.ts "pages a searchable submenu with selection page keys (#7629)": PENDING, hand-off to lane owner/tuipage: SelectSubmenu is tui.NewSelectSubmenu (tui/select_submenu.go); port as a tui test (20 items, searchable, PageDown, Enter selects theme-10). Oracle coverage already added in settings_submenu_oracle_test.go (page keys in selectScripts).
- model-selector.test.ts "moves by one page with remapped selection page keys (#7629)": tui/model_select.go, tuipage.
- llama search paging: no upstream test; PiG-only TestHuggingFaceSearchPageKeysMoveTenResults kept.

## (d) async contracts
None touched (McpManagerView.Menu / SearchModels ordering unchanged).

## (e) new interface IDs
None here (uses tui.CapturesViewportInput / tui.GetSelectPageIndex from tuipage). New exported methods: `CapturesViewportInput()` on SessionSelectorComponent, TrustSelectorComponent, FirstTimeSetupComponent, llama.HuggingFaceSearch, llama.LlamaView, mcptui.McpManagerView (replace CapturesSelectPageInput).

## D155 for the lane owner
Pi-following (no marker now): custom editor, session selector, llama search/LlamaView, McpManagerView, modal viewport deferral, session empty-list paging (D155 no longer needs the "keeps selection valid" clause).
PiG-only, keep marker: TrustSelectorComponent, FirstTimeSetupComponent (Pi has no page handling in either). Not touched here, tuipage's: extension selector, config selector, FilterableList (llama/mcp lists run on it and page like Pi's SelectList), tree selector.

## Changelog bullets
### Fixed
- Selection page keys (`tui.select.pageUp`/`pageDown`) now move the selection in the session selector, `/llama` and `/mcp` menus, and an open editor autocomplete menu, and a remapped page key (for example Ctrl+V) pages the menu instead of pasting an image. In fullscreen mode a focused list that handles those keys receives them instead of the transcript.
### Changed
- The session selector no longer keeps its selection index valid when a page key arrives before any session has loaded; like Pi it stays unselected until a key moves it.

## Scenarios
Changed (comments/description only, expected frames unchanged on Pi e7a0df27): model-resolver-selector/22-model-picker-unbound-keys-refilter, 24-fullscreen-model-picker-page-down-scrolls-viewport (name still says "scrolls-viewport"; rename touches normalization-inventory.json and coverage.md, integrator-owned). tui-components/11-select-list-input-state (tuipage): Pi now notifies selection change on PageDown at the last item, so the shared harness's `pageIgnored` is false on Pi (test/parity/testdata/batch2-ui-harness.ts and internal/codingagent/parity_harness.go probeSelectList); not edited by this slice.

## Open questions
- Session selector empty-list paging: removed PiG's divergence to match Pi's final code; restore it as D155 if the owner prefers.
