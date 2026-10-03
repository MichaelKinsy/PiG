# Lane port-99-revendor: re-vendor Pi's own dist to 0.99.1

Branch `port-99-revendor` from `porter/pi-0.99.1` (ab594c3e2). Scope: `coding/extension/host/subprocess/runtime-node/shims/pi-dist`, its manifest, the harness `package.json`, the vendoring automation and the tests that declare its seams. Pi's own code that the Node extension runtime runs (D73).

## Red

The red is the joint run 4 list: the vendored dist was 0.87.1 against an installed and pinned 0.99.1. The lane's own failing state at ab594c3e2 was these twelve tests in `coding/extension/host/subprocess`: `TestVendoredPiDistMatchesThePinnedPackage`, `TestVendoredPiTuiContainsEveryRuntimeModuleAndNativeAsset`, `TestNodeVendorManifestIncludesPinnedNativeAssets`, `TestNodeRuntimeShimsExportEveryPinnedPiValue`, `TestHarnessEntryIsNamedLikePinnedPi`, `TestPiTuiPublicExportsAreRealImplementations`, `TestPiSettingsManagerMatchesThePinnedPackage`, `TestPiThemeHelpersMatchThePinnedPackage`, `TestPiToolFactoriesMatchThePinnedPackage`, `TestNodeVendoredTuiUpstreamTests`, `TestNodeLibraryBundlesKeepSharedIdentitiesWithoutRawCatalogLoads`, `TestImportedExtensionRuntimeMatchesPi`. Re-vendoring exposed five more failing tests (the cold-start, startup-dependency, theme-state, archive and embed tests) that the 0.87.1 files had hidden. No test was ported or added: every case already exists and needed only the new vendor.

## Green

1. `dd90e8f9b` teaches the vendoring automation what 0.99.1 changed (no hand copying):
   - `automation/gen/vendor-pi-dist.sh` and `vendor-node-manifest.mjs`: `@earendil-works/pi-codemode` and `@earendil-works/pi-mcp` join the closure (`index.js` exports `createCodemodeExtension` and `createMcpExtension`, `.upstream/v0.99.1/packages/coding-agent/src/index.ts`), and `quickjs-wasi` (imported by pi-codemode) is vendored like the other dependencies.
   - `automation/gen/vendor-pi-startup-seams.mjs`: `theme.ts:4-24` now imports colour helpers and `getTerminalColorMode` from the pi-tui barrel, and the new `system-theme.ts:21-29` does too. Both are pointed at `pi-tui/colors.js`, `pi-tui/oklab.js` and `pi-tui/terminal-image.js` so theme initialisation does not load the barrel (same startup boundary as before).
   - `runtime-node/theme-palette.mjs`: Pi's `Theme` now keeps `fgAnsi`/`bgAnsi` and reads `dimTokens`, `concreteColors`, `ownAppearance` (`theme.ts:248-256,301-306,372-376`). The host palette carries only resolved escape sequences, so the rehydrated Theme gets those two maps and empty collections for the rest.
   - `harness/package.json` version 0.99.1.
   - Tests: `runtime_node_sdk_vendor_test.go` declares the two new theme seams; `runtime_node_vendored_test.go` adds the two packages to the pinned/closure package lists; `runtime_node_settings_test.go` compares `getOrCreateDeviceId`/`deviceId` by UUID shape because Pi creates a random per-tree UUID (`settings-manager.ts:156,1172-1179`).
2. `d9051ddf6` is the output of `automation/gen/vendor-pi-dist.sh` (run against `npm ci --min-release-age=0` of the committed lock; no `~/.npmrc` edit).
3. Last commit: `go generate ./coding/extension/host/subprocess` (runtime archive and digest).

## Evidence

- The twelve listed tests pass (with `Xvfb` on PATH for `native-clipboard-linux`; this host has it only at `/tmp/xvfb/root/usr/bin`) except `TestPiThemeHelpersMatchThePinnedPackage`, see Deferred.
- Full `coding/extension/host/subprocess` under `-race -timeout 60m` with `GOMAXPROCS=4 taskset -c 130,131,133,134`: all pass except that test. Cores 0-3 were saturated by other lanes and made three 128 MiB frame/cargo tests time out (`TestConn_AcceptsFrameAboveLegacyCap`, `TestHandleIncoming_OversizedResultReturnsError`, `TestContextResultShapesCrossSDKTransports/rust`); they pass on the quieter cores.
- `-race -count=24` with four CPU burners over the startup, theme, settings, vendor, archive, embed and harness tests: pass.
- Mutations: reverting `fgAnsi` to `fgColors` in `theme-palette.mjs` fails `TestNodeThemeCallsReturnHostResultsSynchronously`; pointing `system-theme.js` back at the barrel fails `TestNodeStartupDefersPresentationDependencies` and `TestVendoredPiDistMatchesThePinnedPackage`.
- `make typescript-extension-corpus`: 69/70 upstream example extensions load and register; `extensions/sdk-ts` `npm test` passes (pinned helpers and built-in factories match).
- Gates: `go build ./...`, `go vet`, `GOOS=windows go vet`, gofmt, `go fix -diff`, golangci-lint on the package: clean.

## Deferred

- `TestPiThemeHelpersMatchThePinnedPackage`: the only remaining red. It compares Pi's 0.99.1 `dark` theme with the palette from the Go theme, and `tui/theme_dark.json` is still the 0.87.1 file. Family 9a (`port-99-f9a`, `eb32e4be9`) rewrites it in OKHSL. With Pi's own 0.99.1 palette passed to the shim the same scene passes, so the shim is right and the test turns green when 9a merges.
- `jev-router.ts` (the one corpus failure): `pi.registerVirtualModel is not a function`. The Node runtime does not implement 0.99.1's `registerVirtualModel`, `registerMcpServer` and the other D-C additions (`runtime-node/runtime.mjs`); `port-99-f6e.md` records it as owned by the Node SDK sub-lane. Also red before this lane: `TestSelectedToolsPiContract`, `TestPythonSDKVirtualModelRegistrationAndRouting`, and the `sdksurface` and `customfactoryledger` drift tests (155 problems drop to 91 after the re-vendor).
- `extension-conformance`, `sdksurface` and the customfactory ledger are regenerated centrally at merge, not here.
- `runtime_node_tui_upstream_test.go` still reads the fixtures from `.upstream/v0.87.1` (identical to 0.99.1's, diff-verified); another lane's stale path literals.
- The rehydrated `Theme` does not know faint tokens, terminal-default tokens or a declared appearance because the host palette does not carry them; `theme.colors` and `theme.appearance` on a host theme are therefore Pi's defaults. Owner: the host palette (`internal/codingagent/ext_ui_context.go`), after 9a.
