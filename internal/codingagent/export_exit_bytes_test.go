package codingagent

import "github.com/MichaelKinsy/PiG/agent"

// HandleAgentEventsOnOwnerLoop applies events as the owner loop applies the Session's, and paints the frame they request, or the frame already
// requested when there are none: that frame is paced to come due some milliseconds later, and a child that wrote its ready marker before then would
// have it fall into the bytes the test counts from the marker. The caller is on the owner loop's goroutine.
func (m *InteractiveMode) HandleAgentEventsOnOwnerLoop(events ...agent.AgentEvent) {
	for _, event := range events {
		m.handleAgentEvent(event)
	}
	m.tuiInst.Render()
}

// SetExtensionStatusForTest is an extension's ctx.ui.setStatus, which runs on the goroutine of the extension's handler.
func (m *InteractiveMode) SetExtensionStatusForTest(key, text string) {
	(&ExtUIContext{m: m}).SetStatus(key, text)
}

// SetExtensionTitleForTest is an extension's ctx.ui.setTitle, which runs on the goroutine of the extension's handler.
func (m *InteractiveMode) SetExtensionTitleForTest(title string) {
	(&ExtUIContext{m: m}).SetTitle(title)
}
