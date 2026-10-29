# a1-interactive rows

## (a) Upstream files
| upstream | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/coding-agent/src/modes/interactive/interactive-mode.ts (b3a29f11..e7a0df27, non-page parts: `[Skill warnings]`, model scope notice, extension changelog) | ported | internal/codingagent/{loaded_resources.go,model_scope_line.go,startup_header.go,interactive.go,interactive_changelog.go,slash_commands.go} | test:internal/codingagent/startup_hints_test.go#TestFullscreenModelScopeNoticeRendersInHeader; test:internal/codingagent/interactive_extension_changelog_test.go#TestExtensionChangelogUpdatesUpstream; test:internal/codingagent/loaded_resources_quiet_upstream_test.go#TestLoadedResourcesQuietReloadUpstream; scenario fullscreen/14-fullscreen-model-scope-notice, startup/09-startup-package-glob-collisions | |

## (b) PORT_MAP
interactive-mode.ts rows gain: `getModelScopeNotice`/`showFullscreenModelScopeNotice` -> model_scope_line.go (replaces `modelScopeLine`); `getExtensionChangelogUpdates`/`readPackageJson`/`ExtensionChangelogUpdate` -> interactive_changelog.go (`getExtensionChangelogUpdates`, `readPackageJson`, `extensionChangelogUpdate`); `showStartupNoticesIfNeeded` changelog block -> `showStartupChangelog(newEntries, extensionUpdates)`.

## (c) Upstream tests
- interactive-mode-model-scope.test.ts "renders the model scope inside the fullscreen header instead of printing it" -> TestFullscreenModelScopeNoticeRendersInHeader (stdout absence is `if m.opts.TuiMode != "fullscreen"` in init; byte-compared by scenario fullscreen/14).
- same file "adds nothing when startup details are hidden" -> TestFullscreenModelScopeNoticeHiddenWithStartupDetails.
- interactive-mode-extension-changelog.test.ts 4 cases -> subtests of TestExtensionChangelogUpdatesUpstream (+ BOM, no-version/malformed, unnamed package extras).
- interactive-mode-status.test.ts 'still shows diagnostics on quiet startup when requested' -> loaded_resources_quiet_upstream_test.go (expects `[Skill warnings]`).
- Startup block rendering (no upstream test) -> TestStartupExtensionChangelogBlock.

## (d) async contracts: none touched.
## (e) new interface IDs: none (all unexported).

## A2 symbols used by the part-3 commits (must be integrated first)
`source.SourceInfo.ChangelogPath` (via `PiSourceInfo = extension.SourceInfo`), `source.GetSourcePackageKey(source string, scope source.SourceScope) string` (called as `source.SourceScope(info.Scope)`; compiles whether Scope is string or SourceScope), `(*SettingsManager).GetExtensionChangelogVersion(key) (string, bool)`, `SetExtensionChangelogVersion(key, version) error`.
Part 3 commits (test aa38072356 and green) do not compile on the base without A2.

## Changelog bullets
### Changed
- Skill load warnings in the startup resource listing are now headed `[Skill warnings]` instead of `[Skill conflicts]`, as in Pi.
- In fullscreen mode the `Model scope: ...` notice appears in the header instead of being printed to the terminal before the alternate screen opens, so it no longer lingers in the shell after exit.
### Added
- After a package update PiG shows the package's changelog entries newer than the last seen version under "Extension Updates" at startup (one line per package with `collapseChangelog`); the first launch of a package only records its version.

## Divergences: none new; no divergence text changes.
## Scenarios: added fullscreen/14-fullscreen-model-scope-notice (+ testdata pig/pi-agent-model-scope); changed startup/09 (label).
## Docs: docs/site/docs/packages.md and internal/pigdocs/content/packages.md label sentence; docs/site/docs/settings.md and internal/pigdocs/content/settings.md `collapseChangelog` row. Pi's packages.md gained a "Show a changelog" section about `pi.changelogPath`; not copied here (package-manifest/loader behavior belongs to lane A2). Run `make docs-bundle` check on xd670: I edited the pigdocs mirrors by hand with identical text.
## Pending: none. Open question: ExtensionChangelog `$` in a package name would be a JS replacement pattern upstream; Go replaces literally.
