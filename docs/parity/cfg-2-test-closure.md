# Settings and Package manager test closure

Reference: Pi 0.87.1, `.upstream/v0.87.1/packages/coding-agent/test/`. The denominator is the 46 `it` sites in `settings-manager.test.ts` and the 123 `it` sites in `package-manager.test.ts`. Each line number below identifies one original case. The named Go tests retain the original inputs and assertions, including table rows within a case. Existing tests are requalified, not counted as new ports.

## Settings manager

The tests use isolated settings files or `NewInMemorySettingsManager`, as appropriate. `PIG_USE_PI_DIRS=1` selects the original `.pi` layout in the first-half tests. Other tests use the selected configuration directory under existing D2. Go uses an empty string for the tested absent path values, nil versus empty slices for absent versus empty tool lists, and a pointer for theme-setting presence. The external-editor platform rows exercise the production resolver for Windows, Darwin and Linux; the public getter also runs on the host platform.

All test paths in this table are under `internal/codingagent/`.

| Upstream case lines | Go evidence | Contract |
|---|---|---|
| 29, 60, 88 | `settings_manager_first_half_test.go#TestSettingsManagerFirstHalfExternalChanges` | External enabled models, custom values and same-key UI precedence |
| 115, 130, 160, 188, 221, 240, 251, 262, 276, 285, 295, 313 | `settings_manager_first_half_test.go#TestSettingsManagerFirstHalfStorage` | Local extensions, package objects, reload, ordered errors, trust and directory creation |
| 203 | `settings_manager_first_half_test.go#TestSettingsManagerFirstHalfTheme` | Raw automatic theme versus fixed theme, persistence |
| 339, 358, 374, 379, 388, 397, 410 | `settings_manager_first_half_test.go#TestSettingsManagerFirstHalfValues` | Terminal overrides, retry cap, HTTP timeout and global cache warming |
| 439, 450 | `settings_theme_editor_upstream_test.go#TestSettingsThemeAndEditorUpstream` | In-memory editor precedence and all three platform defaults |
| 464, 477, 485, 494, 520, 533, 543, 556, 564, 573, 582, 597, 607 | `settings_upstream_test.go#TestSettingsManagerOriginalValues` | TUI/fullscreen settings, padding, Mermaid, shell prefix and default tools |
| 614, 620, 626, 633, 641, 647, 653, 662 | `settings_upstream_test.go#TestSettingsManagerOriginalPaths` | Session directory and shell path absence, precedence and home expansion |

The theme/editor test now uses the actual theme-setting getter, awaits the synchronous Go flush boundary, and uses in-memory settings for the original in-memory editor cases. No settings product change is needed.

## Package manager

All test paths in this table are under `cmd/pig/`. Package resolution is exercised through the production resource inventory, extension collector and managed-source resolver. These are the split Go implementation of Pi's manager. Child-process fixtures replace upstream command mocks with hermetic executable stubs. A mocked command rejection therefore becomes the real process exit error, while the original failure text remains on stderr. No network or user configuration is required.

| Upstream case lines | Go evidence | Contract |
|---|---|---|
| 104, 114, 125, 145, 161, 173, 185, 253, 265 | `package_resource_upstream_test.go#TestPackageResolveOriginalCases` | Empty/configured/automatic resources, symlink deduplication and manifest selection |
| 301, 314, 328, 353 | `package_resource_upstream_test.go#TestPackageSkillMetadataOriginalCases` | User, project and ancestor skill metadata |
| 389, 419, 443, 471, 507, 538, 558 | `package_agents_skills_upstream_test.go#TestPackageAgentsSkillDiscoveryOriginal` | Ancestor boundaries, nested Markdown, home scope, links and ignore files |
| 572, 580, 609, 643, 655 | `package_local_identity_upstream_test.go#TestPackageLocalSourceResolutionUpstream` | Direct sources, manifests, literal tilde entries, conventions and skill recursion |
| 670 | `package_progress_upstream_test.go#TestPackageLocalResolutionProgressUpstream` | Local resolution emits no progress |
| 686 | `package_local_identity_upstream_test.go#TestPackageCommandPreservesSpacedArgvUpstream` | Exact argv containing spaces |
| 702, 733, 746 | `package_npm_commands_upstream_test.go#TestPackageNpmCommandOriginal` | Wrapped npm install, uninstall peer flags and wrapped Bun |
| 767, 785, 801, 821, 849, 881, 908, 935, 961, 992 | `package_git_dependencies_upstream_test.go#TestPackageGitDependenciesOriginal` | Dependency argv, cleanup, pinned/unpinned reconciliation and dependency repair |
| 1030 | `package_npm_commands_upstream_test.go#TestPackageNpmRootCommandChangesOriginal` | Changed npm command invalidates old global-root lookup |
| 1069 | `package_npm_commands_upstream_test.go#TestPackagePnpmManagedResolutionOriginal` | Two resolutions perform one managed installation |
| 1116, 1159, 1185 | `package_npm_commands_upstream_test.go#TestPackagePnpmLegacyResolutionOriginal` | Legacy and wrapped pnpm paths, malformed JSON |
| 1202, 1217 | `package_progress_upstream_test.go#TestPackageInstallFailureProgressUpstream` | Start/error events, error propagation and HTTPS clone argv |
| 1231, 1254 | `package_source_parsing_upstream_test.go#TestPackageSourceDocsExamplesOriginal` | Documented source kinds and literal dot-relative paths |
| 1266 | `package_progress_upstream_test.go#TestPackageGitInstallRootTraversalUpstream` | Original parsed traversal rejected in all three scopes |
| 1285 | `package_temporary_path_upstream_test.go#TestTemporaryNpmPathUnderAgentRootUpstream` | Agent-owned temporary npm path and Unix permissions |
| 1305, 1319, 1333, 1344, 1353, 1361 | `package_settings_upstream_test.go#TestPackageSettingsSourceNormalizationUpstream` | Scoped source normalization, equivalent removal, changed booleans and retained filters |
| 1387, 1395, 1402, 1411, 1418, 1423, 1430, 1437, 1444, 1451, 1464, 1487 | `package_source_parsing_upstream_test.go#TestPackageHTTPSParsingOriginal` | HTTPS parsing, refs, shorthand and canonical identity |
| 1499, 1512, 1527, 1540, 1560, 1707, 1745, 1768, 1789, 1812, 1879, 1895, 1918, 1943, 1957, 1979, 1994, 2011, 2024 | `package_patterns_upstream_test.go#TestPackagePatternCasesUpstream` | Top-level/package filtering and exact force include/exclude |
| 1574, 1597, 1624, 1651 | `package_manifest_patterns_upstream_test.go#TestPackageManifestPatternsUpstream` | Manifest filtering, glob expansion, order, hidden paths and link traversal |
| 1833, 1859 | `package_autoload_upstream_test.go#TestPackageAutoloadDisabledOriginal` | Inherited project deltas and positive-only resources |
| 2047, 2069, 2085, 2099, 2112, 2125, 2144 | `package_local_identity_upstream_test.go#TestPackageIdentityDeduplicationUpstream` | Project precedence and cross-format repository identities |
| 2159, 2189, 2214, 2243 | `package_multifile_upstream_test.go#TestPackageMultiFileExtensionDiscoveryUpstream` | Multi-file extension entrypoints, manifests and helper exclusion |
| 2263, 2288, 2309 | `package_reconcile_upstream_test.go#TestNpmUpdateVersionReconciliationUpstream` | Configured range, current version and newer installed version |
| 2328 | `package_npm_commands_upstream_test.go#TestPackageLegacyNpmUpdateMigratesOriginal` | Legacy-to-managed npm update without metadata lookup |
| 2366 | `package_batch_upstream_test.go#TestPackageBatchedConcurrentUpdatesOriginal` | Scope batches, Git concurrency, pinning, ordering and joined completion |
| 2487, 2495 | `package_update_cases_upstream_test.go#TestPackageUpdateLookupPrefixesOriginal` | Exact npm/Git suggestions |
| 2503, 2515, 2531, 2546 | `package_update_cases_upstream_test.go#TestPackageOfflineResolutionOriginal` | Offline misses, cached Git, no npm view and mismatched pinned reinstall |
| 2560, 2569, 2588, 2600 | `package_update_cases_upstream_test.go#TestPackageAvailableUpdatesOriginal` | Offline/current/pinned exclusion and exact available-update objects |
| 2619, 2632 | `package_update_cases_upstream_test.go#TestPackageLatestVersionCommandsOriginal` | Exact metadata argv and returned versions; user lookup cwd follows approved D79 |
| 2653 | `package_reconcile_upstream_test.go#TestPackageCapturedCommandWaitsForStreamClosureUpstream` | Output is retained after process exit until the inherited stream closes |

D79 changes user-scoped metadata cwd to managed storage instead of the invoking project. The existing test asserts that approved exception rather than pretending it matches Pi's cwd. No new divergence or designed-out case is proposed. Unix-only temporary-directory permission assertions use the same platform condition as upstream. No new skip is added.

## Fixes and evidence

- Failed installs emitted one display string and no terminal progress event. `ProgressEvent` now preserves type, action, source and optional message. Install and update report start, then exactly one complete/error event before returning. Concurrent updates serialize callbacks and join all work. Temporary Git refresh reports pull completion/error while retaining the cache on failure. CLI output selects start messages and retains its existing success/error diagnostics.
- Source addition discarded Pi's changed boolean. The production settings function now returns true for addition/ref replacement and false for an identical source. The original six normalization cases assert every boolean and retained setting.
- Installed Git path validation duplicated the containment check and returned a different diagnostic. It now uses the same lexical managed-path resolver as temporary packages. The original traversal receives `outside package install root` in every scope.

The pre-fix `go test` compiled and failed both failed-install rows because only one event arrived. It also failed user/project traversal rows on `invalid Git package path` instead of the expected diagnostic. The source-addition boolean is a newly restored return contract; its no-op assertion is checked with a compiling mutation, not claimed as pre-fix runtime evidence.

The real Pi 0.87.1 oracle confirms local-resolution silence, both failed-install event objects, all three traversal errors, and the true/false/true source-change sequence. The oracle uses temporary directories and mocked commands.

Verification commands:

```sh
go test -count=1 ./internal/codingagent -run '^(TestSettingsManager(Original.*|FirstHalf.*)|TestSettingsThemeAndEditorUpstream)$'
go test -count=1 ./cmd/pig -run '^(TestPackage.*(Original|OriginalCases|Upstream)|TestTemporaryNpmPathUnderAgentRootUpstream|TestNpmUpdateVersionReconciliationUpstream|TestPackageChildOutputAndFailure|TestPackageRemovalProgressAndEmptySettings|TestPackageSourceReplacementPreservesFilters|TestPackageTasksStartOrderAndJoin)$'
go test -count=1 -race ./cmd/pig -run '^(TestPackage(InstallFailureProgress|InstallCompletionProgress|LocalResolutionProgress|TemporaryRefreshProgress|UpdateFailureProgress|GitInstallRootTraversal|SettingsSourceNormalization)Upstream|TestPackageBatchedConcurrentUpdatesOriginal|TestGitCheckout.*|TestGitCloneRepoPerPlatform)$'
go build ./...
go vet ./cmd/pig ./internal/codingagent
make test-porting-release
```

Use Go 1.27.1 and Node 24.19.0 for the command fixtures. The host also has Node 26.7.0; the initial red run used that version, and the final runs select the pinned Node explicitly. The build, touched-package vet, focused tests, race tests and release gate pass. The release gate retains other lanes' approved open obligations.

The existing `TestGitCheckoutRelativePerPlatform` caught a relative-root regression during refactoring. Canonicalizing both arguments to `filepath.Rel` fixes it. The final run includes all of that test's Windows/Linux/Darwin rows. Additional affected guards cover CLI output, trust, empty npm installation, temporary Git lifetime and cleanup, and update worker ordering.

`BenchmarkPackageLocalInstallProgress` runs the real local install/stat/progress path: 3419 ns/op, 385 B/op and 5 allocs/op in the recorded Linux run. CPU and allocation profiles are retained beside the logs. This is a baseline, not a speedup claim. Progress callbacks are synchronous and do not create goroutines, retain events or own process handles. Update callback serialization uses one invocation-owned mutex; existing bounded worker pools still join their work. Tests own their fixture directories and child lifetimes. No tmux server is started.

Lint configuration verification and touched-package lint pass. The mandatory repository lint commands report unrelated baseline blockers: `make lint-changed` includes a nested `examples/extensions/plan-mode` module outside the main module, and `make lint` reports a goimports issue in unchanged `coding/extension/host/subprocess/owner_upgrade_test.go`. Neither blocker is suppressed or changed in this lane.

No extension SDK or generated inventory is changed; the lead owns central regeneration.
