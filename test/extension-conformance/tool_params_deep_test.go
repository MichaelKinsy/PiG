package extensionconformance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Tool arguments nested deeply, read by every SDK through ToolParams and ToolInput. Pi has no fixed limit: its own runtime (the Node SDK) returns the object while V8 has
// stack for JSON.stringify, which ends between 4300 and 4400 levels on Node 24 (it moves with the Node version and --stack-size: Node 26's V8 stringifies a plain nested array
// at any depth), and fails the call with V8's RangeError (`Maximum call stack size exceeded`) beyond. The Node SDK's expected outcome at each depth is what the running Node does
// for the same JSON.stringify (nodeStringifyOverflows). The Go, Python, Rust and Lisp SDKs do not reproduce that stack exhaustion (D268): each returns the object, and the texts Pi
// writes for it, for every frame the host can send (its own marshal refuses a value nested past 10000 levels). No SDK ends its extension or its connection on deep input: the same
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
	// 9998 is the deepest the host sends: its marshal validates the arguments alone against 10000 levels, and they add two around the array.
	depths := []int{200, 1000, 4000, 5000, 9000, 9998}
	eachDeepPlacement(t, func(t *testing.T, r *modelTypesRig) {
		node := strings.Contains(t.Name(), "/node-")
		var overflows []bool
		if node {
			// The fixture's tool stringifies `meta`: the Node SDK fails the call exactly where the running Node's JSON.stringify does (D268).
			metas := make([]string, len(depths))
			for index, depth := range depths {
				metas[index] = `{"d":` + deep(depth) + `}`
			}
			overflows = nodeStringifyOverflows(t, metas)
		}
		for index, depth := range depths {
			report, failure := call(r, depth)
			switch {
			case node && overflows[index]:
				// What Pi's own runtime gives on the running Node (D268).
				if !strings.Contains(failure, "Maximum call stack size exceeded") {
					t.Errorf("nesting %d: the Node SDK = %q (keys %v), want V8's RangeError", depth, failure, report.Keys)
				}
			case failure != "":
				t.Errorf("nesting %d: the call failed: %s", depth, failure)
			default:
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

// A tool call whose arguments hold a lone surrogate escape and nest almost as deep as the host sends: the Rust SDK reads the surrogate as U+FFFD, a retry that must keep the
// deep decode (the arguments nest past serde_json's 128 levels), or the call fails. Isolated and packed.
func TestToolParamsLoneSurrogateAndDeepNestingRust(t *testing.T) {
	const depth = 9990
	eachModelTypesPlacementOf(t, []string{"rust"}, func(t *testing.T, r *modelTypesRig) {
		for _, shape := range []string{"arrays", "objects"} {
			nested, err := json.Marshal(deepNesting(shape, depth))
			if err != nil {
				t.Fatal(err)
			}
			raw := `{"zeta":1,"s":"a\ud83db","meta":{"d":` + string(nested) + `}}`
			result, err := r.ext.Tools["c3_tool_call"].Definition.Execute(t.Context(), "call-c3_tool_call", json.RawMessage(raw), nil)
			if err != nil {
				t.Fatalf("%s: the call failed: %v", shape, err)
			}
			var report struct {
				Keys []string `json:"keys"`
				Meta *string  `json:"meta"`
			}
			if err := json.Unmarshal([]byte(result.Text()), &report); err != nil {
				t.Fatalf("%s: %.80q: %v", shape, result.Text(), err)
			}
			if want := `{"d":` + string(nested) + `}`; report.Meta == nil || *report.Meta != want {
				t.Errorf("%s: meta = %.80q…, want the object", shape, deepValue(report.Meta))
			}
			if want := []string{"zeta", "s", "meta"}; !reflect.DeepEqual(report.Keys, want) {
				t.Errorf("%s: keys = %v, want %v", shape, report.Keys, want)
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

// eachDeepPlacement is eachModelTypesPlacement plus the Lisp SDK in a shared runtime cell beside a peer: the other SDKs' packed placements are in modelTypesSDKs.
func eachDeepPlacement(t *testing.T, body func(t *testing.T, r *modelTypesRig)) {
	t.Helper()
	eachModelTypesPlacement(t, body)
	if testing.Short() {
		return
	}
	t.Run("lisp-packed", func(t *testing.T) {
		requireSBCL(t)
		r := newModelTypesRig(t)
		peers, err := packedUIConfigs["lisp"]()
		if err != nil {
			t.Fatal(err)
		}
		cfg := lispSurfaceFixtureConfig(t)
		cfg.Isolation, cfg.ContentHash = "shared-ok", "packed-modeltypes-lisp"
		configs := []subprocess.ExtConfig{cfg, peers[1]}
		r.host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
		loaded, err := r.host.Reload(t.Context())
		if err != nil {
			t.Fatalf("load: %v (%+v)", err, r.host.LastReloadReport())
		}
		applyQueuedRegistrations(r.host)
		if report := r.host.LastReloadReport(); report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-lisp") || len(report.Cells[0].Extensions) != 2 || len(loaded) != 2 {
			t.Fatalf("the two extensions do not share one packed lisp cell: %+v", report)
		}
		for _, ext := range loaded {
			if ext.Name == lispFixtureName {
				r.finish(ext)
				body(t, r)
				return
			}
		}
		t.Fatalf("%s not loaded: %v", lispFixtureName, loaded)
	})
}
