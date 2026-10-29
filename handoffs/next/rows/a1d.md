# A1d: behavior-input-mapping closure at Pi e7a0df27

Branch `fix/follow-a1d` (base 4a0eec055c, includes fix/follow-a1c).

Files: `test/parity/interfaces/behavior-input-mapping-v1.4.0+e7a0df27.json`, `test/parity/behavior-contracts.toml` (+1 contract), this file.

Counts (pending): inputs 52 -> 36, renders 58 -> 41 (b3a29f11 baseline: inputs 37, renders 41). `make behavior-contracts` exit 0; `make behavior-contracts-strict` exit 2 (first remaining entry `AgentSession._emitExtensionEvent`, pending since before the bump).

Outcomes (33 entries closed, all `ported` or `divergence`, no new divergence):
- Source hash identical at b3a29f11 and e7a0df27, old targets/evidence/contracts copied (current contracts for each id equal the old set): LlamaView.handleInput/render, McpManagerView.handleInput/render, EasterEgg3dAnimation.handleInput, ExtensionEditorComponent.handleInput, ModelSelectorComponent.refreshModels, SessionSelectorComponent.handleInput, SteppedSubmenu.handleInput/render, InteractiveMode.setExtensionFooter/renderWidgetContainer/renderWidgets, CancellableLoader.handleInput, Editor.shouldSubmitOnBackslashEnter/renderTopBorder/renderBottomBorder, Input.handleInput, SelectList.render/renderItem, renderFakeCursor, Container.render.
- Pi changed, PiG already carried it, mapping now cites that evidence: CustomEditor.handleInput (page keys win over image paste; `TestPR7680AutocompletePageKeysWinOverTheImagePasteShortcut`; also fixed the moved `TestCustomEditorExplicitHistoryBindingBeatsModelCycling` path); TuiMainScreen.doRender (OSC 133 stripping; `stripOsc133ZonePrefixes`, contract claim already covers it); McpManagerView.menu.render (handler object refactor, no behavior change).
- New ids: McpManagerView.{status,render,redirectUrl}.handleInput closed from the A1c contracts status-cancel, empty-menu-cancel, redirect-url-screen-keys (their evidence/targets). getSelectPageIndex: new contract `tui/keybindings/get-select-page-index` (hashed at e7a0df27) with `TestGetSelectPageIndex`, `TestSelectListPageKeysMoveByAPage` and scenario model-resolver-selector/24.
- Divergence entries (UserMessageList.render, Editor.render, SettingsList.render, renderMainList): upstream render source hash unchanged, D66 still exists and still describes them, kept as `divergence` D66. None cited D155.
