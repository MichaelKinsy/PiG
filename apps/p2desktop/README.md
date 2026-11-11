# p2 desktop

The p2 server running in the app's own process on a loopback port, shown in one window of the system's web view, with the menus, tray icon, notifications and `pig://` links of a desktop app. It is a client of p2 like a browser tab: the server, its protocol and its data are `apps/p2`'s and are not changed (additive record D481, `docs/additive-features.md`).

**Status: experimental, follows p2's own label (D477).** The window shows p2's own web UI (the embedded Pi bundle `apps/p2` serves, D471); it holds the web facets of the plugins linked so far, so the desktop app grows with `apps/p2`.

## Layout

| Path | What |
|---|---|
| `shell/` | the seam: `shell.Platform`, `Window`, `Menu`, `Command`; plain Go, no framework types |
| `shell/mygoshell/` | the **only** importer of MyGo (`github.com/egoist/mygo`, pinned in `go.mod`); replace it to change framework |
| `shell/fakeshell/` | a recording `shell.Platform` for tests without a display |
| `internal/desktop/` | the app: server start and close, window, menus, tray, deep-link parser, notifier, sticky port |
| `internal/leakcheck/` | goroutine leak check run at shutdown (`P2_DESKTOP_LEAKCHECK=1`) |
| `icons/` | the PiG pixel pig master (`pig-pixel-green.png`) and every generated icon; `go run ./icons/gen` regenerates them all (script header documents the grid, scaling rules and sources) |
| `cmd/p2-desktop/` | the executable: the app, and `p2-desktop worker` for the session workers the server forks |
| `smoke/` | macOS smoke and measurements (against `p2 server` plus a browser tab) |
| `Makefile` | `make run` / `make run-radius`: build and run the app against its own data directory (local, or connected to Radius; Pi main 3a647d9dc) |
| `mygo.json` | app name, identifier, `pig` URL scheme, macOS microphone text, Linux package fields |

The module has its own `go.work` (`.`, `../..`, `../p2`) so that MyGo's requirements do not enter the root workspace; do not add it to the root `go.work` and do not run `go work sync` here (it rewrites the root `go.mod`).

## Behavior

- Server: `hosts.StartServer` in process, listening on `127.0.0.1` only. The port is the one of the last launch when free (the web UI keeps settings per origin), else any free port; the choice is kept in `<user config dir>/p2-desktop/state.json`, never in p2's data directory. Data directory: `P2_DATA_DIR`, else `~/.pi/p2`, as `p2 server`: a running `p2 server` on the same directory makes the app say so and quit (p2's own process lock).
- Auth: the window is a loopback browser, so p2 treats it as the device owner exactly as it does a browser tab (`authenticate` in `hosts/server.go`). Any other program on the machine is the owner too: that is p2's loopback rule, unchanged.
- Window: `PageOptions.Untrusted` (no Go bridge), may navigate and download only within the server's origin; links and `window.open` to http(s) open in the system browser; microphone only for that origin (after the system's own prompt).
- Menus: app menu (macOS), File, Edit, View (reload, force reload, zoom, full screen), Window. **Toggle Developer Tools exists only in debug builds** (`mygo.IsDev()`: any build without `-X github.com/egoist/mygo.production=1`, which `mygo build` sets).
- Tray: Show, Quit.
- Notifications: a session that stops working (or fails) while the window is not in front; the click opens that session. It uses the same status reader the web UI's sidebar uses, over an in-process link.
- Deep links: `pig://open`, `pig://session/<id>`, `pig://session/<id>/c/<n>`; anything else is ignored (`internal/desktop/route.go`).
- Single instance: a second launch hands its arguments to the first (a deep link on Windows and Linux) and exits.

## Build and run

```sh
cd apps/p2desktop
go build ./cmd/p2-desktop                         # debug build: developer tools menu, runs unbundled (no notifications, no pig://)
go run github.com/egoist/mygo/cmd/mygo build -platform darwin/arm64 -skip-dmg   # build/darwin-arm64/PiG.app, production, ad hoc signed
P2_FAKE=1 build/darwin-arm64/PiG.app/Contents/MacOS/PiG   # the faux provider
```

Or use the Makefile, which builds the debug app and runs it in the foreground, each target on its own data directory so `p2 server` on `~/.pi/p2` is left alone:

```sh
make run          # its own server, local only: data in ~/.pi/p2-desktop/local (FAKE=1: faux provider)
make run-radius   # its own server connected to Radius: data in ~/.pi/p2-desktop/radius (signs in on first use)
```

The app has no attach mode (Pi's `make attach`, the p2-mac app on a p2 already running on 8788, has no counterpart): its server runs in its own process, and a `p2 server` on the same data directory makes it quit.

Environment: `P2_FAKE=1` (faux provider), `P2_DATA_DIR`, `P2_IDLE_MS` (worker idle stop), `P2_DESKTOP_STATE_DIR` (where `state.json` lives), `P2_DESKTOP_LEAKCHECK=1` (check at quit that no goroutine started by the server survives; exit code 3 if one does).

## Tests

```sh
go test -race ./...                                           # Linux (xd6702), no display: fake shell + real in-process server
swiftc -O -o /tmp/cgw smoke/windows.swift                     # macOS only: lists an app's windows from the window server
go test -c -o /tmp/p2-smoke.test ./smoke && P2_WINDOWS_BIN=/tmp/cgw P2_DESKTOP_BIN=$PWD/build/darwin-arm64/PiG.app/Contents/MacOS/PiG /tmp/p2-smoke.test -test.run TestSmoke -test.v
```

`TestMeasure` also needs `P2_BASELINE_BIN` (a built `../p2/cmd/p2`) and `P2_CHROME_BIN`. The smoke uses the window server's list because System Events and `screencapture` see nothing while the session is locked.

## Installer recipes (documented, not run, not signed, not published)

All use the pinned CLI from this module: `go run github.com/egoist/mygo/cmd/mygo build ...` (see the supply-chain review: updates stay off, `mygo.json` only). Output goes to `build/<os>-<arch>/`.

**macOS** (needs a Mac; the Mac is the only machine that can sign and notarize):
1. `mygo build -platform darwin/universal` writes `PiG.app` and `pig-desktop-<version>.dmg`.
2. Signing: set `macos.signingIdentity` ("Developer ID Application: ...", the owner's) or pass `-sign`; the default is ad hoc, which Gatekeeper rejects on other Macs.
3. Notarization: `macos.notarize.keychainProfile` (made once with `xcrun notarytool store-credentials`), the CLI submits the dmg, waits for `Accepted` and staples it. Skipped with `-skip-notarize`.
4. Hardened runtime and entitlements: if lane B's codemode plugin links wazero's compiler into the server, the signed app needs `com.apple.security.cs.allow-unsigned-executable-memory` (`macos.entitlements`), or the interpreter (D263); decide when that plugin lands. The microphone usage text is already in `mygo.json`.

**Linux** (any host with Go can build; no cgo):
1. `mygo build -platform linux/amd64,linux/arm64` writes the executable, a `.desktop` entry, `pig-desktop-<version>-linux-<arch>.tar.gz` with `install.sh`, `pig-desktop_<version>_<arch>.deb` and a `PKGBUILD`.
2. Runtime needs: `libgtk-3-0`, `libwebkit2gtk-4.1-0`; `libayatana-appindicator3-1` for the tray (without it the tray is skipped, the app still runs).
3. Signing: the `.deb` and the archive are unsigned; publishing a signed APT repository or checksummed release assets is the owner's step.

**Windows** (blocked today, see below):
1. `mygo build -platform windows/amd64,windows/arm64` writes the executable and `pig-desktop-setup-<version>-<arch>.exe` (NSIS 3.13 is downloaded and SHA-256 checked, or use an installed `makensis`).
2. Signing: `windows.certificate` (+ `MYGO_WINDOWS_CERTIFICATE_PASSWORD`) or `windows.signCommand`; timestamp server `http://timestamp.digicert.com` by default.
3. Runtime needs the WebView2 Runtime (included in Windows 11; the installer for Windows 10 is Microsoft's).
4. **Blocker found:** `GOOS=windows go build ./cmd/p2-desktop` fails in `chord/nextnode/app/layout.go:71` (`undefined: syscall.Stat_t`), a Chord Files v2 file the p2 server imports. It is not MyGo's; the Windows build of p2 itself has the same failure. Needs a Chord fix (a build-tagged owner check) before any Windows package exists.

## Branding and icons

The app is called **PiG** (window title, menus, About, notifications); the executable and package files are `pig-desktop` so they never clash with the `pig` command. Every icon comes from one master, the PiG pixel pig of the site (`icons/pig-pixel-green.png`, copied from `handoffs/brand/pig-pixel-green.png`, which came from `site-r5/lead/public/` of the site tree): `go run ./icons/gen` rebuilds `icons/AppIcon.icns` (macOS grid: 824 px tile on a 1024 canvas, `macos.icon`), `resources/icon.png` (full-bleed 1024 px, from which `mygo build` derives the Windows and Linux icons), `icons/p2.ico`, `icons/linux/p2-<size>.png` and the tray icons (`cmd/p2-desktop/resources/`: a monochrome template pig for the macOS menu bar, coloured ones for Windows and Linux). Cells scale by whole pixels, nearest neighbour. Known limit: `mygo build` makes the Windows `.ico` and the Linux 256/512 px icons from `resources/icon.png` by area averaging, so those two are slightly soft; the crisp `icons/p2.ico` and `icons/linux/*.png` are generated for the installer recipes and for a MyGo fork patch (`handoffs/0.8.0/mygo-upstream/3-icon-overrides.md`).
