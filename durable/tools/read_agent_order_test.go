package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// agentCountingAPI answers Agent with an error and counts the calls.
type agentCountingAPI struct {
	*fakeAPI
	calls int
}

var errAgentUnavailable = errors.New("agent unavailable")

func (api *agentCountingAPI) Agent(context.Context) (durable.Agent, error) {
	api.calls++
	return durable.Agent{}, errAgentUnavailable
}

// Pi ea448f45 read.ts:109-119 awaits imageModel(api, context), and so api.agent, only for a file detected as an image,
// after it opened and scanned the file. A text read never asks for the conversation's agent, so neither its result nor
// its errors depend on it; an image read does.
func TestReadAsksForTheAgentOnlyForAnImage(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "notes.txt", "text")
	writeText(t, executionEnv, "image.dat", must(base64.StdEncoding.DecodeString(onePixelPNG)))

	api := &agentCountingAPI{fakeAPI: &fakeAPI{env: executionEnv}}
	result, err := executeRead(background, map[string]any{"path": "notes.txt"}, api, nil)
	mustDo(t, err)
	if got := textOutput(result); got != "text" {
		t.Fatalf("text read = %q", got)
	}
	if api.calls != 0 {
		t.Fatalf("a text read asked for the agent %d times, want 0", api.calls)
	}

	if _, err := executeRead(background, map[string]any{"path": "image.dat"}, api, nil); !errors.Is(err, errAgentUnavailable) {
		t.Fatalf("image read error = %v, want the agent's error", err)
	}
	if api.calls != 1 {
		t.Fatalf("an image read asked for the agent %d times, want 1", api.calls)
	}
}
