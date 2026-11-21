package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's RPC mode dispatches every stdin line as it is read and awaits none of them (rpc-mode.ts:832-834 `void handleInputLine(line)`, :415 `void session.prompt(...)`). What orders a record written behind a command that calls ctx.reload() is Pi's one event loop: session.reload() (agent-session.ts:3815-3861) awaits session_shutdown, the loaders, session_start and resources_discover in one chain, and the command that awaited it answers in the microtasks of its end, so no stdin chunk is read inside the chain unless a handler of it waits on a timer or I/O.
//
// Probed against Pi 1.5.2 with these fixtures' shapes (ping marks the generation that ran it):
//   - /rl and /ping in one chunk: the ping runs on the old generation before the reload goes past its first await; responses ping, rl.
//   - /ping written while the reload runs: it runs on the reloaded generation after the reload; responses rl, ping.
//   - /ping written while a session_start handler of the reload awaits a timer: it runs on the reloaded generation during the wait; responses ping, rl.
//   - a command in flight when the reload starts goes on with a stale ctx: its ctx.ui throws Pi's stale message, which RPC reports as extension_error, and its prompt still answers.
//
// The fixtures (testdata/rpc-reload.mjs, testdata/rpc-reload-go) gate each interleaving on files in RL_DIR, so no test depends on how long a reload takes. The expected streams are Pi 1.5.2's for the same fixture and steps.

// reloadRPC is one RPC process serving a reload fixture, with the directory its gates live in.
type reloadRPC struct {
	p   *rpcProcess
	dir string
}

// reloadFixture returns the Node or Go reload fixture as RPC mode's -e takes it.
func reloadFixture(t *testing.T, sdk string) string {
	t.Helper()
	var fixture string
	switch sdk {
	case "node":
		var err error
		if fixture, err = filepath.Abs("testdata/rpc-reload.mjs"); err != nil {
			t.Fatal(err)
		}
	case "go":
		source, err := os.ReadFile("testdata/rpc-reload-go/extension.go")
		if err != nil {
			t.Fatal(err)
		}
		fixture = filepath.Join(t.TempDir(), "rlfix")
		if err := os.MkdirAll(fixture, 0o755); err != nil {
			t.Fatal(err)
		}
		mod := fmt.Sprintf("module example.com/rlfix\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.3.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", filepath.Join(fixtureSourceRoot, "extensions", "sdk"))
		for name, content := range map[string][]byte{"go.mod": []byte(mod), "extension.go": source} {
			if err := os.WriteFile(filepath.Join(fixture, name), content, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return fixture
}

// startReloadRPC starts RPC mode with the Node or Go reload fixture; env adds the fixture's switches.
func startReloadRPC(t *testing.T, sdk string, env ...string) *reloadRPC {
	t.Helper()
	return startReloadRPCWith(t, env, reloadFixture(t, sdk))
}

// startReloadRPCWith starts RPC mode with the given extensions, in their order; RL_DIR names the directory their gates live in.
func startReloadRPCWith(t *testing.T, env []string, extensions ...string) *reloadRPC {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	args := []string{"--no-session", "--model", "test-faux/faux-1"}
	for _, extension := range extensions {
		args = append(args, "-e", extension)
	}
	p := startRPCProcessAt(t, t.TempDir(), append([]string{
		"HOME=" + home, "USERPROFILE=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR=",
		"PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "RL_DIR=" + dir,
	}, env...), args...)
	return &reloadRPC{p: p, dir: dir}
}

func (f *reloadRPC) prompt(id, message string) string {
	return fmt.Sprintf(`{"id":%q,"type":"prompt","message":%q}`, id, message)
}

// open creates a gate file the fixture waits for.
func (f *reloadRPC) open(gate string) {
	f.p.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, gate), nil, 0o600); err != nil {
		f.p.t.Fatal(err)
	}
}

// reached waits for a marker file the fixture writes when it reaches a point no record reports.
func (f *reloadRPC) reached(marker string) {
	f.p.t.Helper()
	deadline := time.Now().Add(testbudget.Wait(f.p.t))
	for {
		if _, err := os.Stat(filepath.Join(f.dir, marker)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			f.p.t.Fatalf("the fixture never wrote %s\n%s", marker, f.p.stderr.String())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// reloadToken names what a client can order in the stream: a notification, a dialog, an extension error or a response. Other records give "".
func reloadToken(record rpcRecord) string {
	switch record["type"] {
	case "response":
		return fmt.Sprintf("response %v success=%v", record["id"], record["success"])
	case "extension_error":
		message, _ := record["error"].(string)
		if strings.HasPrefix(message, "This extension ctx is stale after session replacement or reload.") {
			message = "stale ctx"
		}
		return fmt.Sprintf("extension_error %v: %s", record["extensionPath"], message)
	case "extension_ui_request":
		if record["method"] == "notify" {
			return fmt.Sprintf("notify %v", record["message"])
		}
		return fmt.Sprintf("%v %v", record["method"], record["title"])
	}
	return ""
}

// through returns the tokens of the records up to and including the one whose token is last.
func (f *reloadRPC) through(last string) []string {
	f.p.t.Helper()
	var tokens []string
	f.p.awaitProgress(func() string { return fmt.Sprintf("%q after %q", last, tokens) }, func(record rpcRecord) bool {
		token := reloadToken(record)
		if token == "" {
			return false
		}
		tokens = append(tokens, token)
		return token == last
	})
	return tokens
}

// finish proves the command loop goes on: a state query answers, and stdin's end ends the process.
func (f *reloadRPC) finish() {
	f.p.t.Helper()
	f.p.send(`{"id":"state","type":"get_state"}`)
	if got, want := f.through("response state success=true"), []string{"response state success=true"}; !slices.Equal(got, want) {
		f.p.t.Fatalf("after the reload the stream carried %q, want %q", got, want)
	}
	f.p.closeAndWait("the reload settled")
}

func eachReloadSDK(t *testing.T, test func(t *testing.T, sdk string)) {
	for _, sdk := range []string{"node", "go"} {
		t.Run(sdk, func(t *testing.T) {
			t.Parallel()
			test(t, sdk)
		})
	}
}

// Defect A. A prompt written while a reload runs is read after the reload and the response of the command that awaited it, and runs on the reloaded extension. The reloaded generation's factory blocks its thread until the test has written the prompt, as a factory that computes does: Pi's loop cannot turn there, so Pi reads the prompt after the reload whatever the timing.
// mutation-checked: with rpcSettleGate.openLocked ignoring the reloads the gate holds, the prompt answers first: the Go fixture writes `response c` alone, the Node one `extension_error command:ping: stale ctx`, `response c`.
func TestRPCPromptWrittenDuringAReloadRunsOnTheReloadedExtension(t *testing.T) {
	eachReloadSDK(t, func(t *testing.T, sdk string) {
		f := startReloadRPC(t, sdk, "RL_LOAD=block")
		f.p.send(f.prompt("a", "/ping"))
		if got, want := f.through("response a success=true"), []string{"notify ping g1", "response a success=true"}; !slices.Equal(got, want) {
			t.Fatalf("first ping: %q, want %q", got, want)
		}
		f.p.send(f.prompt("b", "/rl"))
		f.reached("loading")
		f.p.send(f.prompt("c", "/ping"))
		f.open("load-gate")
		want := []string{"notify started g2", "response b success=true", "notify ping g2", "response c success=true"}
		if got := f.through("response c success=true"); !slices.Equal(got, want) {
			t.Fatalf("a prompt written while the reload ran: %q, want %q", got, want)
		}
		f.finish()
	})
}

// A prompt in the chunk that carries the reloading command is dispatched before the reload goes past its first await, so it runs on the extension the reload replaces and answers first. On the base the Node runtime never answered either prompt: the host dropped the ping's notify call once the reload had armed the generation.
func TestRPCPromptOfTheReloadsChunkRunsOnTheReplacedExtension(t *testing.T) {
	eachReloadSDK(t, func(t *testing.T, sdk string) {
		f := startReloadRPC(t, sdk)
		f.p.send(f.prompt("b", "/rl") + "\n" + f.prompt("c", "/ping"))
		want := []string{"notify ping g1", "response c success=true", "notify started g2", "response b success=true"}
		if got := f.through("response b success=true"); !slices.Equal(got, want) {
			t.Fatalf("a prompt in the reload's chunk: %q, want %q", got, want)
		}
		f.finish()
	})
}

// A prompt written while the reloaded extension's factory awaits a timer. Pi's loop turns inside such a load (agent-session.ts:3838 `await this._resourceLoader.reload()`), reads the prompt there and runs it on the runner the reload has already invalidated: `extension_error command:ping: stale ctx`, `response c`, and only then `notify started g2`, `response b`. PiG holds the prompt to the end of the reload, as it does when the factory computes, and runs it on the reloaded extension (D630).
func TestRPCPromptWrittenWhileAReloadedFactoryAwaitsRunsOnTheReloadedExtension(t *testing.T) {
	f := startReloadRPC(t, "node", "RL_LOAD=await")
	f.p.send(f.prompt("b", "/rl"))
	f.reached("loading")
	f.p.send(f.prompt("c", "/ping"))
	f.open("load-gate")
	f.expectStream("a prompt written while the reloaded factory awaits", "notify started g2", "response b success=true", "notify ping g2", "response c success=true")
	f.finish()
}

// expectStream sends nothing: it requires the tokens up to last to be want.
func (f *reloadRPC) expectStream(what string, want ...string) {
	f.p.t.Helper()
	if got := f.through(want[len(want)-1]); !slices.Equal(got, want) {
		f.p.t.Fatalf("%s: %q, want %q", what, got, want)
	}
}

// awaitDialog returns the id of the input dialog a handler opened.
func (f *reloadRPC) awaitDialog(title string) string {
	f.p.t.Helper()
	var id string
	f.p.await("the "+title+" dialog", func(record rpcRecord) bool {
		if record["type"] == "extension_ui_request" && record["method"] == "input" && record["title"] == title {
			id, _ = record["id"].(string)
		}
		return id != ""
	})
	return id
}

func (f *reloadRPC) answer(dialog string) string {
	return fmt.Sprintf(`{"type":"extension_ui_response","id":%q,"value":"v"}`, dialog)
}

// A handler of the reload that waits on a timer lets Pi's loop turn, so a prompt written meanwhile is read inside the reload: on the extension being replaced while a session_shutdown handler waits (the old runner is invalidated only after session_shutdown, agent-session.ts:3825-3826), and on the reloaded one while a session_start handler waits (the rebuilt runner is current before session_start, :3844-3857). It answers before the reloading command.
// mutation-checked: with the generation superseded before its session_shutdown(reload) handler runs (Host.supersedeGenerations at that event's dispatch), the session_shutdown subtest never reads `notify shutting down g1`: the handler's ctx throws the stale message.
func TestRPCPromptReadWhileAReloadsHandlerWaitsRunsInsideTheReload(t *testing.T) {
	t.Run("session_start", func(t *testing.T) {
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_START=await")
			f.p.send(f.prompt("b", "/rl"))
			f.expectStream("the reload", "notify started g2")
			f.p.send(f.prompt("c", "/ping"))
			f.expectStream("a prompt read while session_start waits", "notify ping g2", "response c success=true")
			f.open("start-gate")
			f.expectStream("after the session_start handler returned", "notify start done g2", "response b success=true")
			f.finish()
		})
	})
	t.Run("session_shutdown", func(t *testing.T) {
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_SHUTDOWN=await")
			f.p.send(f.prompt("b", "/rl"))
			f.expectStream("the reload", "notify shutting down g1")
			f.p.send(f.prompt("c", "/ping"))
			f.expectStream("a prompt read while session_shutdown waits", "notify ping g1", "response c success=true")
			f.open("shutdown-gate")
			f.expectStream("after the session_shutdown handler returned", "notify shutdown done g1", "notify started g2", "response b success=true")
			f.finish()
		})
	})
}

// A handler of the reload that asks the client something waits for a line of stdin: Pi's loop turns at the dialog's await and reads it, so the hold on stdin must not keep the answer out. A prompt in the chunk that carries the answer is dispatched before the handler's continuation runs, and one written before the answer is read inside the reload like any prompt read during a handler's wait.
//
// For the answer and a prompt in one chunk Pi's stream is the prompt's chain and then the handler's: `notify ping`, `response c`, the notifications of the handler that went on, `response b` (Pi 1.5.2 with this fixture, 20 of 20 runs of each of the two cases). Pi resolves the dialog on its line and the handler goes on in a microtask behind the chunk's lines (rpc-mode.ts:784-797, 832-834; jsonl.ts:29-40), and the prompt's handler, which awaits nothing, has answered by then (agent-session.ts:2171-2176). PiG delivers an answer read during a reload behind its batch (deliverRPCDialogAnswer), and the two subtests require Pi's stream.
// mutation-checked: with deliverRPCDialogAnswer delivering at once during a reload, both "answered in a prompt's chunk" subtests failed in 5 of 5 runs: the notification of the handler that went on was written before `response c`.
func TestRPCDialogOfAReloadsHandlerIsAnsweredDuringTheReload(t *testing.T) {
	t.Run("session_start answered", func(t *testing.T) {
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_START=ask")
			f.p.send(f.prompt("b", "/rl"))
			f.p.send(f.answer(f.awaitDialog("start-ask")))
			f.expectStream("the answered reload", "notify start answered v g2", "response b success=true")
			f.finish()
		})
	})
	t.Run("session_start answered in a prompt's chunk", func(t *testing.T) {
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_START=ask")
			f.p.send(f.prompt("b", "/rl"))
			f.p.send(f.answer(f.awaitDialog("start-ask")) + "\n" + f.prompt("c", "/ping"))
			f.expectStream("the answer and a prompt in one chunk", "notify ping g2", "response c success=true", "notify start answered v g2", "response b success=true")
			f.finish()
		})
	})
	t.Run("a prompt before the session_start answer", func(t *testing.T) {
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_START=ask")
			f.p.send(f.prompt("b", "/rl"))
			dialog := f.awaitDialog("start-ask")
			f.p.send(f.prompt("c", "/ping"))
			f.expectStream("a prompt read while the dialog is open", "notify ping g2", "response c success=true")
			f.p.send(f.answer(dialog))
			f.expectStream("the answered reload", "notify start answered v g2", "response b success=true")
			f.finish()
		})
	})
	t.Run("session_shutdown answered in a prompt's chunk", func(t *testing.T) {
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_SHUTDOWN=ask")
			f.p.send(f.prompt("b", "/rl"))
			f.p.send(f.answer(f.awaitDialog("shutdown-ask")) + "\n" + f.prompt("c", "/ping"))
			f.expectStream("the answer and a prompt in one chunk", "notify ping g1", "response c success=true", "notify shutdown done g1", "notify started g2", "response b success=true")
			f.finish()
		})
	})
}

// The hold on stdin has an end on every path, each bounded here by the event that ends it.
func TestRPCReloadHoldEnds(t *testing.T) {
	// Stdin ends while the reload runs: Pi reads the end after the reload's chain, so the reload finishes, its command answers and the process exits.
	t.Run("stdin ends during the reload", func(t *testing.T) {
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_LOAD=block")
			f.p.send(f.prompt("b", "/rl"))
			f.reached("loading")
			f.p.closeInput()
			f.open("load-gate")
			if sdk == "node" {
				f.expectStream("the reload after stdin's end", "notify started g2", "response b success=true")
			}
			// The Go fixture's process exits without the reload's response (D19: a command of a runtime that reports no window counts as unanswered at stdin's end), where the Node one equals Pi: differs from Pi here: item 4 of FOUR-DIFFERENCES.md.
			f.p.waitForExit("after the reload")
		})
	})
	// Stdin ends while the reloaded extension's factory awaits a timer. Pi's loop turns inside such a load, reads the end and exits 0 at once, without the reload's response. PiG holds stdin's end to the end of the reload as it holds a prompt (D630): the reload finishes, its command answers and the process exits, bounded by the load's end.
	t.Run("stdin ends while the reloaded factory awaits", func(t *testing.T) {
		f := startReloadRPC(t, "node", "RL_LOAD=await")
		f.p.send(f.prompt("b", "/rl"))
		f.reached("loading")
		f.p.closeInput()
		f.open("load-gate")
		f.expectStream("the reload after stdin's end", "notify started g2", "response b success=true")
		f.p.waitForExit("after the reload")
	})
	// A termination signal during the reload ends the process with the signal's status; the reload's hold does not keep the shutdown waiting.
	t.Run("SIGTERM during the reload", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a termination signal cannot be delivered to a child on windows")
		}
		eachReloadSDK(t, func(t *testing.T, sdk string) {
			f := startReloadRPC(t, sdk, "RL_LOAD=await")
			f.p.send(f.prompt("b", "/rl"))
			f.reached("loading")
			if err := f.p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			waited := make(chan error, 1)
			go func() { waited <- f.p.cmd.Wait() }()
			select {
			case err := <-waited:
				f.p.exited = true
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 143 {
					t.Fatalf("wait = %v, want exit status 143\n%s", err, f.p.stderr.String())
				}
			case <-time.After(testbudget.Wait(t)):
				t.Fatalf("the process did not exit after SIGTERM during a reload\n%s", f.p.stderr.String())
			}
		})
	})
}

// A user message a session_start handler of the reload sends is handled by the reloaded extension: its input handler sees it, and the run it starts ends.
func TestRPCUserMessageSentByAReloadsSessionStartRunsOnTheReloadedExtension(t *testing.T) {
	f := startReloadRPC(t, "node", "RL_START=send")
	f.p.send(f.prompt("b", "/rl"))
	var tokens []string
	settled, answered := false, false
	f.p.await("the run the session_start handler started and the reload's response", func(record rpcRecord) bool {
		if token := reloadToken(record); token != "" {
			tokens = append(tokens, token)
			answered = answered || token == "response b success=true"
		}
		settled = settled || record["type"] == "agent_settled"
		return settled && answered
	})
	// Pi writes the reload's response before the run's agent_start; PiG writes it after the run, so the order of the two is not required here: differs from Pi here: item 2 of FOUR-DIFFERENCES.md.
	want := []string{"notify started g2", "notify input g2: hello from session_start", "response b success=true"}
	if !slices.Equal(tokens, want) {
		t.Fatalf("a user message sent by session_start: %q, want %q", tokens, want)
	}
	f.finish()
}

// Defect B. A command whose handler is in flight when a reload replaces its generation still answers. The handler signals that it runs, the test starts the reload and lets the handler go on while the replacement generation is loading: the handler's ctx is stale by then (agent-session.ts:3825-3826), so in Pi its ctx.ui throws, RPC reports the error and the prompt answers "handled" (agent-session.ts:2280-2291). On the base the host dropped the Node handler's call unanswered and the prompt never answered.
// mutation-checked: without supersedeGenerations in Host.Reload the Node fixture leaves `response c` unwritten, as on the base.
func TestRPCCommandInFlightAtAReloadIsAnswered(t *testing.T) {
	eachReloadSDK(t, func(t *testing.T, sdk string) {
		f := startReloadRPC(t, sdk, "RL_LOAD=await")
		f.p.send(f.prompt("c", "/held"))
		if got, want := f.through("notify held running g1"), []string{"notify held running g1"}; !slices.Equal(got, want) {
			t.Fatalf("held: %q, want %q", got, want)
		}
		f.p.send(f.prompt("b", "/rl"))
		f.reached("loading")
		f.open("held-gate")
		want := []string{"extension_error command:held: stale ctx", "response c success=true"}
		if sdk == "go" {
			// A Go extension's generation is served until the reload's commit replaces it (Host.supersedeGenerations): the handler's notification is delivered.
			want = []string{"notify held done g1", "response c success=true"}
		}
		if got := f.through("response c success=true"); !slices.Equal(got, want) {
			_, wentOn := os.Stat(filepath.Join(f.dir, "held-went-on"))
			t.Fatalf("the command in flight at the reload: %q, want %q (the Node handler made its call with a ctx that did not throw: %v)", got, want, wentOn == nil)
		}
		f.open("load-gate")
		if got, want := f.through("response b success=true"), []string{"notify started g2", "response b success=true"}; !slices.Equal(got, want) {
			t.Fatalf("the reload: %q, want %q", got, want)
		}
		f.finish()
	})
}

// A dialog in flight at a reload is answered after it: the handler goes on with a stale ctx and its prompt answers.
func TestRPCDialogInFlightAtAReloadIsAnswered(t *testing.T) {
	eachReloadSDK(t, func(t *testing.T, sdk string) {
		f := startReloadRPC(t, sdk)
		f.p.send(f.prompt("c", "/ask"))
		var dialog string
		f.p.await("the dialog", func(record rpcRecord) bool {
			if record["type"] == "extension_ui_request" && record["method"] == "input" {
				dialog, _ = record["id"].(string)
			}
			return dialog != ""
		})
		f.p.send(f.prompt("b", "/rl"))
		if got, want := f.through("response b success=true"), []string{"notify started g2", "response b success=true"}; !slices.Equal(got, want) {
			t.Fatalf("the reload: %q, want %q", got, want)
		}
		f.p.sendJSON(rpcRecord{"type": "extension_ui_response", "id": dialog, "value": "v"})
		want := []string{"extension_error command:ask: stale ctx", "response c success=true"}
		if sdk == "go" {
			want = []string{"response c success=true"}
		}
		if got := f.through("response c success=true"); !slices.Equal(got, want) {
			t.Fatalf("the command whose dialog was open at the reload: %q, want %q", got, want)
		}
		// The call in flight when the reload made the generation stale keeps its own answer: Pi checks staleness where a member is used (runner.ts:738-741), not where a pending promise settles.
		if asked, err := os.ReadFile(filepath.Join(f.dir, "asked")); err != nil || string(asked) != "v" {
			t.Fatalf("the dialog in flight at the reload answered %q (%v), want the client's value", asked, err)
		}
		f.finish()
	})
}

// A tool call in flight when a command asks for a reload: Pi refuses the reload while the Session runs (agent-session.ts:3816-3818), the command's prompt answers with the error reported, the tool call ends in its run, and the refused reload leaves no hold on stdin.
func TestRPCToolCallInFlightAtARefusedReloadIsAnswered(t *testing.T) {
	f := startReloadRPC(t, "node")
	f.p.send(f.prompt("p", "Run: ext tool gate_tool"))
	f.reached("tool-running")
	f.p.send(f.prompt("b", "/rl"))
	want := []string{"response p success=true", "extension_error command:rl: Wait for the current response to finish before reloading.", "response b success=true"}
	if got := f.through("response b success=true"); !slices.Equal(got, want) {
		t.Fatalf("a reload asked for while a tool runs: %q, want %q", got, want)
	}
	f.open("tool-gate")
	var ended bool
	f.p.await("the run's end", func(record rpcRecord) bool {
		if record["type"] == "tool_execution_end" {
			ended = record["toolName"] == "gate_tool" && record["isError"] == false
		}
		return record["type"] == "agent_settled"
	})
	if !ended {
		t.Fatal("the tool call in flight at the refused reload did not end in its run")
	}
	f.finish()
}
