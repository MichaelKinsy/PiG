package codingagent

import "github.com/MichaelKinsy/PiG/coding/extension"

// runtimeErrorPath is the ExtensionError path upstream uses for a failure of the runtime's own bound actions (agent-session.ts:3556-3572).
const runtimeErrorPath = "<runtime>"

// reportRuntimeError reports err as the extension error `event`, as Pi's void actions sendMessage and sendUserMessage do when the
// Promise they start rejects (`runner.emitError({ extensionPath: "<runtime>", event, error })`): the extension's call itself returns.
// The Session's binding in print, JSON and RPC mode does the same (coding.Session.reportRuntimeError).
func (m *InteractiveMode) reportRuntimeError(event string, err error) {
	if err == nil || m.newRunner == nil {
		return
	}
	m.newRunner.EmitError(&extension.ExtensionError{ExtensionPath: runtimeErrorPath, Event: event, Error: err.Error()})
}

// reportRuntimeErrorAfter is reportRuntimeError for a send whose caller asked to make the report when the sending code's run ends (subprocess.SendUserMessageOptions.AfterRun).
func (m *InteractiveMode) reportRuntimeErrorAfter(after func(report func()), event string, err error) {
	if err == nil {
		return
	}
	if after == nil {
		m.reportRuntimeError(event, err)
		return
	}
	after(func() { m.reportRuntimeError(event, err) })
}
