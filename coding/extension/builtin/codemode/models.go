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

	"github.com/MichaelKinsy/PiG/agent/harness/utils"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// maxConcurrentModelCalls is how many `models.classify` calls of one script run at once.
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

// modelGlobals are `models.*` for scripts: the registry methods declared in modelGlobalDeclarations. Classifier calls
// appear as nested call rows so the renderer shows them, and their usage joins the result's.
//
// Ports packages/coding-agent/src/extensions/codemode/execute.ts (createModelGlobals).
func (r *run) modelGlobals(models modelRuntime) []sandbox.Tool {
	limit := &limiter{limit: maxConcurrentModelCalls}
	var classifyCount int
	implementations := map[string]func(ctx context.Context, args []json.RawMessage) (any, error){
		"models.getModelsOfType": func(_ context.Context, args []json.RawMessage) (any, error) {
			modelType, err := toModelType(arg(args, 0))
			if err != nil {
				return nil, err
			}
			provider, err := toProvider(arg(args, 1))
			if err != nil {
				return nil, err
			}
			return toModelInfos(models.GetModelsOfType(modelType, provider...)), nil
		},
		"models.getAvailableOfType": func(ctx context.Context, args []json.RawMessage) (any, error) {
			modelType, err := toModelType(arg(args, 0))
			if err != nil {
				return nil, err
			}
			provider, err := toProvider(arg(args, 1))
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
			provider, okProvider := stringArg(arg(args, 1))
			id, okID := stringArg(arg(args, 2))
			if !okProvider || !okID {
				return nil, errors.New("models.getModelOfType() expects a type, a provider, and an id")
			}
			modelType, err := toModelType(arg(args, 0))
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
			var ref struct {
				Provider any `json:"provider"`
				ID       any `json:"id"`
			}
			// A model that is not an object (a string, null) has neither field.
			_ = json.Unmarshal(arg(args, 0), &ref)
			provider, okProvider := ref.Provider.(string)
			id, okID := ref.ID.(string)
			if !okProvider || !okID {
				return nil, errors.New("models.classify() expects a model from models.getModelOfType() or models.getAvailableOfType()")
			}
			// Only provider and id count. A script-supplied baseUrl or headers must never receive the credentials.
			resolved, _ := models.GetModelOfType(ai.ModelTypeClassifier, provider, id).(*ai.ClassifierModel)
			if resolved == nil {
				return nil, fmt.Errorf("Unknown classifier model %q", provider+"/"+id)
			}
			r.mu.Lock()
			classifyCount++
			record := &NestedCall{ID: fmt.Sprintf("%s/models.classify/%d", r.toolCallID, classifyCount), Name: "models.classify", Args: resolved.Provider + "/" + resolved.ID, Status: StatusRunning}
			r.calls = append(r.calls, record)
			r.mu.Unlock()
			r.publish()
			startedAt := time.Now()
			var request ai.ClassifierContext
			var result ai.ClassifierResult
			limit.run(func() {
				if err := json.Unmarshal(arg(args, 1), &request); err != nil {
					result = ai.ClassifierErrorResult(resolved, err, false)
					return
				}
				result = models.Classify(ctx, resolved, request)
			})
			duration := millisSince(startedAt)
			r.update(record, func(c *NestedCall) {
				c.DurationMs = &duration
				switch result.StopReason {
				case ai.ClassifierStopReasonStop:
					c.Status = StatusOK
				case ai.ClassifierStopReasonAborted:
					c.Status = StatusCancelled
				default:
					c.Status = StatusError
				}
				if result.ErrorMessage != "" {
					c.Error = truncateText(result.ErrorMessage, errorPreviewChars)
				}
				if result.Usage != nil {
					c.Cost = &result.Usage.Cost.Total
				}
			})
			if result.Usage != nil {
				r.addModelUsage(*result.Usage)
			}
			return result, nil
		},
	}
	globals := make([]sandbox.Tool, len(modelGlobalDeclarations))
	for i, declaration := range modelGlobalDeclarations {
		implementation := implementations[declaration.Name]
		globals[i] = sandbox.Tool{Name: declaration.Name, Spread: true, Execute: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
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
		}}
	}
	return globals
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
	sum := utils.AddUsage(*r.modelUsage, usage)
	r.modelUsage = &sum
}

// arg is the i-th call argument, or JSON null when the script passed fewer (an undefined argument).
func arg(args []json.RawMessage, i int) json.RawMessage {
	if i < len(args) {
		return args[i]
	}
	return json.RawMessage("null")
}

func stringArg(raw json.RawMessage) (string, bool) {
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
	shown := strings.TrimSpace(string(raw))
	if shown == "null" {
		shown = "undefined"
	}
	return "", fmt.Errorf("Unknown model type %s. Use \"chat\", \"image\", or \"classifier\".", shown)
}

// toProvider is the optional provider argument; nothing means every provider.
func toProvider(raw json.RawMessage) ([]string, error) {
	if string(raw) == "null" {
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
