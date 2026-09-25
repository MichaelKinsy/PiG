package harness

import (
	"encoding/json"
	"errors"
	"testing"
)

// The expected strings were produced by JSON.stringify on the upstream
// packages/agent/src/harness/result.ts classes under Node 24.
func TestTaggedErrorJSONMatchesUpstreamToJSON(t *testing.T) {
	cases := []struct {
		err  TaggedError
		want string
	}{
		{&LaneBusy{Lane: "main", OperationID: "op", OperationKind: "run", Message: "busy"}, `{"_tag":"LaneBusy","message":"busy","name":"LaneBusy","lane":"main","operationId":"op","operationKind":"run"}`},
		{&OperationMismatch{Lane: "main", ExpectedOperationID: "a", Message: "mm"}, `{"_tag":"OperationMismatch","message":"mm","name":"OperationMismatch","lane":"main","expectedOperationId":"a"}`},
		{&OperationMismatch{Lane: "main", ExpectedOperationID: "a", CurrentOperationID: new("b"), LastOperationID: new("c"), Message: "mm"}, `{"_tag":"OperationMismatch","message":"mm","name":"OperationMismatch","lane":"main","expectedOperationId":"a","currentOperationId":"b","lastOperationId":"c"}`},
		{&Closed{Message: "c"}, `{"_tag":"Closed","message":"c","name":"Closed"}`},
		{&UnknownSkill{Name: "deploy", Message: "Unknown skill: deploy"}, `{"_tag":"UnknownSkill","message":"Unknown skill: deploy","name":"deploy"}`},
		{&UnknownTemplate{Name: "review", Message: "Unknown template: review"}, `{"_tag":"UnknownTemplate","message":"Unknown template: review","name":"review"}`},
		{&UnknownTarget{TargetID: "t", Message: "u"}, `{"_tag":"UnknownTarget","message":"u","name":"UnknownTarget","targetId":"t"}`},
		{&InvalidLane{Lane: "l", Reason: "r", Message: "m"}, `{"_tag":"InvalidLane","message":"m","name":"InvalidLane","lane":"l","reason":"r"}`},
		{&InvalidMessage{Lane: "l", Reason: "empty", Message: "m"}, `{"_tag":"InvalidMessage","message":"m","name":"InvalidMessage","lane":"l","reason":"empty"}`},
		{&InvalidNavigation{Lane: "l", Reason: "r", Message: "m"}, `{"_tag":"InvalidNavigation","message":"m","name":"InvalidNavigation","lane":"l","reason":"r"}`},
		{&NoActiveRun{Lane: "l", Message: "m"}, `{"_tag":"NoActiveRun","message":"m","name":"NoActiveRun","lane":"l"}`},
		{&NoActiveOperation{Lane: "l", Message: "m"}, `{"_tag":"NoActiveOperation","message":"m","name":"NoActiveOperation","lane":"l"}`},
		{&NothingToResume{Lane: "l", Message: "m"}, `{"_tag":"NothingToResume","message":"m","name":"NothingToResume","lane":"l"}`},
		{&NothingToCompact{Lane: "l", Message: "m"}, `{"_tag":"NothingToCompact","message":"m","name":"NothingToCompact","lane":"l"}`},
	}
	for _, testCase := range cases {
		encoded, err := json.Marshal(testCase.err)
		if err != nil {
			t.Fatalf("%s: %v", testCase.err.Tag(), err)
		}
		if string(encoded) != testCase.want {
			t.Errorf("%s JSON\n got: %s\nwant: %s", testCase.err.Tag(), encoded, testCase.want)
		}
		if testCase.err.Error() == "" {
			t.Errorf("%s has an empty message", testCase.err.Tag())
		}
	}
}

func TestHarnessFaultAndClosedMessages(t *testing.T) {
	cause := errors.New("disk full")
	fault := &HarnessFault{Message: "commit failed", Cause: cause}
	if !errors.Is(fault, cause) || fault.Error() != "commit failed" {
		t.Fatalf("fault = %v", fault)
	}
	if got := (&HarnessClosed{}).Error(); got != "AgentHarness was closed while the operation was active" {
		t.Fatalf("closed message = %q", got)
	}
}
