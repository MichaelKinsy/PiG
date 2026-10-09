package ai

// Ports the classifier parts of packages/ai/src/types.ts and packages/ai/src/utils/model-operations.ts.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// ClassifierQuestion is the closed union of classifier questions: ClassifierChoiceQuestion, ClassifierScoreQuestion and
// ClassifierBoolQuestion.
type ClassifierQuestion interface {
	classifierQuestion()
	// QuestionType is the wire `type` of the question: "choice", "score" or "bool".
	QuestionType() string
}

// ClassifierChoiceCriterion is one option of a choice question.
type ClassifierChoiceCriterion struct {
	Key         string
	Description string
}

// ClassifierChoiceQuestion asks for one of several named options. Criteria keep their declaration order.
type ClassifierChoiceQuestion struct {
	Instructions string
	Criteria     []ClassifierChoiceCriterion
}

// ClassifierScoreQuestion asks for a level; Criteria describe the levels from lowest to highest.
type ClassifierScoreQuestion struct {
	Instructions string
	Criteria     []string
}

// ClassifierBoolCriteria describes the two outcomes of a bool question.
type ClassifierBoolCriteria struct {
	True  string
	False string
}

// ClassifierBoolQuestion asks a yes/no question.
type ClassifierBoolQuestion struct {
	Instructions string
	Criteria     ClassifierBoolCriteria
}

func (ClassifierChoiceQuestion) classifierQuestion() {}
func (ClassifierScoreQuestion) classifierQuestion()  {}
func (ClassifierBoolQuestion) classifierQuestion()   {}

func (ClassifierChoiceQuestion) QuestionType() string { return "choice" }
func (ClassifierScoreQuestion) QuestionType() string  { return "score" }
func (ClassifierBoolQuestion) QuestionType() string   { return "bool" }

// ClassifierQuestionEntry is one named question.
type ClassifierQuestionEntry struct {
	ID       string
	Question ClassifierQuestion
}

// ClassifierQuestions keeps the questions of a request in insertion order.
type ClassifierQuestions []ClassifierQuestionEntry

// ClassifierContext is the input to a classification: the state to judge and the questions to answer about it.
type ClassifierContext struct {
	State JsonObject
	// Images are judged together with State. Only models whose Input includes "image" accept them; other models return an error result (types.ts:669-677).
	Images    []ImageContent
	Questions ClassifierQuestions
}

// ClassifierProbability is the probability of one option of a choice answer.
type ClassifierProbability struct {
	Key         string
	Probability float64
}

// ClassifierAnswer is the closed union of classifier answers: ClassifierChoiceAnswer, ClassifierScoreAnswer and
// ClassifierBoolAnswer.
type ClassifierAnswer interface {
	classifierAnswer()
	// AnswerType is the wire `type` of the answer: "choice", "score" or "bool".
	AnswerType() string
}

// ClassifierChoiceAnswer is the answer to a choice question.
type ClassifierChoiceAnswer struct {
	Choice        string
	Probabilities []ClassifierProbability
	Confidence    float64
}

// ClassifierScoreAnswer is the answer to a score question.
type ClassifierScoreAnswer struct {
	Score      float64
	Confidence float64
}

// ClassifierBoolAnswer is the answer to a bool question.
type ClassifierBoolAnswer struct {
	Probability float64
}

func (ClassifierChoiceAnswer) classifierAnswer() {}
func (ClassifierScoreAnswer) classifierAnswer()  {}
func (ClassifierBoolAnswer) classifierAnswer()   {}

func (ClassifierChoiceAnswer) AnswerType() string { return "choice" }
func (ClassifierScoreAnswer) AnswerType() string  { return "score" }
func (ClassifierBoolAnswer) AnswerType() string   { return "bool" }

// ClassifierAnswerEntry is the answer to one named question.
type ClassifierAnswerEntry struct {
	ID     string
	Answer ClassifierAnswer
}

// ClassifierAnswers keeps the answers of a result in the order the service returned them.
type ClassifierAnswers []ClassifierAnswerEntry

// ClassifierStopReason is why a classification ended.
type ClassifierStopReason string

const (
	ClassifierStopReasonStop    ClassifierStopReason = "stop"
	ClassifierStopReasonError   ClassifierStopReason = "error"
	ClassifierStopReasonAborted ClassifierStopReason = "aborted"
)

// ClassifierResult is the final result of a classification. Failures are results with StopReason error or aborted.
type ClassifierResult struct {
	API      ClassifierAPI
	Provider string
	Model    string
	Answers  ClassifierAnswers
	// Usage is the token usage priced at the model's catalog price, when the service reports token counts.
	Usage        *Usage
	StopReason   ClassifierStopReason
	ErrorMessage string
	Timestamp    int64
}

// ClassifierOptions configures classifier provider requests. Cancellation comes from the request context.
type ClassifierOptions struct {
	// TelemetryContext parents provider request spans; nil means no recording backend.
	TelemetryContext telemetry.TelemetryContext
	// Fetch replaces HTTP execution without changing the caller's request context or redirect policy.
	Fetch  *http.Client
	APIKey string
	// APIKeySet distinguishes an explicit empty key from an omitted override.
	APIKeySet bool
	Headers   ProviderHeaders
	Env       ProviderEnv
	// TimeoutMs bounds each HTTP attempt. Nil means no timeout.
	TimeoutMs *int
	// MaxRetries is the retry budget after a retryable failure. Nil means 2.
	MaxRetries *int
	// MaxRetryDelayMs caps a server-requested retry delay. Nil means 60000; 0 disables the cap.
	MaxRetryDelayMs *int
	// OnPayload inspects or replaces the request payload before it is sent. Returning false keeps the payload.
	OnPayload  func(payload any, model ClassifierModel) (any, bool, error)
	OnResponse func(response ProviderResponse, model ClassifierModel) error
	// Temperature divides the answer logits before they are normalized into probabilities. It must be positive; APIs
	// that cannot apply it ignore it.
	Temperature *float64
}

// ClassifierFunction classifies with one classifier API. It reports failures in the result.
type ClassifierFunction func(context.Context, ClassifierModel, ClassifierContext, ClassifierOptions) ClassifierResult

// ProviderClassifier is a classifier API module.
type ProviderClassifier struct {
	Classify func(context.Context, *ClassifierModel, ClassifierContext, ClassifierOptions) (ClassifierResult, error)
}

// ProviderClassifierMap keys classifier implementations by ClassifierModel.API.
type ProviderClassifierMap map[ClassifierAPI]*ProviderClassifier

// ModelsClassifierOptions is ClassifierOptions plus the per-request header transform Models applies.
type ModelsClassifierOptions struct {
	ClassifierOptions
	// TransformHeaders runs once after configured, auth, and request headers are merged.
	TransformHeaders func(context.Context, ProviderHeaders) (ProviderHeaders, error)
}

func classifierErrorResult(model *ClassifierModel, err error, aborted bool) ClassifierResult {
	reason := ClassifierStopReasonError
	if aborted {
		reason = ClassifierStopReasonAborted
	}
	return ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, StopReason: reason, ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli()}
}

// Classify classifies structured state through the owning provider with auth resolved like Stream. It never returns an
// error: failures and cancellation are error or aborted results.
// Ports Models.classify (.upstream/v0.99.1/packages/ai/src/models.ts:966).
func (m *Models) Classify(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ...ModelsClassifierOptions) ClassifierResult {
	var opts ModelsClassifierOptions
	if len(options) > 0 {
		opts = options[0]
	}
	result, err := m.classify(ctx, model, request, opts)
	if err != nil {
		return classifierErrorResult(model, err, ctx.Err() != nil)
	}
	return result
}

func (m *Models) classify(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ModelsClassifierOptions) (ClassifierResult, error) {
	if err := AssertClassifierInputSupported(model, request); err != nil {
		return ClassifierResult{}, err
	}
	provider, err := m.requireProvider(model)
	if err != nil {
		return ClassifierResult{}, err
	}
	if provider.Classify == nil {
		return ClassifierResult{}, NewModelsError(ModelsErrorProvider, "Provider "+model.Provider+" does not support classification", nil)
	}
	auth, err := m.resolveRequestAuth(ctx, model, options.APIKey, options.APIKeySet, options.Headers, options.Env, options.TransformHeaders)
	if err != nil {
		return ClassifierResult{}, err
	}
	requestModel := model
	if auth.baseURL != "" {
		requestModel = new(*model)
		requestModel.BaseURL = auth.baseURL
	}
	requestOptions := options.ClassifierOptions
	requestOptions.APIKey, requestOptions.Headers, requestOptions.Env = auth.apiKey, auth.headers, auth.env
	return provider.Classify(ctx, requestModel, request, requestOptions)
}

// jsObjectKeyOrder orders the keys of a JavaScript object built from an entry list: array-index keys first in ascending
// order, then the other keys in insertion order.
func jsObjectKeyOrder(keys []string) []int {
	var indexes, others []int
	for i, key := range keys {
		if _, ok := arrayIndexKey(key); ok {
			indexes = append(indexes, i)
		} else {
			others = append(others, i)
		}
	}
	slices.SortStableFunc(indexes, func(a, b int) int {
		x, _ := arrayIndexKey(keys[a])
		y, _ := arrayIndexKey(keys[b])
		return cmp.Compare(x, y)
	})
	return append(indexes, others...)
}

// arrayIndexKey reports whether key is a canonical array index (an integer from 0 to 2^32-2 without leading zeros).
func arrayIndexKey(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	value, err := strconv.ParseUint(key, 10, 64)
	if err != nil || value >= 1<<32-1 || strconv.FormatUint(value, 10) != key {
		return 0, false
	}
	return value, true
}

// marshalJSONString is JSON.stringify(value) for a string.
func marshalJSONString(value string) []byte {
	encoded, _ := jsstring.MarshalJSON(value)
	return encoded
}

// marshalJSONValue is JSON.stringify(value): negative zero is 0 and the line and paragraph separators are written literally.
func marshalJSONValue(value any) ([]byte, error) {
	return jsstring.MarshalJSON(value)
}

// writeJSONObject writes ordered entries as one JSON object.
func writeJSONObject(count int, key func(int) string, value func(int) ([]byte, error)) ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i := range count {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(marshalJSONString(key(i)))
		out.WriteByte(':')
		encoded, err := value(i)
		if err != nil {
			return nil, err
		}
		out.Write(encoded)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func (q ClassifierChoiceQuestion) MarshalJSON() ([]byte, error) {
	criteria, err := writeJSONObject(len(q.Criteria), func(i int) string { return q.Criteria[i].Key }, func(i int) ([]byte, error) { return marshalJSONValue(q.Criteria[i].Description) })
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"type":"choice","instructions":%s,"criteria":%s}`, marshalJSONString(q.Instructions), criteria), nil
}

func (q ClassifierScoreQuestion) MarshalJSON() ([]byte, error) {
	criteria, err := marshalJSONValue(append([]string{}, q.Criteria...))
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"type":"score","instructions":%s,"criteria":%s}`, marshalJSONString(q.Instructions), criteria), nil
}

func (q ClassifierBoolQuestion) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `{"type":"bool","instructions":%s,"criteria":{"true":%s,"false":%s}}`, marshalJSONString(q.Instructions), marshalJSONString(q.Criteria.True), marshalJSONString(q.Criteria.False)), nil
}

// MarshalJSON writes the questions as an object keyed by question ID, in order.
func (q ClassifierQuestions) MarshalJSON() ([]byte, error) {
	return writeJSONObject(len(q), func(i int) string { return q[i].ID }, func(i int) ([]byte, error) { return json.Marshal(q[i].Question) })
}

// UnmarshalJSON reads an object keyed by question ID, keeping the order of the keys.
func (q *ClassifierQuestions) UnmarshalJSON(data []byte) error {
	entries, err := orderedJSONObject(data)
	if err != nil {
		return err
	}
	out := make(ClassifierQuestions, 0, len(entries))
	for _, entry := range entries {
		question, err := unmarshalClassifierQuestion(entry.value)
		if err != nil {
			return fmt.Errorf("question %q: %w", entry.key, err)
		}
		out = append(out, ClassifierQuestionEntry{ID: entry.key, Question: question})
	}
	*q = out
	return nil
}

func unmarshalClassifierQuestion(data []byte) (ClassifierQuestion, error) {
	var head struct {
		Type         string          `json:"type"`
		Instructions string          `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	switch head.Type {
	case "choice":
		entries, err := orderedJSONObject(head.Criteria)
		if err != nil {
			return nil, err
		}
		criteria := make([]ClassifierChoiceCriterion, 0, len(entries))
		for _, entry := range entries {
			var description string
			if err := json.Unmarshal(entry.value, &description); err != nil {
				return nil, err
			}
			criteria = append(criteria, ClassifierChoiceCriterion{Key: entry.key, Description: description})
		}
		return ClassifierChoiceQuestion{Instructions: head.Instructions, Criteria: criteria}, nil
	case "score":
		var criteria []string
		if err := json.Unmarshal(head.Criteria, &criteria); err != nil {
			return nil, err
		}
		return ClassifierScoreQuestion{Instructions: head.Instructions, Criteria: criteria}, nil
	case "bool":
		var criteria struct {
			True  string `json:"true"`
			False string `json:"false"`
		}
		if err := json.Unmarshal(head.Criteria, &criteria); err != nil {
			return nil, err
		}
		return ClassifierBoolQuestion{Instructions: head.Instructions, Criteria: ClassifierBoolCriteria{True: criteria.True, False: criteria.False}}, nil
	}
	return nil, fmt.Errorf("unknown question type %q", head.Type)
}

func (a ClassifierChoiceAnswer) MarshalJSON() ([]byte, error) {
	probabilities, err := writeJSONObject(len(a.Probabilities), func(i int) string { return a.Probabilities[i].Key }, func(i int) ([]byte, error) { return marshalJSONValue(a.Probabilities[i].Probability) })
	if err != nil {
		return nil, err
	}
	confidence, err := marshalJSONValue(a.Confidence)
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"type":"choice","choice":%s,"probabilities":%s,"confidence":%s}`, marshalJSONString(a.Choice), probabilities, confidence), nil
}

func (a ClassifierScoreAnswer) MarshalJSON() ([]byte, error) {
	score, err := marshalJSONValue(a.Score)
	if err != nil {
		return nil, err
	}
	confidence, err := marshalJSONValue(a.Confidence)
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"type":"score","score":%s,"confidence":%s}`, score, confidence), nil
}

func (a ClassifierBoolAnswer) MarshalJSON() ([]byte, error) {
	probability, err := marshalJSONValue(a.Probability)
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"type":"bool","probability":%s}`, probability), nil
}

// MarshalJSON writes the answers as an object keyed by question ID, in order.
func (a ClassifierAnswers) MarshalJSON() ([]byte, error) {
	return writeJSONObject(len(a), func(i int) string { return a[i].ID }, func(i int) ([]byte, error) { return json.Marshal(a[i].Answer) })
}
