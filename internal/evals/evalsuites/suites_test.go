package evalsuites

// The suite inventory must match Pi's packages/evals/evals directory: the same suites, files (with the Go suffix),
// projects and case names, because the comparison runner identifies a case by "<suite> > <case>".

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

func TestSuitesMatchPisEvalInventory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "pigdocs", "content", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"models.md", "sub/b.md", "a.md", "B.md", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(root, "internal", "pigdocs", "content", page), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PI_EVAL_REPOSITORY_ROOT", root)
	runner := evals.Runner{Suites: All(), PackageRoot: "/repo/packages/evals"}

	var docs []string
	for _, test := range runner.List(evals.Selection{Project: evals.EvalProjectDocs}) {
		docs = append(docs, test.Name+" @ "+filepath.Base(test.File))
	}
	wantDocs := []string{
		"Add custom streaming provider > implements a provider from an API fixture @ custom-provider.docs.eval.go",
		"Create and use a tool extension > creates, reloads, and invokes the extension @ extensions.docs.eval.go",
		"Add model to existing provider > adds the model without replacing existing models @ models.docs.eval.go",
		"Add OpenAI-compatible provider > configures a provider that works through Pi @ openai-provider.docs.eval.go",
		"Customize the interactive context footer > replaces numeric context usage with a progress bar @ tui.docs.eval.go",
	}
	if len(docs) != len(wantDocs) {
		t.Fatalf("docs cases = %v", docs)
	}
	for i := range wantDocs {
		if docs[i] != wantDocs[i] {
			t.Errorf("docs case %d = %q, want %q", i, docs[i], wantDocs[i])
		}
	}

	var host []string
	for _, test := range runner.List(evals.Selection{Project: evals.EvalProjectHost}) {
		host = append(host, test.Name)
	}
	wantHost := []string{
		"Audit documentation against implementation > a.md matches the implementation",
		"Audit documentation against implementation > B.md matches the implementation",
		"Audit documentation against implementation > models.md matches the implementation",
		"Audit documentation against implementation > sub/b.md matches the implementation",
		"Answer a basic prompt > returns the expected answer",
	}
	if len(host) != len(wantHost) {
		t.Fatalf("host cases = %v", host)
	}
	for i := range wantHost {
		if host[i] != wantHost[i] {
			t.Errorf("host case %d = %q, want %q", i, host[i], wantHost[i])
		}
	}
}

func TestDocumentationSuitesRefuseToRunOutsideTheSandbox(t *testing.T) {
	t.Setenv("PI_EVAL_CONTAINER", "")
	for _, suite := range All() {
		if suite.Project() != evals.EvalProjectDocs {
			continue
		}
		_, err := suite.Harness(context.Background(), evals.PromptInput("x"))
		if err == nil || err.Error() != "Documentation evals must run in the isolated container sandbox." {
			t.Errorf("%s: error = %v", suite.Name, err)
		}
	}
}

func TestAuditToolEndsTheRunAndEchoesItsArguments(t *testing.T) {
	tool := submitAudit()
	result, err := tool.Execute(context.Background(), "call", map[string]any{"verdict": "match", "explanation": "ok"})
	if err != nil || result.Content != "Documentation audit submitted." || !result.Terminate || result.Details.(map[string]any)["verdict"] != "match" {
		t.Errorf("result = %+v, %v", result, err)
	}
	// documentation-audit.eval.ts:11-15 defines the label and the prompt snippet the model sees.
	if tool.Label != "Submit documentation audit" || tool.PromptSnippet != "Submit the final documentation audit as validated structured data" {
		t.Errorf("label = %q, prompt snippet = %q", tool.Label, tool.PromptSnippet)
	}
	if tool.ConstrainedSampling == nil || tool.ConstrainedSampling.Type != "json_schema" || tool.ConstrainedSampling.Strict != "prefer" {
		t.Errorf("constrained sampling = %+v", tool.ConstrainedSampling)
	}
}
