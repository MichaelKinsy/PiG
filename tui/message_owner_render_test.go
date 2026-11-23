//go:build !pig_strip_syntax_highlight

package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The owners of a Markdown that never streams finish it when they build it, so it keeps no streaming memos. The rows
// must be what they were with the memos: the hash of every owner's rows at several widths, themes and expansion states
// was taken on the tree before the change. It was taken again on the tree that carries Pi 2ce9d527 (#10925), which
// drops the space after a styled word at a wrap boundary (packages/tui/src/utils.ts:997): the rows that wrap after a
// styled word changed, and the tree without the memos renders the same bytes as the tree with them on both sides of
// that fix. It was taken again on the tree that carries Pi 562da75f9, which sets default: (s) => t.fg("text", s) in
// buildCliHighlightTheme (packages/coding-agent/src/modes/interactive/theme/theme.ts:894), wrapping unscoped code tokens
// in the theme text foreground escape.
func TestMessageOwnersRenderBytes(t *testing.T) {
	prev := ActiveTheme()
	t.Cleanup(func() { SetTheme(prev.Name) })
	// The hash was taken on a terminal that is not truecolor; detection reports truecolor for a Windows host or COLORTERM=truecolor.
	previousCaps := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previousCaps) })
	SetCapabilities(TerminalCapabilities{})
	src := retentionSource(7) + "\n1. one\n2. two\n\n> quote\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n" + strings.Repeat("A long line without breaks that wraps at every width tested. ", 8) + "\n"
	hash := sha256.New()
	for _, theme := range []string{"dark", "light"} {
		SetTheme(theme)
		for _, width := range []int{20, 40, 80, 120} {
			for _, pad := range []int{0, 1, 3} {
				user := NewUserMessageComponent(src, nil, pad, nil, "id", "ts")
				branch := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: src}, nil, pad, nil, "", "")
				compaction := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: src, TokensBefore: 87432}, nil, pad, nil, "", "")
				custom := NewCustomMessageComponent(&CustomMessage{CustomType: "x", Content: src}, nil, nil, pad, nil, "", "")
				skill := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "skill", Content: src}, nil, pad, nil, "", "")
				for _, expanded := range []bool{false, true} {
					branch.SetExpanded(expanded)
					compaction.SetExpanded(expanded)
					custom.SetExpanded(expanded)
					skill.SetExpanded(expanded)
					for _, c := range []Component{user, branch, compaction, custom, skill} {
						// Render, invalidate and render again: a retained memo would serve the second render.
						for range 2 {
							hash.Write([]byte(strings.Join(c.Render(width), "\n")))
							hash.Write([]byte{0})
							c.Invalidate()
						}
					}
				}
			}
		}
	}
	const want = "6ddab6b29e98ea6f623d37a49387a164017e7fd29aad2afdaa04fcc6b615ecd6"
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		t.Errorf("owner rows hash %s, want %s", got, want)
	}
}
