package codingagent

import (
	"errors"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── Thinking level helpers ──────────────────────────────────────

// levelsForModel returns the supported thinking levels for the model,
// mirroring upstream models.ts:getSupportedThinkingLevels. The returned
// slice excludes levels explicitly mapped to null in the model's
// ThinkingLevelMap (e.g. gpt-5-mini maps "off" → null, so the user
// cannot disable thinking for that model).
func levelsForModel(model *ai.Model) []string {
	levels := ai.GetSupportedThinkingLevels(model)
	out := make([]string, len(levels))
	for i, l := range levels {
		out[i] = string(l)
	}
	return out
}

// refreshThinkingLevel synchronizes the footer and editor with the Session's effective agent state at startup and after a model or thinking-level change.
func (m *InteractiveMode) refreshThinkingLevel() {
	if m.agent == nil {
		return
	}
	level := string(m.agent.ThinkingLevel())
	if level == "" {
		level = string(ai.ThinkingOff)
	}
	m.thinkingLevel = level
	if m.editor != nil {
		m.editor.ThinkingLevel = level
		m.editor.Invalidate()
	}
	if m.statusLine != nil {
		m.statusLine.SetThinkingLevel(level)
	}
}

// maxThinkingIndex returns the highest index in the model's level slice that
// the model supports. 0 means no thinking support (level is always "off").
// Uses slices.Index so adding new levels never requires updating this function.
func maxThinkingIndex(model *ai.Model) int {
	if model == nil {
		return 0
	}
	levels := levelsForModel(model)
	idx := slices.Index(levels, string(model.Capabilities.MaxThinking))
	if idx < 0 {
		return 0
	}
	return idx
}

// initThinkingLevel binds the UI to the Session's already selected and clamped level, including restored session state.
func (m *InteractiveMode) initThinkingLevel() {
	m.hideThinking = m.opts.Settings.HideThinkingBlock
	m.refreshThinkingLevel()
}

// cycleThinkingLevel advances to the next thinking level and updates state.
// Mirrors upstream interactive-mode.ts:3292-3301.
func (m *InteractiveMode) cycleThinkingLevel() {
	maxIdx := maxThinkingIndex(m.opts.Model)
	if maxIdx == 0 {
		if m.statusLine != nil {
			m.statusLine.Flash("Current model does not support thinking", 2*time.Second)
		}
		return
	}

	levels := levelsForModel(m.opts.Model)
	// Pi's session.cycleThinkingLevel reads the Session's level when it runs, so a cycle made before an earlier one settles advances from the
	// level that one set.
	m.requestThinkingLevel(func() string {
		cur := max(slices.Index(levels, string(m.agent.ThinkingLevel())), 0)
		return levels[(cur+1)%(maxIdx+1)]
	}, false)
}

// selectThinkingLevel applies an in-session choice and reports it to the user, as Pi's cycle and select paths call session.setThinkingLevel and
// show the level the Session settled on. Only an explicit save also changes the global default.
func (m *InteractiveMode) selectThinkingLevel(level string, persist bool) {
	m.requestThinkingLevel(func() string { return level }, persist)
}

// requestThinkingLevel is selectThinkingLevel with the level resolved on the owner loop when the request runs.
func (m *InteractiveMode) requestThinkingLevel(resolve func() string, persist bool) {
	m.applyThinkingLevelFrom(resolve, persist, func(level string, err error) {
		if err != nil {
			m.showError(err.Error())
			return
		}
		if m.statusLine != nil {
			message := "Thinking level: " + m.thinkingLevel
			if persist {
				message = "Default thinking level: " + level
			}
			m.statusLine.Flash(message, 2*time.Second)
		}
	})
}

// applyThinkingLevel asks the Session to change reasoning (agent-session.ts setThinkingLevel: it clamps to the model, records the change,
// saves the default when asked, and emits thinking_level_select unawaited), then binds the UI to the level the Session settled on. The Session's
// notification admits an extension handler's synchronous prefix, so the call runs on a background task: only the state mutation runs on the owner
// loop. Requests run one after another in the order made, and finish runs on the owner loop with the outcome.
func (m *InteractiveMode) applyThinkingLevel(level string, persist bool, finish func(error)) {
	m.applyThinkingLevelFrom(func() string { return level }, persist, func(_ string, err error) { finish(err) })
}

// applyThinkingLevelFrom is applyThinkingLevel with the level resolved on the owner loop once the earlier requests have run.
func (m *InteractiveMode) applyThinkingLevelFrom(resolve func() string, persist bool, finish func(string, error)) {
	handle := m.opts.SessionHandle
	if handle == nil {
		finish("", errors.New("interactive: SessionHandle is required"))
		return
	}
	ctx := m.runCtx
	previous := m.thinkingTail
	done := make(chan struct{})
	m.thinkingTail = done
	m.backgroundTasks.Go(func() {
		defer close(done)
		if previous != nil {
			select {
			case <-previous:
			case <-ctx.Done():
				return
			}
		}
		var level string
		if err := m.runOnMainAndWait(ctx, func() error { level = resolve(); return nil }); err != nil {
			return
		}
		err := handle.SetThinkingLevelOnMain(ai.ModelThinkingLevel(level), ModelMutationOptions{Persist: persist}, func(mutate func() error) error {
			return m.runOnMainAndWait(ctx, mutate)
		})
		m.runOnMain(ctx, func() {
			if err == nil {
				if persist {
					m.opts.Settings.DefaultThinkingLevel = level
				}
				m.refreshThinkingLevel()
			}
			finish(level, err)
		})
	})
}

// showThinkingSelector runs the /thinking selector in the editor slot. Enter
// selects a level for this session; app.thinking.save also saves it as the
// default. Mirrors upstream showThinkingSelector.
func (m *InteractiveMode) showThinkingSelector() {
	done := false
	selectLevel := func(level string, persist bool) {
		m.selectThinkingLevel(level, persist)
		done = true
	}
	current := m.thinkingLevel
	if current == "" {
		current = DefaultThinkingLevel
	}
	defaultLevel := m.opts.Settings.DefaultThinkingLevel
	if m.opts.SettingsManager != nil {
		defaultLevel = m.opts.SettingsManager.Get().DefaultThinkingLevel
	}
	if defaultLevel == "" {
		defaultLevel = DefaultThinkingLevel
	}
	selector := NewThinkingSelectorComponent(
		ai.ThinkingLevel(current),
		thinkingLevelsOf(levelsForModel(m.opts.Model)),
		func(level ai.ThinkingLevel) { selectLevel(string(level), false) },
		func() { done = true },
		func(level ai.ThinkingLevel) { selectLevel(string(level), true) },
		ai.ThinkingLevel(defaultLevel),
	)
	m.runEditorSlotComponent(selector, selector.HandleInput, func() bool { return done })
}

// setHiddenThinkingLabel replaces the label of hidden thinking runs in every assistant block and in the blocks created later; an empty label restores the default "Thinking..." (interactive-mode.ts setHiddenThinkingLabel).
func (m *InteractiveMode) setHiddenThinkingLabel(label string) {
	m.hiddenThinkingLabel = label
	if label == "" {
		label = tui.DefaultHiddenThinkingLabel
	}
	for _, b := range m.assistantBlocks {
		b.SetHiddenThinkingLabel(label)
	}
	if m.tuiInst != nil {
		m.tuiInst.RequestRender()
	}
}

// toggleThinkingVisibility flips hideThinking and updates all visible blocks.
// Mirrors upstream interactive-mode.ts:3340-3355.
func (m *InteractiveMode) toggleThinkingVisibility() {
	m.hideThinking = !m.hideThinking

	// Update every assistant block in the session. Single method call per block -
	// mirrors upstream iterating chatContainer children that are AssistantMessageComponent
	// instances (interactive-mode.ts:1638-1643).
	for _, b := range m.assistantBlocks {
		b.SetHideThinkingBlock(m.hideThinking)
	}

	// Persist preference, as upstream toggleThinkingBlockVisibility does
	// (interactive-mode.ts:4431), so /reload re-reads the toggled value.
	m.opts.Settings.HideThinkingBlock = m.hideThinking
	if m.opts.SettingsManager != nil {
		_ = m.opts.SettingsManager.SetHideThinkingBlock(m.hideThinking)
	}

	visibility := "visible"
	if m.hideThinking {
		visibility = "hidden"
	}
	if m.statusLine != nil {
		m.statusLine.Flash("Thinking blocks: "+visibility, 2*time.Second)
	}
}

func thinkingLevelsOf(levels []string) []ai.ThinkingLevel {
	out := make([]ai.ThinkingLevel, len(levels))
	for i, level := range levels {
		out[i] = ai.ThinkingLevel(level)
	}
	return out
}
