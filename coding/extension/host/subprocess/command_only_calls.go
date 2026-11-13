package subprocess

import "fmt"

// commandOnlyCall reports whether method is a session-changing action that only Pi's command context has (runner.ts createCommandContext, v1.5.2 :1004-1038). Pi's
// createContext() object, which every event, tool and shortcut handler receives, has no such member.
func commandOnlyCall(method string) bool {
	switch method {
	case "newSession", "fork", "navigateTree", "switchSession", "reload":
		return true
	}
	return false
}

// commandOnlyCallError is the error a command-only call answers when it is made through a context that is not a command context: Pi's TypeError for a member its
// context does not have. Node's runtime never sends the call, because its event context has no such member.
// pig divergence (D617): the Go, Python, Rust and Lisp SDKs have one static Context type with the method on it, so the call is written, sent and answered here.
func commandOnlyCallError(method string) *ErrorInfo {
	return &ErrorInfo{Code: "not_a_command_context", Message: fmt.Sprintf("ctx.%s is not a function: only a command context has it", method)}
}
