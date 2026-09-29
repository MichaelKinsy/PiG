# Experimental CLI entry

Pi 0.87.1 publishes `dist/bundle/cli.js` as its stable command and excludes its experimental directories from published files (`packages/coding-agent/package.json:9-11,29-34`). Its separate source entry is `packages/coding-agent/src/experimental/cli.ts`. See the [Pi reference implementation](https://github.com/earendil-works/pi).

PiG selects the development entry at build time:

```sh
go build -tags=pig_experimental -o bin/pig-experimental ./cmd/pig
```

The source lives in `cmd/pig/main_experimental.go`. This directory documents that entry; it is not a second Go main package to build. The build tag selects an entrypoint permanently. It does not replace runtime dependencies with stubs or omit dependencies from the selected entry.

The normal build remains:

```sh
go build -o bin/pig ./cmd/pig
```

The normal entry does not import experimental runtime dispatch. The experimental executable uses the same stable CLI implementation in the same binary when experimental dispatch does not apply. It does not execute a sibling binary or search PATH. Both entrypoints preserve the same package globals and linker variables.

## Dispatch

Set `PI_EXPERIMENTAL=1` to select the experimental `server` or `client` command when that word is the first argument. Other arguments, or any other value of `PI_EXPERIMENTAL`, enter the stable CLI implementation.

Experimental parsing happens before stable version processing. For example, `server --server-id invalid --version` reports experimental parser errors when experiments are enabled. When experiments are disabled, the same executable delegates those arguments to the stable CLI, including its version precedence. Stable version output uses PiG's composite version under D63.

The client uses the TUI only when no prompt is supplied and both stdin and stdout are terminals. Otherwise it prints discovered Session addresses, an attachment result, or streamed prompt text. The server reports its ID and socket. SIGINT and SIGTERM close an initialized foreground server and join cleanup.

The selected native executable also dispatches the internal coordinator, server, and Session-worker roles used by `SpawnInternalProcess`. Internal roles are not public CLI options. The role environment variable is validated and consumed once so descendants do not inherit the selected role accidentally.

The server uses the existing D2 configuration-identity policy. Experimental Radius is designed out (D64): the server opens no hosted relay and prints no Radius status, `radius://` addresses use the unsupported-transport diagnostic, and `--auth-token`/`--auth-token-file` are unsupported options.

The client TUI loads theme resources with the stable CLI's precedence: project themes when the project is trusted, user themes, then Package themes.

The development entry is not added to release artifacts.
