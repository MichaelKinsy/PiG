package ai

// Ports packages/ai/src/api/system-one-shared.ts, typesafe-system-one.ts and cloudflare-workers-ai-system-one.ts,
// and the classifier counterpart in providers/cloudflare-stream.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

const (
	ClassifierAPITypesafeSystemOne            ClassifierAPI = "typesafe-system-one"
	ClassifierAPICloudflareWorkersAISystemOne ClassifierAPI = "cloudflare-workers-ai-system-one"
	ClassifierAPILlamaCppClassify             ClassifierAPI = "llama-cpp-classify"
)

// systemOneWireRequest is the System One request body without the transport-specific envelope.
type systemOneWireRequest struct {
	state     JsonObject
	questions systemOneWireQuestions
}

// systemOneWireQuestions are the questions as System One names them: a public bool question is a wire-level noul.
type systemOneWireQuestions ClassifierQuestions

func (q systemOneWireQuestions) MarshalJSON() ([]byte, error) {
	return writeJSONObject(len(q), func(i int) string { return q[i].ID }, func(i int) ([]byte, error) {
		if bool, ok := q[i].Question.(ClassifierBoolQuestion); ok {
			return fmt.Appendf(nil, `{"type":"noul","instructions":%s,"criteria":{"true":%s,"false":%s}}`, marshalJSONString(bool.Instructions), marshalJSONString(bool.Criteria.True), marshalJSONString(bool.Criteria.False)), nil
		}
		return json.Marshal(q[i].Question)
	})
}

// systemOneTransport holds the differences between services that serve System One models.
type systemOneTransport struct {
	// api is the classifier API this transport implements.
	api ClassifierAPI
	// label names the service in error messages.
	label string
	url   func(model ClassifierModel) string
	// payload wraps the System One request in the service's request envelope.
	payload func(model ClassifierModel, request systemOneWireRequest) map[string]any
	// output extracts the System One output ({ answers, usage }) from the service's response envelope.
	output func(body json.RawMessage) (map[string]json.RawMessage, error)
}

func requiredNumber(label string, value json.RawMessage, field string) (float64, error) {
	number, ok := finiteNumberOf(value)
	if !ok {
		return 0, fmt.Errorf("%s returned an invalid %s", label, field)
	}
	return number, nil
}

func parseProbabilities(label string, raw json.RawMessage, id string) ([]ClassifierProbability, error) {
	if _, ok := jsonObjectOf(raw); !ok {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", label, id)
	}
	entries, err := orderedJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", label, id)
	}
	// Object.entries visits the parsed object in JavaScript key order, so the first invalid probability reported is the
	// first in that order, not in source order (system-one-shared.ts:70-78).
	keys := make([]string, len(entries))
	for i, entry := range entries {
		keys[i] = entry.key
	}
	probabilities := make([]ClassifierProbability, 0, len(entries))
	for _, index := range jsObjectKeyOrder(keys) {
		entry := entries[index]
		probability, err := requiredNumber(label, entry.value, fmt.Sprintf("probability for %s.%s", id, entry.key))
		if err != nil {
			return nil, err
		}
		probabilities = append(probabilities, ClassifierProbability{Key: entry.key, Probability: probability})
	}
	return probabilities, nil
}

// orderProbabilities applies the key order of a JavaScript object built from these entries.
func orderProbabilities(probabilities []ClassifierProbability) []ClassifierProbability {
	keys := make([]string, len(probabilities))
	for i, probability := range probabilities {
		keys[i] = probability.Key
	}
	ordered := make([]ClassifierProbability, len(probabilities))
	for i, index := range jsObjectKeyOrder(keys) {
		ordered[i] = probabilities[index]
	}
	return ordered
}

// orderAnswers applies the key order of a JavaScript object built from these entries.
func orderAnswers(answers ClassifierAnswers) ClassifierAnswers {
	keys := make([]string, len(answers))
	for i, answer := range answers {
		keys[i] = answer.ID
	}
	ordered := make(ClassifierAnswers, len(answers))
	for i, index := range jsObjectKeyOrder(keys) {
		ordered[i] = answers[index]
	}
	return ordered
}

func parseAnswers(label string, raw json.RawMessage, request ClassifierContext) (ClassifierAnswers, error) {
	value, ok := jsonObjectOf(raw)
	if !ok {
		return nil, fmt.Errorf("%s returned an unexpected response", label)
	}
	answers := make(ClassifierAnswers, 0, len(request.Questions))
	for _, entry := range request.Questions {
		id := entry.ID
		answer, ok := jsonObjectOf(value[id])
		if !ok {
			return nil, fmt.Errorf("%s did not return an answer for %s", label, id)
		}
		answerType, _ := jsonStringOf(answer["type"])
		switch entry.Question.(type) {
		case ClassifierChoiceQuestion:
			choice, isString := jsonStringOf(answer["choice"])
			if answerType != "choice" || !isString {
				return nil, fmt.Errorf("%s did not return a choice answer for %s", label, id)
			}
			probabilities, err := parseProbabilities(label, answer["probabilities"], id)
			if err != nil {
				return nil, err
			}
			confidence, err := requiredNumber(label, answer["confidence"], "confidence for "+id)
			if err != nil {
				return nil, err
			}
			answers = append(answers, ClassifierAnswerEntry{ID: id, Answer: ClassifierChoiceAnswer{Choice: choice, Probabilities: probabilities, Confidence: confidence}})
		case ClassifierScoreQuestion:
			if answerType != "score" {
				return nil, fmt.Errorf("%s did not return a score answer for %s", label, id)
			}
			score, err := requiredNumber(label, answer["score"], "score for "+id)
			if err != nil {
				return nil, err
			}
			confidence, err := requiredNumber(label, answer["confidence"], "confidence for "+id)
			if err != nil {
				return nil, err
			}
			answers = append(answers, ClassifierAnswerEntry{ID: id, Answer: ClassifierScoreAnswer{Score: score, Confidence: confidence}})
		default:
			if answerType != "noul" {
				return nil, fmt.Errorf("%s did not return a bool answer for %s", label, id)
			}
			probability, err := requiredNumber(label, answer["noul"], "probability for "+id)
			if err != nil {
				return nil, err
			}
			answers = append(answers, ClassifierAnswerEntry{ID: id, Answer: ClassifierBoolAnswer{Probability: probability}})
		}
	}
	return orderAnswers(answers), nil
}

func tokenCount(raw json.RawMessage, present bool) int {
	if !present {
		return 0
	}
	if value, ok := finiteNumberOf(raw); ok && value > 0 {
		return int(math.Min(value, math.MaxInt32))
	}
	return 0
}

// parseUsage reads System One's { input_tokens, output_tokens } and prices it from the model catalog like chat usage.
// A missing or malformed usage object leaves the result without usage instead of failing it.
func parseUsage(raw json.RawMessage, model ClassifierModel) *Usage {
	value, ok := jsonObjectOf(raw)
	if !ok {
		return nil
	}
	inputRaw, hasInput := value["input_tokens"]
	outputRaw, hasOutput := value["output_tokens"]
	if !hasInput && !hasOutput {
		return nil
	}
	usage := Usage{Input: tokenCount(inputRaw, hasInput), Output: tokenCount(outputRaw, hasOutput)}
	usage.TotalTokens = usage.Input + usage.Output
	CalculateCost(&model, &usage)
	return &usage
}

func systemOneRequestHeaders(model ClassifierModel, apiKey string, options ProviderHeaders) []classifierHeader {
	return classifierRequestHeaders(ProviderHeadersFromStrings(map[string]string{"authorization": "Bearer " + apiKey, "content-type": "application/json"}), ProviderHeadersFromStrings(model.Headers), options)
}

// classifySystemOne runs one System One classification over the given transport.
func classifySystemOne(ctx context.Context, transport systemOneTransport, model ClassifierModel, request ClassifierContext, options ClassifierOptions) ClassifierResult {
	output := ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, Answers: ClassifierAnswers{}, StopReason: ClassifierStopReasonStop, Timestamp: time.Now().UnixMilli()}
	fail := func(err error) ClassifierResult {
		output.StopReason = ClassifierStopReasonError
		if ctx.Err() != nil {
			output.StopReason = ClassifierStopReasonAborted
		}
		output.ErrorMessage = classifierErrorMessage(err, transport.label)
		return output
	}
	if model.API != transport.api {
		return fail(fmt.Errorf("Unsupported classifier API: %s", model.API))
	}
	if options.APIKey == "" {
		return fail(fmt.Errorf("No API key for provider: %s", model.Provider))
	}
	var payload any = transport.payload(model, systemOneWireRequest{state: request.State, questions: systemOneWireQuestions(request.Questions)})
	if options.OnPayload != nil {
		transformed, replace, err := options.OnPayload(payload, model)
		if err != nil {
			return fail(err)
		}
		if replace {
			payload = transformed
		}
	}
	body, err := marshalJSONValue(payload)
	if err != nil {
		return fail(err)
	}
	headers := systemOneRequestHeaders(model, options.APIKey, options.Headers)
	target := transport.url(model)
	response, err := retryClassifierRequest(ctx, options, func() (classifierResponse, error) {
		return classifierPost(ctx, options, transport.label, target, headers, body)
	})
	if err != nil {
		return fail(err)
	}
	if options.OnResponse != nil {
		if err := options.OnResponse(ProviderResponse{Status: response.status, Headers: headersToRecord(response.headers)}, model); err != nil {
			return fail(err)
		}
	}
	var envelope json.RawMessage = response.body
	if !json.Valid(envelope) {
		return fail(errors.New("Unexpected response body: invalid JSON"))
	}
	result, err := transport.output(envelope)
	if err != nil {
		return fail(err)
	}
	// Set before parsing answers: a request with malformed answers was still billed.
	output.Usage = parseUsage(result["usage"], model)
	answers, err := parseAnswers(transport.label, result["answers"], request)
	if err != nil {
		return fail(err)
	}
	output.Answers = answers
	return output
}

func serviceURL(baseURL, path string) string {
	base, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/" + path
	}
	return base.JoinPath(path).String()
}

// typesafeSystemOneTransport is TypeSafe's native System One protocol. OpenRouter serves the same protocol, so both
// providers use this API with different base URLs.
var typesafeSystemOneTransport = systemOneTransport{
	api:   ClassifierAPITypesafeSystemOne,
	label: "System One API",
	url:   func(model ClassifierModel) string { return serviceURL(model.BaseURL, "systemone") },
	payload: func(model ClassifierModel, request systemOneWireRequest) map[string]any {
		return map[string]any{"model": model.ID, "state": request.state, "questions": request.questions}
	},
	output: func(body json.RawMessage) (map[string]json.RawMessage, error) {
		object, ok := jsonObjectOf(body)
		if !ok {
			return nil, errors.New("System One API returned an unexpected response")
		}
		return object, nil
	},
}

// ClassifyTypesafeSystemOne runs TypeSafe's native System One protocol with public bool values mapped to wire-level
// noul. OpenRouter, OpenCode Zen and Vercel AI Gateway serve the same protocol at other base URLs.
func ClassifyTypesafeSystemOne(ctx context.Context, model ClassifierModel, request ClassifierContext, options ClassifierOptions) ClassifierResult {
	return classifySystemOne(ctx, typesafeSystemOneTransport, model, request, options)
}

const cloudflareSystemOneLabel = "Cloudflare Workers AI"

func cloudflareErrorMessage(errors json.RawMessage) string {
	if entries, ok := jsonArrayOf(errors); ok {
		var messages []string
		for _, entry := range entries {
			if object, ok := jsonObjectOf(entry); ok {
				if message, ok := jsonStringOf(object["message"]); ok {
					messages = append(messages, message)
				}
			}
		}
		if len(messages) > 0 {
			return cloudflareSystemOneLabel + " error: " + strings.Join(messages, "; ")
		}
	}
	return cloudflareSystemOneLabel + " request failed"
}

// cloudflareSystemOneTransport serves System One models on the Workers AI REST endpoint: POST /accounts/{account}/ai/run
// with { model, input }. The REST API wraps the model output in Cloudflare's API envelope. Third-party models such as
// typesafe/jev add a run record: { success, result: { state: "Completed", result: { answers, usage } } }. Cloudflare-hosted
// models such as @cf/cloudflare/clef return the output directly: { success, result: { model, answers, usage } }.
var cloudflareSystemOneTransport = systemOneTransport{
	api:   ClassifierAPICloudflareWorkersAISystemOne,
	label: cloudflareSystemOneLabel,
	url:   func(model ClassifierModel) string { return serviceURL(model.BaseURL, "run") },
	payload: func(model ClassifierModel, request systemOneWireRequest) map[string]any {
		return map[string]any{"model": model.ID, "input": map[string]any{"state": request.state, "questions": request.questions}}
	},
	output: func(body json.RawMessage) (map[string]json.RawMessage, error) {
		object, ok := jsonObjectOf(body)
		if !ok {
			return nil, errors.New(cloudflareSystemOneLabel + " returned an unexpected response")
		}
		if string(bytes.TrimSpace(object["success"])) == "false" {
			return nil, errors.New(cloudflareErrorMessage(object["errors"]))
		}
		run, ok := jsonObjectOf(object["result"])
		if !ok {
			return nil, errors.New(cloudflareSystemOneLabel + " returned an unexpected response")
		}
		// upstream: packages/ai/src/api/cloudflare-workers-ai-system-one.ts:transport.output (`"answers" in result`).
		if _, direct := run["answers"]; direct {
			return run, nil
		}
		if state, ok := jsonStringOf(run["state"]); !ok || state != "Completed" {
			return nil, fmt.Errorf("%s run did not complete (state: %s)", cloudflareSystemOneLabel, jsStateString(run["state"]))
		}
		result, ok := jsonObjectOf(run["result"])
		if !ok {
			return nil, errors.New(cloudflareSystemOneLabel + " returned an unexpected response")
		}
		return result, nil
	},
}

// jsStateString is String(value) for a parsed JSON value; an absent member is undefined.
func jsStateString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "undefined"
	}
	return jsJSONValueString(trimmed)
}

// jsJSONValueString is String(value) for a present JSON value: objects are "[object Object]", arrays join their
// elements with "," (null elements are empty) and numbers use JavaScript's number formatting.
func jsJSONValueString(raw json.RawMessage) string {
	if text, ok := jsonStringOf(raw); ok {
		return text
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && (trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9')) {
		return jsnumber.String(jsnumber.FromJSON(trimmed))
	}
	if elements, ok := jsonArrayOf(raw); ok {
		parts := make([]string, len(elements))
		for i, element := range elements {
			if element = bytes.TrimSpace(element); string(element) != "null" {
				parts[i] = jsJSONValueString(element)
			}
		}
		return strings.Join(parts, ",")
	}
	if _, ok := jsonObjectOf(raw); ok {
		return "[object Object]"
	}
	return string(bytes.TrimSpace(raw))
}

// ClassifyCloudflareWorkersAISystemOne runs System One models on the Workers AI REST endpoint, with public bool values
// mapped to wire-level noul.
func ClassifyCloudflareWorkersAISystemOne(ctx context.Context, model ClassifierModel, request ClassifierContext, options ClassifierOptions) ClassifierResult {
	return classifySystemOne(ctx, cloudflareSystemOneTransport, model, request, options)
}

func classifierModule(classify func(context.Context, ClassifierModel, ClassifierContext, ClassifierOptions) ClassifierResult) *ProviderClassifier {
	return &ProviderClassifier{Classify: func(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		return classify(ctx, *model, request, options), nil
	}}
}

// TypesafeSystemOneAPI is the typesafe-system-one classifier module.
func TypesafeSystemOneAPI() *ProviderClassifier { return classifierModule(ClassifyTypesafeSystemOne) }

// CloudflareWorkersAISystemOneAPI is the cloudflare-workers-ai-system-one classifier module.
func CloudflareWorkersAISystemOneAPI() *ProviderClassifier {
	return classifierModule(ClassifyCloudflareWorkersAISystemOne)
}

// CloudflareClassifier wraps a classifier module so Cloudflare account and gateway endpoint placeholders materialize
// from the resolved provider env before dispatch.
func CloudflareClassifier(classifier *ProviderClassifier) *ProviderClassifier {
	return &ProviderClassifier{Classify: func(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		resolved := *model
		baseURL, err := ResolveCloudflareBaseURL("cloudflare-workers-ai", model.BaseURL, options.Env)
		if err != nil {
			return ClassifierResult{}, err
		}
		resolved.BaseURL = baseURL
		return classifier.Classify(ctx, &resolved, request, options)
	}}
}
