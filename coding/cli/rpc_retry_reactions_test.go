package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Pi 0.87.1's aborted sleep reaction precedes the resolved abort_retry responses. A full abort awaits settlement, while later synchronous commands in the same input batch still observe the active Session.
func TestRPCRetryCancellationReactionBatches(t *testing.T) {
	for _, test := range []struct {
		name, middle string
		want         []string
	}{
		{"repeated cancellation", `{"id":"state","type":"get_state"}\n{"id":"b","type":"abort_retry"}`, []string{"retry-end", "response:a", "response:state", "response:b", "settled"}},
		{"abort does not block later input", `{"id":"stop","type":"abort"}\n{"id":"state","type":"get_state"}`, []string{"retry-end", "response:a", "response:state", "settled", "response:stop"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--offline", "--no-extensions", "--no-session", "--no-context-files", "--no-skills", "--system-prompt", "RPC retry probe.", "--model", "test-faux/faux-1")
			p.send(`{"id":"p","type":"prompt","message":"Trigger: retryable provider error"}`)
			p.await("retry start", func(record rpcRecord) bool { return record["type"] == "auto_retry_start" })
			p.send("{\"id\":\"a\",\"type\":\"abort_retry\"}\n" + strings.ReplaceAll(test.middle, `\n`, "\n"))
			var trace []string
			p.await("retry reactions and settlement", func(record rpcRecord) bool {
				switch record["type"] {
				case "auto_retry_end":
					if record["attempt"] != float64(1) || record["success"] != false || record["finalError"] != "Retry cancelled" {
						t.Fatalf("retry end = %#v", record)
					}
					trace = append(trace, "retry-end")
				case "response":
					id, _ := record["id"].(string)
					if record["success"] != true {
						t.Fatalf("response = %#v", record)
					}
					if id == "state" && record["data"].(map[string]any)["isStreaming"] != true {
						t.Error("later synchronous input waited for abort settlement")
					}
					trace = append(trace, "response:"+id)
				case "agent_settled":
					trace = append(trace, "settled")
				}
				return len(trace) == len(test.want)
			})
			if !reflect.DeepEqual(trace, test.want) {
				t.Fatalf("reaction order=%v, want Pi %v", trace, test.want)
			}
			p.closeAndWait("after retry reaction batch")
		})
	}
}
