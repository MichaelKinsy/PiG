package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func retentionSource(i int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Heading %d\n\n", i)
	for p := range 6 {
		fmt.Fprintf(&sb, "Paragraph %d of message %d with **bold**, *italic*, `code` and a [link](https://example.com/%d) plus enough lorem ipsum dolor sit amet consectetur to wrap past the width of the terminal.\n\n", p, i, p)
	}
	sb.WriteString("- item one\n- item two\n\n```go\nfmt.Println(\"hi\")\nfor i := 0; i < 3; i++ {}\n```\n")
	return sb.String()
}

// The owners of a Markdown that never streams finish it when they build it, so it keeps no streaming memos. The rows
// must be what they were with the memos: the hash of every owner's rows at several widths, themes and expansion states
// was taken on the tree before the change.
func TestMessageOwnersRenderBytes(t *testing.T) {
	prev := ActiveTheme()
	t.Cleanup(func() { SetTheme(prev.Name) })
	src := retentionSource(7) + "\n1. one\n2. two\n\n> quote\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n" + strings.Repeat("A long line without breaks that wraps at every width tested. ", 8) + "\n"
	hash := sha256.New()
	for _, theme := range []string{"dark", "light"} {
		SetTheme(theme)
		for _, width := range []int{20, 40, 80, 120} {
			for _, pad := range []int{0, 1, 3} {
				user := NewUserMessageComponent(src, nil, pad, nil, "id", "ts")
				branch := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: src}, nil, pad)
				compaction := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: src, TokensBefore: 87432}, nil, pad)
				custom := NewCustomMessageComponent(&CustomMessage{CustomType: "x", Content: src}, nil, nil, pad)
				skill := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "skill", Content: src}, nil, pad)
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
	const want = "c409ce9d826528603ded2781f2fe6ce34861028309242375b64cfa3d07d3f103"
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		t.Errorf("owner rows hash %s, want %s", got, want)
	}
}
