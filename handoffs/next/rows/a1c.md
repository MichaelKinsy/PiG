# Lane A1c: behavior-contract re-hash at Pi e7a0df27

All 19 drifting contracts were re-read against the Pi diff b3a29f11..e7a0df27. The ported Go code already matches the new upstream, so no Go code changed. Evidence and claims were extended with the page-key tests.

| contract id | old range -> new range | keybindings change | code change |
|---|---|---|---|
| coding-agent/scoped-models/input-state-machine | same markers; hash only (getSelectPageIndex branch) | none (handler uses getSelectPageIndex) | none |
| tui/select-list/input-state-machine | same markers; hash only | none | none |
| tui/settings-list/search-input-delegation | same markers; hash only | none | none |
| coding-agent/oauth-selector/input-state-machine | same markers; hash only | none | none |
| coding-agent/oauth-selector/boundary-transitions | same markers; hash only | none | none |
| coding-agent/user-message-selector/input-state-machine | same markers; hash only | none | none |
| coding-agent/select-submenu/input-state-machine | same markers; hash only | + tui.select.pageUp, tui.select.pageDown (matched directly) | none |
| coding-agent/custom-editor/active-keybinding-manager | same markers; hash only | + tui.select.pageUp, tui.select.pageDown | none |
| coding-agent/custom-editor/handler-order | same markers; hash only | + tui.select.pageUp, tui.select.pageDown | none |
| coding-agent/custom-editor/app-key-precedence | same markers; hash only | + tui.select.pageUp, tui.select.pageDown | none |
| tui/editor/input-state-machine | same markers; hash only | + tui.select.pageUp, tui.select.pageDown | none |
| tui/editor/handle-input-oracle | same markers; hash only | + tui.select.pageUp, tui.select.pageDown | none |
| tui/editor/submission-whitespace, history-whitespace, atomic-paste-segments, word-action-state | segments unchanged (hash unchanged); handler-level bindings only | + tui.select.pageUp, tui.select.pageDown | none |
| coding-agent/llama-search/handle-input | same markers; hash only | none (getSelectPageIndex) | none |
| tui/tui-alt-screen/viewport-input | same markers; hash only (shouldDeferViewportInput) | none | none |
| tui/render/main-screen-full-redraw | same markers; hash only (OSC 133 prefix strip) | none | none |
| coding-agent/mcp-manager/status-cancel | upstream_id `<anonymous@165>` -> `McpManagerView.status.handleInput`; start `...(data) => {` -> `...), {` (setContent object literal) | none | none |
| coding-agent/mcp-manager/empty-menu-cancel | upstream_id `<anonymous@130>` -> `McpManagerView.render.handleInput`; start moved to the one-line `this.setContent(frame(...), {` | none | none |
| coding-agent/mcp-manager/redirect-url-screen-keys | upstream_id `<anonymous@206>` -> `McpManagerView.redirectUrl.handleInput`; start `(data) => {` -> `{ handleInput: (data) => {` | none | none |
| coding-agent/mcp-manager/handle-input-delegates, menu-render | same markers; hash only | none | none |

Mapping (integrator-owned, not edited here): `behavior-input-mapping-v1.4.0+e7a0df27.json` has the handlers `McpManagerView.status.handleInput`, `.render.handleInput` and `.redirectUrl.handleInput` pending with no contracts. They should reference `coding-agent/mcp-manager/status-cancel`, `empty-menu-cancel` and `redirect-url-screen-keys`, with the targets and evidence of the old `<anonymous@165|130>` entries (the old `<anonymous@206>` entry was pending). `make behavior-contracts-strict` fails on the 52 pending mapping entries (36 were already pending at the previous pin b3a29f11; 16 were reset by the pin bump).
