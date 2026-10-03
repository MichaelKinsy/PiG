# Windows GA audit

Base: the `agg-100` integration branch @ `23d124298`, audited on Linux. Nothing here was run on Windows. The PowerShell lane owns native evidence (W1-W7 in `WIN-NOTES.md`, 2026-10-02 01:05Z). This file separates what the tree shows from what only a native run can show.

## Result

Windows has no stub, TODO or "not supported" path that is a bug. Every production Windows branch either has a `_windows.go` implementation or mirrors a refusal in upstream Pi. The gaps are evidence gaps and four small defects:

| ID | Gap | Class | State |
|---|---|---|---|
| W-01 | The native CI job runs 7 package groups; 56 of 79 `*_windows_test.go` files are in packages it never tests. Nothing on Linux compiled them. | real gap (evidence) | compile gate fixed here; native execution is FIXLANE `win-ci-widen` |
| W-02 | `PIG_DEBUG` wrote the literal `/tmp/pig-debug.log`, which is `C:\tmp\...` on Windows and silently failed. | real gap (product) | fixed here |
| W-03 | The Python pi.events fixture wrote its log in text mode; on Windows every line gained `\r`, failing 35 conformance rows. `cmd/pig/testdata/host-wiring-virtual-model.py` had the same defect and failed `TestSubprocessVirtualModelReachesTheModelRuntime`. | real gap (test) | both fixed here; red on Linux when the fixture opens with `newline="\r\n"`, which is the Windows text-mode translation |
| W-04 | 44 tests call `testenv.Symlink`, which skips without symlink privilege; `RequireDirectoryLink` (junction) exists and is used elsewhere. | real gap (evidence) | FIXLANE `win-symlink-skips` |
| W-05 | `TestAuthStoragePreservesExistingMode` skips on Windows; no test asserts auth.json keeps its ACL across a rewrite. | real gap (evidence) | FIXLANE `win-auth-acl-preserved` |
| W-06 | Five CLI mode tests (`--mode json`, `--tools` precedence, `--export` theme, app mode, Piglet tool scope) are `darwin \|\| linux` only. CI smoke covers print and RPC only. | real gap (evidence) | FIXLANE `win-cli-mode-tests` |
| W-07 | `findPythonExecutable` takes the first of `python`, `python3` on `PATH`. A clean Windows profile has the Store alias stub there, which is not a Python. | suspected (unverified) | FIXLANE `win-python-store-alias` |
| W-08 | Tests that bind their own unix socket under `t.TempDir()` fail when `%TEMP%` is deeper than about 55 bytes (reproduced with a 58-byte TEMP in the 2026-10-02 native run). | real gap (test) | FIXLANE `win-test-socket-paths` |
| W-09 | `windows/arm64` zip and npm packages are published without a native smoke (`docs/plan/release-040-dryrun.md:38`, `release-040-NOTES.md:106`). | evidence gap | owner decision: state it, or add an arm64 runner |
| W-10 | Standalone `pig.exe` is not replaced in place (D39). | platform-specific, matches Pi | GA decision, see below |
| W-11 | #106 (`rename: Access is denied` publishing a cell) and `env-block-createprocess` are READY on their own branches and not in agg-100. | in flight | merge before any GA flip |
| W-12 | Docs: `windows.md` says "Do not treat Windows as release-supported", omits `install.ps1`, and the shell requirement; README, `index.md`, `quickstart.md` say "preview". | docs | both outcomes drafted, `DOCS-DRAFT.md` |

W-10 is a decision, not a defect. Pi 1.0.0 refuses a Windows self-update unless the install is npm or pnpm (`packages/coding-agent/src/package-manager-cli.ts:1058-1066`), and PiG does the same for yarn and bun. Pi checks for an installer-managed install first (`package-manager-cli.ts:1026-1056`, `PI_MANAGED_INSTALL_ROOT`), and that branch has no platform check, but Pi documents its installer for macOS and Linux only (`docs/quickstart.md:9-12`). PiG differs from Pi only in shipping a standalone zip and `install.ps1`, so a standalone install is refused where Pi documents no such install. An in-place replacement (rename the running exe aside, write the new one) would be a new numbered divergence needing owner approval. The 0.4.0 notes already tell users to rerun the installer.

## Method and counts

- Windows implementation files: 56 `_windows.go` (7 are `internal/testenv` support), and 40 other non-test files with a `runtime.GOOS` Windows branch.
- Windows tests: 79 `_windows_test.go` files in 29 directories.
- `go build ./...` and `go vet ./...` pass for `GOOS=windows` on amd64 and arm64. `GOARCH=386` does not build (`tui/tui.go:2136`, `ai/assistant_retry.go:110`); it is not a shipped target.
- Every `process.platform === "win32"` and `.exe` site in the pinned upstream `packages/` (non-test source) was matched to a Pig site: shell and Git Bash lookup, `taskkill`, cross-spawn, `notepad`, `clip`, PowerShell clipboard, tools-manager `tar.exe`, `fd.exe`/`rg.exe`, win32 keybindings, native Shift+Enter, VT input, right-click paste, title restore, suspend, `refreshTerminalDimensions`, quarantine update, `~\`, `normalizeWindowsShellPath`, lowercase path comparison. None is missing, with two exceptions that are not Windows bugs: archive extraction uses the Go standard library on every platform (D14), and Pi's managed npm install layout (`PI_MANAGED_INSTALL_ROOT`, `.bin/pig.cmd`) has no Pig counterpart and was not audited. The only upstream "not supported on Windows" strings (`client/src/unix.ts:38,99`, suspend) are ported verbatim.
- `/tmp`, `/dev/null`, `/bin/sh`, `/proc`, `HOME`, `Getuid`, `syscall.Kill` and POSIX `exec.Command` greps over untagged production code found one defect (W-02). The `HOME` reads copy Pi's `HOME || homedir()` and fall back to `USERPROFILE`.

## Windows skips and exclusions, classified

Legitimately platform-specific (no action):

| Where | Why |
|---|---|
| `selfupdate_platform_test.go:14`, `selfupdate_lock_test.go:23,59,112`, `cmd/pig/self_update_test.go:96` | D39; the Windows tier is covered by `self_update_windows_test.go` |
| `tools_manager_test.go:92`, `self_update_npm_registry_test.go:335`, `chalk_color_test.go:11`, `exec_test.go:183` | Windows has its own native oracle test (`tools_lookup_windows_test.go`, `TestWindowsNpmInstalledPigUpdatesThroughRealNpm`, `chalk_level` build table, WSL launcher) |
| `tools_edit_port_test.go:177`, `models_store_upstream_test.go:58`, `radius_auth_test.go:166`, `socket_runtime_dir_test.go:102,133`, `retained_admission_paths_test.go` (4) | Windows has no read-permission bit, no POSIX mode, accepts UNC `file://` hosts, no owner check, and cannot hold those filename characters |
| `deleted_cwd.go:14`, `parity/runner/runner_test.go:159`, `install_sh_test.go:117` | Windows cannot delete a cwd or take POSIX signals; the tmux harness cannot drive `pig.exe`; `install.sh` refuses Windows |
| `external_editor_test.go:270`, `resolvepath_test.go:40`, `local_path_upstream_test.go:45` | Windows-only tests skipped elsewhere |
| `native_platform_upstream_test.go:133,157` | upstream's own skip; CI sets `PI_TEST_NATIVE_CLIPBOARD=1` |

Needing action: `testenv.go:58` (W-04), `auth_pi_lock_test.go:44` (W-05), `script.go:48` and `script_windows.go:30` (skip when Git for Windows or an interpreter is missing; CI has them, a developer machine may not, so a missing prerequisite reads as a pass).

Unix-only test files (`//go:build unix`, `linux`, `darwin`, `!windows`): 100. Legitimate groups: POSIX signals and process groups (`rpc_signal`, `print_signal_exit`, `suspend_*`, `upstream_hup_shutdown`), pty and tmux (`*_pty_test.go`, `interactive_*`), X11 and Darwin clipboard (`clipboard_x11_*`, `*_darwin*`), ELF and `/proc` (`nodespawn`), unix-socket servers that Pi's `server` package disables on win32 (`internal/experimental`). Not legitimate by name alone: the five `darwin || linux` CLI tests in W-06, and `!windows` tests whose Windows counterpart exists (`pilock/mkdir_errors_other_test.go`, `runtimecell/rename_other_test.go`), which is intended.

## Windows divergences and platform records

| ID | Subject | Verdict |
|---|---|---|
| D39 (`DIVERGENCES.md:246`) | standalone `pig.exe` update; npm and pnpm follow Pi | W-10 |
| D68 (`DIVERGENCES.md:880`) | owner-only files by DACL | platform-specific, Windows has no mode bits; locked by `ownerfile` tests |
| D67 (`additive-features.md:806`) | container builds omit `--user` | platform-specific |
| D69 (`additive-features.md:822`) | `.cmd` Piglet launcher | platform-specific, additive |
| D14 (`DIVERGENCES.md:71`) | fd and rg archives extracted with the Go standard library, so no `tar.exe` or `Expand-Archive` | platform-neutral |
| D19 (`additive-features.md:116`) | Node extensions over a named pipe; Python and Rust over Winsock AF_UNIX | platform-specific |

All six have call-site markers in production code. None needs closing for GA. Each is listed in the supported-outcome `windows.md` draft.

## Docs and status lines

`README.md:30`, `docs/site/docs/index.md:5`, `quickstart.md:3` and `windows.md:3,16` carry the preview wording. `windows.md:3` also says to withhold release support until native verification passes. `CHANGELOG.md:812-818` is the 0.2.0 record and stays.

## Not in agg-100

- `fix-106-win-publish` (#106): bounded retry of the cell-publish rename on `Access is denied`, sharing and lock violations.
- `env-block-createprocess` @ `0ff4baf97`: children get libuv's environment block as is.

## Done here

Three commits, signed, on `team/smc1/win-ga`: W-02, W-03, W-01 (compile gate). Evidence for each is in its commit message.
