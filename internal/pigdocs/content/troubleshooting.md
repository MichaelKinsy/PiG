# Install and troubleshoot PiG

PiG publishes release archives on [GitHub Releases](https://github.com/MichaelKinsy/PiG/releases). The archive names on this page are the ones the release workflow produces. To build `pig` from source instead, follow the [Quickstart](https://pi-in-go.dev/docs/latest/quickstart/).

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

Browsers, AirDrop, and messaging apps add the `com.apple.quarantine` attribute to downloaded files. Gatekeeper then blocks an unsigned binary with a message that the developer cannot be verified. The release workflow does not sign binaries with an Apple Developer ID or notarize them yet; the release notes will state the current status. Files downloaded with `curl` or `wget` are not quarantined.

After you verify the SHA-256, remove the attribute from the binary or from the extracted archive directory:

```bash
xattr -l ./pig
xattr -d com.apple.quarantine ./pig
xattr -dr com.apple.quarantine ./pig-<version>-darwin-arm64
```

Apple silicon Macs use the `darwin-arm64` archive and Intel Macs use `darwin-amd64`. `uname -m` prints `arm64` or `x86_64`.

## Windows

Files downloaded with a browser carry the Mark of the Web, and SmartScreen can warn that the app is unrecognized. After you verify the SHA-256, run `Unblock-File .\pig.exe` in PowerShell. Microsoft Defender can flag unsigned Go binaries heuristically; verify the checksum and report a false positive at https://www.microsoft.com/wdsi/filesubmission.

Add the directory that contains `pig.exe` to `PATH`, then open a new terminal. Windows Terminal gives the best keyboard and color support; see [Windows](https://pi-in-go.dev/docs/latest/windows/).

## Linux

The release workflow builds binaries with cgo disabled, so they do not depend on the system C library. Use `linux-amd64` when `uname -m` prints `x86_64` and `linux-arm64` when it prints `aarch64`. If a download fails with `permission denied`, run `chmod +x pig`; if the file sits on a `noexec` mount such as /tmp, move it to `~/.local/bin`.

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

A real change of terminal width or height redraws the whole transcript, as Pi does. If the view jumps without a size change, set `PI_TUI_DEBUG_REDRAW=1`, reproduce the problem, and read `~/.pig/agent/pig-debug.log`: each full redraw appends its reason. For tmux settings, see [tmux](https://pi-in-go.dev/docs/latest/tmux/).

## Extensions that fail to load

The error names the extension's runtime log, for example `/tmp/pig-ext-<name>-<id>.log`. Read it first: a missing module or export usually means the extension needs an API that PiG does not provide yet. Report it with the log and the extension's source. To force PiG to rebuild compiled extensions, delete `~/.pig/cache/ext`.

## Report a problem

Include the output of `pig verify`, your operating system and terminal, and the steps that reproduce the problem. For a difference from Pi, include the Pi version you compared against; PiG pins the release shown by `pig verify`.
