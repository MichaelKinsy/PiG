package compaction

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type capturingCompleter struct {
	prompt string
	usage  *ai.Usage
}

func (c *capturingCompleter) CompleteSimple(_ context.Context, _ *ai.Model, _ string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	c.prompt = ""
	for _, message := range messages {
		if message.User != nil {
			if blocks, ok := message.User.Content.(ai.UserContentBlocks); ok {
				for _, block := range blocks {
					if text, ok := block.(ai.TextContent); ok {
						c.prompt += text.Text
					}
				}
			}
		}
	}
	return "the summary", c.usage, nil
}

func branchEntriesWithDetails() []codingagent.SessionEntry {
	return []codingagent.SessionEntry{
		makeBranchSummaryEntry("s", nil, "earlier summary", false, &BranchSummaryDetails{ReadFiles: []string{"a.go", "b.go"}, ModifiedFiles: []string{"b.go", "c.go"}}),
		makeMessageEntry("m", new("s"), 10),
	}
}

// packages/coding-agent/src/core/compaction/branch-summarization.ts:79-85,303-331 (GenerateBranchSummaryOptions
// customInstructions, replaceInstructions): custom instructions are appended to the default prompt after
// "Additional focus: ", replace it only together with replaceInstructions, and replaceInstructions without custom
// instructions keeps the default prompt.
func TestGenerateBranchSummaryCustomAndReplaceInstructionsShapeThePrompt(t *testing.T) {
	for _, c := range []struct {
		name    string
		custom  string
		replace bool
		check   func(prompt string) bool
	}{
		{"default", "", false, func(p string) bool {
			return strings.HasSuffix(p, BRANCH_SUMMARY_PROMPT) && !strings.Contains(p, "Additional focus")
		}},
		{"appended", "mind the tests", false, func(p string) bool {
			return strings.HasSuffix(p, BRANCH_SUMMARY_PROMPT+"\n\nAdditional focus: mind the tests")
		}},
		{"replaced", "only list files", true, func(p string) bool {
			return strings.HasSuffix(p, "</conversation>\n\nonly list files") && !strings.Contains(p, BRANCH_SUMMARY_PROMPT)
		}},
		{"replace without text keeps the default", "", true, func(p string) bool {
			return strings.HasSuffix(p, BRANCH_SUMMARY_PROMPT)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			completer := &capturingCompleter{}
			result := GenerateBranchSummary(t.Context(), branchEntriesWithDetails(), GenerateBranchSummaryOptions{
				Model: &ai.Model{}, Completer: completer, CustomInstructions: c.custom, ReplaceInstructions: c.replace,
			})
			if result.Error != "" || !c.check(completer.prompt) {
				t.Fatalf("error %q, prompt tail %q", result.Error, completer.prompt[max(0, len(completer.prompt)-120):])
			}
		})
	}
}

// branch-summarization.ts:36-38,206-215,373-378 (BranchSummaryResult readFiles, modifiedFiles, usage): file lists
// carry the prior summary's details with read files that were also modified listed only as modified, and the usage
// of the summarizing call is returned.
func TestGenerateBranchSummaryReturnsFileListsAndUsage(t *testing.T) {
	usage := &ai.Usage{Input: 11, Output: 7}
	result := GenerateBranchSummary(t.Context(), branchEntriesWithDetails(), GenerateBranchSummaryOptions{Model: &ai.Model{}, Completer: &capturingCompleter{usage: usage}})
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if !slices.Equal(result.ReadFiles, []string{"a.go"}) || !slices.Equal(result.ModifiedFiles, []string{"b.go", "c.go"}) {
		t.Fatalf("read %v modified %v, want [a.go] and [b.go c.go]", result.ReadFiles, result.ModifiedFiles)
	}
	if result.Usage != usage {
		t.Fatalf("usage = %+v, want the completer's", result.Usage)
	}
	if !strings.Contains(result.Summary, "the summary") {
		t.Fatalf("summary %q lacks the model text", result.Summary)
	}
}

// branch-summarization.ts:85,353 (GenerateBranchSummaryOptions.streamFn): a supplied streamFn replaces the completer.
func TestGenerateBranchSummaryStreamFnReplacesTheCompleter(t *testing.T) {
	completer := &capturingCompleter{}
	called := false
	result := GenerateBranchSummary(t.Context(), branchEntriesWithDetails(), GenerateBranchSummaryOptions{
		Model: &ai.Model{}, Completer: completer,
		StreamFn: func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
			called = true
			return "from the stream", nil, nil
		},
	})
	if !called || completer.prompt != "" || !strings.Contains(result.Summary, "from the stream") {
		t.Fatalf("streamFn called=%v completer prompt=%q summary=%q", called, completer.prompt, result.Summary)
	}
}
