package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestInstallValidateOnlyLoadsAndRegistersExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	fixture := filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture")
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", fixture, "--validate-only", "--json"})
	})
	if code != 0 {
		t.Fatalf("install validate-only code = %d, want 0\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var report struct {
		Valid      bool     `json:"valid"`
		Registered bool     `json:"registered"`
		Tools      []string `json:"tools"`
		Commands   []string `json:"commands"`
		Definition struct {
			Form     string `json:"form"`
			Language string `json:"language"`
		} `json:"definition"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("parse json report: %v\n%s", err, stdout)
	}
	var rawReport map[string]any
	if err := json.Unmarshal([]byte(stdout), &rawReport); err != nil {
		t.Fatal(err)
	}
	if _, exists := rawReport["spec"]; exists {
		t.Fatalf("validation exposed removed manifest metadata: %v", rawReport)
	}
	if !report.Valid || !report.Registered {
		t.Fatalf("report valid/registered = %v/%v, want true/true", report.Valid, report.Registered)
	}
	if !containsString(report.Tools, "echo") {
		t.Fatalf("tools = %v, want echo", report.Tools)
	}
	if !containsString(report.Commands, "ping") {
		t.Fatalf("commands = %v, want ping", report.Commands)
	}
	if report.Definition.Form != "standalone" || report.Definition.Language != "go" {
		t.Fatalf("definition = %+v, want Go standalone", report.Definition)
	}
	if entries, err := os.ReadDir(filepath.Join(pigHome, "cache", "ext")); err != nil || len(entries) == 0 {
		t.Fatalf("extension build cache not populated: entries=%v err=%v", entries, err)
	}
}

// TestInstallValidateOnlyEmitsToolAndCommandDetails locks the scannable text
// surface the platform injection scanner consumes: each registered tool and
// command must carry its model-facing description (and tool prompt guidelines)
// in the JSON report, not just its name.
func TestInstallValidateOnlyEmitsToolAndCommandDetails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	t.Setenv("PIG_HOME", t.TempDir())
	fixture := filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture")
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", fixture, "--validate-only", "--json"})
	})
	if code != 0 {
		t.Fatalf("install validate-only code = %d, want 0\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var report struct {
		ToolDetails []struct {
			Name             string   `json:"name"`
			Description      string   `json:"description"`
			PromptGuidelines []string `json:"promptGuidelines"`
		} `json:"toolDetails"`
		CommandDetails []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"commandDetails"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("parse json report: %v\n%s", err, stdout)
	}
	var echoDesc, pingDesc string
	var sawEcho, sawGuidedGuideline bool
	for _, tool := range report.ToolDetails {
		switch tool.Name {
		case "echo":
			sawEcho, echoDesc = true, tool.Description
		case "guided_tool":
			if containsString(tool.PromptGuidelines, "Use guided_tool when the user asks for guided behavior.") {
				sawGuidedGuideline = true
			}
		}
	}
	if !sawEcho || echoDesc != "Echo back the input" {
		t.Fatalf("echo tool detail = (seen %v, desc %q), want description %q", sawEcho, echoDesc, "Echo back the input")
	}
	if !sawGuidedGuideline {
		t.Fatalf("guided_tool detail missing prompt guideline: %+v", report.ToolDetails)
	}
	for _, c := range report.CommandDetails {
		if c.Name == "ping" {
			pingDesc = c.Description
		}
	}
	if pingDesc != "Respond with pong" {
		t.Fatalf("command detail ping description = %q, want %q", pingDesc, "Respond with pong")
	}
}

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

func TestInstallValidateOnlyRejectsMultipleGoPackageFactories(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	t.Setenv("PIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/multi-extensions\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review", "security"} {
		directory := filepath.Join(root, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		source := `package ` + name + `
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension {
  ext := sdk.New("` + name + `")
  ext.Command("` + name + `", "` + name + ` command", func(sdk.Context, string) error { return nil })
  return ext
}
`
		if err := os.WriteFile(filepath.Join(directory, "extension.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := subprocess.ResolveExtConfig(root)
	if err == nil || !strings.Contains(err.Error(), "contains 2") {
		t.Fatalf("ResolveExtConfig error = %v, want ambiguous factory roots", err)
	}

	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", root, "--validate-only", "--json"})
	})
	if code == 0 || !strings.Contains(stdout+stderr, "contains 2") {
		t.Fatalf("ambiguous validation code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestInstallValidateOnlyBuildsGoWorkspaceExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	t.Setenv("PIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse (\n\t./extension\n\t./shared\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extensionRoot := filepath.Join(root, "extension")
	sharedRoot := filepath.Join(root, "shared")
	for _, directory := range []string{extensionRoot, sharedRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sharedRoot, "go.mod"), []byte("module example.com/shared\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedRoot, "shared.go"), []byte("package shared\nfunc Description() string { return \"workspace command\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionRoot, "go.mod"), []byte("module example.com/workspace-extension\n\ngo 1.26\n\nrequire (\n github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n example.com/shared v0.0.0\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package extension
import (
  sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
  "example.com/shared"
)
func Extension() *sdk.Extension {
  ext := sdk.New("workspace-extension")
  ext.Command("workspace", shared.Description(), func(sdk.Context, string) error { return nil })
  return ext
}
`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", root, "--validate-only", "--json"})
	})
	if code != 0 {
		t.Fatalf("validate code = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var report extensionValidationReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Valid || report.Name != "workspace-extension" || !slices.Contains(report.Commands, "workspace") {
		t.Fatalf("report = %+v", report)
	}
}

func TestAC58InstallValidationRefreshesSDKAndRejectsStaleSelectSignature(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension builds in short mode")
	}
	for _, tc := range []struct {
		name       string
		assignment string
		isolation  string
		wantValid  bool
	}{
		{"current-packed", "_, _, err := ctx.Select(\"Pick\", []string{\"one\"}); return err", "shared-ok", true},
		{"current-isolated", "_, _, err := ctx.Select(\"Pick\", []string{\"one\"}); return err", "strict", true},
		{"stale-two-result-signature", "_, _ = ctx.Select(\"Pick\", []string{\"one\"}); return nil", "shared-ok", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pigHome := t.TempDir()
			t.Setenv("PIG_HOME", pigHome)
			t.Setenv("PIG_SDK_GO_ROOT", "")
			staged := filepath.Join(pigHome, "state", "pigsdk", "sdk")
			if err := os.MkdirAll(staged, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staged, ".pig-sdk-version"), []byte("stale\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staged, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staged, "context.go"), []byte("package sdk\ntype Context struct{}\nfunc (Context) Select(string, []string) (string, bool) { return \"\", false }\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			root := filepath.Join(t.TempDir(), "select-abi")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/select-abi\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			source := `package selectabi
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension {
  ext := sdk.New("select-abi")
  ext.Command("select", "select", func(ctx sdk.Context, _ string) error { ` + tc.assignment + ` })
  return ext
}
`
			if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := captureStdoutStderr(t, func() int {
				return runPackageCommand([]string{"install", root, "--validate-only", "--json"})
			})
			if tc.wantValid && code != 0 {
				t.Fatalf("validation failed: code=%d\nstdout=%s\nstderr=%s", code, stdout, stderr)
			}
			if !tc.wantValid && (code == 0 || !strings.Contains(stdout+stderr, "assignment mismatch")) {
				t.Fatalf("stale signature validation: code=%d\nstdout=%s\nstderr=%s", code, stdout, stderr)
			}
			stagedContext, err := os.ReadFile(filepath.Join(staged, "context.go"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(stagedContext), "(string, bool, error)") {
				t.Fatalf("validation did not refresh stale staged SDK:\n%s", stagedContext)
			}
		})
	}
}

func TestPlacementPlanSeparatesStandalone(t *testing.T) {
	reports := []extensionValidationReport{
		{Valid: true, Name: "a", Hash: "ha", Definition: &extensionValidationSourceReport{Runtime: "subprocess", Language: "go", SDK: "pig-go", Isolation: "shared-ok", Form: "factory"}},
		{Valid: true, Name: "b", Hash: "hb", Definition: &extensionValidationSourceReport{Runtime: "subprocess", Language: "go", SDK: "pig-go", Isolation: "shared-ok", Form: "factory"}},
		{Valid: true, Name: "c", Hash: "hc", Definition: &extensionValidationSourceReport{Runtime: "subprocess", Language: "go", SDK: "pig-go", Isolation: "isolated", Form: "standalone"}},
	}
	plan := isolatedPlacementPlan(reports)
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %+v, want shared group plus isolated group", plan.Groups)
	}
	if plan.Groups[0].Strategy != "pack-candidate" || len(plan.Groups[0].Extensions) != 2 {
		t.Fatalf("shared group = %+v", plan.Groups[0])
	}
	if plan.Groups[1].Strategy != "isolated" || len(plan.Groups[1].Extensions) != 1 || plan.Groups[1].Extensions[0] != "c" {
		t.Fatalf("isolated group = %+v", plan.Groups[1])
	}
}

func TestExtensionContentHashChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := extensionContentHash(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := extensionContentHash(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("hash not stable: %q vs %q", first, second)
	}
	if err := os.WriteFile(file, []byte("package main\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := extensionContentHash(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatalf("hash did not change after content update: %q", third)
	}
}

func TestInstallValidateOnlySetReportsDuplicateTools(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	fixture := filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture")
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", "--validate-only", "--json", fixture, fixture})
	})
	if code == 0 {
		t.Fatalf("install validate-only duplicate set code = 0, want failure\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	var report struct {
		Valid       bool `json:"valid"`
		Diagnostics []struct {
			Code       string   `json:"code"`
			Kind       string   `json:"kind"`
			Name       string   `json:"name"`
			Extensions []string `json:"extensions"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("parse json report: %v\n%s", err, stdout)
	}
	if report.Valid || len(report.Diagnostics) == 0 {
		t.Fatalf("report = %+v, want duplicate diagnostics", report)
	}
	found := false
	for _, diag := range report.Diagnostics {
		if diag.Code == "duplicate_tool" && diag.Kind == "tool" && diag.Name == "echo" && len(diag.Extensions) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want duplicate_tool echo", report.Diagnostics)
	}
}

func TestInstallValidateOnlyReportsStructuredRegistrationFailureJSON(t *testing.T) {
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "go.mod"), []byte("module example.com/bad\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", bad, "--validate-only", "--json"})
	})
	if code == 0 {
		t.Fatalf("install validate-only code = 0, want failure\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	var report struct {
		Valid     bool   `json:"valid"`
		Phase     string `json:"phase"`
		Code      string `json:"code"`
		StderrLog string `json:"stderrLog"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("parse json report: %v\n%s", err, stdout)
	}
	if report.Valid || report.Phase != "connect" || report.Code != "process_exited" || report.Error == "" {
		t.Fatalf("report = %+v, want invalid connect/process_exited with error", report)
	}
	if report.StderrLog == "" {
		t.Fatalf("stderrLog empty in report: %+v", report)
	}
}

func TestInstallValidateOnlyReportsRegistrationFailure(t *testing.T) {
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "go.mod"), []byte("module example.com/bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", bad, "--validate-only"})
	})
	if code == 0 {
		t.Fatalf("install validate-only code = 0, want failure\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "error:") {
		t.Fatalf("stderr missing error:\n%s", stderr)
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func TestValidationUsesRuntimeRegistrationNotSourceClaims(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "runtime-only")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/runtime-only\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => " + sdkRoot + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package runtimeonly
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
// Static claim only: tool static_only must never enter validation output.
func Extension() *sdk.Extension {
  ext := sdk.New("runtime-only")
  ext.Tool("runtime_only", "registered", sdk.Schema{"type":"object"}, func(sdk.Context, map[string]any) (any,error) { return "ok", nil })
  return ext
}
`
	if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	reports, err := validateExtensionRuntimes(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !slices.Equal(reports[0].Tools, []string{"runtime_only"}) {
		t.Fatalf("validation tools = %#v", reports)
	}
}
