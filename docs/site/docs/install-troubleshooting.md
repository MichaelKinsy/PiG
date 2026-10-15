# Install and troubleshoot PiG

PiG publishes release archives on [GitHub Releases](https://github.com/MichaelKinsy/PiG/releases). The archive names on this page are the ones the release workflow produces. To build `pig` from source instead, follow the [Quickstart](/docs/latest/quickstart).

## pig: command not found

The terminal runs a command only when the command's folder is on `PATH`, the list of folders the terminal searches. Print the list with `echo $PATH`.

- **Install script.** `install.sh` puts `pig` in `~/.local/bin` (`$PREFIX/bin` in Termux). When that folder is not on `PATH`, the script ends with `pig-install: Add <folder> to PATH to run pig, for example: export PATH="<folder>:$PATH"`. Run that `export` line. Add the same line to the end of `~/.zshrc` (macOS) or `~/.bashrc` (most Linux systems) so that new terminals have it, then open a new terminal.
- **npm.** npm puts `pig` in its global `bin` folder. `npm prefix -g` prints the folder that contains it: `bin` below that folder on macOS and Linux, the folder itself on Windows. Add that folder to `PATH`. If `npm install -g` fails with `EACCES`, your Node.js installation keeps global packages in a system folder. Follow npm's guide at https://docs.npmjs.com/resolving-eacces-permissions-errors-when-installing-packages-globally, then install again.
- **Go.** `go install` puts `pig` in `$(go env GOPATH)/bin`, which is `~/go/bin` by default. Add that folder to `PATH`.

Run `pig --version` again to check.

## Change what the install script does

`install.sh` reads these environment variables. Set them on the `sh` side of the pipe, for example `curl -fsSL https://pi-in-go.dev/install.sh | PIG_VERSION=0.5.0 sh`.

| Variable | Meaning |
|---|---|
| `PIG_VERSION` | Install this release (for example `0.5.0`) instead of the latest. |
| `PIG_INSTALL_DIR` | Directory that receives `pig`. The default is `$HOME/.local/bin`, or `$PREFIX/bin` in Termux. |
| `PIG_HOME` | PiG's settings directory, where the install receipt goes. The default is `~/.pig`. |
| `PIG_API_BASE` | The API that names the latest release. The default is `https://pi-in-go.dev/api`. |
| `PIG_LATEST_RELEASE_URL` | The page that redirects to the latest release, used when the API does not answer. |
| `PIG_DOWNLOAD_BASE` | The release download root. The default is the GitHub Releases download URL of this repository. |
| `PIG_UPDATE_URL` | The update manifest that `pig update` uses. The default is the latest release's `update.json`. |

The script verifies the archive against the release's `SHA256SUMS` before it writes anything to the install directory, whichever mirror these variables select.

## Verify what you installed

`pig verify` prints the binary's identity and the SHA-256 of its bytes. Verification starts from that digest; the version string, file name, and download URL prove nothing on their own.

```bash
pig verify                                   # this binary: identity and SHA-256
pig verify --checksums SHA256SUMS pig-<version>-linux-amd64.tar.gz
pig verify --provenance ./pig                # GitHub build provenance, via gh
pig verify --packages                        # installed npm and git Packages
pig verify ./my-extension ./agent.yaml       # extension, Package, or Piglet
```

`--checksums` requires each file's digest to match its entry in the release `SHA256SUMS`. `--provenance` runs `gh attestation verify`, which checks the Sigstore bundle, its transparency-log entry, and that the signer is PiG's release workflow. A binary you built yourself has no attestation and reports as unverified. `--packages` runs `npm audit signatures` for installed npm Packages and checks that each git Package's working tree matches its commit. A directory or Piglet file is loaded through the same validators PiG uses at startup. The design follows `pi verify` in [dimetron/pi-go](https://github.com/dimetron/pi-go).

## macOS

Browsers, AirDrop, and messaging apps add the `com.apple.quarantine` attribute to downloaded files. Gatekeeper then blocks an unsigned binary with a message that the developer cannot be verified. The release workflow does not sign binaries with an Apple Developer ID and does not notarize them. Files downloaded with `curl` or `wget` are not quarantined.

After you verify the SHA-256, remove the attribute from the binary or from the extracted archive directory:

```bash
xattr -l ./pig
xattr -d com.apple.quarantine ./pig
xattr -dr com.apple.quarantine ./pig-<version>-darwin-arm64
```

Apple silicon Macs use the `darwin-arm64` archive and Intel Macs use `darwin-amd64`. `uname -m` prints `arm64` or `x86_64`.

## Windows

Files downloaded with a browser carry the Mark of the Web, and SmartScreen can warn that the app is unrecognized. After you verify the SHA-256, run `Unblock-File .\pig.exe` in PowerShell. Microsoft Defender can flag unsigned Go binaries heuristically; verify the checksum and report a false positive at https://www.microsoft.com/wdsi/filesubmission.

Add the directory that contains `pig.exe` to `PATH`, then open a new terminal. Windows Terminal gives the best keyboard and color support; see [Windows](/docs/latest/windows).

## Linux

The release workflow builds binaries with cgo disabled, so they do not depend on the system C library. Use `linux-amd64` when `uname -m` prints `x86_64` and `linux-arm64` when it prints `aarch64`. If a download fails with `permission denied`, run `chmod +x pig`; if the file sits on a `noexec` mount such as /tmp, move it to `~/.local/bin`.

## Android (Termux)

Termux reports `uname -s` as `Linux` and `uname -o` as `Android`. The installer detects this, downloads the `android-arm64` archive, and installs `pig` into `$PREFIX/bin`. Other Android CPUs have no archive; `uname -m` must print `aarch64`. Do not use `linux-arm64` there: it is static, and Termux's loader rejects it with `has unexpected e_type: 2`. See [Termux](/docs/latest/termux). `pig setup go` cannot install Go on Android, because Go publishes no archive for `android/arm64`: it stops and tells you to run `pkg install golang`. PiG uses the `go` on `PATH`, and `pig setup status` shows it. `pig install npm:...` and `pig install git:...` start `npm` and `git` through `/system/bin/linker64` like every other program PiG starts. `PIG_CELL_BUILD_TIMEOUT` (default 10 minutes) also bounds `pig install <dir> --validate-only`, so a slow Rust build on a phone can be given more time.

## Proxies and certificates

PiG's HTTP clients honor `HTTPS_PROXY`, `HTTP_PROXY`, and `NO_PROXY`. On macOS and Windows PiG trusts the system certificate store. On Linux, set `SSL_CERT_FILE` or `SSL_CERT_DIR` when a corporate proxy re-signs TLS traffic. Node extensions read additional certificates from `NODE_EXTRA_CA_CERTS`.

## Toolchains for extensions and Piglet builds

PiG runs without any toolchain. Extensions and Piglet builds need the tool for their language:

| Need | Tool | Set up |
|---|---|---|
| Go extensions, native Piglet builds | Go | `pig setup go` installs a verified Go under `~/.pig/toolchains/go`; PiG uses it when `go` is not on `PATH` |
| Container Piglet builds | Docker or Podman | `pig setup container` shows the steps for your system |
| Rust extensions | cargo | https://rustup.rs |
| Node and TypeScript extensions | Node.js 22.13 or newer | https://nodejs.org |
| Python extensions | Python 3 | https://www.python.org/downloads/ |

`pig setup` shows which tools are present. Native Piglet builds also need a PiG source checkout that matches the binary; set `PIG_SOURCE_ROOT` to it.

## Terminal display

A real change of terminal width or height redraws the whole transcript, as Pi does. If the view jumps without a size change, set `PI_TUI_DEBUG_REDRAW=1`, reproduce the problem, and read `pi-tui-debug.log` in the configured agent directory (normally `~/.pig/agent`): each full redraw appends its reason. For tmux settings, see [tmux](/docs/latest/tmux).

## Extensions that fail to load

The error names the extension's runtime log, for example `/tmp/pig-ext-<name>-<id>.log`. Read it first: a missing module or export usually means the extension needs an API that PiG does not provide yet. Report it with the log and the extension's source. To force PiG to rebuild compiled extensions, delete `~/.pig/cache/ext`.

A failed extension stops PiG at startup. `pig -ne` starts PiG without extensions. A Go extension written for an older SDK reports `Extension "<name>" was written for an older SDK`, lists each changed call, and prints a `pig extension upgrade <folder>` command. Run that command on your own extension's source folder. For an installed Package, remove it with `pig remove <source>` and ask its author for an update.

## Restart after an update

A session keeps running the executable it started from. If an update replaces or removes `pig`, or another `pig` prunes an extension cell this session uses, the next error shows `The pig installation this session runs from was removed or replaced` (or `The pig extension files this session runs from were removed or replaced`) once. Restart with the `pig --session <id>` command it prints to continue the session. See [TUI](/docs/latest/tui) for when PiG checks.

## Report a problem

Include the output of `pig verify`, your operating system and terminal, and the steps that reproduce the problem. For a difference from Pi, include the Pi version you compared against; PiG pins the release shown by `pig verify`.
