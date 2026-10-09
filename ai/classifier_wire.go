package ai

// JSON forms of the classifier and image request/result values as the extension wire and Pi's JSON carry them: a
// classifier context is {state, questions}, a result is {api, provider, model, answers, usage?, stopReason,
// errorMessage?, timestamp}, and an images context and result are {input} and {api, provider, model, output, ...}. The
// questions and answers objects keep the order of their entries (JavaScript object order).

import (
	"encoding/json"
	"fmt"
)

// MarshalJSON writes the context as {state, images?, questions} (types.ts:669-677 declaration order); images is omitted when absent.
func (c ClassifierContext) MarshalJSON() ([]byte, error) {
	state := c.State
	if state == nil {
		state = JsonObject{}
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	questions, err := c.Questions.MarshalJSON()
	if err != nil {
		return nil, err
	}
	if c.Images == nil {
		return fmt.Appendf(nil, `{"state":%s,"questions":%s}`, stateJSON, questions), nil
	}
	images, err := json.Marshal(c.Images)
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"state":%s,"images":%s,"questions":%s}`, stateJSON, images, questions), nil
}

// UnmarshalJSON reads {state, images?, questions}, keeping the order of the questions.
func (c *ClassifierContext) UnmarshalJSON(data []byte) error {
	var wire struct {
		State     JsonObject          `json:"state"`
		Images    []ImageContent      `json:"images"`
		Questions ClassifierQuestions `json:"questions"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*c = ClassifierContext{State: wire.State, Images: wire.Images, Questions: wire.Questions}
	return nil
}

// UnmarshalJSON reads an object keyed by question ID, keeping the order of the keys.
func (a *ClassifierAnswers) UnmarshalJSON(data []byte) error {
	entries, err := orderedJSONObject(data)
	if err != nil {
		return err
	}
	out := make(ClassifierAnswers, 0, len(entries))
	for _, entry := range entries {
		answer, err := unmarshalClassifierAnswer(entry.value)
		if err != nil {
			return fmt.Errorf("answer %q: %w", entry.key, err)
		}
		out = append(out, ClassifierAnswerEntry{ID: entry.key, Answer: answer})
	}
	*a = out
	return nil
}

func unmarshalClassifierAnswer(data []byte) (ClassifierAnswer, error) {
	var head struct {
		Type          string          `json:"type"`
		Choice        string          `json:"choice"`
		Probabilities json.RawMessage `json:"probabilities"`
		Score         float64         `json:"score"`
		Confidence    float64         `json:"confidence"`
		Probability   float64         `json:"probability"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	switch head.Type {
	case "choice":
		probabilities := []ClassifierProbability{}
		if len(head.Probabilities) > 0 {
			entries, err := orderedJSONObject(head.Probabilities)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				var probability float64
				if err := json.Unmarshal(entry.value, &probability); err != nil {
					return nil, err
				}
				probabilities = append(probabilities, ClassifierProbability{Key: entry.key, Probability: probability})
			}
		}
		return ClassifierChoiceAnswer{Choice: head.Choice, Probabilities: probabilities, Confidence: head.Confidence}, nil
	case "score":
		return ClassifierScoreAnswer{Score: head.Score, Confidence: head.Confidence}, nil
	case "bool":
		return ClassifierBoolAnswer{Probability: head.Probability}, nil
	}
	return nil, fmt.Errorf("unknown answer type %q", head.Type)
}

type classifierResultWire struct {
	API          ClassifierAPI        `json:"api"`
	Provider     string               `json:"provider"`
	Model        string               `json:"model"`
	Answers      ClassifierAnswers    `json:"answers"`
	Usage        *Usage               `json:"usage,omitempty"`
	StopReason   ClassifierStopReason `json:"stopReason"`
	ErrorMessage string               `json:"errorMessage,omitempty"`
	Timestamp    int64                `json:"timestamp"`
}

// MarshalJSON writes the result with its answers in order.
func (r ClassifierResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(classifierResultWire(r))
}

// UnmarshalJSON reads a result, keeping the order of its answers.
func (r *ClassifierResult) UnmarshalJSON(data []byte) error {
	var wire classifierResultWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*r = ClassifierResult(wire)
	return nil
}

// MarshalJSON writes the context as {input}.
func (c ImagesContext) MarshalJSON() ([]byte, error) {
	input := c.Input
	if input == nil {
		input = []ContentBlock{}
	}
	return json.Marshal(struct {
		Input []ContentBlock `json:"input"`
	}{input})
}

// UnmarshalJSON reads {input}.
func (c *ImagesContext) UnmarshalJSON(data []byte) error {
	var wire struct {
		Input ContentBlocks `json:"input"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*c = ImagesContext{Input: wire.Input}
	return nil
}

type assistantImagesWire struct {
	API          ImageAPI         `json:"api"`
	Provider     string           `json:"provider"`
	Model        string           `json:"model"`
	Output       []ContentBlock   `json:"output"`
	ResponseID   string           `json:"responseId,omitempty"`
	Usage        *Usage           `json:"usage,omitempty"`
	StopReason   ImagesStopReason `json:"stopReason"`
	ErrorMessage string           `json:"errorMessage,omitempty"`
	Timestamp    int64            `json:"timestamp"`
}

// MarshalJSON writes the result with its output blocks.
func (r AssistantImages) MarshalJSON() ([]byte, error) {
	wire := assistantImagesWire(r)
	if wire.Output == nil {
		wire.Output = []ContentBlock{}
	}
	return json.Marshal(wire)
}

// UnmarshalJSON reads a result.
func (r *AssistantImages) UnmarshalJSON(data []byte) error {
	var wire struct {
		assistantImagesWire
		Output ContentBlocks `json:"output"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*r = AssistantImages(wire.assistantImagesWire)
	r.Output = wire.Output
	return nil
}
