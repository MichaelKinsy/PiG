// Ports Pi 1.2.0 packages/durable/test/codemode-models.test.ts.

// pi: packages/durable/src/codemode/models.ts

package codemode

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

var modelsUsage = ai.Usage{Input: 10, Output: 2, TotalTokens: 12, Cost: ai.UsageCost{Input: 0.001, Output: 0.0002, Total: 0.0012}}

// modelsFixture is a chat whose models also have a classifier and an image model of a test provider, and the code mode
// extension.
type modelsFixture struct {
	setup *chat
	calls struct{ classify, images, inFlight, maxInFlight atomic.Int32 }
	// seen is the last classifier request as the provider got it: its state and its question ids.
	seen atomic.Value
	mu   sync.Mutex
	// open holds classifier calls until it is closed; atFour closes it a little after four run, so a fifth would show up.
	open   chan struct{}
	atFour bool
}

func modelsChat(t *testing.T) *modelsFixture {
	t.Helper()
	f := &modelsFixture{setup: chatSetup(), open: make(chan struct{})}
	close(f.open)
	judge := &ai.ClassifierModel{ID: "judge", Name: "Judge", API: "test-classifier", Provider: "test", BaseURL: "https://example.invalid", Headers: map[string]string{"authorization": "secret"}, Input: []string{"text"}, ContextWindow: 1000}
	painter := &ai.ImageModel{ID: "painter", Name: "Painter", API: "test-images", Provider: "test", BaseURL: "https://example.invalid", Input: []string{"text"}, Output: []string{"image"}}
	f.setup.models.SetProvider(ai.CreateProvider(ai.CreateProviderOptions{
		ID:     "test",
		Auth:   ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Test", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}},
		Models: []ai.AnyModel{judge, painter},
		Classifiers: ai.ProviderClassifierMap{"test-classifier": {Classify: func(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
			f.calls.classify.Add(1)
			f.probe(options.Telemetry, "test.classify")
			ids := []string{}
			for _, question := range request.Questions {
				ids = append(ids, question.ID)
			}
			stateJSON, _ := json.Marshal(request.State)
			f.seen.Store(seenRequest{state: string(stateJSON), questions: ids})
			result := ai.ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID}
			var state struct {
				Fail bool `json:"fail"`
			}
			encoded, _ := json.Marshal(request.State)
			_ = json.Unmarshal(encoded, &state)
			if state.Fail {
				result.StopReason, result.ErrorMessage = ai.ClassifierStopReasonError, "quota exceeded"
				return result, nil
			}
			inFlight := f.calls.inFlight.Add(1)
			f.calls.maxInFlight.Store(max(f.calls.maxInFlight.Load(), inFlight))
			f.mu.Lock()
			open, atFour := f.open, f.atFour
			f.mu.Unlock()
			if atFour && inFlight == 4 {
				time.AfterFunc(50*time.Millisecond, func() { f.release() })
			}
			select {
			case <-open:
			case <-ctx.Done():
			}
			f.calls.inFlight.Add(-1)
			usage := modelsUsage
			result.Answers = ai.ClassifierAnswers{{ID: "good", Answer: valueAnswer{}}}
			result.Usage, result.StopReason = &usage, ai.ClassifierStopReasonStop
			return result, nil
		}}},
		Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
			f.calls.images.Add(1)
			f.probe(options.Telemetry, "test.images")
			return ai.AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, Output: []ai.ImagesOutputContent{ai.ImageContent{Data: tinyPNG, MimeType: "image/png"}}, StopReason: ai.ImagesStopReasonStop}, nil
		}}},
	}))
	sandbox, err := cm.NewSandbox(cm.SandboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if err := f.setup.registry.Install(CreateExtension(ToolOptions{Sandbox: sandbox})); err != nil {
		t.Fatal(err)
	}
	return f
}

// probe records an event on the span a provider call is given, as a provider that reports into its caller's trace does.
func (f *modelsFixture) probe(span telemetry.TelemetrySpan, name string) {
	if span != nil {
		span.AddEvent(name, nil)
	}
}

// valueAnswer is the answer the Pi test's provider returns, `{ good: { value: true } }` cast past ClassifierAnswer: an
// answer of no ClassifierAnswer shape, which code mode hands to the script as the provider wrote it.
type valueAnswer struct{ ai.ClassifierBoolAnswer }

func (valueAnswer) MarshalJSON() ([]byte, error) { return []byte(`{"value":true}`), nil }

// hold makes the classifier calls wait for release; atFour: until a little after four run.
func (f *modelsFixture) hold(atFour bool) {
	f.mu.Lock()
	f.open, f.atFour = make(chan struct{}), atFour
	f.mu.Unlock()
}

func (f *modelsFixture) release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.open:
	default:
		close(f.open)
	}
}

type modelsRun struct {
	text    string
	content []ai.ToolResultMessageContent
	details ToolDetails
	usage   *harness.UsageState
	// calls are the stored arguments of the run's nested calls, as JSON text.
	calls []string
}

func (f *modelsFixture) run(t *testing.T, code string, environment ...env.ExecutionEnv) modelsRun {
	t.Helper()
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(code), done()})
	var opened harness.Harness
	var root harness.Conversation
	if len(environment) > 0 {
		opened, root = f.setup.openWith(t, storage.NewMemoryStorage(), environment[0])
	} else {
		opened, root = f.setup.open(t, storage.NewMemoryStorage())
	}
	mustSubmitDone(t, root)
	text, _ := resultText(t, root)
	message := resultEntries(t, root)[0].Model[0].(ai.ToolResultMessage)
	run := modelsRun{text: text, content: message.Content, details: detailsOf(t, root), usage: must(durable.Snapshot(testContext, opened, harness.UsageDoc, root.Id()))}
	for _, task := range toolTasks(t, opened) {
		if input := must(durable.FromJsonValue[harness.ToolTaskInput](task.Input)); input.Kind == harness.ToolTaskNested {
			run.calls = append(run.calls, string(must(input.Call.ArgumentsJSON())))
		}
	}
	mustClose(t, opened)
	return run
}

func (r modelsRun) hasImage() bool {
	for _, item := range r.content {
		if _, ok := item.(ai.ImageContent); ok {
			return true
		}
	}
	return false
}

func (r modelsRun) rows() []string {
	var rows []string
	for _, row := range r.details.Calls {
		rows = append(rows, row.Name+" "+row.Status)
	}
	return rows
}

func expectJSON(t *testing.T, got, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal([]byte(got), &a); err != nil {
		t.Fatalf("%q is not JSON: %v", got, err)
	}
	_ = json.Unmarshal([]byte(want), &b)
	if !equalJSON(a, b) {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

type seenRequest struct {
	state     string
	questions []string
}

const boolQuestion = `{ good: { type: "bool", instructions: "?", criteria: { true: "y", false: "n" } } }`

func TestCodeModeModels(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/codemode-models.test.ts:143 "reads the model catalog without credentials in headers"
	t.Run("reads the model catalog without credentials in headers", func(t *testing.T) {
		run := modelsChat(t).run(t, `const classifiers = await models.getModelsOfType("classifier", "test");
			const one = await models.getModelOfType("classifier", "test", "judge");
			const available = await models.getAvailableOfType("image");
			return [classifiers.map((m) => m.id), one.id, "headers" in one, available.map((m) => m.id)];`)
		expectJSON(t, run.text, `[["judge"],"judge",false,["painter"]]`)
	})

	// Pi 1.2.0 packages/durable/test/codemode-models.test.ts:155 "classifies through an internal tool: a nested call with its usage, hidden from tools and listings"
	t.Run("classifies through an internal tool: a nested call with its usage, hidden from tools and listings", func(t *testing.T) {
		f := modelsChat(t)
		run := f.run(t, `const model = await models.getModelOfType("classifier", "test", "judge");
			const result = await models.classify(model, {
				state: { text: "hi" },
				questions: { good: { type: "bool", instructions: "Is it good?", criteria: { true: "yes", false: "no" } } },
			});
			const found = (await searchTools("classify generate images models")).length;
			const described = (await describeTool("pi.codemode.models.classify")) !== undefined;
			return [result.stopReason, result.answers.good.value, Object.keys(tools).some((name) => name.includes("models")), ALL_TOOLS.some((tool) => tool.name.includes("models")), found, described];`)
		expectJSON(t, run.text, `["stop",true,false,false,0,false]`)
		if f.calls.classify.Load() != 1 {
			t.Errorf("%d classify calls, want 1", f.calls.classify.Load())
		}
		if rows := run.rows(); len(rows) != 1 || rows[0] != "models.classify ok" {
			t.Errorf("rows %q", rows)
		}
		// Counted once, under the internal tool, by the Harness.
		if total := run.usage.Tools["pi.codemode.models.classify"].Cost.Total; math.Abs(total-0.0012) > 1e-9 {
			t.Errorf("usage %+v", run.usage.Tools)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode-models.test.ts:177 "resolves a model call that failed with its result, as the coding agent does"
	t.Run("resolves a model call that failed with its result, as the coding agent does", func(t *testing.T) {
		run := modelsChat(t).run(t, `const model = await models.getModelOfType("classifier", "test", "judge");
			const result = await models.classify(model, { state: { fail: true }, questions: `+boolQuestion+` });
			return [result.stopReason, result.errorMessage];`)
		expectJSON(t, run.text, `["error","quota exceeded"]`)
		// The row still shows the failure.
		if rows := run.rows(); len(rows) != 1 || rows[0] != "models.classify error" {
			t.Errorf("rows %q", rows)
		}
	})

	// PiG addition (no Pi case): the provider gets the questions in the order the script wrote them.
	t.Run("keeps the order of the questions the script wrote", func(t *testing.T) {
		f := modelsChat(t)
		run := f.run(t, `const model = await models.getModelOfType("classifier", "test", "judge");
			const question = { type: "bool", instructions: "?", criteria: { true: "y", false: "n" } };
			await models.classify(model, { state: { zeta: 1, alpha: { yy: [{ b: 1, a: 2 }], xx: 3 } }, questions: { zeta: question, alpha: question } });`)
		seen, _ := f.seen.Load().(seenRequest)
		if want := []string{"zeta", "alpha"}; !slices.Equal(seen.questions, want) {
			t.Errorf("the provider got the questions %v, want %v", seen.questions, want)
		}
		// ai.ClassifierContext.State is a Go map (ai.JsonObject): its key order is lost for every caller, not only code mode.
		if want := `{"alpha":{"xx":3,"yy":[{"a":2,"b":1}]},"zeta":1}`; seen.state != want {
			t.Errorf("the provider got the state %s, want %s", seen.state, want)
		}
		// The nested call stores the arguments the script passed, in its order and with nothing added (codemode.ts:817-829
		// callTool: executeTool(call.name, args, ...)).
		question := `{"type":"bool","instructions":"?","criteria":{"true":"y","false":"n"}}`
		want := `,"context":{"state":{"zeta":1,"alpha":{"yy":[{"b":1,"a":2}],"xx":3}},"questions":{"zeta":` + question + `,"alpha":` + question + `}}}`
		if len(run.calls) != 1 || !strings.HasPrefix(run.calls[0], `{"model":{`) || !strings.HasSuffix(run.calls[0], want) {
			t.Errorf("stored nested call arguments %q, want the model and then the context %s", run.calls, want)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode-models.test.ts:195 "rejects a bad classifier context with the expected shape, before any request"
	t.Run("rejects a bad classifier context with the expected shape, before any request", func(t *testing.T) {
		f := modelsChat(t)
		run := f.run(t, `const model = await models.getModelOfType("classifier", "test", "judge");
			try { await models.classify(model, { prompt: "x" }); } catch (error) { return error.message; }`)
		for _, want := range []string{"context.state must be an object", "Expected context:"} {
			if !strings.Contains(run.text, want) {
				t.Errorf("%q lacks %q", run.text, want)
			}
		}
		if f.calls.classify.Load() != 0 {
			t.Errorf("%d classify calls, want 0", f.calls.classify.Load())
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode-models.test.ts:207 "runs at most four model calls at once"
	t.Run("runs at most four model calls at once", func(t *testing.T) {
		f := modelsChat(t)
		f.hold(true)
		run := f.run(t, `const model = await models.getModelOfType("classifier", "test", "judge");
			const context = { state: {}, questions: `+boolQuestion+` };
			return (await Promise.all(Array.from({ length: 8 }, () => models.classify(model, context)))).length;`)
		if run.text != "8" || f.calls.classify.Load() != 8 || f.calls.maxInFlight.Load() != 4 {
			t.Errorf("text %q, %d calls, at most %d at once; want 8, 8, 4", run.text, f.calls.classify.Load(), f.calls.maxInFlight.Load())
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode-models.test.ts:222 "does not start a model call that waited for a slot after the script ended"
	t.Run("does not start a model call that waited for a slot after the script ended", func(t *testing.T) {
		f := modelsChat(t)
		f.hold(false)
		// The result spills to a file after the script ended and before the tool returns. Then the running calls finish and
		// free their slots, which the two waiting calls would take.
		slow := &slowTempFiles{ExecutionEnv: envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()}), release: f.release}
		run := f.run(t, `const model = await models.getModelOfType("classifier", "test", "judge");
			const context = { state: {}, questions: `+boolQuestion+` };
			for (let i = 0; i < 6; i++) models.classify(model, context).catch(() => {});
			// A journaled call after them: by its result, the first four have been launched.
			await models.getModelsOfType("image");
			return "x".repeat(60000);`, slow)
		if !strings.Contains(run.text, "[Full output: ") || f.calls.classify.Load() != 4 {
			t.Errorf("text %.80q, %d classify calls, want a full output file and 4", run.text, f.calls.classify.Load())
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode-models.test.ts:253 "notes generated images the script did not show"
	t.Run("notes generated images the script did not show", func(t *testing.T) {
		hidden := modelsChat(t).run(t, `const model = await models.getModelOfType("image", "test", "painter");
			const result = await models.generateImages(model, { input: [{ type: "text", text: "a cat" }] });
			return result.output.length;`)
		if !strings.Contains(hidden.text, "Note: models.generateImages() returned 1 image that the script did not show") {
			t.Errorf("no note in %q", hidden.text)
		}
		shown := modelsChat(t).run(t, `const model = await models.getModelOfType("image", "test", "painter");
			const result = await models.generateImages(model, { input: [{ type: "text", text: "a cat" }] });
			image(result.output[0]);`)
		if strings.Contains(shown.text, "Note:") || !shown.hasImage() {
			t.Errorf("shown: %q, image %v", shown.text, shown.hasImage())
		}
	})
}

// slowTempFiles creates its temp directory late, after letting the held classifier calls go.
type slowTempFiles struct {
	env.ExecutionEnv
	release func()
}

func (e *slowTempFiles) CreateTempDir(ctx context.Context, prefix *string) (string, error) {
	e.release()
	time.Sleep(100 * time.Millisecond)
	return e.ExecutionEnv.CreateTempDir(ctx, prefix)
}

// Pi 2ec55d80 packages/durable/src/codemode/models.ts: models.classify() and models.generateImages() pass the calling
// phase's telemetry to the model call, so a provider's spans and events land in the trace of the tool call that made them.
func TestCodeModeModelCallsRunInTheCallingPhasesTrace(t *testing.T) {
	f := modelsChat(t)
	recorder := telemetry.NewInMemoryTelemetryContext()
	f.setup.telemetry = recorder
	f.run(t, `const judge = await models.getModelOfType("classifier", "test", "judge");
		const painter = await models.getModelOfType("image", "test", "painter");
		await models.classify(judge, { state: {}, questions: `+boolQuestion+` });
		await models.generateImages(painter, { input: [{ type: "text", text: "a cat" }] });
		return "ok";`)
	spans := recorder.GetSpans()
	byID := map[int]telemetry.RecordedTelemetrySpan{}
	for _, span := range spans {
		byID[span.ID] = span
	}
	inPhase := func(span telemetry.RecordedTelemetrySpan) bool {
		for {
			if span.Name == "pi.durable.phase" {
				return true
			}
			if span.ParentID == nil {
				return false
			}
			span = byID[*span.ParentID]
		}
	}
	for _, event := range []string{"test.classify", "test.images"} {
		found := false
		for _, span := range spans {
			for _, recorded := range span.Events {
				if recorded.Name == event {
					found = true
					if !inPhase(span) {
						t.Errorf("%s is on span %q, which is not under a pi.durable.phase span", event, span.Name)
					}
				}
			}
		}
		if !found {
			t.Errorf("no span recorded %s: the model call did not receive the phase's telemetry", event)
		}
	}
}
