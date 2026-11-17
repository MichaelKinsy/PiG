package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// modelAfterReplacementRecord is one line of the log testdata/model-after-new.mjs writes: a session_start, or a model_select with the model the event carried.
type modelAfterReplacementRecord struct {
	Event    string                       `json:"event"`
	Reason   string                       `json:"reason"`
	Source   string                       `json:"source"`
	Thinking string                       `json:"thinking"`
	Model    *modelAfterReplacementFields `json:"model"`
}

type modelAfterReplacementFields struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	ContextWindow int                `json:"contextWindow"`
	MaxTokens     int                `json:"maxTokens"`
	Reasoning     bool               `json:"reasoning"`
	Input         []string           `json:"input"`
	Cost          map[string]float64 `json:"cost"`
}

// registeredCost is the cost the fixture registers for every model; no default carries these numbers.
var registeredCost = map[string]float64{"input": 3.25, "output": 16.5, "cacheRead": 0.325, "cacheWrite": 4.0625}

// registeredModels is what testdata/model-after-new.mjs registers, keyed by the id a model_select event reports.
var registeredModels = map[string]modelAfterReplacementFields{
	"metaprov/model-a": {ID: "metaprov/model-a", Name: "Model A", ContextWindow: 777000, MaxTokens: 55555, Reasoning: true, Input: []string{"text", "image"}, Cost: registeredCost},
	"metaprov/model-b": {ID: "metaprov/model-b", Name: "Model B", ContextWindow: 555000, MaxTokens: 44444, Reasoning: true, Input: []string{"text"}, Cost: registeredCost},
}

func readModelAfterReplacementRecords(t *testing.T, path string) []modelAfterReplacementRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var records []modelAfterReplacementRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record modelAfterReplacementRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("fixture log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func marshalModelFields(t *testing.T, fields modelAfterReplacementFields) string {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
