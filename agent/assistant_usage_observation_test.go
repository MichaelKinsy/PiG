package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestAssistantUsageProjectionPreservesPresenceAndLiveSource(t *testing.T) {
	t.Parallel()
	var absent *AssistantMessage
	if absent.ObserveUsage() != nil || (&AssistantMessage{}).ObserveUsage() != nil {
		t.Fatal("absent usage became present")
	}
	plain := &AssistantMessage{Usage: &ai.Usage{Input: 2, Reasoning: new(3)}}
	copy := plain.ObserveUsage()
	if copy == nil || copy.Input != 2 || copy.Reasoning == nil || *copy.Reasoning != 3 {
		t.Fatalf("plain usage=%#v", copy)
	}
	*copy.Reasoning = 9
	if *plain.Usage.Reasoning != 3 {
		t.Fatal("ordinary usage projection aliases source")
	}
	view := &ai.AssistantMessage{Usage: ai.Usage{Input: 7}}
	message := &AssistantMessage{Usage: &ai.Usage{Input: 1}, streamView: view}
	before := message.ObserveUsage()
	view.Usage.Input = 11
	after := message.ObserveUsage()
	if before.Input != 7 || after.Input != 11 {
		t.Fatalf("live source was not selected or snapshots alias: before=%#v after=%#v", before, after)
	}
	if usage := (&AssistantMessage{Usage: &ai.Usage{}}).ObserveUsage(); usage == nil {
		t.Fatal("present zero usage became absent")
	}
}
