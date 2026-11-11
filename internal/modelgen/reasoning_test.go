package modelgen

// pi: packages/ai/catalog/reasoning.ts

import (
	"encoding/json"
	"testing"
)

func effortMap(t *testing.T, input string) *Obj {
	t.Helper()
	var options []ModelsDevReasoningOption
	if err := json.Unmarshal([]byte(input), &options); err != nil {
		t.Fatal(err)
	}
	return GetEffortThinkingLevelMap(options)
}

// The level maps are serialized into the generated catalog, so requireJSON compares the member order too.
func TestGetEffortThinkingLevelMap(t *testing.T) {
	cases := []struct{ name, input, want string }{
		// upstream: packages/ai/test/reasoning-options.test.ts:5
		{"exposes only verified effort values and none", `[{"type":"toggle"},{"type":"effort","values":["none","low","high","max"]}]`, `{"off":"none","minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`},
		// upstream: packages/ai/test/reasoning-options.test.ts:19
		{"does not infer thinking-off from an effort list", `[{"type":"effort","values":["low","high","max"]}]`, `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`},
		// upstream: packages/ai/test/reasoning-options.test.ts:31 (all three assertions)
		{"leaves toggle and budget controls for their adapter-specific implementations/toggle", `[{"type":"toggle"}]`, `null`},
		{"leaves toggle and budget controls for their adapter-specific implementations/budget", `[{"type":"budget_tokens","min":1024,"max":32000}]`, `null`},
		{"leaves toggle and budget controls for their adapter-specific implementations/defaults", `[{"type":"effort","values":[null,"default"]}]`, `null`},
		// Source cases beyond the upstream test.
		{"no options", `[]`, `null`},
		{"an effort option without values", `[{"type":"effort"}]`, `null`},
		{"none alone is a known value", `[{"type":"effort","values":["none"]}]`, `{"off":"none","minimal":null,"low":null,"medium":null,"high":null,"xhigh":null,"max":null}`},
		{"values of several effort options are united", `[{"type":"effort","values":["minimal"]},{"type":"toggle"},{"type":"effort","values":["medium","xhigh","default",null]}]`, `{"off":null,"minimal":"minimal","low":null,"medium":"medium","high":null,"xhigh":"xhigh","max":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireJSON(t, effortMap(t, tc.input), tc.want)
		})
	}
}

// The level map is a JavaScript object: Stringify prints keys in the order the spec fixes (off first, then low to max), indented
// like JSON.stringify(value, null, 2).
func TestGetEffortThinkingLevelMapKeyOrder(t *testing.T) {
	levels := effortMap(t, `[{"type":"effort","values":["max","high","none"]}]`)
	if got := levels.Keys(); len(got) != 7 || got[0] != "off" || got[1] != "minimal" || got[2] != "low" || got[3] != "medium" || got[4] != "high" || got[5] != "xhigh" || got[6] != "max" {
		t.Fatalf("keys = %v", got)
	}
	want := "{\n  \"off\": \"none\",\n  \"minimal\": null,\n  \"low\": null,\n  \"medium\": null,\n  \"high\": \"high\",\n  \"xhigh\": null,\n  \"max\": \"max\"\n}"
	if got := string(Stringify(levels, "  ")); got != want {
		t.Fatalf("Stringify =\n%s\nwant\n%s", got, want)
	}
	if got := string(Stringify(levels, "")); got != `{"off":"none","minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":"max"}` {
		t.Fatalf("compact Stringify = %s", got)
	}
}
