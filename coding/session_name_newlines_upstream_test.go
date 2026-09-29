package coding

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Ports packages/coding-agent/test/suite/regressions/5996-session-name-newlines.test.ts:14,24.
func TestUpstreamSessionNameNewlines(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		bridge            bool
	}{
		{"filters newlines when AgentSession.setSessionName is called", "hello\nworld\r\nagain", "hello world again", false},
		{"filters newlines when an extension calls pi.setSessionName", "from\nextension", "from extension", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRecoveryHarness(t, harnessOptions{}).session
			var mu sync.Mutex
			var names []string
			unsubscribe := s.Subscribe(func(event agent.AgentEvent) {
				info, ok := event.(agent.SessionInfoChangedEvent)
				if !ok {
					return
				}
				mu.Lock()
				names = append(names, info.Name)
				mu.Unlock()
			})
			defer unsubscribe()
			if tc.bridge {
				bridge := subprocess.NewUIBridge(nil)
				bridge.SetHostAction("setSessionName", s.SetSessionName)
				args, err := json.Marshal(map[string]string{"name": tc.input})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := bridge.HandleCall("name-test", &subprocess.CallPayload{Method: "setSessionName", Args: args}); err != nil {
					t.Fatal(err)
				}
			} else if err := s.SetSessionName(tc.input); err != nil {
				t.Fatal(err)
			}
			if got := s.SessionName(); got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
			if err := s.FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(names, []string{tc.want}) {
				t.Errorf("events = %q, want [%q]", names, tc.want)
			}
		})
	}
}
