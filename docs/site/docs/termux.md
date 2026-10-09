# Run PiG on Android with Termux

PiG runs on Android through [Termux](https://termux.dev/), a terminal emulator and Linux environment. Text input, file tools, and shell commands are supported. PiG copies text to the Android clipboard with Termux:API. Clipboard image paste is not supported.

Termux runs PiG's `android-arm64` release binary: a position independent executable linked to Android's C library. Do not use the `linux-arm64` binary in Termux; Termux's loader cannot start it (`has unexpected e_type: 2`). Android on other CPUs (32-bit Arm, x86) has no release binary. No hosted CI runner executes PiG on Android, so treat Termux support as experimental until a device run is recorded in the release evidence.

## Before you begin

Install Termux from [GitHub, F-Droid or Google Play](https://github.com/termux/termux-app#installation). Android does not let an app run files from its own data directory. Termux works around that with `termux-exec`, which starts each program through `/system/bin/linker64`; PiG does the same for the programs it starts, because its Go runtime bypasses `termux-exec`.

[Termux:API](https://github.com/termux/termux-api#installation) is optional. Install it only when you want PiG to copy Android clipboard text, or when shell commands need Android device APIs.

## Install PiG

1. Update Termux packages:

   ```bash
   pkg update && pkg upgrade
   ```

2. Install curl and Git:

   ```bash
   pkg install curl git
   ```

3. Install PiG. The installer detects Termux, downloads the `android-arm64` release, verifies its SHA-256, and installs `pig` into `$PREFIX/bin`:

   ```bash
   curl -fsSL https://pi-in-go.dev/install.sh | sh
   ```

   To install with npm instead, install Node.js and then PiG. Node.js reports its platform as `android` in Termux, so npm selects `@pi-in-go/pig-android-arm64`:

   ```bash
   pkg install nodejs
   npm install -g @pi-in-go/pig
   ```

4. Verify the installation:

   ```bash
   pig --version
   ```

5. Open the folder you want to work in and start PiG:

   ```bash
   cd /path/to/working-folder
   pig
   ```

Continue with the main [Quickstart](quickstart.md#3-choose-a-model) to connect a model and run your first task.

Update a script installation with `pig update`. Update an npm installation with `npm update -g @pi-in-go/pig`.

## Find ripgrep and fd

PiG does not download `rg` or `fd` on Android, because the Linux release binaries it would fetch do not run on Android's C library. It tells you to install them from Termux:

```bash
pkg install ripgrep fd
```

## Access Android shared storage

Termux cannot access shared Android storage until you grant permission. Run this once:

```bash
termux-setup-storage
```

After approval, Android shared storage is available under `/storage/emulated/0` and through the links Termux creates under `~/storage/`.

Only grant this permission when PiG should be able to access those files. Commands and tools running in Termux use the same storage permissions as the Termux process.

## Use clipboard commands

PiG uses `termux-clipboard-set` to copy text. Shell commands can use `termux-clipboard-set` and `termux-clipboard-get` directly. Install the Termux:API app and its command-line package:

```bash
pkg install termux-api
```

Verify the integration:

```bash
printf 'PiG clipboard test' | termux-clipboard-set
termux-clipboard-get
```

The second command should print `PiG clipboard test`.

The Termux clipboard API supports text only. PiG's clipboard-paste shortcut reads no clipboard on Android, as in Pi, whose paste command runs only where Node reports `linux`, and it attaches no clipboard images. Paste text with Termux's own paste gesture.

## Networking, temporary files, and programs PiG starts

The Android build links Android's C library, so it resolves names through Android and trusts Android's system certificates. In Termux it also sets two variables when you have not: `SSL_CERT_FILE` to `$PREFIX/etc/tls/cert.pem`, and `TMPDIR` to `$PREFIX/tmp`. Commands PiG starts inherit them. Set either variable yourself to use another value.

When Termux starts PiG through `/system/bin/linker64`, PiG starts each program below Termux's data directory the same way. It runs a script through its interpreter and reads `/bin` and `/usr/bin` in a `#!` line as `$PREFIX/bin`. `pig update` and PiG's own re-launches find PiG's path through `TERMUX_EXEC__PROC_SELF_EXE`, because `/proc/self/exe` is the linker in such a process.

## Add Termux-specific instructions

PiG detects that it is running in Termux, but it cannot infer how you want it to interact with Android. Add only the environment details relevant to your work to `~/.pig/agent/AGENTS.md`:

````markdown
# Termux environment

- PiG runs in Termux on Android.
- Shared Android storage is under `/storage/emulated/0`.
- Open URLs with `termux-open-url "https://example.com"`.
- Open files with `termux-open <path>`.
- Do not access shared storage unless the task requires it.
````

Run `/reload` after changing the file during an active session.

## Build Piglets

`pig piglet build` uses Termux's own toolchains. `pig setup go` cannot download Go for Android, so install Go with `pkg`:

```bash
pkg install golang clang
```

Install Rust only when a Piglet has Rust extensions:

```bash
pkg install rust
```

`pig setup` prints which build toolchains PiG finds.

- `--format script` writes a launcher script.
- `--format binary` builds a native Android Piglet Binary. A release PiG fetches its own source on the first build, so that build needs network access and takes a few minutes. Later builds reuse the cache.
- A Piglet Binary embeds Go and Rust extensions. A Piglet with no extensions, with only Python extensions, or with any Node extension builds only as a script.
- The container builder does not run in Termux. Termux has no Podman or Docker, and the container builder builds only Linux targets, not the Android host.

Keep about 2 GB free for the first Binary build. Go's module and build caches and PiG's build cache use about 1.3 GB.

## Build from source

Termux's Go toolchain builds PiG for Android with cgo:

```bash
pkg install git golang
git clone https://github.com/MichaelKinsy/PiG.git
cd PiG
go build -o "$PREFIX/bin/pig" ./cmd/pig
pig --version
```

Go 1.27.2 is recommended for this build. Go 1.27.1, the release Termux packages, also builds PiG: the `toolchain` line in `go.mod` stays at go1.27.1 so that `GOTOOLCHAIN=auto` does not request a `go1.27.2.android-arm64` toolchain that Go does not publish. The modules retain Go 1.26 as their language floor.

## Troubleshooting

### PiG is not found after installation

The script installer writes `$PREFIX/bin/pig`, which is on Termux's `PATH`. For an npm installation, open a new Termux shell and run:

```bash
npm prefix -g
command -v pig
```

Confirm that the global npm binary directory is on `PATH`, then reinstall PiG if the package is missing. Do not install with `--omit=optional`, which leaves the native binary out.

### The installer reports an unsupported CPU

Run `uname -m`. PiG publishes arm64 only; `aarch64` is that CPU. A 32-bit Termux (`armv7l` or `armv8l`) or an x86 Android device has no release binary. Build from source instead.

### Clipboard integration fails

Confirm that you installed both components:

1. The Termux:API Android app from the same source as Termux
2. The `termux-api` command-line package

Then run the clipboard verification commands above outside PiG. If they fail there, fix the Termux:API installation before retrying PiG's copy command.

### A Piglet build reports `link ... permission denied`

PiG 0.4.1 and earlier write Piglet scripts, Piglet Binaries, and added Piglets with hard links, and Android does not allow hard links in Termux's data directory. The build fails at its last step with `commit ...: link ...: permission denied`. Update PiG to a release that includes the fix.

### `pig update` reports `preserve current executable for rollback: link ... permission denied`

PiG 0.4.1 and earlier keep a rollback copy of the running `pig` with a hard link before they replace it, and Android does not allow hard links in Termux's data directory. The update stops before it changes anything. Such a release cannot update itself in Termux, so update a script installation by running the installer again:

```bash
curl -fsSL https://pi-in-go.dev/install.sh | sh
```

Later releases copy the running `pig` where the hard link is refused, and `pig update` works.

### Shared storage reports permission denied

Run `termux-setup-storage`, approve the Android permission request, and retry the path under `~/storage/` or `/storage/emulated/0`.

### PiG fails with `has unexpected e_type: 2`

You installed the `linux-arm64` binary, a static executable that `/system/bin/linker64` cannot map. Reinstall with the installer or `npm install -g @pi-in-go/pig`, which choose `android-arm64` in Termux.

### `pig install` fails with `fork/exec ... permission denied`

PiG 0.4.0 started `npm` and `git` for `pig install` and `pig remove` directly, which Android rejects for a file below Termux's data directory. PiG 0.4.1 starts them through `/system/bin/linker64` like every other program. Upgrade; if you installed a package that failed, run `pig remove` for it, or delete its entry from the `packages` list in `~/.pig/agent/settings.json`. PiG 0.4.1 records a package only after it installs, as Pi does.

### `pig setup go` reports no archive for android/arm64

Go publishes no archive for Android. Run `pkg install golang`; PiG uses the `go` on `PATH`, and `pig setup status` lists it.

### Validating a Rust extension stops after two minutes

Set `PIG_CELL_BUILD_TIMEOUT` to a longer duration, such as `30m`. It bounds both extension builds and `pig install <dir> --validate-only`.

## Upstream Pi

See [upstream Pi's Termux documentation](https://pi.dev/docs/latest/termux) when you want to run the TypeScript reference implementation.
