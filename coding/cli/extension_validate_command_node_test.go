//go:build !pig_strip_node_extensions

package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestInstallValidateOnlyWithMultipleSourcesEmitsSetReport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	fixture := filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture")
	other := filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "ctx-mode.mjs")
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", "--validate-only", "--json", fixture, other})
	})
	if code != 0 {
		t.Fatalf("install validate-only set code = %d, want 0\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var report struct {
		Valid      bool `json:"valid"`
		Extensions []struct {
			Valid      bool   `json:"valid"`
			Name       string `json:"name"`
			Hash       string `json:"hash"`
			Definition struct {
				Form     string `json:"form"`
				Language string `json:"language"`
			} `json:"definition"`
		} `json:"extensions"`
		Placement struct {
			Policy string `json:"policy"`
			Groups []struct {
				Language   string   `json:"language"`
				Strategy   string   `json:"strategy"`
				Hash       string   `json:"hash"`
				Extensions []string `json:"extensions"`
			} `json:"groups"`
			Cells []struct {
				Action     string   `json:"action"`
				GroupID    string   `json:"groupId"`
				Key        string   `json:"key"`
				Hash       string   `json:"hash"`
				Extensions []string `json:"extensions"`
			} `json:"cells"`
		} `json:"placement"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("parse json report: %v\n%s", err, stdout)
	}
	if !report.Valid || len(report.Extensions) != 2 {
		t.Fatalf("valid/extensions = %v/%d, want true/2", report.Valid, len(report.Extensions))
	}
	if report.Placement.Policy != "auto" || len(report.Placement.Cells) != 2 || len(report.Placement.Groups) == 0 {
		t.Fatalf("placement = %+v, want auto with grouped isolated cells", report.Placement)
	}
	for _, group := range report.Placement.Groups {
		if group.Hash == "" {
			t.Fatalf("placement group missing hash: %+v", group)
		}
	}
	for _, cell := range report.Placement.Cells {
		if cell.GroupID == "" || cell.Key == "" || cell.Hash == "" {
			t.Fatalf("placement cell missing group/key/hash: %+v", cell)
		}
	}
	for _, ext := range report.Extensions {
		if !ext.Valid || ext.Hash == "" || ext.Definition.Form == "" || ext.Definition.Language == "" {
			t.Fatalf("extension report = %+v", ext)
		}
	}
}
