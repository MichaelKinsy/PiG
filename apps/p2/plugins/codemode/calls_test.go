package codemode

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// panickingCall is a tool api whose Output panics with what the test gives it.
type panickingCall struct {
	durable.ToolExecutionApi
	panicWith any
}

func (c panickingCall) Output(any, ...env.ShellOutputSkip) { panic(c.panicWith) }

// Removing code mode tells each running script why through its output, and a call that settled meanwhile is the expected end
// (Pi's catch: "Already settling: the abort below decides"); any other panic is returned for cancel to report, not swallowed.
func TestAnnounceRemovalTakesTheSettledCallsPanicAndReturnsAnyOther(t *testing.T) {
	if got := announceRemoval(context.Background(), nil, panickingCall{panicWith: fmt.Errorf("Tool call %s has settled", "call-1")}); got != nil {
		t.Fatalf("a settled call = %v, want it taken", got)
	}
	other := errors.New("the output store failed")
	if got := announceRemoval(context.Background(), nil, panickingCall{panicWith: other}); got != other {
		t.Fatalf("another error = %v, want it returned", got)
	}
	if got := announceRemoval(context.Background(), nil, panickingCall{panicWith: "not an error"}); got != "not an error" {
		t.Fatalf("a value that is no error = %v, want it returned", got)
	}
}
