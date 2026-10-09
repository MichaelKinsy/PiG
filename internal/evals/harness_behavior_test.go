package evals

// Pins harness.ts behavior that packages/evals/test/harness.test.ts does not reach: sandbox identity parsing
// (harness.ts:104-120), the working-directory check of the documentation exclusion (harness.ts:501-502), the transcript
// error text of a failed tool (harness.ts:218) and the assistant-settlement checks of promptAgent (harness.ts:238-255).

import (
	"os"
	"testing"
)

func TestResolveSandboxIdentityRequiresBothIdsOrNeither(t *testing.T) {
	for _, tc := range []struct {
		name, uid, gid string
		set            [2]bool
		want           *sandboxIdentity
		err            string
	}{
		{name: "neither", want: nil},
		{name: "both", uid: "65532", gid: "65533", set: [2]bool{true, true}, want: &sandboxIdentity{65532, 65533}},
		{name: "uid only", uid: "1", set: [2]bool{true, false}, err: "Set both PI_EVAL_SANDBOX_UID and PI_EVAL_SANDBOX_GID, or neither."},
		{name: "gid only", gid: "1", set: [2]bool{false, true}, err: "Set both PI_EVAL_SANDBOX_UID and PI_EVAL_SANDBOX_GID, or neither."},
		{name: "zero uid", uid: "0", gid: "1", set: [2]bool{true, true}, err: "PI_EVAL_SANDBOX_UID must be a positive integer."},
		{name: "fractional gid", uid: "1", gid: "1.5", set: [2]bool{true, true}, err: "PI_EVAL_SANDBOX_GID must be a positive integer."},
		{name: "unsafe uid", uid: "9007199254740992", gid: "1", set: [2]bool{true, true}, err: "PI_EVAL_SANDBOX_UID must be a positive integer."},
		{name: "text gid", uid: "1", gid: "root", set: [2]bool{true, true}, err: "PI_EVAL_SANDBOX_GID must be a positive integer."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(t, "PI_EVAL_SANDBOX_UID")
			unsetEnv(t, "PI_EVAL_SANDBOX_GID")
			if tc.set[0] {
				t.Setenv("PI_EVAL_SANDBOX_UID", tc.uid)
			}
			if tc.set[1] {
				t.Setenv("PI_EVAL_SANDBOX_GID", tc.gid)
			}
			got, err := resolveSandboxIdentity()
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("identity = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestExcludePiDocumentationRequiresTheWorkingDirectorySectionAfterTheDocumentation(t *testing.T) {
	const withDocs = "head\n<docs>\nbody\n</docs>\ntail"
	for _, prompt := range []string{withDocs, "head\n<cwd>\n/work\n</cwd>\n<docs>\nbody\n</docs>\ntail"} {
		_, err := ExcludePiDocumentation(prompt)
		requireErrorContaining(t, err, "no working-directory section")
	}
	got, err := ExcludePiDocumentation("head\n<docs>\nbody\n</docs>\n<cwd>\n/work\n</cwd>")
	if err != nil || got != "head\n<cwd>\n/work\n</cwd>" {
		t.Fatalf("stripped = %q, %v", got, err)
	}
}

func TestToTranscriptEventsGivesAFailedToolResultItsTextOrAGenericMessage(t *testing.T) {
	events := toTranscriptEvents([]map[string]any{
		{"role": "toolResult", "toolCallId": "a", "toolName": "read", "isError": true, "content": []any{map[string]any{"type": "text", "text": "no such file"}}},
		{"role": "toolResult", "toolCallId": "b", "toolName": "read", "isError": true, "content": []any{}},
		{"role": "toolResult", "toolCallId": "c", "toolName": "read", "content": "fine"},
	})
	if len(events) != 3 {
		t.Fatalf("events = %v", events)
	}
	if got := events[0]["error"].(map[string]any)["message"]; got != "no such file" {
		t.Errorf("first error = %v, want the tool text", got)
	}
	if got := events[1]["error"].(map[string]any)["message"]; got != "Tool failed" {
		t.Errorf("second error = %v, want the generic message", got)
	}
	if _, has := events[2]["error"]; has {
		t.Errorf("third event has an error: %v", events[2])
	}
}

func TestSettledResponseRejectsRunsWithoutAUsableAssistantMessage(t *testing.T) {
	assistant := func(fields map[string]any) map[string]any {
		fields["role"] = "assistant"
		return fields
	}
	for _, tc := range []struct {
		name     string
		messages []map[string]any
		previous int
		want     string
	}{
		{"no messages", nil, 0, "Agent run completed without an assistant message."},
		{"only earlier assistant", []map[string]any{assistant(map[string]any{"stopReason": "stop"})}, 1, "Agent run completed without an assistant message."},
		{"error with message", []map[string]any{assistant(map[string]any{"stopReason": "error", "errorMessage": "quota exceeded"})}, 0, "quota exceeded"},
		{"unexpected stop reason", []map[string]any{assistant(map[string]any{"stopReason": "length"})}, 0, "Agent run ended with unexpected stop reason: length."},
		{"aborted without message", []map[string]any{assistant(map[string]any{"stopReason": "aborted"})}, 0, "Agent run ended with unexpected stop reason: aborted."},
		{"null message", []map[string]any{assistant(map[string]any{"stopReason": "aborted", "errorMessage": nil})}, 0, "Agent run ended with unexpected stop reason: aborted."},
		// `assistant.errorMessage ?? fallback` keeps an empty string: only an absent or null message falls back.
		{"empty message", []map[string]any{assistant(map[string]any{"stopReason": "error", "errorMessage": ""})}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := settledResponse(t.Context(), nil, tc.messages, tc.previous)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestApplyIsolatedEnvironmentSendsPiGStateRootsIntoTheIsolatedHome pins the isolation the with_docs/without_docs runs
// rely on: pig materializes its documentation under PIG_HOME (or PI_HOME), or under XDG_CONFIG_HOME/pig, when one is set, and not under the
// isolated home, so a runner started with PIG_HOME or XDG_CONFIG_HOME in its environment (as CI does) served docs from outside the run and
// the without_docs removal missed them. harness.ts isolateProcessEnvironment keeps no such state root; PiG does, so the
// harness unsets both for the run and restores them afterwards.
func TestApplyIsolatedEnvironmentSendsPiGStateRootsIntoTheIsolatedHome(t *testing.T) {
	outer := map[string]string{"PIG_HOME": "/outer/pig-home", "PI_HOME": "/outer/pi-home", "XDG_CONFIG_HOME": "/outer/xdg-config", "XDG_DATA_HOME": "/outer/xdg-data", "XDG_CACHE_HOME": "/outer/xdg-cache", "XDG_STATE_HOME": "/outer/xdg-state"}
	for name, value := range outer {
		t.Setenv(name, value)
	}
	restore := ApplyIsolatedEnvironment("/tmp/eval-home", "/tmp/eval-agent")
	for name := range outer {
		if value, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q inside the run, want unset", name, value)
		}
	}
	restore()
	for name, want := range outer {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s after restore = %q, want %q", name, got, want)
		}
	}

	unsetEnv(t, "PIG_HOME")
	restore = ApplyIsolatedEnvironment("/tmp/eval-home", "/tmp/eval-agent")
	t.Setenv("PIG_HOME", "set during the run")
	restore()
	if value, ok := os.LookupEnv("PIG_HOME"); ok {
		t.Errorf("PIG_HOME after restore = %q, want it unset as before the run", value)
	}
}
