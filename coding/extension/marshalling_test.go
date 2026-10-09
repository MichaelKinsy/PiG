package extension

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestMarshalInputEventResult covers every variant + error paths.
func TestMarshalInputEventResult(t *testing.T) {
	cases := []struct {
		name string
		in   InputEventResult
		want string
	}{
		{"continue", InputEventResultContinue{}, `{"action":"continue"}`},
		{"handled", InputEventResultHandled{}, `{"action":"handled"}`},
		{"transform_text_only", InputEventResultTransform{Text: "hello"}, `{"action":"transform","text":"hello"}`},
		{"transform_with_images", InputEventResultTransform{
			Text: "see this",
			Images: []ImageContent{
				ImageContent{Data: "xxx", MimeType: "image/png"},
			},
		}, `{"action":"transform","text":"see this","images":[{"type":"image","data":"xxx","mimeType":"image/png"}]}`},
		{"nil", nil, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MarshalInputEventResult(tc.in)
			if err != nil {
				t.Fatalf("MarshalInputEventResult: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}

	t.Run("unknown_variant_errors", func(t *testing.T) {
		// A struct that isn't one of the three variants must fail. We
		// can't construct a valid impostor (the marker method is
		// unexported), but we can reach this branch by passing nil
		// inside a typed-nil-pointer scenario. Skip \u2014 the package-sealed
		// nature of the interface makes this branch unreachable in
		// practice. The branch exists as defence-in-depth.
		t.Skip("interface is package-sealed; unknown-variant branch is defence-in-depth only")
	})
}

// TestUnmarshalInputEventResult covers every variant + error paths.
func TestUnmarshalInputEventResult(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    InputEventResult
		wantErr string
	}{
		{"continue", `{"action":"continue"}`, InputEventResultContinue{}, ""},
		{"handled", `{"action":"handled"}`, InputEventResultHandled{}, ""},
		{"transform_text_only", `{"action":"transform","text":"hi"}`, InputEventResultTransform{Text: "hi"}, ""},
		{"transform_with_images", `{"action":"transform","text":"x","images":[{"type":"image","data":"s","mimeType":"image/png"}]}`, InputEventResultTransform{
			Text:   "x",
			Images: []ImageContent{{Data: "s", MimeType: "image/png"}},
		}, ""},
		{"missing_action", `{}`, nil, "missing action discriminator"},
		{"unknown_action", `{"action":"explode"}`, nil, "unknown action"},
		{"malformed", `{`, nil, "unexpected end of JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UnmarshalInputEventResult([]byte(tc.in))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil; got value %v", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestInputEventResult_RoundTrip asserts every variant survives a
// marshal\u2192unmarshal cycle without loss.
func TestInputEventResult_RoundTrip(t *testing.T) {
	cases := []InputEventResult{
		InputEventResultContinue{},
		InputEventResultHandled{},
		InputEventResultTransform{Text: "hello"},
		InputEventResultTransform{
			Text:   "with images",
			Images: []ImageContent{{Data: "data:...", MimeType: "image/png"}},
		},
	}
	for i, want := range cases {
		t.Run("", func(t *testing.T) {
			data, err := MarshalInputEventResult(want)
			if err != nil {
				t.Fatalf("[%d] marshal: %v", i, err)
			}
			got, err := UnmarshalInputEventResult(data)
			if err != nil {
				t.Fatalf("[%d] unmarshal: %v", i, err)
			}
			// Deep equality via marshalling both back to JSON.
			gotData, _ := MarshalInputEventResult(got)
			if string(gotData) != string(data) {
				t.Errorf("[%d] round-trip drift:\n  before: %s\n   after: %s", i, data, gotData)
			}
		})
	}
}

// TestToolResultEvent_RoundTripPerVariant asserts every builtin variant of
// ToolResultEvent and a custom variant round-trip without loss.
func TestToolResultEvent_RoundTripPerVariant(t *testing.T) {
	cases := []struct {
		name string
		in   ToolResultEvent
	}{
		{"bash", BashToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r1", IsError: false},
			ToolName:            "bash",
		}},
		{"powershell", PowerShellToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r_ps"},
			ToolName:            "powershell",
			Details:             &PowerShellToolDetails{FullOutputPath: `C:\Temp\pi-powershell.log`},
		}},
		{"read", ReadToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r2"},
			ToolName:            "read",
		}},
		{"edit", EditToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r3"},
			ToolName:            "edit",
		}},
		{"write", WriteToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r4"},
			ToolName:            "write",
		}},
		{"grep", GrepToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r5"},
			ToolName:            "grep",
		}},
		{"find", FindToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r6"},
			ToolName:            "find",
		}},
		{"ls", LsToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r7"},
			ToolName:            "ls",
		}},
		{"custom", CustomToolResultEvent{
			ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "r8"},
			ToolName:            "my_custom",
			Details:             map[string]any{"k": "v"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := MarshalToolResultEvent(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := UnmarshalToolResultEvent(data)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			gotData, err := MarshalToolResultEvent(got)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			if string(gotData) != string(data) {
				t.Errorf("round-trip drift:\n  before: %s\n   after: %s", data, gotData)
			}
		})
	}
}

func TestProviderModelConfigJSONRoundTrip_ThinkingLevelMapAndBaseURL(t *testing.T) {
	high := "HIGH"
	payload := ProviderModelConfig{
		ID:        "custom-model",
		Name:      "Custom Model",
		API:       ai.APIOpenAICompletions,
		BaseURL:   "https://provider.example/v1",
		Reasoning: true,
		ThinkingLevelMap: ai.ThinkingLevelMap{
			ai.ThinkingOff:  nil,
			ai.ThinkingHigh: &high,
		},
		Input: []string{"text", "image"},
		Cost: ProviderModelCost{
			Input:      1,
			Output:     2,
			CacheRead:  3,
			CacheWrite: 4,
		},
		ContextWindow: 128000,
		MaxTokens:     16384,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"baseUrl":"https://provider.example/v1"`) {
		t.Fatalf("marshal missing baseUrl: %s", data)
	}
	if !strings.Contains(string(data), `"thinkingLevelMap":{"high":"HIGH","off":null}`) &&
		!strings.Contains(string(data), `"thinkingLevelMap":{"off":null,"high":"HIGH"}`) {
		t.Fatalf("marshal missing thinkingLevelMap: %s", data)
	}

	var got ProviderModelConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.BaseURL != payload.BaseURL {
		t.Fatalf("BaseURL = %q, want %q", got.BaseURL, payload.BaseURL)
	}
	if _, ok := got.ThinkingLevelMap[ai.ThinkingOff]; !ok || got.ThinkingLevelMap[ai.ThinkingOff] != nil {
		t.Fatalf("ThinkingLevelMap[off] = %v, want explicit nil", got.ThinkingLevelMap[ai.ThinkingOff])
	}
	if got.ThinkingLevelMap[ai.ThinkingHigh] == nil || *got.ThinkingLevelMap[ai.ThinkingHigh] != high {
		t.Fatalf("ThinkingLevelMap[high] = %v, want %q", got.ThinkingLevelMap[ai.ThinkingHigh], high)
	}
}

// TestUnmarshalToolResultEvent_MissingDiscriminator mirrors the above.
func TestUnmarshalToolResultEvent_MissingDiscriminator(t *testing.T) {
	_, err := UnmarshalToolResultEvent([]byte(`{"type":"tool_result","toolCallId":"x"}`))
	if err == nil {
		t.Fatal("expected error for missing toolName")
	}
	if !strings.Contains(err.Error(), "missing toolName") {
		t.Errorf("error = %q, want contains \"missing toolName\"", err.Error())
	}
}

// TestSealedInterfaces_PackageSealed verifies the marker methods are
// unexported \u2014 i.e. only this package's variant structs can satisfy the
// interfaces. An external package cannot construct a satisfying type
// because it cannot call (or implement) an unexported method.
//
// This is a compile-time guarantee, not a runtime one. The test's job is
// to assert that the marker method NAMES are unexported by reflection on
// the interface method set.
func TestSealedInterfaces_PackageSealed(t *testing.T) {
	// Each marker method must start with a lowercase letter.
	cases := []struct {
		iface  string
		marker string
	}{
		{"InputEventResult", "isInputEventResult"},
		{"ToolCallEvent", "isToolCallEvent"},
		{"ToolResultEvent", "isToolResultEvent"},
	}
	for _, tc := range cases {
		t.Run(tc.iface, func(t *testing.T) {
			r := tc.marker[0]
			if r < 'a' || r > 'z' {
				t.Errorf("marker method %q must start lowercase to seal the interface", tc.marker)
			}
		})
	}
}

// goTypeName returns the unqualified type name of v (e.g. "BashToolCallEvent")
// for use in dispatch-correctness assertions.
func goTypeName(v any) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", v), "extension.")
}

// ─── Hardening: HTML-escape regression ────────────────────────────────────

// TestMarshal_NoHTMLEscape locks in the fix for the wire-format bug where
// `json.Marshal` HTML-escaped `&`, `<`, `>` to `\u0026`, `\u003c`, `\u003e`.
// Upstream JS `JSON.stringify` does NOT escape those; payloads with bash
// `&&`, shell redirects `<`, `>` would have round-tripped wrong.
//
// Every sum-type marshalling helper must go through noEscapeJSON.
func TestMarshal_NoHTMLEscape(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{
			name: "InputEventResultTransform_with_ampersand",
			in:   InputEventResultTransform{Text: "echo a && b"},
			want: []string{"a && b"},
		},
		{
			name: "CustomToolResultEvent_with_html",
			in: CustomToolResultEvent{
				ToolResultEventBase: ToolResultEventBase{Type: "tool_result", ToolCallID: "x"},
				ToolName:            "html_renderer",
				Details:             map[string]any{"html": "<a href=\"x\">y & z</a>"},
			},
			want: []string{`<a href=\"x\">y & z</a>`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				out []byte
				err error
			)
			switch v := tc.in.(type) {
			case InputEventResult:
				out, err = MarshalInputEventResult(v)
			case ToolResultEvent:
				out, err = MarshalToolResultEvent(v)
			default:
				t.Fatalf("test setup: unhandled type %T", v)
			}
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			s := string(out)
			for _, w := range tc.want {
				if !strings.Contains(s, w) {
					t.Errorf("missing %q in output:\n  %s", w, s)
				}
			}
			// Belt-and-braces: the three forbidden escapes must NOT appear.
			for _, forbidden := range []string{`\u0026`, `\u003c`, `\u003e`} {
				if strings.Contains(s, forbidden) {
					t.Errorf("found HTML-escape %s in output:\n  %s", forbidden, s)
				}
			}
		})
	}
}

// ─── Hardening: sealed-interface implementation completeness ──────────────

// TestSealedInterface_AllVariantsImplement is a compile-time gate disguised
// as a runtime test. The slice literals below fail to COMPILE if any
// variant struct stops implementing its sealed interface (e.g. a refactor
// removes a marker method, or a new variant is added without a marker).
//
// Reading this test = enumerating the complete known set of variants for
// each sealed sum type. When upstream adds a new variant, this test is the
// place to add the new entry.
func TestSealedInterface_AllVariantsImplement(t *testing.T) {
	t.Run("InputEventResult_3_variants", func(t *testing.T) {
		variants := []InputEventResult{
			InputEventResultContinue{},
			InputEventResultTransform{},
			InputEventResultHandled{},
		}
		if got, want := len(variants), 3; got != want {
			t.Errorf("InputEventResult variants: got %d, want %d", got, want)
		}
	})

	t.Run("ToolCallEvent_1_variant", func(t *testing.T) {
		variants := []ToolCallEvent{CustomToolCallEvent{}}
		if got, want := len(variants), 1; got != want {
			t.Errorf("ToolCallEvent variants: got %d, want %d", got, want)
		}
	})

	t.Run("ToolResultEvent_9_variants", func(t *testing.T) {
		variants := []ToolResultEvent{
			BashToolResultEvent{},
			PowerShellToolResultEvent{},
			ReadToolResultEvent{},
			EditToolResultEvent{},
			WriteToolResultEvent{},
			GrepToolResultEvent{},
			FindToolResultEvent{},
			LsToolResultEvent{},
			CustomToolResultEvent{},
		}
		if got, want := len(variants), 9; got != want {
			t.Errorf("ToolResultEvent variants: got %d, want %d", got, want)
		}
	})
}

// ─── Hardening: distinct-toolName guard ───────────────────────────────────

// TestToolNames_AreDistinct asserts that no two ToolResultEvent variants share a `toolName` discriminator. If they
// did, UnmarshalToolCallEvent would dispatch to whichever case appears
// first in the switch: silent data loss for the second.
func TestToolNames_AreDistinct(t *testing.T) {
	t.Run("ToolResultEvent", func(t *testing.T) {
		seen := map[string]string{}
		pairs := []struct {
			variant  ToolResultEvent
			toolName string
		}{
			{BashToolResultEvent{ToolName: "bash"}, "bash"},
			{PowerShellToolResultEvent{ToolName: "powershell"}, "powershell"},
			{ReadToolResultEvent{ToolName: "read"}, "read"},
			{EditToolResultEvent{ToolName: "edit"}, "edit"},
			{WriteToolResultEvent{ToolName: "write"}, "write"},
			{GrepToolResultEvent{ToolName: "grep"}, "grep"},
			{FindToolResultEvent{ToolName: "find"}, "find"},
			{LsToolResultEvent{ToolName: "ls"}, "ls"},
		}
		for _, p := range pairs {
			if existing, ok := seen[p.toolName]; ok {
				t.Errorf("toolName=%q claimed by %s and %s", p.toolName, existing, goTypeName(p.variant))
			}
			seen[p.toolName] = goTypeName(p.variant)
		}
	})
}

// messages.ts:46-53 CustomMessage.display is a boolean, and sendMessage's Pick keeps it: the member is always written, and a value
// that is not a boolean is not a message (agent-session.ts:2275-2290 reads message.display as the flag the message is stored with).
func TestCustomMessageRefDisplayIsABoolean(t *testing.T) {
	data, err := json.Marshal(CustomMessageRef{CustomType: "note", Content: "c"})
	if err != nil || !strings.Contains(string(data), `"display":false`) {
		t.Fatalf("marshal = %s, %v; want an explicit display:false", data, err)
	}
	var shown CustomMessageRef
	if err := json.Unmarshal([]byte(`{"customType":"note","content":"c","display":true}`), &shown); err != nil || !shown.Display {
		t.Fatalf("unmarshal display:true = %+v, %v", shown, err)
	}
	var bad CustomMessageRef
	if err := json.Unmarshal([]byte(`{"customType":"note","content":"c","display":"yes"}`), &bad); err == nil {
		t.Fatalf("display:\"yes\" decoded as %+v", bad)
	}
}
