# port-104-env: `@earendil-works/pi-env` in PiG

## What pi-env is

`packages/env` gives a Durable `ExecutionEnv` on another machine. A small daemon, `pi-env`, runs there and serves a framed protocol on stdin/stdout (`docs/protocol.md`: `PI-ENV <token>` sync line, `u32 length | u8 type | u32 id | u32 jsonLength | json | payload`, request/result/error/event/cancel/ping, 16 MiB frames, 5 s pings, 30 s silence limit). `RemoteExecutionEnv` (the client) applies Node's path, error and read rules; the daemon performs system calls, commands, line scans and watching. Results must equal `NodeExecutionEnv` running on the remote machine.

| Upstream | Role |
|---|---|
| `src/connection.ts` | `Connection`: lazy daemon start, request/cancel/event dispatch, pings, session ids (handles belong to one daemon session), reconnect |
| `src/remote-env.ts` | `RemoteExecutionEnv`: every `ExecutionEnv` method over the protocol; pipelined reads (8 x 256 KiB) and writes (8 x 512 KiB); Node `readFile` read sizes |
| `src/watch.ts` | `RemoteWatcher`: the daemon's watch request, reconnect with 1 s..30 s backoff and `overflow` after a lost connection |
| `src/ssh.ts` | `ssh` arguments (BatchMode, strict host keys under a fixed alias, no forwarding), host key scan/accept/forget, platform detection (POSIX probe, Windows PowerShell), daemon deployment verified by SHA-256 (POSIX `cat` over stdin, Windows base64 lines ending `PI-ENV-END` because Windows sshd may never end stdin), `loginShell`, lazy `sshConnection` |
| `daemon/` (Rust) | the daemon: file ops, `exec` with spill/timeouts/output windows, `watch` (a port of Durable's `NodeFileWatcher`), line scans, output queues (control before bulk) |

Consumers: `RemoteExecutionEnv` is passed wherever an `ExecutionEnv` goes (Durable harness tools, the coding agent's durable mode). Nothing in Pi's coding-agent imports `pi-env`; it is a library.

## Go mapping

- `env/` (package `env`, import `github.com/MichaelKinsy/PiG/env`): `Connection`, `RemoteExecutionEnv` (implements `durable/env.ExecutionEnv`), `RemoteWatcher`, `ssh.go`. TS Promises that are awaited become blocking calls with `context.Context`; in-flight pipelines use a `Call` started in order (frames go out in call order, which the daemon's write-chunk ordering needs) and awaited afterwards. `AbortSignal` is the call's context (`context.AfterFunc` sends `cancel`).
- `env/daemon/` (package `daemon`) is the daemon as a Go library (`Serve(in, out, token)`), `env/cmd/pi-env` its `main`. The wire is Pi's byte for byte.
- The daemon reuses `durable/env/node` where the Rust daemon re-implements `NodeExecutionEnv` (`exec`, `watch`), and stdlib plus `internal/nodeerrno` for the low-level file calls. No Rust is built.
- Tests: Pi's suite over a pipe to the daemon. The test binary re-executes itself as the daemon (no build step, no network), or uses the binary named by `PI_ENV_DAEMON` as Pi does. The SSH bootstrap tests run against a disposable `sshd` on 127.0.0.1 when one is installed, as Pi's do; `PI_ENV_SSH_HOST` gates the external-server suite.

## Open decisions (owner)

1. The daemon is a Go program, not Rust: same wire and command line, `exec` and `watch` through `durable/env/node`. A numbered divergence entry waits for the owner's approval.
2. `hello` reports Rust's `std::env::consts` spellings (`macos`, `x86_64`, `aarch64`) and the pinned Pi version.
3. Where the packaged daemons live: `<directory of the executable>/pi-env/pi-env-<platform>-<arch>/pi-env[.exe]`, overridable with `PI_ENV_DAEMON_DIR` or `SshConnectOptions.Binary`. `make build-env-daemons` builds them; no release workflow was changed. Android on x86-64 runs the linux/amd64 build, because Go links `GOOS=android` without cgo only on arm64; that build sets the daemon's target system to `android` (`-ldflags -X`), so its `hello.os` and its `tmpdir` fallback (`$PREFIX/tmp`) are Android's, as Pi's Android daemon reports.
4. `exec`'s spill file uses the NodeExecutionEnv temporary-directory rule of PiG, not Node's `os.tmpdir()`.
5. PiG's `NodeExecutionEnv` lists a directory entry whose name is not valid UTF-8 under its raw name; Node (and the daemon) decode the name with replacement characters and `lstat` that, so `listDir` fails with `ENOENT`. `env/differential_test.go` states Node's result for the remote environment and does not compare with the local one.

## Not generated here

`make go-stubs PACKAGE=env OUT=env IMPORTS=@earendil-works/pi-durable=github.com/MichaelKinsy/PiG/durable,@earendil-works/pi-durable/env=github.com/MichaelKinsy/PiG/durable/env,@earendil-works/chord=github.com/MichaelKinsy/PiG/chord TEST_MAPPING=1`, `make interface-go` and `make generate` need the pin that contains `packages/env`; they run when this branch is merged with that pin bump. Against that tree the generator reports no missing declarations and five test rows `ported` (conformance, differential, remote, ssh, ssh-external).
