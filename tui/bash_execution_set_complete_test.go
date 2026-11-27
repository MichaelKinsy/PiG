package tui

import (
	"strings"
	"testing"
)

// upstream: packages/coding-agent/src/modes/interactive/components/bash-execution.ts:101-121,199-202 — setComplete(exitCode, cancelled,
// truncationResult?, fullOutputPath?): the truncation row needs a truncated result and a full-output path; a non-truncated or absent result
// shows none; cancel and a non-zero exit show their status rows.
func TestBashExecutionSetCompleteResult(t *testing.T) {
	seven := 7
	tests := []struct {
		name       string
		exitCode   *int
		cancelled  bool
		truncation *TruncationResult
		path       string
		want       []string
		not        []string
	}{
		{"truncated with a path", nil, false, &TruncationResult{Truncated: true}, "/tmp/full.log", []string{"Output truncated. Full output: /tmp/full.log"}, nil},
		{"truncated without a path", nil, false, &TruncationResult{Truncated: true}, "", []string{"Output truncated"}, []string{"Full output:"}},
		{"a path without truncation", nil, false, nil, "/tmp/full.log", nil, []string{"Output truncated"}},
		{"an untruncated result with a path", nil, false, &TruncationResult{}, "/tmp/full.log", nil, []string{"Output truncated"}},
		{"cancelled", nil, true, nil, "", []string{"(cancelled)"}, nil},
		{"exit code", &seven, false, nil, "", []string{"(exit 7)"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBashExecutionComponent("run", nil, false, 1)
			b.AppendOutput("out")
			b.SetComplete(tt.exitCode, tt.cancelled, tt.truncation, tt.path)
			got := strings.Join(b.Render(100), "\n")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Fatalf("missing %q in %q", w, got)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Fatalf("unexpected %q in %q", n, got)
				}
			}
		})
	}
}

func TestBashExecutionSetCompleteWithErrorRendersReason(t *testing.T) {
	b := NewBashExecutionComponent("seq 3000", nil, false, 1)
	b.AppendOutput("tail")
	b.SetCompleteWithError(new(0), false, &TruncationResult{Truncated: true}, "", "ENOSPC: no space left")
	got := strings.Join(strings.Fields(strings.Join(b.Render(120), " ")), " ")
	if !strings.Contains(got, "Output truncated. Could not save the full output: ENOSPC: no space left") {
		t.Fatalf("render = %q, want fullOutputError note", got)
	}

	b2 := NewBashExecutionComponent("seq 3000", nil, false, 1)
	b2.SetCompleteWithOutputAndError(new(0), false, true, "tail2", "", "EACCES: open")
	got2 := strings.Join(strings.Fields(strings.Join(b2.Render(120), " ")), " ")
	if !strings.Contains(got2, "tail2") || !strings.Contains(got2, "Output truncated. Could not save the full output: EACCES: open") {
		t.Fatalf("render2 = %q", got2)
	}
}
