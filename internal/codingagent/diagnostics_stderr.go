package codingagent

// Ports packages/coding-agent/src/modes/diagnostics-stderr.ts: the CLI and the non-interactive modes write runtime diagnostics to stderr.

import (
	"fmt"
	"os"
)

// WriteDiagnosticsToStderr prints each diagnostic to stderr as "Error: ..." in red, "Warning: ..." in yellow or an info line dimmed. Like upstream's writeDiagnosticsToStderr, which styles them with the default
// chalk instance, the colors follow chalk's stdout color level, not whether stderr is a terminal. RPC mode writes its diagnostics here as well, never to stdout, which carries the protocol.
func WriteDiagnosticsToStderr(diagnostics []AgentSessionRuntimeDiagnostic) {
	color := chalkModifiersEnabled()
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(os.Stderr, formatReportedDiagnostic(diagnostic, color))
	}
}

func formatReportedDiagnostic(diagnostic AgentSessionRuntimeDiagnostic, color bool) string {
	var prefix, open, closeSeq string
	switch diagnostic.Type {
	case "error":
		prefix, open, closeSeq = "Error: ", "\x1b[31m", "\x1b[39m"
	case "warning":
		prefix, open, closeSeq = "Warning: ", "\x1b[33m", "\x1b[39m"
	default:
		open, closeSeq = "\x1b[2m", "\x1b[22m"
	}
	if !color {
		return prefix + diagnostic.Message
	}
	return ChalkApply(open, closeSeq, prefix+diagnostic.Message)
}
