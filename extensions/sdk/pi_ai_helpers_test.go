package sdk

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// goldenCase is one case of the pi-ai vectors (test/parity/sdk-vectors/pi-ai/<export>.json): what Pi's own pi-ai at the pin returned (JSON.stringify of it as Text; absent when it returned undefined).
type goldenCase struct {
	Fn   string            `json:"fn"`
	Args []json.RawMessage `json:"args"`
	Text *string           `json:"text"`
}

type goldenAuth struct {
	Cases []struct {
		Name       string            `json:"name"`
		EnvVars    []string          `json:"envVars"`
		Credential json.RawMessage   `json:"credential"`
		Env        map[string]string `json:"env"`
		Seen       []string          `json:"seen"`
		Text       *string           `json:"text"`
	} `json:"cases"`
	Login struct {
		Prompts []map[string]any `json:"prompts"`
		Result  map[string]any   `json:"result"`
	} `json:"login"`
}

func decodeArg(t *testing.T, c goldenCase, i int) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(c.Args[i], &value); err != nil {
		t.Fatalf("%s arg %d: %v", c.Fn, i, err)
	}
	return value
}

func rawList(t *testing.T, raw json.RawMessage) []json.RawMessage {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func canonical(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	value, err := parseOrderedJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return jsonStringify(value)
}

func rawsText(values []json.RawMessage) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = string(value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func costText(cost map[string]any) string {
	number := func(key string) string {
		if value, _ := cost[key].(float64); !math.IsNaN(value) && !math.IsInf(value, 0) {
			return jsNumberString(value)
		}
		return "null"
	}
	return `{"input":` + number("input") + `,"output":` + number("output") + `,"cacheRead":` + number("cacheRead") + `,"cacheWrite":` + number("cacheWrite") + `,"total":` + number("total") + `}`
}

// Every pure pi-ai helper returns what Pi's own code returned at the pin for each recorded call.
func TestPiAiHelpersMatchThePinnedPackage(t *testing.T) {
	cases, _ := loadGolden(t)
	counted := map[string]int{}
	for _, c := range cases {
		counted[c.Fn]++
		want := "undefined"
		if c.Text != nil {
			want = *c.Text
		}
		got := runGoldenCase(t, c)
		if got != want {
			t.Errorf("%s(%s) = %s, want %s", c.Fn, rawsText(c.Args), got, want)
		}
	}
	for _, fn := range []string{"StringEnum", "getSupportedThinkingLevels", "clampThinkingLevel", "calculateCost", "contentText", "getCurrentTools", "getCurrentSystemMessage", "getCurrentSystemPrompt",
		"normalizeContext", "isRetryableAssistantError", "isContextOverflow", "isRecoverableLength", "retryDelayMs", "getSystemMessageText", "renderSystemMessageUpdate", "createInitialSystemMessage"} {
		if counted[fn] == 0 {
			t.Errorf("the golden file has no %s call", fn)
		}
	}
}

func runGoldenCase(t *testing.T, c goldenCase) string {
	t.Helper()
	must := func(text string, err error) string {
		if err != nil {
			t.Fatalf("%s: %v", c.Fn, err)
		}
		return text
	}
	optional := func(raw json.RawMessage, err error) string {
		if err != nil {
			t.Fatalf("%s: %v", c.Fn, err)
		}
		if raw == nil {
			return "undefined"
		}
		return canonical(t, raw)
	}
	messagesArg := func() []json.RawMessage { return rawList(t, c.Args[0]) }
	contextArg := func() json.RawMessage { return c.Args[0] }
	switch c.Fn {
	case "StringEnum":
		var options *StringEnumOptions
		if len(c.Args) > 1 {
			var given struct{ Description, Default string }
			_ = json.Unmarshal(c.Args[1], &given)
			options = &StringEnumOptions{Description: given.Description, Default: given.Default}
		}
		var values []string
		_ = json.Unmarshal(c.Args[0], &values)
		// The SDK writes U+2028 and U+2029 as escapes, which JSON.stringify leaves raw: the same JSON, so compare the canonical text.
		return canonical(t, json.RawMessage(jsonStringifyAny(t, StringEnum(values, options))))
	case "getSupportedThinkingLevels":
		return jsonStringifyAny(t, GetSupportedThinkingLevels(decodeArg(t, c, 0)))
	case "clampThinkingLevel":
		var level string
		_ = json.Unmarshal(c.Args[1], &level)
		return jsonStringify(ClampThinkingLevel(decodeArg(t, c, 0), level))
	case "calculateCost":
		cost, err := CalculateCost(decodeArg(t, c, 0), decodeArg(t, c, 1))
		if err != nil {
			t.Fatal(err)
		}
		return costText(cost)
	case "contentText":
		var content any
		_ = json.Unmarshal(c.Args[0], &content)
		separator := []string{}
		if len(c.Args) > 1 {
			var given string
			_ = json.Unmarshal(c.Args[1], &given)
			separator = append(separator, given)
		}
		text, err := ContentText(content, separator...)
		return jsonStringify(must(text, err))
	case "getCurrentTools":
		tools, err := GetCurrentTools(messagesArg())
		if err != nil {
			t.Fatal(err)
		}
		return rawsText(tools)
	case "getCurrentSystemMessage":
		return optional(GetCurrentSystemMessage(messagesArg()))
	case "getCurrentSystemPrompt":
		text, err := GetCurrentSystemPrompt(messagesArg())
		return jsonStringify(must(text, err))
	case "getInitialSystemMessage":
		return optional(GetInitialSystemMessage(messagesArg()))
	case "getDeclaredTools":
		tools, err := GetDeclaredTools(messagesArg())
		if err != nil {
			t.Fatal(err)
		}
		return rawsText(tools)
	case "hasNonAdditiveToolChanges":
		got, err := HasNonAdditiveToolChanges(messagesArg())
		if err != nil {
			t.Fatal(err)
		}
		return map[bool]string{true: "true", false: "false"}[got]
	case "resolveTranscriptTools":
		var support string
		_ = json.Unmarshal(c.Args[1], &support)
		resolved, err := ResolveTranscriptTools(messagesArg(), TranscriptToolSupport(support))
		if err != nil {
			t.Fatal(err)
		}
		return `{"requestTools":` + rawsText(resolved.RequestTools) + `,"foldedSystemMessages":` + strconv.Itoa(resolved.FoldedSystemMessages) + `}`
	case "collapseSystemMessages":
		return optional(CollapseSystemMessages(contextArg()))
	case "resolveTranscript":
		var supports bool
		_ = json.Unmarshal(c.Args[1], &supports)
		return optional(ResolveTranscript(contextArg(), supports))
	case "withoutInitialSystemMessage":
		rest, err := WithoutInitialSystemMessage(messagesArg())
		if err != nil {
			t.Fatal(err)
		}
		return rawsText(rest)
	case "normalizeContext":
		return optional(NormalizeContext(contextArg()))
	case "createInitialSystemMessage":
		var prompt *string
		_ = json.Unmarshal(c.Args[0], &prompt)
		var tools []json.RawMessage
		if len(c.Args) > 1 {
			tools = rawList(t, c.Args[1])
		}
		return optional(CreateInitialSystemMessage(prompt, tools))
	case "getSystemMessageText":
		text, err := GetSystemMessageText(c.Args[0])
		return jsonStringify(must(text, err))
	case "renderSystemMessageUpdate":
		text, err := RenderSystemMessageUpdate(c.Args[0])
		return jsonStringify(must(text, err))
	case "getModelType":
		return jsonStringify(GetModelType(decodeArg(t, c, 0)))
	case "isModelType":
		var kind string
		_ = json.Unmarshal(c.Args[1], &kind)
		return map[bool]string{true: "true", false: "false"}[IsModelType(decodeArg(t, c, 0), kind)]
	case "hasApi":
		var api string
		_ = json.Unmarshal(c.Args[1], &api)
		return map[bool]string{true: "true", false: "false"}[HasApi(decodeArg(t, c, 0), api)]
	case "modelsAreEqual":
		return map[bool]string{true: "true", false: "false"}[ModelsAreEqual(decodeArg(t, c, 0), decodeArg(t, c, 1))]
	case "isRetryableAssistantError":
		return map[bool]string{true: "true", false: "false"}[IsRetryableAssistantError(decodeArg(t, c, 0))]
	case "isContextOverflow":
		window := 0.0
		if len(c.Args) > 1 {
			_ = json.Unmarshal(c.Args[1], &window)
		}
		return map[bool]string{true: "true", false: "false"}[IsContextOverflow(decodeArg(t, c, 0), window)]
	case "isRecoverableLength":
		var desired float64
		_ = json.Unmarshal(c.Args[1], &desired)
		return map[bool]string{true: "true", false: "false"}[IsRecoverableLength(decodeArg(t, c, 0), desired)]
	case "retryDelayMs":
		var policy struct {
			BaseDelayMs     float64  `json:"baseDelayMs"`
			MaxAgentDelayMs *float64 `json:"maxAgentDelayMs"`
		}
		_ = json.Unmarshal(c.Args[0], &policy)
		var attempt int
		_ = json.Unmarshal(c.Args[1], &attempt)
		return jsNumberString(RetryDelayMs(RetryPolicy{BaseDelayMs: policy.BaseDelayMs, MaxAgentDelayMs: policy.MaxAgentDelayMs}, attempt))
	}
	if c.Fn == "DEFAULT_MAX_AGENT_RETRY_DELAY_MS" {
		return jsNumberString(DefaultMaxAgentRetryDelayMs)
	}
	t.Fatalf("no Go counterpart for %s", c.Fn)
	return ""
}

// envApiKeyAuth resolves and logs in as Pi's does.
func TestEnvAPIKeyAuthMatchesThePinnedPackage(t *testing.T) {
	_, golden := loadGolden(t)
	for _, c := range golden.Cases {
		auth := EnvAPIKeyAuth(c.Name, c.EnvVars)
		var credential *Credential
		if len(c.Credential) > 0 && string(c.Credential) != "null" {
			credential = decodeCredential(t, c.Credential)
		}
		var seen []string
		result, err := auth.Resolve(APIKeyAuthInput{Credential: credential, Signal: context.Background(), Ctx: AuthContext{Env: func(name string) (*string, error) {
			seen = append(seen, name)
			value, ok := c.Env[name]
			if !ok {
				return nil, nil
			}
			return &value, nil
		}}})
		if err != nil {
			t.Fatal(err)
		}
		want := "undefined"
		if c.Text != nil {
			want = *c.Text
		}
		got := "undefined"
		if result != nil {
			object := newJSObject("auth", newJSObject("apiKey", result.Auth["apiKey"]))
			if result.Env != nil {
				env := newJSObject()
				for key, value := range result.Env {
					env.set(key, value)
				}
				object.set("env", env)
			}
			if result.Source != nil {
				object.set("source", *result.Source)
			}
			got = jsonStringify(object)
		}
		// A stored credential with an empty env serializes `"env":{}` in Pi; the SDK's AuthResult omits an empty env.
		want = strings.Replace(want, `"env":{},`, "", 1)
		if got != want {
			t.Errorf("%v credential %s env %v = %s, want %s", c.EnvVars, c.Credential, c.Env, got, want)
		}
		if !reflect.DeepEqual(append([]string{}, seen...), append([]string{}, c.Seen...)) {
			t.Errorf("%v credential %s env %v asked %v, want %v", c.EnvVars, c.Credential, c.Env, seen, c.Seen)
		}
	}
	auth := EnvAPIKeyAuth("Name", []string{"FIRST", "SECOND"})
	var prompts []map[string]any
	credential, err := auth.Login(AuthInteraction{Signal: context.Background(), Prompt: func(request map[string]any) (string, error) {
		prompts = append(prompts, request)
		return "typed-key", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prompts, golden.Login.Prompts) || credential.String("type") != golden.Login.Result["type"] || credential.String("key") != golden.Login.Result["key"] {
		t.Errorf("login prompted %v and returned %v %v, want %v and %v", prompts, credential.Keys(), credential.String("key"), golden.Login.Prompts, golden.Login.Result)
	}
	if auth.Check != nil {
		t.Error("envApiKeyAuth has no check")
	}
}

func decodeCredential(t *testing.T, raw json.RawMessage) *Credential {
	t.Helper()
	var credential Credential
	if err := json.Unmarshal(raw, &credential); err != nil {
		t.Fatal(err)
	}
	return &credential
}

// jsonStringifyAny is JSON.stringify of a value the SDK marshals itself (an ordered object or a list of strings), without Go's escaping of U+2028 and U+2029.
func jsonStringifyAny(t *testing.T, value any) string {
	t.Helper()
	if levels, ok := value.([]string); ok {
		parts := make([]string, len(levels))
		for i, level := range levels {
			parts[i] = jsonStringify(level)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	encoded, err := value.(json.Marshaler).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// goldenTiming is one case of the createAssistantMessageEventStream vectors: how the final message is delivered to a stream made by createAssistantMessageEventStream, what it carries,
// and what Pi's stream left on it.
type goldenTiming struct {
	Kind       string   `json:"kind"`
	Timestamp  string   `json:"timestamp"`
	Preset     *float64 `json:"preset"`
	Present    bool     `json:"present"`
	Value      *float64 `json:"value"`
	LaterTimed bool     `json:"laterTimed"`
}

// A stream made by CreateAssistantMessageEventStream times its final message as Pi's AssistantMessageEventStream does: durationMs is added unless the message has one or its
// timestamp predates the stream, and a message that arrives after the stream is done is left alone.
func TestAssistantMessageEventStreamTimesItsFinalMessage(t *testing.T) {
	var timing []goldenTiming
	for _, v := range readVectors(t, "pi-ai", "createAssistantMessageEventStream") {
		var scenario struct {
			Kind      string   `json:"kind"`
			Timestamp string   `json:"timestamp"`
			Preset    *float64 `json:"preset"`
		}
		var answer struct {
			Present    bool `json:"present"`
			Kept       bool `json:"kept"`
			LaterTimed bool `json:"laterTimed"`
		}
		if err := json.Unmarshal(v.Args[0], &scenario); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(v.Result, &answer); err != nil {
			t.Fatal(err)
		}
		c := goldenTiming{Kind: scenario.Kind, Timestamp: scenario.Timestamp, Preset: scenario.Preset, Present: answer.Present, LaterTimed: answer.LaterTimed}
		if answer.Kept {
			c.Value = scenario.Preset
		}
		timing = append(timing, c)
	}
	for _, c := range timing {
		stream := CreateAssistantMessageEventStream()
		message := func() map[string]any {
			m := map[string]any{"role": "assistant", "content": []any{}, "stopReason": "stop"}
			if c.Kind == "error" {
				m["stopReason"] = "error"
			}
			switch c.Timestamp {
			case "now":
				m["timestamp"] = float64(time.Now().UnixMilli())
			case "old":
				m["timestamp"] = float64(1)
			}
			if c.Preset != nil {
				m["durationMs"] = *c.Preset
			}
			return m
		}
		final, later := message(), message()
		delete(later, "durationMs")
		later["timestamp"] = float64(time.Now().UnixMilli())
		switch c.Kind {
		case "done":
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": final})
		case "error":
			stream.Push(map[string]any{"type": "error", "reason": "error", "error": final})
		case "end":
			stream.End(final)
		case "doneThenEnd":
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": final})
			stream.End(later)
		}
		duration, present := final["durationMs"]
		_, laterTimed := later["durationMs"]
		var value *float64
		if number, ok := duration.(float64); ok && c.Preset != nil && number == *c.Preset {
			value = c.Preset
		}
		if present != c.Present || !reflect.DeepEqual(value, c.Value) || laterTimed != c.LaterTimed {
			t.Errorf("%+v: durationMs present %t (kept %v), later message timed %t", c, present, value, laterTimed)
		}
		if number, ok := duration.(float64); present && (!ok || number < 0 || number != math.Trunc(number)) {
			t.Errorf("%+v: durationMs = %v, want a non-negative integer", c, duration)
		}
	}
}
