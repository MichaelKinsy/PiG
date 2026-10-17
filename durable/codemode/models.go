// Ports packages/durable/src/codemode/models.ts.

package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// InternalPrefix is the prefix of code mode's internal tools; they are never listed to scripts or the model.
const InternalPrefix = "pi.codemode."

// The names of the internal tools behind models.classify() and models.generateImages().
const (
	ClassifyTool       = InternalPrefix + "models.classify"
	GenerateImagesTool = InternalPrefix + "models.generateImages"
)

// modelCall is a global a script calls, with the internal tool that runs it.
type modelCall struct{ global, tool string }

// modelCalls are the globals that run as internal tools, in the order they are declared.
var modelCalls = []modelCall{{"models.classify", ClassifyTool}, {"models.generateImages", GenerateImagesTool}}

// catalogGlobals are the catalog functions, host globals of the `models` namespace.
var catalogGlobals = []string{"models.getModelsOfType", "models.getAvailableOfType", "models.getModelOfType"}

// maxConcurrentModelCalls is the number of models.classify() and models.generateImages() calls one script may have in
// flight; the rest wait.
const maxConcurrentModelCalls = 4

var modelTypes = []ai.ModelType{ai.ModelTypeChat, ai.ModelTypeImage, ai.ModelTypeClassifier}

// ModelsDescription is what the code mode tool's description says of `models`.
var ModelsDescription = strings.Join([]string{
	"`models` (all functions return promises):",
	"- `models.getModelsOfType(type, provider?)`, `models.getAvailableOfType(type, provider?)` (configured auth only), `models.getModelOfType(type, provider, id)`: the catalog; `type` is \"chat\", \"image\", or \"classifier\".",
	"- `models.classify(model, { state: { ... }, images?: [{ type: \"image\", data, mimeType }], questions: { <id>: { type: \"choice\", instructions, criteria: { <label>: <meaning> } } | { type: \"score\", instructions, criteria: [<lowest level>, ..., <highest level>] } | { type: \"bool\", instructions, criteria: { true: <meaning>, false: <meaning> } } } })` resolves to `{ answers: { <id>: ... }, stopReason, errorMessage?, usage? }`.",
	"- `models.generateImages(model, { input: [{ type: \"text\", text: <prompt> }, ...optional { type: \"image\", data, mimeType }] })` resolves to `{ output: [image blocks], stopReason, errorMessage?, usage? }`; show each image with `image(block)`.",
	fmt.Sprintf("At most %d classify/generateImages calls run at once; more wait.", maxConcurrentModelCalls),
}, "\n")

// catalog is what models.* needs of the Harness's models: the pi-ai Models collection, which the Harness's narrower
// durable.Models is usually.
type catalog interface {
	GetModelsOfType(modelType ai.ModelType, provider ...string) []ai.AnyModel
	GetAvailableOfType(ctx context.Context, modelType ai.ModelType, provider ...string) ([]ai.AnyModel, error)
	GetModelOfType(modelType ai.ModelType, provider, id string) ai.AnyModel
	Classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ...ai.ModelsClassifierOptions) ai.ClassifierResult
	GenerateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ...ai.ModelsImagesOptions) ai.AssistantImages
}

var errNoCatalog = errors.New("The Harness's models have no model catalog, so models.* is unavailable")

func catalogOf(models durable.Models) (catalog, error) {
	if c, ok := models.(catalog); ok {
		return c, nil
	}
	return nil, errNoCatalog
}

// describeValue is a script value in an error message: `undefined`, `a string`, `an array`, or its keys (`{ prompt }`).
func describeValue(v jval) string {
	switch v.kind() {
	case "undefined", "null":
		return v.kind()
	case "array":
		if len(v.items()) == 0 {
			return "an empty array"
		}
		return "an array"
	case "object":
		keys := v.keys()
		if len(keys) == 0 {
			return "{}"
		}
		more := ""
		if len(keys) > 6 {
			more = ", ..."
			keys = keys[:6]
		}
		return "{ " + strings.Join(keys, ", ") + more + " }"
	case "string":
		return "a string"
	}
	return "a " + v.kind()
}

var vowel = regexp.MustCompile(`^[aeiou]`)

// withArticle is `an image`, `a classifier`.
func withArticle(word string) string {
	if vowel.MatchString(word) {
		return "an " + word
	}
	return "a " + word
}

func toModelType(v jval) (ai.ModelType, error) {
	if text, ok := v.str(); ok && slices.Contains(modelTypes, ai.ModelType(text)) {
		return ai.ModelType(text), nil
	}
	return "", fmt.Errorf("Unknown model type %s. Use \"chat\", \"image\", or \"classifier\".", jsonOrUndefined(v))
}

func jsonOrUndefined(v jval) string {
	if text := v.json(); text != "" {
		return text
	}
	return "undefined"
}

func toProvider(v jval) (string, bool, error) {
	if kind := v.kind(); kind == "undefined" || kind == "null" {
		return "", false, nil
	}
	text, ok := v.str()
	if !ok {
		return "", false, errors.New("provider must be a string")
	}
	return text, true, nil
}

// toModelInfo is a catalog entry for scripts. `headers` is dropped because configured headers can carry credentials.
func toModelInfo(model ai.AnyModel) (json.RawMessage, error) {
	encoded, err := ai.EncodeStoredModel(model)
	if err != nil {
		return nil, err
	}
	return dropKey(encoded, "headers")
}

// dropKey removes a top-level key of a JSON object, keeping the order of the others.
func dropKey(object json.RawMessage, key string) (json.RawMessage, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for _, name := range jval(object).keys() {
		if name == key {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		quoted, _ := json.Marshal(name)
		out.Write(quoted)
		out.WriteByte(':')
		out.Write(jval(object).field(name))
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func modelInfos(models []ai.AnyModel) (json.RawMessage, error) {
	infos := make([]json.RawMessage, len(models))
	for i, model := range models {
		info, err := toModelInfo(model)
		if err != nil {
			return nil, err
		}
		infos[i] = info
	}
	return json.Marshal(infos)
}

// catalogCall is what a script's call of the catalog function name with args (its argument list) resolves to.
func catalogCall(ctx context.Context, name string, args json.RawMessage, models durable.Models) (json.RawMessage, error) {
	c, err := catalogOf(models)
	if err != nil {
		return nil, err
	}
	list := jval(args).items()
	arg := func(i int) jval {
		if i < len(list) {
			return list[i]
		}
		return nil
	}
	modelType, provider, id := arg(0), arg(1), arg(2)
	switch name {
	case "models.getModelsOfType", "models.getAvailableOfType":
		kind, err := toModelType(modelType)
		if err != nil {
			return nil, err
		}
		providerID, has, err := toProvider(provider)
		if err != nil {
			return nil, err
		}
		var providers []string
		if has {
			providers = []string{providerID}
		}
		if name == "models.getModelsOfType" {
			return modelInfos(c.GetModelsOfType(kind, providers...))
		}
		available, err := c.GetAvailableOfType(ctx, kind, providers...)
		if err != nil {
			return nil, err
		}
		return modelInfos(available)
	}
	providerID, providerOK := provider.str()
	modelID, idOK := id.str()
	if !providerOK || !idOK {
		described := make([]string, len(list))
		for i, item := range list {
			described[i] = describeValue(item)
		}
		return nil, fmt.Errorf("models.getModelOfType(type, provider, id) expects three strings, got (%s). The provider and the id are separate arguments, for example models.getModelOfType(\"classifier\", \"typesafe\", \"jev-latest\").", strings.Join(described, ", "))
	}
	kind, err := toModelType(modelType)
	if err != nil {
		return nil, err
	}
	model := c.GetModelOfType(kind, providerID, modelID)
	if model == nil {
		return nil, nil
	}
	return toModelInfo(model)
}

const classifierContextShape = `{ state: { ... }, images?: [{ type: "image", data: <base64>, mimeType }], questions: { <id>: { type: "choice", instructions, criteria: { <label>: <meaning> } } | { type: "score", instructions, criteria: [<lowest level>, ..., <highest level>] } | { type: "bool", instructions, criteria: { true: <meaning>, false: <meaning> } } } }`

func isImageBlock(v jval) bool {
	_, dataOK := v.field("data").str()
	_, mimeOK := v.field("mimeType").str()
	kind, _ := v.field("type").str()
	return v.isRecord() && kind == "image" && dataOK && mimeOK
}

// checkClassifierContext checks a script's classifier context, so mistakes fail with the expected shape instead of a
// provider error.
func checkClassifierContext(context jval) (ai.ClassifierContext, error) {
	fail := func(problem string) error {
		return fmt.Errorf("models.classify() %s. Expected context: %s.", problem, classifierContextShape)
	}
	if !context.isRecord() {
		return ai.ClassifierContext{}, fail("expects a context object as its second argument, got " + describeValue(context))
	}
	if state := context.field("state"); !state.isRecord() {
		return ai.ClassifierContext{}, fail("context.state must be an object, got " + describeValue(state))
	}
	if images := context.field("images"); images.kind() != "undefined" {
		if images.kind() != "array" {
			return ai.ClassifierContext{}, fail("context.images must be an array, got " + describeValue(images))
		}
		for i, image := range images.items() {
			if !isImageBlock(image) {
				return ai.ClassifierContext{}, fail(fmt.Sprintf("context.images[%d] must be an image block, got %s", i, describeValue(image)))
			}
		}
	}
	questions := context.field("questions")
	if !questions.isRecord() || len(questions.keys()) == 0 {
		return ai.ClassifierContext{}, fail("context.questions must map question IDs to questions, got " + describeValue(questions))
	}
	isStrings := func(values []jval) bool {
		return len(values) > 0 && !slices.ContainsFunc(values, func(value jval) bool { _, ok := value.str(); return !ok })
	}
	for _, id := range questions.keys() {
		question := questions.field(id)
		at := "context.questions." + id
		if !question.isRecord() {
			return ai.ClassifierContext{}, fail(at + " must be a question object, got " + describeValue(question))
		}
		if _, ok := question.field("instructions").str(); !ok {
			return ai.ClassifierContext{}, fail(at + ".instructions must be a string")
		}
		criteria := question.field("criteria")
		kind, _ := question.field("type").str()
		switch kind {
		case "choice":
			if !criteria.isRecord() || !isStrings(criteria.values()) {
				return ai.ClassifierContext{}, fail(at + ` is a "choice" question, so criteria must map each label to its meaning`)
			}
		case "score":
			if criteria.kind() != "array" || !isStrings(criteria.items()) {
				return ai.ClassifierContext{}, fail(at + ` is a "score" question, so criteria must list the levels as strings, lowest first`)
			}
		case "bool":
			_, trueOK := criteria.field("true").str()
			_, falseOK := criteria.field("false").str()
			if !criteria.isRecord() || !trueOK || !falseOK {
				return ai.ClassifierContext{}, fail(at + ` is a "bool" question, so criteria must be { true: string, false: string }`)
			}
		default:
			return ai.ClassifierContext{}, fail(at + `.type must be "choice", "score", or "bool", got ` + jsonOrUndefined(question.field("type")))
		}
	}
	var checked ai.ClassifierContext
	if err := json.Unmarshal(context, &checked); err != nil {
		return ai.ClassifierContext{}, err
	}
	return checked, nil
}

// checkImagesContext checks a script's image context, so mistakes such as `{ prompt }` fail with the expected shape.
func checkImagesContext(context jval) (ai.ImagesContext, error) {
	fail := func(problem string) error {
		return fmt.Errorf("models.generateImages() %s. Expected context: { input: [{ type: \"text\", text: <prompt> }, ...optional { type: \"image\", data: <base64>, mimeType } references] }.", problem)
	}
	if !context.isRecord() {
		return ai.ImagesContext{}, fail("expects a context object as its second argument, got " + describeValue(context))
	}
	input := context.field("input")
	blocks := input.items()
	if input.kind() != "array" || len(blocks) == 0 {
		return ai.ImagesContext{}, fail("context.input must be a non-empty array of blocks, got " + describeValue(input))
	}
	for i, block := range blocks {
		kind, _ := block.field("type").str()
		if _, ok := block.field("text").str(); block.isRecord() && kind == "text" && ok {
			continue
		}
		if isImageBlock(block) {
			continue
		}
		return ai.ImagesContext{}, fail(fmt.Sprintf("context.input[%d] must be a text or image block, got %s", i, describeValue(block)))
	}
	var checked ai.ImagesContext
	if err := json.Unmarshal(context, &checked); err != nil {
		return ai.ImagesContext{}, err
	}
	return checked, nil
}

// resolveModel resolves the script's model by provider and id only, so a script-supplied baseUrl or headers never receive
// the credentials, with errors that say what to do.
func resolveModel(name string, modelType ai.ModelType, model jval, c catalog) (ai.AnyModel, error) {
	listHint := fmt.Sprintf("List the %s models you can use with models.getAvailableOfType(\"%s\").", modelType, modelType)
	provider, providerOK := model.field("provider").str()
	id, idOK := model.field("id").str()
	if !model.isRecord() || !providerOK || !idOK {
		hint := ""
		if kind := model.kind(); kind == "undefined" || kind == "null" {
			hint = " models.getModelOfType() returns undefined for an unknown provider or id."
		}
		return nil, fmt.Errorf("%s() expects %s model as its first argument, got %s.%s %s", name, withArticle(string(modelType)), describeValue(model), hint, listHint)
	}
	if resolved := c.GetModelOfType(modelType, provider, id); resolved != nil {
		return resolved, nil
	}
	for _, other := range modelTypes {
		if other != modelType && c.GetModelOfType(other, provider, id) != nil {
			return nil, fmt.Errorf("\"%s/%s\" is %s model, not %s model. %s", provider, id, withArticle(string(other)), withArticle(string(modelType)), listHint)
		}
	}
	return nil, fmt.Errorf("Unknown %s model \"%s/%s\". %s", modelType, provider, id, listHint)
}

// modelResult is the result of a model call as the tool returns it: the whole result for the script, usage counted by the
// Harness.
func modelResult(label string, stopReason, errorMessage string, usage *ai.Usage, value json.RawMessage) (durable.ToolExecutionResult, error) {
	ok := stopReason == "stop"
	text := label + " done"
	if !ok {
		text = label + " " + stopReason + ": " + errorMessage
	}
	var structured any
	if err := json.Unmarshal(value, &structured); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	result := durable.ToolExecutionResult{
		Output:              []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		StructuredOutput:    structured,
		HasStructuredOutput: true,
		Usage:               usage,
	}
	if !ok {
		isError := true
		result.IsError = &isError
	}
	return result, nil
}

const modelCallParameters = `{"type":"object","properties":{"model":{},"context":{}},"required":["model","context"]}`

// createModelTools are the internal tools behind models.classify() and models.generateImages(): paid effects, so each runs
// as a nested durable call with its own task, live row, abort, usage accounting, and replay policy.
func createModelTools() []*durable.ToolRegistration {
	define := func(name, description string, run func(ctx context.Context, model, context jval, c catalog) (durable.ToolExecutionResult, error)) *durable.ToolRegistration {
		schema := mustSchemaOf(name, description, modelCallParameters)
		return harness.DefineTool(durable.ToolRegistration{
			ToolSchema:             schema,
			StructuredOutputSchema: map[string]any{},
			Callers:                []durable.ToolCaller{durable.CallerTools},
			Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				c, err := catalogOf(api.Models())
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				fields, _ := args.(map[string]any)
				model, err := json.Marshal(fields["model"])
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				source, err := callArgumentsJSON(ctx, api)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				request, err := ai.MarshalJSONInSourceOrder(fields["context"], []byte(jval(source).field("context")))
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				if _, defined := fields["model"]; !defined {
					model = nil
				}
				if _, defined := fields["context"]; !defined {
					request = nil
				}
				return run(ctx, jval(model), jval(request), c)
			},
		})
	}
	classify := define(ClassifyTool, "models.classify() of code mode scripts.", func(ctx context.Context, model, request jval, c catalog) (durable.ToolExecutionResult, error) {
		resolved, err := resolveModel("models.classify", ai.ModelTypeClassifier, model, c)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		checked, err := checkClassifierContext(request)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		classifier := resolved.(*ai.ClassifierModel)
		result := c.Classify(ctx, classifier, checked)
		encoded, err := json.Marshal(result)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return modelResult("classify "+classifier.Provider+"/"+classifier.ID, string(result.StopReason), result.ErrorMessage, result.Usage, encoded)
	})
	generate := define(GenerateImagesTool, "models.generateImages() of code mode scripts.", func(ctx context.Context, model, request jval, c catalog) (durable.ToolExecutionResult, error) {
		resolved, err := resolveModel("models.generateImages", ai.ModelTypeImage, model, c)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		checked, err := checkImagesContext(request)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		painter := resolved.(*ai.ImageModel)
		result := c.GenerateImages(ctx, painter, checked)
		encoded, err := json.Marshal(result)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return modelResult("generateImages "+painter.Provider+"/"+painter.ID, string(result.StopReason), result.ErrorMessage, result.Usage, encoded)
	})
	return []*durable.ToolRegistration{classify, generate}
}

// callArgumentsJSON is the arguments of the tool's own call in the member order the script wrote them. Execute gets them
// as a Go map, which has none, while the stored call keeps the script's order (NestedCallOptions.ArgumentsJSON), and the
// order of the context's keys reaches the provider: the order of the questions and of the state's keys.
func callArgumentsJSON(ctx context.Context, api durable.ToolExecutionApi) (json.RawMessage, error) {
	task, err := api.GetTask(ctx, api.TaskId())
	if err != nil || task == nil {
		return nil, err
	}
	input, err := durable.FromJsonValue[harness.ToolTaskInput](task.Input)
	if err != nil || input.Call == nil {
		return nil, err
	}
	return input.Call.ArgumentsJSON()
}
