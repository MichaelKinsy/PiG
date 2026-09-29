package agent

import (
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's state.systemPrompt getter runs on the same event loop as writes
// (agent.ts:89-94). Go host callbacks need the corresponding synchronized read.
func TestSystemPromptReadDuringHistoryAndOverrideChanges(t *testing.T) {
	a := NewAgent(AgentOptions{SystemPrompt: "baseline"})
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 1000 {
			a.SetMessages([]AgentMessage{{System: &ai.SystemMessage{Content: ai.SystemText("replayed")}}})
			a.SetSystemPrompt("override")
			a.ClearSystemPrompt()
		}
	})
	workers.Go(func() {
		for range 1000 {
			if got := a.SystemPrompt(); got != "baseline" && got != "replayed" && got != "override" {
				t.Errorf("torn prompt %q", got)
				return
			}
		}
	})
	workers.Wait()
}

func TestSystemPromptSnapshotDistinguishesAbsentAndEmpty(t *testing.T) {
	a := NewAgent(AgentOptions{})
	if prompt, present := a.SystemPromptSnapshot(); present || prompt != "" {
		t.Fatalf("initial prompt %q, %v", prompt, present)
	}
	a.SetSystemPrompt("")
	if prompt, present := a.SystemPromptSnapshot(); !present || prompt != "" {
		t.Fatalf("empty override %q, %v", prompt, present)
	}
	a.ClearSystemPrompt()
	a.SetMessages([]AgentMessage{{System: &ai.SystemMessage{Content: ai.SystemText("")}}})
	if prompt, present := a.SystemPromptSnapshot(); !present || prompt != "" {
		t.Fatalf("empty projection %q, %v", prompt, present)
	}
}

func TestSystemPromptOverrideDoesNotReplayHistory(t *testing.T) {
	a := NewAgent(AgentOptions{})
	a.SetMessages([]AgentMessage{{System: &ai.SystemMessage{Content: ai.SystemText("historical")}}})
	if prompt, present := a.SystemPromptOverride(); present || prompt != "" {
		t.Fatalf("history became an override: %q %v", prompt, present)
	}
	a.SetSystemPrompt("")
	if prompt, present := a.SystemPromptOverride(); !present || prompt != "" {
		t.Fatalf("lost empty override: %q %v", prompt, present)
	}
	a.ClearSystemPrompt()
	if _, present := a.SystemPromptOverride(); present {
		t.Fatal("cleared override remains present")
	}
}

func BenchmarkSystemPromptRead(b *testing.B) {
	for _, count := range []int{1, 4096} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			a := NewAgent(AgentOptions{})
			messages := make([]AgentMessage, count)
			messages[0] = AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("replayed")}}
			a.SetMessages(messages)
			b.ReportAllocs()
			for b.Loop() {
				_ = a.SystemPrompt()
			}
		})
	}
}
