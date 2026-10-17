package ai

import (
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type oracleCauseError struct{ name, message string }

func (e oracleCauseError) Error() string { return e.message }
func (e oracleCauseError) Name() string  { return e.name }

// packages/ai/src/utils/models-error.ts ModelsError/withCauseDetail against pinned Pi: the message of a ModelsError built with an Error cause (any message, name, or both empty), including the detail the cause contributes after JavaScript trim and the substring test that keeps a message already holding it.
func TestModelsErrorMessageMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261114))
	messages := []string{"", " ", "load failed", "load failed: x", "  pad  ", "界🙂", "Error", "a\u2028b", "\ufeffbom", "\u180evowel", "x\u00a0", "TypeError: boom"}
	names := []string{"Error", "TypeError", "", "ModelsError", "Custom"}
	codes := []string{"model_source", "provider", "auth"}
	probes := make([]map[string]any, 3000)
	for i := range probes {
		probe := map[string]any{"code": codes[rng.Intn(len(codes))], "message": messages[rng.Intn(len(messages))]}
		if rng.Intn(5) != 0 {
			probe["cause"] = map[string]string{"name": names[rng.Intn(len(names))], "message": messages[rng.Intn(len(messages))]}
		}
		probes[i] = probe
	}
	var want []string
	pioracle.Run(t, `
const { ModelsError } = await load("pi-ai/utils/models-error.js");
emit(input.map((p) => {
	let cause;
	if (p.cause) { cause = new Error(p.cause.message); cause.name = p.cause.name; }
	return new ModelsError(p.code, p.message, cause === undefined ? undefined : { cause }).message;
}));`, probes, &want)
	failures := 0
	for i, probe := range probes {
		var cause error
		if c, ok := probe["cause"].(map[string]string); ok {
			cause = oracleCauseError{name: c["name"], message: c["message"]}
		}
		if got := NewModelsError(ModelsErrorCode(probe["code"].(string)), probe["message"].(string), cause).Message; got != want[i] {
			failures++
			if failures <= 5 {
				t.Errorf("probe %d %+v: message %q, Pi %q", i, probe, got, want[i])
			}
		}
	}
	t.Logf("%d probes, %d failures", len(probes), failures)
}
