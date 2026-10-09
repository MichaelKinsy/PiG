package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type classifierAnswersProbe struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Body   string `json:"body"`
	// Numeric asks the integer-named questions b, 10, 2, a instead of pick, rate, fine.
	Numeric bool `json:"numeric"`
}

// pi: packages/ai/src/types.ts

// ClassifierAnswer (types.ts:697) is what system-one-shared.ts:42 parseAnswers builds from a service response: a choice answer with a string choice,
// finite probabilities and confidence, a score answer, a bool answer read from the wire's `noul`. A missing or ill-typed answer, probability or
// number fails the whole classification with a named message. The pinned pi-ai typesafe-system-one classifier and PiG's ClassifyTypesafeSystemOne
// read the same canned responses and must agree on the stop reason, the error message, the answers and the usage.
func TestClassifierAnswersMatchPi(t *testing.T) {
	good := map[string]any{
		"pick": map[string]any{"type": "choice", "choice": "a", "probabilities": map[string]any{"a": 0.75, "b": 0.25}, "confidence": 0.5},
		"rate": map[string]any{"type": "score", "score": 3, "confidence": 0.25},
		"fine": map[string]any{"type": "noul", "noul": 0.125},
	}
	with := func(id string, answer any) map[string]any {
		out := map[string]any{}
		for k, v := range good {
			out[k] = v
		}
		if answer == nil {
			delete(out, id)
		} else {
			out[id] = answer
		}
		return out
	}
	object := func(fields ...any) map[string]any {
		out := map[string]any{}
		for i := 0; i < len(fields); i += 2 {
			out[fields[i].(string)] = fields[i+1]
		}
		return out
	}
	ok := func(answers any, extra ...any) string {
		body := object("answers", answers)
		for i := 0; i < len(extra); i += 2 {
			body[extra[i].(string)] = extra[i+1]
		}
		encoded, _ := json.Marshal(body)
		return string(encoded)
	}
	var probes []classifierAnswersProbe
	add := func(name string, status int, body string) {
		probes = append(probes, classifierAnswersProbe{Name: name, Status: status, Body: body})
	}
	add("valid", 200, ok(good))
	add("extra keys and unknown answer id", 200, ok(with("zzz", object("type", "bool")), "other", 1))
	add("usage", 200, ok(good, "usage", object("input_tokens", 12, "output_tokens", 3)))
	add("usage input only", 200, ok(good, "usage", object("input_tokens", 7)))
	add("usage negative", 200, ok(good, "usage", object("input_tokens", -4, "output_tokens", 2)))
	add("usage strings", 200, ok(good, "usage", object("input_tokens", "9", "output_tokens", nil)))
	add("usage empty object", 200, ok(good, "usage", object()))
	add("usage array", 200, ok(good, "usage", []any{1}))
	add("usage with malformed answers", 200, ok(with("pick", nil), "usage", object("input_tokens", 5, "output_tokens", 6)))
	for _, id := range []string{"pick", "rate", "fine"} {
		add("missing "+id, 200, ok(with(id, nil)))
		add("null "+id, 200, ok(with(id, json.RawMessage("null"))))
		add("array "+id, 200, ok(with(id, []any{1})))
		add("string "+id, 200, ok(with(id, "x")))
		add("empty "+id, 200, ok(with(id, object())))
	}
	add("pick type score", 200, ok(with("pick", object("type", "score", "score", 1, "confidence", 1))))
	add("pick choice number", 200, ok(with("pick", object("type", "choice", "choice", 7, "probabilities", object("a", 1), "confidence", 1))))
	add("pick choice missing", 200, ok(with("pick", object("type", "choice", "probabilities", object("a", 1), "confidence", 1))))
	add("pick choice empty string", 200, ok(with("pick", object("type", "choice", "choice", "", "probabilities", object(), "confidence", 0))))
	add("pick probabilities missing", 200, ok(with("pick", object("type", "choice", "choice", "a", "confidence", 1))))
	add("pick probabilities array", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", []any{0.5}, "confidence", 1))))
	add("pick probabilities null", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", nil, "confidence", 1))))
	add("pick probability string", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("a", "0.5"), "confidence", 1))))
	add("pick probability null", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("a", 1, "b", nil), "confidence", 1))))
	add("pick probability bool", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("a", true), "confidence", 1))))
	add("pick probabilities integer-like keys", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("b", 1, "10", 2, "2", 3, "a", 4), "confidence", 1))))
	add("pick probabilities __proto__", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", json.RawMessage(`{"__proto__":1,"a":2}`), "confidence", 1))))
	add("pick confidence missing", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("a", 1)))))
	add("pick confidence string", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("a", 1), "confidence", "1"))))
	add("pick confidence huge", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("a", 1), "confidence", json.RawMessage("1e999")))))
	add("pick confidence negative zero", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", object("a", json.RawMessage("-0")), "confidence", json.RawMessage("-0")))))
	add("pick numbers", 200, ok(with("pick", object("type", "choice", "choice", "a", "probabilities", json.RawMessage(`{"a":1e21,"b":1e-7}`), "confidence", json.RawMessage("123456789012345678901234567890")))))
	add("rate type choice", 200, ok(with("rate", object("type", "choice", "choice", "a"))))
	add("rate type missing", 200, ok(with("rate", object("score", 1, "confidence", 1))))
	add("rate score missing", 200, ok(with("rate", object("type", "score", "confidence", 1))))
	add("rate score string", 200, ok(with("rate", object("type", "score", "score", "3", "confidence", 1))))
	add("rate score null", 200, ok(with("rate", object("type", "score", "score", nil, "confidence", 1))))
	add("rate score negative and large", 200, ok(with("rate", object("type", "score", "score", -2.5e10, "confidence", 100))))
	add("rate confidence missing", 200, ok(with("rate", object("type", "score", "score", 1))))
	add("rate confidence bool", 200, ok(with("rate", object("type", "score", "score", 1, "confidence", false))))
	add("fine type bool", 200, ok(with("fine", object("type", "bool", "noul", 0.5))))
	add("fine type noul without value", 200, ok(with("fine", object("type", "noul"))))
	add("fine probability key", 200, ok(with("fine", object("type", "noul", "probability", 0.5))))
	add("fine probability string", 200, ok(with("fine", object("type", "noul", "noul", "0.5"))))
	add("fine probability above one", 200, ok(with("fine", object("type", "noul", "noul", 7))))
	add("answers null", 200, ok(nil))
	add("answers array", 200, ok([]any{1, 2}))
	add("answers string", 200, ok("x"))
	add("answers missing", 200, `{}`)
	for _, body := range []string{`[]`, `null`, `"text"`, `5`, `true`, ``, `{`, `{"answers":`, "\ufeff{}"} {
		add("body "+body, 200, body)
	}
	add("error 400 json", 400, `{"error":{"message":"bad request"}}`)
	add("error 400 message", 400, `{"message":"nope"}`)
	add("error 400 text", 400, `plain text`)
	add("error 400 empty", 400, ``)
	add("error 401", 401, `{"error":"unauthorized"}`)
	add("error 404 html", 404, `<html>not found</html>`)
	add("error 403 nested", 403, `{"error":{"code":403,"message":"forbidden","metadata":{"raw":"x"}}}`)
	add("error 422 array", 422, `[{"msg":"m"}]`)

	// The request carries the questions in JavaScript key order, bool questions as noul (system-one-shared.ts:100-112).
	numericAnswers := `{"b":{"type":"noul","noul":0.5},"10":{"type":"choice","choice":"z","probabilities":{"z":1},"confidence":1},"2":{"type":"score","score":1,"confidence":1},"a":{"type":"score","score":2,"confidence":0}}`
	probes = append(probes, classifierAnswersProbe{Name: "numeric ids", Status: 200, Body: `{"answers":` + numericAnswers + `}`, Numeric: true},
		classifierAnswersProbe{Name: "numeric ids missing", Status: 200, Body: `{"answers":{"b":{"type":"noul","noul":0.5}}}`, Numeric: true})
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/classifier_answers.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Request      string
		StopReason   string
		ErrorMessage *string
		Answers      map[string]any
		AnswersJSON  string `json:"answersJson"`
		Usage        map[string]float64
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	questions := ClassifierQuestions{
		{ID: "pick", Question: ClassifierChoiceQuestion{Instructions: "Pick", Criteria: []ClassifierChoiceCriterion{{Key: "a", Description: "A"}, {Key: "b", Description: "B"}}}},
		{ID: "rate", Question: ClassifierScoreQuestion{Instructions: "Rate", Criteria: []string{"low", "high"}}},
		{ID: "fine", Question: ClassifierBoolQuestion{Instructions: "Fine?", Criteria: ClassifierBoolCriteria{True: "yes", False: "no"}}},
	}
	// The questions a JavaScript caller builds from {b, 10, 2, a}: integer-like names come first in ascending order.
	numericQuestions := ClassifierQuestions{
		{ID: "2", Question: ClassifierScoreQuestion{Instructions: "Two", Criteria: []string{}}},
		{ID: "10", Question: ClassifierChoiceQuestion{Instructions: "Ten", Criteria: []ClassifierChoiceCriterion{{Key: "2", Description: "two"}, {Key: "10", Description: "ten"}, {Key: "z", Description: "Z"}}}},
		{ID: "b", Question: ClassifierBoolQuestion{Instructions: "B? \u2028 <&>", Criteria: ClassifierBoolCriteria{True: "t", False: "f"}}},
		{ID: "a", Question: ClassifierScoreQuestion{Instructions: "A", Criteria: []string{"só", "high \U0001F600"}}},
	}
	failures := 0
	for i, probe := range probes {
		var sent []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sent, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(probe.Status)
			_, _ = w.Write([]byte(probe.Body))
		}))
		model := ClassifierModel{ID: "m", Name: "M", API: "typesafe-system-one", Provider: "p", BaseURL: server.URL + "/", Input: []string{"text"}, ContextWindow: 1000}
		asked := questions
		if probe.Numeric {
			asked = numericQuestions
		}
		result := ClassifyTypesafeSystemOne(t.Context(), model, ClassifierContext{State: JsonObject{"a": 1, "nested": []any{1, JsonObject{"b": nil}}}, Questions: asked}, ClassifierOptions{APIKey: "k"})
		server.Close()
		if !sameRequest(sent, want[i].Request) {
			if failures++; failures <= 12 {
				t.Errorf("probe %q request:\n PiG %s\n Pi  %s", probe.Name, sent, want[i].Request)
			}
		}
		wantMessage := ""
		if want[i].ErrorMessage != nil {
			wantMessage = *want[i].ErrorMessage
		}
		var gotAnswers map[string]any
		encoded, err := json.Marshal(result.Answers)
		if err == nil {
			err = json.Unmarshal(encoded, &gotAnswers)
		}
		if err != nil {
			t.Fatalf("probe %q: answers %v", probe.Name, err)
		}
		var gotUsage map[string]float64
		if result.Usage != nil {
			gotUsage = map[string]float64{"input": float64(result.Usage.Input), "output": float64(result.Usage.Output), "totalTokens": float64(result.Usage.TotalTokens)}
		}
		if string(result.StopReason) != want[i].StopReason || result.ErrorMessage != wantMessage || !reflect.DeepEqual(gotAnswers, want[i].Answers) || string(encoded) != want[i].AnswersJSON || !reflect.DeepEqual(gotUsage, want[i].Usage) {
			if failures++; failures <= 12 {
				t.Errorf("probe %q (%d %.60s):\n PiG %s %q answers=%v usage=%v\n Pi  %s %q answers=%v usage=%v", probe.Name, probe.Status, probe.Body, result.StopReason, result.ErrorMessage, gotAnswers, gotUsage, want[i].StopReason, wantMessage, want[i].Answers, want[i].Usage)
			}
		}
	}
	if failures > 12 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// sameRequest compares two request bodies member by member in the bytes each member is written in, so the order of the questions, the criteria and
// the state is checked. Only the order of the top-level members (model, state, questions) is left out: PiG hands the request to the payload hook
// as a map, which writes them sorted, and no server reads them by position.
func sameRequest(got []byte, want string) bool {
	var gotMembers, wantMembers map[string]json.RawMessage
	if json.Unmarshal(got, &gotMembers) != nil || json.Unmarshal([]byte(want), &wantMembers) != nil || len(gotMembers) != len(wantMembers) {
		return false
	}
	for name, value := range wantMembers {
		if string(gotMembers[name]) != string(value) {
			return false
		}
	}
	return true
}
