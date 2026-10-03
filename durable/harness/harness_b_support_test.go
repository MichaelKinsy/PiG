// Shared helpers of the d-harness-b test files (generation, inbox, submissions, compaction, live deltas, scheduler).

package harness

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// fauxAnswer is pi-ai fauxAssistantMessage(text) as a faux response step.
func fauxAnswer(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}

// fauxAfter is an async faux response that answers text once gate is resolved.
func fauxAfter(gate *deferredGate, text string) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
		if err := gate.wait(options.Signal); err != nil {
			return ai.FauxResponse{}, err
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}, nil
	})
}

// commitValue runs a typed commit on a conversation and fails the test on error.
func commitValue[T any](t *testing.T, conversation Conversation, change func(tx durable.Tx) (T, error)) T {
	t.Helper()
	value, err := durable.Commit(testContext, conversation, change)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// must returns value, panicking on err; the panic fails the running test with its stack.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// closeHarness closes the Harness and fails the test on error.
func closeHarness(t *testing.T, harness Harness) {
	t.Helper()
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// cancelledContext returns a context cancelled with cause.
func cancelledContext(cause error) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(testContext)
	return ctx, func() { cancel(cause) }
}

// expectEqualJSON fails unless got encodes to the same JSON value as want (object member order ignored), as toEqual does.
func expectEqualJSON(t *testing.T, got any, want string) {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	if err := json.Unmarshal(encoded, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &right); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("got  %s\nwant %s", encoded, want)
	}
}

// expectError fails unless err is non-nil and its message contains want.
func expectError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one containing %q", err, want)
	}
}

// expectLike fails unless got encodes to JSON matching want exactly, where the strings "$number", "$string", and
// "$object" in want match any value of that kind (expect.any(Number), expect.any(String), expect.any(Object)).
func expectLike(t *testing.T, got any, want string) {
	t.Helper()
	if !like(t, got, want) {
		t.Fatalf("got  %s\nwant %s", jsonText(t, got), want)
	}
}

// like reports whether got encodes to JSON matching the want pattern of expectLike.
func like(t *testing.T, got any, want string) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal([]byte(jsonText(t, got)), &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &right); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	return likeValue(left, right)
}

func likeValue(got, want any) bool {
	switch typed := want.(type) {
	case string:
		switch typed {
		case "$number":
			_, ok := got.(float64)
			return ok
		case "$string":
			_, ok := got.(string)
			return ok
		case "$object":
			_, ok := got.(map[string]any)
			return ok
		}
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok || len(object) != len(typed) {
			return false
		}
		for key, value := range typed {
			member, present := object[key]
			if !present || !likeValue(member, value) {
				return false
			}
		}
		return true
	case []any:
		array, ok := got.([]any)
		if !ok || len(array) != len(typed) {
			return false
		}
		for index := range typed {
			if !likeValue(array[index], typed[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}

// expectContainsLike fails unless every pattern of want (a JSON array) matches some member of got
// (expect.arrayContaining).
func expectContainsLike(t *testing.T, got any, want string) {
	t.Helper()
	var members, patterns []any
	if err := json.Unmarshal([]byte(jsonText(t, got)), &members); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &patterns); err != nil {
		t.Fatalf("want is not a JSON array: %v", err)
	}
	for _, pattern := range patterns {
		found := false
		for _, member := range members {
			if likeValue(member, pattern) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("got  %s\nwant it to contain %s", jsonText(t, got), jsonText(t, pattern))
		}
	}
}

// toolsNamed returns the installed tools with these names, as Configure takes them (chat-support.ts toolsNamed).
func toolsNamed(t *testing.T, setup *chatState, names ...string) []*durable.ToolRegistration {
	t.Helper()
	installed := setup.Registry.Snapshot().Tools()
	tools := []*durable.ToolRegistration{}
	for _, name := range names {
		index := slices.IndexFunc(installed, func(entry durable.RegistryTool) bool { return entry.Tool.Name == name })
		if index < 0 {
			t.Fatalf("Tool %s is not installed", name)
		}
		tools = append(tools, installed[index].Tool)
	}
	return tools
}
