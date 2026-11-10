# pi151-chord rows (branch fix/pi151-chord)

Pi 3b921e31, 7e713da2, cd5e3d2a, 23303aed. Red tests failed on 6231a1e77; green at a9ff67595 and 584ab469e.

## upstream-sync/v1.5.1.toml
| path | disposition | evidence | rationale |
|---|---|---|---|
| packages/chord/src/node/tui/sandbox.ts | ported | chord/nextnode/tui/tui_sandbox.go; chord/nextnode/tui/tui_sandbox_test.go | filepath.EvalSymlinks after os.MkdirTemp (Pi realpath); not a grant widening, a startup fix |
| packages/chord/src/node/http/upgrades.ts | ported | chord/nextnode/http/upgrades.go; chord/nextnode/http/websocket.go; chord/nextnode/http/upgrades_test.go | error listener leaves a CLOSING socket alone; readFailed marks the socket Closing before `error` |

## test-mapping-v1.5.1.json
| path | disposition | evidence | rationale |
|---|---|---|---|
| packages/chord/test/node/tui/sandbox.test.ts | ported | chord/nextnode/tui/tui_sandbox_test.go, chord/nextnode/tui/tui_kit_upstream_test.go | 4 cases; new: TestTheSandboxStartsWithASymlinkedTemporaryDirectoryWithoutExpandingFilePermissions |
| packages/chord/test/node/app/files.test.ts | ported | chord/nextnode/app/files_test.go, chord/nextnode/app/files_support_test.go | 30 cases; new: TestMetadataQueriesOrderEqualCreationTimesByIDInPagesAndSummariesIncludingAfterRemoval |
| packages/chord/test/node/http/upgrades.test.ts | ported | chord/nextnode/http/upgrades_test.go | 19 cases (try/finally only); added Go test TestPluginSocketsAnOversizedFrameIsNotTerminatedAndTheSocketReportsItsProtocolCloseCode |
| packages/chord/test/node/plugins/{plugins,watcher}.test.ts, node/surfaces/tui.test.ts, registries/value.test.ts | previous disposition and evidence | unchanged | JS file-watcher/GC timing fixes (settleFileEvents, vi.waitFor, capture before release); no Go counterpart |
| packages/chord/test/node/app/app.test.ts | pending (unchanged) | | recordBase spy ordering fix |

Non-test: test/examples/66-websocket-limits.ts -> chord/nextnode/http/http_example_66_test.go TestExample66WebSocketLimits; vitest.config.ts and test support files: JS-only.

## PORT_MAP
No row change (upgrades.ts and sandbox.ts rows stay ✅).

## Changelog
changelog.d/chord-pi151-584ab469e.md
