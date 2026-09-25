package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunPigletAgentEnvironmentRejectsRuntimeFlagsWithoutEnvironment(t *testing.T) {
	for _, flags := range []map[string]any{{"unsafe-host": true}, {"container-engine": "docker"}} {
		_, _, err := runPigletAgentEnvironment(context.Background(), nil, CLIFlags{UnknownFlags: flags}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "requires a Piglet with agentEnv") {
			t.Fatalf("flags=%v error=%v", flags, err)
		}
	}
}

func TestRuntimeFlagTypesAreStrict(t *testing.T) {
	if _, err := unknownBoolFlag(map[string]any{"unsafe-host": "false"}, "unsafe-host"); err == nil {
		t.Fatal("valued --unsafe-host accepted")
	}
	if _, err := unknownStringFlag(map[string]any{"container-engine": true}, "container-engine", "auto"); err == nil {
		t.Fatal("bare --container-engine accepted")
	}
}
