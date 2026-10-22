# Your first extension

This page builds, checks and runs one Go extension, then edits it and reloads it. It takes about ten minutes, most of it waiting for the first compile. You need two things first:

- PiG signed in to a model ([Your first ten minutes](/docs/latest/first-run)).
- The Go toolchain. Go is the programming language of this extension, and PiG uses Go's compiler to build it. Run `pig setup`: the `go` row must show a path. If it shows `missing`, run `pig setup go`, which installs a verified Go under `~/.pig/toolchains/go` ([Command line by example](/docs/latest/cli-examples)). The extension needs Go 1.26 or newer. An older Go (1.21 or newer) on your `PATH` downloads a newer toolchain by itself on the first build, unless `GOTOOLCHAIN` is set to `local`.

You also need a text editor to change `extension.go` in step 4. Any editor that saves plain text works.

An extension adds behaviour: a tool the model can call, a slash command you can type, a handler for an event. [Extensions](/docs/latest/extensions) is the reference. PiG's extension events and the `tool_call` result follow Pi 1.4.0 ([Pi's extensions reference](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/extensions.md)); Pi's extensions are TypeScript, and the Go SDK on this page is a PiG addition. A skill or a prompt template needs no code, and is the better choice for instructions ([Skills](/docs/latest/skills), [Prompt templates](/docs/latest/prompt-templates)).

Run the commands below in a terminal, in a folder where you keep projects. `./review` means a folder named `review` inside the current folder.

## 1. Create it

```bash
pig extension init ./review --lang go
```

```text
Created go extension "review" in /home/you/project/review
  go.mod
  extension.go

SDK: /home/you/.pig/state/pigsdk/sdk-0cd9eb109aaf2c5f

Next steps:
  pig install /home/you/project/review --validate-only --json
  pig -e /home/you/project/review          # load it into a session, then edit and /reload
```

The command takes under a second. It writes two files and needs no network, because it uses the SDK embedded in PiG. `extension.go` is the whole extension:

```go
package review

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New("review")

	e.Tool("review_ping", "Return a short pong so you can confirm the extension is wired up.",
		sdk.Schema{"type": "object", "properties": map[string]any{}},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return "pong from review", nil
		})

	e.Command("review", "Say hello from the review extension.", func(ctx sdk.Context, args string) error {
		ctx.Notify("hello from review", "info")
		return nil
	})

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus("review", "review loaded")
		return nil, nil
	})

	return e
}
```

It registers one tool (`review_ping`, which the model may call), one command (`/review`, which you type) and one handler (it runs at the start of every session and puts `review loaded` in the status line).

## 2. Check it

```bash
pig install ./review --validate-only --json
```

`--validate-only` builds the extension and reports what it registers, and installs nothing. The first run compiles the extension and the SDK: it took 40 seconds on the machine that produced this page. Later runs reuse the build. The result:

```json
{
  "valid": true,
  "name": "review",
  "source": "/home/you/project/review",
  "hash": "fd28f4bbcf417c3f31b7720edae8ca0697b1c10c5c028a3c9c5d2fc2d6ea7662",
  "tools": [
    "review_ping"
  ],
  "commands": [
    "review"
  ],
  "handlers": [
    "session_start"
  ],
  "toolDetails": [
    {
      "name": "review_ping",
      "description": "Return a short pong so you can confirm the extension is wired up."
    }
  ],
  "commandDetails": [
    {
      "name": "review",
      "description": "Say hello from the review extension."
    }
  ],
  "registered": true,
  "definition": {
    "form": "factory",
    "language": "go",
    "root": "/home/you/project/review",
    "package": "example.com/review",
    "factory": "Extension",
    "packable": true,
    "runtime": "subprocess",
    "sdk": "github.com/MichaelKinsy/PiG/extensions/sdk",
    "isolation": "shared-ok"
  }
}
```

`"valid": true` and the lists of `tools`, `commands` and `handlers` are what you check. They must match what you wrote. `"runtime": "subprocess"` says the extension runs as its own process.

## 3. Run it

```bash
pig -e ./review
```

The first start compiles again for the session and takes about 20 seconds. The startup screen lists what loaded:

```text
[Extensions]
  review
```

and the footer's status line shows `review loaded`. Type the command:

```text
/review
```

```text
 hello from review
```

Now ask the model to use the tool. This is a request you type; the model decides to call it:

```text
Call the ping tool
```

```text
 review_ping
 pong from review
 The tool answered: pong from review.
```

The line `review_ping` is the tool call, `pong from review` is what your function returned, and the last line is the model's reply (your model's wording differs). `-e` loads the extension for this run only.

## 4. Edit and reload

Change `"hello from review"` in `extension.go` to `"hello again from review"`. Save. In the running session:

```text
/reload
```

```text
 Reloaded keybindings, extensions, skills, prompts, themes, and context files
```

PiG rebuilds the extension, which took under 25 seconds here, and swaps it in without ending the session. Type `/review` again:

```text
 hello again from review
```

After a reload the startup listing shows everything PiG found, for example `[Skills]`, `[Prompts]` and `[Extensions]`.

## 5. Keep it

To load it in every session, install it:

```bash
pig install ./review
pig list
```

`pig remove ./review` removes it. To give it to others, put it in a Package ([Packages](/docs/latest/packages)) or a Piglet ([Piglets](/docs/latest/piglets)). An extension runs with your permissions, so read someone else's before you run it ([Security](/docs/latest/security)).

## 6. A useful one: stop a dangerous command

A handler for the `tool_call` event runs before the model's tool runs, and can stop it. This extension refuses any `bash` command that contains `rm -rf`. Create it the same way (`pig extension init ./guard --lang go`) and replace `extension.go`:

```go
package guard

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New("guard")

	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, event map[string]any) (any, error) {
		if event["toolName"] != "bash" {
			return nil, nil
		}
		input, _ := event["input"].(map[string]any)
		command, _ := input["command"].(string)
		if strings.Contains(command, "rm -rf") {
			ctx.Notify("guard blocked: "+command, "warning")
			return map[string]any{"block": true, "reason": "guard: rm -rf is not allowed here"}, nil
		}
		return nil, nil
	})

	return e
}
```

Returning `nil` lets the tool run. Returning `{"block": true, "reason": ...}` stops it, and the model receives the reason as the tool's result (Pi's `tool_call` hook, `docs/extensions.md`). Run it with `pig -e ./guard` and ask for something the guard refuses. Checked on PiG with a test model that tried `rm -rf build`:

```text
 Clean the build folder
 $ rm -rf build
 guard: rm -rf is not allowed here
 Took 0.0s
 Warning: guard blocked: rm -rf build
 The command was blocked.
```

The command never ran. The line under `$ rm -rf build` is the reason, and the model's next message is built on it (the provider request carried `guard: rm -rf is not allowed here` as the tool result). A later request for a harmless command, `echo safe`, ran normally.

This guards only what you thought of. A model can express a deletion another way (`find . -delete`), so use the guard as a seatbelt and not as a sandbox ([Security](/docs/latest/security)). For a real boundary run PiG in a container ([Isolate PiG](/docs/latest/containerization)).

## 7. More things an extension can add

The guard used an event. An extension can also add a command that takes arguments, a dialog, a key binding and a command-line flag. This one has all four. Create it with `pig extension init ./tour --lang go` and replace `extension.go`:

```go
package tour

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New("tour")

	e.Flag("shout", sdk.FlagOptions{Description: "Upper-case the greeting", Type: sdk.FlagBoolean, Default: false})

	e.Command("greet", "Greet someone: /greet <name>", func(ctx sdk.Context, args string) error {
		message := "Hello, " + strings.TrimSpace(args)
		if shout, _ := ctx.GetFlag("shout"); shout == true {
			message = strings.ToUpper(message)
		}
		ctx.Notify(message, "info")
		return nil
	})

	e.Command("ask", "Ask a yes/no question", func(ctx sdk.Context, args string) error {
		ok, err := ctx.Confirm("Continue?", "This only shows a dialog.")
		if err != nil {
			return err
		}
		if ok == true {
			ctx.Notify("You said yes", "info")
		} else {
			ctx.Notify("You said no", "warning")
		}
		return nil
	})

	e.Shortcut("alt+g", "Say hello from a shortcut", func(ctx sdk.Context) error {
		ctx.Notify("hello from alt+g", "info")
		return nil
	})

	return e
}
```

A mistake shows up at the validation step, with the compiler's message and the log file that holds the rest. The first draft of this example used a constant name that does not exist, and the check said (`valid` false, exit status 1):

```json
{
  "valid": false,
  "name": "./tour",
  "registered": false,
  "error": "extension \"tour\": build packed cell packed-go:fd07590f807be13e: go build: extension.go:12:84: undefined: sdk.FlagBool (details: /home/you/.pig/cache/logs/build-bf362bc5e282.log)"
}
```

The constant is named `FlagBoolean`, so the fix is `sdk.FlagBoolean`. After it, `pig install ./tour --validate-only --json` reports `"valid": true`, `"commands": ["ask", "greet"]`, `"flags": ["shout"]` and `"shortcuts": ["alt+g"]`.

The flag shows in the help of a PiG started with the extension:

```bash
pig -e ./tour --help
```

```text
Extension CLI Flags:
  --shout                     Upper-case the greeting
```

Start it with the flag and try each part (checked on PiG):

```bash
pig -e ./tour --shout
```

| You do | PiG shows |
|---|---|
| Type `/greet Ada` | `HELLO, ADA` (the flag upper-cased it; without `--shout`: `Hello, Ada`) |
| Type `/ask` | A dialog: `Continue?`, `This only shows a dialog.`, then `→ Yes` and `No`. `Enter` on `Yes` shows `You said yes`. |
| Press `alt+g` | `hello from alt+g` |

The same `ctx` also offers `Select` (a list), `Input` (a text box), `SetStatus`, `Exec` (run a program) and the model, session and settings calls. [Extensions](/docs/latest/extensions) lists them all, and [Keybindings](/docs/latest/keybindings) explains how an extension's key interacts with yours.

## If it does not work

| You see | Cause and fix |
|---|---|
| `go` is `missing` in `pig setup` | Install Go. `pig setup go` installs a verified toolchain under `~/.pig/toolchains/go`. |
| `go.mod requires go >= 1.26.0 (running go 1.22.2; GOTOOLCHAIN=local)` | The `go` on your `PATH` is too old and may not download a newer one. Unset `GOTOOLCHAIN`, or install a newer Go. PiG uses the Go from `pig setup go` only when no `go` is on `PATH`. |
| Validation takes a long time | The first build compiles the SDK. Wait for it. A repeat is fast. |
| `valid` is `false` | The output names the problem. Fix `extension.go` and run the check again. |
| `/review` is not found | The extension did not load. Check the `[Extensions]` list on the startup screen, and the lines above it for an error. |
