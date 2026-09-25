package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// marshalDetails runs a value through the extension emission boundary and
// returns its JSON, mirroring what an extension's tool_result handler receives.
func marshalDetails(t *testing.T, details any) string {
	t.Helper()
	wire := extension.ToolResultDetailsFor(details)
	if wire == nil {
		return ""
	}
	b, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func truncatedResult() *TruncationResult {
	return &TruncationResult{
		Content:     "kept",
		Truncated:   true,
		TruncatedBy: "bytes",
		TotalLines:  10,
		TotalBytes:  9000,
		OutputLines: 3,
		OutputBytes: 4,
		MaxLines:    2000,
		MaxBytes:    51200,
	}
}

func TestBashDetails_OnlyAttachedWhenTruncated(t *testing.T) {
	// fullOutputPath without truncation must NOT produce details (bash.ts:354).
	if got := marshalDetails(t, &BashDetails{FullOutputPath: "/tmp/x"}); got != "" {
		t.Fatalf("expected no details when not truncated, got %s", got)
	}
	got := marshalDetails(t, &BashDetails{Truncation: truncatedResult(), FullOutputPath: "/tmp/x"})
	if !strings.Contains(got, `"fullOutputPath":"/tmp/x"`) || !strings.Contains(got, `"truncation":{`) {
		t.Fatalf("bash details wire wrong: %s", got)
	}
	if !strings.Contains(got, `"truncatedBy":"bytes"`) || !strings.Contains(got, `"totalLines":10`) {
		t.Fatalf("truncation wire keys not camelCase: %s", got)
	}
}

func TestReadDetails_OnlyTruncationField(t *testing.T) {
	// No truncation -> no details, even though the internal struct carries
	// StartLine/TotalLines for the renderer.
	if got := marshalDetails(t, &ReadDetails{Path: "a.go", StartLine: 1, TotalLines: 5}); got != "" {
		t.Fatalf("expected nil details for untruncated read, got %s", got)
	}
	got := marshalDetails(t, &ReadDetails{Path: "a.go", StartLine: 1, TotalLines: 5, Truncated: true, Truncation: truncatedResult()})
	if strings.Contains(got, "StartLine") || strings.Contains(got, "Path") || strings.Contains(got, "TotalLines") {
		t.Fatalf("read SDK details leaked internal render fields: %s", got)
	}
	if !strings.Contains(got, `"truncation":{`) {
		t.Fatalf("read details missing truncation: %s", got)
	}
}

func TestGrepDetails_Sparse(t *testing.T) {
	if got := marshalDetails(t, &GrepDetails{}); got != "" {
		t.Fatalf("empty grep details should be nil, got %s", got)
	}
	if got := marshalDetails(t, &GrepDetails{MatchLimitReached: 100}); got != `{"matchLimitReached":100}` {
		t.Fatalf("match-limit-only grep details wrong: %s", got)
	}
	if got := marshalDetails(t, &GrepDetails{LinesTruncated: true}); got != `{"linesTruncated":true}` {
		t.Fatalf("lines-truncated-only grep details wrong: %s", got)
	}
	got := marshalDetails(t, &GrepDetails{Truncation: truncatedResult(), MatchLimitReached: 50, LinesTruncated: true})
	for _, want := range []string{`"truncation":{`, `"matchLimitReached":50`, `"linesTruncated":true`} {
		if !strings.Contains(got, want) {
			t.Fatalf("grep details missing %s: %s", want, got)
		}
	}
}

func TestFindAndLsDetails_Sparse(t *testing.T) {
	if got := marshalDetails(t, &FindDetails{}); got != "" {
		t.Fatalf("empty find details should be nil, got %s", got)
	}
	if got := marshalDetails(t, &FindDetails{ResultLimitReached: 200}); got != `{"resultLimitReached":200}` {
		t.Fatalf("find details wrong: %s", got)
	}
	if got := marshalDetails(t, &LsDetails{}); got != "" {
		t.Fatalf("empty ls details should be nil, got %s", got)
	}
	if got := marshalDetails(t, &LsDetails{EntryLimitReached: 1000}); got != `{"entryLimitReached":1000}` {
		t.Fatalf("ls details wrong: %s", got)
	}
}

func TestWriteDetails_NoSDKDetails(t *testing.T) {
	// upstream write attaches no details (write.ts:223).
	if got := marshalDetails(t, &WriteDetails{Path: "x", Content: "y", Overwrote: true}); got != "" {
		t.Fatalf("write should expose no SDK details, got %s", got)
	}
}

func TestToolResultDetailsFor_Passthrough(t *testing.T) {
	// Custom-tool details (already JSON) pass through unchanged.
	custom := map[string]any{"foo": "bar"}
	if got := extension.ToolResultDetailsFor(custom); got == nil {
		t.Fatal("custom details should pass through, got nil")
	}
	// edit details are already SDK-shaped and pass through.
	edit := &EditToolDetails{Diff: "d", Patch: "p", FirstChangedLine: 3}
	b, _ := json.Marshal(extension.ToolResultDetailsFor(edit))
	if !strings.Contains(string(b), `"diff":"d"`) || !strings.Contains(string(b), `"firstChangedLine":3`) {
		t.Fatalf("edit details passthrough wrong: %s", b)
	}
}

// TestReadTool_EmitsTruncationDetails drives the real read tool and asserts the
// extension receives a faithful truncation object.
func TestReadTool_EmitsTruncationDetails(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := range 2100 {
		fmt.Fprintf(&sb, "line%d\n", i+1)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(readParams{Path: "big.txt"})
	res, err := (&ReadTool{CWD: dir}).Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("read failed: %v", err)
	}
	got := marshalDetails(t, res.Details)
	if !strings.Contains(got, `"truncated":true`) || !strings.Contains(got, `"truncatedBy":"lines"`) {
		t.Fatalf("read truncation details wrong: %s", got)
	}
}

// TestLsTool_EmitsEntryLimitDetails drives the real ls tool past its entry
// limit and asserts entryLimitReached reaches the extension. Pure Go (no
// external binary), unlike grep/find which shell out.
func TestLsTool_EmitsEntryLimitDetails(t *testing.T) {
	dir := t.TempDir()
	for i := range 12 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]any{"limit": 5})
	res, err := (&LsTool{CWD: dir}).Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("ls failed: %v %s", err, res.Content)
	}
	got := marshalDetails(t, res.Details)
	if !strings.Contains(got, `"entryLimitReached":5`) {
		t.Fatalf("ls entry-limit details wrong: %s", got)
	}
}
