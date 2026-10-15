package ai

import (
	"strings"
	"testing"
)

// Pi tests these sites with a non-unicode /.../i regex or toLowerCase. Expectations were probed in Node 24:
//
//	/data retention mode/i.test("data retent\u0130on mode") === false
//	/usage_limit_reached|usage_not_included|rate_limit_exceeded/i.test("usage_l\u0130mit_reached") === false
//	"PRO\u03a3".toLowerCase() === "proς"; "M\u0130NIMAL".toLowerCase() === "mi\u0307nimal"
//
// Go's strings.ToLower maps U+0130 to "i" and a final capital sigma to "σ", so each case below fails with it.

// openai-codex-responses.ts:1608-1609 usage-limit code test and plan_type.toLowerCase().
func TestCodexUsageLimitCodeAndPlanLowerCaseLikeJS(t *testing.T) {
	if err := codexHTTPError(400, "Bad Request", []byte(`{"error":{"code":"usage_l\u0130mit_reached","message":"plain"}}`)); err.Error() != "plain" {
		t.Errorf("U+0130 code = %q, want the provider message", err)
	}
	if err := codexHTTPError(400, "Bad Request", []byte(`{"error":{"code":"USAGE_LIMIT_REACHED"}}`)); !strings.HasPrefix(err.Error(), "You have hit your ChatGPT usage limit") {
		t.Errorf("upper-case code = %q, want the usage-limit message", err)
	}
	if err := codexHTTPError(429, "Too Many Requests", []byte(`{"error":{"code":"x","plan_type":"PRO\u03a3"}}`)); err.Error() != "You have hit your ChatGPT usage limit (proς plan)." {
		t.Errorf("plan = %q", err)
	}
}

// google-shared.ts:53 lower-cases a mapped thinking level with toLowerCase before the switch.
func TestGoogleThinkingLevelMappingLowerCasesLikeJS(t *testing.T) {
	model := &Model{ID: "g", ProviderMeta: ProviderMetadata{ProviderID: "google"}}
	model.ThinkingLevelMap = ThinkingLevelMap{ThinkingLow: new("MINIMAL")}
	if got, err := resolveGoogleThinkingLevel(model, ThinkingLow); err != nil || got != ResolvedGoogleThinkingLevelMinimal {
		t.Errorf("MINIMAL = %q, %v", got, err)
	}
	model.ThinkingLevelMap = ThinkingLevelMap{ThinkingLow: new("M\u0130NIMAL")}
	if got, err := resolveGoogleThinkingLevel(model, ThinkingLow); err == nil {
		t.Errorf("U+0130 mapping resolved to %q, want the unsupported-mapping error", got)
	}
}

// models.ts:375-386 mergeHeaders replaces a header whose name.toLowerCase() equals the override's. Node 24:
// mergeHeaders({"X-\u017f":"a","X-\u212a":"c","X-A":"d"}, {"x-s":"b","x-k":"e","x-a":"f"}) keeps "X-\u017f" (U+017F lower-cases to itself) and replaces the Kelvin-sign and ASCII spellings.
func TestMergeProviderHeadersMatchesNamesLikeJSToLowerCase(t *testing.T) {
	merged := MergeProviderHeaders(ProviderHeaders{"X-\u017f": new("a"), "X-\u212a": new("c"), "X-A": new("d")}, ProviderHeaders{"x-s": new("b"), "x-k": new("e"), "x-a": new("f")})
	got := map[string]string{}
	for name, value := range merged {
		got[name] = *value
	}
	want := map[string]string{"X-\u017f": "a", "x-s": "b", "x-k": "e", "x-a": "f"}
	if len(got) != len(want) {
		t.Fatalf("merged = %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("merged = %v, want %v", got, want)
		}
	}
}
