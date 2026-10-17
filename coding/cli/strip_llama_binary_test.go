package cli

import (
	"strings"
	"testing"
)

// A Binary that compiled llama.cpp out has no /llama command: print mode sends `/llama` to the model as an ordinary prompt
// instead of running the llama host (which warns "/llama is available in interactive mode"), and `-e builtin:llama.cpp`
// is skipped silently like any stripped built-in.
func TestStrippedBinaryHasNoLlamaCommand(t *testing.T) {
	stdout, stderr, code := runStrippedPig(t, "", "--mode", "json", "--model", "test-faux/faux-1", "-e", "builtin:llama.cpp", "/llama")
	if code != 0 || strings.Contains(stderr, "extension") || strings.Contains(stderr+stdout, "/llama is available in interactive mode") {
		t.Fatalf("exit %d\nstdout: %q\nstderr: %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"type":"agent_start"`) || !strings.Contains(stdout, `"text":"/llama"`) {
		t.Fatalf("/llama did not reach the model as a prompt\nstdout: %q\nstderr: %q", stdout, stderr)
	}
}
