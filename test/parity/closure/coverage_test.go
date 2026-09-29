package closure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditGoCoverageReportsCoveredAndUncoveredBranchOutcomes(t *testing.T) {
	root := t.TempDir()
	source := `package sample
func classify(n int) int {
	if n > 0 {
		return 1
	} else {
		return 0
	}
}
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	profile := "mode: set\nsample.go:4.3,4.11 1 1\nsample.go:6.3,6.11 1 0\n"
	audit, err := AuditGoCoverage(root, strings.NewReader(profile), []string{"sample.go"})
	if err != nil {
		t.Fatalf("AuditGoCoverage(): %v", err)
	}
	var trueStatus, falseStatus string
	for _, branch := range audit.Branches {
		if branch.Kind != "if" {
			continue
		}
		switch branch.Outcome {
		case "true":
			trueStatus = branch.Status
		case "false":
			falseStatus = branch.Status
		}
	}
	if trueStatus != "covered" || falseStatus != "uncovered" {
		t.Fatalf("if outcomes = true:%s false:%s, want covered/uncovered", trueStatus, falseStatus)
	}
	report := string(RenderCoverageAudit(audit))
	if !strings.Contains(report, "sample.go\tif\tfalse\t5-7\tuncovered") {
		t.Fatalf("coverage audit omitted uncovered false branch:\n%s", report)
	}
	if !strings.Contains(report, "sample.go\t6.3-6.11\t1\t0") {
		t.Fatalf("coverage audit omitted uncovered block:\n%s", report)
	}
}

func TestParseGoCoverageNormalizesModulePathsAfterRepositoryMove(t *testing.T) {
	profile := strings.Join([]string{
		"mode: set",
		"github.com/MichaelKinsy/PiG/ai/openai.go:10.1,10.2 1 1",
		"github.com/MichaelKinsy/PiG/ai/openai.go:20.1,20.2 1 2",
	}, "\n") + "\n"

	blocks, err := ParseGoCoverage(t.TempDir(), strings.NewReader(profile))
	if err != nil {
		t.Fatalf("ParseGoCoverage(): %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("block count = %d, want 2", len(blocks))
	}
	for _, block := range blocks {
		if block.Path != "ai/openai.go" {
			t.Errorf("block path = %q, want ai/openai.go", block.Path)
		}
	}
}

func TestParseGoCoverageRejectsMalformedAndEscapingInput(t *testing.T) {
	for name, profile := range map[string]string{
		"empty":     "",
		"no mode":   "sample.go:1.1,1.2 1 1\n",
		"malformed": "mode: set\nsample.go:not-a-range 1 1\n",
		"escape":    "mode: set\n../sample.go:1.1,1.2 1 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseGoCoverage(t.TempDir(), strings.NewReader(profile)); err == nil {
				t.Fatalf("ParseGoCoverage(%q) succeeded", profile)
			}
		})
	}
}
