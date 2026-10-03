# Lane gate-992-misc: chord + tui + carried mcp/codemode test porting (upstream 0.99.2)

Base `porter/pi-0.99.1`. Upstream mirror used: `.upstream/v0.99.2` (not present in the worktree; linked locally to a pinned 0.99.2 checkout whose test-file hashes equal `test-mapping-v0.99.2.json` for every file in scope). All 35 files in scope were pending in `test-mapping-v0.99.2.json`.

Phase 1 method (owner-approved): credit existing Go coverage with evidence, or port the test; no production fixes.

## Pending list in scope (35)

- chord (16): delta-apply-immutable, delta-clone, delta-diff, delta-tracker/retention, delta-tracker/tracker, delta, json, service-delivery, service-wire, services, state-delivery, state-diff, state-draft, state-fuzz, state-value, state
- tui (12): autocomplete-skill-slash, autocomplete, colors, editor, mouse-components, overlay-options, terminal-colors, terminal-image, terminal, tui-alt-screen, visible-width, wheel-scroll
- carried mcp/codemode (7): codemode declarations, codemode source, mcp client, mcp content, mcp oauth, mcp stdio, coding-agent mcp-oauth-refresh

## Status per upstream file

Per-case tables: `test/parity/unit-evidence/gate-992-misc-audit.md`.

### Credited (existing Go tests, all PASSING; ledger row `ported`): 19 files
All tui (12) and carried mcp/codemode (7). The 0.99.1 lanes had ported these tests; only the ledger rows were reset by the hash change. Changed/new 0.99 cases were re-read against the Go assertions (terminal-image sizing, terminal-colors query, overlay hide-after-stop, mouse submenu, autocomplete wrappers, wheel scroll, editor rows, DA1 forwarding, skill-slash). Test-path citations in the Go tests were moved from `.upstream/v0.99.1` to `.upstream/v0.99.2` (byte-identical files, checked with `cmp`).

### Ported-passing: 0. Ported-FAILING: 0.

### Chord (16 files): ledger row `designed-out` per owner decision D-H (plan section 13), pending lead confirmation (QUESTION gate-992-misc)
Per-file rationales in the mapping name the Go tests that cover part of each file (services 4 of 22, state-delivery 9 of 12 for the attached kind only, state ~5 of 19). Go has no JSON-draft/revision-diff/delta-tracker implementation, so a port would be failing tests against new feature code.

## Environment notes
- `TestColorDetectionMatchesPi`, `TestExtensionDialogsMatchPiBindingsAndPalette`, `TestExtensionDialogCountdownMatchesPi`, `TestLoginDialogMaskDisabledMatchesPi`, `TestGenerateSystemThemeColorsMatchesUpstream` in `./tui` fail here: they need `extensions/sdk-ts/node_modules` (installed Pi package), not present in this worktree. Unrelated to scope.
- `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` needs Xvfb (absent). `terminal`, `terminal-image`, `layout` pass against the vendored 0.99.2 TUI.

## Production fixes
None.
