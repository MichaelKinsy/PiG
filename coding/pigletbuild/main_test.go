package pigletbuild

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

func TestParseArgsAcceptsBuilderAndRejectsUnregisteredVerification(t *testing.T) {
	if _, opts, _, _, err := parseArgs([]string{"release", "--format", "binary", "--builder", "docker"}); err != nil || opts.Builder != "docker" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	if _, opts, _, _, err := parseArgs([]string{"release", "--format", "binary", "--sign-key", "author.key"}); err != nil || opts.SignKeyPath != "author.key" {
		t.Fatalf("signing opts=%+v err=%v", opts, err)
	}
	if _, _, _, _, err := parseArgs([]string{"release", "--format", "binary", "--verification", "signed"}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("verification error = %v", err)
	}
	if _, opts, _, _, err := parseArgs([]string{"release", "--format", "binary", "--builder", "native", "--verification", "basic", "--no-input"}); err != nil || opts.Builder != "native" || opts.Verification != "basic" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
}

func TestParseArgsRejectsRemovedBuildFlags(t *testing.T) {
	for _, args := range [][]string{
		{"release", "--sandbox", "env"},
		{"release", "--tier", "fuse"},
		{"release", "--strict"},
		{"release", "--with-settings"},
		{"release", "--update-url", "https://example.com/update.json"},
		{"release", "--version", "1.2.0"},
		{"release", "--piglet-version", "1.2.0"},
	} {
		if _, _, _, _, err := parseArgs(args); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("parseArgs(%v) error = %v", args, err)
		}
	}
}

func TestParseArgsAcceptsFormatsAndArtifactBoundaries(t *testing.T) {
	for _, format := range []string{"script", "binary", "image"} {
		_, opts, _, _, err := parseArgs([]string{"release", "--format", format})
		if err != nil || opts.Format != format {
			t.Fatalf("format %q: opts=%+v err=%v", format, opts, err)
		}
	}
	if _, _, _, _, err := parseArgs([]string{"release", "--format", "archive"}); err == nil || !strings.Contains(err.Error(), "script, binary, or image") {
		t.Fatalf("format error = %v", err)
	}
	if _, opts, _, _, err := parseArgs([]string{"release", "--format", "script", "--locked", "--record", "record.json"}); err != nil || !opts.Locked || opts.Record != "record.json" {
		t.Fatalf("script artifact boundary parse: opts=%+v err=%v", opts, err)
	}
	if _, _, _, _, err := parseArgs([]string{"release", "--format", "binary", "--locked"}); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("binary locked error = %v", err)
	}
	if _, _, _, _, err := parseArgs([]string{"release"}); err == nil || !strings.Contains(err.Error(), "--format is required") {
		t.Fatalf("missing format error = %v", err)
	}
}

func TestRunBuildRejectsRequiredFusedFallback(t *testing.T) {
	dir := t.TempDir()
	extensionDir := filepath.Join(dir, "sidecar")
	if err := os.Mkdir(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "sidecar.py"), []byte("def new_extension() -> Extension:\n    return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(dir, "nonfused.yaml")
	pigletSource := "name: nonfused\nbuild:\n  extensionRealization: fused\nextensions:\n  - name: sidecar\n    origins: [local:./sidecar]\n"
	if err := os.WriteFile(pigletPath, []byte(pigletSource), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "pig-nonfused")
	var stdout, stderr strings.Builder
	code := runBuild([]string{pigletPath, "--format", "binary", "--builder", "native", "--out", output}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), `extension "sidecar" resolves to sidecar: language:python; build.extensionRealization requires fused`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("rejected build output stat error = %v, want not exist", err)
	}
}

// A Piglet that lists an extension that resolves to nothing still fails with
// the unresolved extension and the "resolves to no extensions" blocker, and
// writes no Binary.
func TestRunBuildRejectsListedExtensionThatResolvesToNothing(t *testing.T) {
	dir := t.TempDir()
	pigletPath := filepath.Join(dir, "missing.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: missing\nextensions:\n  - name: gone\n    origins: [local:./gone]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "pig-missing")
	var stdout, stderr strings.Builder
	code := runBuild([]string{pigletPath, "--format", "binary", "--builder", "native", "--out", output}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), `Piglet will not build: unresolved extension: extension "gone": `) || !strings.Contains(stderr.String(), "; piglet resolves to no extensions") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("rejected build output stat error = %v, want not exist", err)
	}
}

// A Piglet whose extension cells all stay outside the Binary (here one
// Python factory) still fails with "nothing to embed or fuse": only a Piglet
// that lists no extensions builds a Binary of PiG's own parts (D18).
func TestRunBuildRejectsPigletWhoseCellsAllStayOutsideTheBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(dir, "home"))
	t.Setenv("HOME", filepath.Join(dir, "home"))
	extensionDir := filepath.Join(dir, "sidecar")
	if err := os.Mkdir(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "sidecar.py"), []byte("def new_extension() -> Extension:\n    return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(dir, "outside.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: outside\nextensions:\n  - name: sidecar\n    origins: [local:./sidecar]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "pig-outside")
	var stdout, stderr strings.Builder
	code := runBuild([]string{pigletPath, "--format", "binary", "--builder", "native", "--out", output}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), `piglet "outside" has nothing to embed or fuse; nothing to build`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("rejected build output stat error = %v, want not exist", err)
	}
}

// The functional floor refuses a Piglet whose effective strip removes every
// built-in tool and /quit before the build plans anything; the build loader
// accepts a Piglet that strips every tool alone or /quit alone.
func TestRunBuildRejectsPigletBelowTheStripFloor(t *testing.T) {
	dir := t.TempDir()
	tools := strings.Join(pigstrip.Known(pigstrip.ListTools), ", ")
	if err := os.WriteFile(filepath.Join(dir, "chat.yaml"), []byte("name: chat\nstrip:\n  tools: ["+tools+"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(dir, "stuck.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: stuck\nextends:\n  source: local:./chat.yaml\nstrip:\n  commands: [/quit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "pig-stuck")
	var stdout, stderr strings.Builder
	code := runBuild([]string{pigletPath, "--format", "binary", "--builder", "native", "--out", output}, &stdout, &stderr)
	want := "a Piglet can't strip every tool and /quit (strip.tools: " + tools + "; strip.commands: /quit); keep at least one tool or /quit"
	if code != 1 || !strings.Contains(stderr.String(), want) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("rejected build output stat error = %v, want not exist", err)
	}
	for _, source := range []string{
		"name: chatonly\nextends:\n  source: local:./chat.yaml\n",
		"name: noquit\nstrip:\n  commands: [/quit]\n",
	} {
		path := filepath.Join(dir, "allowed.yaml")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadPiglet(path); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
}

func TestRenderBuildErrorJSONIncludesReadiness(t *testing.T) {
	readiness := BuilderReadiness{Builder: "docker", Ready: false, Code: "login-required", Remedy: "log in"}
	var stdout, stderr strings.Builder
	code := renderBuildError(true, "release", &BuilderUnavailableError{Readiness: readiness}, 1, &stdout, &stderr)
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output pigletBuildOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Unavailable) != 1 || output.Unavailable[0].Code != "login-required" || output.Unavailable[0].Remedy != "log in" {
		t.Fatalf("output = %#v", output)
	}
}

func TestRenderBuildErrorJSON(t *testing.T) {
	var stdout, stderr strings.Builder
	code := renderBuildError(true, "release", errors.New("failed clearly"), 1, &stdout, &stderr)
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output pigletBuildOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	if output.Success || output.Error != "failed clearly" || output.Piglet != "release" {
		t.Fatalf("output = %#v", output)
	}
}

func TestWriteBuildJSONUsesRecordVocabulary(t *testing.T) {
	var stdout strings.Builder
	writeBuildJSON(pigletBuildOutput{Success: true, Piglet: "release", Record: "/records/binary.json"}, &stdout)
	if !strings.Contains(stdout.String(), `"record":"/records/binary.json"`) || strings.Contains(stdout.String(), `"receipt"`) {
		t.Fatalf("build JSON = %s", stdout.String())
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["version"]; exists {
		t.Fatalf("build JSON exposed a Pig-owned format version: %s", stdout.String())
	}
}

func TestApplyPigletBuildDefaults(t *testing.T) {
	native := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	opts := Options{
		Targets: []Target{native},
		Sandbox: Sandbox{Native: native},
	}
	out := ""
	p := &piglet.Piglet{
		Release: &piglet.ReleaseSpec{Version: "1.2.0"},
		Build: &piglet.BuildSpec{
			Targets:    []string{"linux/amd64"},
			OutputName: "pig-release",
		},
	}
	if err := applyPigletBuildDefaults(p, nil, &opts, &out); err != nil {
		t.Fatal(err)
	}
	if len(opts.Targets) != 1 || opts.Targets[0].String() != "linux/amd64" || out != "pig-release" || opts.Version != "1.2.0" {
		t.Fatalf("opts=%+v out=%q", opts, out)
	}
}

func TestApplyPigletBuildDefaultsPreservesCLIOverrides(t *testing.T) {
	native := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	opts := Options{
		Targets: []Target{native},
		Sandbox: Sandbox{Native: native},
	}
	out := "cli-pig"
	p := &piglet.Piglet{
		Release: &piglet.ReleaseSpec{Version: "1.0.0"},
		Build: &piglet.BuildSpec{
			Targets:    []string{"linux/amd64"},
			OutputName: "piglet-pig",
		},
	}
	args := []string{"release", "--targets=darwin/arm64", "--out", "cli-pig"}
	if err := applyPigletBuildDefaults(p, args, &opts, &out); err != nil {
		t.Fatal(err)
	}
	if len(opts.Targets) != 1 || opts.Targets[0] != native || out != "cli-pig" || opts.Version != "1.0.0" {
		t.Fatalf("opts=%+v out=%q", opts, out)
	}
}

// TestLoadPigletResolvesExtends proves the build path consumes the effective
// composition while preserving only the selected child's release/build inputs.
func TestLoadPigletResolvesExtends(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.yaml")
	child := filepath.Join(dir, "child.yaml")
	if err := os.WriteFile(base, []byte("name: base\ndescription: inherited\nrelease:\n  version: 9.0.0\nbuild:\n  outputName: base-pig\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("name: child\nextends:\n  source: local:./base.yaml\nrelease:\n  version: 1.2.0\nbuild:\n  outputName: child-pig\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := loadPiglet(child)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "child" || p.Description != "inherited" || p.Release == nil || p.Release.Version != "1.2.0" || p.Build == nil || p.Build.OutputName != "child-pig" || p.Extends != nil {
		t.Fatalf("build Piglet = %#v", p)
	}
}
