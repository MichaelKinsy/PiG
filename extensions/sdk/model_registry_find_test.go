package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi's ModelRegistry.find is a synchronous lookup that returns undefined only when no model matches (model-registry.ts:72).
// The SDK asks the host, so a host or transport failure must not read as "no such model": it is the error, as every other
// getter's failure is (TestContextGettersDistinguishAbsentEmptyAndFailure).
func TestModelRegistryFindDistinguishesAbsentFromHostFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  *callResultMsg
		want    string
		wantErr string
	}{
		{name: "found", answer: &callResultMsg{Result: json.RawMessage(`{"id":"a","provider":"p"}`)}, want: `{"id":"a","provider":"p"}`},
		{name: "absent", answer: &callResultMsg{Result: json.RawMessage(`null`)}, want: `null`},
		{name: "host failure", answer: &callResultMsg{Error: &errorInfo{Code: "host_failed", Message: "boom"}}, wantErr: "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := New("find-state")
			var got map[string]any
			var gotErr error
			ext.Command("probe", "probe", func(ctx Context, _ string) error {
				got, gotErr = ctx.ModelRegistry().Find("p", "a")
				return nil
			})
			host, _, done := surfaceHost(t, ext, nil)
			defer surfaceShutdown(t, host, done)
			calls, _ := runSurfaceCommand(t, host, "probe", func(*callMsg) *callResultMsg { return tc.answer })
			if len(calls) != 1 || calls[0].Method != "getModel" {
				t.Fatalf("calls = %+v, want one getModel", calls)
			}
			if tc.wantErr != "" {
				if gotErr == nil || !strings.Contains(gotErr.Error(), tc.wantErr) || got != nil {
					t.Fatalf("Find = %#v, %v; want the host failure %q and no model", got, gotErr, tc.wantErr)
				}
				return
			}
			if gotErr != nil {
				t.Fatal(gotErr)
			}
			encoded, _ := json.Marshal(got)
			if string(encoded) != tc.want {
				t.Fatalf("Find = %s, want %s", encoded, tc.want)
			}
		})
	}
}
