package ai

// Ports packages/ai/src/api/system-one-shared.ts, typesafe-system-one.ts and cloudflare-workers-ai-system-one.ts,
// the classifier counterpart in providers/cloudflare-stream.ts, and packages/ai/src/providers/compat-schema.ts (SystemOneCompatSchema: SystemOneCompat).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

const (
	ClassifierAPITypesafeSystemOne            ClassifierAPI = "typesafe-system-one"
	ClassifierAPICloudflareWorkersAISystemOne ClassifierAPI = "cloudflare-workers-ai-system-one"
	ClassifierAPILlamaCppClassify             ClassifierAPI = "llama-cpp-classify"
	ClassifierAPIOpenAIDecisions              ClassifierAPI = "openai-decisions"
)

// SystemOneImageAsset is a System One image asset: base64 file bytes, referenced by its name from the state.
type SystemOneImageAsset struct {
	Format     string
	DataBase64 string
}

func (a SystemOneImageAsset) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `{"type":"image","format":%s,"data_base64":%s}`, marshalJSONString(a.Format), marshalJSONString(a.DataBase64)), nil
}

// SystemOneImageAssets are the assets in insertion order, keyed by the names the state references.
type SystemOneImageAssets struct {
	Names  []string
	Assets []SystemOneImageAsset
}

// All yields the assets in insertion order.
func (a SystemOneImageAssets) All() iter.Seq2[string, SystemOneImageAsset] {
	return func(yield func(string, SystemOneImageAsset) bool) {
		for i, name := range a.Names {
			if !yield(name, a.Assets[i]) {
				return
			}
		}
	}
}

// Get returns the asset with the given name.
func (a SystemOneImageAssets) Get(name string) (SystemOneImageAsset, bool) {
	for i, have := range a.Names {
		if have == name {
			return a.Assets[i], true
		}
	}
	return SystemOneImageAsset{}, false
}

func (a SystemOneImageAssets) MarshalJSON() ([]byte, error) {
	return writeJSONObject(len(a.Names), func(i int) string { return a.Names[i] }, func(i int) ([]byte, error) { return a.Assets[i].MarshalJSON() })
}

// SystemOneWireRequest is the System One request body without the transport-specific envelope.
type SystemOneWireRequest struct {
	// State is the context state, or with images a list of the images or their asset names followed by the state.
	State     any
	Questions SystemOneWireQuestions
	// Assets are the images as System One assets, keyed by the names the state references.
	Assets *SystemOneImageAssets `json:",omitempty"`
	// Images are the images as data URLs, Clef's extension to System One. nil is an absent `images` member and an empty slice
	// is an explicit `[]` (system-one-shared.ts SystemOneRequest.images?: string[]); Body and MarshalJSON both keep that difference.
	Images []string
}

// MarshalJSON writes the request's members in field order, with an absent assets or images member left out and an empty images list kept as [].
func (r SystemOneWireRequest) MarshalJSON() ([]byte, error) {
	wire := struct {
		State     any
		Questions SystemOneWireQuestions
		Assets    *SystemOneImageAssets `json:",omitempty"`
		Images    *[]string             `json:",omitempty"`
	}{State: r.State, Questions: r.Questions, Assets: r.Assets}
	if r.Images != nil {
		wire.Images = &r.Images
	}
	return json.Marshal(wire)
}

// Body is the request's members in Pi's insertion order: state, questions, then assets or images.
func (r SystemOneWireRequest) Body() map[string]any {
	body := map[string]any{"state": r.State, "questions": r.Questions}
	if r.Assets != nil {
		body["assets"] = r.Assets
	}
	if r.Images != nil {
		body["images"] = r.Images
	}
	return body
}

// SystemOneImageInput is how a service takes classifier images (SystemOneCompat.ImageInput).
type SystemOneImageInput string

const (
	// SystemOneImageInputAssets is TypeSafe's assets map, with the names placed before the state. PNG and JPEG only.
	SystemOneImageInputAssets SystemOneImageInput = "assets"
	// SystemOneImageInputImages is Clef's images list of data URLs: PNG, JPEG and WebP, at most four.
	SystemOneImageInputImages SystemOneImageInput = "images"
	// SystemOneImageInputStateImageURLs is OpenRouter's form: data URL parts placed before the state.
	SystemOneImageInputStateImageURLs SystemOneImageInput = "state-image-urls"
)

// SystemOneCompat holds the compatibility settings of System One classifier APIs (compat-schema.ts SystemOneCompatSchema).
// An unset field is detected from the provider, baseUrl and model ID.
type SystemOneCompat struct {
	ImageInput SystemOneImageInput `json:"imageInput,omitempty"`
	// SendImageOptInHeader sends TypeSafe-Danger-Zone-Images: true with requests that carry images.
	SendImageOptInHeader *bool `json:"sendImageOptInHeader,omitempty"`
}

// ResolvedSystemOneCompat is SystemOneCompat with every field resolved.
type ResolvedSystemOneCompat struct {
	ImageInput           SystemOneImageInput
	SendImageOptInHeader bool
}

// systemOneImageOptInHeaders is TypeSafe's opt-in header for its experimental image input.
var systemOneImageOptInHeaders = ProviderHeadersFromStrings(map[string]string{"TypeSafe-Danger-Zone-Images": "true"})

// SystemOneWireQuestions are the questions as System One names them: a public bool question is a wire-level noul.
type SystemOneWireQuestions ClassifierQuestions

func (q SystemOneWireQuestions) MarshalJSON() ([]byte, error) {
	return writeJSONObject(len(q), func(i int) string { return q[i].ID }, func(i int) ([]byte, error) {
		if bool, ok := q[i].Question.(ClassifierBoolQuestion); ok {
			return fmt.Appendf(nil, `{"type":"noul","instructions":%s,"criteria":{"true":%s,"false":%s}}`, marshalJSONString(bool.Instructions), marshalJSONString(bool.Criteria.True), marshalJSONString(bool.Criteria.False)), nil
		}
		return json.Marshal(q[i].Question)
	})
}

// SystemOneTransport holds the differences between services that serve System One models.
type SystemOneTransport struct {
	// API is the classifier API this transport implements.
	API ClassifierAPI
	// DetectCompat gives the settings of models that do not set them in Compat.
	DetectCompat func(model ClassifierModel) ResolvedSystemOneCompat
	// Label names the service in error messages.
	Label string
	URL   func(model ClassifierModel) string
	// Payload wraps the System One request in the service's request envelope.
	Payload func(model ClassifierModel, request SystemOneWireRequest) map[string]any
	// Output extracts the System One output ({ answers, usage }) from the service's response envelope.
	Output func(body json.RawMessage) (map[string]json.RawMessage, error)
}

func RequiredNumber(label string, value json.RawMessage, field string) (float64, error) {
	number, ok := finiteNumberOf(value)
	if !ok {
		return 0, fmt.Errorf("%s returned an invalid %s", label, field)
	}
	return number, nil
}

func parseProbabilities(label string, raw json.RawMessage, id string) ([]ClassifierProbability, error) {
	if !IsRecord(raw) {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", label, id)
	}
	entries, err := orderedJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", label, id)
	}
	// Object.entries visits the parsed object in JavaScript key order, so the first invalid probability reported is the
	// first in that order, not in source order (system-one-shared.ts:70-78).
	keys := make([]string, len(entries))
	for i, entry := range entries {
		keys[i] = entry.key
	}
	probabilities := make([]ClassifierProbability, 0, len(entries))
	for _, index := range jsObjectKeyOrder(keys) {
		entry := entries[index]
		probability, err := RequiredNumber(label, entry.value, fmt.Sprintf("probability for %s.%s", id, entry.key))
		if err != nil {
			return nil, err
		}
		probabilities = append(probabilities, ClassifierProbability{Key: entry.key, Probability: probability})
	}
	return probabilities, nil
}

// orderProbabilities applies the key order of a JavaScript object built from these entries.
func orderProbabilities(probabilities []ClassifierProbability) []ClassifierProbability {
	keys := make([]string, len(probabilities))
	for i, probability := range probabilities {
		keys[i] = probability.Key
	}
	ordered := make([]ClassifierProbability, len(probabilities))
	for i, index := range jsObjectKeyOrder(keys) {
		ordered[i] = probabilities[index]
	}
	return ordered
}

// orderAnswers applies the key order of a JavaScript object built from these entries.
func orderAnswers(answers ClassifierAnswers) ClassifierAnswers {
	keys := make([]string, len(answers))
	for i, answer := range answers {
		keys[i] = answer.ID
	}
	ordered := make(ClassifierAnswers, len(answers))
	for i, index := range jsObjectKeyOrder(keys) {
		ordered[i] = answers[index]
	}
	return ordered
}

func parseAnswers(label string, raw json.RawMessage, request ClassifierContext) (ClassifierAnswers, error) {
	value, ok := jsonObjectOf(raw)
	if !ok {
		return nil, fmt.Errorf("%s returned an unexpected response", label)
	}
	answers := make(ClassifierAnswers, 0, len(request.Questions))
	for _, entry := range request.Questions {
		id := entry.ID
		answer, ok := jsonObjectOf(value[id])
		if !ok {
			return nil, fmt.Errorf("%s did not return an answer for %s", label, id)
		}
		answerType, _ := jsonStringOf(answer["type"])
		switch entry.Question.(type) {
		case ClassifierChoiceQuestion:
			choice, isString := jsonStringOf(answer["choice"])
			if answerType != "choice" || !isString {
				return nil, fmt.Errorf("%s did not return a choice answer for %s", label, id)
			}
			probabilities, err := parseProbabilities(label, answer["probabilities"], id)
			if err != nil {
				return nil, err
			}
			confidence, err := RequiredNumber(label, answer["confidence"], "confidence for "+id)
			if err != nil {
				return nil, err
			}
			answers = append(answers, ClassifierAnswerEntry{ID: id, Answer: ClassifierChoiceAnswer{Choice: choice, Probabilities: probabilities, Confidence: confidence}})
		case ClassifierScoreQuestion:
			if answerType != "score" {
				return nil, fmt.Errorf("%s did not return a score answer for %s", label, id)
			}
			score, err := RequiredNumber(label, answer["score"], "score for "+id)
			if err != nil {
				return nil, err
			}
			confidence, err := RequiredNumber(label, answer["confidence"], "confidence for "+id)
			if err != nil {
				return nil, err
			}
			answers = append(answers, ClassifierAnswerEntry{ID: id, Answer: ClassifierScoreAnswer{Score: score, Confidence: confidence}})
		default:
			if answerType != "noul" {
				return nil, fmt.Errorf("%s did not return a bool answer for %s", label, id)
			}
			probability, err := RequiredNumber(label, answer["noul"], "probability for "+id)
			if err != nil {
				return nil, err
			}
			answers = append(answers, ClassifierAnswerEntry{ID: id, Answer: ClassifierBoolAnswer{Probability: probability}})
		}
	}
	return orderAnswers(answers), nil
}

// ClassifySystemOne runs one System One classification over the given transport.
func ClassifySystemOne(ctx context.Context, transport SystemOneTransport, model ClassifierModel, request ClassifierContext, options ClassifierOptions) ClassifierResult {
	//portlint:allow clock system-one-shared.ts:111 `timestamp: Date.now()`; ai's clock seams are per-struct (oauth, bedrock backoff), none covers classifiers
	output := ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, Answers: ClassifierAnswers{}, StopReason: ClassifierStopReasonStop, Timestamp: time.Now().UnixMilli()}
	fail := func(err error) ClassifierResult {
		output.StopReason = ClassifierStopReasonError
		if ctx.Err() != nil {
			output.StopReason = ClassifierStopReasonAborted
		}
		output.ErrorMessage = classifierErrorMessage(err, transport.Label)
		return output
	}
	if model.API != transport.API {
		return fail(fmt.Errorf("Unsupported classifier API: %s", model.API))
	}
	wire := SystemOneWireRequest{State: request.State, Questions: SystemOneWireQuestions(request.Questions)}
	requestOptions := options
	if len(request.Images) > 0 {
		if !slices.Contains(model.Input, "image") {
			return fail(fmt.Errorf("%s does not support image input for %s", transport.Label, model.ID))
		}
		detected := transport.DetectCompat(model)
		imageInput := detected.ImageInput
		sendHeader := detected.SendImageOptInHeader
		if model.Compat != nil {
			if model.Compat.ImageInput != "" {
				imageInput = model.Compat.ImageInput
			}
			if model.Compat.SendImageOptInHeader != nil {
				sendHeader = *model.Compat.SendImageOptInHeader
			}
		}
		if err := addSystemOneImages(transport.Label, model, imageInput, request.Images, &wire); err != nil {
			return fail(err)
		}
		if sendHeader {
			requestOptions.Headers = MergeProviderHeaders(systemOneImageOptInHeaders, options.Headers)
		}
	}
	envelope, err := PostClassifierRequest(ctx, transport.Label, transport.URL(model), model, transport.Payload(model, wire), requestOptions, nil)
	if err != nil {
		return fail(err)
	}
	result, err := transport.Output(envelope)
	if err != nil {
		return fail(err)
	}
	// Set before parsing answers: a request with malformed answers was still billed.
	output.Usage = ParseClassifierUsage(result["usage"], model)
	answers, err := parseAnswers(transport.Label, result["answers"], request)
	if err != nil {
		return fail(err)
	}
	output.Answers = answers
	return output
}

var systemOneAssetFormats = map[string]string{"image/png": "png", "image/jpeg": "jpeg"}

// maxSystemOneDataURLImages is how many images Clef accepts per request.
const maxSystemOneDataURLImages = 4

// addSystemOneImages adds the context images to request in the service's form (system-one-shared.ts addImages).
func addSystemOneImages(label string, model ClassifierModel, input SystemOneImageInput, images []ImageContent, request *SystemOneWireRequest) error {
	switch input {
	case SystemOneImageInputImages:
		if len(images) > maxSystemOneDataURLImages {
			return fmt.Errorf("%s accepts at most %d images for %s, got %d", label, maxSystemOneDataURLImages, model.ID, len(images))
		}
		urls := make([]string, len(images))
		for i, image := range images {
			if image.MimeType != "image/png" && image.MimeType != "image/jpeg" && image.MimeType != "image/webp" {
				return fmt.Errorf("%s accepts PNG, JPEG, and WebP images for %s, got %s", label, model.ID, image.MimeType)
			}
			urls[i] = "data:" + image.MimeType + ";base64," + image.Data
		}
		request.Images = urls
		return nil
	case SystemOneImageInputStateImageURLs:
		// OpenRouter validates the images against the model that serves the request.
		state := make([]any, 0, len(images)+1)
		for _, image := range images {
			state = append(state, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + image.MimeType + ";base64," + image.Data}})
		}
		state = append(state, request.State)
		request.State = state
		return nil
	}
	assets := &SystemOneImageAssets{}
	for i, image := range images {
		format, ok := systemOneAssetFormats[image.MimeType]
		if !ok {
			return fmt.Errorf("%s accepts PNG and JPEG images for %s, got %s", label, model.ID, image.MimeType)
		}
		assets.Names = append(assets.Names, fmt.Sprintf("<image-%d>", i+1))
		assets.Assets = append(assets.Assets, SystemOneImageAsset{Format: format, DataBase64: image.Data})
	}
	request.Assets = assets
	// Every asset must be referenced from the request. The names go before the state, where Clef also places its images.
	state := make([]any, 0, len(assets.Names)+1)
	for _, name := range assets.Names {
		state = append(state, name)
	}
	state = append(state, request.State)
	request.State = state
	return nil
}

// serviceURL is `new URL(path, \`${baseUrl.replace(/\/+$/u, "")}/\`)` as the System One and decisions transports build it. Pi's constructor throws inside the request's try block; the URL field returns a
// string, so an invalid base URL comes back concatenated and the request then fails on it.
func serviceURL(baseURL, path string) string {
	resolved, err := resolveServiceURL(baseURL, path)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/" + path
	}
	return resolved
}

func resolveServiceURL(baseURL, path string) (string, error) {
	base, err := nodeurl.ParseURL(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return "", err
	}
	resolved, err := base.ResolveRelativePath(path)
	if err != nil {
		return "", err
	}
	return resolved.Href(), nil
}

// typesafeSystemOneTransport is TypeSafe's native System One protocol. OpenRouter serves the same protocol, so both
// providers use this API with different base URLs.
var typesafeSystemOneTransport = SystemOneTransport{
	API:   ClassifierAPITypesafeSystemOne,
	Label: "System One API",
	// OpenRouter by provider ID, which survives baseUrl overrides, or by host, which covers regional endpoints.
	DetectCompat: func(model ClassifierModel) ResolvedSystemOneCompat {
		//portlint:allow providerliteral typesafe-system-one.ts:20 detectCompat tests model.provider === "openrouter" || model.baseUrl.includes("openrouter.ai")
		if model.Provider == "openrouter" || strings.Contains(model.BaseURL, "openrouter.ai") {
			return ResolvedSystemOneCompat{ImageInput: SystemOneImageInputStateImageURLs}
		}
		return ResolvedSystemOneCompat{ImageInput: SystemOneImageInputAssets, SendImageOptInHeader: true}
	},
	URL: func(model ClassifierModel) string { return serviceURL(model.BaseURL, "systemone") },
	Payload: func(model ClassifierModel, request SystemOneWireRequest) map[string]any {
		body := request.Body()
		body["model"] = model.ID
		return body
	},
	Output: func(body json.RawMessage) (map[string]json.RawMessage, error) {
		object, ok := jsonObjectOf(body)
		if !ok {
			return nil, errors.New("System One API returned an unexpected response")
		}
		return object, nil
	},
}

// ClassifyTypesafeSystemOne runs TypeSafe's native System One protocol with public bool values mapped to wire-level
// noul. OpenRouter, OpenCode Zen and Vercel AI Gateway serve the same protocol at other base URLs.
func ClassifyTypesafeSystemOne(ctx context.Context, model ClassifierModel, request ClassifierContext, options ClassifierOptions) ClassifierResult {
	return ClassifySystemOne(ctx, typesafeSystemOneTransport, model, request, options)
}

const cloudflareSystemOneLabel = "Cloudflare Workers AI"

func cloudflareErrorMessage(errors json.RawMessage) string {
	if entries, ok := jsonArrayOf(errors); ok {
		var messages []string
		for _, entry := range entries {
			if object, ok := jsonObjectOf(entry); ok {
				if message, ok := jsonStringOf(object["message"]); ok {
					messages = append(messages, message)
				}
			}
		}
		if len(messages) > 0 {
			return cloudflareSystemOneLabel + " error: " + strings.Join(messages, "; ")
		}
	}
	return cloudflareSystemOneLabel + " request failed"
}

// cloudflareSystemOneTransport serves System One models on the Workers AI REST endpoint: POST /accounts/{account}/ai/run
// with { model, input }. The REST API wraps the model output in Cloudflare's API envelope. Third-party models such as
// typesafe/jev add a run record: { success, result: { state: "Completed", result: { answers, usage } } }. Cloudflare-hosted
// models such as @cf/cloudflare/clef return the output directly: { success, result: { model, answers, usage } }.
var cloudflareSystemOneTransport = SystemOneTransport{
	API:   ClassifierAPICloudflareWorkersAISystemOne,
	Label: cloudflareSystemOneLabel,
	// Clef models take images as an images list of data URLs; TypeSafe's models take System One assets without the opt-in header.
	DetectCompat: func(model ClassifierModel) ResolvedSystemOneCompat {
		if strings.HasPrefix(model.ID, "@cf/cloudflare/clef") {
			return ResolvedSystemOneCompat{ImageInput: SystemOneImageInputImages}
		}
		return ResolvedSystemOneCompat{ImageInput: SystemOneImageInputAssets}
	},
	URL: func(model ClassifierModel) string { return serviceURL(model.BaseURL, "run") },
	Payload: func(model ClassifierModel, request SystemOneWireRequest) map[string]any {
		return map[string]any{"model": model.ID, "input": request.Body()}
	},
	Output: func(body json.RawMessage) (map[string]json.RawMessage, error) {
		object, ok := jsonObjectOf(body)
		if !ok {
			return nil, errors.New(cloudflareSystemOneLabel + " returned an unexpected response")
		}
		if string(bytes.TrimSpace(object["success"])) == "false" {
			return nil, errors.New(cloudflareErrorMessage(object["errors"]))
		}
		run, ok := jsonObjectOf(object["result"])
		if !ok {
			return nil, errors.New(cloudflareSystemOneLabel + " returned an unexpected response")
		}
		// upstream: packages/ai/src/api/cloudflare-workers-ai-system-one.ts:transport.Output (`"answers" in result`).
		if _, direct := run["answers"]; direct {
			return run, nil
		}
		if state, ok := jsonStringOf(run["state"]); !ok || state != "Completed" {
			return nil, fmt.Errorf("%s run did not complete (state: %s)", cloudflareSystemOneLabel, jsStateString(run["state"]))
		}
		result, ok := jsonObjectOf(run["result"])
		if !ok {
			return nil, errors.New(cloudflareSystemOneLabel + " returned an unexpected response")
		}
		return result, nil
	},
}

// jsStateString is String(value) for a parsed JSON value; an absent member is undefined.
func jsStateString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "undefined"
	}
	return jsJSONValueString(trimmed)
}

// jsJSONValueString is String(value) for a present JSON value: objects are "[object Object]", arrays join their
// elements with "," (null elements are empty) and numbers use JavaScript's number formatting.
func jsJSONValueString(raw json.RawMessage) string {
	if text, ok := jsonStringOf(raw); ok {
		return text
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && (trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9')) {
		return jsnumber.String(jsnumber.FromJSON(trimmed))
	}
	if elements, ok := jsonArrayOf(raw); ok {
		parts := make([]string, len(elements))
		for i, element := range elements {
			if element = bytes.TrimSpace(element); string(element) != "null" {
				parts[i] = jsJSONValueString(element)
			}
		}
		return strings.Join(parts, ",")
	}
	if IsRecord(raw) {
		return "[object Object]"
	}
	return string(bytes.TrimSpace(raw))
}

// ClassifyCloudflareWorkersAISystemOne runs System One models on the Workers AI REST endpoint, with public bool values
// mapped to wire-level noul.
func ClassifyCloudflareWorkersAISystemOne(ctx context.Context, model ClassifierModel, request ClassifierContext, options ClassifierOptions) ClassifierResult {
	return ClassifySystemOne(ctx, cloudflareSystemOneTransport, model, request, options)
}

func classifierModule(classify ClassifierFunction) *ProviderClassifier {
	return &ProviderClassifier{Classify: func(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		return classify(ctx, *model, request, options), nil
	}}
}

// TypesafeSystemOneAPI is the typesafe-system-one classifier module.
func TypesafeSystemOneAPI() *ProviderClassifier { return classifierModule(ClassifyTypesafeSystemOne) }

// CloudflareWorkersAISystemOneAPI is the cloudflare-workers-ai-system-one classifier module.
func CloudflareWorkersAISystemOneAPI() *ProviderClassifier {
	return classifierModule(ClassifyCloudflareWorkersAISystemOne)
}

// CloudflareClassifier wraps a classifier module so Cloudflare account and gateway endpoint placeholders materialize
// from the resolved provider env before dispatch (cloudflare-stream.ts:31-36). The classifier receives model itself when nothing changes.
func CloudflareClassifier(classifier *ProviderClassifier) *ProviderClassifier {
	return &ProviderClassifier{Classify: func(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		if baseURL := resolveCloudflarePlaceholders(model.BaseURL, options.Env); baseURL != model.BaseURL {
			resolved := *model
			resolved.BaseURL = baseURL
			model = &resolved
		}
		return classifier.Classify(ctx, model, request, options)
	}}
}
