package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// An action an agent_settled handler deferred is released once the last handler returned and posts its start to the owner loop. When a quit key sets requestExit between that post and
// the owner loop running it, the start still begins its turn, and the quit waits for that turn: the pinned Pi, quit while the handler of the interrupted run sends a user message,
// runs the turn and emits session_shutdown after its agent_settled (scenario 115-quit-settled-send-user-message-*).
//
// The interleaving is forced without a sleep: no owner loop drains uiTaskCh, so the posted start waits in the channel; the test sets requestExit, then takes the task out of the
// channel and runs it itself. Both rows must start a turn (runGen is bumped by startTurn); the row without exit shows the harness reaches the start.
func TestQuitAfterTheReleaseOfADeferredStartStillBeginsTheTurn(t *testing.T) {
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
				if m.runGen != 1 {
					t.Fatalf("the start began no turn (exit requested: %v): runGen = %d, want 1", exit, m.runGen)
				}
				m.queueMu.Lock()
				ended := m.turnEnded
				m.queueMu.Unlock()
				select {
				case <-ended:
				case <-time.After(5 * time.Second):
					t.Fatal("the started run did not end")
				}
			})
		}
	}
}
