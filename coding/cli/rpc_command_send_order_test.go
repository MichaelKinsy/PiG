package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// rpcCommandSendOrderExtension starts a turn from an extension command without awaiting it. Its before_agent_start handler writes a setStatus request, which marks where the run's preparation lands among the records.
const rpcCommandSendOrderExtension = `export default function (pi) {
  pi.on("before_agent_start", (_event, ctx) => {
    ctx.ui.setStatus("order", "busy");
  });
  pi.registerCommand("sync-user", {
    handler: (_args, _ctx) => {
      pi.sendUserMessage("Say hello");
    },
  });
  pi.registerCommand("async-followup", {
    handler: async (_args, _ctx) => {
      pi.sendMessage({ customType: "order", content: "Say hello", display: true }, { triggerTurn: true, deliverAs: "followUp" });
    },
  });
}
`

// rpcRecordTag names a record by the members that place it in the order: the response and its disposition, the UI request method, the event type and the message role. message_update records are dropped because their count follows the provider's chunking.
func rpcRecordTag(r rpcRecord) string {
	switch r["type"] {
	case "response":
		data, _ := r["data"].(map[string]any)
		return fmt.Sprintf("response:%v", data["disposition"])
	case "extension_ui_request":
		return fmt.Sprintf("ui:%v", r["method"])
	case "message_start", "message_end":
		message, _ := r["message"].(map[string]any)
		return fmt.Sprintf("%v:%v", r["type"], message["role"])
	case "message_update":
		return ""
	}
	return fmt.Sprint(r["type"])
}

// An extension command that starts a turn without awaiting it answers its prompt where Pi's microtasks place the answer, measured on Pi 1.1.0 with the same extension and the test-faux provider.
//
// sync-user: pi.sendUserMessage starts prompt() inside the handler. Its awaits of _runInputHandlers and checkAuth put the before_agent_start handler two microtasks after the call; the command's `await command.handler()` and `await this._tryExecuteExtensionCommand(text)` write the response two microtasks after the handler returned, behind it. agent_start follows many awaits later (agent-session.ts:1968-2112, 2116-2140; rpc-mode.ts:394-413).
//
// async-followup: Pi 1.2.0's sendCustomMessage calls _startRun (Pi PR #10739), whose _prepareRun calls emitBeforeAgentStart, which calls the handler before its first await, so setStatus precedes the response; agent_start follows the awaits of the handler result and of _prepareRun, after the response (agent-session.ts:1832-1877, runner.ts emitBeforeAgentStart). Pi 1.1.0 itself runs no before_agent_start here and writes agent_start first.
func TestRPCCommandTurnStartOrderMatchesPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and runs a Node extension")
	}
	for _, tc := range rpcCommandSendOrderCases {
		t.Run(strings.TrimPrefix(tc.command, "/"), func(t *testing.T) {
			home := t.TempDir()
			extensionPath := filepath.Join(t.TempDir(), "order.mjs")
			if err := os.WriteFile(extensionPath, []byte(rpcCommandSendOrderExtension), 0o600); err != nil {
				t.Fatal(err)
			}
			p := startRPCProcessAt(t, t.TempDir(), rpcCommandSendOrderEnv(home), append(rpcCommandSendOrderArgs, "-e", extensionPath)...)
			assertRPCCommandSendOrder(t, p, tc.command, tc.want)
		})
	}
}

// The program's own in-process extension (MainOptions.ExtensionFactories) calls the same actions through the runner rather than the subprocess bridge, and its runs take the same places.
func TestRPCInlineCommandTurnStartOrderMatchesPi(t *testing.T) {
	if os.Getenv(embeddedSendOrderEnv) == "1" {
		Main(flag.Args(), &MainOptions{ExtensionFactories: []extension.InlineExtension{extension.ExtensionFactory(sendOrderFactory)}})
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	for _, tc := range rpcCommandSendOrderCases {
		t.Run(strings.TrimPrefix(tc.command, "/"), func(t *testing.T) {
			home := t.TempDir()
			argv := append([]string{"-test.run=^TestRPCInlineCommandTurnStartOrderMatchesPi$", "--", "--mode", "rpc"}, rpcCommandSendOrderArgs...)
			p := startRPCCommandAt(t, t.TempDir(), append(rpcCommandSendOrderEnv(home), embeddedSendOrderEnv+"=1"), os.Args[0], argv...)
			assertRPCCommandSendOrder(t, p, tc.command, tc.want)
		})
	}
}

// embeddedSendOrderEnv makes the test binary a program whose own extension starts turns from its commands.
const embeddedSendOrderEnv = "PIG_TEST_EMBEDDED_SEND_ORDER"

// sendOrderFactory is rpcCommandSendOrderExtension as an in-process Go extension.
func sendOrderFactory(api extension.API) error {
	api.OnBeforeAgentStart(func(ctx context.Context, _ extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
		if ui, err := extension.FromContext(ctx).UI(); err == nil {
			ui.SetStatus("order", "busy")
		}
		return extension.BeforeAgentStartEventResult{}, nil
	})
	api.RegisterCommand("sync-user", extension.CommandOptions{Handler: func(context.Context, string) error {
		api.SendUserMessage("Say hello", nil)
		return nil
	}})
	api.RegisterCommand("async-followup", extension.CommandOptions{Handler: func(context.Context, string) error {
		api.SendMessage(extension.SendMessagePayload{CustomType: "order", Content: "Say hello", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true), DeliverAs: extension.DeliverAsFollowUp})
		return nil
	}})
	return nil
}

var rpcCommandSendOrderCases = []struct {
	command string
	want    []string
}{
	{"/sync-user", []string{
		"ui:setStatus", "response:handled", "agent_start", "turn_start",
		"message_start:system", "message_end:system", "message_start:user", "message_end:user",
		"message_start:assistant", "message_end:assistant", "turn_end", "agent_end", "agent_settled",
	}},
	{"/async-followup", []string{
		"ui:setStatus", "response:handled", "agent_start", "turn_start",
		"message_start:system", "message_end:system", "message_start:custom", "message_end:custom",
		"message_start:assistant", "message_end:assistant", "turn_end", "agent_end", "agent_settled",
	}},
}

var rpcCommandSendOrderArgs = []string{"--offline", "--no-extensions", "--no-skills", "--no-context-files", "--no-session", "--system-prompt", "P.", "--model", "test-faux/faux-1"}

func rpcCommandSendOrderEnv(home string) []string {
	return []string{"HOME=" + home, "USERPROFILE=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}
}

// assertRPCCommandSendOrder runs command and compares the records up to agent_settled with want.
func assertRPCCommandSendOrder(t *testing.T, p *rpcProcess, command string, want []string) {
	t.Helper()
	p.sendJSON(rpcRecord{"id": "c", "type": "prompt", "message": command})
	var got []string
	p.await("agent_settled", func(r rpcRecord) bool {
		if tag := rpcRecordTag(r); tag != "" {
			got = append(got, tag)
		}
		return r["type"] == "agent_settled"
	})
	p.closeAndWait("after agent_settled")
	if !slices.Equal(got, want) {
		t.Fatalf("record order\n got: %q\nwant: %q", got, want)
	}
}

// rpcCommandAwaitsRunExtension starts a turn from a command, after the command already awaited once, and then awaits a promise
// only the run's agent_end resolves.
const rpcCommandAwaitsRunExtension = `export default function (pi) {
  let ended;
  pi.on("agent_end", () => { ended?.(); });
  pi.registerCommand("await-run", {
    handler: async (_args, ctx) => {
      await new Promise((resolve) => setTimeout(resolve, 20));
      const done = new Promise((resolve) => { ended = resolve; });
      pi.sendMessage({ customType: "order", content: "Say hello", display: true }, { triggerTurn: true, deliverAs: "followUp" });
      await done;
      ctx.ui.notify("run ended", "info");
    },
  });
}
`

// The host holds the start of a run a command sends while the command's handler runs, as Pi's run starts only once the handler
// awaits or returns (agent-session.ts:2129, 2293). A Node handler that awaits something other than a host call after a send
// that follows its first await reports the new await, so the run starts while it waits: here the await ends only with the
// run, which a held start would never begin.
func TestRPCCommandThatAwaitsTheRunItSentAnswersAfterIt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and runs a Node extension")
	}
	home := t.TempDir()
	extensionPath := filepath.Join(t.TempDir(), "await-run.mjs")
	if err := os.WriteFile(extensionPath, []byte(rpcCommandAwaitsRunExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, t.TempDir(), rpcCommandSendOrderEnv(home), append(rpcCommandSendOrderArgs, "-e", extensionPath)...)
	p.sendJSON(rpcRecord{"id": "c", "type": "prompt", "message": "/await-run"})
	var got []string
	answered, settled := false, false
	p.await("the command's response and agent_settled", func(r rpcRecord) bool {
		if tag := rpcRecordTag(r); tag != "" {
			got = append(got, tag)
		}
		answered = answered || r["type"] == "response" && r["id"] == "c"
		settled = settled || r["type"] == "agent_settled"
		return answered && settled
	})
	p.closeAndWait("after the command answered")
	if start, notify, response := slices.Index(got, "agent_start"), slices.Index(got, "ui:notify"), slices.Index(got, "response:handled"); start < 0 || start > notify || notify > response {
		t.Fatalf("records %q: want the run started, then the command's notification once agent_end ran, then its response", got)
	}
}
