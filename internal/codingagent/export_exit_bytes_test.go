package codingagent

import "github.com/MichaelKinsy/PiG/agent"

// HandleAgentEventsOnOwnerLoop applies events as the owner loop applies the Session's. The caller is on the owner loop's goroutine.
func (m *InteractiveMode) HandleAgentEventsOnOwnerLoop(events ...agent.AgentEvent) {
	for _, event := range events {
		m.handleAgentEvent(event)
	}
}

// SetExtensionTitleForTest is an extension's ctx.ui.setTitle, which runs on the goroutine of the extension's handler.
func (m *InteractiveMode) SetExtensionTitleForTest(title string) { (&ExtUIContext{m: m}).SetTitle(title) }
