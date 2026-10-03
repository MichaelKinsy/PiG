package coding

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The harness of packages/coding-agent/test/suite/agent-session-codemode.test.ts: a Session with the native built-in
// codemode extension (docs/specs/builtin-codemode-tool-search.md) and Node extension fixtures.

type codemodeHarnessOptions struct {
	// fixtures name Node extensions under testdata/codemode, loaded after the built-in codemode extension.
	fixtures []string
	// tools are Go tools added to the Session next to the extension's.
	tools []agent.AgentTool
	// settings is the settings.json content.
	settings string
	// goExtensions are extensions written in Go, loaded after the Node fixtures.
	goExtensions []extension.Extension
	// noExtension leaves the built-in codemode extension out.
	noExtension bool
	// activeTools are the tool names active after construction (upstream initialActiveToolNames).
	activeTools []string
}

type codemodeHarness struct {
	*recoveryHarness
	mode atomic.Value
}

func newCodemodeHarness(t *testing.T, opts codemodeHarnessOptions, responses ...scriptedResponse) *codemodeHarness {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("codemode Session complete") })
	var configs []subprocess.ExtConfig
	for _, name := range opts.fixtures {
		source, err := filepath.Abs(filepath.Join("testdata", "codemode", name+".mjs"))
		if err != nil {
			t.Fatal(err)
		}
		configs = append(configs, subprocess.ExtConfig{Name: name, Source: source, Enabled: true})
	}
	// The extensions keep this context for their lifetime: it ends with the test, not with this function.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)
	loaded, errs := host.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d of %d extensions, errors %v", len(loaded), len(configs), errs)
	}
	loaded = append(loaded, opts.goExtensions...)
	h := &codemodeHarness{}
	h.mode.Store("on")
	if !opts.noExtension {
		// upstream: extensionFactories: [createCodemodeExtension()], reading `codemode.mode` from the settings on every use.
		entry, err := builtin.Resolve("builtin:codemode", builtin.Options{Codemode: codemode.Options{GetSettings: func() extension.Settings {
			return extension.Settings{"codemode": map[string]any{"mode": h.mode.Load().(string)}}
		}}})
		if err != nil {
			t.Fatal(err)
		}
		ext, err := entry.Factory()
		if err != nil {
			t.Fatal(err)
		}
		info := codingagent.PiSourceInfo{Path: entry.Path(), Source: "builtin", Scope: "temporary", Origin: "top-level"}
		ext.Name, ext.Path, ext.ResolvedPath, ext.SourceInfo, ext.Replaceable, ext.Hidden = entry.Name, entry.Path(), entry.Path(), info, true, true
		for name, tool := range ext.Tools {
			tool.SourceInfo = info
			ext.Tools[name] = tool
		}
		loaded = append([]extension.Extension{ext}, loaded...)
	}
	h.recoveryHarness = newBoundaryHarness(t, harnessOptions{tools: opts.tools, settings: opts.settings, extensions: loaded}, responses...)
	// upstream's createHarness binds the extensions to the session before the first prompt.
	if err := h.session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	if opts.activeTools != nil {
		// upstream's initialActiveToolNames narrow the built-in tools; the tools an extension registers after that are
		// active by default, so the names are added to the tools active after loading.
		builtins := []string{"read", "bash", "edit", "write", "grep", "find", "ls"}
		var active []string
		for _, name := range h.session.ActiveToolNames() {
			if !slices.Contains(builtins, name) {
				active = append(active, name)
			}
		}
		h.session.SetActiveToolsByName(append(active, opts.activeTools...))
	}
	return h
}

// setCodemodeMode is upstream's `harness.settingsManager.applyOverrides({ codemode: { mode } })`: the built-in reads the
// mode on every use.
func (h *codemodeHarness) setCodemodeMode(t *testing.T, mode string) {
	t.Helper()
	h.mode.Store(mode)
}

// classifierCall is one call the scorer provider's classifier API receives: upstream's observation record
// (agent-session-codemode.test.ts:497-501) with the model's base URL and the request's API key.
type classifierCall struct {
	BaseURL string
	APIKey  string
	Text    any
}

// imagesCall is one request the scorer provider's image API receives: upstream's ImagesObservation
// (agent-session-codemode.test.ts:574-578, v1.0.0) with the model's base URL, the request's API key and the input blocks.
type imagesCall struct {
	BaseURL string
	APIKey  string
	Input   []ai.ContentBlock
}

// registerScorerProvider is upstream's `harness.session.modelRuntime.registerProvider("scorer", ...)`
// (agent-session-codemode.test.ts:590-646, v1.0.0): provider "scorer" with API key "secret-key", the classifier model
// "judge" (base URL https://classifier.test/v1, header X-Secret) with the API "test-classifier", and the image model
// "painter" (base URL https://images.test/v1) with the API "test-images". The classifier sleeps 10 ms per call, counts
// concurrent calls and answers as classify describes. The image API answers the prompt "explode" with an error result
// and any other prompt with the text "painted <prompt>", one tiny PNG and usage(100, 0.04).
func (h *codemodeHarness) registerScorerProvider(t *testing.T) (observed func() []classifierCall, maxActive func() int, imageRequests func() []imagesCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []classifierCall
	var images []imagesCall
	var active, peak int
	painter := &ai.ImageModel{
		ID: "painter", Name: "Painter", API: "test-images", Provider: "scorer", BaseURL: "https://images.test/v1",
		Input: []string{"text", "image"}, Output: []string{"text", "image"},
	}
	generateImages := func(_ context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
		mu.Lock()
		images = append(images, imagesCall{BaseURL: model.BaseURL, APIKey: options.APIKey, Input: slices.Clone(request.Input)})
		mu.Unlock()
		var prompt string
		for _, block := range request.Input {
			if text, ok := block.(ai.TextContent); ok {
				prompt = text.Text
				break
			}
		}
		result := ai.AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID}
		if prompt == "explode" {
			result.Output, result.StopReason, result.ErrorMessage = []ai.ContentBlock{}, ai.ImagesStopReasonError, "painter exploded"
			return result, nil
		}
		result.Output = []ai.ContentBlock{ai.TextContent{Text: "painted " + prompt}, ai.ImageContent{Data: tinyPNGBase64, MimeType: "image/png"}}
		result.Usage = &ai.Usage{Input: 100, TotalTokens: 100, Cost: ai.UsageCost{Input: 0.04, Total: 0.04}}
		result.StopReason = ai.ImagesStopReasonStop
		return result, nil
	}
	scorer := &ai.ClassifierModel{
		ID: "judge", Name: "Judge", API: "test-classifier", Provider: "scorer", BaseURL: "https://classifier.test/v1",
		Input: []string{"text"}, ContextWindow: 1000, Headers: map[string]string{"X-Secret": "hunter2"},
	}
	classify := func(_ context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
		mu.Lock()
		active++
		peak = max(peak, active)
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		text := request.State["text"]
		calls = append(calls, classifierCall{BaseURL: model.BaseURL, APIKey: options.APIKey, Text: text})
		mu.Unlock()
		result := ai.ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID}
		if text == "explode" {
			result.Answers, result.StopReason, result.ErrorMessage = ai.ClassifierAnswers{}, ai.ClassifierStopReasonError, "classifier exploded"
			return result, nil
		}
		probability := 0.1
		if text == "good" {
			probability = 0.9
		}
		result.Answers = ai.ClassifierAnswers{{ID: "approved", Answer: ai.ClassifierBoolAnswer{Probability: probability}}}
		result.Usage = &ai.Usage{Input: 300, TotalTokens: 300, Cost: ai.UsageCost{Input: 0.001, Total: 0.001}}
		result.StopReason = ai.ClassifierStopReasonStop
		return result, nil
	}
	err := h.session.ModelRuntime().RegisterProvider("scorer", ProviderConfigInput{
		APIKey:      "secret-key",
		Models:      []ai.AnyModel{scorer, painter},
		Images:      ai.ProviderImageAPIMap{"test-images": {GenerateImages: generateImages}},
		Classifiers: ai.ProviderClassifierMap{"test-classifier": {Classify: classify}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return func() []classifierCall {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(calls)
		}, func() int {
			mu.Lock()
			defer mu.Unlock()
			return peak
		}, func() []imagesCall {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(images)
		}
}
