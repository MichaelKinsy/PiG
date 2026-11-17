# Event bus

The event bus lets one extension call another. An extension emits a value on a named channel. Every extension that listens on that channel receives the value. The extensions can be in different languages: a Go extension can ask a Node or a Python extension for a result.

The bus is Pi's `pi.events`. It has two calls: `emit(channel, data)`, and `on(channel, handler)`, which returns the function that unsubscribes. It has no reply call. For a reply, use a second channel, as the example does.

## Example: a Go extension asks and a Node extension answers

Create the Go extension with `pig extension init asker --lang go`. The command writes `asker/go.mod` and `asker/extension.go`, and names the package and the extension after the directory. Keep `go.mod`. Replace `asker/extension.go`:

```go
// asker/extension.go: asks for a place on the event bus and shows the reply.
package asker

import (
	"fmt"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var requests atomic.Int64

func Extension() *sdk.Extension {
	ext := sdk.New("asker")
	ext.Command("locate", "Ask another extension where a place is", func(ctx sdk.Context, name string) error {
		id := fmt.Sprintf("asker-%d", requests.Add(1))
		replies := make(chan map[string]any, 1)
		off, err := ctx.Events().On("geo:found", func(_ sdk.Context, data any) error {
			if reply, ok := data.(map[string]any); ok && reply["id"] == id {
				select {
				case replies <- reply:
				default:
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		defer off()
		if err := ctx.Events().Emit("geo:locate", map[string]any{"id": id, "name": name}); err != nil {
			return err
		}
		select {
		case place := <-replies:
			ctx.Notify(fmt.Sprintf("%v is at %v, %v", place["name"], place["lat"], place["lon"]), "info")
		default:
			ctx.Notify("no extension answers geo:locate", "warning")
		}
		return nil
	})
	return ext
}
```

`ext.Command("locate", ...)` adds the command `/locate`, and `name` is the text after it. The command sends a request on `geo:locate` and reads the reply from `geo:found`. `Emit` returns after every listener has returned. The listener in `geo.mjs` emits the reply before it returns, so the reply is in `replies` when `Emit` returns. The request carries an `id`, and the answer repeats it. Every listener on `geo:found` receives every reply, so the listener takes only the reply with its own `id`. `ctx.Notify(text, level)` shows a line in the session; the level is `info`, `warning` or `error`.

The answering extension is one file:

```js
// geo.mjs: answers geo:locate on the event bus.
const places = { Paris: [48.86, 2.35], Lagos: [6.52, 3.38] };

export default function (pi) {
  pi.events.on("geo:locate", (request) => {
    const [lat, lon] = places[request.name] ?? [0, 0];
    pi.events.emit("geo:found", { id: request.id, name: request.name, lat, lon });
  });
}
```

Load both extensions and type the command:

```text
pig -e ./asker -e ./geo.mjs

/locate Paris
Paris is at 48.86, 2.35
```

The order of the two `-e` options does not change the result. Without `geo.mjs` no extension listens on `geo:locate`. `Emit` still returns no error, and the command shows `no extension answers geo:locate`.

The same answer in Python, loaded with `pig -e ./asker -e ./geo.py`. PiG calls `new_extension()` and supplies `pig_sdk`; you install nothing:

```python
# geo.py: answers geo:locate on the event bus.
import pig_sdk

PLACES = {"Paris": (48.86, 2.35), "Lagos": (6.52, 3.38)}


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("geo")

    def locate(ctx, request):
        lat, lon = PLACES.get(request["name"], (0, 0))
        ctx.events.emit("geo:found", {"id": request["id"], "name": request["name"], "lat": lat, "lon": lon})

    ext.events.on("geo:locate", locate)
    return ext
```

## Example: the answer comes later

An answering extension can need time, for example to read a file or to call a service. `Emit` does not wait for the part of an `async` Node listener that follows its first `await`. The asking extension then waits for the reply itself, with a time limit. Create the extension with `pig extension init waiter --lang go`. Replace `waiter/extension.go`:

```go
// waiter/extension.go: asks for a place on the event bus and waits for a reply that comes later.
package waiter

import (
	"fmt"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var requests atomic.Int64

func Extension() *sdk.Extension {
	ext := sdk.New("waiter")
	ext.Command("locate", "Ask another extension where a place is and wait for the reply", func(ctx sdk.Context, name string) error {
		id := fmt.Sprintf("waiter-%d", requests.Add(1))
		replies := make(chan map[string]any, 1)
		off, err := ctx.Events().On("geo:found", func(_ sdk.Context, data any) error {
			if reply, ok := data.(map[string]any); ok && reply["id"] == id {
				select {
				case replies <- reply:
				default:
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		defer off()
		if err := ctx.Events().Emit("geo:locate", map[string]any{"id": id, "name": name}); err != nil {
			return err
		}
		select {
		case place := <-replies:
			ctx.Notify(fmt.Sprintf("%v is at %v, %v", place["name"], place["lat"], place["lon"]), "info")
		case <-time.After(2 * time.Second):
			ctx.Notify("no extension answered geo:locate in 2 s", "warning")
		}
		return nil
	})
	return ext
}
```

The last `select` waits for the reply or for the end of two seconds, whichever comes first. The listener on `geo:found` runs while the command waits.

A Node extension that answers after an `await`:

```js
// slow-geo.mjs: answers geo:locate after an await.
const places = { Paris: [48.86, 2.35], Lagos: [6.52, 3.38] };

export default function (pi) {
  pi.events.on("geo:locate", async (request) => {
    await new Promise((resolve) => setTimeout(resolve, 200));
    const [lat, lon] = places[request.name] ?? [0, 0];
    pi.events.emit("geo:found", { id: request.id, name: request.name, lat, lon });
  });
}
```

With `pig -e ./waiter -e ./slow-geo.mjs`, `/locate Paris` shows `Paris is at 48.86, 2.35`. With `pig -e ./waiter` alone, the command shows `no extension answered geo:locate in 2 s` after two seconds.

A Python listener is a plain function, and `emit` waits until it returns. A Python extension that answers later emits from a thread:

```python
# slow-geo.py: answers geo:locate from a timer thread.
import threading

import pig_sdk

PLACES = {"Paris": (48.86, 2.35), "Lagos": (6.52, 3.38)}


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("slow-geo")

    def locate(ctx, request):
        def answer():
            lat, lon = PLACES.get(request["name"], (0, 0))
            ext.events.emit("geo:found", {"id": request["id"], "name": request["name"], "lat": lat, "lon": lon})

        threading.Timer(0.2, answer).start()

    ext.events.on("geo:locate", locate)
    return ext
```

## Calls

| SDK | The bus | Listen | Emit |
|---|---|---|---|
| Node | `pi.events` | `on(channel, (data) => {})` returns the unsubscribe function | `emit(channel, data)` |
| Go | `ext.Events()`, `ctx.Events()` | `On(channel, func(ctx sdk.Context, data any) error)` returns `(unsubscribe func(), err error)` | `Emit(channel, data)` returns an error |
| Python | `ext.events`, `ctx.events` (properties, no parentheses) | `on(channel, handler)` calls `handler(ctx, data)` and returns the unsubscribe function | `emit(channel, data)` |
| Rust | `ext.events()`, `ctx.events()` | `on(channel, handler)` calls `handler(&Context, &Value)` and returns a `Subscription`; `unsubscribe()` removes it | `emit(channel, data)` |
| Lisp | `(extension-events ext)`, `(ctx-events ctx)` | `(events-on bus channel handler)` calls `(handler ctx data)` and returns the unsubscribe function | `(events-emit bus channel data)` |

Inside a handler, use the bus of the handler's context (`ctx`). Use the bus of the extension (`ext`) while the factory runs.

## Rules

- **Payload.** A payload is a JSON value. To or from a Go, Rust, Python or Lisp extension it crosses by value. Go receives an object as `map[string]any`, an array as `[]any` and a number as `float64`. Between two Node extensions the listener receives the emitter's own object, as in Pi.
- **Order.** Listeners run in the order in which they subscribed, across all extensions. `emit` returns after the last listener has returned. A listener can emit, and that emit completes before the listener returns.
- **Async listeners.** An `async` Node listener returns at its first `await`. `emit` does not wait for the rest of that listener, as in Pi. A reply that a listener emits after an `await` arrives after the asking `emit` has returned. An extension that asks such a listener must wait for the reply itself, as the second example does.
- **No timeout.** A listener that does not return blocks its emitter.
- **No listener.** An emit that no extension listens to does nothing and reports nothing. The channel `error` is the exception: an emit on `error` with no listener fails.
- **A listener fails.** The error does not reach the emitter or the listeners after it. PiG reports it as an error of the extension that owns the listener, as it reports the error of a `pi.on` handler: the interactive chat shows `Extension "<path>" error: <message>` with the stack, RPC mode sends an `extension_error` event whose event is `events:<channel>`, and print mode writes `Extension error (<path>): <message>` to standard error. Nothing is written over the terminal. A listener that throws is reported while `emit` runs, so before the command that emitted answers. An `async` listener that fails after its first `await` is reported when it fails, after `emit` returned: RPC mode sends the event after the command's response, and print and JSON mode show nothing once their session has ended. The error of a handler that fails after its session was replaced is dropped.
- **No stored events.** A listener receives only the emits made after it subscribed. An emit made while a factory runs reaches only the extensions that loaded before that factory.
- **Reload.** `/reload` and a session replacement remove the listeners of the replaced extension. The new extension subscribes again when its factory or its handler runs.
- **One bus.** All extensions of a session share the channel names. Start a channel name with the name of the extension that owns it.

## Differences from Pi

Pi runs every extension in one Node process, so every listener receives the emitter's object. PiG runs extensions in several processes. Between Node extensions PiG keeps Pi's behavior except for the scheduling limits of [D83](../../parity/DIVERGENCES.md), for example the order of independent timers in two processes. Go, Rust, Python and Lisp extensions do not exist in Pi; their payloads cross by value (D83).
