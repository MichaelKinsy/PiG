// Package tui is model and thinking selection in the TUI (p2-tui-port.md section 1.7, P4): pi's model selector, thinking selector and scoped models selector as
// overlays, Shift+Tab thinking cycling and Ctrl+P / Shift+Ctrl+P model cycling over the owner's scope (`enabledModels`, else every signed-in model). The model
// is the owner's choice and cost: `Models` and `Chat.configure` are owner-only, so guests get a read-only line. Without a session, a choice sets the default for
// new sessions.
//
// Ports apps/p2/plugins/models/tui.ts.
package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// ModelMeta is the conversation's model (guest-safe metadata, `Chat.model`).
type ModelMeta struct {
	Provider, ModelID, Name string
	ContextWindow           float64
	Reasoning               bool
	ThinkingLevels          []string
}

// ModelChoice is a model with a thinking level.
type ModelChoice struct{ Provider, ModelID, ThinkingLevel string }

// Scoped is a model of the scope, with its level when the pattern gave one.
type Scoped struct{ Provider, ModelID, ThinkingLevel string }

// ModelInfo is a model of the catalog.
type ModelInfo struct {
	Provider, ModelID, Name string
	ThinkingLevels          []string
	ContextWindow           float64
}

// shown is what the commands read: the state with the choices in flight applied. thinkingLevel "" is undefined.
type shown struct {
	model         *ModelMeta
	thinkingLevel string
	defaultModel  *ModelChoice
	scoped        []Scoped
}

// guess is a choice in flight: the conversation's model and level, the default, or the scope (a set flag marks a field present).
type guess struct {
	model         *ModelMeta
	hasModel      bool
	thinkingLevel string
	defaultModel  *ModelChoice
	hasDefault    bool
	scoped        []Scoped
	hasScoped     bool
}

func metaOf(value any) *ModelMeta {
	if p2tui.JObj(value) == nil {
		return nil
	}
	meta := &ModelMeta{Provider: p2tui.JStr(p2tui.JField(value, "provider")), ModelID: p2tui.JStr(p2tui.JField(value, "modelId")), Name: p2tui.JStr(p2tui.JField(value, "name")),
		ContextWindow: p2tui.JNum(p2tui.JField(value, "contextWindow")), Reasoning: p2tui.JBool(p2tui.JField(value, "reasoning"))}
	for _, level := range p2tui.JList(p2tui.JField(value, "thinkingLevels")) {
		meta.ThinkingLevels = append(meta.ThinkingLevels, p2tui.JStr(level))
	}
	return meta
}

func choiceOf(value any) *ModelChoice {
	if p2tui.JObj(value) == nil {
		return nil
	}
	return &ModelChoice{p2tui.JStr(p2tui.JField(value, "provider")), p2tui.JStr(p2tui.JField(value, "modelId")), p2tui.JStr(p2tui.JField(value, "thinkingLevel"))}
}

// StateSlot is the model state's slot (a non-screen slot).
const StateSlot = "service:models"

// ModelState is what the commands need, held per runtime: the owner's catalog, scope and default, and the viewed conversation's model and thinking level. Its
// fields are the owner loop's.
type ModelState struct {
	runtime  *runtime.TuiRuntime
	scope    *chordtui.TuiScope
	owner    *chordtui.TuiScope
	chat     *chordtui.TuiScope
	identity string
	hasID    bool
	models   *host.Reference
	chatRef  *host.Reference
	// shownState is the state as shown: the choices in flight over what the server last sent.
	shownState func() shown
	choosing   chordtui.OptimisticAdd[guess]
	catalog    []ModelInfo
	received   shown
	remove     func()
}

// NewModelState is the model state of rt.
func NewModelState(rt *runtime.TuiRuntime) *ModelState {
	s := &ModelState{runtime: rt, received: shown{}}
	s.scope = chordtui.NewTuiScope(context.Background(), rt.Host, rt.Ctx.RequestRender)
	s.shownState, s.choosing, _ = chordtui.Optimistic(s.scope, func() shown { return s.received }, func(current shown, g guess) shown {
		if g.hasModel {
			current.model = g.model
		}
		if g.thinkingLevel != "" {
			current.thinkingLevel = g.thinkingLevel
		}
		if g.hasDefault {
			current.defaultModel = g.defaultModel
		}
		if g.hasScoped {
			current.scoped = g.scoped
		}
		return current
	})
	s.remove = rt.AddListener(s.changed)
	s.changed()
	return s
}

func (s *ModelState) changed() {
	rt := s.runtime
	// The catalog is owner-only: guests never request it.
	if rt.Role == runtime.RoleOwner && s.owner == nil {
		s.owner = chordtui.NewTuiScope(context.Background(), rt.Host, rt.Ctx.RequestRender)
		ref, err := s.owner.Hold(contract.Models, delta.JsonObjectOf())
		if err == nil {
			s.models = ref
			follow := func(member string, apply func(value delta.JsonValue)) {
				if source := ref.Member(member); source != nil {
					p2tui.FollowMember(source, func(value delta.JsonValue) { rt.Post(func() { apply(value); rt.Ctx.RequestRender() }) })
				}
			}
			follow("models", func(value delta.JsonValue) {
				s.catalog = nil
				for _, each := range p2tui.JList(value) {
					info := ModelInfo{Provider: p2tui.JStr(p2tui.JField(each, "provider")), ModelID: p2tui.JStr(p2tui.JField(each, "modelId")), Name: p2tui.JStr(p2tui.JField(each, "name")), ContextWindow: p2tui.JNum(p2tui.JField(each, "contextWindow"))}
					for _, level := range p2tui.JList(p2tui.JField(each, "thinkingLevels")) {
						info.ThinkingLevels = append(info.ThinkingLevels, p2tui.JStr(level))
					}
					s.catalog = append(s.catalog, info)
				}
			})
			follow("scopedModels", func(value delta.JsonValue) {
				s.received.scoped = nil
				for _, each := range p2tui.JList(value) {
					s.received.scoped = append(s.received.scoped, Scoped{p2tui.JStr(p2tui.JField(each, "provider")), p2tui.JStr(p2tui.JField(each, "modelId")), p2tui.JStr(p2tui.JField(each, "thinkingLevel"))})
				}
			})
			follow("defaultModel", func(value delta.JsonValue) { s.received.defaultModel = choiceOf(value) })
		}
	}
	if s.hasID && rt.Identity() == s.identity {
		return
	}
	s.identity, s.hasID = rt.Identity(), true
	if s.chat != nil {
		s.chat.Dispose()
		s.chat = nil
	}
	s.chatRef = nil
	s.received.model, s.received.thinkingLevel = nil, ""
	if !rt.HasSession {
		return
	}
	s.chat = chordtui.NewTuiScope(context.Background(), rt.Host, rt.Ctx.RequestRender)
	ref, err := s.chat.Hold(chatcontract.Chat, delta.JsonObjectOf("session", rt.Session, "conversation", float64(rt.Conversation)))
	if err != nil {
		return
	}
	s.chatRef = ref
	identity := s.identity
	follow := func(member string, apply func(value delta.JsonValue)) {
		if source := ref.Member(member); source != nil {
			p2tui.FollowMember(source, func(value delta.JsonValue) {
				rt.Post(func() {
					if s.chatRef == ref && s.identity == identity {
						apply(value)
						rt.Ctx.RequestRender()
					}
				})
			})
		}
	}
	follow("model", func(value delta.JsonValue) { s.received.model = metaOf(value) })
	follow("view", func(value delta.JsonValue) {
		s.received.thinkingLevel = p2tui.JStr(p2tui.JField(p2tui.JField(p2tui.JField(value, "docs"), "pi.agent"), "thinkingLevel"))
	})
}

// Model is the viewed conversation's model (guest-safe metadata), nil for none.
func (s *ModelState) Model() *ModelMeta { return s.shownState().model }

// ThinkingLevel is the viewed conversation's level, "" when unknown.
func (s *ModelState) ThinkingLevel() string { return s.shownState().thinkingLevel }

// DefaultModel is the default for new sessions, nil for none.
func (s *ModelState) DefaultModel() *ModelChoice { return s.shownState().defaultModel }

// Scoped is the owner's scope.
func (s *ModelState) Scoped() []Scoped { return s.shownState().scoped }

// Catalog is the owner's model catalog.
func (s *ModelState) Catalog() []ModelInfo { return s.catalog }

// service is the owner's Models reference once live.
func (s *ModelState) service() *host.Reference {
	if ref := s.models; ref != nil {
		if variant, _, _ := ref.Variant(); variant == "owner" && p2runtime.StatusOf(ref) == host.RefStateLive {
			return ref
		}
	}
	return nil
}

// participant is the viewed conversation's participant reference once live.
func (s *ModelState) participant() *host.Reference {
	if ref := s.chatRef; ref != nil {
		if variant, _, _ := ref.Variant(); variant == "participant" && p2runtime.StatusOf(ref) == host.RefStateLive {
			return ref
		}
	}
	return nil
}

func (s *ModelState) selectorModels() []components.SelectorModel {
	var out []components.SelectorModel
	for _, model := range s.catalog {
		out = append(out, components.SelectorModel{Provider: model.Provider, ModelID: model.ModelID, Name: model.Name})
	}
	return out
}

// Render draws nothing.
func (s *ModelState) Render(int) []string { return nil }

// Invalidate is a no-op.
func (s *ModelState) Invalidate() {}

// Dispose releases the references.
func (s *ModelState) Dispose() {
	s.remove()
	s.scope.Dispose()
	if s.owner != nil {
		s.owner.Dispose()
	}
	if s.chat != nil {
		s.chat.Dispose()
	}
}

func stateOf(ctx runtime.TuiContext) *ModelState {
	state, _ := ctx.Surface(StateSlot).(*ModelState)
	return state
}

func describe(state *ModelState) string {
	model := state.Model()
	if model == nil {
		return "no model"
	}
	thinking := state.ThinkingLevel()
	if thinking == "" {
		thinking = "off"
	}
	return render.LineText(model.Provider) + "/" + render.LineText(model.ModelID) + " · thinking " + render.LineText(thinking)
}

// change is a model and/or level a command chose.
type change struct {
	model         *components.SelectorModel
	thinkingLevel string
}

// metaFor is the conversation's model as shown while a change to model is in flight: the catalog's facts about it.
func metaFor(state *ModelState, model components.SelectorModel) *ModelMeta {
	var info *ModelInfo
	for i := range state.catalog {
		if state.catalog[i].Provider == model.Provider && state.catalog[i].ModelID == model.ModelID {
			info = &state.catalog[i]
		}
	}
	meta := &ModelMeta{Provider: model.Provider, ModelID: model.ModelID, Name: model.Name}
	if info != nil {
		meta.Name, meta.ContextWindow, meta.ThinkingLevels = info.Name, info.ContextWindow, info.ThinkingLevels
	}
	meta.Reasoning = len(meta.ThinkingLevels) > 1
	return meta
}

// perform runs call off the loop with the choice shown meanwhile; its failure is shown as an error line.
func perform(ctx runtime.TuiContext, state *ModelState, g guess, call func() error) {
	go func() {
		if err := state.choosing(g, call); err != nil {
			ctx.Post(func() { ctx.Fail(err) })
		}
	}()
}

// choose applies a change: the conversation's (Chat.configure) with a session, else the default. The choice shows at once (the notice, and the selectors and
// cycling start from it); a refused call takes it back and says why.
func choose(ctx runtime.TuiContext, state *ModelState, c change, done string) {
	if ctx.Identity().Session != "" {
		chat := state.participant()
		if chat == nil {
			ctx.Notify("This conversation is not connected or read-only", runtime.NoticeError)
			return
		}
		g := guess{thinkingLevel: c.thinkingLevel}
		pairs := []any{}
		if c.model != nil {
			g.model, g.hasModel = metaFor(state, *c.model), true
			pairs = append(pairs, "model", delta.JsonObjectOf("provider", c.model.Provider, "modelId", c.model.ModelID))
		}
		if c.thinkingLevel != "" {
			pairs = append(pairs, "thinkingLevel", c.thinkingLevel)
		}
		ctx.Notify(done, runtime.NoticeInfo)
		perform(ctx, state, g, func() error {
			_, err := chat.Call("configure", delta.JsonObjectOf(pairs...), context.Background())
			return err
		})
		return
	}
	var model *components.SelectorModel
	if c.model != nil {
		model = c.model
	}
	if model != nil {
		setDefault(ctx, state, ModelChoice{model.Provider, model.ModelID, c.thinkingLevel}, done)
	} else if current := state.DefaultModel(); current != nil {
		setDefault(ctx, state, ModelChoice{current.Provider, current.ModelID, c.thinkingLevel}, done)
	}
}

// setDefault sets the default for new sessions (keeping the default's level unless a level is given), shown at once.
func setDefault(ctx runtime.TuiContext, state *ModelState, choice ModelChoice, done string) {
	service := state.service()
	if service == nil {
		ctx.Notify("Models are not available", runtime.NoticeError)
		return
	}
	level := choice.ThinkingLevel
	if level == "" {
		if current := state.DefaultModel(); current != nil {
			level = current.ThinkingLevel
		}
	}
	if level == "" {
		level = "off"
	}
	defaultModel := ModelChoice{choice.Provider, choice.ModelID, level}
	ctx.Notify(done, runtime.NoticeInfo)
	perform(ctx, state, guess{defaultModel: &defaultModel, hasDefault: true}, func() error {
		_, err := service.Call("setDefault", delta.JsonObjectOf("provider", defaultModel.Provider, "modelId", defaultModel.ModelID, "thinkingLevel", defaultModel.ThinkingLevel), context.Background())
		return err
	})
}

// Ready is whether the owner's catalog is live: the selectors change the model only then.
func (m *ModelState) Ready() bool { return m.service() != nil }

// readOnly: guests (and the owner without the catalog yet) see the model read-only.
func readOnly(ctx runtime.TuiContext, state *ModelState) bool {
	if ctx.Role() == runtime.RoleOwner && state != nil && state.service() != nil {
		return false
	}
	switch {
	case state == nil:
		ctx.Notify("No model", runtime.NoticeInfo)
	case ctx.Role() == runtime.RoleOwner:
		ctx.Notify("Model: "+describe(state), runtime.NoticeInfo)
	default:
		ctx.Notify("Model: "+describe(state)+" (only the owner changes it)", runtime.NoticeInfo)
	}
	return true
}

func openModelSelector(ctx runtime.TuiContext, initialSearch string) {
	state := stateOf(ctx)
	if state == nil || readOnly(ctx, state) {
		return
	}
	var current *components.ModelRef
	if ctx.Identity().Session != "" {
		if model := state.Model(); model != nil {
			current = &components.ModelRef{Provider: model.Provider, ModelID: model.ModelID}
		}
	} else if model := state.DefaultModel(); model != nil {
		current = &components.ModelRef{Provider: model.Provider, ModelID: model.ModelID}
	}
	var scoped []components.SelectorModel
	for _, ref := range state.Scoped() {
		for _, model := range state.catalog {
			if model.Provider == ref.Provider && model.ModelID == ref.ModelID {
				scoped = append(scoped, components.SelectorModel{Provider: model.Provider, ModelID: model.ModelID, Name: model.Name})
				break
			}
		}
	}
	options := components.ModelSelectorOptions{Models: state.selectorModels(), Scoped: scoped, Current: current, InitialSearch: initialSearch}
	if ctx.Identity().Session == "" {
		options.Title = "Choose default model for new sessions"
	}
	if model := state.DefaultModel(); model != nil {
		options.DefaultModel = &components.ModelRef{Provider: model.Provider, ModelID: model.ModelID}
	}
	var handle runtime.TuiOverlayHandle
	options.OnSelect = func(model components.SelectorModel) {
		handle.Close()
		choose(ctx, state, change{model: &model}, fmt.Sprintf("Model: %s/%s", model.Provider, model.ModelID))
	}
	options.OnSelectAsDefault = func(model components.SelectorModel) {
		handle.Close()
		thinking := state.ThinkingLevel()
		if thinking == "" {
			if current := state.DefaultModel(); current != nil {
				thinking = current.ThinkingLevel
			}
		}
		if thinking == "" {
			thinking = "off"
		}
		setDefault(ctx, state, ModelChoice{model.Provider, model.ModelID, thinking}, fmt.Sprintf("Default model: %s/%s", model.Provider, model.ModelID))
		if ctx.Identity().Session != "" {
			choose(ctx, state, change{model: &model}, fmt.Sprintf("Model: %s/%s", model.Provider, model.ModelID))
		}
	}
	options.OnCancel = func() { handle.Close() }
	handle = ctx.Overlay("models", components.NewModelSelector(runtime.Selectors(ctx), options), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
}

func openThinkingSelector(ctx runtime.TuiContext) {
	state := stateOf(ctx)
	if state == nil || readOnly(ctx, state) {
		return
	}
	levels := []string{"off"}
	if model := state.Model(); model != nil && len(model.ThinkingLevels) > 0 {
		levels = model.ThinkingLevels
	}
	current := state.ThinkingLevel()
	if current == "" {
		current = "off"
		if d := state.DefaultModel(); d != nil && d.ThinkingLevel != "" {
			current = d.ThinkingLevel
		}
	}
	options := components.ThinkingSelectorOptions{Current: current, Levels: levels}
	if d := state.DefaultModel(); d != nil {
		options.DefaultLevel = d.ThinkingLevel
	}
	var handle runtime.TuiOverlayHandle
	options.OnSelect = func(level string) {
		handle.Close()
		choose(ctx, state, change{thinkingLevel: level}, "Thinking level: "+level)
	}
	options.OnSelectAsDefault = func(level string) {
		handle.Close()
		var model *ModelChoice
		if meta := state.Model(); meta != nil {
			model = &ModelChoice{Provider: meta.Provider, ModelID: meta.ModelID}
		} else {
			model = state.DefaultModel()
		}
		if model == nil {
			return
		}
		setDefault(ctx, state, ModelChoice{model.Provider, model.ModelID, level}, "Default thinking level: "+level)
		if ctx.Identity().Session != "" {
			choose(ctx, state, change{thinkingLevel: level}, "Thinking level: "+level)
		}
	}
	options.OnCancel = func() { handle.Close() }
	handle = ctx.Overlay("thinking", components.NewThinkingSelector(runtime.Selectors(ctx), options), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
}

// cycleModel is pi's Ctrl+P / Shift+Ctrl+P: the next model of the scope (or of every signed-in model), with its scoped level.
func cycleModel(ctx runtime.TuiContext, direction int) {
	state := stateOf(ctx)
	if state == nil || readOnly(ctx, state) {
		return
	}
	ring := state.Scoped()
	if len(ring) == 0 {
		for _, model := range state.catalog {
			ring = append(ring, Scoped{Provider: model.Provider, ModelID: model.ModelID})
		}
	}
	if len(ring) == 0 {
		return
	}
	var current *ModelChoice
	if meta := state.Model(); meta != nil {
		current = &ModelChoice{Provider: meta.Provider, ModelID: meta.ModelID}
	} else {
		current = state.DefaultModel()
	}
	index := -1
	if current != nil {
		index = slices.IndexFunc(ring, func(ref Scoped) bool { return ref.Provider == current.Provider && ref.ModelID == current.ModelID })
	}
	next := ring[((index+direction)%len(ring)+len(ring))%len(ring)]
	name := next.ModelID
	for _, model := range state.catalog {
		if model.Provider == next.Provider && model.ModelID == next.ModelID {
			name = model.Name
		}
	}
	model := components.SelectorModel{Provider: next.Provider, ModelID: next.ModelID, Name: name}
	done := fmt.Sprintf("Model: %s/%s", next.Provider, next.ModelID)
	if next.ThinkingLevel != "" {
		done += " · thinking " + next.ThinkingLevel
	}
	choose(ctx, state, change{model: &model, thinkingLevel: next.ThinkingLevel}, done)
}

// cycleThinking is pi's Shift+Tab: the next of the model's thinking levels.
func cycleThinking(ctx runtime.TuiContext) {
	state := stateOf(ctx)
	if state == nil || readOnly(ctx, state) {
		return
	}
	var levels []string
	if model := state.Model(); model != nil {
		levels = model.ThinkingLevels
	}
	if len(levels) <= 1 {
		ctx.Notify("This model has no thinking levels to cycle", runtime.NoticeInfo)
		return
	}
	current := state.ThinkingLevel()
	if current == "" {
		current = "off"
	}
	next := levels[(slices.Index(levels, current)+1)%len(levels)]
	choose(ctx, state, change{thinkingLevel: next}, "Thinking level: "+next)
}

func openScopedModels(ctx runtime.TuiContext) {
	state := stateOf(ctx)
	if state == nil || readOnly(ctx, state) {
		return
	}
	var enabled []components.ModelRef
	for _, ref := range state.Scoped() {
		enabled = append(enabled, components.ModelRef{Provider: ref.Provider, ModelID: ref.ModelID})
	}
	var handle runtime.TuiOverlayHandle
	handle = ctx.Overlay("scoped-models", components.NewScopedModelsSelector(runtime.Selectors(ctx), components.ScopedModelsSelectorOptions{
		Models:  state.selectorModels(),
		Enabled: enabled,
		OnSave: func(patterns []string) {
			handle.Close()
			service := state.service()
			if service == nil {
				return
			}
			if len(patterns) > 0 {
				ctx.Notify(fmt.Sprintf("Scoped models: %d", len(patterns)), runtime.NoticeInfo)
			} else {
				ctx.Notify("Model scope cleared", runtime.NoticeInfo)
			}
			// The selector saves exact `provider/modelId` patterns: cycling uses them at once.
			var scoped []Scoped
			list := make([]any, len(patterns))
			for i, pattern := range patterns {
				list[i] = pattern
				slash := strings.Index(pattern, "/")
				scoped = append(scoped, Scoped{Provider: pattern[:max(slash, 0)], ModelID: pattern[slash+1:]})
			}
			perform(ctx, state, guess{scoped: scoped, hasScoped: true}, func() error {
				_, err := service.Call("setEnabledModels", delta.JsonObjectOf("patterns", list), context.Background())
				return err
			})
		},
		OnCancel: func() { handle.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
}

// Commands are the model commands.
var Commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("app.model.select", runtime.TuiCommand{
		Title: "Select model", Category: "Model", When: "!overlay",
		Slash: &runtime.TuiCommandSlash{
			Name: "model", ArgumentHint: "[provider/model]",
			Complete: func(prefix string, ctx runtime.TuiContext) ([]pitui.AutocompleteItem, error) {
				state := stateOf(ctx)
				if state == nil {
					return nil, nil
				}
				lower := strings.ToLower(prefix)
				var items []pitui.AutocompleteItem
				for _, model := range state.catalog {
					ref := model.Provider + "/" + model.ModelID
					if strings.Contains(strings.ToLower(ref), lower) {
						items = append(items, pitui.AutocompleteItem{Value: ref, Label: ref})
					}
					if len(items) == 30 {
						break
					}
				}
				return items, nil
			},
		},
		Run: func(ctx runtime.TuiContext, args string) error {
			state := stateOf(ctx)
			query := strings.TrimSpace(args)
			// pi: `/model provider/id` switches at once when it names exactly one model.
			var exact *components.SelectorModel
			if state != nil && query != "" {
				for _, model := range state.catalog {
					if strings.EqualFold(model.Provider+"/"+model.ModelID, query) {
						exact = &components.SelectorModel{Provider: model.Provider, ModelID: model.ModelID, Name: model.Name}
						break
					}
				}
			}
			if state != nil && exact != nil && !readOnly(ctx, state) {
				choose(ctx, state, change{model: exact}, fmt.Sprintf("Model: %s/%s", exact.Provider, exact.ModelID))
			} else if exact == nil {
				openModelSelector(ctx, query)
			}
			return nil
		},
	}),
	p2runtime.Entry("app.model.cycleForward", runtime.TuiCommand{Title: "Cycle to next model", Category: "Model", When: "chat && !overlay", Run: func(ctx runtime.TuiContext, _ string) error { cycleModel(ctx, 1); return nil }}),
	p2runtime.Entry("app.model.cycleBackward", runtime.TuiCommand{Title: "Cycle to previous model", Category: "Model", When: "chat && !overlay", Run: func(ctx runtime.TuiContext, _ string) error { cycleModel(ctx, -1); return nil }}),
	p2runtime.Entry("app.thinking.cycle", runtime.TuiCommand{Title: "Cycle thinking level", Category: "Model", When: "chat && !overlay && !autocomplete", Run: func(ctx runtime.TuiContext, _ string) error { cycleThinking(ctx); return nil }}),
	p2runtime.Entry("p2.thinking.select", runtime.TuiCommand{Title: "Select thinking level", Category: "Model", When: "!overlay", Slash: &runtime.TuiCommandSlash{Name: "thinking"}, Run: func(ctx runtime.TuiContext, _ string) error { openThinkingSelector(ctx); return nil }}),
	p2runtime.Entry("p2.models.scope", runtime.TuiCommand{Title: "Scoped models for cycling", Category: "Model", When: "owner && !overlay", Slash: &runtime.TuiCommandSlash{Name: "scoped-models"}, Run: func(ctx runtime.TuiContext, _ string) error { openScopedModels(ctx); return nil }}),
}

// Facet is the plugin's TUI facet.
var Facet = runtime.TuiFacet(StateSlot, func(rt *runtime.TuiRuntime) runtime.TuiSurface { return NewModelState(rt) }, Commands, nil)
