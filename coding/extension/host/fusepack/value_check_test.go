package fusepack

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/typebox/engine"
)

// The Go SDK's Value.Check and Value.Errors through the fused path (the SDK calls the host's engine in process). The isolated path is the conformance row
// TestConformance_RefinedValueRunsRefinements, which also compares every SDK with TypeBox's own Value; the engine's own order is checked against TypeBox in
// internal/typebox/engine (TestRefineMatchesTypeBox).

type refineCalls struct{ log []string }

func (c *refineCalls) refine(schema *sdk.TSchema, tag string, check func(any) (bool, error)) *sdk.TSchema {
	return sdk.Type.Refine(schema, func(value any) (bool, error) {
		c.log = append(c.log, "check:"+tag)
		return check(value)
	}, func(any) (string, error) {
		c.log = append(c.log, "error:"+tag)
		return tag + " refused", nil
	})
}

func TestGoValueCheckRunsRefinementsInTypeBoxOrder(t *testing.T) {
	sdk.SetHostHelpers(&sdk.HostHelpers{TypeEngine: engine.Serve})
	t.Cleanup(func() { sdk.SetHostHelpers(nil) })
	calls := &refineCalls{}
	positive := func(v any) (bool, error) { n, _ := v.(float64); return n > 0, nil }
	schema := sdk.Type.Object(sdk.NewOrderedObject(
		"a", calls.refine(sdk.Type.Number(), "a", positive),
		"b", calls.refine(sdk.Type.Number(), "b", positive),
		"c", sdk.Type.String(),
	))
	cases := []struct {
		name   string
		value  any
		want   bool
		checks []string
	}{
		{"all pass", map[string]any{"a": 1.0, "b": 2.0, "c": "x"}, true, []string{"check:a", "check:b"}},
		{"first refinement fails and stops the walk", map[string]any{"a": -1.0, "b": 2.0, "c": "x"}, false, []string{"check:a"}},
		{"second fails", map[string]any{"a": 1.0, "b": -2.0, "c": "x"}, false, []string{"check:a", "check:b"}},
		{"a type failure after the refinements still ran them", map[string]any{"a": 1.0, "b": 2.0, "c": 3.0}, false, []string{"check:a", "check:b"}},
		{"a refinement under a wrong type is not run", map[string]any{"a": "no", "b": 2.0, "c": "x"}, false, nil},
	}
	for _, tc := range cases {
		calls.log = nil
		got, err := sdk.Value.Check(schema, tc.value)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want || !reflect.DeepEqual(calls.log, tc.checks) {
			t.Errorf("%s: Check = %v with calls %v, want %v with %v", tc.name, got, calls.log, tc.want, tc.checks)
		}
	}
}

func TestGoValueErrorsReportRefinementMessagesInTypeBoxOrder(t *testing.T) {
	sdk.SetHostHelpers(&sdk.HostHelpers{TypeEngine: engine.Serve})
	t.Cleanup(func() { sdk.SetHostHelpers(nil) })
	calls := &refineCalls{}
	positive := func(v any) (bool, error) { n, _ := v.(float64); return n > 0, nil }
	schema := sdk.Type.Object(sdk.NewOrderedObject(
		"a", calls.refine(sdk.Type.Number(), "a", positive),
		"b", sdk.Type.Refine(sdk.Type.Number(), positive),
		"c", calls.refine(sdk.Type.String(), "c", func(any) (bool, error) { return true, nil }),
	))
	errs, err := sdk.Value.Errors(schema, map[string]any{"a": -1.0, "b": -2.0, "c": 5.0})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range errs {
		got = append(got, e.Keyword+"@"+e.InstancePath+": "+e.Message)
	}
	want := []string{"~refine@/a: a refused", "~refine@/b: Refine Error", "type@/c: must be string"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors %v, want %v", got, want)
	}
	if want := []string{"check:a", "error:a"}; !reflect.DeepEqual(calls.log, want) {
		t.Errorf("calls %v, want %v (the refinement under the wrong type must not run)", calls.log, want)
	}
	if index, _ := errs[0].Params.Get("index"); index != float64(0) {
		t.Errorf("params of a refinement error: %v", errs[0].Params)
	}
}

func TestGoValueCheckReturnsTheErrorOfARefinement(t *testing.T) {
	sdk.SetHostHelpers(&sdk.HostHelpers{TypeEngine: engine.Serve})
	t.Cleanup(func() { sdk.SetHostHelpers(nil) })
	ran := 0
	schema := sdk.Type.Object(sdk.NewOrderedObject(
		"a", sdk.Type.Refine(sdk.Type.Number(), func(any) (bool, error) { ran++; return false, errors.New("kaboom") }),
		"b", sdk.Type.Refine(sdk.Type.Number(), func(any) (bool, error) { ran += 100; return true, nil }),
	))
	_, err := sdk.Value.Check(schema, map[string]any{"a": 1.0, "b": 2.0})
	if err == nil || !strings.Contains(err.Error(), "kaboom") || ran != 1 {
		t.Errorf("Check = %v after %d calls, want the refinement's error after exactly one", err, ran)
	}
}
