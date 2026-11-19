package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// An action an agent_settled handler deferred is released once the last handler returned and posts its start to the owner loop. When a quit key sets requestExit between that post and
// the owner loop running it, the start must not begin a turn: the quit settles the interrupted run on the owner loop (settleRunAtQuit, D247) and would wait for the new turn to end. Pi
// never starts it: its quit ends in process.exit(0) without waiting for the run (1.5.0 interactive-mode.ts shutdown, agent-session-runtime.ts dispose).
//
// The interleaving is forced without a sleep: no owner loop drains uiTaskCh, so the posted start waits in the channel; the test sets requestExit, then takes the task out of the
// channel and runs it itself. The control row runs the same task with requestExit unset and must start a turn (runGen is bumped by startTurn), which shows the harness reaches the start.
// Removing the requestExit check from either posted closure turns the exit rows red.
func TestQuitAfterTheReleaseOfADeferredStartBeginsNoTurn(t *testing.T) {
	starts := []struct {
		name string
		post func(m *InteractiveMode) error
	}{
		{"sendMessage with triggerTurn (startCustomTurn)", func(m *InteractiveMode) error {
			msg := agent.AgentMessage{Custom: map[string]any{"role": agent.RoleCustom, "customType": "wake", "content": "WAKE-UP", "display": true, "timestamp": int64(1)}}
			return m.startCustomTurn(msg, false)
		}},
		{"sendUserMessage (deliverUserMessage)", func(m *InteractiveMode) error {
			return m.deliverUserMessage("DEFERRED", "")
		}},
	}
	for _, start := range starts {
		for _, exit := range []bool{false, true} {
			name := start.name + " exit not requested"
			if exit {
				name = start.name + " exit requested"
			}
			t.Run(name, func(t *testing.T) {
				m, _, _ := newLifecycleMode(t)
				if err := start.post(m); err != nil {
					t.Fatal(err)
				}
				if m.runGen != 0 {
					t.Fatalf("the start ran before it was drained: runGen = %d", m.runGen)
				}
				m.requestExit.Store(exit)
				task := <-m.uiTaskCh
				task()
				if exit {
					if m.runGen != 0 {
						t.Fatalf("a turn started after exit was requested: runGen = %d", m.runGen)
					}
					return
				}
				if m.runGen != 1 {
					t.Fatalf("control: the start began no turn: runGen = %d, want 1", m.runGen)
				}
				m.queueMu.Lock()
				ended := m.turnEnded
				m.queueMu.Unlock()
				select {
				case <-ended:
				case <-time.After(5 * time.Second):
					t.Fatal("control: the started run did not end")
				}
			})
		}
	}
}
