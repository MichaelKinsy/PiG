package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi packages/coding-agent/src/core/agent-session.ts:1673-1691 validates the
// Session model before accepting a prompt. A bare Agent's unknown descriptor
// and injected stream do not supply a Session's native provider runtime.
func TestSessionMissingModelRejectsBeforeAcceptance(t *testing.T) {
	for _, path := range []string{"session", "direct-agent"} {
		t.Run(path, func(t *testing.T) {
			s, err := NewSession(newTestServices(t), SessionOptions{SystemPrompt: "configured instructions"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			accepted := false
			if path == "session" {
				_, err = s.SendContentWithPreflight(context.Background(), BuildUserContent("hello", nil), func() { accepted = true })
			} else {
				_, err = s.Agent().Send(context.Background(), "hello")
			}
			if !errors.Is(err, agent.ErrNoModelSelected) || accepted || len(s.Messages()) != 0 || s.GetSessionStats().TotalMessages != 0 {
				t.Fatalf("error=%v accepted=%v messages=%v", err, accepted, s.Messages())
			}
		})
	}
}
