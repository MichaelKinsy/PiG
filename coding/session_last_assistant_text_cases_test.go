package coding

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// lastAssistantTextCase is one history of testdata/last_assistant_text_cases.json: the messages as a session holds them and what Pi's
// getLastAssistantText returns for them (null for undefined).
type lastAssistantTextCase struct {
	Name     string               `json:"name"`
	Messages []agent.AgentMessage `json:"messages"`
	Want     *string              `json:"want"`
}

func lastAssistantTextCases(t *testing.T) []lastAssistantTextCase {
	t.Helper()
	data, err := os.ReadFile("testdata/last_assistant_text_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []lastAssistantTextCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("testdata/last_assistant_text_cases.json holds no case")
	}
	return cases
}

func quotedText(text *string) string {
	if text == nil {
		return "nil"
	}
	return strconv.Quote(*text)
}

// Pi ede360e4d (#12121, issue #12119) packages/coding-agent/test/suite/regressions/12119-copy-after-abort-or-error.test.ts: /copy and RPC
// get_last_assistant_text read getLastAssistantText, which skips an assistant message without text whose turn did not end normally
// (stopReason other than "stop"). The three cases of Pi's test are the "Pi 12119:" rows, with the histories its faux provider leaves:
//
//	"returns the previous answer after Esc during a tool call"             the answer, a tool-call-only message, its result, an empty aborted message -> "Here is the answer."
//	"returns the previous answer after a provider error"                   the answer, an empty message with stopReason error                         -> "Here is the answer."
//	"keeps a reply that ended normally without text as the last reply"     the answer, an empty message with stopReason stop                          -> undefined
//
// Pi's test says nothing about the other rows (an aborted turn with partial text, an empty text block, a turn of tool calls only, an orphaned
// tool call, length, a message with no or an unknown stopReason, thinking only, two skipped turns, a session with nothing to copy, and
// JavaScript's trim on U+FEFF, U+0085 and U+00A0). Their `want` is what Pi's function returned when it was RUN at ede360e4d (the method
// taken from agent-session.ts at the commit; its parent returned null for 21 of them); TestSessionLastAssistantTextMatchesPi
// runs the installed Pi over the same file.
func TestSessionLastAssistantTextCases(t *testing.T) {
	for _, tc := range lastAssistantTextCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			h := newModelExtensionHarness(t, []bool{false}, "", true, extension.Extension{}, nil)
			h.session.Agent().SetMessages(tc.Messages)
			if got := h.session.LastAssistantText(); quotedText(got) != quotedText(tc.Want) {
				t.Fatalf("LastAssistantText = %s, want %s", quotedText(got), quotedText(tc.Want))
			}
		})
	}
}

// The Pi the parity tests compare with carries ede360e4d as a post-release backport (automation/gen/pi-post-release-backports.mjs): its
// getLastAssistantText returns each case's `want`. Without the block the installed Pi is the pinned release, which returns null where a
// turn without an answer follows the answer, and this test fails.
func TestSessionLastAssistantTextMatchesPi(t *testing.T) {
	cases := lastAssistantTextCases(t)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/last_assistant_text.mjs", pigversion.UpstreamPackageVersion)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var results []struct {
		Name string  `json:"name"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(cases) {
		t.Fatalf("Pi answered %d cases, want %d", len(results), len(cases))
	}
	for i, tc := range cases {
		if results[i].Name != tc.Name || quotedText(results[i].Text) != quotedText(tc.Want) {
			t.Errorf("%s: the installed Pi returns %s, want %s", tc.Name, quotedText(results[i].Text), quotedText(tc.Want))
		}
	}
}
