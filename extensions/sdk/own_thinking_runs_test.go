package sdk

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A load-time OwnThinkingRuns(true) reaches the register payload as owns_thinking_runs; the last call wins, false or no call leaves the key out.
func TestOwnThinkingRunsLoadTimeValueIsInTheRegisterPayload(t *testing.T) {
	for _, step := range []struct {
		name  string
		calls []bool
		want  bool
	}{
		{"true", []bool{true}, true},
		{"none", nil, false},
		{"false", []bool{false}, false},
		{"true then false", []bool{true, false}, false},
		{"false then true", []bool{false, true}, true},
	} {
		t.Run(step.name, func(t *testing.T) {
			ext := New("owner")
			for _, enabled := range step.calls {
				ext.OwnThinkingRuns(enabled)
			}
			host, reg, done := surfaceHost(t, ext, nil)
			defer surfaceShutdown(t, host, done)
			if reg.OwnsThinkingRuns != step.want {
				t.Fatalf("OwnsThinkingRuns = %t, want %t", reg.OwnsThinkingRuns, step.want)
			}
			raw, err := json.Marshal(reg)
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeArgs(t, raw); (got["owns_thinking_runs"] == true) != step.want || (!step.want && got["owns_thinking_runs"] != nil) {
				t.Fatalf("register payload = %s", raw)
			}
		})
	}
}

// ctx.OwnThinkingRuns and a post-register Extension.OwnThinkingRuns send the ownThinkingRuns call with enabled always present.
func TestOwnThinkingRunsRuntimeCallsSendEnabled(t *testing.T) {
	ext := New("owner")
	ext.Command("off", "", func(ctx Context, _ string) error { return ctx.OwnThinkingRuns(false) })
	ext.Command("on", "", func(ctx Context, _ string) error { return ctx.OwnThinkingRuns(true) })
	ext.Command("ext-off", "", func(Context, string) error { ext.OwnThinkingRuns(false); return nil })
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	for _, step := range []struct {
		command string
		want    bool
	}{{"off", false}, {"on", true}, {"ext-off", false}} {
		calls, resp := runSurfaceCommand(t, host, step.command, func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} })
		if resp.Error != nil || len(calls) != 1 || calls[0].Method != "ownThinkingRuns" {
			t.Fatalf("%s: calls = %+v, response = %+v", step.command, calls, resp)
		}
		args := decodeArgs(t, calls[0].Args)
		enabled, present := args["enabled"]
		if !present || enabled != step.want || len(args) != 1 {
			t.Fatalf("%s: args = %s", step.command, calls[0].Args)
		}
	}
}
