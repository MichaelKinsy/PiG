package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var llamaServerCount atomic.Int64

// llamaTestModel returns a fresh server URL per test: label tokens are cached per server and model.
func llamaTestModel() ClassifierModel {
	n := llamaServerCount.Add(1)
	return ClassifierModel{ID: "qwen", Name: "qwen", API: "llama-cpp-classify", Provider: "llama.cpp", BaseURL: fmt.Sprintf("http://llama-%d.test:8080/v1", n), Input: []string{"text"}, ContextWindow: 32768}
}

type llamaRecordedRequest struct {
	url     string
	body    map[string]any
	headers http.Header
}

type llamaFakeServerOptions struct {
	// next returns the log-probabilities of the next token by token text.
	next     func(prompt string, depth int) map[string]float64
	template func(messages []map[string]any) string
	tokenize func(content string) []int
}

type llamaFakeServer struct {
	mu       sync.Mutex
	requests []llamaRecordedRequest
	fetch    *http.Client
}

func (s *llamaFakeServer) recorded() []llamaRecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llamaRecordedRequest(nil), s.requests...)
}

// charTokens gives one token per character: the character code.
func charTokens(content string) []int {
	tokens := []int{}
	for _, r := range content {
		tokens = append(tokens, int(r))
	}
	return tokens
}

func newLlamaFakeServer(t *testing.T, options llamaFakeServerOptions) *llamaFakeServer {
	t.Helper()
	server := &llamaFakeServer{}
	server.fetch = clsClient(func(r *http.Request) (*http.Response, error) {
		body := clsBody(t, r)
		server.mu.Lock()
		server.requests = append(server.requests, llamaRecordedRequest{url: r.URL.String(), body: body, headers: r.Header.Clone()})
		server.mu.Unlock()
		switch r.URL.Path {
		case "/tokenize":
			tokenize := options.tokenize
			if tokenize == nil {
				tokenize = charTokens
			}
			data, _ := json.Marshal(map[string]any{"tokens": tokenize(body["content"].(string))})
			return clsJSON(200, string(data)), nil
		case "/apply-template":
			var messages []map[string]any
			for _, message := range body["messages"].([]any) {
				messages = append(messages, message.(map[string]any))
			}
			var prompt string
			if options.template != nil {
				prompt = options.template(messages)
			} else {
				for _, message := range messages {
					prompt += fmt.Sprintf("<|%s|>\n%s\n", message["role"], message["content"])
				}
				prompt += "<|assistant|>\n"
			}
			data, _ := json.Marshal(map[string]any{"prompt": prompt})
			return clsJSON(200, string(data)), nil
		case "/completion":
			next := map[string]float64{"A": -0.1, "B": -2.5}
			if options.next != nil {
				next = options.next(body["prompt"].(string), int(body["n_probs"].(float64)))
			}
			var top []map[string]any
			for token, logprob := range next {
				top = append(top, map[string]any{"id": int([]rune(token)[0]), "token": token, "bytes": []int{}, "logprob": logprob})
			}
			data, _ := json.Marshal(map[string]any{"content": "A", "completion_probabilities": []any{map[string]any{"id": 65, "token": "A", "top_logprobs": top}}})
			return clsJSON(200, string(data)), nil
		}
		return clsText(404, "not found", nil), nil
	})
	return server
}

// answerByPrompt gives completion log-probabilities for bool (Yes/No) and letter labels; the fake tokenizer maps a label
// to its first character.
func answerByPrompt(prompt string, _ int) map[string]float64 {
	if strings.Contains(prompt, "Answer Yes or No.") {
		return map[string]float64{"Y": -0.05, "N": -3}
	}
	if strings.Contains(prompt, "Answer with one level number.") {
		return map[string]float64{"2": -0.2, "1": -1.8, "0": -4}
	}
	return map[string]float64{"B": -0.3, "A": -1.5, "C": -3}
}

func llamaTestContext() ClassifierContext {
	return ClassifierContext{State: JsonObject{"message": "Help! My payouts have been failing for 3 days."}, Questions: ClassifierQuestions{
		{ID: "team", Question: ClassifierChoiceQuestion{Instructions: "Which team should handle this?", Criteria: clsChoices("billing", "Payments and refunds", "technical", "Bugs and outages", "sales", "")}},
		{ID: "urgent", Question: ClassifierBoolQuestion{Instructions: "Does this convey urgency?", Criteria: ClassifierBoolCriteria{True: "The user needs help soon", False: "No time pressure"}}},
		{ID: "severity", Question: ClassifierScoreQuestion{Instructions: "How severe is this?", Criteria: []string{"low", "medium", "high"}}},
	}}
}

// wordTokens maps multi-character labels to single tokens, as a real vocabulary would.
func wordTokens(content string) []int {
	words := map[string]int{"Yes": 89, "No": 78}
	tokens := []int{}
	var parts []string
	for len(content) > 0 {
		if index := strings.Index(content, "\n"); index < 0 {
			parts, content = append(parts, content), ""
		} else {
			if index > 0 {
				parts = append(parts, content[:index])
			}
			parts, content = append(parts, "\n"), content[index+1:]
		}
	}
	for _, part := range parts {
		if id, ok := words[part]; ok {
			tokens = append(tokens, id)
		} else {
			tokens = append(tokens, charTokens(part)...)
		}
	}
	return tokens
}

func llamaPickContext() ClassifierContext {
	return ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "pick", Question: ClassifierChoiceQuestion{Instructions: "Pick", Criteria: clsChoices("a", "", "b", "")}}}}
}

func llamaCompletionDepths(requests []llamaRecordedRequest) []int {
	depths := []int{}
	for _, request := range requests {
		if strings.HasSuffix(request.url, "/completion") {
			depths = append(depths, int(request.body["n_probs"].(float64)))
		}
	}
	return depths
}

func llamaRequestsTo(requests []llamaRecordedRequest, suffix string) []llamaRecordedRequest {
	var out []llamaRecordedRequest
	for _, request := range requests {
		if strings.HasSuffix(request.url, suffix) {
			out = append(out, request)
		}
	}
	return out
}

// Ports packages/ai/test/llama-cpp-classify.test.ts.
func TestLlamaCppClassifyUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:117
	t.Run("answers choice, bool and score questions from label log-probabilities", func(t *testing.T) {
		server := newLlamaFakeServer(t, llamaFakeServerOptions{next: answerByPrompt, tokenize: wordTokens})
		model := llamaTestModel()

		result := ClassifyLlamaCpp(t.Context(), model, llamaTestContext(), ClassifierOptions{APIKey: "local", Fetch: server.fetch})

		if result.ErrorMessage != "" || result.StopReason != "stop" {
			t.Fatalf("result=%+v", result)
		}
		choice := LlamaCppLabelProbabilities([]float64{-1.5, -0.3, -3}, 1)
		wantTeam := ClassifierChoiceAnswer{Choice: "technical", Probabilities: []ClassifierProbability{{"billing", choice[0]}, {"technical", choice[1]}, {"sales", choice[2]}}, Confidence: LlamaCppPeakConfidence(choice)}
		if got := clsAnswer(t, result.Answers, "team"); !reflect.DeepEqual(got, wantTeam) {
			t.Errorf("team=%+v want %+v", got, wantTeam)
		}
		if got, want := clsAnswer(t, result.Answers, "urgent"), (ClassifierBoolAnswer{Probability: LlamaCppLabelProbabilities([]float64{-0.05, -3}, 1)[0]}); got != want {
			t.Errorf("urgent=%+v want %+v", got, want)
		}
		levels := LlamaCppLabelProbabilities([]float64{-4, -1.8, -0.2}, 1)
		if got, want := clsAnswer(t, result.Answers, "severity"), (ClassifierScoreAnswer{Score: levels[1] + 2*levels[2], Confidence: LlamaCppPeakConfidence(levels)}); got != want {
			t.Errorf("severity=%+v want %+v", got, want)
		}

		root := strings.TrimSuffix(model.BaseURL, "/v1")
		for _, request := range server.recorded() {
			if !strings.HasPrefix(request.url, root+"/") || request.body["model"] != "qwen" || request.headers.Get("authorization") != "Bearer local" {
				t.Errorf("request=%+v", request)
			}
		}
		completion := llamaRequestsTo(server.recorded(), "/completion")[0].body
		if completion["n_predict"] != 1.0 || completion["n_probs"] != 256.0 || completion["post_sampling_probs"] != false || completion["cache_prompt"] != true {
			t.Errorf("completion=%v", completion)
		}
		if template := llamaRequestsTo(server.recorded(), "/apply-template")[0].body; !reflect.DeepEqual(template["chat_template_kwargs"], map[string]any{"enable_thinking": false}) {
			t.Errorf("template=%v", template)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:157
	t.Run("repeats the state around all questions and ends with this question's labels", func(t *testing.T) {
		rendered, err := LlamaCppRenderQuestion(llamaTestContext(), "team")
		if err != nil {
			t.Fatal(err)
		}
		state := "State:\n{\n \"message\": \"Help! My payouts have been failing for 3 days.\"\n}"
		if !reflect.DeepEqual(rendered.Labels, []string{"A", "B", "C"}) || !reflect.DeepEqual(rendered.Keys, []string{"billing", "technical", "sales"}) {
			t.Errorf("labels=%v keys=%v", rendered.Labels, rendered.Keys)
		}
		want := strings.Join([]string{
			state,
			"",
			"Task: answer each of the following questions about the state.",
			"",
			"Question: Which team should handle this?",
			"",
			"Options:",
			"- billing: Payments and refunds",
			"- technical: Bugs and outages",
			"- sales",
			"",
			"Question: Does this convey urgency?",
			"",
			"Yes means: The user needs help soon",
			"No means: No time pressure",
			"",
			"Question: How severe is this?",
			"",
			"Levels:",
			"0. low",
			"1. medium",
			"2. high",
			"",
			state,
			"",
			"Question: Which team should handle this?",
			"",
			"Options:",
			"A. billing: Payments and refunds",
			"B. technical: Bugs and outages",
			"C. sales",
			"",
			"Answer with one letter.",
		}, "\n")
		if rendered.Content != want {
			t.Errorf("content=%q\nwant %q", rendered.Content, want)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:201
	t.Run("shares everything before the final question across the questions of a request", func(t *testing.T) {
		prefix := func(id string) string {
			rendered, err := LlamaCppRenderQuestion(llamaTestContext(), id)
			if err != nil {
				t.Fatal(err)
			}
			return rendered.Content[:strings.LastIndex(rendered.Content, "Question:")]
		}
		if prefix("urgent") != prefix("team") || prefix("severity") != prefix("team") {
			t.Error("prefixes differ")
		}
		urgent, _ := LlamaCppRenderQuestion(llamaTestContext(), "urgent")
		severity, _ := LlamaCppRenderQuestion(llamaTestContext(), "severity")
		if !strings.HasSuffix(urgent.Content, "No means: No time pressure\n\nAnswer Yes or No.") || !strings.HasSuffix(severity.Content, "2. high\n\nAnswer with one level number.") {
			t.Error("final question suffix")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:216
	t.Run("divides label log-probabilities by the temperature", func(t *testing.T) {
		server := newLlamaFakeServer(t, llamaFakeServerOptions{next: func(string, int) map[string]float64 { return map[string]float64{"A": -0.1, "B": -2.5} }})

		result := ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaPickContext(), ClassifierOptions{Fetch: server.fetch, Temperature: new(2.0)})

		expected := LlamaCppLabelProbabilities([]float64{-0.1 / 2, -2.5 / 2}, 1)
		pick, _ := clsAnswer(t, result.Answers, "pick").(ClassifierChoiceAnswer)
		if !reflect.DeepEqual(pick.Probabilities, []ClassifierProbability{{"a", expected[0]}, {"b", expected[1]}}) {
			t.Errorf("pick=%+v want %v", pick, expected)
		}
		if !reflect.DeepEqual(LlamaCppLabelProbabilities([]float64{-0.1, -2.5}, 2), expected) {
			t.Error("temperature scaling differs")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:229
	t.Run("rejects non-positive temperatures before sending requests", func(t *testing.T) {
		server := newLlamaFakeServer(t, llamaFakeServerOptions{})
		result := ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaTestContext(), ClassifierOptions{Fetch: server.fetch, Temperature: new(0.0)})

		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "Temperature must be a positive number, got 0") || len(server.recorded()) != 0 {
			t.Errorf("result=%+v requests=%d", result, len(server.recorded()))
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:238
	t.Run("retries deeper readouts when a label is missing and fails without inventing zeros", func(t *testing.T) {
		deep := newLlamaFakeServer(t, llamaFakeServerOptions{next: func(_ string, depth int) map[string]float64 {
			if depth < 4096 {
				return map[string]float64{"A": -0.1}
			}
			return map[string]float64{"A": -0.1, "B": -9}
		}})
		recovered := ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaPickContext(), ClassifierOptions{Fetch: deep.fetch})
		if recovered.StopReason != "stop" || !reflect.DeepEqual(llamaCompletionDepths(deep.recorded()), []int{256, 4096}) {
			t.Errorf("recovered=%+v depths=%v", recovered, llamaCompletionDepths(deep.recorded()))
		}

		never := newLlamaFakeServer(t, llamaFakeServerOptions{next: func(string, int) map[string]float64 { return map[string]float64{"A": -0.1} }})
		failed := ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaPickContext(), ClassifierOptions{Fetch: never.fetch})
		if failed.StopReason != "error" || len(failed.Answers) != 0 || !strings.Contains(failed.ErrorMessage, "did not rank labels B for pick within the top 32768 tokens") {
			t.Errorf("failed=%+v", failed)
		}
		if !reflect.DeepEqual(llamaCompletionDepths(never.recorded()), []int{256, 4096, 32768}) {
			t.Errorf("depths=%v", llamaCompletionDepths(never.recorded()))
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:257
	t.Run("closes a reasoning block the template leaves open", func(t *testing.T) {
		server := newLlamaFakeServer(t, llamaFakeServerOptions{template: func([]map[string]any) string { return "<|assistant|>\n<think>" }})
		ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaPickContext(), ClassifierOptions{Fetch: server.fetch})

		completion := llamaRequestsTo(server.recorded(), "/completion")
		if len(completion) == 0 || completion[0].body["prompt"] != "<|assistant|>\n<think></think>" {
			t.Errorf("completion=%+v", completion)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:269
	t.Run("reads labels in reply position and rejects labels that are not one token", func(t *testing.T) {
		// A tokenizer that merges a newline with a following letter falls back to the label alone.
		merging := newLlamaFakeServer(t, llamaFakeServerOptions{tokenize: func(content string) []int {
			if strings.HasPrefix(content, "\n") && len(content) > 1 {
				return []int{1000}
			}
			return charTokens(content)
		}})
		merged := ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaPickContext(), ClassifierOptions{Fetch: merging.fetch})
		if merged.StopReason != "stop" {
			t.Errorf("merged=%+v", merged)
		}

		// The default fake tokenizer splits "Yes" into three tokens.
		split := newLlamaFakeServer(t, llamaFakeServerOptions{})
		boolContext := ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "ok", Question: ClassifierBoolQuestion{Instructions: "OK?"}}}}
		result := ClassifyLlamaCpp(t.Context(), llamaTestModel(), boolContext, ClassifierOptions{Fetch: split.fetch})
		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, `Label "Yes" is not a single token for qwen`) {
			t.Errorf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:292
	t.Run("caches label tokens per server and model", func(t *testing.T) {
		model := llamaTestModel()
		server := newLlamaFakeServer(t, llamaFakeServerOptions{})
		ClassifyLlamaCpp(t.Context(), model, llamaPickContext(), ClassifierOptions{Fetch: server.fetch})
		firstTokenizations := len(llamaRequestsTo(server.recorded(), "/tokenize"))
		ClassifyLlamaCpp(t.Context(), model, llamaPickContext(), ClassifierOptions{Fetch: server.fetch})

		if firstTokenizations == 0 || len(llamaRequestsTo(server.recorded(), "/tokenize")) != firstTokenizations {
			t.Errorf("first=%d total=%d", firstTokenizations, len(llamaRequestsTo(server.recorded(), "/tokenize")))
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:306
	t.Run("validates option counts before sending requests", func(t *testing.T) {
		server := newLlamaFakeServer(t, llamaFakeServerOptions{})
		var criteria []ClassifierChoiceCriterion
		for index := range 63 {
			criteria = append(criteria, ClassifierChoiceCriterion{Key: "option" + strconv.Itoa(index)})
		}
		tooMany := ClassifyLlamaCpp(t.Context(), llamaTestModel(), ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "pick", Question: ClassifierChoiceQuestion{Instructions: "Pick", Criteria: criteria}}}}, ClassifierOptions{Fetch: server.fetch})
		tooFew := ClassifyLlamaCpp(t.Context(), llamaTestModel(), ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "rate", Question: ClassifierScoreQuestion{Instructions: "Rate", Criteria: []string{"only"}}}}}, ClassifierOptions{Fetch: server.fetch})

		if !strings.Contains(tooMany.ErrorMessage, "A choice question needs 2 to 62 options, got 63") || !strings.Contains(tooFew.ErrorMessage, "A score question needs 2 to 10 levels, got 1") || len(server.recorded()) != 0 {
			t.Errorf("tooMany=%q tooFew=%q requests=%d", tooMany.ErrorMessage, tooFew.ErrorMessage, len(server.recorded()))
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:325
	t.Run("passes completion payloads and responses through the request hooks", func(t *testing.T) {
		server := newLlamaFakeServer(t, llamaFakeServerOptions{})
		var payloads []any
		var statuses []int
		options := ClassifierOptions{Fetch: server.fetch,
			OnPayload: func(payload any, _ ClassifierModel) (any, bool, error) {
				payloads = append(payloads, payload)
				replaced := map[string]any{}
				maps.Copy(replaced, payload.(map[string]any))
				replaced["id_slot"] = 1
				return replaced, true, nil
			},
			OnResponse: func(response ProviderResponse, _ ClassifierModel) error {
				statuses = append(statuses, response.Status)
				return nil
			},
		}

		ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaPickContext(), options)

		if len(payloads) != 1 || !reflect.DeepEqual(statuses, []int{200}) {
			t.Fatalf("payloads=%v statuses=%v", payloads, statuses)
		}
		if nPredict, _ := json.Marshal(payloads[0].(map[string]any)["n_predict"]); string(nPredict) != "1" {
			t.Errorf("payload=%v", payloads[0])
		}
		if got := llamaRequestsTo(server.recorded(), "/completion")[0].body["id_slot"]; got != 1.0 {
			t.Errorf("id_slot=%v", got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:352
	t.Run("reports server errors and cancellation", func(t *testing.T) {
		failing := ClassifyLlamaCpp(t.Context(), llamaTestModel(), llamaTestContext(), ClassifierOptions{MaxRetries: new(0), Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			return clsText(400, `{"error":{"message":"context overflow"}}`, nil), nil
		})})
		if failing.StopReason != "error" || !strings.Contains(failing.ErrorMessage, "llama.cpp error (400)") || !strings.Contains(failing.ErrorMessage, "context overflow") {
			t.Errorf("failing=%+v", failing)
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		aborted := ClassifyLlamaCpp(ctx, llamaTestModel(), llamaTestContext(), ClassifierOptions{Fetch: clsClient(func(r *http.Request) (*http.Response, error) {
			if err := r.Context().Err(); err != nil {
				return nil, err
			}
			return clsJSON(200, `{}`), nil
		})})
		if aborted.StopReason != "aborted" {
			t.Errorf("aborted=%+v", aborted)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:373
	t.Run("rejects models for other classifier APIs", func(t *testing.T) {
		server := newLlamaFakeServer(t, llamaFakeServerOptions{})
		model := llamaTestModel()
		model.API = "typesafe-system-one"
		result := ClassifyLlamaCpp(t.Context(), model, llamaTestContext(), ClassifierOptions{Fetch: server.fetch})

		if !strings.Contains(result.ErrorMessage, "Unsupported classifier API: typesafe-system-one") || len(server.recorded()) != 0 {
			t.Errorf("result=%+v", result)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:381
	t.Run("derives the server root from OpenAI-compatible base URLs", func(t *testing.T) {
		for input, want := range map[string]string{
			"http://127.0.0.1:8080/v1/":     "http://127.0.0.1:8080",
			"https://example.com/prefix/v1": "https://example.com/prefix",
			"http://127.0.0.1:8080":         "http://127.0.0.1:8080",
		} {
			if got := LlamaCppServerRoot(input); got != want {
				t.Errorf("LlamaCppServerRoot(%q)=%q want %q", input, got, want)
			}
		}
	})
	// .upstream/v0.99.1/packages/ai/test/llama-cpp-classify.test.ts:387
	t.Run("computes TypeSafe's confidence and expected scores", func(t *testing.T) {
		if got := LlamaCppPeakConfidence([]float64{0.89, 0.06, 0.05}); math.Abs(got-0.835) >= 5e-6 {
			t.Errorf("confidence=%v", got)
		}
		if LlamaCppPeakConfidence([]float64{0.5, 0.5}) != 0 || LlamaCppPeakConfidence([]float64{1, 0, 0}) != 1 {
			t.Error("confidence bounds")
		}
		got := LlamaCppAnswerFromProbabilities(ClassifierScoreQuestion{Criteria: []string{"a", "b", "c"}}, []string{"0", "1", "2"}, []float64{0.2, 0.3, 0.5})
		if want := (ClassifierScoreAnswer{Score: 1.3, Confidence: LlamaCppPeakConfidence([]float64{0.2, 0.3, 0.5})}); got != want {
			t.Errorf("answer=%+v want %+v", got, want)
		}
	})
}
