package llama

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Ports packages/coding-agent/src/extensions/llama/provider.ts.

// LlamaProviderID mirrors LLAMA_PROVIDER_ID.
const LlamaProviderID = "llama.cpp"

// DefaultLlamaServerURL mirrors DEFAULT_LLAMA_SERVER_URL.
const DefaultLlamaServerURL = "http://127.0.0.1:8080"

const openAICompletionsAPI = "openai-completions"

// llamaCppClassifyAPI is the classifier API the provider serves (pi-ai api/llama-cpp-classify).
const llamaCppClassifyAPI = "llama-cpp-classify"

// ThinkingLevelMap mirrors the thinkingLevelMap object toPiModel builds, in
// upstream key order; nil entries serialize as null.
type ThinkingLevelMap struct {
	Off     *string `json:"off"`
	Minimal *string `json:"minimal"`
	Low     *string `json:"low"`
	Medium  *string `json:"medium"`
	High    *string `json:"high"`
	XHigh   *string `json:"xhigh"`
}

// ModelCost mirrors Model.cost.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ModelCompat mirrors the openai-completions compat object toPiModel builds.
type ModelCompat struct {
	SupportsStore            bool   `json:"supportsStore"`
	SupportsDeveloperRole    bool   `json:"supportsDeveloperRole"`
	SupportsReasoningEffort  bool   `json:"supportsReasoningEffort"`
	SupportsUsageInStreaming bool   `json:"supportsUsageInStreaming"`
	SupportsStrictMode       bool   `json:"supportsStrictMode"`
	MaxTokensField           string `json:"maxTokensField"`
	ThinkingFormat           string `json:"thinkingFormat,omitempty"`
	ThinkingTokenBudgetField string `json:"thinkingTokenBudgetField,omitempty"`
}

// Model mirrors the pi-ai Model<"openai-completions"> the provider publishes
// and persists, in upstream field order.
type Model struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	API              string            `json:"api"`
	Provider         string            `json:"provider"`
	BaseURL          string            `json:"baseUrl"`
	Reasoning        bool              `json:"reasoning"`
	ThinkingLevelMap *ThinkingLevelMap `json:"thinkingLevelMap,omitempty"`
	Input            []string          `json:"input"`
	Cost             ModelCost         `json:"cost"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	Compat           ModelCompat       `json:"compat"`
}

// AnyModel is a model the provider lists: Model (chat) or ClassifierModel.
type AnyModel interface{ anyModel() }

func (Model) anyModel()           {}
func (ClassifierModel) anyModel() {}

// ClassifierModel mirrors the pi-ai ClassifierModel<"llama-cpp-classify"> the provider publishes and persists.
type ClassifierModel struct {
	Type          string    `json:"type"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	API           string    `json:"api"`
	Provider      string    `json:"provider"`
	BaseURL       string    `json:"baseUrl"`
	Input         []string  `json:"input"`
	Cost          ModelCost `json:"cost"`
	ContextWindow int       `json:"contextWindow"`
}

// AuthContext mirrors pi-ai AuthContext. Env reports ok=false for an unset or
// blank variable, like defaultProviderAuthContext.
type AuthContext struct {
	Env func(name string) (string, bool)
}

// DefaultAuthContext mirrors defaultProviderAuthContext over the process
// environment.
func DefaultAuthContext() AuthContext {
	return AuthContext{Env: func(name string) (string, bool) {
		value, ok := os.LookupEnv(name)
		if !ok || strings.TrimFunc(value, isJSWhitespace) == "" {
			return "", false
		}
		return value, true
	}}
}

// AuthPrompt mirrors the text and secret members of pi-ai AuthPrompt, the
// only kinds this provider asks for.
type AuthPrompt struct {
	Type        string
	Message     string
	Placeholder string
}

// AuthInteraction mirrors ProviderAuthInteraction; Ctx carries its signal.
type AuthInteraction struct {
	Ctx    context.Context
	Prompt func(AuthPrompt) (string, error)
}

// ModelAuth mirrors pi-ai ModelAuth.
type ModelAuth struct {
	APIKey  string
	BaseURL string
}

// AuthResult mirrors pi-ai AuthResult.
type AuthResult struct {
	Auth   ModelAuth
	Env    map[string]string
	Source string
}

// AuthCheck mirrors pi-ai AuthCheck.
type AuthCheck struct {
	Source string
	Type   string
}

// APIKeyAuth mirrors pi-ai ApiKeyAuth.
type APIKeyAuth struct {
	Name    string
	Login   func(interaction AuthInteraction) (ai.Credential, error)
	Check   func(ctx context.Context, auth AuthContext, credential *ai.Credential) (*AuthCheck, error)
	Resolve func(ctx context.Context, auth AuthContext, credential *ai.Credential) (*AuthResult, error)
}

// ModelsPublication mirrors pi-ai ModelsPublication; a nil Persist leaves
// storage unchanged.
type ModelsPublication struct {
	Persist *ai.ModelsStoreEntry
	Update  func()
}

// RefreshModelsContext mirrors pi-ai RefreshModelsContext.
type RefreshModelsContext struct {
	Ctx          context.Context
	Credential   *ai.Credential
	Stored       *ai.ModelsStoreEntry
	Publish      func(ModelsPublication) (bool, error)
	AllowNetwork bool
}

// Provider mirrors the pi-ai Provider object createLlamaProvider returns.
// Streaming uses the host's openai-completions implementation for API.
type Provider struct {
	ID           string
	Name         string
	BaseURL      string
	API          string
	APIKey       APIKeyAuth
	GetModels    func() []Model
	GetAllModels func() []AnyModel
	// Classify is the provider object's classifier operation. upstream: provider.ts:262 (classify)
	Classify      func(ctx context.Context, model ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) ai.ClassifierResult
	RefreshModels func(refresh RefreshModelsContext) error
}

// LlamaProviderController mirrors LlamaProviderController.
type LlamaProviderController struct {
	Provider *Provider

	mu          sync.Mutex
	models      []Model
	classifiers []ClassifierModel
}

func credentialServerURL(credential *ai.Credential) (string, error) {
	if credential == nil {
		return "", nil
	}
	value := credential.Env["LLAMA_BASE_URL"]
	if strings.TrimFunc(value, isJSWhitespace) == "" {
		return "", nil
	}
	return NormalizeLlamaServerURL(value)
}

func resolveServerURL(auth AuthContext, credential *ai.Credential) (string, error) {
	configured, err := credentialServerURL(credential)
	if err != nil {
		return "", err
	}
	if configured == "" {
		value, _ := auth.Env("LLAMA_BASE_URL")
		configured = strings.TrimFunc(value, isJSWhitespace)
	}
	if configured == "" {
		return "", nil
	}
	return NormalizeLlamaServerURL(configured)
}

func modelIsSelectable(model LlamaModelInfo, routerAutoload bool) bool {
	switch model.Status.Value {
	case LlamaModelStatusLoaded:
		return true
	case LlamaModelStatusSleeping:
		// llama.cpp reports idle-slept models as "sleeping"; requests wake them automatically.
		return true
	}
	// Unloaded presets are routable only when llama.cpp router autoload can load them on first use.
	return routerAutoload && model.Status.Value == LlamaModelStatusUnloaded && !model.Status.Failed && model.Source == "preset"
}

func routerAutoloadEnabled(ctx context.Context, client *LlamaClient, catalog []LlamaModelInfo) bool {
	if !slices.ContainsFunc(catalog, func(model LlamaModelInfo) bool {
		return model.Status.Value == LlamaModelStatusUnloaded && model.Source == "preset"
	}) {
		return false
	}
	props, err := client.Props(ctx, "")
	return err == nil && props.ModelsAutoload
}

var decimalNumber = lazyregexp.New(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

// serverArgNumber mirrors Number(text) for the strings a llama-server argument list holds. A base-prefixed literal above Number.MAX_SAFE_INTEGER or the platform's int range reports no value: its only caller keeps positive safe integers (provider.ts configuredContextWindow, Number.isSafeInteger), and bounding the uint64 before conversion keeps the later int conversion in range.
func serverArgNumber(text string) (float64, bool) {
	text = strings.TrimFunc(text, isJSWhitespace)
	if text == "" {
		return 0, true
	}
	if len(text) > 2 && text[0] == '0' {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[text[1]]
		if base != 0 {
			value, err := strconv.ParseUint(text[2:], base, 64)
			if err != nil || value > 1<<53-1 || value > math.MaxInt {
				return 0, false
			}
			return float64(value), true
		}
	}
	if !decimalNumber.MatchString(text) {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil
}

// configuredContextWindow reads --ctx-size, -c or -ctx from the server arguments of a model llama.cpp has not loaded (provider.ts configuredContextWindow).
func configuredContextWindow(model LlamaModelInfo) int {
	args := model.Status.Args
	for index := 0; index < len(args)-1; index++ {
		if flag := args[index]; flag != "--ctx-size" && flag != "-c" && flag != "-ctx" {
			continue
		}
		if value, ok := serverArgNumber(args[index+1]); ok {
			if window, ok := positiveSafeInt(value, math.MaxInt); ok {
				return window
			}
		}
	}
	return 0
}

// positiveSafeInt is value as an int when it is a positive integer no larger than Number.MAX_SAFE_INTEGER (provider.ts Number.isSafeInteger) and no larger than the platform's int range (math.MaxInt, which static analysis reads as the bound) and than limit, so the conversion cannot wrap.
func positiveSafeInt(value float64, limit int) (int, bool) {
	if value != math.Trunc(value) || value <= 0 || value > 1<<53-1 || value > math.MaxInt || value > float64(limit) {
		return 0, false
	}
	return int(value), true
}

// contextWindowOf is the context window of a model: the loaded size, the configured size, the cached size, the training size, then 128000 (provider.ts contextWindowOf).
func contextWindowOf(model LlamaModelInfo, cachedContextWindow int) int {
	if model.Meta != nil && model.Meta.NCtx != nil && *model.Meta.NCtx > 0 {
		return int(*model.Meta.NCtx)
	}
	if configured := configuredContextWindow(model); configured > 0 {
		return configured
	}
	if cachedContextWindow > 0 {
		return cachedContextWindow
	}
	if model.Meta != nil && model.Meta.NCtxTrain != nil && *model.Meta.NCtxTrain > 0 {
		return int(*model.Meta.NCtxTrain)
	}
	return 128000
}

// toPiClassifierModel is the same llama.cpp model used as a classifier: answers are read from next-token label probabilities (provider.ts toPiClassifierModel).
func toPiClassifierModel(model LlamaModelInfo, serverURL string, cachedContextWindow int) ClassifierModel {
	return ClassifierModel{
		Type:          "classifier",
		ID:            model.ID,
		Name:          model.ID,
		API:           llamaCppClassifyAPI,
		Provider:      LlamaProviderID,
		BaseURL:       serverURL,
		Input:         []string{"text"},
		ContextWindow: contextWindowOf(model, cachedContextWindow),
	}
}

func toPiModel(model LlamaModelInfo, serverURL string, props *LlamaServerProps, cachedContextWindow int) Model {
	contextWindow := contextWindowOf(model, cachedContextWindow)
	reasoning := props != nil && strings.Contains(props.ChatTemplate, "enable_thinking")
	inferenceURL, _ := LlamaInferenceURL(serverURL)
	input := []string{"text"}
	if model.Architecture != nil && slices.Contains(model.Architecture.InputModalities, "image") {
		input = []string{"text", "image"}
	}
	result := Model{
		ID:            model.ID,
		Name:          model.ID,
		API:           openAICompletionsAPI,
		Provider:      LlamaProviderID,
		BaseURL:       inferenceURL,
		Reasoning:     reasoning,
		Input:         input,
		ContextWindow: contextWindow,
		MaxTokens:     contextWindow,
		Compat:        ModelCompat{SupportsUsageInStreaming: true, MaxTokensField: "max_tokens"},
	}
	if reasoning {
		// pig divergence (D90): Pi maps only off and medium and sends no budget. Every budgeted level is offered and sends its thinking_budget_tokens; xhigh would send high's budget, so it stays unsupported.
		off, minimal, low, medium, high := "off", "minimal", "low", "medium", "high"
		result.ThinkingLevelMap = &ThinkingLevelMap{Off: &off, Minimal: &minimal, Low: &low, Medium: &medium, High: &high}
		result.Compat.ThinkingFormat = "qwen-chat-template"
		result.Compat.ThinkingTokenBudgetField = "thinking_budget_tokens"
	}
	return result
}

// SetCatalog mirrors LlamaProviderController.setCatalog.
func (c *LlamaProviderController) SetCatalog(catalog []LlamaModelInfo, serverURL string, routerAutoload bool) {
	var models []Model
	var classifiers []ClassifierModel
	for _, model := range catalog {
		if modelIsSelectable(model, routerAutoload) {
			models = append(models, toPiModel(model, serverURL, nil, 0))
			classifiers = append(classifiers, toPiClassifierModel(model, serverURL, 0))
		}
	}
	c.setModels(models, classifiers)
}

func (c *LlamaProviderController) setModels(models []Model, classifiers []ClassifierModel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = models
	c.classifiers = classifiers
}

func (c *LlamaProviderController) getModels() []Model {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.models)
}

// getAllModels lists the chat models, then the classifier models (provider.ts getAllModels).
func (c *LlamaProviderController) getAllModels() []AnyModel {
	c.mu.Lock()
	defer c.mu.Unlock()
	all := make([]AnyModel, 0, len(c.models)+len(c.classifiers))
	for _, model := range c.models {
		all = append(all, model)
	}
	for _, model := range c.classifiers {
		all = append(all, model)
	}
	return all
}

// CreateLlamaProvider mirrors createLlamaProvider.
func CreateLlamaProvider() *LlamaProviderController {
	controller := &LlamaProviderController{}
	defaultBaseURL, _ := LlamaInferenceURL(DefaultLlamaServerURL)
	controller.Provider = &Provider{
		ID:      LlamaProviderID,
		Name:    "llama.cpp",
		BaseURL: defaultBaseURL,
		API:     openAICompletionsAPI,
		APIKey: APIKeyAuth{
			Name:    "llama.cpp server",
			Login:   login,
			Check:   check,
			Resolve: resolve,
		},
		GetModels:     controller.getModels,
		GetAllModels:  controller.getAllModels,
		RefreshModels: controller.refreshModels,
		Classify:      classify,
	}
	return controller
}

// classify reads next-token label probabilities from llama-server through the shared llama-cpp-classify implementation
// (provider.ts:140 llamaCppClassifyApi, :262 classify).
func classify(ctx context.Context, model ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) ai.ClassifierResult {
	return ai.ClassifyLlamaCpp(ctx, model.aiModel(), request, options)
}

func (m ClassifierModel) aiModel() ai.ClassifierModel {
	return ai.ClassifierModel{
		ID:            m.ID,
		Name:          m.Name,
		API:           ai.ClassifierAPI(m.API),
		Provider:      m.Provider,
		BaseURL:       m.BaseURL,
		Input:         m.Input,
		Cost:          ai.ModelCost{Input: m.Cost.Input, Output: m.Cost.Output, CacheRead: m.Cost.CacheRead, CacheWrite: m.Cost.CacheWrite},
		ContextWindow: m.ContextWindow,
	}
}

func login(interaction AuthInteraction) (ai.Credential, error) {
	envURL, envSet := os.LookupEnv("LLAMA_BASE_URL")
	placeholder := DefaultLlamaServerURL
	if envSet {
		placeholder = envURL
	}
	enteredURL, err := interaction.Prompt(AuthPrompt{Type: "text", Message: "llama.cpp server URL", Placeholder: placeholder})
	if err != nil {
		return ai.Credential{}, err
	}
	candidate := strings.TrimFunc(enteredURL, isJSWhitespace)
	if candidate == "" {
		candidate = envURL
	}
	if candidate == "" {
		candidate = DefaultLlamaServerURL
	}
	serverURL, err := NormalizeLlamaServerURL(candidate)
	if err != nil {
		return ai.Credential{}, err
	}
	enteredKey, err := interaction.Prompt(AuthPrompt{Type: "secret", Message: "API key (optional)"})
	if err != nil {
		return ai.Credential{}, err
	}
	apiKey := strings.TrimFunc(enteredKey, isJSWhitespace)
	client, err := NewLlamaClient(serverURL, apiKey)
	if err != nil {
		return ai.Credential{}, err
	}
	if _, err := client.List(interaction.Ctx, false); err != nil {
		return ai.Credential{}, err
	}
	return ai.Credential{Type: ai.CredentialAPIKey, Key: apiKey, Env: map[string]string{"LLAMA_BASE_URL": serverURL}}, nil
}

func authSource(credential *ai.Credential) string {
	if credential != nil {
		return "stored credential"
	}
	return "LLAMA_BASE_URL"
}

func check(_ context.Context, auth AuthContext, credential *ai.Credential) (*AuthCheck, error) {
	serverURL, err := resolveServerURL(auth, credential)
	if err != nil || serverURL == "" {
		return nil, err
	}
	return &AuthCheck{Type: "api_key", Source: authSource(credential)}, nil
}

func resolve(_ context.Context, auth AuthContext, credential *ai.Credential) (*AuthResult, error) {
	serverURL, err := resolveServerURL(auth, credential)
	if err != nil || serverURL == "" {
		return nil, err
	}
	apiKey := "local"
	if credential != nil && credential.Key != "" {
		apiKey = credential.Key
	} else if value, ok := auth.Env("LLAMA_API_KEY"); ok {
		apiKey = value
	}
	env := map[string]string{}
	if credential != nil {
		maps.Copy(env, credential.Env)
	}
	env["LLAMA_BASE_URL"] = serverURL
	inferenceURL, _ := LlamaInferenceURL(serverURL)
	return &AuthResult{Auth: ModelAuth{APIKey: apiKey, BaseURL: inferenceURL}, Env: env, Source: authSource(credential)}, nil
}

// restoredModels mirrors the stored-model filters: chat models with the openai-completions API and classifier models with the llama-cpp-classify API. Entries that do not decode as a model are skipped.
func restoredModels(stored *ai.ModelsStoreEntry) ([]Model, []ClassifierModel) {
	var models []Model
	var classifiers []ClassifierModel
	for _, raw := range stored.Models {
		var kind struct{ Type, Provider string }
		if json.Unmarshal(raw, &kind) != nil || kind.Provider != LlamaProviderID {
			continue
		}
		switch kind.Type {
		case "", "chat":
			var model Model
			if json.Unmarshal(raw, &model) == nil && model.API == openAICompletionsAPI {
				models = append(models, model)
			}
		case "classifier":
			var model ClassifierModel
			if json.Unmarshal(raw, &model) == nil && model.API == llamaCppClassifyAPI {
				classifiers = append(classifiers, model)
			}
		}
	}
	return models, classifiers
}

func (c *LlamaProviderController) refreshModels(refresh RefreshModelsContext) error {
	cachedContextWindows := map[string]int{}
	if refresh.Stored != nil {
		restored, restoredClassifiers := restoredModels(refresh.Stored)
		for _, model := range restored {
			cachedContextWindows[model.ID] = model.ContextWindow
		}
		for _, model := range restoredClassifiers {
			cachedContextWindows[model.ID] = model.ContextWindow
		}
		published, err := refresh.Publish(ModelsPublication{Update: func() { c.setModels(restored, restoredClassifiers) }})
		if err != nil || !published {
			return err
		}
	}
	ctx := refresh.Ctx
	if !refresh.AllowNetwork || ctx.Err() != nil || refresh.Credential == nil || refresh.Credential.Type != ai.CredentialAPIKey {
		return nil
	}
	serverURL, err := credentialServerURL(refresh.Credential)
	if err != nil || serverURL == "" {
		return err
	}
	client, err := NewLlamaClient(serverURL, refresh.Credential.Key)
	if err != nil {
		return err
	}
	catalog, err := client.List(ctx, false)
	if err != nil || ctx.Err() != nil {
		return err
	}
	routerAutoload := routerAutoloadEnabled(ctx, client, catalog)
	if ctx.Err() != nil {
		return nil
	}
	selectable := selectableModels(catalog, routerAutoload)
	refreshed, err := classifyModels(ctx, client, selectable, serverURL, cachedContextWindows)
	if err != nil || ctx.Err() != nil {
		return err
	}
	refreshedClassifiers := make([]ClassifierModel, len(selectable))
	for index, model := range selectable {
		refreshedClassifiers[index] = toPiClassifierModel(model, serverURL, cachedContextWindows[model.ID])
	}
	encoded := make([]json.RawMessage, 0, len(refreshed)+len(refreshedClassifiers))
	for _, model := range refreshed {
		raw, err := json.Marshal(model)
		if err != nil {
			return err
		}
		encoded = append(encoded, raw)
	}
	for _, model := range refreshedClassifiers {
		raw, err := json.Marshal(model)
		if err != nil {
			return err
		}
		encoded = append(encoded, raw)
	}
	checkedAt := float64(time.Now().UnixMilli())
	_, err = refresh.Publish(ModelsPublication{
		Persist: &ai.ModelsStoreEntry{Models: encoded, CheckedAt: &checkedAt},
		Update:  func() { c.setModels(refreshed, refreshedClassifiers) },
	})
	return err
}

func selectableModels(catalog []LlamaModelInfo, routerAutoload bool) []LlamaModelInfo {
	var selectable []LlamaModelInfo
	for _, model := range catalog {
		if modelIsSelectable(model, routerAutoload) {
			selectable = append(selectable, model)
		}
	}
	return selectable
}

// classifyModels mirrors the Promise.all over selectable models: one joined
// request per loaded model, results in catalog order, first rejection wins.
func classifyModels(ctx context.Context, client *LlamaClient, selectable []LlamaModelInfo, serverURL string, cachedContextWindows map[string]int) ([]Model, error) {
	results := make([]Model, len(selectable))
	groupCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var group sync.WaitGroup
	var once sync.Once
	var firstErr error
	for index, model := range selectable {
		// Only loaded models expose their template without side effects. Unloaded autoload presets
		// would need to be loaded, while querying sleeping models may wake them. Those models remain
		// unclassified until they are loaded or woken and a later catalog refresh discovers them.
		if model.Status.Value != LlamaModelStatusLoaded {
			results[index] = toPiModel(model, serverURL, nil, cachedContextWindows[model.ID])
			continue
		}
		group.Go(func() {
			props, err := client.Props(groupCtx, model.ID)
			if err != nil {
				once.Do(func() { firstErr = err; cancel(err) })
				return
			}
			results[index] = toPiModel(model, serverURL, &props, cachedContextWindows[model.ID])
		})
	}
	group.Wait()
	return results, firstErr
}
