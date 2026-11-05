package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// refineValueCase is one value run through `Value.Check` and `Value.Errors` of the "refine-probe" schema in every SDK. The calls are what the refinements logged, in order
// ("check:len", "error:lenmsg"): TypeBox's own `Value` (the Node runtime serves the real one) is the reference, and wantCalls pins what it does so that a reference that stopped running
// refinements would fail here too.
type refineValueCase struct {
	name  string
	value string
	// wantCheck is Value.Check's answer; thrown names a refinement error the call must fail with.
	wantCheck  bool
	thrown     string
	checkCalls []string
	// errorCalls are the calls of Value.Errors, which runs every keyword of a node and every refinement of a node whose keywords passed.
	errorCalls []string
	// wantMessages are the messages of the `~refine` errors Value.Errors reports, in order.
	wantMessages []string
}

var refineValueCases = []refineValueCase{
	{
		name: "every refinement passes, the union's branches both run and the root runs last", value: `{"name":"abc","pick":"a"}`, wantCheck: true,
		checkCalls: []string{"check:len", "check:u1", "check:u2", "check:root"},
		errorCalls: []string{"check:len", "check:u1", "check:u2", "error:u2m", "check:root"},
	},
	{
		name: "a short name stops Check at the first refinement", value: `{"name":"ab","pick":"c","qty":3}`, wantCheck: false,
		checkCalls: []string{"check:len"},
		errorCalls: []string{"check:len", "error:lenmsg", "check:even", "error:evenmsg", "check:u1", "error:u1m", "check:u2", "error:u2m"},
		// pick fails both branches of the union: the branches' own errors are reported.
		wantMessages: []string{"name is too short", "qty must be even, got 3", "not a", "not b"},
	},
	{
		name: "an odd qty after a good name", value: `{"name":"abc","qty":3,"pick":"b"}`, wantCheck: false,
		checkCalls:   []string{"check:len", "check:even"},
		errorCalls:   []string{"check:len", "check:even", "error:evenmsg", "check:u1", "error:u1m", "check:u2"},
		wantMessages: []string{"qty must be even, got 3"},
	},
	{
		name: "an even qty and the second branch", value: `{"name":"abcd","qty":2,"pick":"b"}`, wantCheck: true,
		checkCalls: []string{"check:len", "check:even", "check:u1", "check:u2", "check:root"},
		errorCalls: []string{"check:len", "check:even", "check:u1", "error:u1m", "check:u2", "check:root"},
	},
	{
		name: "a pick outside both branches", value: `{"name":"abc","pick":"c"}`, wantCheck: false,
		checkCalls:   []string{"check:len", "check:u1", "check:u2"},
		errorCalls:   []string{"check:len", "check:u1", "error:u1m", "check:u2", "error:u2m"},
		wantMessages: []string{"not a", "not b"},
	},
	{
		name: "a refinement under the wrong type does not run", value: `{"name":5,"pick":"a"}`, wantCheck: false,
		checkCalls: nil,
		errorCalls: []string{"check:u1", "check:u2", "error:u2m"},
	},
	{
		name: "a refinement that fails with an error ends the call", value: `{"name":"abc","pick":"a","boom":"x"}`, thrown: "kaboom",
		checkCalls: []string{"check:len", "check:u1", "check:u2", "check:boom"},
		errorCalls: []string{"check:len", "check:u1", "check:u2", "error:u2m", "check:boom"},
	},
}

// sameStrings compares two lists, an empty one and a nil one being the same.
func sameStrings(a, b []string) bool { return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b) }

type refineValueReport struct {
	Result json.RawMessage `json:"result"`
	Thrown *string         `json:"thrown"`
	Calls  []string        `json:"calls"`
}

// runRefineValue runs the command refine_value of a harness's extension and returns its report.
func runRefineValue(t *testing.T, h *harness, mode, value string) refineValueReport {
	t.Helper()
	before := len(*h.notify)
	args, err := json.Marshal(map[string]any{"mode": mode, "value": json.RawMessage(value)})
	if err != nil {
		t.Fatal(err)
	}
	if !h.runner.ExecuteCommand(context.Background(), "refine_value", string(args)) {
		t.Fatal("no refine_value command")
	}
	var reported string
	pollUntilConformance(t, "refine_value: no report", func() bool {
		for _, n := range (*h.notify)[before:] {
			if rest, ok := strings.CutPrefix(n, "refine_value:"); ok {
				reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
				return true
			}
		}
		return false
	})
	var report refineValueReport
	if err := json.Unmarshal([]byte(reported), &report); err != nil {
		t.Fatalf("report %q: %v", reported, err)
	}
	return report
}

func compactJSONText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	// Compact keeps the order of every object's members (params), which re-encoding a decoded map would sort.
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return out.String()
}

// TestConformance_RefinedValueRunsRefinements: every SDK runs `Value.Check` and `Value.Errors` on the schema of the tool "refine-probe" (built with Type and Type.Refine) and must answer
// what TypeBox's own `Value` answers in the Node runtime: the same result, the same errors with the refinements' messages, and the same refinement calls in the same order. The cases
// have both outcomes and different call sequences, so an SDK that returns a constant, skips the refinements or runs them in another order fails.
func TestConformance_RefinedValueRunsRefinements(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixture)")
	}
	type answer struct{ check, errors refineValueReport }
	reference := make([]answer, len(refineValueCases))
	cases := sdkHarnessCases()
	var node harnessCase
	for _, tc := range cases {
		if tc.name == "subprocess-node" {
			node = tc
		}
	}
	{ // the reference runs for every selection of the subtests below: they compare with its answers
		h := node.make(t)
		t.Cleanup(func() {
			if h.cleanup != nil {
				h.cleanup()
			}
			if h.host != nil {
				h.host.Shutdown("test done")
			}
		})
		for i, vc := range refineValueCases {
			ref := answer{check: runRefineValue(t, h, "check", vc.value), errors: runRefineValue(t, h, "errors", vc.value)}
			reference[i] = ref
			if vc.thrown != "" {
				if ref.check.Thrown == nil || !strings.Contains(*ref.check.Thrown, vc.thrown) {
					t.Errorf("%s: TypeBox's Value.Check = %s thrown %v, want the error %q", vc.name, ref.check.Result, ref.check.Thrown, vc.thrown)
				}
			} else if want := vc.wantCheck; string(ref.check.Result) != map[bool]string{true: "true", false: "false"}[want] {
				t.Errorf("%s: TypeBox's Value.Check = %s (thrown %v), want %v", vc.name, ref.check.Result, ref.check.Thrown, want)
			}
			if !sameStrings(ref.check.Calls, vc.checkCalls) {
				t.Errorf("%s: TypeBox's Value.Check calls %v, want %v", vc.name, ref.check.Calls, vc.checkCalls)
			}
			if !sameStrings(ref.errors.Calls, vc.errorCalls) {
				t.Errorf("%s: TypeBox's Value.Errors calls %v, want %v", vc.name, ref.errors.Calls, vc.errorCalls)
			}
			var messages []string
			var rows []struct{ Keyword, Message string }
			if ref.errors.Thrown == nil {
				if err := json.Unmarshal(ref.errors.Result, &rows); err != nil {
					t.Fatalf("%s: %v", vc.name, err)
				}
			}
			for _, row := range rows {
				if row.Keyword == "~refine" {
					messages = append(messages, row.Message)
				}
			}
			if !sameStrings(messages, vc.wantMessages) {
				t.Errorf("%s: TypeBox's refinement messages %v, want %v", vc.name, messages, vc.wantMessages)
			}
		}
	}
	for _, tc := range cases {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for i, vc := range refineValueCases {
				ref := reference[i]
				for _, mode := range []string{"check", "errors"} {
					got := runRefineValue(t, h, mode, vc.value)
					want := ref.check
					if mode == "errors" {
						want = ref.errors
					}
					if (got.Thrown == nil) != (want.Thrown == nil) || got.Thrown != nil && !strings.Contains(*got.Thrown, vc.thrown) {
						t.Errorf("%s (%s): failed with %v, TypeBox failed with %v", vc.name, mode, got.Thrown, want.Thrown)
						continue
					}
					if got.Thrown == nil && compactJSONText(t, got.Result) != compactJSONText(t, want.Result) {
						t.Errorf("%s (%s): result\n  sdk %s\n  tb  %s", vc.name, mode, got.Result, want.Result)
					}
					if !sameStrings(got.Calls, want.Calls) {
						t.Errorf("%s (%s): refinement calls %v, TypeBox made %v", vc.name, mode, got.Calls, want.Calls)
					}
				}
			}
		})
	}
}
