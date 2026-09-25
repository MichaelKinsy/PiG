# PiG documentation

PiG is a faithful Go implementation of [Pi](https://github.com/earendil-works/pi), the reference terminal coding agent. PiG follows observable Pi behavior unless a documented divergence explains a necessary difference.

PiG is under active development. Install it on macOS or Linux with `curl -fsSL https://pi-in-go.dev/install.sh | sh`, on any supported platform with `npm install -g @pi-in-go/pig` or `go install github.com/MichaelKinsy/PiG/cmd/pig@latest`, or download an archive from [GitHub Releases](https://github.com/MichaelKinsy/PiG/releases). Windows support is a preview.

## Start here

- [Quickstart](/docs/latest/quickstart) explains how to build and start PiG.
- [Using PiG](/docs/latest/usage) covers the normal command-line and interactive surfaces.
- [PiG concepts](/docs/latest/concepts) explains Stock PiG, Resources, Packages, Piglets, and Piglet Binaries.
- [Security](/docs/latest/security) explains trust and process boundaries.
- [Providers](/docs/latest/providers) explains model authentication and configuration.

## Agent applications

Use a Piglet to define one agent application:

```bash
pig --piglet ./agents/reviewer.yaml
```

A Piglet selects extensions, skills, tools, prompts, model preferences, discovery policy, secrets, and environment requirements.

Build that application as a native executable when you need direct delivery:

```bash
pig piglet build ./agents/reviewer.yaml \
  --format binary \
  --out ./pig-reviewer
```

Read:

- [Piglets](/docs/latest/piglets)
- [Piglet Binaries](/docs/latest/piglet-binaries)
- [Build a derivative harness](/docs/latest/derivative-harnesses)

## Extend PiG

PiG extensions can use Go, Rust, Python, or Node. Source extensions normally use the subprocess host. Compatible Go factories can be fused into a Piglet Binary.

Create and validate a Go extension:

```bash
pig extension init ./review --lang go
pig install ./review --validate-only --json
```

Read:

- [Extensions](/docs/latest/extensions)
- [Packages](/docs/latest/packages)
- [Skills](/docs/latest/skills)
- [Themes](/docs/latest/themes)

## Stock PiG and PiG Standard

Stock PiG contains the Pi-compatible engine and generic extension, Package, Piglet, Binary, and documentation machinery. It does not activate product extensions or PiG artwork.

PiG Standard is an explicit Piglet composition. It uses ordinary extension and Piglet contracts. Stock PiG never selects it automatically.

Run Standard from a source checkout:

```bash
pig --piglet piglets/standard/pig-standard.yaml
```

PiG Standard requires every selected extension to be compiled into its Piglet Binary.

## Project facts

| Item | Value |
|---|---|
| Executable | `pig` |
| Module | `github.com/MichaelKinsy/PiG` |
| User configuration root | `~/.pig` or `PIG_HOME` |
| Agent directory | `~/.pig/agent` or `PIG_CODING_AGENT_DIR` |
| Pinned Pi release (behavior oracle) | Pi 0.87.1 |
| License | MIT |

## Upstream Pi

Pi remains the reference implementation. PiG is not an official Pi release and does not imply endorsement by Pi's maintainers.

Use the upstream project when you need Pi itself:

- [Pi source](https://github.com/earendil-works/pi)
- [Pi documentation](https://pi.dev/docs/latest)
- [Pi community](https://discord.com/invite/3cU7Bz4UPx)

The PiG website preserves upstream attribution and labels upstream commands and TypeScript APIs when it presents them for comparison.
