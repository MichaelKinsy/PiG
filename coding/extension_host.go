package coding

import "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"

// ExtensionHost is the extension host behind the runner of the Sessions a [Runtime] creates. Pi runs extensions in its own process, so a mode reaches them through AgentSession alone; Pig runs file extensions in subprocesses (D19), and a mode reaches the controls of those processes through the Session that owns them, with [Session.ExtensionHost].
//
// The process controls (EndInput, HoldRetirements and TerminateProcesses) act on every host of the process that created the Session, including the hosts of replaced Sessions that still finish a command and of Sessions created later (D70). Pi has one Node process, so those controls have no Pi counterpart.
type ExtensionHost interface {
	// UIBridge returns the bridge that carries the subprocess extensions' UI requests and host actions, or nil when no subprocess extension host runs.
	UIBridge() *subprocess.UIBridge
	// FlushCommands applies the stdin-end window to the commands of runtimes that report none.
	FlushCommands()
	// SetRuntimeDrainHandler installs the handler that runs when the extension runtimes report that nothing keeps their event loops alive.
	SetRuntimeDrainHandler(fn func())
	// SetSettleTailHandler installs the handler that receives the count of extension calls that keep a settle tail open.
	SetSettleTailHandler(fn func(suspended int))
	// SetCommandSuspendHandler installs the handler that receives the count of in-flight extension commands that are suspended.
	SetCommandSuspendHandler(fn func(suspended int))
	// SetCommandWindowHandler installs the handler that receives the count of in-flight extension commands still inside their own window.
	SetCommandWindowHandler(fn func(suspended int))
	// EndInput tells every host of the process, and each host created later, that no further input can arrive.
	EndInput()
	// HoldRetirements keeps every host a later retirement would release running until TerminateProcesses.
	HoldRetirements()
	// TerminateProcesses kills the extension processes of every live host. Only a process about to exit calls it.
	TerminateProcesses()
}

// ExtensionHost returns the extension host of the Runtime that created the Session, or nil when that Runtime has none.
func (s *Session) ExtensionHost() ExtensionHost { return s.extensionHost }
