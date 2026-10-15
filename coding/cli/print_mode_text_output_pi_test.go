package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// print-mode.ts:138-158: in text mode, after the prompts, the session's last message decides the output. An assistant message that stopped with
// "error" or "aborted" writes `errorMessage || "Request <stopReason>"` to stderr and fails the run; any other assistant message writes each text
// block on its own line to stdout and nothing for thinking blocks.
func TestPrintModeTextOutputFollowsPisLastMessageRules(t *testing.T) {
	for _, tc := range []struct {
		name       string
		response   ai.FauxResponse
		wantStdout string
		wantStderr string
		wantFailed bool
	}{
		{
			name: "every text block on its own line, thinking skipped",
			response: ai.FauxResponse{StopReason: "stop", Content: []ai.FauxContentBlock{
				ai.FauxText("first"), ai.FauxThinking("hidden reasoning"), ai.FauxText("second\nline"),
			}},
			wantStdout: "first\nsecond\nline\n",
		},
		{
			name:       "error with a message prints it",
			response:   ai.FauxResponse{StopReason: "error", ErrorMessage: "provider exploded"},
			wantStderr: "provider exploded\n", wantFailed: true,
		},
		{
			name:       "error without a message prints Request error",
			response:   ai.FauxResponse{StopReason: "error"},
			wantStderr: "Request error\n", wantFailed: true,
		},
		{
			name:       "aborted without a message prints Request aborted",
			response:   ai.FauxResponse{StopReason: "aborted"},
			wantStderr: "Request aborted\n", wantFailed: true,
		},
		{
			name: "an errored message prints no text blocks",
			response: ai.FauxResponse{StopReason: "error", ErrorMessage: "boom", Content: []ai.FauxContentBlock{
				ai.FauxText("partial answer"),
			}},
			wantStderr: "boom\n", wantFailed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses(fauxSteps(tc.response))
			result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{Mode: "text", InitialMessage: "go"})
			if result.stdout != tc.wantStdout || result.stderr != tc.wantStderr {
				t.Errorf("stdout %q stderr %q, want stdout %q stderr %q", result.stdout, result.stderr, tc.wantStdout, tc.wantStderr)
			}
			if failed := errors.Is(result.err, errPrintModeHandled); failed != tc.wantFailed || (!tc.wantFailed && result.err != nil) {
				t.Errorf("err = %v, want failed=%v", result.err, tc.wantFailed)
			}
		})
	}
}

// print-mode.ts:122-127: in json mode the first line written is the session header, before any event.
func TestPrintModeJSONWritesTheSessionHeaderFirst(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(fauxTextResponse("answer")))
	result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{Mode: "json", InitialMessage: "go"})
	if result.err != nil || result.stderr != "" {
		t.Fatalf("err = %v, stderr %q", result.err, result.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(result.stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("stdout %q: want a header line and events", result.stdout)
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("first line %q is not JSON: %v", lines[0], err)
	}
	if header["type"] != "session" || header["id"] == nil || header["cwd"] == nil {
		t.Errorf("first line = %v, want the session header (type session, id, cwd)", header)
	}
	for i, line := range lines[1:] {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil || event["type"] == "session" {
			t.Errorf("line %d = %q: events follow the header and are not headers (%v)", i+1, line, err)
		}
	}
}
