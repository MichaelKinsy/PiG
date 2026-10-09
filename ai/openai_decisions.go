package ai

// Ports packages/ai/src/api/openai-decisions.ts and packages/ai/src/api/openai-decisions.lazy.ts.
//
// OpenAI's Decisions API is POST /v1/decisions with { model, input, questions }
// (https://developers.openai.com/api/docs/guides/decisions). The state is sent as JSON text. With images, the input
// becomes one user message with the state as input_text followed by input_image data URLs. Questions map to Decisions
// types: choice to choice, score to score and bool to predicate. Predicates have no criteria field, so the meanings of
// true and false are appended to the instructions. Only OpenAI API keys work: Sign in with ChatGPT tokens are rejected on
// this route.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const openAIDecisionsLabel = "OpenAI Decisions"

// openAIDecisionsMaxImages is the number of image parts the endpoint accepts in one request.
const openAIDecisionsMaxImages = 128

// openAIDecisionsNoRetryStatuses is Cloudflare in front of api.openai.com answering 504 with an HTML page when a request
// runs longer than about five seconds. Large inputs, currently above roughly 600K tokens, hit this limit, and retrying
// the same input runs into it again, so 504 is not retried.
var openAIDecisionsNoRetryStatuses = []int{http.StatusGatewayTimeout}

type openAIDecisionChoice struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type openAIDecisionLevel struct {
	Label string `json:"label"`
}

type openAIChoiceDecision struct {
	Type         string                 `json:"type"`
	Name         string                 `json:"name"`
	Instructions string                 `json:"instructions"`
	Choices      []openAIDecisionChoice `json:"choices"`
}

type openAIScoreDecision struct {
	Type         string                `json:"type"`
	Name         string                `json:"name"`
	Instructions string                `json:"instructions"`
	Levels       []openAIDecisionLevel `json:"levels"`
}

type openAIPredicateDecision struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
}

func openAIPredicateInstructions(question ClassifierBoolQuestion) string {
	var meanings []string
	if question.Criteria.True != "" {
		meanings = append(meanings, "True means: "+question.Criteria.True)
	}
	if question.Criteria.False != "" {
		meanings = append(meanings, "False means: "+question.Criteria.False)
	}
	if len(meanings) == 0 {
		return question.Instructions
	}
	return question.Instructions + "\n\n" + strings.Join(meanings, "\n")
}

func openAIDecisionWireQuestion(name string, question ClassifierQuestion) any {
	switch question := question.(type) {
	case ClassifierChoiceQuestion:
		choices := make([]openAIDecisionChoice, 0, len(question.Criteria))
		for _, criterion := range orderedChoiceCriteria(question.Criteria) {
			choices = append(choices, openAIDecisionChoice{Value: criterion.Key, Description: criterion.Description})
		}
		return openAIChoiceDecision{Type: "choice", Name: name, Instructions: question.Instructions, Choices: choices}
	case ClassifierScoreQuestion:
		levels := make([]openAIDecisionLevel, len(question.Criteria))
		for i, label := range question.Criteria {
			levels[i] = openAIDecisionLevel{Label: label}
		}
		return openAIScoreDecision{Type: "score", Name: name, Instructions: question.Instructions, Levels: levels}
	}
	return openAIPredicateDecision{Type: "predicate", Name: name, Instructions: openAIPredicateInstructions(question.(ClassifierBoolQuestion))}
}

// openAIDecisionsInput is the request input: the state as JSON text, or one user message carrying the state and images.
func openAIDecisionsInput(request ClassifierContext) (any, error) {
	state := request.State
	if state == nil {
		state = JsonObject{}
	}
	encoded, err := marshalJSONValue(state)
	if err != nil {
		return nil, err
	}
	if len(request.Images) == 0 {
		return string(encoded), nil
	}
	if len(request.Images) > openAIDecisionsMaxImages {
		return nil, fmt.Errorf("%s accepts at most %d images, got %d", openAIDecisionsLabel, openAIDecisionsMaxImages, len(request.Images))
	}
	content := []map[string]string{{"type": "input_text", "text": string(encoded)}}
	for _, image := range request.Images {
		content = append(content, map[string]string{"type": "input_image", "image_url": "data:" + image.MimeType + ";base64," + image.Data})
	}
	return []map[string]any{{"role": "user", "content": content}}, nil
}

func openAIDecisionsProbabilities(raw json.RawMessage, id string) ([]ClassifierProbability, error) {
	invalid := fmt.Errorf("%s returned invalid probabilities for %s", openAIDecisionsLabel, id)
	entries, ok := jsonArrayOf(raw)
	if !ok {
		return nil, invalid
	}
	// Object.fromEntries: a repeated value keeps its first position and takes the last probability.
	var probabilities []ClassifierProbability
	for _, entry := range entries {
		object, ok := jsonObjectOf(entry)
		if !ok {
			return nil, invalid
		}
		value, ok := jsonStringOf(object["value"])
		if !ok {
			return nil, invalid
		}
		probability, err := RequiredNumber(openAIDecisionsLabel, object["probability"], fmt.Sprintf("probability for %s.%s", id, value))
		if err != nil {
			return nil, err
		}
		replaced := false
		for i := range probabilities {
			if probabilities[i].Key == value {
				probabilities[i].Probability, replaced = probability, true
			}
		}
		if !replaced {
			probabilities = append(probabilities, ClassifierProbability{Key: value, Probability: probability})
		}
	}
	return orderProbabilities(probabilities), nil
}

func parseOpenAIDecisionsAnswer(id string, question ClassifierQuestion, answer map[string]json.RawMessage) (ClassifierAnswer, error) {
	answerType, _ := jsonStringOf(answer["type"])
	if answerType == "refusal" {
		return nil, fmt.Errorf("%s refused to answer %s", openAIDecisionsLabel, id)
	}
	switch question.(type) {
	case ClassifierChoiceQuestion:
		choice, isString := jsonStringOf(answer["choice"])
		if answerType != "choice" || !isString {
			return nil, fmt.Errorf("%s did not return a choice answer for %s", openAIDecisionsLabel, id)
		}
		probabilities, err := openAIDecisionsProbabilities(answer["probabilities"], id)
		if err != nil {
			return nil, err
		}
		confidence, err := RequiredNumber(openAIDecisionsLabel, answer["confidence"], "confidence for "+id)
		if err != nil {
			return nil, err
		}
		return ClassifierChoiceAnswer{Choice: choice, Probabilities: probabilities, Confidence: confidence}, nil
	case ClassifierScoreQuestion:
		if answerType != "score" {
			return nil, fmt.Errorf("%s did not return a score answer for %s", openAIDecisionsLabel, id)
		}
		score, err := RequiredNumber(openAIDecisionsLabel, answer["score"], "score for "+id)
		if err != nil {
			return nil, err
		}
		confidence, err := RequiredNumber(openAIDecisionsLabel, answer["confidence"], "confidence for "+id)
		if err != nil {
			return nil, err
		}
		return ClassifierScoreAnswer{Score: score, Confidence: confidence}, nil
	}
	if answerType != "predicate" {
		return nil, fmt.Errorf("%s did not return a predicate answer for %s", openAIDecisionsLabel, id)
	}
	probability, err := RequiredNumber(openAIDecisionsLabel, answer["probability"], "probability for "+id)
	if err != nil {
		return nil, err
	}
	return ClassifierBoolAnswer{Probability: probability}, nil
}

func parseOpenAIDecisionsAnswers(raw json.RawMessage, request ClassifierContext) (ClassifierAnswers, error) {
	values, ok := jsonArrayOf(raw)
	if !ok {
		return nil, fmt.Errorf("%s returned an unexpected response", openAIDecisionsLabel)
	}
	byName := map[string]map[string]json.RawMessage{}
	for _, value := range values {
		if answer, ok := jsonObjectOf(value); ok {
			if name, ok := jsonStringOf(answer["name"]); ok {
				byName[name] = answer
			}
		}
	}
	answers := make(ClassifierAnswers, 0, len(request.Questions))
	for _, entry := range request.Questions.inJSOrder() {
		answer, ok := byName[entry.ID]
		if !ok {
			return nil, fmt.Errorf("%s did not return an answer for %s", openAIDecisionsLabel, entry.ID)
		}
		parsed, err := parseOpenAIDecisionsAnswer(entry.ID, entry.Question, answer)
		if err != nil {
			return nil, err
		}
		answers = append(answers, ClassifierAnswerEntry{ID: entry.ID, Answer: parsed})
	}
	return orderAnswers(answers), nil
}

func openAIDecisionsErrorMessage(err error) string {
	if request, ok := errors.AsType[*ClassifierHTTPError](err); ok && request.Status != nil && *request.Status == http.StatusGatewayTimeout {
		return openAIDecisionsLabel + " error (504): the request timed out at the gateway. Very large inputs (above roughly 600K tokens) currently exceed its time limit."
	}
	return classifierErrorMessage(err, openAIDecisionsLabel)
}

// ClassifyOpenAIDecisions classifies through OpenAI's Decisions API.
func ClassifyOpenAIDecisions(ctx context.Context, model ClassifierModel, request ClassifierContext, options ClassifierOptions) ClassifierResult {
	output := ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, Answers: ClassifierAnswers{}, StopReason: ClassifierStopReasonStop, Timestamp: time.Now().UnixMilli()}
	fail := func(err error) ClassifierResult {
		output.Answers = ClassifierAnswers{}
		output.StopReason = ClassifierStopReasonError
		if ctx.Err() != nil {
			output.StopReason = ClassifierStopReasonAborted
		}
		output.ErrorMessage = openAIDecisionsErrorMessage(err)
		return output
	}
	if model.API != ClassifierAPIOpenAIDecisions {
		return fail(fmt.Errorf("Unsupported classifier API: %s", model.API))
	}
	input, err := openAIDecisionsInput(request)
	if err != nil {
		return fail(err)
	}
	questions := make([]any, 0, len(request.Questions))
	for _, entry := range request.Questions.inJSOrder() {
		questions = append(questions, openAIDecisionWireQuestion(entry.ID, entry.Question))
	}
	payload := struct {
		Model     string `json:"model"`
		Input     any    `json:"input"`
		Questions []any  `json:"questions"`
	}{model.ID, input, questions}
	body, err := PostClassifierRequest(ctx, openAIDecisionsLabel, serviceURL(model.BaseURL, "decisions"), model, payload, options, openAIDecisionsNoRetryStatuses)
	if err != nil {
		return fail(err)
	}
	response, ok := jsonObjectOf(body)
	if !ok {
		return fail(fmt.Errorf("%s returned an unexpected response", openAIDecisionsLabel))
	}
	// Set before parsing answers: a request with malformed or refused answers was still billed.
	output.Usage = ParseClassifierUsage(response["usage"], model)
	answers, err := parseOpenAIDecisionsAnswers(response["answers"], request)
	if err != nil {
		return fail(err)
	}
	output.Answers = answers
	return output
}

// OpenAIDecisionsAPI is the openai-decisions classifier module.
func OpenAIDecisionsAPI() *ProviderClassifier { return classifierModule(ClassifyOpenAIDecisions) }
