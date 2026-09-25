# Quickstart

PiG is under active development. Install it on macOS or Linux with `curl -fsSL https://pi-in-go.dev/install.sh | sh`, on any supported platform with `npm install -g @pi-in-go/pig` or `go install github.com/MichaelKinsy/PiG/cmd/pig@latest`, or download an archive from [GitHub Releases](https://github.com/MichaelKinsy/PiG/releases). Windows support is a preview. To build from source instead, follow the steps below.

## Requirements

Build the `pig` executable with Go 1.27.1. Go 1.27 release binaries for
macOS require macOS 13 or later.

The complete verification suite also uses:

- Git;
- Node.js 24.19.0 and npm 12.0.2;
- Python 3.12;
- Rust 1.97.1;
- tmux on Unix.

You do not need every verification tool to run a previously built executable.

## Build from source

Clone the repository and build PiG:

```bash
git clone https://github.com/MichaelKinsy/PiG.git
cd PiG
go build -o bin/pig ./cmd/pig
./bin/pig --version
```

Do not install PiG by running an upstream Pi npm command. The package `@earendil-works/pi-coding-agent` installs Pi, not PiG.

## Install with npm

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

## Install with Go

Go 1.26 or newer installs the command without cloning the repository:

```bash
go install github.com/MichaelKinsy/PiG/cmd/pig@latest
```

Use an exact version for a reproducible installation:

```bash
go install github.com/MichaelKinsy/PiG/cmd/pig@v0.2.0
```

Go writes the `pig` executable to `GOBIN`. When `GOBIN` is empty, Go uses the
`bin` directory below the first path in `GOPATH`: `$(go env GOPATH)/bin`, which
is `~/go/bin` by default (`%USERPROFILE%\go\bin` on Windows). With an older Go
1.21 or later and the default `GOTOOLCHAIN=auto`, Go downloads a new enough
toolchain automatically. Add that directory to `PATH`, then inspect the
installed module identity:

```bash
pig version
go version -m "$(command -v pig)"
```

Update a Go installation by running `go install` again with a newer exact
version or with `@latest`. PiG does not treat a Go-installed executable as a
standalone release download, so `pig update` refuses to replace it without a
matching installation receipt.

Each release tags the root module (`v0.2.0`) and the nested Go extension SDK
module (`extensions/sdk/v0.2.0`) on the same commit; `go install` needs both.

## Start PiG

Run PiG in the project you want it to inspect:

```bash
cd /path/to/project
/path/to/PiG/bin/pig
```

PiG stores user state below `~/.pig`. Use `PIG_HOME` only when you need an isolated configuration root.

## Authenticate a provider

List login targets available in the current binary:

```bash
pig login --list
```

Start an available OAuth login:

```bash
pig login github-copilot
```

For an API-key provider, set the provider's documented environment variable:

```bash
export OPENAI_API_KEY=...
pig --model openai/gpt-4.1
```

Use `pig --help` and `pig login --help` as the authority for your binary.

## Start a first session

Type a request and press Enter:

```text
Summarize this repository and tell me how to run its checks.
```

Stock PiG gives the model its normal built-in coding tools unless the command line, settings, or an active Piglet narrows them.

## Use print mode

Run one prompt without the interactive TUI:

```bash
pig -p "Summarize this repository"
```

Select a model explicitly when needed:

```bash
pig --model github-copilot/gpt-5-mini -p "Find the test entry points"
```

## Run PiG Standard

PiG Standard is not part of bare Stock PiG startup. Select it explicitly from a source checkout:

```bash
pig --piglet piglets/standard/pig-standard.yaml
```

The current Standard composition provides its PiG login, `/sprite`, `/runner`, and `/pig-runner` through ordinary extension Resources.

## Run your own Piglet

Validate and run one agent application:

```bash
pig piglet validate ./agents/reviewer.yaml
pig --piglet ./agents/reviewer.yaml
```

Register portable Piglet source when you want to invoke it by name:

```bash
pig piglet add ./agents/reviewer.yaml
pig --piglet reviewer
```

See [Piglets](/docs/latest/piglets).

## Build a Piglet Binary

```bash
pig piglet build ./agents/reviewer.yaml \
  --format binary \
  --out ./pig-reviewer
./pig-reviewer
```

A Piglet Binary is a target-native executable for one fixed Piglet composition. It can still have explicit external requirements. See [Piglet Binaries](/docs/latest/piglet-binaries).

## Add an extension

Create and validate a Go factory:

```bash
pig extension init ./review --lang go
pig install ./review --validate-only --json
```

Run it for one session:

```bash
pig -e ./review
```

Use a Piglet when the extension belongs to a named agent application.

## Verify a source checkout

Prepare the exact Pi comparator and source mirror:

```bash
make upstream-mirror
```

Run the primary gate:

```bash
make check
```

Use the toolchain versions declared by the repository. A green build does not replace parity review, vulnerability review, artifact inventory, or release authorization.

## Next steps

- [Using PiG](/docs/latest/usage)
- [PiG concepts](/docs/latest/concepts)
- [Extensions](/docs/latest/extensions)
- [Packages](/docs/latest/packages)
- [Piglets](/docs/latest/piglets)
- [Build a derivative harness](/docs/latest/derivative-harnesses)
