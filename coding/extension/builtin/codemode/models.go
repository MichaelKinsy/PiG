package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	sandbox "github.com/MichaelKinsy/PiG/codemode"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/usagetotals"
)

// maxConcurrentModelCalls is how many `models.classify()` and `models.generateImages()` calls one script may have in
// flight; `Promise.all` over many items queues the rest.
//
// upstream: execute.ts MAX_CONCURRENT_MODEL_CALLS
const maxConcurrentModelCalls = 4

// modelRuntime is the part of the model registry that scripts reach through `models`: the Session's ModelRegistry.
//
// upstream: tool.ts CodemodeModelRuntime
type modelRuntime interface {
	GetModelsOfType(modelType ai.ModelType, provider ...string) []ai.AnyModel
	GetAvailableOfType(ctx context.Context, modelType ai.ModelType, provider ...string) ([]ai.AnyModel, error)
	GetModelOfType(modelType ai.ModelType, provider, id string) ai.AnyModel
	Classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ...ai.ModelsClassifierOptions) ai.ClassifierResult
	GenerateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ...ai.ModelsImagesOptions) ai.AssistantImages
}

// modelRuntimeOf is the registry of the tool context, when it has the typed operations.
func modelRuntimeOf(tc *extension.ToolContext) modelRuntime {
	if tc == nil {
		return nil
	}
	registry, err := tc.ModelRegistry()
	if err != nil {
		return nil
	}
	runtime, _ := registry.(modelRuntime)
	return runtime
}

// limiter runs at most `limit` calls at once, in call order. A finishing call hands its slot to the first waiter, so a caller arriving between the release and the waiter's wake-up queues behind it instead of taking the slot as well.
//
// upstream: execute.ts createLimiter
type limiter struct {
	mu      sync.Mutex
	limit   int
	active  int
	waiting []chan struct{}
}

func (l *limiter) run(call func()) {
	l.mu.Lock()
	if l.active >= l.limit {
		wake := make(chan struct{})
		l.waiting = append(l.waiting, wake)
		l.mu.Unlock()
		// The releasing call kept active counted for this waiter.
		<-wake
	} else {
		l.active++
		l.mu.Unlock()
	}
	defer func() {
		l.mu.Lock()
		if len(l.waiting) > 0 {
			wake := l.waiting[0]
			l.waiting = l.waiting[1:]
			close(wake)
		} else {
			l.active--
		}
		l.mu.Unlock()
	}()
	call()
}

// modelGlobals are `models.*` for scripts: the model registry methods documented in docs/codemode.md. Classifier and
// image calls appear as nested call rows so the renderer shows them, and their usage joins the result's. Rows show only
// the model, never prompts or image data.
//
// Ports packages/coding-agent/src/extensions/codemode/execute.ts (createModelGlobals).
func (r *run) modelGlobals(models modelRuntime) []sandbox.Tool {
	limit := &limiter{limit: maxConcurrentModelCalls}
	var callCount int

	// runModelCall resolves the script's model by provider and id only, checks the context, then runs the call as a
	// nested call row. A script-supplied baseUrl or headers must never receive the credentials.
	//
	// upstream: execute.ts createModelGlobals runModelCall
	runModelCall := func(name string, modelType ai.ModelType, args []json.RawMessage, checkContext func(json.RawMessage) error, call func(resolved ai.AnyModel) modelCallResult) (modelCallResult, error) {
		model, context := argOrUndefined(args, 0), argOrUndefined(args, 1)
		listHint := fmt.Sprintf("List the %s models you can use with models.getAvailableOfType(%q).", modelType, string(modelType))
		provider, okProvider := stringArg(field(model, "provider"))
		id, okID := stringArg(field(model, "id"))
		if !isRecord(model) || !okProvider || !okID {
			// undefined arrives as null: spread arguments cross the sandbox as a JSON array.
			undefinedHint := ""
			if model == nil || string(model) == "null" {
				undefinedHint = " models.getModelOfType() returns undefined for an unknown provider or id."
			}
			return modelCallResult{}, fmt.Errorf("%s() expects %s model as its first argument, got %s.%s %s", name, withArticle(string(modelType)), describeValue(model), undefinedHint, listHint)
		}
		ref := provider + "/" + id
		resolved := models.GetModelOfType(modelType, provider, id)
		if resolved == nil {
			for _, other := range modelTypes {
				if other != modelType && models.GetModelOfType(other, provider, id) != nil {
					return modelCallResult{}, fmt.Errorf("%q is %s model, not %s model. %s", ref, withArticle(string(other)), withArticle(string(modelType)), listHint)
				}
			}
			return modelCallResult{}, fmt.Errorf("Unknown %s model %q. %s", modelType, ref, listHint)
		}
		if err := checkContext(context); err != nil {
			return modelCallResult{}, err
		}

		r.mu.Lock()
		callCount++
		record := &NestedCall{ID: fmt.Sprintf("%s/%s/%d", r.toolCallID, name, callCount), Name: name, Args: resolved.ProviderID() + "/" + resolved.ModelID(), Status: StatusRunning}
		r.calls = append(r.calls, record)
		r.mu.Unlock()
		r.publish()
		startedAt := time.Now()
		var result modelCallResult
		limit.run(func() { result = call(resolved) })
		duration := millisSince(startedAt)
		r.update(record, func(c *NestedCall) {
			c.DurationMs = &duration
			switch result.stopReason {
			case "stop":
				c.Status = StatusOK
			case "aborted":
				c.Status = StatusCancelled
			default:
				c.Status = StatusError
			}
			if result.errorMessage != "" {
				c.Error = truncateText(result.errorMessage, errorPreviewChars)
			}
			if result.usage != nil {
				c.Cost = &result.usage.Cost.Total
			}
		})
		if result.usage != nil {
			r.addModelUsage(*result.usage)
		}
		return result, nil
	}

	implementations := map[string]func(ctx context.Context, args []json.RawMessage) (any, error){
		"models.getModelsOfType": func(_ context.Context, args []json.RawMessage) (any, error) {
			modelType, err := toModelType(argOrUndefined(args, 0))
			if err != nil {
				return nil, err
			}
			provider, err := toProvider(argOrUndefined(args, 1))
			if err != nil {
				return nil, err
			}
			return toModelInfos(models.GetModelsOfType(modelType, provider...)), nil
		},
		"models.getAvailableOfType": func(ctx context.Context, args []json.RawMessage) (any, error) {
			modelType, err := toModelType(argOrUndefined(args, 0))
			if err != nil {
				return nil, err
			}
			provider, err := toProvider(argOrUndefined(args, 1))
			if err != nil {
				return nil, err
			}
			available, err := models.GetAvailableOfType(ctx, modelType, provider...)
			if err != nil {
				return nil, err
			}
			return toModelInfos(available), nil
		},
		"models.getModelOfType": func(_ context.Context, args []json.RawMessage) (any, error) {
			provider, okProvider := stringArg(argOrUndefined(args, 1))
			id, okID := stringArg(argOrUndefined(args, 2))
			if !okProvider || !okID {
				described := make([]string, len(args))
				for i, value := range args {
					described[i] = describeValue(value)
				}
				return nil, fmt.Errorf("models.getModelOfType(type, provider, id) expects three strings, got (%s). The provider and the id are separate arguments, for example models.getModelOfType(\"classifier\", \"typesafe\", \"jev-latest\").", strings.Join(described, ", "))
			}
			modelType, err := toModelType(argOrUndefined(args, 0))
			if err != nil {
				return nil, err
			}
			model := models.GetModelOfType(modelType, provider, id)
			if model == nil {
				return undefined{}, nil
			}
			return toModelInfo(model), nil
		},
		"models.classify": func(ctx context.Context, args []json.RawMessage) (any, error) {
			var request ai.ClassifierContext
			result, err := runModelCall("models.classify", ai.ModelTypeClassifier, args, func(context json.RawMessage) error {
				if err := checkClassifierContext(context); err != nil {
					return err
				}
				// A checked context always decodes: ai.ClassifierContext reads only the members checkClassifierContext checked.
				if json.Unmarshal(context, &request) != nil {
					request = ai.ClassifierContext{}
				}
				return nil
			}, func(resolved ai.AnyModel) modelCallResult {
				classifier, _ := resolved.(*ai.ClassifierModel)
				classified := models.Classify(ctx, classifier, request)
				return modelCallResult{value: classified, stopReason: string(classified.StopReason), errorMessage: classified.ErrorMessage, usage: classified.Usage}
			})
			if err != nil {
				return nil, err
			}
			return result.value, nil
		},
		"models.generateImages": func(ctx context.Context, args []json.RawMessage) (any, error) {
			var request ai.ImagesContext
			result, err := runModelCall("models.generateImages", ai.ModelTypeImage, args, func(context json.RawMessage) error {
				if err := checkImagesContext(context); err != nil {
					return err
				}
				request = imagesContextOf(context)
				return nil
			}, func(resolved ai.AnyModel) modelCallResult {
				image, _ := resolved.(*ai.ImageModel)
				generated := models.GenerateImages(ctx, image, request)
				count := 0
				for _, block := range generated.Output {
					if _, ok := block.(ai.ImageContent); ok {
						count++
					}
				}
				r.addGeneratedImages(count)
				return modelCallResult{value: generated, stopReason: string(generated.StopReason), errorMessage: generated.ErrorMessage, usage: generated.Usage}
			})
			if err != nil {
				return nil, err
			}
			return result.value, nil
		},
	}
	globals := make([]sandbox.Tool, 0, len(modelGlobalNames))
	for _, name := range modelGlobalNames {
		implementation := implementations[name]
		globals = append(globals, sandbox.Tool{Name: name, Spread: true, Execute: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var args []json.RawMessage
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, err
			}
			value, err := implementation(ctx, args)
			if err != nil {
				return nil, err
			}
			if _, none := value.(undefined); none {
				return nil, nil
			}
			return json.Marshal(value)
		}})
	}
	return globals
}

// modelGlobalNames are the `models` globals in upstream's declaration order (execute.ts createModelGlobals implementations).
var modelGlobalNames = []string{"models.getModelsOfType", "models.getAvailableOfType", "models.getModelOfType", "models.classify", "models.generateImages"}

// modelTypes is upstream MODEL_TYPES, in its order.
var modelTypes = []ai.ModelType{ai.ModelTypeChat, ai.ModelTypeImage, ai.ModelTypeClassifier}

// modelCallResult holds the fields of ClassifierResult and AssistantImages that a nested call row reports, and the
// value the script receives.
//
// upstream: execute.ts ModelCallResult
type modelCallResult struct {
	value        any
	stopReason   string
	errorMessage string
	usage        *ai.Usage
}

// imagesGenerated is the number of images `models.generateImages()` returned during the script.
func (r *run) imagesGenerated() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.generatedImages
}

// addGeneratedImages counts images returned by `models.generateImages()`, to notice a script that never shows them.
func (r *run) addGeneratedImages(count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generatedImages += count
}

// undefined is the value of a global that returns undefined, which the sandbox reads as no result.
type undefined struct{}

// addModelUsage adds the usage of a script's `models.*` call. Nested tool calls report theirs through the session.
func (r *run) addModelUsage(usage ai.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.modelUsage == nil {
		r.modelUsage = &usage
		return
	}
	sum := usagetotals.CombineUsage(*r.modelUsage, usage)
	r.modelUsage = &sum
}

// argOrUndefined is the i-th call argument, or nil (undefined) when the script passed fewer. An undefined argument
// the script passed before a later one arrives as JSON null.
func argOrUndefined(args []json.RawMessage, i int) json.RawMessage {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// stringArg is the string raw holds; null, undefined and other values are not strings.
func stringArg(raw json.RawMessage) (string, bool) {
	if jsonKind(raw) != '"' {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func toModelType(raw json.RawMessage) (ai.ModelType, error) {
	if value, ok := stringArg(raw); ok {
		switch modelType := ai.ModelType(value); modelType {
		case ai.ModelTypeChat, ai.ModelTypeImage, ai.ModelTypeClassifier:
			return modelType, nil
		}
	}
	return "", fmt.Errorf("Unknown model type %s. Use \"chat\", \"image\", or \"classifier\".", jsonStringify(raw))
}

// toProvider is the optional provider argument; nothing means every provider.
func toProvider(raw json.RawMessage) ([]string, error) {
	if raw == nil || string(raw) == "null" {
		return nil, nil
	}
	value, ok := stringArg(raw)
	if !ok {
		return nil, errors.New("provider must be a string")
	}
	return []string{value}, nil
}

// extensionWireModelAliases are the keys the extension wire adds to a model object that Pi's model objects do not have.
var extensionWireModelAliases = []string{"modelId", "displayName", "maxOutputTokens", "inputCostPer1M", "outputCostPer1M", "cacheReadCostPer1M", "cacheWriteCostPer1M"}

// toModelInfo is the catalog entry for scripts: Pi's model object ({ ...model }). Headers are dropped because models.json headers can carry credentials.
func toModelInfo(model ai.AnyModel) map[string]any {
	info := extension.AnyModelInfo(model)
	delete(info, "headers")
	for _, key := range extensionWireModelAliases {
		delete(info, key)
	}
	// An optional field the model does not set is absent from Pi's object, not null.
	for key, value := range info {
		if isNilValue(value) {
			delete(info, key)
		}
	}
	return info
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice, reflect.Interface:
		return reflected.IsNil()
	}
	return false
}

func toModelInfos(models []ai.AnyModel) []map[string]any {
	infos := make([]map[string]any, 0, len(models))
	for _, model := range models {
		infos = append(infos, toModelInfo(model))
	}
	return infos
}
