package extensionconformance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tool arguments nested deeply, read by every SDK through ToolParams and ToolInput. Pi's own runtime returns the object up to a nesting of 4000 and fails the
// call with V8's RangeError (`Maximum call stack size exceeded`) at 5000. No SDK may end its extension on deep input (a frame an SDK could not decode used to end the
// connection, and the extension with it): a call that fails fails as a tool error, and the same extension answers the next call. Up to 4000 levels every SDK returns the
// object and the texts Pi writes for it. Beyond that the SDKs differ in how far they read: Pi and the Node SDK stop below 5000, the Rust and Lisp SDKs at their bound of 4096
// levels, where the Go and Python SDKs have none and return the object (as every runtime but V8's does).
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
		for _, depth := range []int{200, 1000, 4000} {
			report, failure := call(r, depth)
			if failure != "" {
				t.Errorf("nesting %d: the call failed: %s", depth, failure)
				continue
			}
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
		// Past Pi's limit: the call fails with the RangeError, or (Go, Python) returns the object; either way the extension lives on.
		for _, depth := range []int{5000, 9000} {
			report, failure := call(r, depth)
			boundedSDK := strings.Contains(t.Name(), "/node-") || strings.Contains(t.Name(), "/rust-") || strings.Contains(t.Name(), "/lisp-")
			switch {
			case boundedSDK && failure == "":
				t.Errorf("nesting %d: the call returned %d keys, want the RangeError", depth, len(report.Keys))
			case boundedSDK && !strings.Contains(failure, "Maximum call stack size exceeded"):
				t.Errorf("nesting %d: the call failed with %q, want the RangeError", depth, failure)
			case !boundedSDK && failure != "" && !strings.Contains(failure, "Maximum call stack size exceeded"):
				t.Errorf("nesting %d: the call failed with %q, want the object or the RangeError", depth, failure)
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
