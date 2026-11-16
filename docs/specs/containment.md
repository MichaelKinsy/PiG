# Opt-in containment of what the agent can touch

Status: draft for owner review, 2026-11-16. No production code exists for this specification. Phase 2 (`Order of work`) starts only after the owner approves this text and allocates the record number. The record is written `D<N>` below (`Open questions`). `Review record` lists what was decided during review.

Upstream reference (Pi 1.5.0, tag `v1.5.0`, commit `f9a26365`, read from `earendil-works/pi` at that tag):

- `SECURITY.md`: "the Pi coding agent intentionally does not have a sandbox"; the user contains it "within a container, virtual machine or other Sandbox solution".
- `packages/coding-agent/docs/containerization.md`: whole-process isolation (plain Docker, Docker Sandboxes, OpenShell) and tool-only isolation (Gondolin). It states the limit this design removes: "Tool-only isolation does not constrain the host Pi process or extension tools that do not use the isolated backend."
- `packages/coding-agent/examples/extensions/sandbox/` is not in Pi 1.5.0. Pi shipped that example through 1.4.0 and removed it in commit `b4c2e92ac` (2026-10-31, "fix: remove vulnerable dependency chains": "retire the native sandbox example in favor of existing isolation options"). The pinned Pi therefore offers containers and the Gondolin example, and nothing that sandboxes a command on the host. As it stood in 1.4.0, the example wrapped `bash` and `user_bash` with `@anthropic-ai/sandbox-runtime` 0.0.26. Project `.pi/sandbox.json` overrode the global file (`deepMerge(deepMerge(DEFAULT_CONFIG, globalConfig), projectConfig)`). A failed initialization, an unsupported platform or `--no-sandbox` left `sandboxEnabled = false`, and the commands ran unsandboxed after a notification. This design keeps none of those three properties.
- `packages/coding-agent/examples/extensions/bash-spawn-hook.ts` (`spawnHook`), `protected-paths.ts`, `permission-gate.ts` (`tool_call` gates an extension can register and another extension or a tool override can bypass), `gondolin/`, `project-trust.ts`.

Related PiG work: issue #118 (PiG running inside someone else's sandbox: detection and fallbacks) is a different subject. No issue or pull request covers containment.

File and line references in this record are to `main` at `bc46a1395`.

## Decision

Stock PiG gains one inert capability: a containment policy. A process that has no policy behaves exactly as Pi does. A process that has one confines every action the agent can cause to the policy's roots, through two enforcement points that share one policy:

1. A path policy inside the PiG process, for the built-in file tools.
2. An operating-system sandbox around every child process PiG starts for the agent.

The policy has at most one source per process: the selected Piglet or the launcher. PiG resolves it once, at startup, against the directory it was started in. Nothing that runs later can change it, and no session replacement moves it. The first release has no way out of the boundary from inside PiG. A later step adds a host-drawn approval for one exact command in interactive mode, which stays off unless the policy turns it on (`Human widening`).

PiG never falls back to running unconfined. When the host cannot enforce the policy, the process fails at startup.

## 1. Classification (AGENTS.md product-extension boundary)

| Part | Class | Reason |
|---|---|---|
| Policy document: parser, validator, variable resolution, digest | required substrate | The verifier of a boundary cannot be supplied by what it confines. |
| Path policy for the built-in file tools | required substrate, selected as an inert capability | It is the isolation boundary for in-process tools. |
| Sandbox wrap of child processes (Seatbelt, Landlock and seccomp, bubblewrap argv, wrapper prefix) | required substrate, selected as an inert capability | "A Piglet cannot supply the ... isolation boundary." |
| Network modes and the byte forwarder | inert capability | Mechanism only. PiG does not filter traffic. |
| Host-drawn single-command approval (a later step) | inert capability | The trusted path to the human must be host code. |
| Piglet `containment` field, baking, digest, overlay refusal | required substrate | Piglet parsing and verification are Stock. |
| The policy values: roots, deny lists, toolchain and cache paths, proxy address, which extensions run outside, environment allowlist, approval on or off | policy: a Piglet, a Package's documentation, PiG Standard, or the embedder's launcher | Stock ships no opinionated policy and selects none. |
| Domain filtering proxy, per-tool allowlists, presets such as "workspace-write", a status-bar indicator | not Stock | Product behaviour. Not designed here. |

The capability is an **inert capability**, not an "explicit unsafe opt-in". Selecting it removes authority from the agent. It is off by default, a Piglet or the launcher selects it, and Stock PiG contains no code path that selects it by itself. The unsafe direction is not a setting: no configuration widens a policy.

Four values inside a policy weaken the boundary. Each is visible in the policy document, and PiG Standard must not select them: `sandbox: "none"`, `sandbox: {"wrapper": ...}` (PiG cannot verify an embedder's wrapper), `extensions.outside`, and `extensions.allowUnmediatedUnixSockets`. They follow the "explicit unsafe opt-in" rule (off by default, visible at the configuration boundary). `sandbox: {"mxc": ...}` on Windows does not weaken the boundary, and is explicit for another reason: it runs a third-party executor whose official builds send diagnostic data to their vendor (`Telemetry of the Windows executor`).

## 2. Threat model

The adversary is the model's output: tool calls and the commands, scripts and files it produces, including output steered by prompt injection. The adversary is not a local attacker and not the human.

Containment protects against:

- reads outside the read roots and writes outside the write roots, by any tool, command, interpreter, subshell, symbolic link, hard-link creation, `/proc` or `/dev/fd` path, extension tool or MCP server inside the boundary;
- reads of credential files: PiG's own (`auth.json`, `models.json`, `mcp.json`, sessions) always, and the policy's `deny` entries;
- network egress from child processes: none, or only through one configured proxy;
- launching programs outside the boundary: GUI applications (`open`, `osascript`, desktop and session buses), and typing into the terminal (`TIOCSTI`);
- provider credentials in the environment of child processes;
- an agent that edits the configuration PiG would later load or install outside the boundary (`Self-carried escapes`).

Containment does not protect against:

- prompt injection inside the workspace, and anything the agent can do within its roots, including damage to the workspace;
- workspace poisoning for a later, uncontained actor: a git hook, a Makefile or a script that the human or another tool runs outside the boundary;
- the PiG process itself and the code it links: compiled-in extensions and fused Piglet members (D31) run with the host's authority;
- provider credentials in PiG's memory, and requests PiG's own process makes (providers, MCP over HTTP, tool downloads);
- extensions the policy places outside the boundary, and an embedder's wrapper, which PiG cannot verify;
- an extension that misuses the extension API. A host call that an extension makes (`registerProvider`, `setModel`, `sendUserMessage`, a session replacement) runs in the PiG process. Containment confines the host calls that touch files or start processes, and treats an extension inside the boundary as contained code, not as an adversary of the API;
- a file that already has a hard link inside a root (it is in the root by definition; measured readable on both platforms);
- kernel and sandbox implementation defects, covert channels, and resource exhaustion;
- diagnostic data that a third-party executor sends to its vendor (`Telemetry of the Windows executor`);
- on the Landlock backend: observing that other processes exist (no PID namespace), signals to the user's other processes below ABI 6, and, below ABI 9, the Unix sockets of an extension process when the policy sets `extensions.allowUnmediatedUnixSockets`.

### Update to `docs/threat-model.md`

Replace the second sentence of "User process boundary":

> PiG runs with the user's operating-system permissions. Without a containment policy, PiG does not isolate model output or built-in tools from the user account. With one (`docs/specs/containment.md`, D<N>), the built-in file tools and every child process PiG starts for the agent are confined to the policy's roots and network mode. The PiG process, its compiled-in and fused extensions, and its provider credentials stay outside that boundary.

Add to "Extension boundary":

> Under a containment policy an extension process and an MCP stdio server run inside the boundary unless the policy names the extension in `extensions.outside`.

Add rows to "Threats and controls":

| Threat | Control |
|---|---|
| Model output reads or writes outside the workspace, reads credentials, or reaches the network | Opt-in containment policy: identity-based path policy for file tools, OS sandbox for agent child processes, network off or proxy-only; fail closed when unenforceable |
| A project, the agent, an extension, a settings change or a resumed session widens the boundary | One immutable policy per process (Piglet or launcher), resolved at startup against the launch directory; no setting, command, extension API or RPC command changes it; a session whose working directory is outside the read roots is refused; nothing inside PiG widens it in the first release |
| The agent rewrites configuration that PiG loads outside the boundary | Sources that PiG executes or installs outside the boundary must not be writable inside it; otherwise they are not loaded |
| A contained process types into the terminal: user input, or later the answer to an approval | Terminal devices are never granted; children get pipes; host prompts are read by the host's input owner only |

"Review triggers" already lists "sandbox or process isolation".

Also update `docs/site/docs/security.md` ("It is not a sandbox", and rows C1, C3a and C3b of "Enterprise deployment") and regenerate its mirror.

## 3. Policy document

One JSON object. The Piglet field carries the same object in YAML.

```json
{
  "read": ["${workspace}", "/opt/toolchains"],
  "write": ["${workspace}"],
  "deny": ["${workspace}/.env"],
  "readOnly": ["${workspace}/.git/hooks"],
  "network": "off",
  "sandbox": "builtin",
  "extensions": { "outside": [] },
  "env": ["PATH", "HOME", "LANG", "TERM"]
}
```

| Field | Values | Default when a policy exists |
|---|---|---|
| `read` | Directories or files the agent may read. A write root is readable. | `["${workspace}"]` |
| `write` | Directories the agent may write. | `["${workspace}"]` |
| `deny` | Paths inside the roots with no access. | none |
| `readOnly` | Paths inside the write roots with no write access. | none |
| `network` | `"off"`, `"on"`, or `{"proxy": "http://host:port"}` | `"off"` |
| `sandbox` | `"builtin"`, `"bubblewrap"`, `{"mxc": "<absolute path of wxc-exec.exe>"}`, `"none"`, or `{"wrapper": ["program", "arg"]}` | `"builtin"` |
| `extensions.outside` | Extension source references, as written in the Piglet, `-e` or settings | none |
| `extensions.allowUnmediatedUnixSockets` | `true` or `false`. It matters only on Landlock below ABI 9 (`Unix sockets below ABI 9`). | `false` |
| `env` | Names of environment variables children may inherit | inherit all except provider credential variables |

A later step adds `approval` (`"never"` or `"interactive"`, default `"never"`) together with the approval tools (`Human widening`). Until then the field does not exist, and the closed schema rejects it.

Variables:

- `${workspace}`: the directory PiG was started in. PiG binds it once per process. A session replacement does not move it (`Session working directory` below).
- `${home}`: the home directory.
- `${tmp}`: a private temporary directory that PiG creates once per process, exports to children as `TMPDIR`, writes its tool-output spill files into, and removes at exit. It is always a write root.

No other expansion, no globs, no relative paths. The schema is closed: an unknown field fails.

On Linux the example above needs bubblewrap, because it has `deny` and `readOnly` entries (`Backend selection on Linux`).

Fixed rules the policy cannot lift:

- **Baseline.** A platform baseline of read-only paths is always added, so that programs can start. Decision (2026-11-16, `Review record`): it holds only what the operating-system vendor installs and protects. Nothing that a user or a third-party installer can write is in it.
  - macOS: `/System`, `/bin`, `/sbin`, `/usr` without `/usr/local`, `/Library/Developer/CommandLineTools`, `/Library/Apple`, the active developer directory (`xcode-select -p`) with `/Library/Preferences/com.apple.dt.Xcode.plist`, `/private/etc`, `/private/var/db/timezone`, `/private/var/select`.
  - Linux: `/usr`, `/bin`, `/sbin`, `/lib*`, `/etc`, and the process's own `/proc/self`.
  - Both: `/dev/null`, `/dev/zero`, `/dev/random`, `/dev/urandom`, and PiG's own tool directory (`bin` in the agent directory, where `fd` and `rg` are installed).
  - Not in it: `/usr/local` on macOS, `/opt/homebrew`, `/nix/store`, `/Library` as a whole, a home-directory version manager, `~/.gitconfig`, module caches. They are policy: the embedder adds them to `read` or `write`.

  Reasons, with measurements on macOS 27.2.1. The lists above are measured to be enough for the commands named here; phase 2 trims them by test.
  - The baseline is a rule the policy cannot lift, so every entry is forced on every embedder.
  - A Homebrew prefix belongs to the user and holds service state beside programs: on the measurement host `/opt/homebrew` is owned by the user, and its `var` holds `db`, `lib`, `log` and `run`. On an Intel Mac that prefix is `/usr/local`, so `/usr/local` is taken out of `/usr`.
  - With this baseline `ls`, `cat`, `grep`, `awk`, `sed`, `find`, `tar`, `curl`, `perl` and `zsh` run. Apple's `git`, `python3`, `clang` and `make` are shims that load the active developer directory. Without it they fail (`xcrun: error: unable to load libxcrun`). With it and without the licence file they fail (`You have not agreed to the Xcode license agreements`). With both they run, `git init`, `add`, `commit` and `log` included.
  - A Homebrew `node` outside the read roots fails with a loader error (`Library not loaded: @rpath/libnode...`), not with "command not found". The next rule exists for that reason.
  - `git` exits with an error when `~/.gitconfig` exists and is unreadable (measured), so a policy for git work lists it or sets `GIT_CONFIG_GLOBAL` through its launcher.
- **Programs PiG starts.** At startup PiG checks that the programs it will start itself lie inside the read roots: the shell Pi resolves, `fd`, `rg`, and the runtime executable of each extension that runs inside the boundary. A program outside them is a startup error that names the path to add to `read`.
- **PiG's own files.** `auth.json`, `models.json`, `mcp.json`, the session directory and the policy file are denied. The agent directory and `PIG_HOME` are never writable.
- **Terminal devices** are never granted.
- **Session working directory.** A session whose working directory is outside the read roots is refused: at startup with exit status 1, and at a session replacement with an error that leaves the current session in place. Pi follows the working directory a session file stores: `--session` and `--resume` start in it (`coding/cli/startup_session.go`), and `SwitchSession` rebuilds the runtime for it (`coding/runtime_replacement.go`, `agent-session-runtime.ts:196-222`). If `${workspace}` followed the session, resuming a session file would move the boundary to whatever directory the file names. The boundary does not depend on the refusal: with `${workspace}` fixed, a session elsewhere could reach nothing. The refusal replaces a session in which every call is denied with one clear error.
- **Validation** rejects a root that is not absolute after expansion, a root at or under `/dev`, `/proc` or `/sys`, a `deny` or `readOnly` entry outside every root, a proxy URL with user information, `sandbox: "bubblewrap"` off Linux, and `sandbox: {"mxc": ...}` off Windows or with a relative path.

The resolved policy (variables expanded, symbolic links in roots resolved, lists sorted) has a SHA-256 digest. The digest identifies the policy in events and in the extension cell key. It is the same for every session of one process.

## 4. Layers

### a. Path policy for built-in file tools

Why: `read`, `write`, `edit`, `ls`, `find` and `grep` run inside the PiG process. No sandbox around a child process covers them, and on Windows this layer is the only one PiG implements itself.

Where: every tool resolves its argument with Pi's `resolveToCwd` or `resolveReadPath` (`internal/codingagent/tools/path_utils.go`), which is lexical, and then performs I/O through a Pi-shaped operations struct (`ReadOperations`, `WriteOperations`, `EditOperations`, `LsOperations`, `FindOperations`, `GrepOperations`). When a tool has no operations of its own, it falls back to the local file system inside the tool. Containment replaces that fallback. The tools, their schemas and Pi's path resolution do not change.

No unguarded path: the file tools are constructed at six places (`coding/session_tool_registry.go:258`, `internal/codingagent/reload_resources.go:130`, `internal/codingagent/interactive_commands.go:316`, `coding/extension/host/subprocess/host_builtin_tool.go:440` for a built-in tool that an extension creates, `coding/tool_definitions.go` for the public constructors, `coding/cli/diagnose.go:119`). So the policy does not depend on each constructor passing it. It travels with the session's services and with the context of each tool call, as the working directory already does (`toolCWD`, `internal/codingagent/tools/context_cwd.go`). When the process has a policy source, the local fallback of every file tool and the wrap of layer b refuse a call that carries no policy. A call path that someone forgets fails loudly. It cannot run unconfined. Operations that an extension supplies run in the extension process, which layer b confines.

How each operation decides:

1. Take the absolute path the tool produced. `..`, absolute paths, `~`, `@`, `file://` and Windows shell paths are already reduced by Pi's resolution.
2. Resolve symbolic links on the longest existing ancestor (`filepath.EvalSymlinks`) and append the remaining components. This covers a write target that does not exist yet. On Windows this also resolves junctions and 8.3 names.
3. Deny process-handle paths outright: `/dev/fd`, `/dev/stdin`, `/dev/stdout`, `/dev/stderr`, and `/proc/<any>/{fd,root,cwd,exe,environ,mem,maps,map_files,task}`. In the PiG process these name PiG's own objects, not the agent's. `/proc/self/root/etc/passwd` resolves to `/etc/passwd` in step 2 and fails step 4.
4. Find the root by file identity, not by spelling: walk the resolved path's ancestors and compare each with each root using `os.SameFile` (device and inode, or volume and file index). This is correct for case-insensitive volumes, Unicode normalization, macOS firmlinks (`/System/Volumes/Data/...`), drive-letter case, `\\?\` and UNC aliases of one directory, and bind mounts. A spelling that PiG fails to match is denied, never allowed.
5. Apply `deny` and `readOnly` by the same identity walk for existing entries, and by parent identity plus name (case-folded on macOS and Windows) for entries that do not exist yet.
6. Perform the I/O through an `os.Root` opened on the matched root, with the root-relative resolved path. The bash tool runs in parallel with file tools, so a command can replace a directory with a symbolic link between the check and the open. `os.Root` refuses a link that leaves the root, so the race fails closed.

`find` and `grep` start `fd` and `rg`. Layer b wraps them. Their search directory passes steps 1 to 5 first, so the model gets the explicit denial.

Knowingly not covered: a file with an existing hard link inside a root; device files and `/proc` special files inside a root that the policy itself lists (`os.Root` does not stop them, so validation rejects roots at or under `/dev`, `/proc` and `/sys`).

### b. OS sandbox for every child process PiG starts for the agent

Why: `bash`, `powershell`, `!` commands, interpreters, extension tools and MCP servers reach the file system and the network through the kernel, not through PiG. Only the kernel can confine them and their descendants.

One function wraps an `exec.Cmd` for a resolved policy. In a process without a policy source it returns the command untouched. In a process with one, a call that carries no resolved policy is an error (`No unguarded path`). It is called at the innermost spawn point, after any extension `spawnHook` or replacement operations, so no hook can remove it:

| Site | Covers |
|---|---|
| `tools.LocalShellOperations.Exec` (`bash_operations.go:127`) | `bash` and `powershell` tools, `!` and `!!` commands (`coding/session.go:2115`, `interactive_turn.go:627`), the RPC `bash` command, shell tools an extension creates (`host_builtin_tool.go:691`) |
| `find.go:134`, `grep.go:257` | `fd`, `rg` |
| `coding/extension/exec.go:161` | `pi.exec` |
| `process_tree_unix.go:27`, `process_tree_windows.go:87` | extension processes: Node, Bun, Python, Go, Rust and Lisp hosts, isolated and packed cells |
| `mcp/stdio.go` | MCP stdio servers. They talk over pipes, so they get the profile of a tool command, not of an extension process. |

Outside the boundary, unchanged, because the host or the human starts them and the model supplies no argument: clipboard, external editor, browser for login, the status-line `git symbolic-ref`, the tmux probe, the trash command, `@` completion `fd`, `!command` configuration values, self-update, Package installation, extension and Piglet builds. `chord/host`, `chord/nextnode/workers`, `durable/env/node` and `env` (SSH) are not linked into `cmd/pig`. Codemode runs in-process in wazero with no file-system configuration and no process spawn; a script reaches the host only through tool calls, which layers a and b confine.

**Extension processes run inside the boundary.** An extension tool is model-driven code, and Pi's own documentation names host-side extension tools as the gap of tool-only isolation. PiG adds, per extension process: read access to the extension's source, its built artifact and the staged SDK directory, and access to its own host socket. A runtime installed under the home directory comes from the policy's `read` list. On Landlock below ABI 9 an extension process inside the boundary needs an explicit choice (`Unix sockets below ABI 9`). Exceptions:

- `extensions.outside` names extensions that run as today (for example a provider extension that needs credentials, or one that starts subagents). The embedder owns that trust decision.
- Compiled-in extensions and fused members (D31) are host code. Their `pi.exec` calls and the shell tools they create still pass through the wrapped sites.
- Extension builds and Package installs run outside the boundary. See `Self-carried escapes`.

Packed cells: placement and policy digest join the cell plan key, so inside members and outside members never share a process.

**macOS: Seatbelt.** `sandbox-exec -p <profile> <argv>` with `(deny default)`. The profile allows process execution, `sysctl-read`, the read and write roots as `subpath` rules, `deny` and `readOnly` as later deny rules, the few mach services a command-line program needs (user lookup, logging, notifications), and nothing else. Not allowing LaunchServices and Apple Events is what stops GUI launches: a program started by `launchd` would be outside the sandbox. Measured on macOS 27.2.1 with a prototype profile: `ls ~`, `cat ~/.ssh/known_hosts`, `cd ..`, writes to `/tmp`, reads and writes through an outside-pointing symbolic link, `ln /etc/hosts`, `python3 -c 'open(...)'`, `/dev/fd`, `curl` and `git -C <other repository>` fail; `open -a Calculator` and `osascript` fail and nothing launches. `sandbox-exec` is marked deprecated in its manual page and is present and working on macOS 27.2.1. PiG probes it at startup and fails closed without it.

**Linux: Landlock and seccomp (default).** No root, no extra binary, and it works under Docker's default seccomp profile (measured). Go cannot run code between `fork` and `exec`, so PiG re-executes its own executable under a reserved internal verb; that helper locks its thread, sets `no_new_privs`, applies the Landlock ruleset and a seccomp filter, and `execve`s the command. The PiG process itself is never restricted, so no all-threads syscall library is needed. PiG probes the Landlock ABI number, never the kernel version. The kernel column is read from `security/landlock/syscalls.c` at each release tag:

| Landlock ABI | First kernel | Used for | Without it |
|---|---|---|---|
| 1 | 5.13 | read, write, execute, create, remove beneath the roots | fail closed |
| 2 | 5.19 | rename and hard link between directories inside the roots (`REFER`); ABI 1 denies them all | fail closed, because ABI 3 is the minimum |
| 3 | 6.2 | `truncate` | fail closed: the kernel always allows truncation below ABI 3 |
| 4 | 6.7 | TCP bind and connect denied (defence in depth under seccomp) | seccomp alone |
| 5 | 6.10 | device `ioctl` | no device is granted anyway |
| 6 | 6.12 | signals and abstract Unix sockets limited to the sandbox | signals reach other processes of the user; abstract sockets fall under the next row |
| 9 | 7.1 | pathname Unix sockets (`RESOLVE_UNIX`) | `Unix sockets below ABI 9` |

ABI 7 (6.15), 8 (7.0), 10 (7.2) and 11 (7.3) add nothing this design needs. The minimum is ABI 3.

The seccomp filter returns `EPERM` for `socket()` outside `AF_UNIX` and for `io_uring_setup` (it creates sockets without the `socket` syscall). Landlock checks the opened object's position in the hierarchy, so `/proc/self/root/...` and symbolic links cannot leave the roots (measured). A file rule may carry file rights only (the prototype got `EINVAL` otherwise).

Landlock alone has four limits, and the design states what happens at each:

- It is allow-only. It cannot remove a path from an allowed tree. A policy with `deny` or `readOnly` entries needs bubblewrap; see `Backend selection on Linux`.
- Before ABI 9 it does not mediate pathname Unix sockets. See `Unix sockets below ABI 9`.
- Its TCP rules name a port, not an address, so it cannot express "only the proxy". Proxy mode needs a network namespace.
- No PID namespace: other processes' `/proc/<pid>` entries are unreadable only because `/proc` is not a root.

Two consequences on this backend are worth knowing before a policy is written. With `network: "off"`, a build that needs loopback (a Gradle daemon, a local test server) fails, because the host's loopback is not granted; under bubblewrap the sandbox has its own. And below ABI 9, Python 3.14 and later fail to start `multiprocessing` workers in a tool command: their default start method became `forkserver` (Python's documentation for 3.14), which opens an `AF_UNIX` socket (`Lib/multiprocessing/forkserver.py`).

**Unix sockets below ABI 9.** A pathname Unix socket is a way out of a file-system boundary: Docker's socket, the session bus (`systemd-run --user` starts a process outside the sandbox) and the display server all sit behind one. Measured with the prototype on kernel 7.2.12 (ABI 10):

| Case | Result |
|---|---|
| Landlock that does not handle `RESOLVE_UNIX` (what every kernel below 7.1 does), socket outside every root | the connect succeeds |
| Landlock that handles `RESOLVE_UNIX`, same socket | `connect: permission denied` |
| No `RESOLVE_UNIX`, seccomp denies `socket(AF_UNIX)` | `socket: operation not permitted` |
| The same filter, `socketpair` | works |
| `RESOLVE_UNIX` handled, socket inside a write root | the connect succeeds |
| `RESOLVE_UNIX` handled, socket under a read-only root | `connect: permission denied`: the rule on the host socket's directory must grant `RESOLVE_UNIX` |

Kernel 7.1 is a 2026 release, and distribution kernels trail the mainline (Ubuntu 24.04 started at 6.8, Debian 13 at 6.12). So the rule below ABI 9 is the common case for now, not the exception.

- From ABI 9: every process inside the boundary may use Unix sockets. Landlock limits pathname sockets to the write roots and, for an extension process, to its own host socket. Abstract sockets stay inside the sandbox (ABI 6).
- Below ABI 9, tool commands and MCP stdio servers: the seccomp filter also denies `socket(AF_UNIX)`. `socketpair` stays allowed (measured), so pipes and socket pairs between a parent and its children work.
- Below ABI 9, extension processes. Decision (2026-11-16, `Review record`): PiG fails at startup when an extension would run inside the boundary. An extension process must open a Unix socket to reach its host (`PIG_EXT_SOCKET`), and a filter that lets it do that lets it, and every command it starts by itself, reach any other socket. The error names the four ways forward: a kernel with ABI 9, `sandbox: "bubblewrap"`, `extensions.outside` for that extension, or `extensions.allowUnmediatedUnixSockets: true`.
- `extensions.allowUnmediatedUnixSockets: true` accepts the gap for extension processes on this backend. It is for a host that has no socket worth reaching, such as a container without a session bus or a Docker socket; only the embedder knows that. The value exists because the alternative on such a host, `extensions.outside`, removes every limit from the extension, which is weaker than a file and network boundary with this one gap.

A Piglet Binary whose extensions are fused (D31) starts no extension process, so none of this applies to it.

**Linux: bubblewrap (alternative).** PiG builds the argument list: `--unshare-all` (with `--share-net` only when `network` is `"on"`), `--die-with-parent`, `--new-session`, a read-only bind per read root, a bind per write root, read-only binds and masks for `readOnly` and `deny`, and a fresh `/proc` and minimal `/dev`. It needs the `bwrap` program and unprivileged user namespaces, which Ubuntu 24.04 restricts by default through AppArmor. PiG executes bubblewrap as a separate program (LGPL-2.0-or-later) and neither links nor ships it.

**Backend selection on Linux** is a function of the policy alone, so the result is never weaker than the policy and does not change with the host or with what the workspace contains: `"builtin"` uses Landlock and seccomp unless the policy has `deny` or `readOnly` entries or `network: {"proxy": ...}`; then it requires bubblewrap. `"bubblewrap"` always requires it. A requirement the host cannot meet fails at startup and names the feature and the missing piece.

**Windows: MXC ProcessContainer, selected explicitly.** PiG has no Windows boundary of its own, and `sandbox: "builtin"` fails at startup there. Windows 11 gained a native boundary in 2026: a process security environment (`CreateProcessSecurityEnvironment` in `processmodel.dll`, attached to `CreateProcessW`), with path grants and denials and per-container WFP egress filters, and without changes to the access-control lists of the user's files. Its specification blob is undocumented (the Win32 reference page lists the function only), so PiG does not call it. Microsoft eXecution Container (MXC, `microsoft/mxc`, MIT) builds that blob. PiG runs MXC's standalone executor as an external program, as it runs `sandbox-exec` and `bwrap`:

- Policy: `sandbox: {"mxc": "<absolute path of wxc-exec.exe>"}`. PiG does not ship, download or search `PATH` for the executor. The startup error of `"builtin"` on Windows names this value and the wrapper.
- Per command PiG writes an MXC request in the stable `1.0.0` contract and runs `wxc-exec.exe <request.json> -- <argv>`: `write` becomes `filesystem.readwritePaths`, `read` becomes `readonlyPaths`, `deny` becomes `deniedPaths`, and `network: "off"` becomes `network.egress.default: "deny"`. UI access stays disabled, which is MXC's default and applies the Win32k system-call mitigation, so the process cannot create a window. The request never has a `telemetry` member (`Telemetry of the Windows executor`).
- At startup PiG runs `wxc-exec.exe --probe <request.json>`, which exits 1 when native ProcessContainer cannot enforce the request, and fails closed on that.
- Host floor: Windows 11 24H2 build 26100.9278 (the August 2026 update). MXC's support table lists Windows 11 releases only: no Windows 10 and no Windows Server. Native `git` and Node need the October 2026 update (MXC issue #1464: path translation fails below build 26200.9711).
- The read baseline on Windows is what the OS grants `ALL APPLICATION PACKAGES`. PiG does not define one.

Known limits, from MXC's documentation and issue tracker. None of this is measured: no Windows 11 machine was available for this design.

- **Git Bash does not run inside ProcessContainer.** MSYS2 runtimes cannot initialize (MXC #1061, open since 2026-08-28), and with the startup fix a second MSYS process still dies: pipelines, subshells, command substitution and `git` through bash fail (#1729, open). Pi's `bash` tool on Windows is Git Bash. See `Shell on Windows`.
- Unknown until tested on Windows: whether an extension process inside the container reaches its host socket; whether a `readonlyPaths` entry inside a `readwritePaths` tree is enforced (if not, the project configuration rule falls back as on Landlock); what PiG's hard kill of a process tree leaves behind (`wxc-exec` has `--force-reclaim` for "an orphan left after a launcher hard-kill"); and the per-command overhead (MXC #1629 reports about 28 s to first output with UI enabled and a fast start with it disabled).
- Proxy mode: MXC accepts a loopback proxy only, and calls an unpackaged proxy process, which PiG's forwarder is, its "lowest-enforcement deployment option", "intended primarily for development and debugging". On Windows `network: {"proxy": ...}` is a startup error until it is tested.
- Tools that walk up from the working directory get `ACCESS_DENIED`, not "not found", at the first parent outside the grants.

**Shell on Windows.** Decision (maintainer review, 2026-11-16): under `mxc` the model's shell is Pi's `powershell` built-in.

- Pi registers `powershell` on every platform and activates it only when a tool allowlist, the `defaultTools` setting or an explicit active set names it. So the Piglet's `tools` or the launcher's `--tools` names `powershell` and leaves `bash` out. PiG does not change the tool selection by itself: composition is policy.
- PiG does not hard-code the Git Bash defect. Under `mxc`, startup runs `echo ok | cat` inside the container through the shell Pi resolves (`getShellConfig`: `shellPath`, else Git Bash) and expects `ok`. In Git Bash `cat` is a second MSYS process, which is what #1729 breaks. When the check fails and the `bash` tool is active, startup fails and the message names `powershell`. When MXC and MSYS2 fix the defect, or `shellPath` names a bash that runs inside the container, the same check passes and `bash` works with no change in PiG.
- `!` commands and the RPC `bash` command fail inside the Windows boundary while that check fails. PiG never runs them through PowerShell. Decision (2026-11-16, made by the design author at the reviewing maintainer's request). The error reads: `Containment: this command runs through bash, which does not run inside the Windows boundary on this host. Ask the agent to use the powershell tool, or use another terminal.` It takes the path of Pi's own shell-resolution error: `ExecuteBash` returns it and records no `bashExecution` message (`coding/session.go`). Reasons:
  - They are Pi's bash commands. The session records each one as a `bashExecution` message, and the model reads it back as ``Ran `<command>` `` (`bashExecutionToText`, `messages.ts:82`). Running the command in PowerShell would put into the Transcript something that did not happen.
  - Bash syntax is not PowerShell syntax. On Windows `ls` and `cat` are aliases of `Get-ChildItem` and `Get-Content`. Measured with PowerShell 7.7.0: `Get-ChildItem -la` fails ("A parameter cannot be found that matches parameter name 'la'"), and `"ok" | Get-Content` fails to bind its input. A correct `!ls -la` would return a PowerShell error.
  - `shellPath` and `shellCommandPrefix` belong to the bash contract. Each would need a special case.
  - The refusal ends by itself when the check passes. A PowerShell form of `!` can be added later without breaking anyone; a reinterpretation could not be removed later without a behaviour change.
- `fd`, `rg`, `pi.exec`, extension processes and MCP servers are native programs and do not depend on the shell.

**Telemetry of the Windows executor.** From MXC's documentation at v1.1.0, not observed:

- Official Windows builds send four "Required" events to Microsoft for every execution: `MXC.ExecutionStart`, `MXC.ExecutionEnd`, `MXC.Error` and `MXC.ProcessExited`. "They need no user consent or per-request opt-in, and administrative policy does not block them." A request that sets `telemetry.enabled` to `false` does not disable them.
- This is new in v1.1.0 (2026-11-15). In v1.0.1 (2026-10-17) telemetry was off unless the request opted in, the user consented and `HKLM\SOFTWARE\Policies\Mxc\AllowTelemetry` permitted it, and the documentation said "MXC emits no required diagnostic data". Two commits changed it (2026-11-05 and 2026-11-13), and the change also applies to requests written in the older contract versions. The terms changed within one minor release.
- Fields: MXC version, channel, backend, sandbox kind, outcome, exit code, duration, failure category, lifecycle phase, process ID, and random session and correlation identifiers. The documentation states that no command line, path, environment value, output or user name is sent.
- Mechanism: MXC writes ETW events to the provider `Microsoft.MXC`, tagged with a routing group that the build reads from a signed `mxc_telemetry.dll` committed in the repository. MXC opens no connection; the Windows diagnostic pipeline uploads. MXC's documentation says the Windows Required or Optional setting does not gate these events.
- There is none on Linux and macOS (compile-time no-op), in a debug build, or in a release build made with `MXC_TELEMETRY_COMPONENT_PATH` set to an empty value. The MIT licence lets an embedder build and distribute that.

Consequence: PiG wraps every command, so the events are a per-device record of when each agent command ran, how long it took and how it exited.

Decision (2026-11-16, made by the design author at the reviewing maintainer's request): the telemetry is the embedder's choice. PiG's documentation states the facts and recommends neither build.

1. Stock PiG never bundles, downloads or locates MXC. No Stock artifact contains it, so no Stock artifact sends anything.
2. PiG never opts in to optional collection: its requests have no `telemetry` member. With an executor up to v1.0.1 nothing is sent. From v1.1.0 the four Required events are sent whatever the request says.
3. The documentation of the `mxc` value states: what official builds send, with the MXC version the statement was checked against; that neither PiG nor an MXC setting turns it off; the two builds an embedder can deploy and what each costs; that MXC asks integrators to disclose the collection to their users; and that the terms changed once within a minor release, so the embedder reads MXC's `docs/telemetry.md` for the version it deploys.
4. PiG adds no acknowledgement field and no notice at run time. The `mxc` value is already an explicit selection by absolute path, and a notice to end users is the embedder's product decision.

Why no recommendation:

- The two builds trade privacy against provenance. The official executor is signed by its vendor (the v1.0.1 x64 `wxc-exec.exe` carries an Authenticode certificate table that names Microsoft Corporation; the signature was not verified cryptographically here) and is the build the vendor tests against the OS feature. An unrouted build is one the embedder compiles, signs and updates. For a security boundary that provenance has a value, and its value against four events without commands or paths depends on the embedder's obligations, which PiG does not know.
- PiG cannot tell a routed executor from an unrouted one, so it could not check its own recommendation.
- PiG's own analytics are opt-in (`enableAnalytics`). That is why PiG never opts in and never bundles. It is not a reason to choose for the embedder.

MXC v1.1.0 is younger than 7 days. PiG pins no version because it ships none. The escape suite takes the executor path as a parameter and records the version it ran against.

**Nested PiG and subagents.** A `pig` that an agent command starts is inside the boundary. The kernel confines it whatever it does, and it reaches neither PiG's credentials nor, with the network off, a provider. PiG makes that explicit: the policy file is denied inside the boundary, so a child that inherits `PIG_CONTAINMENT` fails at startup. Seatbelt does not nest (measured: `sandbox_apply: Operation not permitted`, exit 71), so on macOS a PiG whose policy needs the built-in sandbox also fails at startup when it finds itself already sandboxed. A launcher that provides the outer sandbox selects `sandbox: "none"`. Landlock and seccomp stack (measured), so on Linux a Binary with a baked policy adds its own layer. A subagent that must work is started from outside the boundary, by the host or by an extension that the policy places outside. PiG exports `PIG_CONTAINMENT` to the processes it starts when the launcher supplied the policy, so such a child applies the same source against its own launch directory.

### c. Network

Why: read confinement without egress control still lets the agent send the workspace anywhere, and an open network lets it fetch what the roots exclude.

- `"off"`: no IP sockets and no name resolution for children. Loopback is off too: the host's loopback reaches services outside the boundary. Under bubblewrap the sandbox has its own empty loopback, so local test servers work there.
- `{"proxy": "http://host:port"}`: children reach exactly one endpoint. PiG runs one byte forwarder per process and relays to the configured proxy. PiG does not parse or filter the traffic; filtering is the embedder's proxy. On macOS the endpoint is a loopback port that the profile allows by number (measured: Seatbelt rejects any other host with `host must be * or localhost in network address`). Under bubblewrap it is a Unix socket bound into the sandbox, and the helper bridges a loopback port inside the network namespace to it. PiG sets `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, their lower-case forms and an empty `NO_PROXY` for children. Name resolution stays off: a proxy client sends the host name to the proxy, and local DNS is an exfiltration channel.
- `"on"`: no network rule.

PiG's own requests (providers, MCP over HTTP) are not children and are unaffected.

### d. Embedder-supplied wrapper

Why: an embedder with a hardened profile or a platform PiG does not cover must be able to use it without forking PiG.

`sandbox: {"wrapper": ["/opt/x/confine", "--flag"]}` replaces PiG's backend. PiG runs `wrapper... -- <original argv>` at the same sites, and exports `PIG_CONTAINMENT_POLICY` (the path of the resolved policy as JSON) and `PIG_CONTAINMENT_KIND` (`tool`, `exec`, `extension` or `mcp`). Layer a, the environment rule and the denial reporting stay in force. PiG cannot verify what a wrapper enforces and says so in `pig diagnose`. The escape suite takes the wrapper as a parameter, so an embedder can run it against its own boundary.

Decision (2026-11-16, `Review record`): the command prefix is the only form. PiG does not export the same seam as a Go function for a program that writes its own `main` until an embedder asks for it. The prefix already serves the command line, a Piglet Binary and a program that links PiG. `MainOptions` mirrors Pi's `main(args, options)` (`coding/cli/main.go`), so a PiG-only member there is a shape Pi does not have. And an exported function type would freeze the seam (the kinds, the policy structure) before three backends have used it. The internal seam keeps the shape `func(*exec.Cmd, Kind) error`, so exporting it later is a small change.

### Self-carried escapes

Rule: **anything PiG executes or installs outside the boundary must not be writable inside it.** Otherwise the agent edits it now and PiG runs it unconfined at the next start. At startup PiG checks the project configuration directory (`ConfigDirName()` under the workspace: `.pig`, or `.pi` under D2's `PIG_USE_PI_DIRS`), every configured extension and Package source path, and the policy file:

- Under Seatbelt and bubblewrap, PiG adds the project configuration directory to `readOnly`.
- Under Landlock, which cannot do that, a contained session treats the project as untrusted. Decision (2026-11-16, `Review record`). This is Pi's own trust gate (`internal/codingagent/trust_manager.go`), set for the session and not stored: the project's `settings.json`, `mcp.json`, `extensions`, `skills`, `prompts`, `themes`, `SYSTEM.md` and `APPEND_SYSTEM.md` are not loaded. PiG reports it in one line on standard error at startup. Context files load as before; they are prompt text, which the threat model leaves to the workspace.
- A source the launcher named explicitly (`-e <path>`) that is writable inside the boundary is a startup error.

Why the whole gate, and why not a startup failure:

- Project settings reach more host-side behaviour than `packages`: the npm command, the shell path, MCP servers reached over HTTP. Loading some of them would need an audit of every reader, and a new gate beside Pi's. The existing gate is one tested switch.
- Requiring bubblewrap for every workspace that has a project configuration directory would turn a common layout into a startup failure on hosts where bubblewrap cannot run.
- The cost is stated: on Landlock a contained session also loses the project's skills, prompt templates and themes, which carry no escape. To load project-scope resources on Linux, select `sandbox: "bubblewrap"`, which protects the directory as Seatbelt does.

### Child environment

By default PiG removes from a child's environment every provider credential variable that Pi's auth metadata names (`ai/auth_env_keys.go`). A policy `env` list replaces that default with an exact allowlist, plus the variables PiG itself sets.

## 5. Who can set and who can loosen the policy

| Actor | Can set | Can widen |
|---|---|---|
| Piglet author: `containment` field. Baked into a Piglet Binary and covered by its Piglet digest. Not inherited through `extends` and refused in an overlay Piglet, as `build.closed` is. | yes | no, after the build |
| Launcher: `--containment <file>`, else `PIG_CONTAINMENT=<file>` | yes | by starting another process |
| Global or project settings, `/settings`, `settings.json` edits | no: there is no settings key | no |
| Project files (the project configuration directory, context files) | no | no |
| The model, tools, codemode | no | no |
| Extensions (any SDK), MCP servers | no: there is no API | no |
| RPC and JSON hosts | no: there is no command | no |
| The human at the terminal | no | not in the first release; later, one exact command, once, when the policy allows it (`Human widening`) |

A Piglet policy and a launcher policy in one process is a startup error, not a merge. There is no narrowing merge, so there is no merge rule to get wrong. `--unsafe-host` never bypasses `containment`.

PiG resolves the policy once per process, at startup, against the launch directory. `/reload`, compaction, a model change, `new_session`, fork, resume and `switch_session` do not change it. A replacement that would move the session to a working directory outside the read roots is refused before the current session is torn down (`Session working directory`). The resolved policy is held by the services of each session, not by a process global, and every session of one process holds the same one.

### Human widening

Decision (2026-11-16, `Review record`): the first release has no widening inside PiG. A human who needs a command outside the boundary runs it in another terminal. That already satisfies "only a human, explicitly, one command at a time", and it cannot be socially engineered through a dialog. The approval tools are the last step of `Order of work` and land after the boundary has been released and used. Reasons:

- They are the only code path that removes the wrap. They land when the wrap sites and the escape suite are stable, so that a test can prove the unwrapped path is reachable through the dialog and through nothing else.
- The first users are embedders without a human at the terminal. They would set `approval: "never"`.
- How often a model asks, and what a human approves without reading, are facts that only use shows. The dialog should be shaped by them.

When they land:

- The policy gains `approval`: `"never"` (the default) or `"interactive"`.
- With `"interactive"`, in interactive mode, PiG registers one additive tool per active shell tool: `bash_unsandboxed` for `bash` and `powershell_unsandboxed` for `powershell`. The names follow Pi's `tool_search` form and the policy's own word for layer b (`sandbox`), and each says which shell reads the command. Parameters: `command` and `reason`.
- A call opens a host-drawn dialog that shows the exact command, the working directory and the model's reason, with two choices: "Run once without the sandbox" and "Deny" (the default). Approval runs that one command through the normal path of its shell tool without the wrap.
- The answer is not stored: there is no "always allow", no pattern and no per-session memory.
- The dialog is read by the host's input owner. No extension API, RPC command or tool can answer it, and a contained process has no terminal device.
- In print, JSON and RPC modes the tools are not registered, so nobody can ask and every denial is final.
- `!` commands always run contained.

## 6. Behaviour on denial

- File tools return a tool error that states the operation, the resolved path, the rule and that a retry does not help, for example: `Containment: read denied: /home/u/.ssh/id_ed25519 is outside the read roots (/work/repo). The policy is fixed. Do not retry with another path form.` The roots are listed; the text never reveals whether the path exists.
- A shell command that exits non-zero gets one appended line that names the boundary: read roots, write roots and network mode, and that a permission or connection error may be the boundary and does not change on retry. The kernel does not tell an unprivileged PiG which call it denied, so PiG does not guess.
- The system prompt gets one guideline line while a policy is active, with the same three facts, so the model does not start by probing.
- JSON and RPC output gets an additive event for every decision PiG itself makes: `{"type":"containment_denied","layer":"path"|"sandbox"|"session"|"startup","operation":"read"|"write"|"exec","target":"...","rule":"...","toolCallId":"...","policy":"<digest>"}`. The approval tools add the layer `"approval"`. Tool results of wrapped commands carry `contained: true` and the digest in their details, so a host can correlate failures.
- A wrap that cannot be built is an error of that tool call. The command does not run. That refusal, a call that carries no policy in a process that has one, and a bash command refused inside the Windows boundary emit the event with `layer: "sandbox"`. A refused session replacement emits it with `layer: "session"`.
- Startup fails, with exit status 1 and one line per unmet requirement, when: two policy sources exist; the document is invalid; `sandbox` is `"builtin"` or `"bubblewrap"` and the platform, ABI, `sandbox-exec`, `bwrap` or user namespaces are missing; `sandbox` is `mxc` and the executor is missing or its probe rejects the request; `sandbox` is `mxc`, the `bash` tool is active and Git Bash does not run inside the container; a program PiG starts itself lies outside the read roots; the selected session's working directory lies outside the read roots; an extension would run inside the boundary on Landlock below ABI 9 and the policy makes none of the four choices; a nested PiG on macOS has a different digest; an explicit extension source is writable inside the boundary.

## 7. Parity

- Without a policy source every wrapped site receives the command it receives today, the tools use Pi's default operations, and no schema, prompt, event or record changes. `pig diagnose` prints what it prints today. Parity scenarios do not change, and none is added to a parity family. A unit test pins that the wrap leaves an `exec.Cmd` byte-identical in a process without a policy source.
- The record is additive: `docs/additive-features.md` `D<N>`, `Stock disposition: inert capability`, `Audience: author`, with `// pig additive (D<N>)` at each site in `Layers`. No `docs/parity/DIVERGENCES.md` entry: Pi has no overlapping behaviour that differs.
- The `--containment` flag joins `pigClaimedFlags` (`coding/cli/args.go:455`). `make generate` covers the flag, the Piglet schema and the docs mirrors.
- No SDK interface changes. Extensions get no containment API, so there is nothing to keep identical across SDKs.

## 8. `--listen` (D232, PR #404)

D232 gives every session the server's working directory, flags and Piglet, and shares a cell process found under the cell hash, working directory, configuration root and runtime tier.

Decision (2026-11-16, `Review record`): this design adds no per-connection workspace to `--listen` and needs none.

- One server has one launch directory, so it has one resolved policy and one digest. Every connection's session holds that policy. `${tmp}` is per process for the same reason: a per-session directory would give every session its own digest, and no two sessions could share a cell.
- The sessions keep sharing cells, as D232 intends. A shared extension process is sandboxed once, with the profile of the policy that all of them hold.
- The resolved policy digest and the placement join the cell registry key. Today that key cannot differ between two sessions of one server. It is the guard for the day it can: an extension process is sandboxed with one policy's profile, so sharing it with a session of another policy would cross the boundary, not only share state.
- An embedder that serves several workspaces runs one server per workspace, which is what D232's "the server's working directory" already means.
- The `switch_session` command of a connection is a session replacement. It is refused when the destination's working directory is outside the read roots (`Session working directory`).

Tests: two Hosts with different digests in one process get separate cell processes and separate enforcement (Host level, because no server can produce two digests). Two connections of one contained server are both denied the same canary and share one extension process (end to end). If D232 later accepts a working directory per connection, the policy resolves per connection with no change here, because each session's services already hold it, and the end-to-end row for two policies lands with that change.

## 9. Piglets

- `containment` becomes a root field of the closed Piglet schema, with the object of `Policy document`. `pig piglet validate` checks it, `pig piglet schema` prints it, `Bake` keeps it, and the Binary's Piglet digest covers it.
- A Piglet Binary cannot carry `agentEnv` (`pig piglet build` refuses it). For a Binary in CI, `containment` is therefore the only boundary the Binary carries by itself: `${workspace}` is the directory the job starts the Binary in, nothing inside PiG widens the boundary, and with `build.closed: true` nothing at launch adds an extension, an MCP server or an overlay. Its fused extensions start no extension process, so the Landlock rule for Unix sockets does not concern it. An open Binary still refuses a `containment` in an overlay, and an `-e` extension runs inside the boundary.
- `agentEnv` (image, `devContainer`) moves the whole PiG process, its credentials and its extensions into a container. `containment` confines the agent's tools and children from a PiG that stays where it is. The Piglet specification already separates them ("code-execution sandbox is separate"). They compose: inside a container, `containment` hides the container's own credentials and system from the tools. Landlock works inside a default Docker container (measured); bubblewrap usually does not; `sandbox: "none"` keeps layer a when the container is the OS boundary.
- `agentEnv.policy.preset` governs the container's mounts and stays unrelated. No preset implies a containment policy.

## 10. Cost (measured 2026-11-16)

Method: a no-op command, spawn plus wait, 1000 runs after warm-up, no shell around the harness. Machine: Apple M3 Max. macOS 27.2.1 with hyperfine; Linux in a virtual machine on the same host (kernel 7.2.12, Landlock ABI 10, Debian 13.7 arm64 container, uid 1000) with a Go harness. The prototype helper and profile are throwaway code outside the repository.

| Platform | Command | Median |
|---|---|---:|
| macOS | `/bin/sh -c true` | 7.56 ms |
| macOS | `sandbox-exec -f <deny-default, 15 rules> /bin/sh -c true` | 15.36 ms |
| macOS | `sandbox-exec -p '(version 1)(allow default)' /bin/sh -c true` | 15.38 ms |
| Linux | `/bin/sh -c true` | 0.17 ms |
| Linux | 2.4 MB Go helper: Landlock (9 rules, TCP, scopes) + seccomp, then `execve` | 1.00 ms |
| Linux | same without seccomp | 0.88 ms |
| Linux | raw `clone` into user, mount and PID namespaces | 0.29 ms |
| Linux | the same plus a network namespace | 0.45 ms |
| Linux | `pig --version` (the cost of re-executing `pig` as the helper) | 5.9 ms |
| macOS | `pig --version` | 12.6 ms |

Per command: about +7.8 ms on macOS, independent of profile size at this scale; about +6 ms on Linux when the helper is `pig` itself, or +0.8 ms with a dedicated small helper. The design re-executes `pig`: one binary, and 6 ms is small beside a model turn. Phase 2 measures the real helper on the CI runner with a 10 ms median budget; above it, the helper becomes a small embedded binary.

Not measured: bubblewrap itself (no `bwrap` binary and no package network on the measurement host; the namespace rows are the kernel's share only), Windows, and the path-policy overhead per file-tool call (a few `stat` calls; phase 2 benchmarks it with allocations).

## 11. Options considered

| Option | Verdict |
|---|---|
| Reuse `@anthropic-ai/sandbox-runtime` through Node (Pi's example until 1.4.0) | Rejected. Pi itself retired the example in 1.5.0 to remove vulnerable dependency chains. It needs Node and an npm package at run time, plus bubblewrap, socat and ripgrep on Linux; no Windows; covers commands only. Apache-2.0 is compatible with MIT. It is pre-1.0: 0.0.83 was published 2026-11-13 and 0.0.82 on 2026-11-10, both younger than 7 days, and Pi's example pinned 0.0.26. A boundary that lives in an extension cannot confine extensions or the file tools. |
| Port `sandbox-runtime` to Go | Rejected as a port. Its Linux design is bubblewrap plus socat plus its own filtering proxies. PiG takes the two ideas it needs (a deny-default Seatbelt profile, a bubblewrap argument list) and writes them against PiG's policy; no code is copied. |
| `github.com/landlock-lsm/go-landlock` (MIT) | Not needed. It pulls `kernel.org/pub/linux/libs/security/libcap/psx` to apply a ruleset to every thread of a Go process; PiG restricts a helper thread that then `execve`s. v0.11.2 (2026-11-10) and v0.11.1 (2026-11-09) are younger than 7 days; v0.11.0 (2026-10-23) is not. |
| Landlock and seccomp through `golang.org/x/sys/unix` | Chosen. `x/sys` v0.49.0 is already a direct dependency and carries the constants through ABI 9, the attribute structs, the three syscall numbers and `SockFprog`. No new module. |
| Restrict a locked OS thread of the PiG process and fork from it | Rejected. It saves the helper's start, but the Go runtime then performs collections, signals and file opens on a restricted thread. |
| bubblewrap as the only Linux backend | Rejected as the only one: it needs user namespaces, which containers and Ubuntu 24.04 restrict. Kept as the alternative, because only it gives deny entries, exact proxy mode and a private loopback. |
| Run the file tools as sandboxed child processes | Rejected. A process per `read` call, and a rewrite of ported tools. `os.Root` gives a race-safe in-process check. |
| Path-prefix string comparison | Rejected: case-insensitive volumes, firmlinks, 8.3 names and UNC aliases defeat it. Identity comparison does not depend on spelling. |
| A settings key, merged like Pi's settings | Rejected. `deepMergeSettings(global, project)` lets a trusted project override every key, which is the widening Pi's former sandbox example had. |
| A `tool_call` gate extension (`permission-gate.ts`, `protected-paths.ts`) | Rejected as the boundary: it sees tool arguments, not what a command does, and another extension or a tool override can bypass it. |
| MXC on every platform | Rejected. Its Linux default is bubblewrap and it has no Landlock backend, so hosts without user namespaces stay uncovered; its macOS backend is the same Seatbelt mechanism. It would add an external executor on platforms where PiG needs a profile string and a syscall helper. It has no Go SDK. v1.0.0 is from 2026-10-07, and the repository has 172 open issues and pull requests. |
| MXC through its C library (`mxc_ffi.dll`) or its Node SDK | Rejected. A third-party native library inside the PiG process, or Node at run time. The executor keeps MXC a separate program. |
| Call `CreateProcessSecurityEnvironment` from Go | Rejected. The specification format is undocumented, and MXC's runner for it is about 150 KB of Rust. |
| AppContainer with access-control changes, a restricted token, or a low integrity level | Rejected. The first changes the access-control lists of the user's files; the other two do not deny reads. |
| Mediate `connect` below Landlock ABI 9 with seccomp user notification | Deferred. A supervisor in PiG would receive each `connect` of an extension process, check the address and perform the connect itself. That closes the gap without a kernel requirement or an SDK change. It needs a supervisor per extension process and either ptrace permission over it (`pidfd_getfd`, limited by Yama) or the descriptor-replacing form, whose effect on each SDK's runtime would have to be tested. It is the way to retire `extensions.allowUnmediatedUnixSockets` if hosts below ABI 9 stay common. |
| Give an extension process its host link as an inherited descriptor | Deferred. It removes the need for `socket(AF_UNIX)`, but every SDK dials a path today (`PIG_EXT_SOCKET`), and a packed or shared cell admits later members on new sockets. |
| A new `pig containment check` command | Rejected. `pig diagnose` exists and gains a section (`Diagnostics`). |
| Whole-process container only (`agentEnv`, Pi's containerization guide) | Kept as the stronger, complementary choice. It does not fit a Binary, a desktop launcher, or a host without a container engine. |

## Diagnostics

Decision (2026-11-16, `Review record`): no new command. `pig diagnose` exists (`coding/cli/diagnose.go`) and gains a containment section when the process has a policy source (`--containment`, `PIG_CONTAINMENT`, or the Piglet baked into a Binary). The section prints the source, the digest, the resolved roots, the backend with the Landlock ABI or the executor path and version, the statement that a wrapper is not verified or that an MXC executor may send diagnostic data, and every unmet requirement. With a policy source, `pig diagnose` exits with status 1 when a session would not start, so a launcher can use it as a check before it serves users. Without a policy source its output and its exit status are what they are today.

## Order of work (phase 2)

Each step is one reviewable change with its tests, a `changelog.d` fragment, the record and ledger updates it needs, and no version change. Nothing is pushed. The owner allocates the record number before step 1.

1. Policy package: parse, validate, resolve once per process, digest; the two sources and their conflict error; the record in `docs/additive-features.md`; the `docs/threat-model.md` text.
2. Escape suite harness (`Escape suite`), with every case proving its escape on an uncontained run.
3. Layer a: the contained fallback of the six file tools, the policy in the tool-call context, the refusal of a call without a policy, the denial error and the event.
4. Layer b on macOS: Seatbelt for shells, `!`, `fd`, `rg`, `pi.exec`; the baseline and the check of the programs PiG starts; the environment rule; the private `${tmp}`.
5. Layer b on Linux: the helper verb, Landlock and seccomp, ABI probing, the socket filter for tool commands, startup failure.
6. Session working directory: the refusal at startup and at a replacement.
7. Extension processes and MCP stdio servers inside the boundary; `extensions.outside`; the Unix-socket rule and `extensions.allowUnmediatedUnixSockets`; cell plan key; the self-carried escape checks, with the project trust switch on Landlock.
8. Network: `"off"` on both platforms, then the forwarder and proxy mode.
9. bubblewrap backend and the embedder wrapper.
10. Piglet `containment`: schema, validation, baking, digest, overlay refusal, `--unsafe-host`.
11. `--listen`: the registry key and the two tests, after PR #404 merges.
12. Windows: startup failure for `"builtin"` and layer a cases on the Windows runner; then the `mxc` backend (request builder with a test that no request has a `telemetry` member, probe, shell check, telemetry statement in the documentation), developed and tested on a Windows 11 machine at the build floor.
13. `docs/site/docs/security.md` and its mirror; the containment section of `pig diagnose`.
14. After the capability has been released and used: `approval` and the approval tools (`Human widening`).

## Escape suite

A Go test package outside the parity families, run in CI on `macos-15` and `ubuntu-24.04`, and on `windows-latest` for the layer a cases. `windows-latest` is a Windows Server image, which is not in MXC's support table, so the `mxc` rows run on a Windows 11 host. They drive the `powershell` tool with the PowerShell form of each case (home and profile listing, a junction and a symbolic link that point outside, `[IO.File]` reads, `cmd /c`, `Start-Process` of a GUI program, `Invoke-WebRequest`), set `$ProgressPreference = 'SilentlyContinue'` first, and add two rows: `bash` active under `mxc` fails startup while Git Bash is broken, and a `!` command returns the containment error. Each case plants a canary outside the roots and drives a real session with the faux provider issuing the tool call. Each case runs twice:

1. Without a policy the attempt must reach the canary. This is the red evidence on `main`, and it keeps a case from passing because its attempt was broken.
2. With the policy the attempt must not reach the canary, the tool result must name the boundary, and for layer a the event must be emitted.

Escape cases: `find /`; `ls ~`; `cat ~/.ssh/id_*`; `cd .. && ls`; absolute paths through each file tool; a symbolic link created inside the workspace that points outside, read and written through each file tool and through the shell, including the swap race; hard-link creation to an outside file; `git -C /other/repo log`; `python3 -c 'open(...)'`; `node -e`; subshells and `bash -c`; writes outside with `tee`, `>`, `cp`, `mv`; `/dev/fd` and `/proc/self/root`; `open` and `osascript` on macOS; direct `curl` against the same request through the proxy; a pathname Unix socket outside the roots and an abstract socket, from a tool command and from an extension process, on each side of Landlock ABI 9; provider credential variables in a child's environment; an extension tool that reads outside (one fixture per SDK host); a built-in tool that an extension creates; `pi.exec`; an MCP stdio server; a codemode script; `!` commands and the RPC `bash` command; a child `pig` started inside the boundary and one started by an outside extension.

Cases about the policy itself: it holds after compaction, `/reload`, `new_session` and fork; a `--session` start and a `switch_session` whose working directory is outside the read roots are refused, and the refused replacement leaves the current session running; two Hosts with different digests share no cell; two connections of one contained `--listen` server are both denied and share one extension process; a process without a policy source leaves every site untouched; a call that carries no policy in a process that has one fails; the programs of the baseline list start; and each startup failure of `Behaviour on denial`, among them a program outside the read roots, an extension inside the boundary on Landlock below ABI 9, and two policy sources. On Landlock the project's trust-gated resources are not loaded; under Seatbelt and bubblewrap the project configuration directory is not writable.

The runner's Landlock ABI decides which Linux rows run. The rows on the other side of ABI 9 run where such a kernel is available. The bubblewrap rows need the runner's user-namespace restriction lifted in the job. Rows that cannot run on a runner are reported as not run, never as passed.

## Open questions for the owner

1. Record number: `D<N>`. This is the only question the review could not settle, because the number is the owner's to allocate. `main` at `bc46a1395` holds D224 to D228, D231, D233 and D247, and PR #404 takes D232. The `follow/pi-main` line and the fix branches cut from it on the upstream remote carry further records that `main` does not have yet: numbers between D234 and D277, and blocks from D300 to D541. A number that looks free on `main` is therefore not free. Phase 2 writes no number until the owner names one.

## Review record

- 2026-11-16, maintainer review of the draft: on Windows the contained shell is Pi's `powershell` built-in (`Shell on Windows`).
- 2026-11-16, decided by the design author at the reviewing maintainer's request, open to reversal at owner review: (1) MXC's telemetry is the embedder's choice, PiG never opts in and never bundles MXC, and the documentation recommends neither build (`Telemetry of the Windows executor`); (2) `!` commands and the RPC `bash` command fail inside the Windows boundary while bash does not run there, and are never run through PowerShell (`Shell on Windows`). With these, MXC selected by absolute path is the Windows backend of this design.
- 2026-11-16, the eight open questions of the draft, decided by the design author at the reviewing maintainer's request and open to reversal at owner review:
  1. Record number: not decided. It stays with the owner (`Open questions`).
  2. `--listen` gets no per-connection workspace from this design. One server has one policy, its sessions share cells, and the digest in the registry key is a guard (`--listen`).
  3. The baseline holds only what the operating-system vendor installs and protects. `/usr/local` on macOS, `/opt/homebrew` and `/nix/store` are policy. A startup check names a program that PiG starts and that lies outside the read roots (`Policy document`).
  4. On Landlock below ABI 9, an extension inside the boundary is a startup error unless the policy chooses bubblewrap, `extensions.outside` or `extensions.allowUnmediatedUnixSockets`. Tool commands and MCP stdio servers lose `socket(AF_UNIX)` there (`Unix sockets below ABI 9`).
  5. On Landlock a contained session treats the project as untrusted, through Pi's own trust gate. `sandbox: "bubblewrap"` restores project-scope resources on Linux (`Self-carried escapes`).
  6. The approval tools are `bash_unsandboxed` and `powershell_unsandboxed`. They and the `approval` field land last, after the capability has been released and used (`Human widening`).
  7. The wrapper is a command prefix only. A Go function for a program that writes its own `main` waits until an embedder asks (`Embedder-supplied wrapper`).
  8. No `pig containment check` command. `pig diagnose` gains a section and a failing exit status (`Diagnostics`).
- 2026-11-16, corrections that deciding these questions exposed in the earlier draft:
  - `${workspace}` followed the session's working directory. Pi follows the directory a session file stores, so resuming a session would have moved the boundary. It is now bound once per process, and a session outside the read roots is refused.
  - `${tmp}` was per session. That gave every session its own digest, so no two `--listen` sessions could have shared a cell. It is now per process.
  - The path policy was supplied through `ToolsOptions` at construction. The file tools are constructed at six places, so a forgotten one would have run unconfined. The policy now travels with the tool-call context, and a call without one fails (`No unguarded path`).
  - The macOS baseline listed `/usr` and `/Library` whole. `/usr/local` is a user-writable Homebrew prefix on Intel Macs, and `/Library` is wider than the developer tools need. Both are narrowed, and the active developer directory is added, because Apple's `git` loads it.
  - The record said the highest number in use was D233 and proposed the next one. In-flight branches of the upstream remote already use the numbers after it.
- 2026-11-16, correction found while preparing the pull request: the record cited Pi's sandbox example as part of the pinned Pi. It was read from a local mirror that was older than the pin. The pinned Pi retired the example. Every upstream citation in this record is now checked against tag `v1.5.0`: `SECURITY.md`, `containerization.md`, the example extensions named above, `messages.ts:82`, `getShellConfig` in `utils/shell.ts`, `agent-session-runtime.ts:196`, and `executeBash` in `agent-session.ts`, which has no PowerShell path.
