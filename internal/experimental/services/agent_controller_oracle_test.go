package services

import (
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestControllerMatchesPinnedProvider(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "node", "testdata/controller-oracle.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("pinned upstream probe: %v\n%s", err, output)
	}
	var want []AgentOperationResponse
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("upstream output: %v\n%s", err, output)
	}
	var got []AgentOperationResponse
	for _, status := range []string{"completed", "declined", "aborted", "failed", "suspended"} {
		controller := CreateAgentController(&controllerLane{operation: func(context.Context, string, []ai.ImageContent) (LaneOperation, error) {
			return LaneOperation{OperationID: "op", Status: status, Error: &AgentOperationError{Code: "provider", Message: "failed"}}, nil
		}})
		response, err := controller.Prompt(t.Context(), AgentPromptRequest{Message: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, response)
	}
	for _, failure := range []error{
		&harness.LaneBusy{OperationID: "busy", Message: "failure"},
		&harness.InvalidMessage{Message: "failure"},
		&harness.UnknownSkill{Message: "failure"},
		&harness.UnknownTemplate{Message: "failure"},
		&harness.NothingToCompact{Message: "failure"},
		&harness.NothingToResume{Message: "failure"},
		&harness.InvalidNavigation{Message: "failure"},
		&harness.UnknownTarget{Message: "failure"},
		&harness.Closed{Message: "failure"},
		&harness.NoActiveOperation{Message: "failure"},
	} {
		controller := CreateAgentController(&controllerLane{operation: func(context.Context, string, []ai.ImageContent) (LaneOperation, error) {
			return LaneOperation{}, failure
		}})
		response, err := controller.Prompt(t.Context(), AgentPromptRequest{Message: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, response)
	}
	if !reflect.DeepEqual(got, want) {
		actual, _ := json.Marshal(got)
		t.Fatalf("provider differs\nGo: %s\nPi: %s", actual, output)
	}
}
