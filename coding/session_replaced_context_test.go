package coding

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's createReplacedSessionContext (agent-session.ts:4375-4383) copies the runner's command context and rebinds sendMessage and sendUserMessage
// to the Session's own awaited operations (this.sendCustomMessage, this.sendUserMessage), so a withSession callback that sends a message that
// triggers a turn does not return before the turn settles. The extension action bound on the runner (Session.SendMessage) starts that work in the
// background instead.
func TestCreateReplacedSessionContextSendMessageAwaitsTheTriggeredTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		h := newRuntimeTestHarness(t, runtimeTestOptions{tools: []agent.AgentTool{runtimeAbortTool{started: started}}})
		h.provider.responses = []scriptedResponse{fauxToolCall("block")}
		bindReplacementCommands(t, h.runtime)
		session := h.runtime.Session()
		replaced := session.CreateReplacedSessionContext(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- replaced.SendMessage(extension.CustomMessageRef{CustomType: "note", Content: "wake", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		}()
		<-started
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("SendMessage returned before the triggered turn settled: %v", err)
		default:
		}
		session.RequestAbort()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, entry := range session.Entries() {
			if raw := string(entry.Raw()); strings.Contains(raw, `"customType":"note"`) {
				found = true
			}
		}
		if !found {
			t.Fatal("the custom message the replaced context sent is not in the Session")
		}
	})
}
