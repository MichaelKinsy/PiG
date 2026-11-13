# Go hot reload

PiG builds a Go extension from its source. After you edit the source, `/reload` builds the extension again and starts the new build in the running session. You do not restart `pig`, and the session keeps its messages.

This page uses an extension that `-e` loads from its source directory. This page does not cover a Go extension that is fused into a Piglet Binary.

## Steps

1. Create the extension with `pig extension init greeter --lang go`. The command writes `greeter/go.mod` and `greeter/extension.go`, and names the package and the extension after the directory. Keep `go.mod`. Replace `greeter/extension.go`:

```go
// greeter/extension.go: one tool and one command that share a greeting.
package greeter

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const greeting = "hello from v1"

var calls int

func Extension() *sdk.Extension {
	ext := sdk.New("greeter")
	ext.Tool("greet", "Return the greeting", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]any{"content": "tool: " + greeting}, nil
	})
	ext.Command("greet", "Show the greeting and count the calls", func(ctx sdk.Context, args string) error {
		calls++
		ctx.Notify(fmt.Sprintf("%s (call %d)", greeting, calls), "info")
		return nil
	})
	return ext
}
```

2. Start a session with `pig -e ./greeter`. The first start builds the extension and shows `Building extensions [0/1] (first run, will be cached): greeter`. Wait for `Building extensions [1/1] (first run, will be cached)`. A later start with the same source uses the cached build and shows no build line.
3. Enter `/greet`, the command that `ext.Command("greet", ...)` adds. PiG shows `hello from v1 (call 1)`: `ctx.Notify(text, level)` shows a line in the session, and the level is `info`, `warning` or `error`. Enter `/greet` again. PiG shows `hello from v1 (call 2)`.
4. Change `"hello from v1"` to `"hello from v2"` in `greeter/extension.go`. Save the file.
5. Enter `/reload`. PiG shows `Reloading keybindings, extensions, skills, prompts, themes, and context files...`, then the same two build lines as in step 2, then `Reloaded keybindings, extensions, skills, prompts, themes, and context files`.
6. Enter `/greet`. PiG shows `hello from v2 (call 1)`. Ask the model to call the tool `greet`. The tool now returns `tool: hello from v2`.

The first build of an extension compiles the SDK and takes the longest. A build after an edit compiles only the extension, because Go keeps the rest in its build cache.

## State across a reload

- A reload after an edit starts a new process for the extension: package-level variables start again. In step 6 the count is 1.
- A reload with no edit keeps the process and calls `Extension()` again: package-level variables keep their values, and what the factory creates starts again ([D70](../../parity/DIVERGENCES.md)). A variable that `Extension()` declares and its handlers capture is such a value. PiG shows the `Reloading` and `Reloaded` lines and no build line.
- Keep state that must survive in the session. State that belongs to no session goes in a file below `~/.pig/state/<extension-id>/`.
- The session, its messages and the model stay.
- Listeners on the [event bus](event-bus.md) and views that the extension mounted end with the reload. The new factory registers them again.

## When the build fails

If the edited source does not compile, `/reload` shows the error under `[Extension issues]`. For example, after `return ext` became `return ext +`:

```text
Failed to load extension: parse /home/you/greeter/extension.go:25:1: expected operand, found '}'
```

PiG then runs without the extension: `/greet` is no longer a command. The other extensions stay loaded. Correct the source and enter `/reload` again.

## Limits

- `/reload` waits for the model. While a response is running, PiG shows `Wait for the current response to finish before reloading.` and does not reload.
- Every `.go` file of the extension is part of its source. `/reload` builds an edit to any of them.

## Differences from Pi

Pi has `/reload` and no Go extensions: it loads TypeScript in its own process. PiG compiles a Go extension against the SDK of the `pig` that runs it (D109) and runs it as a separate process.
