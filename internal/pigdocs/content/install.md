# Install and use Pig

Use this page to get from a fresh binary to a working session. Return to the
[docs router](README.md) for focused tasks.

## 1. Build or install the binary

On macOS or Linux, install the latest release with
`curl -fsSL https://pi-in-go.dev/install.sh | sh`, or download an archive from
[GitHub Releases](https://github.com/MichaelKinsy/PiG/releases). Windows
support is a preview. From a source checkout, build it with Go 1.27.1. Go 1.27 release binaries for macOS require macOS 13 or later:

```bash
go build -o bin/pig ./cmd/pig
./bin/pig --version
```

Install the command with npm (Node.js 18 or newer):

```bash
npm install -g @pi-in-go/pig
pig --version
```

Or run it once without installing:

```bash
npx @pi-in-go/pig --version
```

`pig` itself is the native binary: npm installs the matching platform package
(`@pi-in-go/pig-<os>-<cpu>`, for macOS, Linux and Windows on x64 and arm64) as
an optional dependency, and Node.js runs only a small launcher. Do not install
with `--omit=optional` or `--no-optional`, which leaves the binary out. Update
with `npm update -g @pi-in-go/pig` and uninstall with
`npm uninstall -g @pi-in-go/pig`. `pig update` does not replace an
npm-installed binary; update through npm.

Install the command with Go 1.26 or newer, without a source checkout:

```bash
go install github.com/MichaelKinsy/PiG/cmd/pig@latest
```

Use an exact version for a reproducible installation:

```bash
go install github.com/MichaelKinsy/PiG/cmd/pig@v0.2.0
```

Go writes the `pig` executable to `GOBIN`, or to the `bin` directory below the
first `GOPATH` entry when `GOBIN` is empty: `$(go env GOPATH)/bin`, which is
`~/go/bin` by default (`%USERPROFILE%\go\bin` on Windows). Add that directory to
`PATH`. With an older Go 1.21 or later and the default `GOTOOLCHAIN=auto`, Go
downloads a new enough toolchain automatically. Update this installation by
running `go install` again. PiG does not treat a Go-installed executable as a
standalone release download, so `pig update` refuses to replace it without a
matching installation receipt.

Each release tags the root module (`v0.2.0`) and the nested Go extension SDK
module (`extensions/sdk/v0.2.0`) on the same commit; `go install` needs both.

After the project publishes a release, you can also download an artifact from
its release page. Verify the published checksum before you place `pig` on
`PATH`.

Pig stores user state under `~/.pig`. Set `PIG_HOME` only for an isolated config
root, tests, or CI.

### macOS Gatekeeper after a manual download

A browser download can add the local `com.apple.quarantine` attribute to a PiG
archive. First verify the published SHA-256 checksum. For a verified PiG binary,
run:

```bash
xattr -p com.apple.quarantine /path/to/pig
xattr -d com.apple.quarantine /path/to/pig
/path/to/pig --version
```

Do not recursively remove quarantine attributes from a broad directory.

## 2. Authenticate a model provider

```bash
pig login github-copilot
```

API-key providers may use their documented environment variable:

```bash
export OPENAI_API_KEY=...
pig --model openai/gpt-4.1
```

See [Providers](providers.md). Product extensions may contribute additional
login targets, such as a platform host, through the same `pig login` surface.
Use `pig login --help`/`pig --help` for the targets available in your binary.

## 3. Install packages

Raw Pig accepts local, git, npm, and HTTP package sources:

```bash
pig install ./my-package
pig install git:https://github.com/example/my-package.git
pig install npm:@example/package
```

An installed product extension may add another resolver scheme, such as a
private marketplace. Its authentication and catalog commands are product
features, not assumptions in raw Pig.

## 4. Run Pig

```bash
pig                              # interactive TUI
pig -p "summarize this repo"     # one-shot prompt
pig --model github-copilot/gpt-5-mini
```

## 5. Install and run a piglet

Install and validate a registerable Piglet, then invoke it by name:

```bash
pig piglet add ./piglets/my-agent.yaml
pig piglet validate my-agent
pig --piglet my-agent
```

A Piglet with relative `local:` extension or skill origins is source-bound.
Run it directly instead of registering it:

```bash
pig piglet validate ./piglets/my-agent.yaml
pig --piglet ./piglets/my-agent.yaml
```

See [Piglets](piglets.md) for schema and resolution.

## 6. Build an explicit Piglet entry point or Binary

A source script is a thin Host Piglet entry point. It is not managed by Pig and
creates no Pig state or record:

```bash
pig piglet build my-agent --format script --out ~/.local/bin/my-agent
my-agent
```

On Windows the script is a cmd.exe batch file, so give it a `.cmd` name, for
example `--out my-agent.cmd` (D69).

A Piglet Binary is a target-native executable for one immutable Piglet
composition:

```bash
pig piglet build my-agent --format binary --out ./pig-my-agent
./pig-my-agent
```

The Piglet artifact lifecycle lives under `pig piglet`.

A Piglet Binary may fuse compatible Go components and host exact subprocess
components through Pig's ordinary subprocess extension host. Its registered
Piglet release records where each component comes from: binary, release
closure, required `agentEnv`, or external mapping. Subprocess use does not create
a different binary kind.

Piglet Image is the OCI carrier when the Piglet needs an environment-complete
runtime. Artifact startup never silently installs, pulls, logs in, or substitutes
an origin.

## 7. Set up an extension toolchain only when authoring/building

You need a language toolchain to author a source extension or build its release
closure. A consumer needs no compiler for a prebuilt native component; interpreted
subprocess components still need the exact runtime recorded by the Piglet
release or supplied by `agentEnv`.

Check before installing anything:

```bash
go version
cargo --version
python3 --version
node --version
```

Prefer a version manager already present on the machine. Otherwise use the OS
package manager or the language's official per-user installer, with the user's
consent. Do not remove or replace an existing system toolchain without asking.

For a Go extension, the required version is the `go` directive in the staged SDK
module. `pig extension init` stages an offline-resolvable project and
`pig install <path> --validate-only --json` validates it.

## Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| `pig` not found | binary directory absent from `PATH` | add the install directory |
| no selectable models | no provider credential | run `pig login <provider>` or set its API key env var |
| contributed source scheme is unknown | product resolver is not installed/loaded | install or run the product distribution that owns it |
| piglet origin does not resolve | missing local source/package/product resolver | run `pig piglet validate <name>` and fix the reported origin |
| source extension fails on first use | missing language toolchain | install it with consent or use a prebuilt Piglet release/Image |
| Piglet artifact cannot start a component | registered release/environment/external requirement missing | rebuild with a supported format or follow the reported mapping remediation; do not substitute PATH content |

## Offline docs

```bash
pig docs list
pig docs show install
pig docs path
```
