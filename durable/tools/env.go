package tools

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// requireEnv is the call's execution environment; a tool without one fails with
// an ordinary error result.
//
// Ports packages/durable/src/tools/env.ts.
func requireEnv(api durable.ToolExecutionApi) (env.ExecutionEnv, error) {
	executionEnv := api.Env()
	if executionEnv == nil {
		return nil, errors.New("No execution environment is configured")
	}
	return executionEnv, nil
}
