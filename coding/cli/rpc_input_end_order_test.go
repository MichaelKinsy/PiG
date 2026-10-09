package cli

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

type rpcOrderImplementation = struct {
	name  string
	start func(t *testing.T, extraEnv ...string) (*rpcProcess, string)
}

// rpcOrderImplementations are Pi and Pig, and Pig with a sibling extension in the same packed Node process.
func rpcOrderImplementations() []rpcOrderImplementation {
	impls := slices.Clone(rpcShutdownImplementations)
	return append(impls, rpcOrderImplementation{"pig-with-sibling", startRPCShutdownFixtureWithSibling})
}

// Pi writes an extension command's prompt response as soon as the handler returns (agent-session.ts:1904-1908 preflightResult("handled"), rpc-mode.ts:394-412) and reads stdin's end in a later event-loop iteration (rpc-mode.ts:802-805). A client that closes stdin on seeing the handler's last notification therefore always receives the response before the exit. The /wait handler awaits the Session's idle state, so it has real suspensions before its notification.
func TestRPCInputEndAfterCommandNotificationAnswersComparedWithPi(t *testing.T) {
	for _, impl := range rpcOrderImplementations() {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_NO_HANDLER=1")
			p.sendJSON(map[string]any{"id": "c", "type": "prompt", "message": "/wait"})
			p.await("the command's notification", func(r rpcRecord) bool {
				return r["type"] == "extension_ui_request" && r["method"] == "notify" && r["message"] == "idle reached"
			})
			p.closeInput()
			afterEOF := drainRPCOutput(p)
			p.waitForExit("after the last notification")
			want := []rpcRecord{handledPromptResponse("c")}
			if !reflect.DeepEqual(afterEOF, want) {
				t.Fatalf("stdout after the notification = %v, want %v\n%s", afterEOF, want, p.stderr.String())
			}
		})
	}
}

// A command that settles in the microtasks and check phase of its line's iteration answers before Pi reads stdin's end, so its response precedes the session_shutdown handler's notification (rpc-mode.ts:726-742,802-805). Pig's runtime is another process: the host must not start the shutdown before such a command's response is written.
func TestRPCInputEndResponsePrecedesShutdownNotificationComparedWithPi(t *testing.T) {
	for _, impl := range rpcOrderImplementations() {
		for _, command := range []string{"sync", "micro", "nexttick", "immediate", "microimmediate", "awaits5immediate"} {
			t.Run(impl.name+"/"+command, func(t *testing.T) {
				p, _ := impl.start(t)
				p.sendJSON(map[string]any{"id": "c", "type": "prompt", "message": "/" + command})
				p.closeInput()
				afterEOF := drainRPCOutput(p)
				p.waitForExit("after the last command line")
				want := []rpcRecord{handledPromptResponse("c"), rpcShutdownStartedNotify}
				if !reflect.DeepEqual(afterEOF, want) {
					t.Fatalf("stdout after stdin ended = %v, want %v\n%s", afterEOF, want, p.stderr.String())
				}
			})
		}
	}
}

// Pi emits agent_settled in the microtasks that follow agent_end, so no stdin line is read between them: a command a client sends on seeing agent_end is answered after agent_settled (agent-session.ts:1752-1777 _runAgentPrompt finally, 1044-1058 _emitAgentSettled, rpc-mode.ts:355-360). An extension's agent_settled handler is a round trip for Pig.
func TestRPCCommandAfterAgentEndAnswersAfterAgentSettledComparedWithPi(t *testing.T) {
	for _, impl := range rpcOrderImplementations() {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_NO_HANDLER=1")
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			var after []string
			p.await("the get_state response", func(r rpcRecord) bool {
				after = append(after, fmt.Sprint(r["type"]))
				return r["id"] == "s"
			})
			if want := []string{"agent_settled", "response"}; !slices.Equal(after, want) {
				t.Fatalf("records after agent_end = %v, want %v\n%s", after, want, p.stderr.String())
			}
		})
	}
}

// The same ordering without an extension: Pi's own listener writes agent_settled in the microtasks after agent_end, so a get_state sent on seeing agent_end is answered after it. The race is between Pig's stdin goroutine and the Session's settle path, so every round repeats it.
func TestRPCCommandAfterAgentEndWithoutExtensionsAnswersAfterAgentSettled(t *testing.T) {
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic"}, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1")
	awaitReady(p)
	for round := range 40 {
		p.sendJSON(map[string]any{"id": fmt.Sprintf("p%d", round), "type": "prompt", "message": "hello"})
		p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
		id := fmt.Sprintf("s%d", round)
		p.sendJSON(map[string]any{"id": id, "type": "get_state"})
		var after []string
		p.await("the get_state response", func(r rpcRecord) bool {
			after = append(after, fmt.Sprint(r["type"]))
			return r["id"] == id
		})
		if want := []string{"agent_settled", "response"}; !slices.Equal(after, want) {
			t.Fatalf("round %d: records after agent_end = %v, want %v\n%s", round, after, want, p.stderr.String())
		}
	}
	p.closeAndWait("after the last round")
}

// An agent_settled handler that waits on a timer or I/O suspends the tail, and Pi's loop reads stdin meanwhile: a get_state sent on seeing agent_end is answered while agent_settled is still pending (agent-session.ts:1044-1050 awaits the handler before it emits agent_settled; rpc-mode.ts:808-810). The handler here never settles, so no agent_settled can precede the response.
func TestRPCCommandDuringSuspendedSettleTailAnswersComparedWithPi(t *testing.T) {
	for _, impl := range rpcOrderImplementations() {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_NO_HANDLER=1", "RPC_SHUTDOWN_SETTLED_WAIT=never")
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
			p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
			var after []string
			p.await("the get_state response", func(r rpcRecord) bool {
				after = append(after, fmt.Sprint(r["type"]))
				return r["id"] == "s"
			})
			if want := []string{"response"}; !slices.Equal(after, want) {
				t.Fatalf("records after agent_end = %v, want %v\n%s", after, want, p.stderr.String())
			}
			p.closeAndWait("after the get_state response")
		})
	}
}

// Stdin's end during an agent_settled handler that never settles runs shutdown(): Pi's dispose does not wait for the run (rpc-mode.ts:726-742,802-805; agent-session-runtime.ts:404-411; agent-session.ts dispose), so the session_shutdown handler's notification is written and the process exits.
func TestRPCInputEndDuringSuspendedSettleTailShutsDownComparedWithPi(t *testing.T) {
	for _, impl := range rpcOrderImplementations() {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t, "RPC_SHUTDOWN_SETTLED_WAIT=never")
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
			p.closeInput()
			afterEOF := drainRPCOutput(p)
			p.waitForExit("after stdin ended during the settle tail")
			if want := []rpcRecord{rpcShutdownStartedNotify}; !reflect.DeepEqual(afterEOF, want) {
				t.Fatalf("stdout after stdin ended = %v, want %v\n%s", afterEOF, want, p.stderr.String())
			}
		})
	}
}
