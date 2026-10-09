# Piglet Binaries

A Piglet Binary is a target-native PiG executable for one fixed Piglet composition. It delivers an agent application directly without creating another harness implementation.

A Piglet Binary is a carrier for a Piglet. It does not introduce another configuration schema.

## Build a Binary

Validate the Piglet first:

```bash
pig piglet validate ./agents/reviewer.yaml
```

Build the executable:

```bash
pig piglet build ./agents/reviewer.yaml \
  --format binary \
  --out ./pig-reviewer
```

Run it directly:

```bash
./pig-reviewer
```

The build records the Piglet, its Resource closure, its component plan, the target, and verification information.

A Piglet needs no extension to build (D18). A base that only strips built-ins and sets defaults, such as a `model`, builds a Binary of PiG's own parts with that Piglet baked in:

```yaml
name: pig-core
model:
  provider: openai
  name: gpt-5
strip:
  extensions: [mcp, codemode, tool-search]
  commands: [/share]
```

Whether a Piglet lists extensions is read from its effective form, after `extends` and `extends.remove`. A Piglet that lists extensions must still resolve at least one of them: an extension whose origins do not resolve fails the build as `unresolved extension`, and when none resolves the build also reports `piglet resolves to no extensions`. A child that extends an empty base and adds extensions builds as any other Piglet.

### Build progress

PiG reports build progress on stderr (D18). A terminal shows a spinner, the current phase, elapsed time, and dim step text. CI and pipes receive one plain line per phase. The final summary gives the binary path, byte size, and total time after verification and record publication.

Add `--verbose` to stream toolchain output with member labels:

```bash
pig piglet build ./agents/reviewer.yaml --format binary --out ./pig-reviewer --verbose
```

Packed members share one compiler invocation and label. Fused Go members compile with the final binary. Module resolution, compilation, and linking can occur inside one compiler invocation, so PiG reports that operation as one phase rather than estimating progress. A failed build names its phase, shows the diagnostic tail, and prints a hint. With `--json`, progress stays on stderr and stdout remains one JSON result.

The source-checkout command `pig build [--verbose]` uses the same progress display. It builds Stock PiG, not a Piglet Binary.

## Sign a Binary

Create an Ed25519 author key once. Keep the private file secret and publish the `.pub` file:

```bash
pig piglet keygen ./reviewer-signing.key
```

Sign during the native build:

```bash
pig piglet build ./agents/reviewer.yaml \
  --format binary \
  --out ./pig-reviewer \
  --sign-key ./reviewer-signing.key
```

The signature covers every executable byte and a DSSE manifest that names the Piglet, target, PiG version, resolution record, component plan, components, and embedded files. The Binary embeds the public half of its author key and checks the signature before command dispatch. An altered signature, manifest, Piglet, record, component, or executable byte makes the Binary refuse to run.

Check a file without running it:

```bash
pig verify ./pig-reviewer
pig piglet verify ./pig-reviewer
```

`pig verify` reports a valid Piglet signature as `ok` and an unsigned file as `n/a`; because it also verifies arbitrary archives and data files, it applies the required-signature policy only after a Piglet signature block identifies the target. `pig piglet verify` is Piglet-specific and succeeds only when the file carries a valid signature. Both commands reject a signed file whose key is revoked, and Piglet Binary startup enforces the required-signature policy for unsigned Binaries.

Trust and policy are explicit local actions:

```bash
pig piglet trust add ./reviewer-signing.key.pub
pig piglet trust list
pig piglet trust revoke ed25519:<key-id>
pig piglet trust require on
```

A valid signature by the embedded author key proves that the file has not changed since that key signed it. It does not prove who controls the key. Add a reviewed public key to the trust store to attach local identity, and enable `require on` when every Piglet Binary must be signed by a trusted, non-revoked key.

## Publish to GitHub Releases

Set `release.version` in the Piglet. Then publish signed Binaries as one GitHub Release named `v<release.version>`:

```bash
pig piglet publish ./agents/reviewer.yaml \
  --to github \
  --repo acme/reviewer \
  --sign-key ./reviewer-signing.key
```

Without `--yes`, publish is a dry run. It validates the Piglet, the signing key, and every target, checks that the release does not exist yet, and prints the assets it would upload. It builds and uploads nothing. Add `--yes` to build, sign, and upload these assets:

- one signed Binary per target, named `pig-<name>-<os>-<arch>` (with `.exe` for Windows);
- `SHA256SUMS`, which lists the SHA-256 of each Binary;
- `piglet-release.json`, the release index, signed with the same key.

Targets come from `--targets os/arch,...`, else `build.targets`, else the host target. The native builder builds only the host target, and a container builder does not sign, so one machine can usually publish only its own target. When a target has no ready signing builder, publish lists each missing target with every builder's reason and uploads nothing. Each build also writes the managed build record that `pig piglet build` writes.

To publish several targets, build each target on a matching host, collect the Binaries in one directory, and publish them without building:

```bash
pig piglet publish ./agents/reviewer.yaml --to github --repo acme/reviewer \
  --sign-key ./reviewer-signing.key --artifacts ./dist --yes
```

Publish identifies each file in `--artifacts` by its signed manifest. Every file must be a Binary of this Piglet source and `release.version`, signed by `--sign-key`. The directory must hold exactly one Binary for each target, and every Binary must come from the same PiG version. Before the upload, PiG checks each staged asset against the signed index with the checks that `pig piglet pull` applies.

PiG runs the GitHub CLI (`gh release view` and `gh release create`) with your existing `gh` authentication and never handles a GitHub token. It refuses a release that already exists, because a published release is immutable. A SemVer prerelease version, such as `2.0.0-rc.1`, publishes as a GitHub prerelease. `--commit <sha>` creates a missing release tag at that commit. The index records the Piglet source as `git:github.com/<owner>/<repo>@<commit or tag>` unless `--source-ref` names another npm or Git source. Publication contacts GitHub only through `gh`, and it never contacts pi.dev or a PiG service.

### Publish from a monorepo

Use an explicit Piglet-name namespace when several Piglets share a repository (D18). For `pig-with-batteries` in `MichaelKinsy/pigpen`, use the following shape. Replace `<version>` with a published release version:

```bash
pig piglet publish ./piglets/pig-with-batteries/piglet.yaml \
  --to github --repo MichaelKinsy/pigpen --tag-prefix pig-with-batteries/ \
  --sign-key ./pig-with-batteries-signing.key --artifacts ./dist/pig-with-batteries --yes
pig piglet pull 'github:MichaelKinsy/pigpen/pig-with-batteries@<version>'
```

`--tag-prefix` must be `<manifest-name>/`. It produces `pig-with-batteries/v1.2.3` while `release.version` stays `1.2.3`. Omitting the option keeps `v1.2.3`. PiG does not infer a namespace by scanning a checkout, so adding another Piglet cannot change existing release names. Dry runs, existing-release checks, uploads, default Git source refs, and download URLs use the same namespace. URLs escape the slash as `%2F`.

The signed index records the GitHub repository and tag prefix independently of the source ref. A named pull rejects a different Piglet name, version, repository, or prefix. Its receipt retains that signed identity. For independently addable monorepo source, provide `--source-ref 'git:https://github.com/MichaelKinsy/pigpen.git@<full-commit-sha>#subdirectory=piglets%2Fpig-with-batteries'`. Without `--source-ref`, the source ref identifies the repository's commit or namespaced tag, not a selected subdirectory.

The reusable workflow [`docs/examples/piglet-release.yml`](https://github.com/MichaelKinsy/PiG/blob/main/docs/examples/piglet-release.yml) builds each target on a native GitHub runner, attests the build provenance of each Binary, and publishes the release with `--artifacts`. Copy it to `.github/workflows/piglet-release.yml` in the Piglet's repository. Its header shows the workflow that calls it.

## Pull a published Binary

A publisher can place signed per-target Binaries and a signed `piglet-release.json` index on GitHub Releases or another HTTPS host. Pull the Binary for the current target:

```bash
pig piglet pull https://github.com/acme/reviewer/releases/download/v1.2.3/piglet-release.json
```

Select another target or use a GitHub release reference explicitly:

```bash
pig piglet pull github:acme/reviewer@1.2.3 --target linux/amd64
```

PiG verifies the release-index DSSE signature, the complete asset size and SHA-256, the Binary signature trailer, and the Piglet, release, target, and PiG identities before it installs any file. The first successful pull pins the signer key for that Piglet. A later signer change is refused unless you name the new key exactly with `--accept-signer ed25519:<key-id>`. Local revocation and `trust require on` apply to pulled releases.

Pulled Binaries live under `~/.pig/artifacts/piglets/`, and their signed receipts live under `~/.pig/receipts/piglets/`. `pig piglet list` and `show` report them as Binary facets. Pull requests only the supplied index and the asset URL signed into that index; it performs no automatic catalog or PiG service request.

### Update an installed GitHub Binary

```bash
pig piglet update pig-with-batteries
pig piglet update pig-with-batteries --version 2.0.0-rc.1
```

Update reads the repository and tag namespace from the current signed receipt. It enumerates GitHub's public release API and selects the highest stable SemVer in that namespace. It never uses repository-wide `latest`, even for unprefixed releases. Drafts, prereleases, unrelated prefixes, and invalid versions do not enter automatic selection. Use `--version` to request a prerelease explicitly. Discovery fails if the bounded release inventory is incomplete.

Update preserves the installed target unless `--target os/arch` selects another. It refuses rollback, repository or namespace changes, revoked keys, and unapproved signer changes. `--accept-signer ed25519:<key-id>` explicitly authorizes a new signer. The same checksum, size, signature, and manifest checks as pull apply before the current pointer changes. An already-current release is a no-op. An index without signed GitHub identity cannot supply automatic updates. Update does not refresh registered source Piglets or query a catalog. `PIG_OFFLINE` and `PI_OFFLINE` prevent update requests.

### Sigstore keyless provenance

An Ed25519 Piglet signature is offline artifact integrity and local trust policy. Sigstore keyless provenance is separate evidence that a named CI workflow built an artifact. A release workflow can publish GitHub build provenance, and the recipient can verify it explicitly:

```bash
pig verify --provenance \
  --repo acme/reviewer \
  --signer-workflow acme/reviewer/.github/workflows/piglet-release.yml \
  ./pig-reviewer
```

This command delegates to `gh attestation verify`, which checks the Sigstore bundle, certificate identity, and transparency-log inclusion. It requires the GitHub CLI and may contact GitHub's attestation service. PiG never performs this network check at startup. Startup signature verification remains offline.

## What the Binary contains

A Piglet Binary always contains:

- the Stock PiG execution engine;
- one fixed Piglet;
- its resolution and component plan;
- every extension or native component marked as Binary materialized;
- verification data required at startup.

A Binary can still require external items when its plan declares them:

- an external MCP service;
- a secret value;
- a configuration file;
- an interpreted runtime;
- a component supplied by a release or required agent environment.

Do not describe a Binary as self-contained unless its record proves that none of these requirements remain.

## Realization and materialization

Each executable component records two independent facts.

| Axis | Values | Question answered |
|---|---|---|
| Realization | fused, subprocess, external | How does the component execute? |
| Materialization | binary, release, `agentEnv`, external | Where do its bytes and runtime come from? |

A subprocess component does not create a different Binary type. It remains part of the same Piglet application and component plan.

## Fused Go extensions

A compatible Go factory can be fused into the Binary. PiG compiles the extension into the executable and starts it through the same registration contract used by subprocess extensions.

Advantages:

- one target-native executable;
- no target-side Go compiler;
- no extension process startup;
- no serialization cost for process placement;
- exact extension source included in the build closure.

Costs:

- the Binary must be rebuilt when the extension changes;
- the artifact is target specific;
- the extension shares the PiG process failure boundary;
- fused code requires stricter review;
- only compatible Go factories can fuse.

Fused behavior must match source subprocess behavior. Fusion is a delivery optimization, not a second extension API.

## Fused members with external Go modules

A fused extension or frontend member may require Go modules beyond the PiG SDK. The build links them into the Binary through PiG's own module, without writing into the PiG source tree:

- The member's `require` directives join PiG's requirements by Go's minimum version selection. Each module gets the highest version PiG or any member requires, so a member never downgrades PiG. When a member raises one of PiG's own requirements, the build prints a `note:` line naming the module and both versions.
- PiG and its extension SDK are pinned by the PiG source being built. A member that requires a newer SDK version than that source fails the build with an error naming both versions; build it with a PiG source at that version or later.
- The member's `replace` directives for other modules apply to the build, with relative paths resolved against the member. Two members, or a member and PiG, that replace one module differently fail the build.
- Checksums come from the member's `go.sum` together with PiG's `go.sum` and, for a checkout with `go.work`, its `go.work.sum`. Keep the member tidy (`go mod tidy`) so its `go.sum` covers its module graph. The build verifies these checksums under `-mod=readonly`, works offline once the modules are in the Go module cache, and succeeds from a read-only PiG checkout. A missing checksum stops the build instead of being written into the checkout.

The member's `go.mod` and `go.sum`, and the source of each local replacement, are part of the Binary's recorded build inputs, so a change to the member's module graph changes the Binary's identity.

### What the fused vet checks

Before it links fused members, the build vets them for process-global hazards: calls such as `os.Exit`, `os.Chdir`, `log.Fatal`, and writes to standard output, which a fused member would run inside the PiG process instead of an isolated subprocess (see [Fused Go extensions](#fused-go-extensions)). The vet reads Go source and follows imports, so its reach is the source it can read in the build:

- **Vetted:** each member package and every package it imports, directly or indirectly, from the member's own module and from the workspace modules it names.
- **Not vetted:** every other module the members require, direct or indirect, including the modules a local `replace` points to. The vet does not read their source, so a hazard in that code reaches the Binary unchecked. Reading it would mean fetching and parsing the whole third-party graph on every build, and a hazard in a library is not always a defect: a command-line library may call `os.Exit` on a path your member never takes. PiG therefore reports the gap instead of guessing.

When a build fuses members that require such modules, `pig piglet build` prints one `note:` after the vet passes. It lists each unvetted module once as `path@version`, sorted by module path, at the version the build selects: the highest version PiG or any member requires. When a `replace` directive supplies the module's source, the entry ends with `=> ` and the replacement module or directory. A build whose members have no external modules prints no note. PiG and its extension SDK are not listed, because the PiG being built is the one that is linked. The list comes from the `require` directives of the members' and workspace modules' `go.mod` files, so it can also name a module that only the members' tests use. The note follows from those `go.mod` files and the PiG source, which are already part of the Binary's recorded build inputs, so the Binary's record needs no separate entry for it.

Review the listed modules yourself, or keep a module with hazards out of a fused member by running that member as a subprocess extension.

## Subprocess extensions

A normal Piglet Binary can start exact subprocess components when its plan permits them.

Advantages:

- process failure isolation;
- support for Go, Rust, Python, Node, and native programs;
- independent heartbeat and cancellation handling;
- easier source development and reload.

Costs:

- process and IPC overhead;
- explicit runtime requirements;
- more lifecycle and transport handling;
- the Binary might not contain every required runtime.

A prebuilt native subprocess component can avoid a target-side compiler. An interpreted component still needs its recorded runtime.

### TypeScript and JavaScript extensions

A Piglet Binary can carry TypeScript and JavaScript extensions. The build packs each Node extension's sources into one archive per Node cell and embeds the archive in the Binary. It archives a directory extension whole, including its `node_modules`, but not its `.git` directory, and it archives a single-file extension as that file alone. A symbolic link that points outside the extension directory fails the build (a link to the directory itself is kept), because the Binary would run without the code the link points to. Keep an extension's local imports and dependencies inside its directory.

The Binary does not contain Node. It runs the extension with the Node on the user's `PATH`, which must be version 22.13 or newer, the same requirement Stock PiG has. At startup the Binary checks the archive against the digest in its embedded cell manifest and extracts it once per digest under `piglet-binary-cells/node/` in the PiG configuration directory (`~/.pig` by default). A later start reuses that directory and does not check its files again, so a file changed or deleted there stays changed until you delete the `<digest>` directory; the next start then extracts the archive again. It then plans, starts, and reloads the extension through the same Node cell path that Stock PiG uses for the same extension. The extension registers the same tools, commands, and handlers, in the same order.

The component plan records a Node extension as a subprocess component with binary materialization and a `node` runtime requirement. The signed manifest lists the archive among its embedded files, and the signature covers its bytes.

The Binary costs what the extension costs in Stock PiG. On an Apple M4 Pro, a Binary with one fused Go extension and one small TypeScript extension is 16,528 bytes larger than the same Binary without the TypeScript extension. Its `-p` run with the faux provider took 178 ms (median of 10), against 179 ms for Stock PiG with `-e` and the same extension, and 34 ms for the Binary without it. Almost all of that time is Node starting the extension.

When Node is missing or too old, the Binary behaves like Stock PiG. It reports the failed extension, names the Node version it needs, and prints the `-ne` hint. It then exits with status 1 before the session starts:

```text
Error: Failed to load extension "/Users/me/.pig/piglet-binary-cells/node/<digest>/sources/greeter": Failed to load extension: reload packed Node cell packed-node:<key>: spawn:runtime_unavailable: TypeScript extensions need Node.js 22.13 or newer; node was not found on PATH
Hint: Start without extensions using "pig -ne".
```

A Piglet that strips `node-extensions` cannot carry a Node extension. The build stops with the conflict described in [Stripped built-ins](#stripped-built-ins).

## External components

An external realization connects to a separately managed service. The Binary records the requirement but does not embed or operate that service.

Use an external realization for network services that have their own lifecycle, scaling, credentials, or deployment owner.

## Require all extensions to fuse

A Piglet can reject subprocess fallback:

```yaml
build:
  extensionRealization: fused
```

The build then requires every selected extension to be a fuse-compatible Go factory. If one extension resolves to a subprocess, the build fails and names that extension and the reason.

PiG Standard uses this requirement. Every extension added to PiG Standard must:

- use the public Go extension factory contract;
- work in source mode through the normal subprocess host;
- work as a fused component;
- pass the same behavior and lifecycle tests in both modes.

## Frontend members

A Piglet Binary can fuse one frontend member that draws the interactive mode in place of PiG's terminal renderer (D91). Pi has no equivalent: Stock PiG and a Piglet without a frontend member paint exactly as Pi does.

```yaml
slots:
  frontend:
    member: ./frontend
```

`slots.frontend.member` names a directory inside the directory of the Piglet file that names it. It holds a Go module whose root package exports:

```go
func Frontend() frontend.Frontend
```

The `frontend` package is `github.com/MichaelKinsy/PiG/extensions/sdk/frontend`. A frontend member depends on PiG only through the Go SDK module; it may require other Go modules, as described in [Fused members with external Go modules](#fused-members-with-external-go-modules). It is not an extension: it has no wire protocol and runs only fused, and only the native builder fuses it. A Piglet may have a frontend member and no extensions, as a Piglet may have neither (see [Build a Binary](#build-a-binary)).

A Piglet that extends another inherits its frontend member, which still resolves against the base Piglet's directory. The child replaces it by naming its own `slots.frontend`, or removes it so PiG's terminal renderer applies again:

```yaml
extends:
  source: local:./pig-tern/piglet.yaml
  remove:
    slots: [frontend]
```

`pig piglet show` lists the frontend member in its `Slots` section as `frontend  replaced(<member directory>)`, beside any stripped built-ins (see [Piglets](piglets.md#strip-built-ins)). The Binary's own embedded Piglet carries no `slots`, because the member is compiled into the Binary.

On every interactive start, PiG calls the member's `Open` after the terminal enters raw mode and before the first paint. A member that returns no session leaves the terminal renderer in place. A session receives the interactive screen as retained-tree frames: transcript entries in the main region and the input dock in the dock region. Tool calls arrive as tool nodes with their arguments, status and output, and an edit's result with its unified diff; a call the user aborted has the status `cancelled`. User and assistant messages arrive as Markdown nodes with their role and Markdown source, and thinking as thinking nodes that say whether the user hid it; an assistant message is one node per part, and the part still being generated is marked as streaming, so a frontend can append what a frame adds. Every other component arrives as the terminal lines it rendered, at the width the session reports for its region. Terminal input that the session claims never reaches the editor.

The input editor arrives as an editor node with its text, its cursor in UTF-16 code units, a placeholder and whether it is sendable. PiG draws no ANSI border around it, since the frontend frames it. Above it comes a working node while PiG works, compacts, summarizes a branch or waits to retry: its kind, its message, and an extension's spinner frames and interval, without the current frame, so the frontend animates the spinner. Below it come the lines of its completion list and the components under it, then a footer node while PiG's footer shows: the working directory, git branch and session name, the session's token and cost totals, the cache-hit rate, the context usage, the model with its provider and thinking level, and the extension statuses. A footer that an extension sets replaces the footer node and arrives as lines. While a component PiG does not model replaces the editor, or an overlay that takes the keys shows, the dock arrives as lines. The session acts on the editor through `Env`: `Edit` replaces a range of its text as one undo step, `Undo` steps back through the editor's own history, and `Send` submits a prompt through the path Enter takes, so commands, multi-line prompts and steering behave as typed. PiG runs these on its own loop in the order they arrive, ignores an edit computed against text that has changed since, and reports the editor sendable only once its input loop runs and while the editor has the keys.

A selector or dialog that takes the editor's place, such as `/model`, `/resume`, `/tree`, `/fork`, `/login`, the thinking level picker or an extension's select or confirm dialog, arrives in its place as a selector node: its title and description, the search text, the items shown in the order the arrow keys step through them with a label, a detail, a check for the current or enabled entry and a tree depth, the selected item, its tabs (such as the scope of the model and session lists), a status message, a loading flag and a confirm question. `/settings` arrives as a settings node: each setting with its value, the values it cycles through, and whether it opens a submenu, which then arrives as a selector under the same id. Keys still reach the selector as typed. A session acts on one with `Env.Act`: highlight, choose, dismiss, filter, switch tab or set a value. PiG performs each action as the keys it stands for, so the selector behaves exactly as with the keyboard. Overlays, such as an extension's custom component shown as an overlay, arrive in an overlay region as overlay nodes with their lines, width, anchor and whether they take the keys. Components PiG does not model, such as an extension's input or editor dialog, the login dialog, and the session rename and tree label fields, arrive as lines.

The first frame also names the session file, the file the session persists to, or none for a session that is not persisted (`--no-session`). A later frame names it again only when it changes, such as after `/new`, `/resume`, `/fork` or `/clone`. A frontend can pass it on, for example to let another program read the conversation.

A session is also the terminal that PiG's interactive mode reports its program status to: working while a run or compaction goes on, blocked while a dialog or a login waits for the user, then done, error or idle (the OSC 7501 Program Status Protocol of Pi 1.1.0). A session that implements `frontend.ProgramStatusSession` receives each change through `ProgramStatus` as the report Pi writes to a supporting terminal, including the report that clears the status. PiG calls it on its owner loop in the order the status changed, so it must return without blocking. It clears the status before `Suspend` and `Close` and reports the latest one again after `Resume`. A session without the method receives nothing. Either way, while a session may draw, PiG writes neither the OSC 7501 support query nor a report to the terminal itself.

When a session asks to fall back, or fails to draw a frame, PiG closes it, repaints with its configured terminal renderer and shows a warning. While a session draws, PiG refuses to switch the TUI mode. `--print`, JSON and RPC modes never consult the member.

The member's source is part of the Binary's identity: its tree digest is a `frontend` input in the resolution record.

## Stripped built-ins

A Piglet's `strip` list (D92, see [Piglets](piglets.md#strip-built-ins)) carries into its Binary. The native builder passes one Go build tag per stripped built-in extension, model API and compile-out feature (`pig_strip_<id>`, for example `pig_strip_mcp` or `pig_strip_node_extensions`), so the Binary does not link that code; the build prints the tags it uses. The baked Piglet keeps the list, so the Binary also disables the runtime-only entries (tools, commands, `themes`, `skills`, `prompt-templates`) at startup. A Piglet cannot compile out an extension runtime its own extensions run on: a Node or Python extension, or a Go or Rust extension the Binary builds at runtime, fails the build with the strip entry named. A Piglet whose strip list removes every built-in tool and `/quit` together does not build either (see [Piglets](piglets.md#strip-built-ins)).

Stripping cuts size and startup work. On darwin/arm64 a Piglet Binary with one fused Go extension is 62.2 MB; stripping MCP, codemode, llama.cpp, tool-search and three provider APIs makes it 54.2 MB, and stripping every binary ID makes it 31.7 MB.

The Binary record lists each stripped ID with its kind and disposition in its `strip` field, so the strip list is part of the Binary's identity and record digest. A Binary without a strip list has no `strip` field. `pig piglet show` prints the list under each Binary in a `Slots` section (`extensions.mcp  stripped(binary)`, `tools.bash  stripped(runtime)`), and `pig piglet show --record <path>` prints it for one record.

A keep-mode list ([Piglets](piglets.md#keep-mode)) reaches the Binary expanded: the build compiles out every built-in of that list the Piglet does not keep, and the `strip` field lists each one. The record states the intent too: `stripKeep` lists each keep-mode list with the IDs it keeps (`[{"kind": "extension", "ids": []}]`), and `pig piglet show` prints it above the expanded rows (`extensions  keep([])`). A deny-mode Piglet has no `stripKeep` field. The baked Piglet keeps the keep lists, not their expansion, and the Binary expands them again at startup against its own table, which is the table it was built from.

Every Binary record also keeps `stripTable`, the strip table of the PiG that built it, one list of IDs per kind. A later PiG compares its own table with it and prints the [strip delta report](piglets.md#strip-delta-report) on `pig piglet build` and `pig piglet show`: the new built-ins a keep-mode list leaves out and the ones a deny-mode list now includes.

## Startup verification

A Piglet Binary verifies its embedded Piglet, component closure, and optional Ed25519 signature before command dispatch. A signed Binary refuses to run when any executable or signature byte changes, within the limit that the verification cache below describes. An unsigned Binary reports unsigned and runs unless the local trust policy requires every Piglet Binary to carry a trusted signature.

Signature verification reads and hashes the whole executable. For a 63 MB darwin/arm64 Binary on an Apple M4 Pro the hash takes about 25 ms. An unsigned Binary has no signature block to check and skips the hash.

Signature verification reads and hashes the whole executable at every start. For a 62 MB darwin/arm64 Binary on an Apple M4 Pro this adds about 26 ms. An unsigned Binary has no signature block to check and skips the hash.

Startup does not silently:

- install a Package;
- download a missing component;
- substitute a same-named executable from `PATH`;
- log in to a provider;
- weaken an environment requirement;
- replace a failed fused extension with a subprocess.

A missing or tampered requirement causes a clear failure.

### Verification cache

A signed Binary hashes its executable only when its file changed since its last passing start (D18). After every startup check passes, the Binary records the check in `state/piglet-verify/verified.json` under the PiG config root (`$PIG_HOME`, else `$XDG_CONFIG_HOME/pig`, else `~/.pig`). The entry holds:

- the Binary's path with symbolic links resolved;
- its file identity: device, inode, size, modification time, and status-change time; on Windows the volume serial number, file index, size, last write time, and change time;
- the SHA-256 of its signature block;
- the trust policy that applied: `require on`, the trusted, revoked, and embedded keys.

At the next start the Binary still reads its signature block and checks the signature, the signed size, the signer under the current trust policy, and the signed manifest. When every recorded field matches, it skips the hash of its executable bytes. Any difference causes a full check: a new path, a touched or rewritten file, a file replaced by another one, a changed trust store or `require on`. A missing, corrupt, unreadable, or not owner-only cache also causes a full check. An entry never vouches for another path.

- A failed check is never recorded. A check is recorded only after the signed manifest matches the Binary.
- The cache file is readable and writable by its owner only and is replaced atomically. It keeps one entry per Binary path, at most 8, and drops the oldest entry first.
- `pig verify` and `pig piglet verify` never read the cache. They always hash the whole file.
- When PiG cannot write the cache, the Binary still starts and prints `pig: warning: write Piglet verification cache: ...` on stderr. The next start checks the Binary in full again.

On an Apple M4 Pro (median of 10, `-p` with the faux provider), Stock PiG took 31.1 ms. The 63 MB signed Binary took 56.1 ms with a full check at every start, 59.3 ms for a start that also writes the cache, and 33.3 ms for a start with a matching cache entry.

What the cache keeps and what it gives up:

- It keeps detection of every change made through a file system that records a status-change time (on Windows, NTFS's change time). Writing to the Binary changes that time even when the writer restores the modification time, and replacing the Binary changes its inode, so the next start hashes the new bytes and refuses a change.
- It gives up detection, at start, of corruption that keeps the size and every recorded time, such as a storage fault below the file system, and of an in-place write that restores the modification time on a file system without a status-change time, such as FAT. On Windows a writer can also set the change time back through the file information API; such a writer is not detected at start. `pig piglet verify` still detects all of these.
- The startup check is a self-check. Anyone who can rewrite the Binary can also rewrite the code that checks it, with or without a cache. The check does not stop an attacker who can write the Binary. Protect the Binary with file permissions, and run `pig piglet verify` on a Binary you did not build before you run it.

## Failure and recovery

Fusion and subprocess execution have different physical failure boundaries, but they use the same extension behavior contract.

For subprocess components, PiG provides bounded heartbeat, cancellation, crash detection, quarantine, reload, and replacement behavior. A failed replacement does not remove the last working extension set.

PiG does not automatically replay an interrupted tool or command. A replay could duplicate file changes, messages, or network actions. The caller decides whether an operation is safe to retry.

A fused extension cannot lose a process socket because it runs inside PiG, but it can still panic, block, or misuse resources. Fused extensions therefore require lifecycle, shutdown, and leak verification before release.

See [Extensions](/docs/latest/extensions) for the complete tradeoff and recovery model.

## Portability

Build one Binary per target:

```yaml
build:
  targets:
    - linux/amd64
    - darwin/arm64
    - windows/amd64
```

Do not claim target support until the artifact passes native verification on that target.

A Binary built on one target does not make an external service, secret, or interpreter portable. The record keeps these requirements visible.

## Source mode versus Binary mode

| Need | Use |
|---|---|
| Edit and reload extension source | Host Piglet source mode |
| Inspect a composition before release | Host Piglet source mode |
| Hand off one executable | Piglet Binary |
| Require one native all-fused application | Piglet with `extensionRealization: fused` |
| Carry a complete operating environment | Piglet Image when that producer is available |

## Related documentation

- [Piglets](/docs/latest/piglets)
- [Extensions](/docs/latest/extensions)
- [Derivative harnesses](/docs/latest/derivative-harnesses)
- [Packages](/docs/latest/packages)
