package tui

import (
	"fmt"
	"maps"
	"slices"
)

// ViewOverridableTokens are the theme tokens a frontend view may override
// (extension component kit spec §6).
var ViewOverridableTokens = []string{
	"accent", "muted", "dim", "text", "border", "borderAccent", "borderMuted",
	"success", "warning", "error", "selectedBg", "customMessageBg",
}

// WithTokenColors returns a copy of t whose given tokens use the given colors,
// escaped in t's color mode as the theme constructor escapes a concrete
// color. Faint tokens stay faint. The copy has no JSON source, so
// WithColorMode returns it unchanged. A token that is neither a foreground
// nor a background token of t is an error.
func (t *Theme) WithTokenColors(colors map[string]Color) (*Theme, error) {
	for _, token := range slices.Sorted(maps.Keys(colors)) {
		_, isForeground := t.fgAnsi[token]
		_, isBackground := t.bgAnsi[token]
		if !isForeground && !isBackground {
			return nil, fmt.Errorf("Unknown theme color: %s", token)
		}
	}
	derived := &Theme{
		Name:           t.Name,
		ExportPageBg:   t.ExportPageBg,
		ExportCardBg:   t.ExportCardBg,
		ExportInfoBg:   t.ExportInfoBg,
		fgAnsi:         maps.Clone(t.fgAnsi),
		bgAnsi:         maps.Clone(t.bgAnsi),
		concreteColors: maps.Clone(t.concreteColors),
		dimTokens:      maps.Clone(t.dimTokens),
		ownAppearance:  t.ownAppearance,
		mode:           t.mode,
	}
	var concreteKeys []string
	for _, token := range t.colorKeys {
		if _, ok := t.concreteColors[token]; ok {
			concreteKeys = append(concreteKeys, token)
		}
	}
	mode := t.GetColorMode()
	for _, token := range slices.Sorted(maps.Keys(colors)) {
		color := colors[token]
		if _, ok := derived.concreteColors[token]; !ok {
			concreteKeys = append(concreteKeys, token)
		}
		derived.concreteColors[token] = color
		if _, ok := derived.fgAnsi[token]; ok {
			derived.fgAnsi[token] = ForegroundAnsi(color, mode)
		} else {
			derived.bgAnsi[token] = BackgroundAnsi(color, mode)
		}
	}
	overridden := func(token string) bool { _, ok := colors[token]; return ok }
	derived.defaultForegroundTokens = slices.DeleteFunc(slices.Clone(t.defaultForegroundTokens), overridden)
	derived.defaultBackgroundTokens = slices.DeleteFunc(slices.Clone(t.defaultBackgroundTokens), overridden)
	derived.colorKeys = slices.Concat(concreteKeys, derived.defaultForegroundTokens, derived.defaultBackgroundTokens)
	derived.populateFields()
	return derived, nil
}
