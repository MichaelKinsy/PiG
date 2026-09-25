package codingagent

import (
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
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

// thinkingLevelToAI maps the user-facing display string to ai.ThinkingLevel.
func thinkingLevelToAI(level string) ai.ThinkingLevel {
	switch level {
	case "minimal":
		return ai.ThinkingMinimal
	case "low":
		return ai.ThinkingLow
	case "medium":
		return ai.ThinkingMedium
	case "high":
		return ai.ThinkingHigh
	case "xhigh":
		return ai.ThinkingXHigh
	case "max":
		return ai.ThinkingMax
	}
	return ai.ThinkingNone
}

// refreshThinkingLevel re-reads the thinking level from the agent after a
// model switch, which applies the target model's level (upstream
// _getThinkingLevelForModelSwitch). Upstream's footer and editor border read
// session.thinkingLevel on every render; pig caches it for both, so each
// switch refreshes them.
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

// initThinkingLevel sets the initial thinking level from settings, clamped
// to model capabilities. Called once during Run() setup.
func (m *InteractiveMode) initThinkingLevel() {
	m.hideThinking = m.opts.Settings.HideThinkingBlock

	// Start from the persisted default (if any), fallback to "medium"
	// (matches upstream DEFAULT_THINKING_LEVEL in defaults.ts).
	// --thinking flag overrides the setting (upstream interactive-mode.ts:1249).
	start := m.opts.Settings.DefaultThinkingLevel
	if model := m.opts.Model; model != nil {
		if perModel := m.opts.Settings.ModelThinkingLevels[model.ProviderMeta.ProviderID+"/"+model.ID]; perModel != "" {
			start = perModel
		}
	}
	if m.opts.ThinkingLevel != "" {
		start = m.opts.ThinkingLevel
	}
	if start == "" {
		start = "medium"
	}

	levels := levelsForModel(m.opts.Model)
	maxIdx := maxThinkingIndex(m.opts.Model)
	if maxIdx == 0 {
		start = "off"
	} else {
		// Find the level in the cycle and clamp to max supported.
		idx := min(max(slices.Index(levels, start), 0), maxIdx)
		start = levels[idx]
	}

	m.thinkingLevel = start
	m.agent.SetThinkingLevel(thinkingLevelToAI(start))
	m.editor.ThinkingLevel = start
	if m.statusLine != nil {
		m.statusLine.SetThinkingLevel(start)
	}
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
	cur := max(slices.Index(levels, m.thinkingLevel), 0)
	next := (cur + 1) % (maxIdx + 1)
	m.selectThinkingLevel(levels[next], false)
}

// selectThinkingLevel applies an in-session choice and reports it to the user.
// Only an explicit save also changes the global default.
func (m *InteractiveMode) selectThinkingLevel(level string, persist bool) {
	if err := m.applyThinkingLevel(level, persist); err != nil {
		m.showError(err.Error())
		return
	}
	if m.statusLine != nil {
		message := "Thinking level: " + level
		if persist {
			message = "Default thinking level: " + level
		}
		m.statusLine.Flash(message, 2*time.Second)
	}
}

// applyThinkingLevel updates reasoning without adding a selection notice to the transcript.
func (m *InteractiveMode) applyThinkingLevel(level string, persist bool) error {
	prev := m.thinkingLevel
	m.thinkingLevel = level
	m.agent.SetThinkingLevel(thinkingLevelToAI(level))
	m.editor.ThinkingLevel = level
	m.editor.Invalidate()

	if persist {
		if m.opts.SettingsManager != nil {
			if err := m.opts.SettingsManager.SetDefaultThinkingLevel(level); err != nil {
				return err
			}
		}
		m.opts.Settings.DefaultThinkingLevel = level
	}
	if level != prev && m.currentSession() != nil {
		if err := m.currentSession().AppendThinkingLevelChange(level); err != nil {
			return err
		}
	}
	if m.statusLine != nil {
		m.statusLine.SetThinkingLevel(level)
	}
	if level != prev {
		// Extension handlers run off the input loop; level and prev are captured values.
		go emitThinkingLevelSelect(m.newRunner, level, prev)
	}
	return nil
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
		current,
		levelsForModel(m.opts.Model),
		func(level string) { selectLevel(level, false) },
		func() { done = true },
		func(level string) { selectLevel(level, true) },
		defaultLevel,
	)
	m.runEditorSlotComponent(selector, selector.HandleInput, func() bool { return done })
}

// toggleThinkingVisibility flips hideThinking and updates all visible blocks.
// Mirrors upstream interactive-mode.ts:3340-3355.
func (m *InteractiveMode) toggleThinkingVisibility() {
	m.hideThinking = !m.hideThinking

	// Update every assistant block in the session. Single method call per block -
	// mirrors upstream iterating chatContainer children that are AssistantMessageComponent
	// instances (interactive-mode.ts:1638-1643).
	for _, b := range m.assistantBlocks {
		b.SetHiddenThinking(m.hideThinking)
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
