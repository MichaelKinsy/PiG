package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

// packages/coding-agent/test/agent-session-branching.test.ts:90,110,131.
// Original message-count assertions at105/127/152 disagree with Pi0.87.1's implementation: retained native system entries are observable. session-branching-oracle-pi.mjs probes these same inputs without filtering them.
func TestRuntimeOriginalBranching(t *testing.T) {
	for _, tc := range []struct {
		site    int
		name    string
		memory  bool
		prompts []string
		pick    int
		roles   []string
	}{
		{90, "should allow forking from single message", false, []string{"Say hello"}, 0, []string{"system"}},
		{110, "should support in-memory forking in --no-session mode", true, []string{"Say hi"}, 0, []string{"system"}},
		{131, "should fork from middle of conversation", false, []string{"Say one", "Say two", "Say three"}, 1, []string{"system", "user", "assistant"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRuntimeTestHarness(t, runtimeTestOptions{memory: tc.memory})
			if tc.memory && h.runtime.Session().Path() != "" {
				t.Fatal("memory session has a path")
			}
			for _, prompt := range tc.prompts {
				runtimePrompt(t, h.runtime, prompt)
			}
			previous := h.runtime.Session()
			// agent-session-runtime.ts:164-193,291-349 replaces factory-owned Services/Session/runner; memory forks alone retain their log manager.
			previousManager := previous.SessionManager()
			previousServices := h.runtime.Services()
			previousRunner := previous.currentRunner()
			users := previous.UserMessagesForForking()
			if len(users) != len(tc.prompts) {
				t.Fatalf("users=%v", users)
			}
			if tc.pick == 0 && users[0].Text != tc.prompts[0] {
				t.Fatalf("text=%q", users[0].Text)
			}
			if len(previous.Messages()) == 0 {
				t.Fatal("prompted session has no messages")
			}
			result, err := h.runtime.Fork(t.Context(), users[tc.pick].EntryID, nil)
			if err != nil || result.Cancelled || result.SelectedText == nil || *result.SelectedText != tc.prompts[tc.pick] {
				t.Fatalf("fork=%+v error=%v", result, err)
			}
			if previous == h.runtime.Session() {
				t.Fatal("fork did not replace the actual Session")
			}
			if h.runtime.Services() == previousServices {
				t.Fatal("fork retained outgoing factory Services")
			}
			if previousRunner == h.runtime.Session().currentRunner() || !previousRunner.IsStale() {
				t.Fatal("fork retained a live outgoing runner")
			}
			if tc.memory && h.runtime.Session().SessionManager() != previousManager {
				t.Fatal("in-memory fork replaced its SessionManager")
			}
			if !tc.memory && h.runtime.Session().SessionManager() == previousManager {
				t.Fatal("persisted fork reused the outgoing SessionManager")
			}
			var roles []string
			for _, message := range h.runtime.Session().Messages() {
				roles = append(roles, message.Role())
			}
			if !reflect.DeepEqual(roles, tc.roles) {
				t.Fatalf("roles=%v want=%v", roles, tc.roles)
			}
			if os.Getenv("PIG_RUNTIME_REPLACEMENT_PROBE") != "" {
				var file any = "undefined"
				if path := h.runtime.Session().Path(); path != "" {
					_, err := os.Stat(path)
					file = err == nil
				}
				record := struct {
					Site         int      `json:"site"`
					Prompts      []string `json:"prompts"`
					SelectedText string   `json:"selectedText"`
					Roles        []string `json:"roles"`
					File         any      `json:"file"`
				}{tc.site, tc.prompts, *result.SelectedText, roles, file}
				data, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Println("BRANCHING_ORACLE " + string(data))
			}
			if tc.memory {
				if h.runtime.Session().Path() != "" {
					t.Fatal("in-memory fork acquired a path")
				}
			} else if tc.pick == 0 {
				if h.runtime.Session().Path() == "" {
					t.Fatal("persisted fork has no selected path")
				}
				if _, err := os.Stat(h.runtime.Session().Path()); !os.IsNotExist(err) {
					t.Fatalf("fork file must be deferred: %v", err)
				}
			}
		})
	}
}
