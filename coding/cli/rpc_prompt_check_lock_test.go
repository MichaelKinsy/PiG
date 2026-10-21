package cli

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A prompt whose compaction check finds the Session busy (BeginPromptCompactionCheck reports ok = false because another holder has the Session lock) does
// not skip the check: it runs off the input loop, where CheckPromptCompaction waits for the holder, decides on the Session the holder left, and resolves
// after it. The holder is a due compaction from the exported BeginPromptCompactionCheck, which keeps the Session lock while its summary request is held
// open; its summary fails, so the Session is still over the threshold when the waiting check decides, and the waiting check runs the one compaction.
// Resolving the busy case as "nothing due" resolves while the lock is held and leaves the Session uncompacted.
func TestRPCPromptCompactionCheckWaitsForABusySession(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":100}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	summaryStarted, releaseSummary := make(chan struct{}), make(chan struct{})
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
		close(summaryStarted)
		<-releaseSummary
		return ai.FauxAssistantMessage(ai.FauxContentString(""), ai.FauxAssistantMessageOptions{StopReason: "error", ErrorMessage: "summary failed"}), nil
	}), ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("summary")}, StopReason: "stop"})})
	model := &ai.Model{ID: "faux-1", Provider: faux, Capabilities: ai.ModelCapabilities{ContextWindow: 10_000, MaxOutputTokens: 100}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	now := time.Now().UnixMilli()
	for i, message := range []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "old question"}}, Timestamp: now - 10}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "old answer"}}, Provider: "faux", ModelID: "faux-1",
			Usage: &ai.Usage{Input: 9_945, Output: 5, TotalTokens: 9_950}, StopReason: ai.StopReasonStop, Timestamp: now - 9}},
	} {
		if _, err := session.Inner().AppendMessage(message); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	session.RefreshContext()

	due, ok, err := session.BeginPromptCompactionCheck(t.Context())
	if err != nil || !ok || due == nil {
		t.Fatalf("BeginPromptCompactionCheck on an idle Session over the threshold = (due %v, ok %v, err %v), want a due compaction", due != nil, ok, err)
	}
	holderDone := make(chan error, 1)
	go func() { holderDone <- due(t.Context()) }()
	select {
	case <-summaryStarted:
	case <-time.After(testbudget.Wait(t)):
		close(releaseSummary)
		t.Fatal("the holding compaction never requested its summary")
	}

	turn := &rpcResponseTurn{write: func(any) {}}
	admission := &rpcAdmission{ctx: t.Context(), turn: turn, session: session}
	resolved := make(chan error, 1)
	var released, resolvedEarly atomic.Bool
	turn.Begin()
	admission.promptCompactionCheck().then(func(_ struct{}, err error) {
		resolvedEarly.Store(!released.Load())
		resolved <- err
	})
	turn.End()
	select {
	case err := <-resolved:
		close(releaseSummary)
		t.Fatalf("the check resolved (err %v) while another holder kept the Session lock, want it to wait off the loop", err)
	default:
	}
	released.Store(true)
	close(releaseSummary)
	select {
	case err := <-holderDone:
		if err != nil {
			t.Fatalf("the holding compaction returned %v, want its summary failure reported as an event, not returned", err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the holding compaction never finished")
	}
	select {
	case err := <-resolved:
		if err != nil {
			t.Fatalf("the check failed: %v", err)
		}
		if resolvedEarly.Load() {
			t.Fatal("the check resolved before the holder released the Session lock")
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the check never resolved after the holder released the Session lock")
	}
	compactions := 0
	for _, entry := range session.Inner().GetEntries() {
		if entry.Base().Type == "compaction" {
			compactions++
		}
	}
	if compactions != 1 {
		t.Fatalf("%d compaction entries, want the one compaction the waiting check ran after the holder's summary failed", compactions)
	}
}
