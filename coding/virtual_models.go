package coding

// Ports packages/coding-agent/src/core/virtual-models.ts and the virtual-model members of core/model-runtime.ts (registerVirtualModel, unregisterVirtualModel, resolveModel, getPhysicalModel, streamSimple routing).
//
// Virtual models are catalog entries that route each request to a physical model. The selection (a `model_change` entry, the agent's model, `ctx.model`) may name a virtual model. Everything below the routing step sees only physical models: providers stream them and assistant messages record them. A virtual model never reaches a provider.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// The routing types and constants live in package extension, which the extension API needs and which coding imports.
type (
	// VirtualModelDefinition is a selectable catalog entry that routes each request to a physical model. upstream: virtual-models.ts:87-101
	VirtualModelDefinition = extension.VirtualModelDefinition
	// ModelRoute is the physical model and thinking level for one request. upstream: virtual-models.ts:75-85
	ModelRoute = extension.ModelRoute
	// ModelRouteRequest is what a router sees for one request. upstream: virtual-models.ts:55-72
	ModelRouteRequest = extension.ModelRouteRequest
	// ModelRouteReason says why a request is being routed. upstream: virtual-models.ts:53
	ModelRouteReason = extension.ModelRouteReason
	// VirtualModelStateData is the data of a `pi.virtual-model-state` custom entry. upstream: virtual-models.ts:35-39
	VirtualModelStateData = extension.VirtualModelStateData
)

// The reasons of upstream's ModelRouteReason union.
const (
	ModelRouteReasonUser         = extension.ModelRouteReasonUser
	ModelRouteReasonContinuation = extension.ModelRouteReasonContinuation
	ModelRouteReasonRetry        = extension.ModelRouteReasonRetry
	ModelRouteReasonDirect       = extension.ModelRouteReasonDirect
)

// VirtualModelAPI is the API id of virtual catalog entries. upstream: virtual-models.ts:29 (VIRTUAL_MODEL_API)
const VirtualModelAPI = extension.VirtualModelAPI

// VirtualModelStateEntry is the custom entry type that stores router state on the session branch. upstream: virtual-models.ts:32 (VIRTUAL_MODEL_STATE_ENTRY)
const VirtualModelStateEntry = extension.VirtualModelStateEntry

// IsVirtualModel reports whether a model names a virtual model. Failed routing leaves the virtual model on its message, so a message's API is checked the same way with [IsVirtualAPI].
//
// upstream: virtual-models.ts:105-107 (isVirtualModel)
func IsVirtualModel(model *ai.Model) bool {
	return model != nil && model.ProviderMeta.API == VirtualModelAPI
}

// IsVirtualAPI reports whether an API id is [VirtualModelAPI].
//
// upstream: virtual-models.ts:105-107 (isVirtualModel, over a message)
func IsVirtualAPI(api ai.API) bool {
	return api == VirtualModelAPI
}

// FindLatestResponse returns the latest successful response. Its model is physical: failed or aborted requests, including failed routing, are skipped.
//
// upstream: virtual-models.ts:110-118 (findLatestResponse)
func FindLatestResponse(messages []agent.AgentMessage) *agent.AssistantMessage {
	for _, message := range slices.Backward(messages) {
		if message := message.Assistant; message != nil && message.StopReason != ai.StopReasonError && message.StopReason != ai.StopReasonAborted {
			return message
		}
	}
	return nil
}

// ModelSelection names a model by provider and id. Provider and ModelID hold what JavaScript's String() gives the entry's members, as a template literal prints them. Unresolvable is set when either member is not a string: the selection names no catalog model and a lookup finds nothing.
type ModelSelection struct {
	Provider     string
	ModelID      string
	Unresolvable bool
}

// GetBranchSelection returns the model selection a session branch records, or nil. A virtual `model_change` holds until the next `model_change`, because responses name the physical models it routed to. Otherwise the latest physical response wins, as in sessions without virtual models. A virtual model that is no longer registered does not hold, so the selection falls back to the physical model that answered last.
//
// Only the last `model_change` can hold, so this looks up at most one model in the catalog.
//
// upstream: virtual-models.ts:126-157 (getBranchSelection, findLastModelChange)
func GetBranchSelection(branch []icodingagent.SessionEntry, getModel func(provider, modelID string) *ai.Model) *ModelSelection {
	for i, entry := range slices.Backward(branch) {
		switch entry.Base().Type {
		case "model_change":
			if change := modelChangeSelection(entry); change != nil {
				return change
			}
		case "message":
			message, ok := entry.(icodingagent.MessageEntry)
			if !ok || message.Message.Assistant == nil || IsVirtualAPI(message.Message.Assistant.API) {
				continue
			}
			response := &ModelSelection{Provider: message.Message.Assistant.Provider, ModelID: message.Message.Assistant.ModelID}
			if change := findLastModelChange(branch, i); change != nil {
				if !change.Unresolvable {
					if model := getModel(change.Provider, change.ModelID); model != nil && IsVirtualModel(model) {
						return change
					}
				}
			}
			return response
		}
	}
	return nil
}

func modelChangeSelection(entry icodingagent.SessionEntry) *ModelSelection {
	var change struct {
		Provider json.RawMessage `json:"provider"`
		ModelID  json.RawMessage `json:"modelId"`
	}
	if err := json.Unmarshal(entry.Raw(), &change); err != nil {
		return nil
	}
	provider, providerOK := jsMemberString(change.Provider)
	modelID, modelIDOK := jsMemberString(change.ModelID)
	return &ModelSelection{Provider: provider, ModelID: modelID, Unresolvable: !providerOK || !modelIDOK}
}

// jsMemberString is String(value) of a decoded entry member and whether it is a string: an absent member is undefined.
func jsMemberString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "undefined", false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	text, isString := value.(string)
	if isString {
		return text, true
	}
	return jsValueString(value), false
}

func jsValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return jsnumber.String(v)
	case string:
		return v
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = jsValueString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func findLastModelChange(branch []icodingagent.SessionEntry, before int) *ModelSelection {
	for i := before - 1; i >= 0; i-- {
		if branch[i].Base().Type != "model_change" {
			continue
		}
		if change := modelChangeSelection(branch[i]); change != nil {
			return change
		}
	}
	return nil
}

// GetVirtualModelState returns the latest router state a session branch stores for a virtual model, or nil.
//
// upstream: virtual-models.ts:148-156 (getVirtualModelState)
func GetVirtualModelState(branch []icodingagent.SessionEntry, provider, modelID string) json.RawMessage {
	for _, b := range slices.Backward(branch) {
		if b.Base().Type != "custom" {
			continue
		}
		var custom struct {
			CustomType string                 `json:"customType"`
			Data       *VirtualModelStateData `json:"data"`
		}
		if err := json.Unmarshal(b.Raw(), &custom); err != nil || custom.CustomType != VirtualModelStateEntry || custom.Data == nil {
			continue
		}
		if custom.Data.Provider == provider && custom.Data.ModelID == modelID {
			return custom.Data.State
		}
	}
	return nil
}

// CreateVirtualModel builds the catalog entry of a virtual model.
//
// upstream: virtual-models.ts:159-176 (createVirtualModel)
func CreateVirtualModel(definition VirtualModelDefinition) *ai.Model {
	levels := definition.ThinkingLevels
	if levels == nil {
		levels = []ai.ModelThinkingLevel{ai.ThinkingOff}
	}
	thinkingMap := ai.ThinkingLevelMap{}
	reasoning := false
	for _, level := range []ai.ModelThinkingLevel{ai.ThinkingOff, ai.ThinkingMinimal, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh, ai.ThinkingXHigh, ai.ThinkingMax} {
		if slices.Contains(levels, level) {
			thinkingMap[level] = new(string(level))
		} else {
			thinkingMap[level] = nil
		}
	}
	for _, level := range levels {
		reasoning = reasoning || level != ai.ThinkingOff
	}
	input := definition.Input
	if input == nil {
		input = []string{"text", "image"}
	}
	model := &ai.Model{ID: definition.ID, DisplayName: definition.Name, Input: slices.Clone(input), ThinkingLevelMap: thinkingMap,
		Capabilities: ai.ModelCapabilities{ContextWindow: definition.ContextWindow, MaxOutputTokens: definition.MaxTokens},
		ProviderMeta: ai.ProviderMetadata{ProviderID: definition.Provider, API: VirtualModelAPI, Reasoning: reasoning}}
	model.Provider = unroutedProvider{id: definition.Provider, model: model}
	return model
}

// RegisterVirtualModel registers a virtual model under definition.Provider, which may also list physical models or several virtual models. Re-registering the same provider and id replaces the virtual model. It fails when the id belongs to a physical model of that provider.
//
// upstream: model-runtime.ts:938-960 (registerVirtualModel)
func (runtime *ModelRuntime) RegisterVirtualModel(definition VirtualModelDefinition) error {
	if strings.TrimSpace(definition.Provider) == "" || strings.TrimSpace(definition.ID) == "" {
		return errors.New("Virtual model provider and id must not be empty.")
	}
	if existing := runtime.GetModel(definition.Provider, definition.ID); existing != nil && !IsVirtualModel(existing) {
		return fmt.Errorf("Virtual model %s/%s conflicts with a physical model.", definition.Provider, definition.ID)
	}
	runtime.virtuals.register(definition)
	runtime.syncVirtualCatalog(definition.Provider)
	return nil
}

// UnregisterVirtualModel removes a virtual model. An unknown provider or id is ignored.
//
// upstream: model-runtime.ts:963-970 (unregisterVirtualModel)
func (runtime *ModelRuntime) UnregisterVirtualModel(provider, id string) {
	if runtime.virtuals.unregister(provider, id) {
		runtime.syncVirtualCatalog(provider)
	}
}

// RuntimeModels returns the selection metadata of the composed catalog: the registry's models with the registered virtual models added after each provider's physical models, or after every provider for a provider of only virtual models. A virtual model hides a physical chat model with the same id. Startup model resolution reads it, so `--model`, the saved default and the `--models` scope can name a virtual model.
//
// upstream: model-runtime.ts:285-293 (provider list with virtual models), virtual-models.ts:190-227 (withVirtualModels)
func (runtime *ModelRuntime) RuntimeModels() []icodingagent.RuntimeModel {
	models := runtime.services.Registry().RuntimeModels()
	store := &runtime.virtuals
	store.mu.RLock()
	providers := slices.Clone(store.providers)
	store.mu.RUnlock()
	for _, provider := range providers {
		virtuals := store.models(provider)
		ids := make(map[string]bool, len(virtuals))
		added := make([]icodingagent.RuntimeModel, 0, len(virtuals))
		for _, model := range virtuals {
			ids[model.ID] = true
			added = append(added, icodingagent.RuntimeModel{Provider: provider, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != ""})
		}
		models = slices.DeleteFunc(models, func(model icodingagent.RuntimeModel) bool { return model.Provider == provider && ids[model.ID] })
		last := -1
		for i, model := range slices.Backward(models) {
			if model.Provider == provider {
				last = i
				break
			}
		}
		if last < 0 {
			models = append(models, added...)
		} else {
			models = slices.Insert(models, last+1, added...)
		}
	}
	return models
}

// HasConfiguredAuth reports the provider's configured auth from the registry's published state, without credential I/O. A provider of only virtual models needs no credentials, so it is configured.
//
// upstream: model-runtime.ts:543-545 (hasConfiguredAuth), 953-958 (a provider of only virtual models is configured at registration)
func (runtime *ModelRuntime) HasConfiguredAuth(providerID string) bool {
	registry := runtime.services.Registry()
	return registry.ModelRegistry.HasConfiguredAuth(providerID) || runtime.virtuals.onlyVirtual(providerID, registry.GetProviderModelData)
}

// ResolveModelOptions are the options of [ModelRuntime.ResolveModel].
type ResolveModelOptions struct {
	Reason        ModelRouteReason
	ThinkingLevel ai.ModelThinkingLevel
	// Failed is the failed response of a retry; the messages no longer contain it.
	Failed *ai.AssistantMessage
	// State is the router state the caller stored; the caller also stores the returned state.
	State json.RawMessage
}

// ResolveModel asks a virtual model's router for the model and thinking level of one request. The router must return a physical catalog model whose provider has credentials; the thinking level is clamped to that model. It fails when routing fails.
//
// Previous reports the latest successful response in messages. A retry passes the failed response as options.Failed; messages no longer contains it.
//
// upstream: model-runtime.ts:973-1012 (resolveModel)
func (runtime *ModelRuntime) ResolveModel(ctx context.Context, model *ai.Model, messages []ai.Message, options ResolveModelOptions) (ModelRoute, error) {
	name := fmt.Sprintf("Virtual model %s/%s", model.ProviderMeta.ProviderID, model.ID)
	definition := runtime.virtuals.definition(model.ProviderMeta.ProviderID, model.ID)
	if definition == nil {
		return ModelRoute{}, fmt.Errorf("%s is not registered.", name)
	}
	request := ModelRouteRequest{Model: model, ThinkingLevel: options.ThinkingLevel, Reason: options.Reason, State: options.State}
	for _, message := range slices.Backward(messages) {
		latest, ok := message.(ai.AssistantMessage)
		if !ok || latest.StopReason == ai.StopReasonError || latest.StopReason == ai.StopReasonAborted {
			continue
		}
		if previous := runtime.GetPhysicalModel(latest.Provider, latest.Model); previous != nil {
			request.Previous = &extension.ModelRoutePrevious{Model: previous, ThinkingLevel: latest.ThinkingLevel}
		}
		break
	}
	// A failed routing attempt names the virtual model; there is no physical request to report.
	if failed := options.Failed; failed != nil {
		if failedModel := runtime.GetPhysicalModel(failed.Provider, failed.Model); failedModel != nil {
			request.Failed = &extension.ModelRouteFailed{Model: failedModel, ThinkingLevel: failed.ThinkingLevel, Message: *failed}
		}
	}
	request.Messages = messages
	route, err := definition.Route(ctx, request)
	if err != nil {
		return ModelRoute{}, err
	}
	routedTo := "unknown"
	if route.Model != nil {
		routedTo = route.Model.ProviderMeta.ProviderID + "/" + route.Model.ID
	}
	routed := fmt.Sprintf("%s routed to %s", name, routedTo)
	var target *ai.Model
	if route.Model != nil {
		target = runtime.GetPhysicalModel(route.Model.ProviderMeta.ProviderID, route.Model.ID)
	}
	if target == nil {
		return ModelRoute{}, fmt.Errorf("%s, which is not a physical model.", routed)
	}
	if !runtime.hasConfiguredAuth(ctx, target.ProviderMeta.ProviderID) {
		return ModelRoute{}, fmt.Errorf("%s, which has no credentials.", routed)
	}
	return ModelRoute{Model: target, ThinkingLevel: ai.ClampThinkingLevel(target, route.ThinkingLevel), State: route.State}, nil
}

// GetPhysicalModel returns a catalog chat model that is not virtual, or nil.
//
// upstream: model-runtime.ts:1015-1018 (getPhysicalModel)
func (runtime *ModelRuntime) GetPhysicalModel(provider, id string) *ai.Model {
	model := runtime.GetModel(provider, id)
	if model == nil || IsVirtualModel(model) {
		return nil
	}
	return model
}

// RegisterVirtualModel registers a virtual model with the runtime. It is the registry action extensions' `registerVirtualModel` reaches.
//
// upstream: runner.ts:497-500 (registry.registerVirtualModel)
func (registry *ModelRegistry) RegisterVirtualModel(definition VirtualModelDefinition) error {
	return registry.runtime.RegisterVirtualModel(definition)
}

// UnregisterVirtualModel removes a virtual model from the runtime.
//
// upstream: runner.ts:538-541 (registry.unregisterVirtualModel)
func (registry *ModelRegistry) UnregisterVirtualModel(provider, id string) {
	registry.runtime.UnregisterVirtualModel(provider, id)
}

// RoutedModel is the physical model and thinking level a virtual selection currently resolves to.
type RoutedModel struct {
	Model         *ai.Model
	ThinkingLevel ai.ModelThinkingLevel
}

// RoutedModel returns, under a virtual selection, the physical model and thinking level of the latest successful response, or nil.
//
// upstream: agent-session.ts:1408-1414 (routedModel)
func (s *Session) RoutedModel() *RoutedModel {
	model := s.Model()
	if !IsVirtualModel(model) {
		return nil
	}
	latest := FindLatestResponse(s.agent.Messages())
	if latest == nil {
		return nil
	}
	physical := s.modelRuntime.GetPhysicalModel(latest.Provider, latest.ModelID)
	if physical == nil {
		return nil
	}
	return &RoutedModel{Model: physical, ThinkingLevel: latest.ThinkingLevel}
}

// Session is the AgentSession Pi's FooterComponent reads sessionManager and routedModel from (footer.ts:103-107, :241); the footer's
// consumer-owned interface lists those two members.
var _ icodingagent.FooterSession = (*Session)(nil)

// RoutedModelSelection is RoutedModel in the form the interactive footer renders.
func (s *Session) RoutedModelSelection() *icodingagent.RoutedModelSelection {
	routed := s.RoutedModel()
	if routed == nil {
		return nil
	}
	return &icodingagent.RoutedModelSelection{Model: routed.Model, ThinkingLevel: routed.ThinkingLevel}
}

// limitsModel is the model whose limits apply to the conversation: under a virtual selection, the physical model that answered last.
//
// upstream: agent-session.ts:599-601 (_limitsModel)
func (s *Session) limitsModel() *ai.Model {
	if routed := s.RoutedModel(); routed != nil {
		return routed.Model
	}
	return s.Model()
}

// modelForMessage returns the model whose limits apply to message, or nil when the message came from another model. Under a virtual selection that is the physical model that produced it.
//
// upstream: agent-session.ts:571-578 (_modelForMessage)
func (s *Session) modelForMessage(message *agent.AssistantMessage) *ai.Model {
	model := s.Model()
	if IsVirtualModel(model) {
		return s.modelRuntime.GetPhysicalModel(message.Provider, message.ModelID)
	}
	if model != nil && model.Provider != nil && message.Provider == model.Provider.ID() && message.ModelID == model.ID {
		return model
	}
	return nil
}

// recordSelection records the selection on the current branch when the branch implies another one, so a resume restores it. Tree navigation can leave the latest `model_change` on another branch; responses cannot record a virtual selection because they name physical models.
//
// upstream: agent-session.ts:587-597 (_recordSelection)
func (s *Session) recordSelection() {
	if s.inner == nil || s.modelRuntime == nil {
		return
	}
	model := s.Model()
	if model == nil {
		return
	}
	recorded := GetBranchSelection(s.inner.GetBranch(), s.modelRuntime.GetModel)
	if recorded == nil || (recorded.Provider == providerID(model) && recorded.ModelID == model.ID) {
		return
	}
	recordedModel := s.modelRuntime.GetModel(recorded.Provider, recorded.ModelID)
	if !IsVirtualModel(model) && (recordedModel == nil || !IsVirtualModel(recordedModel)) {
		return
	}
	_, _ = s.inner.AppendModelChange(providerID(model), model.ID)
}

// routeRequest routes the request of a virtual selection. The selection stays in agent state; only this request uses the routed model. Only messages the user wrote start a turn; extension messages can follow them, for example from before_agent_start.
//
// upstream: agent-session.ts:769-806 (_installAgentRequestProjection)
func (s *Session) routeRequest(ctx context.Context, model *ai.Model, thinking ai.ModelThinkingLevel, messages []agent.AgentMessage, failed *agent.AssistantMessage) (ModelRoute, error) {
	lastResponse := -1
	for i, message := range messages {
		if message.Assistant != nil {
			lastResponse = i
		}
	}
	userTurn := slices.ContainsFunc(messages[lastResponse+1:], func(message agent.AgentMessage) bool { return message.User != nil })
	reason := ModelRouteReasonContinuation
	switch {
	case failed != nil:
		reason = ModelRouteReasonRetry
	case userTurn:
		reason = ModelRouteReasonUser
	}
	options := ResolveModelOptions{Reason: reason, ThinkingLevel: thinking, State: GetVirtualModelState(s.inner.GetBranch(), providerID(model), model.ID)}
	if failed != nil {
		response := failed.LLMMessage()
		options.Failed = &response
	}
	route, err := s.modelRuntime.ResolveModel(ctx, model, agent.ConvertToLLM(agent.NormalizeMessages(messages, model)), options)
	if err != nil {
		return ModelRoute{}, err
	}
	if route.State != nil && !bytes.Equal(route.State, options.State) {
		data := VirtualModelStateData{Provider: providerID(model), ModelID: model.ID, State: route.State}
		if id, err := s.inner.AppendCustomEntry(VirtualModelStateEntry, data); err == nil {
			if entry, ok := s.inner.GetEntry(id); ok {
				s.emitEvent(agent.EntryAppendedEvent{Entry: entry.Raw()})
			}
		}
	}
	return route, nil
}

// unroutedProvider is the provider of a virtual model that was not routed, for example `Stream` with API-specific options. Requests for it fail.
//
// upstream: virtual-models.ts:179-183 (unroutedStream)
type unroutedProvider struct {
	id    string
	model *ai.Model
}

func (p unroutedProvider) ID() string { return p.id }
func (unroutedProvider) Close() error { return nil }
func (p unroutedProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return nil, fmt.Errorf("Virtual model %s/%s must be routed before streaming", p.model.ProviderMeta.ProviderID, p.model.ID)
}

// hasConfiguredAuth is upstream's ModelRuntime.hasConfiguredAuth: the provider has credentials. Go reads the credential store where upstream reads its refreshed snapshot, because the snapshot is refreshed in the background after a credential change.
//
// upstream: model-runtime.ts:543-545 (hasConfiguredAuth)
func (runtime *ModelRuntime) hasConfiguredAuth(ctx context.Context, providerID string) bool {
	if runtime.configuredInSnapshot(providerID) || !modelRuntimeRequiresAuth(providerID) {
		return true
	}
	check, err := runtime.CheckAuth(ctx, providerID)
	return err == nil && check != nil
}

// syncVirtualCatalog republishes the availability snapshot after a virtual-model change and refreshes it in the background, as upstream's register and unregister do (`updateModelSnapshot(); void refresh({allowNetwork:false})`).
func (runtime *ModelRuntime) syncVirtualCatalog(string) {
	runtime.startBackground(func(ctx context.Context) {
		_ = runtime.queueAvailabilityRefresh(ctx)
	})
}

// virtualEntry is one registered virtual model.
type virtualEntry struct {
	model      *ai.Model
	definition VirtualModelDefinition
}

// virtualModelStore holds the virtual models by provider id, then model id, in registration order. Virtual models belong to a provider id but are not provider models: the runtime keeps them separately and adds them to the provider's catalog.
//
// upstream: model-runtime.ts:178-179 (virtualModels), virtual-models.ts:190-227 (withVirtualModels)
type virtualModelStore struct {
	mu        sync.RWMutex
	providers []string
	byID      map[string][]*virtualEntry
}

func (store *virtualModelStore) register(definition VirtualModelDefinition) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.byID == nil {
		store.byID = map[string][]*virtualEntry{}
	}
	entry := &virtualEntry{model: CreateVirtualModel(definition), definition: definition}
	entries := store.byID[definition.Provider]
	for i, existing := range entries {
		if existing.model.ID == definition.ID {
			entries[i] = entry
			return
		}
	}
	if len(entries) == 0 && !slices.Contains(store.providers, definition.Provider) {
		store.providers = append(store.providers, definition.Provider)
	}
	store.byID[definition.Provider] = append(entries, entry)
}

func (store *virtualModelStore) unregister(provider, id string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries := store.byID[provider]
	index := slices.IndexFunc(entries, func(entry *virtualEntry) bool { return entry.model.ID == id })
	if index < 0 {
		return false
	}
	entries = slices.Delete(entries, index, index+1)
	if len(entries) == 0 {
		delete(store.byID, provider)
		store.providers = slices.DeleteFunc(store.providers, func(p string) bool { return p == provider })
	} else {
		store.byID[provider] = entries
	}
	return true
}

func (store *virtualModelStore) entry(provider, id string) *virtualEntry {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, entry := range store.byID[provider] {
		if entry.model.ID == id {
			return entry
		}
	}
	return nil
}

func (store *virtualModelStore) model(provider, id string) *ai.Model {
	if entry := store.entry(provider, id); entry != nil {
		return entry.model
	}
	return nil
}

func (store *virtualModelStore) definition(provider, id string) *VirtualModelDefinition {
	if entry := store.entry(provider, id); entry != nil {
		return &entry.definition
	}
	return nil
}

func (store *virtualModelStore) models(provider string) []*ai.Model {
	store.mu.RLock()
	defer store.mu.RUnlock()
	var models []*ai.Model
	for _, entry := range store.byID[provider] {
		models = append(models, entry.model)
	}
	return models
}

// onlyVirtual reports whether virtual models are all a provider lists.
func (store *virtualModelStore) onlyVirtual(provider string, physical func(string) []*ai.Model) bool {
	store.mu.RLock()
	has := len(store.byID[provider]) > 0
	store.mu.RUnlock()
	return has && len(physical(provider)) == 0
}

// overlay adds every provider's virtual models after the physical models. A virtual model hides a physical chat model with the same id, which a catalog refresh can add after registration.
func (store *virtualModelStore) overlay(models []*ai.Model) []*ai.Model {
	store.mu.RLock()
	providers := slices.Clone(store.providers)
	store.mu.RUnlock()
	if len(providers) == 0 {
		return models
	}
	hidden := map[string]bool{}
	for _, provider := range providers {
		for _, model := range store.models(provider) {
			hidden[provider+"\x00"+model.ID] = true
		}
	}
	out := models[:0:0]
	for _, model := range models {
		if !hidden[model.ProviderMeta.ProviderID+"\x00"+model.ID] {
			out = append(out, model)
		}
	}
	for _, provider := range providers {
		out = append(out, store.models(provider)...)
	}
	return out
}

// overlayAvailable adds the virtual models of every available provider, and of every provider of only virtual models, which needs no credentials. only limits the result to one provider. physical lists a provider's catalog models.
func (store *virtualModelStore) overlayAvailable(models []*ai.Model, physical func(string) []*ai.Model, only ...string) []*ai.Model {
	store.mu.RLock()
	providers := slices.Clone(store.providers)
	store.mu.RUnlock()
	if len(providers) == 0 {
		return models
	}
	out := slices.Clone(models)
	for _, provider := range providers {
		if len(only) > 0 && only[0] != provider {
			continue
		}
		virtuals := store.models(provider)
		ids := map[string]bool{}
		for _, model := range virtuals {
			ids[model.ID] = true
		}
		out = slices.DeleteFunc(out, func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == provider && ids[model.ID] })
		last := -1
		for i, model := range out {
			if model.ProviderMeta.ProviderID == provider {
				last = i
			}
		}
		// Virtual models on a physical provider are available when the provider is; a provider of only virtual models is always available.
		if last < 0 && len(physical(provider)) > 0 {
			continue
		}
		if last < 0 {
			out = append(out, virtuals...)
		} else {
			out = slices.Insert(out, last+1, virtuals...)
		}
	}
	return out
}

// streamVirtual routes a request made outside the agent loop. Callers sized it before routing, so the output budget is capped to the routed model. Caller credentials were resolved for the virtual model's provider; another provider resolves its own, so they are not sent to the wrong vendor.
//
// upstream: model-runtime.ts:715-737 (streamSimple, virtual branch)
func (runtime *ModelRuntime) streamVirtual(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	transcript := ai.NormalizeContext(request)
	outer := ai.NewAssistantMessageEventStream()
	ctx = outer.ObservationContext(ctx)
	go func() {
		thinking := ai.ThinkingOff
		if options.Thinking != "" {
			thinking = ai.ModelThinkingLevel(options.Thinking)
		}
		route, err := runtime.ResolveModel(ctx, model, transcript.Messages(), ResolveModelOptions{Reason: ModelRouteReasonDirect, ThinkingLevel: thinking})
		if err != nil {
			runtime.fail(ctx, outer, model, err)
			return
		}
		if limit := route.Model.Capabilities.MaxOutputTokens; options.MaxTokens > 0 && limit > 0 {
			options.MaxTokens = min(options.MaxTokens, limit)
		}
		options.Thinking = route.ThinkingLevel.ReasoningOption()
		if route.Model.ProviderMeta.ProviderID != model.ProviderMeta.ProviderID {
			options.APIKey, options.Headers, options.Env = "", nil, nil
		}
		inner := runtime.StreamSimple(ctx, route.Model, request, options)
		if err := outer.ForwardStream(ctx, inner); err != nil {
			runtime.fail(ctx, outer, route.Model, err)
		}
	}()
	return outer
}
