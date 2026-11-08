package tui

// Ports packages/chord/src/tui/bridge.ts, its launcher side (the message types). The terminal's bridge (slice 11 §4, decision
// "Slice 11, P8"): the only way between the sandbox, where the app's terminal code and the plugins run, and the trusted launcher
// outside it. The launcher sends input, resizes and the relayed bytes of the Chord link; the sandbox sends frames for the
// terminal, opens and feeds link sockets through the launcher, asks for plugin scripts and the server's base hash, and asks
// for actions on the machine (copy, open a URL, attach a file the user names), which the launcher confirms with the user.
// Messages are plain data, framed over the sandbox's pipes; requests carry an id their reply has. The sandbox's side of the
// bridge is Pi's `src/tui/bridge.ts`, vendored (D601).

// BridgeStart is what the launcher tells the sandbox at its start.
type BridgeStart struct {
	// Base is the base bundle's hash (the sandbox runs it).
	Base string
	// Combined is the text of the combined plugin scripts at start.
	Combined string
	Columns  int
	Rows     int
	// Colors is how many colours the terminal shows: "truecolor" or "256".
	Colors string
	// Mode is the terminal's background: "dark" or "light".
	Mode string
	// Locales are the user's languages, from the system's locale (`de-DE`), most preferred first.
	Locales []string
}

// BridgeRequestKind is the literal union of a request's `kind`.
type BridgeRequestKind string

const (
	BridgeRequestFetch  BridgeRequestKind = "fetch"
	BridgeRequestBase   BridgeRequestKind = "base"
	BridgeRequestCopy   BridgeRequestKind = "copy"
	BridgeRequestOpen   BridgeRequestKind = "open"
	BridgeRequestAttach BridgeRequestKind = "attach"
)

// SandboxMessageKind is the literal union of a sandbox message's `kind`.
type SandboxMessageKind string

const (
	SandboxWrite       SandboxMessageKind = "write"
	SandboxSocketOpen  SandboxMessageKind = "socket_open"
	SandboxSocketSend  SandboxMessageKind = "socket_send"
	SandboxSocketClose SandboxMessageKind = "socket_close"
	SandboxRequest     SandboxMessageKind = "request"
	SandboxOutdated    SandboxMessageKind = "outdated"
	SandboxFailed      SandboxMessageKind = "failed"
	SandboxExit        SandboxMessageKind = "exit"
)

// LauncherMessageKind is the literal union of a launcher message's `kind`.
type LauncherMessageKind string

const (
	LauncherInput         LauncherMessageKind = "input"
	LauncherResize        LauncherMessageKind = "resize"
	LauncherSocketOpened  LauncherMessageKind = "socket_opened"
	LauncherSocketMessage LauncherMessageKind = "socket_message"
	LauncherSocketClosed  LauncherMessageKind = "socket_closed"
	LauncherReply         LauncherMessageKind = "reply"
	LauncherAttached      LauncherMessageKind = "attached"
	LauncherRefused       LauncherMessageKind = "refused"
)

// BridgeRequest is a request of the sandbox the launcher answers: Kind "fetch" (Path), "base", "copy" (Text), "open" (URL) or
// "attach" (Path: a file of the machine the user names).
type BridgeRequest struct {
	Kind BridgeRequestKind
	Path string
	Text string
	URL  string
}

// SandboxMessage is what the sandbox sends. Kind is "write" (Data), "socket_open" (Socket), "socket_send" (Socket, Bytes),
// "socket_close" (Socket), "request" (ID, Request), "outdated" (the sandbox's code is outdated: the launcher starts a new
// one), "failed" (the base bundle failed to start: Message; the launcher shows none of the sandbox's own output) or "exit"
// (Code).
type SandboxMessage struct {
	Kind    SandboxMessageKind
	Data    string
	Socket  int
	Bytes   []byte
	ID      int
	Request BridgeRequest
	Message string
	Code    int
}

// LauncherMessage is what the launcher sends. Kind is "input" (Data), "resize" (Columns, Rows), "socket_opened" (Socket),
// "socket_message" (Socket, Bytes), "socket_closed" (Socket), "reply" (ID, Value), "attached" (ID, Name, Bytes: an attach
// request's answer, the file the user confirmed) or "refused" (ID, Message).
type LauncherMessage struct {
	Kind    LauncherMessageKind
	Data    string
	Columns int
	Rows    int
	Socket  int
	ID      int
	Value   any
	Name    string
	Bytes   []byte
	Message string
}
