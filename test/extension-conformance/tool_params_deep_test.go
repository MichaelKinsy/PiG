package extensionconformance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tool arguments nested deeply, read by every SDK through ToolParams and ToolInput. Pi has no fixed limit: its own runtime (the Node SDK) returns the object while V8 has
// stack for JSON.stringify, which ends between 4300 and 4400 levels on the pinned Node (it moves with the Node version and --stack-size), and fails the call with V8's RangeError
// (`Maximum call stack size exceeded`) beyond. The Go, Python, Rust and Lisp SDKs do not reproduce that stack exhaustion (D268): each returns the object, and the texts Pi writes
// for it, for every frame the host can send (its own marshal refuses a value nested past 10000 levels). No SDK ends its extension or its connection on deep input: the same
// extension answers the next call after each depth.
func TestToolParamsNestingAcrossSDKs(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("[", n) + "1" + strings.Repeat("]", n) }
	call := func(r *modelTypesRig, depth int) (report struct {
		Input *string  `json:"input"`
		Keys  []string `json:"keys"`
		Meta  *string  `json:"meta"`
	}, failure string) {
		raw := `{"zeta":1,"meta":{"d":` + deep(depth) + `}}`
		result, err := r.ext.Tools["c3_tool_call"].Definition.Execute(t.Context(), "call-c3_tool_call", json.RawMessage(raw), nil)
		if err != nil {
			return report, err.Error()
		}
		if err := json.Unmarshal([]byte(result.Text()), &report); err != nil {
			t.Fatalf("depth %d: %q: %v", depth, result.Text(), err)
		}
		return report, ""
	}
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		node := strings.Contains(t.Name(), "/node-")
		// 9998 is the deepest the host sends: its marshal validates the arguments alone against 10000 levels, and they add two around the array.
		for _, depth := range []int{200, 1000, 4000, 5000, 9000, 9998} {
			report, failure := call(r, depth)
			if node && depth >= 5000 {
				// What Pi's own runtime gives on the pinned Node (D268).
				if !strings.Contains(failure, "Maximum call stack size exceeded") {
					t.Errorf("nesting %d: the Node SDK = %q (keys %v), want V8's RangeError", depth, failure, report.Keys)
				}
			} else if failure != "" {
				t.Errorf("nesting %d: the call failed: %s", depth, failure)
			} else {
				meta := `{"d":` + deep(depth) + `}`
				if report.Input == nil || *report.Input != `{"zeta":1,"meta":`+meta+`}` {
					t.Errorf("nesting %d: input = %.80q…, want Pi's text", depth, deepValue(report.Input))
				}
				if want := []string{"zeta", "meta"}; len(report.Keys) != 2 || report.Keys[0] != want[0] || report.Keys[1] != want[1] {
					t.Errorf("nesting %d: keys = %v, want %v", depth, report.Keys, want)
				}
				if report.Meta == nil || *report.Meta != meta {
					t.Errorf("nesting %d: meta = %.80q…, want the object", depth, deepValue(report.Meta))
				}
			}
			if _, again := call(r, 200); again != "" {
				t.Fatalf("after nesting %d the extension fails the next call: %s", depth, again)
			}
		}
	})
}

func deepValue(text *string) string {
	if text == nil {
		return "<none>"
	}
	return *text
}
