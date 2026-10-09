package coding

import (
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestSessionSystemPromptPublishesBaselineSafely(t *testing.T) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{SystemPrompt: "BASE"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 100 {
			session.SetSystemPromptSections(ai.OrderedSections{{Name: "preamble", Value: new("NEXT")}})
		}
	})
	workers.Go(func() {
		for range 100 {
			got := session.SystemPrompt()
			if !strings.Contains(got, "BASE") && !strings.Contains(got, "NEXT") {
				t.Errorf("torn baseline %q", got)
				return
			}
		}
	})
	workers.Wait()
	session.agent.SetSystemPrompt("")
	if got := session.SystemPrompt(); got != "" {
		t.Fatalf("empty override fell back to baseline: %q", got)
	}
}
