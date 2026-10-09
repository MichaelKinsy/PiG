package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateRequiresARuleAndAReleaseNote(t *testing.T) {
	lines := []string{
		"- Context.GetSessionID: changed from func() string to func() (string, error)",
		"- Context.SetEditorComponent: changed from func(any) error to func(EditorFactory) error",
		"- (*Extension).Tool: changed from func(string) to func(string, int)",
		"- Brand.New: removed",
		"- OAuthCredentials: old is comparable, new is not",
	}
	covered := func(symbol string) bool { return strings.HasPrefix(symbol, "Context.") || symbol == "OAuthCredentials" }
	notes := "Changed `Context.GetSessionID` and `(*Extension).Tool(` and `OAuthCredentials`.\n"
	got := evaluate(lines, covered, notes)
	want := []string{
		"(*Extension).Tool: no upgrade rule (changed from func(string) to func(string, int))",
		"Brand.New: no upgrade rule and release note (removed)",
		"Context.SetEditorComponent: no release note (changed from func(any) error to func(EditorFactory) error)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := evaluate([]string{"garbage"}, covered, notes); len(got) != 1 || !strings.Contains(got[0], "unreadable") {
		t.Fatalf("an unreadable line is not reported: %v", got)
	}
	if got := evaluate(nil, covered, ""); len(got) != 0 {
		t.Fatalf("no changes reported problems: %v", got)
	}
}

func TestNotedMatchesAMethodWithItsReceiverPrefix(t *testing.T) {
	if !noted("see `Extension.Tool`", "(*Extension).Tool") || noted("see Extension.Tool without quotes", "(*Extension).Tool") || noted("`Context.GetFlagX`", "Context.GetFlag") {
		t.Fatal("release-note matching is wrong")
	}
}

// The gate reads real apidiff output: two copies of a package that differ by one signature produce one incompatible symbol, and a compatible addition produces none.
func TestIncompatibleChangesRunsApidiffOnTwoModules(t *testing.T) {
	tool, err := apidiffTool(repoRoot(t))
	if err != nil {
		t.Fatalf("apidiff tool: %v", err)
	}
	write := func(source string) string {
		dir := t.TempDir()
		for name, content := range map[string]string{
			"go.mod": "module " + sdkModule + "\n\ngo 1.26\n",
			"sdk.go": source,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	oldDir := write("package sdk\n\ntype Context struct{}\n\nfunc (Context) IsIdle() bool { return true }\n")
	newDir := write("package sdk\n\ntype Context struct{}\n\nfunc (Context) IsIdle() (bool, error) { return true, nil }\n\nfunc (Context) Added() {}\n")
	got, err := incompatibleChanges(tool, oldDir, newDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "- Context.IsIdle: changed from func() bool to func() (bool, error)") {
		t.Fatalf("incompatible = %q", got)
	}
	if got, err := incompatibleChanges(tool, newDir, newDir); err != nil || len(got) != 0 {
		t.Fatalf("identical packages: %q, %v", got, err)
	}
}

// A break in a subpackage of the SDK module fails the gate as one in the root package does; the upgrade package's own API does not.
func TestIncompatibleChangesCoversSubpackages(t *testing.T) {
	tool, err := apidiffTool(repoRoot(t))
	if err != nil {
		t.Fatalf("apidiff tool: %v", err)
	}
	write := func(json, upgrade string) string {
		dir := t.TempDir()
		for name, content := range map[string]string{
			"go.mod":             "module " + sdkModule + "\n\ngo 1.26\n",
			"sdk.go":             "package sdk\n\ntype Context struct{}\n",
			"json/json.go":       "package json\n\n" + json,
			"upgrade/upgrade.go": "package upgrade\n\n" + upgrade,
		} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	oldDir := write("func MarshalIndent(v any, prefix, indent string) ([]byte, error) { return nil, nil }\n", "func Symbols() []string { return nil }\n")
	newDir := write("func MarshalIndent(v any, prefix string) ([]byte, error) { return nil, nil }\n", "")
	got, err := incompatibleChanges(tool, oldDir, newDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "- ./json.MarshalIndent: changed from") {
		t.Fatalf("incompatible = %q, want only the json break", got)
	}
	if problems := evaluate(got, func(string) bool { return true }, "`json.MarshalIndent` lost its indent.\n"); len(problems) != 0 {
		t.Fatalf("a subpackage symbol noted without ./ is reported: %v", problems)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
