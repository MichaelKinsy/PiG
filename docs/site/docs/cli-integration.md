# CLI integration

Run `pig` with no options in a terminal to open the interactive terminal UI. Scripts and other programs use one of the three non-interactive modes instead: print, JSON or RPC.

Every mode uses the same agent loop, sessions, tools and resources. The mode decides how input reaches PiG, what PiG writes to standard output, and whether the process stays up for more commands. The options that select the model, tools, resources and session storage work the same way in every mode. See [command line](/docs/latest/cli).

The [Go SDK](/docs/latest/sdk) is not a CLI mode. It runs sessions inside a Go program without a separate process. Pi's SDK is a TypeScript library instead.

## Choose a mode

| Mode | Start it with | Output | Lifetime | Use it when |
|---|---|---|---|---|
| Interactive | `pig` | Terminal UI | Until you exit | A person works with PiG directly. |
| Print | `pig -p "prompt"` | Final answer as text | One invocation | A script needs only the final answer. |
| JSON | `pig --mode json "prompt"` | Events as JSON Lines | One invocation | A program needs the progress of one run. |
| RPC | `pig --mode rpc` | Responses and events as JSON Lines | Until standard input closes | A program needs to send several commands. |

PiG selects print mode on its own when standard input or standard output is not a terminal. A pipe or a redirect is therefore enough:

```bash
git diff | pig "Write a commit message for this diff" > message.txt
```

## Print mode

Print mode runs the prompt, writes the text of the final assistant message to standard output, and exits:

```bash
pig -p "Summarize the changes in this repository"
```

`--print` (`-p`) takes no value. PiG builds the prompt from piped standard input, `@file` arguments and the first message argument, in that order. It sends each further message argument as a separate prompt after the first one finishes.

When standard output is not a terminal, PiG keeps it for the answer and redirects anything that tools or extensions write there to standard error. Extension errors appear on standard error as `Extension error (<path>): <message>`.

| Result | Exit status |
|---|---|
| The final assistant message completes | `0` |
| The final assistant message has stop reason `error` or `aborted` | `1`, with the error on standard error |
| PiG cannot start the run, for example because no model is configured | `1` |
| `SIGINT`, `SIGTERM` or `SIGHUP` stops the run | `128` plus the signal number: `130`, `143` or `129` |

## JSON mode

JSON mode writes one JSON object per line. The first line is the session header. Agent and session events follow:

```bash
pig --mode json "Review this repository" > events.jsonl
```

The output is a stream of events. It does not ask the model to answer in JSON.

All prompts come from the command line and standard input. The process streams the events of that run and exits. It does not accept further commands.

A failed or aborted assistant message appears in the events but does not change the exit status. Read the `message_end` events when success matters. PiG exits with status `1` when the run itself fails.

A `message_update` event carries one delta, not the whole message so far. Build live output from the deltas, then replace it with the complete message in `message_end`.

`agent_end` can be followed by an automatic retry or by queued work. Wait for `agent_settled` to know that the run has no more work.

Standard output carries only JSON Lines. PiG writes diagnostics and logs to standard error. See [JSON event stream mode](/docs/latest/json) for event shapes.

## RPC mode

RPC mode keeps PiG running while another program sends commands:

```bash
pig --mode rpc --no-session
```

The client writes one JSON command per line to standard input. PiG writes one JSON response or event per line to standard output.

Add an `id` to a command to match it with its response. The response repeats the `id`. Events have no `id`, because they describe the session and not one command.

A successful response to `prompt` means that PiG accepted the prompt. It does not mean the run finished. Keep reading events until `agent_settled` when you need the result.

Commands can change the model, read the session state, manage sessions, run shell commands and answer extension dialogs. See [RPC commands](/docs/latest/rpc-commands). Extension dialogs use a request and response exchange. See [RPC extension UI](/docs/latest/rpc-extension-ui). Other extension UI records are notices that a client can show or ignore. Features that need the terminal UI are not available in RPC mode.

Close standard input to stop PiG. See [RPC mode](/docs/latest/rpc) for framing and events.

### Go client

Go programs can use `github.com/MichaelKinsy/PiG/coding/rpcclient`, the Go port of Pi's TypeScript `RpcClient`. It starts `pig --mode rpc`, matches responses to commands, offers one method per command, and delivers events to listeners:

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/MichaelKinsy/PiG/coding/rpcclient"
)

func main() {
	client := rpcclient.NewRpcClient(rpcclient.RpcClientOptions{
		CliPath: "pig",
		Model:   "openai/gpt-5",
		Args:    []string{"--no-session"},
	})
	if err := client.Start(); err != nil {
		log.Fatal(err)
	}
	defer func() {
		client.Stop()
		client.Wait()
	}()

	events, err := client.PromptAndWait("List the Go packages in this repository.", nil, 5*time.Minute)
	if err != nil {
		log.Fatalf("%v\n%s", err, client.GetStderr())
	}
	for _, event := range events {
		fmt.Println(event.Type)
	}
}
```

`PromptAndWait` starts collecting events before it sends the prompt, so a fast run cannot finish before the client listens. It returns every event up to and including `agent_settled`. For separate steps, register a listener with `OnEvent` before you call `Prompt`, and call `WaitForIdle` only while a run is active.

Programs in other languages use the JSON Lines protocol directly. [RPC mode](/docs/latest/rpc) includes a minimal Python client.

## Build your own agent on PiG

Pi lets a source fork rename its command and configuration directory through a `piConfig` block in `package.json`. PiG has no such block. The command name `pig` and the `~/.pig` directory are part of the binary (D2).

To ship an agent with its own name, extensions and defaults, write a [Piglet](/docs/latest/piglets). Run it with `pig --piglet <name|path>`, or build it into a native executable with `pig piglet build --format binary`. See [derivative harnesses](/docs/latest/derivative-harnesses).

## Related

- [Command line](/docs/latest/cli) lists the command-line options.
- [JSON event stream mode](/docs/latest/json) describes the event records.
- [RPC mode](/docs/latest/rpc) describes the RPC protocol.
- [RPC commands](/docs/latest/rpc-commands) lists every RPC command.
- [Go SDK](/docs/latest/sdk) runs sessions inside a Go program.
- [Message types](/docs/latest/message-types) defines the messages inside events.
