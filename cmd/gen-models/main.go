// Command gen-models reads upstream pi's catalog and emits ai/models_generated.go. Optional raw models.dev and OpenRouter API snapshots regenerate verified effort controls before emission.
//
// Run via `go generate ./ai/...` or directly:
//
//	go run ./cmd/gen-models \
//	  -src .upstream/current/packages/ai/src/models.generated.ts \
//	  -out ai/models_generated.go
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

type modelRow struct {
	Type             string
	ID               string
	Provider         string
	Name             string
	API              string
	BaseURL          string
	Headers          map[string]string
	Compat           *ModelCompat
	ThinkingLevelMap map[string]*string
	SamplingParams   map[string]any
	PromptCache      map[string]int
	InputLimits      *jsonModelInputLimits
	ContextWindow    int
	MaxTokens        int
	InputCost        float64
	OutputCost       float64
	CacheRead        float64
	CacheWrite       float64
	Tiers            []jsonCostTier
	Reasoning        bool
	Inputs           []string // text/image/audio/video
	Output           []string // image models: the modalities the model returns
	Enabled          *bool
	Lab              string
	Providers        []jsonCatalogProvider
}

type ModelCompat struct {
	SupportsDeveloperRole                       *bool                      `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort                     *bool                      `json:"supportsReasoningEffort,omitempty"`
	SupportsStore                               *bool                      `json:"supportsStore,omitempty"`
	SupportsUsageInStreaming                    *bool                      `json:"supportsUsageInStreaming,omitempty"`
	MaxTokensField                              string                     `json:"maxTokensField,omitempty"`
	RequiresToolResultName                      *bool                      `json:"requiresToolResultName,omitempty"`
	RequiresAssistantAfterToolResult            *bool                      `json:"requiresAssistantAfterToolResult,omitempty"`
	RequiresThinkingAsText                      *bool                      `json:"requiresThinkingAsText,omitempty"`
	RequiresReasoningContentOnAssistantMessages *bool                      `json:"requiresReasoningContentOnAssistantMessages,omitempty"`
	ThinkingFormat                              string                     `json:"thinkingFormat,omitempty"`
	CacheControlFormat                          string                     `json:"cacheControlFormat,omitempty"`
	SendSessionAffinityHeaders                  *bool                      `json:"sendSessionAffinityHeaders,omitempty"`
	SupportsStrictMode                          *bool                      `json:"supportsStrictMode,omitempty"`
	SupportsStrictTools                         *bool                      `json:"supportsStrictTools,omitempty"`
	SupportsOpenAIGrammarTools                  *bool                      `json:"supportsOpenAIGrammarTools,omitempty"`
	SupportsExplicitPromptCacheMode             *bool                      `json:"supportsExplicitPromptCacheMode,omitempty"`
	ReasoningEffortMap                          map[string]string          `json:"reasoningEffortMap,omitempty"`
	OpenRouterRouting                           map[string]any             `json:"openRouterRouting,omitempty"`
	VercelGatewayRouting                        map[string]any             `json:"vercelGatewayRouting,omitempty"`
	ZaiToolStream                               *bool                      `json:"zaiToolStream,omitempty"`
	SupportsLongCacheRetention                  *bool                      `json:"supportsLongCacheRetention,omitempty"`
	SendSessionIdHeader                         *bool                      `json:"sendSessionIdHeader,omitempty"`
	SupportsEagerToolInputStreaming             *bool                      `json:"supportsEagerToolInputStreaming,omitempty"`
	ForceAdaptiveThinking                       *bool                      `json:"forceAdaptiveThinking,omitempty"`
	SupportsTemperature                         *bool                      `json:"supportsTemperature,omitempty"`
	AllowEmptySignature                         *bool                      `json:"allowEmptySignature,omitempty"`
	SupportsCacheControlOnTools                 *bool                      `json:"supportsCacheControlOnTools,omitempty"`
	DeferredToolsMode                           string                     `json:"deferredToolsMode,omitempty"`
	SessionAffinityFormat                       string                     `json:"sessionAffinityFormat,omitempty"`
	SupportsToolSearch                          *bool                      `json:"supportsToolSearch,omitempty"`
	SupportsToolReferences                      *bool                      `json:"supportsToolReferences,omitempty"`
	ChatTemplateArgs                            map[string]any             `json:"chatTemplateArgs,omitempty"`
	SupportsThinkingTokenBudget                 *bool                      `json:"supportsThinkingTokenBudget,omitempty"`
	ThinkingTokenBudgetField                    string                     `json:"thinkingTokenBudgetField,omitempty"`
	SupportsAdditionalTools                     *bool                      `json:"supportsAdditionalTools,omitempty"`
	SupportsMidConvoEffort                      *bool                      `json:"supportsMidConvoEffort,omitempty"`
	SupportsMidConvoSystemMessages              *bool                      `json:"supportsMidConvoSystemMessages,omitempty"`
	SupportsMidConvoToolAdditions               *bool                      `json:"supportsMidConvoToolAdditions,omitempty"`
	SupportsMidConvoToolChanges                 *bool                      `json:"supportsMidConvoToolChanges,omitempty"`
	AllowedFallbackModels                       []jsonAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
}

var (
	// reBarrelImport matches a per-provider import in the catalog barrel
	// (models.generated.ts/.js), e.g.
	//   import { ANTHROPIC_CLASSIFIER_MODELS, ANTHROPIC_IMAGE_MODELS, ANTHROPIC_MODELS } from "./providers/anthropic.models.ts";
	// The captured path keeps the extension so the same code resolves the
	// .ts git-tag mirror and the .js npm dist.
	reBarrelImport = regexp.MustCompile(`^import \{[^}]*\} from "(\./providers/[^"]+\.models\.(?:ts|js))";`)

	// reDataImport matches the provider shard's import of its data file, e.g.
	//   import values from "./data/anthropic.json" with { type: "json" };
	reDataImport = regexp.MustCompile(`import\s+values\s+from\s+"(\./data/[^"]+\.json)"`)
)

func main() {
	src := flag.String("src", "", "path to upstream models.generated.ts or .js")
	out := flag.String("out", "ai/models_generated.go", "output Go file for chat models")
	imageOut := flag.String("image-out", "", "output Go file for image models (default image_models_generated.go beside -out)")
	classifierOut := flag.String("classifier-out", "", "output Go file for classifier models (default classifier_models_generated.go beside -out)")
	providerOut := flag.String("provider-out", "", "output Go file for the barrel's provider ids (default providers_generated.go beside -out)")
	modelsDev := flag.String("models-dev", "", "raw models.dev API JSON snapshot for verified reasoning controls")
	openRouter := flag.String("openrouter", "", "raw OpenRouter models API JSON snapshot for reasoning controls")
	strict := flag.Bool("strict", false, "validate models.dev Individual model membership before publication")
	jsonOnly := flag.Bool("json-only", false, "emit the models.dev Fireworks and Qwen Token Plan stages as JSON without changing Go catalogs")
	jsonOutput := flag.String("json-output", "", "output directory for models.dev JSON catalogs")
	pretty := flag.Bool("pretty", false, "indent JSON catalog output")
	flag.Parse()
	if *src == "" && (*strict || *jsonOnly || *jsonOutput != "") {
		if *jsonOnly && *jsonOutput == "" {
			fmt.Fprintln(os.Stderr, "--json-only requires --json-output")
			os.Exit(1)
		}
		if err := generateModelsDev(context.Background(), modelsDevGeneratorOptions{Strict: *strict, JSONOnly: *jsonOnly, JSONOutput: *jsonOutput, Pretty: *pretty, GoOutput: *out}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *src == "" {
		fmt.Fprintln(os.Stderr, "gen-models: -src is required")
		os.Exit(2)
	}
	if *imageOut == "" {
		*imageOut = filepath.Join(filepath.Dir(*out), "image_models_generated.go")
	}
	if *classifierOut == "" {
		*classifierOut = filepath.Join(filepath.Dir(*out), "classifier_models_generated.go")
	}

	if *providerOut == "" {
		*providerOut = filepath.Join(filepath.Dir(*out), "providers_generated.go")
	}

	catalog, err := collectCatalog(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	if len(catalog.Chat) == 0 {
		fmt.Fprintln(os.Stderr, "gen-models: parsed 0 models: refusing to clobber output")
		os.Exit(1)
	}
	if err := applyVendorReasoning(catalog.Chat, *modelsDev, *openRouter); err != nil {
		fmt.Fprintln(os.Stderr, "reasoning:", err)
		os.Exit(1)
	}
	if err := emit(*out, *src, catalog.Chat); err != nil {
		fmt.Fprintln(os.Stderr, "emit:", err)
		os.Exit(1)
	}
	if err := emitImages(*imageOut, *src, catalog.Image); err != nil {
		fmt.Fprintln(os.Stderr, "emit images:", err)
		os.Exit(1)
	}
	if err := emitClassifiers(*classifierOut, *src, catalog.Classifier); err != nil {
		fmt.Fprintln(os.Stderr, "emit classifiers:", err)
		os.Exit(1)
	}
	if err := emitProviders(*providerOut, *src, catalog.Providers); err != nil {
		fmt.Fprintln(os.Stderr, "emit providers:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "gen-models: wrote %d chat models to %s, %d image models to %s and %d classifier models to %s\n", len(catalog.Chat), *out, len(catalog.Image), *imageOut, len(catalog.Classifier), *classifierOut)
}

// modelCatalog holds the flattened catalog of every provider shard, each list in barrel order. Providers holds the barrel's
// provider ids.
type modelCatalog struct {
	Chat, Image, Classifier []modelRow
	Providers               []string
}

// collectCatalog reads the catalog barrel at src (models.generated.ts or .js) and, for every provider shard it imports,
// the shard's data/<provider>.json. The rows of each type keep the barrel's provider order and the data's insertion order
// because upstream exposes catalog order to callers (models.generated.ts, providers/<provider>.models.ts,
// model-catalog.ts flattenChatModelCatalog, flattenImageModelCatalog and flattenClassifierModelCatalog).
func collectCatalog(src string) (modelCatalog, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return modelCatalog{}, err
	}
	dir := filepath.Dir(src)
	var catalog modelCatalog
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		m := reBarrelImport.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		shardPath := filepath.Join(dir, filepath.FromSlash(m[1]))
		provider := strings.TrimSuffix(filepath.Base(shardPath), filepath.Ext(shardPath))
		provider = strings.TrimSuffix(provider, ".models")
		shard, err := os.ReadFile(shardPath)
		if err != nil {
			return modelCatalog{}, err
		}
		dm := reDataImport.FindSubmatch(shard)
		if dm == nil {
			return modelCatalog{}, fmt.Errorf("provider %s: no data/<provider>.json import", shardPath)
		}
		rows, err := parseDataJSON(filepath.Join(filepath.Dir(shardPath), filepath.FromSlash(string(dm[1]))))
		if err != nil {
			return modelCatalog{}, fmt.Errorf("provider %s: %w", shardPath, err)
		}
		for _, row := range rows {
			if row.Provider != provider {
				return modelCatalog{}, fmt.Errorf("provider %s: model %s:%s has provider %q, expected %q", shardPath, row.Type, row.ID, row.Provider, provider)
			}
			if fields := unsupportedFields(row); len(fields) > 0 {
				return modelCatalog{}, fmt.Errorf("provider %s: %s model %s has fields the Go %s model cannot carry: %s", shardPath, row.Type, row.ID, row.Type, strings.Join(fields, ", "))
			}
		}
		catalog.Providers = append(catalog.Providers, provider)
		catalog.Chat = append(catalog.Chat, flattenModelCatalog(rows, "chat")...)
		catalog.Image = append(catalog.Image, flattenModelCatalog(rows, "image")...)
		catalog.Classifier = append(catalog.Classifier, flattenModelCatalog(rows, "classifier")...)
	}
	if err := sc.Err(); err != nil {
		return modelCatalog{}, err
	}
	return catalog, nil
}

// flattenModelCatalog selects the models of one type (model-catalog.ts flattenModelCatalog). Object.fromEntries
// enumerates canonical array-index ids before every other id, so a numeric model id moves to the front in ascending order.
func flattenModelCatalog(rows []modelRow, modelType string) []modelRow {
	var out []modelRow
	for _, row := range rows {
		if row.Type == modelType {
			out = append(out, row)
		}
	}
	indexID := func(row modelRow) (uint64, bool) {
		n, err := strconv.ParseUint(row.ID, 10, 32)
		return n, err == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == row.ID
	}
	slices.SortStableFunc(out, func(a, b modelRow) int {
		x, xok := indexID(a)
		y, yok := indexID(b)
		switch {
		case xok && yok:
			return cmp.Compare(x, y)
		case xok:
			return -1
		case yok:
			return 1
		}
		return 0
	})
	return out
}

// jsonModel mirrors every current field of a data/<provider>.json model entry.
// JSON decoding rejects unknown fields so a future upstream capability cannot
// silently disappear from the generated Go catalog.
type jsonModel struct {
	Type             string                `json:"type"`
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	API              string                `json:"api"`
	Provider         string                `json:"provider"`
	BaseURL          string                `json:"baseUrl"`
	Reasoning        bool                  `json:"reasoning"`
	Input            []string              `json:"input"`
	Output           []string              `json:"output"`
	Cost             *jsonCost             `json:"cost"`
	PromptCache      map[string]int        `json:"promptCache"`
	InputLimits      *jsonModelInputLimits `json:"inputLimits"`
	ContextWindow    int                   `json:"contextWindow"`
	MaxTokens        int                   `json:"maxTokens"`
	Headers          map[string]string     `json:"headers"`
	Compat           *ModelCompat          `json:"compat"`
	ThinkingLevelMap map[string]*string    `json:"thinkingLevelMap"`
	SamplingParams   map[string]any        `json:"samplingParams"`
	Enabled          *bool                 `json:"enabled"`
	Lab              string                `json:"lab"`
	Providers        []jsonCatalogProvider `json:"providers"`
}

type jsonModelInputLimits struct {
	MaxRequestBytes jsonOptionalInt       `json:"maxRequestBytes"`
	Images          *jsonModelImageLimits `json:"images"`
}

type jsonModelImageLimits struct {
	Resize        *jsonModelImageResize `json:"resize"`
	MaxPerMessage jsonOptionalInt       `json:"maxPerMessage"`
	MaxPerRequest jsonOptionalInt       `json:"maxPerRequest"`
}

type jsonModelImageResize struct {
	MaxWidth    jsonOptionalInt `json:"maxWidth"`
	MaxHeight   jsonOptionalInt `json:"maxHeight"`
	MaxBytes    jsonOptionalInt `json:"maxBytes"`
	JPEGQuality jsonOptionalInt `json:"jpegQuality"`
}

type jsonOptionalInt struct {
	Value   int
	Present bool
}

func (value *jsonOptionalInt) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("must be an integer")
	}
	var decoded int
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = decoded
	value.Present = true
	return nil
}

type jsonCost struct {
	Input      float64        `json:"input"`
	Output     float64        `json:"output"`
	CacheRead  float64        `json:"cacheRead"`
	CacheWrite float64        `json:"cacheWrite"`
	Tiers      []jsonCostTier `json:"tiers,omitempty"`
}

// jsNumber marshals like JSON.stringify: a number that is not finite is null, and negative zero is 0.
type jsNumber float64

func (n jsNumber) MarshalJSON() ([]byte, error) {
	return jsnumber.JSON(float64(n)), nil
}

// MarshalJSON writes the cost the way JSON.stringify writes it, so a NaN price (an unparsable OpenRouter price string) is null.
func (c jsonCost) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Input      jsNumber       `json:"input"`
		Output     jsNumber       `json:"output"`
		CacheRead  jsNumber       `json:"cacheRead"`
		CacheWrite jsNumber       `json:"cacheWrite"`
		Tiers      []jsonCostTier `json:"tiers,omitempty"`
	}{jsNumber(c.Input), jsNumber(c.Output), jsNumber(c.CacheRead), jsNumber(c.CacheWrite), c.Tiers})
}

type jsonCostTier struct {
	InputTokensAbove int     `json:"inputTokensAbove"`
	Input            float64 `json:"input"`
	Output           float64 `json:"output"`
	CacheRead        float64 `json:"cacheRead"`
	CacheWrite       float64 `json:"cacheWrite"`
}

type jsonAllowedFallbackModel struct {
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Cost     jsonCost `json:"cost"`
}

type jsonCatalogProvider struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Credential string `json:"credential"`
	Source     string `json:"source"`
}

// parseDataJSON reads one data/<provider>.json: API groups of "<type>:<id>" keyed models. Every model object is decoded
// strictly so new catalog fields fail generation instead of disappearing from Pig. Identity follows
// packages/ai/scripts/model-data.ts validateModelValue and validateModelDataDirectory: a known type, a key that is
// "<type>:<id>", and no key repeated across API groups.
func parseDataJSON(path string) ([]modelRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode catalog root: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("catalog root is %v, want object", token)
	}
	var models []jsonModel
	seen := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		api, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("catalog key is %T, want string", keyToken)
		}
		var encoded json.RawMessage
		if err := decoder.Decode(&encoded); err != nil {
			return nil, fmt.Errorf("%s: %w", api, err)
		}
		group, err := decodeJSONModelGroup(encoded, seen)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", api, err)
		}
		models = append(models, group...)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	rows := make([]modelRow, 0, len(models))
	for _, jm := range models {
		row := modelRow{
			Type:             jm.Type,
			ID:               jm.ID,
			Provider:         jm.Provider,
			Name:             jm.Name,
			API:              jm.API,
			BaseURL:          jm.BaseURL,
			Headers:          jm.Headers,
			Compat:           normalizeCompat(jm.Compat),
			ThinkingLevelMap: jm.ThinkingLevelMap,
			SamplingParams:   jm.SamplingParams,
			PromptCache:      jm.PromptCache,
			InputLimits:      jm.InputLimits,
			ContextWindow:    jm.ContextWindow,
			MaxTokens:        jm.MaxTokens,
			Reasoning:        jm.Reasoning,
			Inputs:           jm.Input,
			Output:           jm.Output,
			Enabled:          jm.Enabled,
			Lab:              jm.Lab,
			Providers:        jm.Providers,
		}
		if jm.Cost != nil {
			row.InputCost = jm.Cost.Input
			row.OutputCost = jm.Cost.Output
			row.CacheRead = jm.Cost.CacheRead
			row.CacheWrite = jm.Cost.CacheWrite
			row.Tiers = jm.Cost.Tiers
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// decodeJSONModelGroup decodes one API group. seen maps every "<type>:<id>" key of the file to its API group.
func decodeJSONModelGroup(data []byte, seen map[string]string) ([]jsonModel, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode model group: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("model group root is %v, want object", token)
	}
	var models []jsonModel
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("model key is %T, want string", keyToken)
		}
		var encoded json.RawMessage
		if err := decoder.Decode(&encoded); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		model, err := decodeJSONModel(encoded)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if key != model.Type+":"+model.ID {
			return nil, fmt.Errorf("%s: mismatched type/id identity (model is %s:%s)", key, model.Type, model.ID)
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s appears in more than one API group", key)
		}
		seen[key] = model.API
		models = append(models, model)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return models, nil
}

func decodeJSONModel(data []byte) (jsonModel, error) {
	var model jsonModel
	if err := rejectNullInputLimits(data); err != nil {
		return model, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&model); err != nil {
		return model, err
	}
	switch model.Type {
	case "chat", "image", "classifier":
	default:
		return model, fmt.Errorf("model has type %q, expected \"chat\", \"image\", or \"classifier\"", model.Type)
	}
	if model.ID == "" || model.Provider == "" || model.API == "" {
		return model, errors.New("model requires id, provider, and api")
	}
	if err := validateModelInputLimits(model.InputLimits); err != nil {
		return model, err
	}
	return model, nil
}

func validateModelInputLimits(limits *jsonModelInputLimits) error {
	if limits == nil {
		return nil
	}
	if err := validatePositiveLimit("inputLimits.maxRequestBytes", limits.MaxRequestBytes); err != nil {
		return err
	}
	if limits.Images == nil {
		return nil
	}
	if err := validatePositiveLimit("inputLimits.images.maxPerMessage", limits.Images.MaxPerMessage); err != nil {
		return err
	}
	if err := validatePositiveLimit("inputLimits.images.maxPerRequest", limits.Images.MaxPerRequest); err != nil {
		return err
	}
	if limits.Images.Resize == nil {
		return nil
	}
	resize := limits.Images.Resize
	for _, field := range []struct {
		name  string
		value jsonOptionalInt
	}{
		{name: "maxWidth", value: resize.MaxWidth},
		{name: "maxHeight", value: resize.MaxHeight},
		{name: "maxBytes", value: resize.MaxBytes},
		{name: "jpegQuality", value: resize.JPEGQuality},
	} {
		if err := validatePositiveLimit("inputLimits.images.resize."+field.name, field.value); err != nil {
			return err
		}
	}
	if resize.JPEGQuality.Present && resize.JPEGQuality.Value > 100 {
		return fmt.Errorf("inputLimits.images.resize.jpegQuality must be at most 100")
	}
	return nil
}

func validatePositiveLimit(name string, value jsonOptionalInt) error {
	if value.Present && value.Value < 1 {
		return fmt.Errorf("%s must be at least 1", name)
	}
	return nil
}

func rejectNullInputLimits(data []byte) error {
	var model map[string]json.RawMessage
	if err := json.Unmarshal(data, &model); err != nil {
		return err
	}
	limitsData, exists := model["inputLimits"]
	if !exists {
		return nil
	}
	limits, err := decodePresentObject("inputLimits", limitsData)
	if err != nil {
		return err
	}
	if err := rejectNullFields("inputLimits", limits, "maxRequestBytes"); err != nil {
		return err
	}
	imagesData, exists := limits["images"]
	if !exists {
		return nil
	}
	images, err := decodePresentObject("inputLimits.images", imagesData)
	if err != nil {
		return err
	}
	if err := rejectNullFields("inputLimits.images", images, "maxPerMessage", "maxPerRequest"); err != nil {
		return err
	}
	resizeData, exists := images["resize"]
	if !exists {
		return nil
	}
	resize, err := decodePresentObject("inputLimits.images.resize", resizeData)
	if err != nil {
		return err
	}
	return rejectNullFields("inputLimits.images.resize", resize, "maxWidth", "maxHeight", "maxBytes", "jpegQuality")
}

func decodePresentObject(name string, data json.RawMessage) (map[string]json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, fmt.Errorf("%s must be an object", name)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", name, err)
	}
	return value, nil
}

func rejectNullFields(parent string, object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		data, exists := object[field]
		if exists && bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return fmt.Errorf("%s.%s must be an integer", parent, field)
		}
	}
	return nil
}

// normalizeCompat drops an all-empty compat object to nil so a model with no
// compat overrides emits no ModelCompat literal, matching the line parser.
func normalizeCompat(c *ModelCompat) *ModelCompat {
	if c == nil {
		return nil
	}
	if b, err := json.Marshal(c); err != nil || string(b) == "{}" {
		return nil
	}
	return c
}

func emit(path, src string, rows []modelRow) error {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by cmd/gen-models. DO NOT EDIT.\n")
	// Always record source as the basename so re-runs from different
	// working directories produce byte-identical output (required by
	// TestCodegenByteIdentical).
	fmt.Fprintf(&b, "// Source: %s\n", basename(src))
	fmt.Fprintf(&b, "// Models: %d\n\n", len(rows))
	b.WriteString("package ai\n\n")
	b.WriteString("// GeneratedModel is a static catalog entry for one model. Lives in\n")
	b.WriteString("// generated code so the registry init is just a slice copy. Field\n")
	b.WriteString("// shape mirrors upstream pi-ai's `Model<api>` discriminated union\n")
	b.WriteString("// reduced to the fields the pig runtime actually consumes.\n")
	b.WriteString("type GeneratedModel struct {\n")
	b.WriteString("\tID                   string\n")
	b.WriteString("\tProvider             string\n")
	b.WriteString("\tDisplayName          string\n")
	b.WriteString("\tAPI                  API\n")
	b.WriteString("\tBaseURL              string\n")
	b.WriteString("\tHeaders              map[string]string\n")
	b.WriteString("\tCompat               *ModelCompat\n")
	b.WriteString("\tThinkingLevelMap     map[ThinkingLevel]*string\n")
	b.WriteString("\tSamplingParams       map[string]any\n")
	b.WriteString("\tPromptCache          ModelPromptCache\n")
	b.WriteString("\tInputLimits         *ModelInputLimits\n")
	b.WriteString("\tContextWindow        int\n")
	b.WriteString("\tMaxOutputTokens      int\n")
	b.WriteString("\tInputCostPerMTokens  float64\n")
	b.WriteString("\tOutputCostPerMTokens float64\n")
	b.WriteString("\tCacheReadCost        float64\n")
	b.WriteString("\tCacheWriteCost       float64\n")
	b.WriteString("\tTiers                []CostTier\n")
	b.WriteString("\tReasoning            bool\n")
	b.WriteString("\tCapabilities         []string\n")
	b.WriteString("\tEnabled              *bool\n")
	b.WriteString("\tLab                  string\n")
	b.WriteString("\tProviders            []ModelCatalogProvider\n")
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "// GeneratedModels is the static catalog (%d entries).\n", len(rows))
	b.WriteString("var GeneratedModels = []GeneratedModel{\n")
	for _, r := range rows {
		b.WriteString("\t{\n")
		fmt.Fprintf(&b, "\t\tID: %q,\n", r.ID)
		fmt.Fprintf(&b, "\t\tProvider: %q,\n", r.Provider)
		fmt.Fprintf(&b, "\t\tDisplayName: %q,\n", r.Name)
		fmt.Fprintf(&b, "\t\tAPI: %q,\n", r.API)
		fmt.Fprintf(&b, "\t\tBaseURL: %q,\n", r.BaseURL)
		if len(r.Headers) > 0 {
			fmt.Fprintf(&b, "\t\tHeaders: %#v,\n", r.Headers)
		}
		if r.Compat != nil {
			compat, err := json.Marshal(r.Compat)
			if err != nil {
				return fmt.Errorf("marshal compat for %s/%s: %w", r.Provider, r.ID, err)
			}
			fmt.Fprintf(&b, "\t\tCompat: %s,\n", compatLiteral(string(compat)))
		}
		if len(r.ThinkingLevelMap) > 0 {
			fmt.Fprintf(&b, "\t\tThinkingLevelMap: %s,\n", thinkingLevelMapLiteral(r.ThinkingLevelMap))
		}
		if len(r.SamplingParams) > 0 {
			fmt.Fprintf(&b, "\t\tSamplingParams: %#v,\n", r.SamplingParams)
		}
		if len(r.PromptCache) > 0 {
			fmt.Fprintf(&b, "\t\tPromptCache: %s,\n", intMapLiteral(r.PromptCache))
		}
		if r.InputLimits != nil {
			fmt.Fprintf(&b, "\t\tInputLimits: %s,\n", inputLimitsLiteral(r.InputLimits))
		}
		fmt.Fprintf(&b, "\t\tContextWindow: %d,\n", r.ContextWindow)
		fmt.Fprintf(&b, "\t\tMaxOutputTokens: %d,\n", r.MaxTokens)
		fmt.Fprintf(&b, "\t\tInputCostPerMTokens: %s,\n", floatLit(r.InputCost))
		fmt.Fprintf(&b, "\t\tOutputCostPerMTokens: %s,\n", floatLit(r.OutputCost))
		fmt.Fprintf(&b, "\t\tCacheReadCost: %s,\n", floatLit(r.CacheRead))
		fmt.Fprintf(&b, "\t\tCacheWriteCost: %s,\n", floatLit(r.CacheWrite))
		if len(r.Tiers) > 0 {
			fmt.Fprintf(&b, "\t\tTiers: %s,\n", tiersLiteral(r.Tiers))
		}
		fmt.Fprintf(&b, "\t\tReasoning: %t,\n", r.Reasoning)
		if len(r.Inputs) > 0 {
			b.WriteString("\t\tCapabilities: []string{")
			for i, c := range r.Inputs {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%q", c)
			}
			b.WriteString("},\n")
		}
		if r.Enabled != nil {
			fmt.Fprintf(&b, "\t\tEnabled: ptrBool(%t),\n", *r.Enabled)
		}
		if r.Lab != "" {
			fmt.Fprintf(&b, "\t\tLab: %q,\n", r.Lab)
		}
		if len(r.Providers) > 0 {
			fmt.Fprintf(&b, "\t\tProviders: %s,\n", catalogProvidersLiteral(r.Providers))
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}
	return os.WriteFile(path, formatted, 0o644)
}

// unsupportedFields names the model fields a row of its type cannot carry. ImageModel and ClassifierModel hold the base
// model fields plus output (image) or contextWindow (classifier), and chat models carry no output (model-data.ts:170-172);
// a field beyond that would disappear from the Go catalog.
func unsupportedFields(row modelRow) []string {
	var fields []string
	add := func(present bool, name string) {
		if present {
			fields = append(fields, name)
		}
	}
	if row.Type != "image" {
		add(len(row.Output) > 0, "output")
	}
	if row.Type == "chat" {
		return fields
	}
	add(row.Compat != nil, "compat")
	add(len(row.ThinkingLevelMap) > 0, "thinkingLevelMap")
	add(len(row.SamplingParams) > 0, "samplingParams")
	add(len(row.PromptCache) > 0, "promptCache")
	add(row.MaxTokens != 0, "maxTokens")
	add(row.Reasoning, "reasoning")
	add(row.Enabled != nil, "enabled")
	add(row.Lab != "", "lab")
	add(len(row.Providers) > 0, "providers")
	if row.Type == "image" {
		add(row.ContextWindow != 0, "contextWindow")
	}
	return fields
}

func emitImages(path, src string, rows []modelRow) error {
	var b strings.Builder
	writeCatalogHeader(&b, src, len(rows))
	fmt.Fprintf(&b, "// GeneratedImageModels is the static image model catalog (%d entries).\n", len(rows))
	b.WriteString("var GeneratedImageModels = []ImageModel{\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "\t{ID: %q, Name: %q, API: ImageAPI(%q), Provider: %q, BaseURL: %q,", row.ID, row.Name, row.API, row.Provider, row.BaseURL)
		writeBaseModelFields(&b, row)
		fmt.Fprintf(&b, " Output: %s, Cost: %s},\n", stringSliceLiteral(row.Output), modelCostLiteral(row))
	}
	b.WriteString("}\n")
	return writeGoSource(path, b.String())
}

// emitProviders writes the catalog barrel's provider ids in barrel order. Object.keys(MODELS) is getBuiltinProviders
// (providers/all.ts:94-96), and a provider whose shard holds only image or classifier models is still one of its keys.
func emitProviders(path, src string, providers []string) error {
	var b strings.Builder
	b.WriteString("// Code generated by cmd/gen-models. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "// Source: %s\n", basename(src))
	fmt.Fprintf(&b, "// Providers: %d\n\n", len(providers))
	b.WriteString("package ai\n\n")
	b.WriteString("// GeneratedProviders lists the catalog barrel's providers in barrel order (providers/all.ts getBuiltinProviders).\n")
	b.WriteString("var GeneratedProviders = []string{\n")
	for _, provider := range providers {
		fmt.Fprintf(&b, "\t%q,\n", provider)
	}
	b.WriteString("}\n")
	return writeGoSource(path, b.String())
}

func emitClassifiers(path, src string, rows []modelRow) error {
	var b strings.Builder
	writeCatalogHeader(&b, src, len(rows))
	fmt.Fprintf(&b, "// GeneratedClassifierModels is the static classifier model catalog (%d entries).\n", len(rows))
	b.WriteString("var GeneratedClassifierModels = []ClassifierModel{\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "\t{ID: %q, Name: %q, API: ClassifierAPI(%q), Provider: %q, BaseURL: %q,", row.ID, row.Name, row.API, row.Provider, row.BaseURL)
		writeBaseModelFields(&b, row)
		fmt.Fprintf(&b, " Cost: %s, ContextWindow: %d},\n", modelCostLiteral(row), row.ContextWindow)
	}
	b.WriteString("}\n")
	return writeGoSource(path, b.String())
}

// writeCatalogHeader writes the header shared by every generated catalog file. The source is recorded as its basename so
// re-runs from different working directories are byte-identical.
func writeCatalogHeader(b *strings.Builder, src string, models int) {
	b.WriteString("// Code generated by cmd/gen-models. DO NOT EDIT.\n")
	fmt.Fprintf(b, "// Source: %s\n", basename(src))
	fmt.Fprintf(b, "// Models: %d\n\n", models)
	b.WriteString("package ai\n\n")
}

// writeBaseModelFields writes the BaseModel fields image and classifier models share after baseUrl: headers, input, inputLimits.
func writeBaseModelFields(b *strings.Builder, row modelRow) {
	if len(row.Headers) > 0 {
		b.WriteString(" Headers: map[string]string{")
		for i, key := range slices.Sorted(maps.Keys(row.Headers)) {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%q: %q", key, row.Headers[key])
		}
		b.WriteString("},")
	}
	fmt.Fprintf(b, " Input: %s,", stringSliceLiteral(row.Inputs))
	if row.InputLimits != nil {
		fmt.Fprintf(b, " InputLimits: %s,", inputLimitsLiteral(row.InputLimits))
	}
}

func stringSliceLiteral(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

func modelCostLiteral(row modelRow) string {
	literal := fmt.Sprintf("ModelCost{Input: %s, Output: %s, CacheRead: %s, CacheWrite: %s", floatLit(row.InputCost), floatLit(row.OutputCost), floatLit(row.CacheRead), floatLit(row.CacheWrite))
	if len(row.Tiers) > 0 {
		literal += ", Tiers: " + tiersLiteral(row.Tiers)
	}
	return literal + "}"
}

func writeGoSource(path, source string) error {
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}
	return os.WriteFile(path, formatted, 0o644)
}

func floatLit(v float64) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// tiersLiteral emits an []CostTier literal for a model's request-wide pricing
// tiers. Only JSON-backed 0.81+ catalogs carry tiers; empty slices are omitted
// by the caller so pre-tier catalogs stay byte-identical.
func tiersLiteral(tiers []jsonCostTier) string {
	var b strings.Builder
	b.WriteString("[]CostTier{")
	for _, t := range tiers {
		fmt.Fprintf(&b, "{InputTokensAbove: %d, InputCostPer1M: %s, OutputCostPer1M: %s, CacheReadCostPer1M: %s, CacheWriteCostPer1M: %s}, ",
			t.InputTokensAbove, floatLit(t.Input), floatLit(t.Output), floatLit(t.CacheRead), floatLit(t.CacheWrite))
	}
	b.WriteString("}")
	return b.String()
}

func inputLimitsLiteral(limits *jsonModelInputLimits) string {
	if limits == nil {
		return "nil"
	}
	fields := make([]string, 0, 2)
	if limits.MaxRequestBytes.Present {
		fields = append(fields, fmt.Sprintf("MaxRequestBytes: %d", limits.MaxRequestBytes.Value))
	}
	if limits.Images != nil {
		imageFields := make([]string, 0, 3)
		if limits.Images.Resize != nil {
			resize := limits.Images.Resize
			resizeFields := make([]string, 0, 4)
			for _, field := range []struct {
				name  string
				value jsonOptionalInt
			}{
				{name: "MaxWidth", value: resize.MaxWidth},
				{name: "MaxHeight", value: resize.MaxHeight},
				{name: "MaxBytes", value: resize.MaxBytes},
				{name: "JPEGQuality", value: resize.JPEGQuality},
			} {
				if field.value.Present {
					resizeFields = append(resizeFields, fmt.Sprintf("%s: %d", field.name, field.value.Value))
				}
			}
			imageFields = append(imageFields, "Resize: &ModelImageResizeOptions{"+strings.Join(resizeFields, ", ")+"}")
		}
		if limits.Images.MaxPerMessage.Present {
			imageFields = append(imageFields, fmt.Sprintf("MaxPerMessage: %d", limits.Images.MaxPerMessage.Value))
		}
		if limits.Images.MaxPerRequest.Present {
			imageFields = append(imageFields, fmt.Sprintf("MaxPerRequest: %d", limits.Images.MaxPerRequest.Value))
		}
		fields = append(fields, "Images: &ModelImageInputLimits{"+strings.Join(imageFields, ", ")+"}")
	}
	return "&ModelInputLimits{" + strings.Join(fields, ", ") + "}"
}

func intMapLiteral(values map[string]int) string {
	keys := slices.Sorted(maps.Keys(values))
	var b strings.Builder
	b.WriteString("ModelPromptCache{")
	for _, key := range keys {
		fmt.Fprintf(&b, "%q: %d, ", key, values[key])
	}
	b.WriteString("}")
	return b.String()
}

func catalogProvidersLiteral(providers []jsonCatalogProvider) string {
	var b strings.Builder
	b.WriteString("[]ModelCatalogProvider{")
	for _, provider := range providers {
		fmt.Fprintf(&b, "{ID: %q, Name: %q, Credential: %q, Source: %q}, ", provider.ID, provider.Name, provider.Credential, provider.Source)
	}
	b.WriteString("}")
	return b.String()
}

func allowedFallbackModelsLiteral(models []jsonAllowedFallbackModel) string {
	var b strings.Builder
	b.WriteString("[]AnthropicAllowedFallbackModel{")
	for _, model := range models {
		fmt.Fprintf(&b, "{Provider: %q, Model: %q, Cost: ModelCost{Input: %s, Output: %s, CacheRead: %s, CacheWrite: %s, Tiers: %s}}, ", model.Provider, model.Model, floatLit(model.Cost.Input), floatLit(model.Cost.Output), floatLit(model.Cost.CacheRead), floatLit(model.Cost.CacheWrite), tiersLiteral(model.Cost.Tiers))
	}
	b.WriteString("}")
	return b.String()
}

func compatLiteral(jsonText string) string {
	var compat ModelCompat
	if err := json.Unmarshal([]byte(jsonText), &compat); err != nil {
		return "nil"
	}
	var fields []string
	if compat.SupportsDeveloperRole != nil {
		fields = append(fields, fmt.Sprintf("SupportsDeveloperRole:%s", boolPtrLit(*compat.SupportsDeveloperRole)))
	}
	if compat.SupportsReasoningEffort != nil {
		fields = append(fields, fmt.Sprintf("SupportsReasoningEffort:%s", boolPtrLit(*compat.SupportsReasoningEffort)))
	}
	if compat.SupportsStore != nil {
		fields = append(fields, fmt.Sprintf("SupportsStore:%s", boolPtrLit(*compat.SupportsStore)))
	}
	if compat.SupportsUsageInStreaming != nil {
		fields = append(fields, fmt.Sprintf("SupportsUsageInStreaming:%s", boolPtrLit(*compat.SupportsUsageInStreaming)))
	}
	if compat.MaxTokensField != "" {
		fields = append(fields, fmt.Sprintf("MaxTokensField:%q", compat.MaxTokensField))
	}
	if compat.RequiresToolResultName != nil {
		fields = append(fields, fmt.Sprintf("RequiresToolResultName:%s", boolPtrLit(*compat.RequiresToolResultName)))
	}
	if compat.RequiresAssistantAfterToolResult != nil {
		fields = append(fields, fmt.Sprintf("RequiresAssistantAfterToolResult:%s", boolPtrLit(*compat.RequiresAssistantAfterToolResult)))
	}
	if compat.RequiresThinkingAsText != nil {
		fields = append(fields, fmt.Sprintf("RequiresThinkingAsText:%s", boolPtrLit(*compat.RequiresThinkingAsText)))
	}
	if compat.RequiresReasoningContentOnAssistantMessages != nil {
		fields = append(fields, fmt.Sprintf("RequiresReasoningContentOnAssistantMessages:%s", boolPtrLit(*compat.RequiresReasoningContentOnAssistantMessages)))
	}
	if compat.ThinkingFormat != "" {
		fields = append(fields, fmt.Sprintf("ThinkingFormat:%q", compat.ThinkingFormat))
	}
	if compat.CacheControlFormat != "" {
		fields = append(fields, fmt.Sprintf("CacheControlFormat:%q", compat.CacheControlFormat))
	}
	if compat.SendSessionAffinityHeaders != nil {
		fields = append(fields, fmt.Sprintf("SendSessionAffinityHeaders:%s", boolPtrLit(*compat.SendSessionAffinityHeaders)))
	}
	if compat.SupportsStrictMode != nil {
		fields = append(fields, fmt.Sprintf("SupportsStrictMode:%s", boolPtrLit(*compat.SupportsStrictMode)))
	}
	if compat.SupportsStrictTools != nil {
		fields = append(fields, fmt.Sprintf("SupportsStrictTools:%s", boolPtrLit(*compat.SupportsStrictTools)))
	}
	if compat.SupportsOpenAIGrammarTools != nil {
		fields = append(fields, fmt.Sprintf("SupportsOpenAIGrammarTools:%s", boolPtrLit(*compat.SupportsOpenAIGrammarTools)))
	}
	if compat.SupportsExplicitPromptCacheMode != nil {
		fields = append(fields, fmt.Sprintf("SupportsExplicitPromptCacheMode:%s", boolPtrLit(*compat.SupportsExplicitPromptCacheMode)))
	}
	if len(compat.ReasoningEffortMap) > 0 {
		fields = append(fields, fmt.Sprintf("ReasoningEffortMap:%#v", compat.ReasoningEffortMap))
	}
	if len(compat.OpenRouterRouting) > 0 {
		fields = append(fields, fmt.Sprintf("OpenRouterRouting:%#v", compat.OpenRouterRouting))
	}
	if len(compat.VercelGatewayRouting) > 0 {
		fields = append(fields, fmt.Sprintf("VercelGatewayRouting:%#v", compat.VercelGatewayRouting))
	}
	if compat.ZaiToolStream != nil {
		fields = append(fields, fmt.Sprintf("ZaiToolStream:%s", boolPtrLit(*compat.ZaiToolStream)))
	}
	if compat.SupportsLongCacheRetention != nil {
		fields = append(fields, fmt.Sprintf("SupportsLongCacheRetention:%s", boolPtrLit(*compat.SupportsLongCacheRetention)))
	}
	if compat.SendSessionIdHeader != nil {
		fields = append(fields, fmt.Sprintf("SendSessionIdHeader:%s", boolPtrLit(*compat.SendSessionIdHeader)))
	}
	if compat.SupportsEagerToolInputStreaming != nil {
		fields = append(fields, fmt.Sprintf("SupportsEagerToolInputStreaming:%s", boolPtrLit(*compat.SupportsEagerToolInputStreaming)))
	}
	if compat.ForceAdaptiveThinking != nil {
		fields = append(fields, fmt.Sprintf("ForceAdaptiveThinking:%s", boolPtrLit(*compat.ForceAdaptiveThinking)))
	}
	if compat.SupportsTemperature != nil {
		fields = append(fields, fmt.Sprintf("SupportsTemperature:%s", boolPtrLit(*compat.SupportsTemperature)))
	}
	if compat.AllowEmptySignature != nil {
		fields = append(fields, fmt.Sprintf("AllowEmptySignature:%s", boolPtrLit(*compat.AllowEmptySignature)))
	}
	if compat.SupportsCacheControlOnTools != nil {
		fields = append(fields, fmt.Sprintf("SupportsCacheControlOnTools:%s", boolPtrLit(*compat.SupportsCacheControlOnTools)))
	}
	if compat.DeferredToolsMode != "" {
		fields = append(fields, fmt.Sprintf("DeferredToolsMode:%q", compat.DeferredToolsMode))
	}
	if compat.SessionAffinityFormat != "" {
		fields = append(fields, fmt.Sprintf("SessionAffinityFormat:%q", compat.SessionAffinityFormat))
	}
	if compat.SupportsToolSearch != nil {
		fields = append(fields, fmt.Sprintf("SupportsToolSearch:%s", boolPtrLit(*compat.SupportsToolSearch)))
	}
	if compat.SupportsToolReferences != nil {
		fields = append(fields, fmt.Sprintf("SupportsToolReferences:%s", boolPtrLit(*compat.SupportsToolReferences)))
	}
	if len(compat.ChatTemplateArgs) > 0 {
		fields = append(fields, fmt.Sprintf("ChatTemplateArgs:%#v", compat.ChatTemplateArgs))
	}
	if compat.SupportsThinkingTokenBudget != nil {
		fields = append(fields, fmt.Sprintf("SupportsThinkingTokenBudget:%s", boolPtrLit(*compat.SupportsThinkingTokenBudget)))
	}
	if compat.ThinkingTokenBudgetField != "" {
		fields = append(fields, fmt.Sprintf("ThinkingTokenBudgetField:%q", compat.ThinkingTokenBudgetField))
	}
	if compat.SupportsAdditionalTools != nil {
		fields = append(fields, fmt.Sprintf("SupportsAdditionalTools:%s", boolPtrLit(*compat.SupportsAdditionalTools)))
	}
	if compat.SupportsMidConvoEffort != nil {
		fields = append(fields, fmt.Sprintf("SupportsMidConvoEffort:%s", boolPtrLit(*compat.SupportsMidConvoEffort)))
	}
	if compat.SupportsMidConvoSystemMessages != nil {
		fields = append(fields, fmt.Sprintf("SupportsMidConvoSystemMessages:%s", boolPtrLit(*compat.SupportsMidConvoSystemMessages)))
	}
	if compat.SupportsMidConvoToolAdditions != nil {
		fields = append(fields, fmt.Sprintf("SupportsMidConvoToolAdditions:%s", boolPtrLit(*compat.SupportsMidConvoToolAdditions)))
	}
	if compat.SupportsMidConvoToolChanges != nil {
		fields = append(fields, fmt.Sprintf("SupportsMidConvoToolChanges:%s", boolPtrLit(*compat.SupportsMidConvoToolChanges)))
	}
	if len(compat.AllowedFallbackModels) > 0 {
		fields = append(fields, fmt.Sprintf("AllowedFallbackModels:%s", allowedFallbackModelsLiteral(compat.AllowedFallbackModels)))
	}
	if len(fields) == 0 {
		return "nil"
	}
	return "&ModelCompat{" + strings.Join(fields, ", ") + "}"
}

func boolPtrLit(v bool) string {
	if v {
		return "ptrBool(true)"
	}
	return "ptrBool(false)"
}

func thinkingLevelMapLiteral(m map[string]*string) string {
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := m[key]
		if value == nil {
			parts = append(parts, fmt.Sprintf("ThinkingLevel(%q): nil", key))
			continue
		}
		parts = append(parts, fmt.Sprintf("ThinkingLevel(%q): ptrString(%q)", key, *value))
	}
	return "map[ThinkingLevel]*string{" + strings.Join(parts, ", ") + "}"
}

func basename(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}
