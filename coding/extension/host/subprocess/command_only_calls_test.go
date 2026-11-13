package subprocess

import (
	"strings"
	"testing"
)

// Pi v1.5.2 runner.ts:1004-1038 gives only the command context newSession, fork, navigateTree, switchSession and reload.
func TestCommandOnlyCallsAreThePiCommandContextSessionActions(t *testing.T) {
	for _, method := range []string{"newSession", "fork", "navigateTree", "switchSession", "reload"} {
		if !commandOnlyCall(method) {
			t.Errorf("%s is not command-only", method)
		}
	}
	for _, method := range []string{"waitForIdle", "sendMessage", "ui.notify", "exec"} {
		if commandOnlyCall(method) {
			t.Errorf("%s is command-only", method)
		}
	}
}

func TestCommandOnlyCallErrorIsPisMissingFunctionError(t *testing.T) {
	err := commandOnlyCallError("switchSession")
	if err.Code != "not_a_command_context" || !strings.HasPrefix(err.Message, "ctx.switchSession is not a function") {
		t.Fatalf("commandOnlyCallError = %+v", err)
	}
}
