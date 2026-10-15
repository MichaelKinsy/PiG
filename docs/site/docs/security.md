# Security

PiG runs with the permissions of the user who starts it. PiG is not a security sandbox for model output, tools, extensions, skills, hooks, or shell commands.

Use a container, virtual machine, or another operating-system boundary when you need isolation.

## Project trust

A repository can contain project settings and Resources that change PiG behavior. PiG requires a trust decision before it loads project-controlled executable or configuration input.

Project trust is not a complete startup boundary. As in Pi, PiG reads the project `sessionDir` setting while it selects or creates a session, before it resolves project trust. Project trust also does not limit what tool calls can access: enabled tools use the operating-system permissions of the PiG process.

Trust-sensitive project paths include:

```text
.pig/settings.json
.pig/mcp.json
.pig/extensions/
.pig/skills/
.pig/prompts/
.pig/themes/
.pig/SYSTEM.md
.pig/APPEND_SYSTEM.md
.agents/skills/
```

A bare `.pig` directory does not require project trust. Context files such as `AGENTS.override.md`, `AGENTS.md` and `CLAUDE.md` load whatever the trust decision is, unless you disable context loading. Treat instructions in a folder as untrusted input even when you decline project trust.

A Piglet in `.pig/piglets/` is also project input. `--piglet <name>` uses it only with a saved trust decision, `defaultProjectTrust: "always"` or `--approve`, and a bare name is never read from the working directory.

PiG chooses a trust decision as Pi does:

1. A command-line `--approve` or `--no-approve` applies first.
2. User-level and command-line extensions can handle the `project_trust` event. The first extension that returns yes or no owns the decision.
3. Otherwise PiG uses the saved decision for the current directory or its closest parent. Decisions are stored by canonical directory in `~/.pig/agent/trust.json`.
4. Otherwise PiG follows the global `defaultProjectTrust` setting, whose default is `"ask"`.

Print, JSON and RPC modes cannot show the trust prompt. Without an override, extension or saved decision, `defaultProjectTrust: "always"` loads the protected project files, and `"ask"` or `"never"` skips them.

Use `/trust` to manage the current project decision. The selector shows the saved decision and the current session's trust state separately. A checkmark stays on the saved choice while you browse. Select **Trust parent folder** to trust the parent and clear the current project's override. Restart PiG after changing trust so startup discovery runs under the new decision.

## Sessions and credentials

Sessions can contain prompts, tool arguments, command output, file contents and credentials that appeared in the conversation. Review a session before you export or share it.

PiG creates the agent directory, the `sessions` directory and each project's session directory with mode 0700 when it creates them. Pi creates them with the umask default, usually 0755 (D132, Pi #10770). A directory that already exists keeps its mode. `auth.json` is created owner-only (mode 0600 on POSIX, an owner-only DACL on Windows, D68).

## Extensions

Extensions execute code. A subprocess boundary can isolate crashes and support cancellation, but it is not a permission sandbox.

Before you load an extension:

- verify its source and origin;
- review its dependencies;
- inspect required commands and network access;
- understand which files and secrets it can read;
- prefer a constrained operating-system environment for untrusted code.

A fused extension shares the PiG process. Review fused code more strictly because a panic, deadlock, or resource leak can affect the whole agent application.

## Packages

A Package can distribute executable extensions and instruction Resources. Installing a Package can make those Resources available to normal discovery.

Review Package membership before installation:

```bash
pig package validate ./package
```

Validate extension behavior without changing settings:

```bash
pig install ./extension --validate-only --json
```

Package installation never activates a Piglet. A Piglet separately selects an agent application.

## Piglets

A Piglet is explicit composition, not a security boundary. It can narrow tools and discovery, declare secrets, and require an environment.

Use exact tool lists and empty discovery lists when an application must not inherit ambient Resources:

```yaml
tools: [read, grep, find]
discovery:
  extensions: []
  skills: []
```

Command-line and platform policy can narrow a Piglet. They must not silently widen it.

## Secrets

Keep secret values outside portable Piglet source, Package metadata, records, sessions, logs, command arguments, and image layers.

Use environment, owner-only files, or contributed secret resolvers. PiG validates declarations and fails before startup when a required value is missing or unauthorized.

Do not put credentials in:

- a Piglet YAML file;
- an extension source archive;
- a prompt or skill;
- a website example;
- a generated Binary record.

## Model providers

Provider credentials can grant access to paid services and private data. Use the provider's documented environment variable or PiG login store. Do not copy tokens into repository settings.

Treat model output as untrusted input. A model can propose a harmful command even when the provider connection is trusted.

## Connection failure

PiG detects extension transport and heartbeat failures within bounded lifecycle rules. It cancels owned pending calls, closes connection-owned UI state, and isolates or quarantines the affected extension or cell.

PiG does not replay an interrupted operation automatically. Automatic replay could duplicate file, shell, network, message, or transaction effects.

See [Extensions](/docs/latest/extensions#connection-and-liveness-model).

## Piglet Binary verification

A Piglet Binary verifies its embedded Piglet and component plan before command dispatch. A Binary built with `--sign-key` also verifies its Ed25519 signature offline and refuses to run when its executable, manifest, or signature changed. When its file is unchanged since its last passing start, it skips the hash of its executable bytes. See [Verification cache](/docs/latest/piglet-binaries#verification-cache) for what that keeps and gives up. The startup check is a self-check: it does not stop an attacker who can rewrite the Binary.

Use `pig verify <binary>` to report the signature status without running the Binary. Use `pig piglet trust add <key.pub>` to trust a reviewed publisher key, `pig piglet trust revoke <key-id>` to reject it, and `pig piglet trust require on` to require signatures by locally trusted keys. An embedded author key proves continuity from that key, not the human identity of its holder.

A Sigstore keyless attestation is separate build-provenance evidence. `pig verify --provenance --repo <owner/name> --signer-workflow <workflow> <binary>` delegates to `gh attestation verify` and may contact GitHub. Startup never makes that network request.

It does not silently install, download, substitute, or weaken missing requirements. Verify the release checksum, offline signature, and available provenance before execution.

A Binary can still require external services, configuration, runtimes, and secrets. Read its record instead of assuming that one file is self-contained.

## Network content

The documentation site's package and model catalogs contain third-party metadata. A listing does not prove PiG compatibility, security, or endorsement.

Review the publisher, license, source, and integrity evidence before use.

## Report a vulnerability

Do not open a public issue for an undisclosed vulnerability. Follow the private reporting instructions in the repository's `.github/SECURITY.md`. Do not publish exploit details.

## Related documentation

- [Extensions](/docs/latest/extensions)
- [Packages](/docs/latest/packages)
- [Piglets](/docs/latest/piglets)
- [Piglet Binaries](/docs/latest/piglet-binaries)
- [Containerization](/docs/latest/containerization)
- [upstream Pi's security documentation](https://pi.dev/docs/latest/security)
