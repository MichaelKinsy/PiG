# Termux on Android

PiG does not currently publish or qualify an Android release artifact. This page records an experimental source-build path only.

Do not install the upstream npm package and call it PiG. `@earendil-works/pi-coding-agent` installs upstream Pi.

## Experimental source build

Install Git and a compatible Go toolchain in Termux:

```bash
pkg update
pkg install git golang
```

Clone PiG after the repository becomes public:

```bash
git clone https://github.com/MichaelKinsy/PiG.git
cd PiG
go build -o bin/pig ./cmd/pig
./bin/pig --version
```

Use Go 1.27.1 for this build. The modules retain Go 1.26 as their language
floor.

## Configuration

PiG uses:

```text
~/.pig
~/.pig/agent
```

Create global instructions under the PiG agent directory when needed:

```bash
mkdir -p ~/.pig/agent
$EDITOR ~/.pig/agent/AGENTS.md
```

## Limitations

Android and Termux are not release-qualified PiG targets yet. Verify:

- terminal input and keybindings;
- clipboard behavior;
- filesystem permissions;
- provider TLS and authentication;
- source extension toolchains;
- shutdown and Session persistence.

Use an operating-system boundary appropriate for sensitive data. Termux package isolation is not a substitute for reviewing extensions and model-generated commands.

## Upstream Pi

See [upstream Pi's Termux documentation](https://pi.dev/docs/latest/termux) when you want to run the TypeScript reference implementation.
