package extension

// Capability is a typed string for well-known extension capability names.
// Authors may use unknown strings; hosts only enforce the well-known set.
type Capability = string

// Well-known capability names. These mirror the extension API surface groups.
const (
	CapOnEvent           Capability = "onEvent" // umbrella for all on*() subscribers
	CapRegisterTool      Capability = "registerTool"
	CapRegisterCommand   Capability = "registerCommand"
	CapRegisterShortcut  Capability = "registerShortcut"
	CapRegisterFlag      Capability = "registerFlag"
	CapRegisterRenderer  Capability = "registerMessageRenderer"
	CapRegisterProvider  Capability = "registerProvider"
	CapSendMessage       Capability = "sendMessage"
	CapSendUserMessage   Capability = "sendUserMessage"
	CapAppendEntry       Capability = "appendEntry"
	CapSessionMetadata   Capability = "sessionMetadata"
	CapExec              Capability = "exec"
	CapToolIntrospection Capability = "toolIntrospection"
	CapModelControl      Capability = "modelControl"
	CapThinkingLevel     Capability = "thinkingLevel"
	CapEvents            Capability = "events" // EventBus pub/sub
)
