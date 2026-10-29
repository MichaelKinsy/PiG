# Lane F rows (new extension APIs in every SDK), delivered by FollowF

## (a) Upstream files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/coding-agent/src/core/exec.ts | ported | coding/extension/exec.go, exec_unix.go, exec_windows.go, opaque_types.go, internal/utf8stream | test:coding/extension/exec_max_buffer_test.go#TestExecCommandCapsCombinedOutputAtMaxBuffer, exec_utf8_chunks_test.go#TestExecCommandDiscardsPendingUTF8WhenAStreamExceedsTheCap, exec_tree_test.go#TestExecCommandForceKillsAChildThatIgnoresSIGTERMAfterFiveSeconds, exec_oracle_test.go#TestExecCommandMatchesPi, test/extension-conformance/exec_limits_test.go#TestExecLimitsAcrossSDKs | `MaxBuffer *float64` (0 distinct from undefined, NaN/fraction reject), `Truncated`, StringDecoder -> utf8stream.Decoder (moved out of internal/codingagent/tools), SIGKILL after 5 s via time.AfterFunc |
| packages/coding-agent/src/core/source-info.ts (changelogPath, SDK wire) | ported | coding/session_graph_package_manager.go, coding/session_graph_resource_loader.go, extensions/sdk/{context,resource_loader}.go, extensions/sdk-rs/src/context.rs | test:coding/session_graph_changelog_path_test.go, test/extension-conformance/host_surface_test.go#TestExtensionSourceInfoAcrossSDKs | Python/Lisp pass dicts through |
| packages/tui/src/tui.ts Component.capturesViewportInput | ported (SDK side) | coding/extension/host/subprocess/{protocol,ui_bridge,tui_alt_bridge,editor_proxy}.go, runtime-node/{runtime,editor-component}.mjs, internal/codingagent/{custom_overlay,ext_ui_context,remote_editor}.go, tui/editor{,_remote}.go, every SDK | test:test/extension-conformance/extension_mouse_test.go#TestConformance_ExtensionCapturesViewportInput | frame flag next to `mouse`; tui.alt caps + `capturesViewportInput` callback; editor frames |
| packages/tui/src/keybindings.ts getSelectPageIndex | ported (SDK helpers) | extensions/sdk/tui, sdk-py tui, sdk-rs tui, sdk-lisp tui-keybindings | per-SDK unit tests | Node uses Pi's own export |

## (e) New public API / interface IDs
Go sdk: `ExecOptions.MaxBuffer`, `ExecResult.Truncated`, `SourceInfo.ChangelogPath`, `PathMetadata.ChangelogPath`, `ViewportInputCapturer`, `tui.GetSelectPageIndex`. Rust: `ExecOptions.max_buffer`, `ExecResult.truncated`, `SourceInfo.changelog_path`, `captures_viewport_input()`, `tui::get_select_page_index`. Python: `exec(max_buffer=)`, `ExecResult.truncated`, `captures_viewport_input`, `tui.get_select_page_index`. Lisp: `ctx-exec :max-buffer`, `exec-result-truncated`, `:captures-viewport-input`, `tui-get-select-page-index`. Host: `extension.ExecOptions.MaxBuffer`, `ExecResult.Truncated`, `RemoteOverlayViewportHost`, `RemoteEditorViewportHost`, `tui.Editor.SetRemoteCapturesViewportInput`, `internal/utf8stream.Decoder` (moved from tools; regenerate the Go inventory: `utf8StreamDecoder` ids in tools are gone).

## sdk-*.toml proposals
Add exec option `maxBuffer`/result `truncated`, `changelogPath` on SourceInfo/PathMetadata, component member `capturesViewportInput`, helper `getSelectPageIndex` to sdk-surface/sdk-exports for each SDK; conformance ids: TestExecLimitsAcrossSDKs, TestConformance_ExtensionCapturesViewportInput, TestExtensionSourceInfoAcrossSDKs.

## Notes
- Published npm @earendil-works/pi-coding-agent 1.4.0 lacks maxBuffer/truncated/capturesViewportInput; sdk-ts follows the source-built oracle that `make parity-deps` installs.
- The SDK `.gz` bundle copies are regenerated with `go generate`.
- Pi MAX_STRING_LENGTH is fixed to Node 24 64-bit (536870888) in coding/extension/exec.go.
