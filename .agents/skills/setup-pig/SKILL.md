---
name: setup-pig
description: Check and prepare only the tools needed for a selected PiG task.
---

<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Set up PiG development

Start by identifying one task:

1. run an existing PiG binary;
2. build PiG from source;
3. author an extension;
4. build a Piglet Binary; or
5. run the complete verification suite.

If the user did not select a task, ask which task they need. Do not install the
complete toolchain by default.

Read `docs/site/docs/development.md`, `go.mod`, and the relevant language
manifest before checking versions. Repository files are the version authority.

## Inspect the host

Check the operating system and only the commands required for the selected task.
Use non-mutating commands such as:

```bash
uname -srm
go version
node --version
npm --version
pnpm --version
python3 --version
rustc --version
cargo --version
tmux -V
```

Report installed, missing, and incompatible tools separately.

## Requirements by task

| Task | Required tools |
|---|---|
| Run an existing binary | a compatible PiG binary and the runtime required by selected interpreted extensions |
| Build PiG | Git and the Go version required by `go.mod` |
| Author a Go extension | a PiG binary and Go |
| Author a Rust extension | a PiG binary, Rust, and Cargo |
| Author a Python extension | a PiG binary and Python |
| Author a Node extension | a PiG binary and Node.js |
| Build a Piglet Binary | Go, plus the compiler required by each source extension in the Piglet |
| Run complete verification | Go, Git, Node.js, npm, Python, Rust, Cargo, and tmux on Unix |

A prebuilt native extension does not require its compiler. A Python or Node
extension still requires its interpreter unless its declared environment
supplies it.

## Installation boundary

Before installing or replacing a tool:

1. show the missing tool and required version;
2. check for an existing manager such as `mise`, `asdf`, Homebrew, or the host
   package manager;
3. propose one command appropriate for the host; and
4. ask the user for approval.

Do not use `sudo`, modify global Git configuration, replace a system toolchain,
or pipe a downloaded script into a shell without explicit approval.

## Verify the selected task

Use the smallest maintained command:

```bash
make build
```

For extension authoring, use:

```bash
pig extension init <directory>
pig install <directory> --validate-only --json
```

For the complete repository suite, use:

```bash
make check
```

Report every command run, its result, and any tool that still requires user
approval.
