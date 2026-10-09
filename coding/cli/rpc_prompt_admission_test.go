package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Pi 0.87.1 agent-session.ts:1630-1767 yields prompt preflight while both queue operations advance; rpc-mode.ts:417-424 awaits their replies. agent-loop.ts:116-117 yields between agent_start and turn_start.
func TestRPCPromptPreflightAdmitsQueueBatch(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--offline", "--no-extensions", "--no-session", "--model", "test-faux/faux-1")
	p.send(strings.Join([]string{
		`{"id":"prompt","type":"prompt","message":"TUI_LIVE_STREAM"}`,
		`{"id":"steer","type":"steer","message":"What is 20+22?"}`,
		`{"id":"follow","type":"follow_up","message":"Remember this exact code: letters A L P H A and digits 7 7 4 9"}`,
	}, "\n"))
	want := []string{"queue_update", "queue_update", "response:prompt", "agent_start", "response:steer", "response:follow", "turn_start"}
	var got []string
	p.await("initial turn", func(r rpcRecord) bool {
		kind, _ := r["type"].(string)
		if kind == "response" {
			if r["success"] != true {
				t.Fatal(r)
			}
			kind += ":" + r["id"].(string)
		}
		got = append(got, kind)
		return len(got) == len(want)
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("admission order=%v want=%v", got, want)
	}
	var deltas []string
	p.await("both queued messages consumed", func(r rpcRecord) bool {
		if event, ok := r["assistantMessageEvent"].(map[string]any); ok && event["type"] == "text_delta" {
			deltas = append(deltas, event["delta"].(string))
		}
		return r["type"] == "agent_settled"
	})
	if want := []string{"42", "Alpha"}; !reflect.DeepEqual(deltas, want) {
		t.Fatalf("queued replies=%v want=%v", deltas, want)
	}
	p.closeAndWait("after queued messages settle")
}
