package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// pi: packages/durable/src/tools/env.ts

type envOnlyAPI struct {
	durable.ToolExecutionApi
	executionEnv env.ExecutionEnv
}

func (a envOnlyAPI) Env() env.ExecutionEnv { return a.executionEnv }

type markerEnv struct{ env.ExecutionEnv }

// requireEnv (env.ts:5-8) returns the call's execution environment, and a tool without one fails with an ordinary error whose message is
// "No execution environment is configured".
func TestRequireEnvMatchesPi(t *testing.T) {
	configured := markerEnv{}
	got, err := requireEnv(envOnlyAPI{executionEnv: configured})
	if err != nil {
		t.Fatalf("requireEnv with an environment: %v", err)
	}
	if got != env.ExecutionEnv(configured) {
		t.Fatalf("requireEnv returned %v, want the configured environment", got)
	}

	got, err = requireEnv(envOnlyAPI{})
	if err == nil || err.Error() != "No execution environment is configured" {
		t.Fatalf("requireEnv without an environment: err = %v, want the Pi message", err)
	}
	if got != nil {
		t.Fatalf("requireEnv without an environment returned %v, want nil", got)
	}
}
