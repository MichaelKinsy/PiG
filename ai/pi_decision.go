package ai

// Ports packages/ai/src/api/pi-decision.ts (Pi c2f3a5cea, 9553d5834, 604359311, bff71c709).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

const piDecisionLabel = "Pi Decision"

// PiDecisionOptions configures a pi-decision classification request.
type PiDecisionOptions struct {
	ClassifierOptions
	// SessionID is an optional session identifier for gateway correlation/analytics.
	SessionID string
}

// PiDecisionRequestOptions is the nested options object on the wire.
type PiDecisionRequestOptions struct {
	SessionID string `json:"sessionId,omitempty"`
}

// PiDecisionRequest is the wire request body sent by pi-decision to POST <baseUrl>/decision.
type PiDecisionRequest struct {
	Model   string                    `json:"model"`
	Context ClassifierContext         `json:"context"`
	Options *PiDecisionRequestOptions `json:"options,omitempty"`
}

// PiDecisionResponse is the wire response body returned by a pi-decision backend on 200 OK.
type PiDecisionResponse struct {
	Answers    ClassifierAnswers `json:"answers"`
	Usage      *Usage            `json:"usage,omitempty"`
	ResponseID string            `json:"responseId,omitempty"`
}

func piDecisionFiniteNonNegative(raw json.RawMessage) float64 {
	var v float64
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

func piDecisionIsFiniteNumber(raw json.RawMessage) (float64, bool) {
	var v float64
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func piDecisionRequiredUnitInterval(raw json.RawMessage, field string) (float64, error) {
	num, err := RequiredNumber(piDecisionLabel, raw, field)
	if err != nil {
		return 0, err
	}
	if num < 0 || num > 1 {
		return 0, fmt.Errorf("%s returned out-of-range %s: %g", piDecisionLabel, field, num)
	}
	return num, nil
}

func parsePiDecisionUsage(raw json.RawMessage, model ClassifierModel) *Usage {
	obj, ok := jsonObjectOf(raw)
	if !ok {
		return nil
	}
	_, hasInput := piDecisionIsFiniteNumber(obj["input"])
	_, hasOutput := piDecisionIsFiniteNumber(obj["output"])
	if !hasInput && !hasOutput {
		return nil
	}
	input := piDecisionFiniteNonNegative(obj["input"])
	output := piDecisionFiniteNonNegative(obj["output"])
	cacheRead := piDecisionFiniteNonNegative(obj["cacheRead"])
	cacheWrite := piDecisionFiniteNonNegative(obj["cacheWrite"])
	totalTokens := input + output + cacheRead + cacheWrite
	if t, ok := piDecisionIsFiniteNumber(obj["totalTokens"]); ok && t >= 0 {
		totalTokens = t
	}
	usage := &Usage{
		Input:       int(input),
		Output:      int(output),
		CacheRead:   int(cacheRead),
		CacheWrite:  int(cacheWrite),
		TotalTokens: int(totalTokens),
	}
	if costObj, ok := jsonObjectOf(obj["cost"]); ok {
		if total, ok := piDecisionIsFiniteNumber(costObj["total"]); ok {
			usage.Cost = UsageCost{
				Input:      piDecisionFiniteNonNegative(costObj["input"]),
				Output:     piDecisionFiniteNonNegative(costObj["output"]),
				CacheRead:  piDecisionFiniteNonNegative(costObj["cacheRead"]),
				CacheWrite: piDecisionFiniteNonNegative(costObj["cacheWrite"]),
				Total:      math.Max(0, total),
			}
			var fromProvider bool
			if len(costObj["totalFromProvider"]) > 0 && json.Unmarshal(costObj["totalFromProvider"], &fromProvider) == nil {
				usage.Cost.TotalFromProvider = &fromProvider
			}
			return usage
		}
	}
	CalculateCost(&model, usage)
	return usage
}

func parsePiDecisionProbabilities(raw json.RawMessage, id string, criteria []ClassifierChoiceCriterion) ([]ClassifierProbability, error) {
	entries, err := orderedJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", piDecisionLabel, id)
	}
	allowed := make(map[string]struct{}, len(criteria))
	for _, c := range criteria {
		allowed[c.Key] = struct{}{}
	}
	probs := make([]ClassifierProbability, 0, len(entries))
	for _, entry := range entries {
		if _, ok := allowed[entry.key]; !ok {
			return nil, fmt.Errorf("%s returned unknown choice %q in probabilities for %s", piDecisionLabel, entry.key, id)
		}
		p, err := piDecisionRequiredUnitInterval(entry.value, fmt.Sprintf("probability for %s.%s", id, entry.key))
		if err != nil {
			return nil, err
		}
		probs = append(probs, ClassifierProbability{Key: entry.key, Probability: p})
	}
	return probs, nil
}

func parsePiDecisionAnswer(id string, question ClassifierQuestion, raw json.RawMessage) (ClassifierAnswer, error) {
	obj, ok := jsonObjectOf(raw)
	if !ok {
		return nil, fmt.Errorf("%s returned an invalid answer for %s", piDecisionLabel, id)
	}
	var answerType string
	_ = json.Unmarshal(obj["type"], &answerType)
	switch q := question.(type) {
	case ClassifierChoiceQuestion:
		var choice string
		if answerType != "choice" || json.Unmarshal(obj["choice"], &choice) != nil {
			return nil, fmt.Errorf("%s did not return a choice answer for %s", piDecisionLabel, id)
		}
		validChoice := false
		for _, c := range q.Criteria {
			if c.Key == choice {
				validChoice = true
				break
			}
		}
		if !validChoice {
			return nil, fmt.Errorf("%s returned unknown choice %q for %s", piDecisionLabel, choice, id)
		}
		probs, err := parsePiDecisionProbabilities(obj["probabilities"], id, q.Criteria)
		if err != nil {
			return nil, err
		}
		conf, err := piDecisionRequiredUnitInterval(obj["confidence"], "confidence for "+id)
		if err != nil {
			return nil, err
		}
		return ClassifierChoiceAnswer{Choice: choice, Probabilities: probs, Confidence: conf}, nil
	case ClassifierScoreQuestion:
		if answerType != "score" {
			return nil, fmt.Errorf("%s did not return a score answer for %s", piDecisionLabel, id)
		}
		score, err := RequiredNumber(piDecisionLabel, obj["score"], "score for "+id)
		if err != nil {
			return nil, err
		}
		maxScore := float64(max(0, len(q.Criteria)-1))
		if score < 0 || score > maxScore {
			return nil, fmt.Errorf("%s returned out-of-range score for %s: %g", piDecisionLabel, id, score)
		}
		conf, err := piDecisionRequiredUnitInterval(obj["confidence"], "confidence for "+id)
		if err != nil {
			return nil, err
		}
		return ClassifierScoreAnswer{Score: score, Confidence: conf}, nil
	default:
		if answerType != "bool" {
			return nil, fmt.Errorf("%s did not return a bool answer for %s", piDecisionLabel, id)
		}
		prob, err := piDecisionRequiredUnitInterval(obj["probability"], "probability for "+id)
		if err != nil {
			return nil, err
		}
		return ClassifierBoolAnswer{Probability: prob}, nil
	}
}

func parsePiDecisionAnswers(raw json.RawMessage, request ClassifierContext) (ClassifierAnswers, error) {
	obj, ok := jsonObjectOf(raw)
	if !ok {
		return nil, errors.New(piDecisionLabel + " returned an unexpected response")
	}
	answers := make(ClassifierAnswers, 0, len(request.Questions))
	for _, entry := range request.Questions {
		ansRaw, exists := obj[entry.ID]
		if !exists {
			return nil, fmt.Errorf("%s did not return an answer for %s", piDecisionLabel, entry.ID)
		}
		ans, err := parsePiDecisionAnswer(entry.ID, entry.Question, ansRaw)
		if err != nil {
			return nil, err
		}
		answers = append(answers, ClassifierAnswerEntry{ID: entry.ID, Answer: ans})
	}
	return answers, nil
}

// ClassifyPiDecision classifies through pi's canonical POST <baseUrl>/decision protocol.
func ClassifyPiDecision(ctx context.Context, model ClassifierModel, request ClassifierContext, options PiDecisionOptions) ClassifierResult {
	//portlint:allow clock pi-decision.ts:154 `timestamp: Date.now()`
	output := ClassifierResult{
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Answers:    ClassifierAnswers{},
		StopReason: ClassifierStopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
	fail := func(err error) ClassifierResult {
		output.Answers = ClassifierAnswers{}
		output.StopReason = ClassifierStopReasonError
		if ctx.Err() != nil {
			output.StopReason = ClassifierStopReasonAborted
		}
		output.ErrorMessage = classifierErrorMessage(err, piDecisionLabel)
		return output
	}
	if model.API != ClassifierAPIPiDecision {
		return fail(fmt.Errorf("Unsupported classifier API: %s", model.API))
	}
	if len(request.Images) > 0 && !slices.Contains(model.Input, "image") {
		return fail(fmt.Errorf("%s model %s does not support image input", piDecisionLabel, model.ID))
	}
	reqCtx := ClassifierContext{
		State:     request.State,
		Questions: request.Questions,
	}
	if len(request.Images) > 0 {
		reqCtx.Images = request.Images
	}
	reqBody := PiDecisionRequest{
		Model:   model.ID,
		Context: reqCtx,
	}
	if options.SessionID != "" {
		reqBody.Options = &PiDecisionRequestOptions{SessionID: options.SessionID}
	}
	target := serviceURL(model.BaseURL, "decision")
	envelope, err := PostClassifierRequest(ctx, piDecisionLabel, target, model, reqBody, options.ClassifierOptions, nil)
	if err != nil {
		return fail(err)
	}
	body, ok := jsonObjectOf(envelope)
	if !ok {
		return fail(errors.New(piDecisionLabel + " returned an unexpected response"))
	}
	if idRaw, ok := body["responseId"]; ok {
		var id string
		if json.Unmarshal(idRaw, &id) == nil && id != "" {
			output.ResponseID = id
		}
	}
	if usage := parsePiDecisionUsage(body["usage"], model); usage != nil {
		output.Usage = usage
	}
	answers, err := parsePiDecisionAnswers(body["answers"], request)
	if err != nil {
		return fail(err)
	}
	output.Answers = answers
	return output
}

// PiDecisionAPI returns the ProviderClassifier for the pi-decision API.
func PiDecisionAPI() *ProviderClassifier {
	return &ProviderClassifier{
		Classify: func(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
			return ClassifyPiDecision(ctx, *model, request, PiDecisionOptions{ClassifierOptions: options}), nil
		},
	}
}
