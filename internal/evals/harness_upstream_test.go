package evals

// Ports packages/evals/test/harness.test.ts.

import (
	"os"
	"reflect"
	"strings"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// unsetEnv removes name for the rest of the test and restores it afterwards.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

// documentationEvalPrompt is buildSystemPrompt({ cwd: "/workspace", selectedTools: [...DOCUMENTATION_EVAL_TOOLS] })
// with PiG's documentation bundle at docsRoot.
func documentationEvalPrompt(docsRoot string) string {
	return prompts.BuildDefaultPrompt(prompts.Options{Cwd: "/workspace", Tools: DocumentationEvalTools[:], PigDocsPath: docsRoot})
}

// TestHarnessUpstream ports packages/evals/test/harness.test.ts. Each subtest names one upstream case.
func TestHarnessUpstream(t *testing.T) {
	t.Run("resolveModelSelection › prefers an explicit harness model", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:16
		selection, err := ResolveModelSelection(
			&PiCodingAgentModelSelection{Provider: "anthropic", ID: "claude-opus-4-6"},
			map[string]string{"PI_PROVIDER": "openai-codex", "PI_MODEL": "gpt-5.6-sol"},
		)
		if err != nil || selection != (PiCodingAgentModelSelection{Provider: "anthropic", ID: "claude-opus-4-6"}) {
			t.Fatalf("selection = %+v, %v", selection, err)
		}
	})

	t.Run("resolveModelSelection › uses trimmed environment defaults", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:25
		selection, err := ResolveModelSelection(nil, map[string]string{"PI_PROVIDER": " openai-codex ", "PI_MODEL": " gpt-5.6-sol "})
		if err != nil || selection != (PiCodingAgentModelSelection{Provider: "openai-codex", ID: "gpt-5.6-sol"}) {
			t.Fatalf("selection = %+v, %v", selection, err)
		}
	})

	t.Run("resolveModelSelection › rejects incomplete model selection", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:32
		for _, environment := range []map[string]string{{}, {"PI_PROVIDER": "openai-codex"}, {"PI_MODEL": "gpt-5.6-sol"}} {
			_, err := ResolveModelSelection(nil, environment)
			requireErrorContaining(t, err, "Select a harness model explicitly")
		}
	})

	t.Run("isolateProcessEnvironment › removes runner metadata and restores the process environment", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:41
		t.Setenv("PI_EVAL_VARIANT", "with_docs")
		t.Setenv("PI_EVAL_ARTIFACT_DIR", "/tmp/artifacts")
		oldHome, oldHomeSet := os.LookupEnv("HOME")
		restore := ApplyIsolatedEnvironment("/tmp/eval-home", "/tmp/eval-agent")
		func() {
			defer restore()
			if home, err := os.UserHomeDir(); err != nil || home != "/tmp/eval-home" {
				t.Errorf("home = %q, %v", home, err)
			}
			if dir := icodingagent.AgentDir(); dir != "/tmp/eval-agent" {
				t.Errorf("agent dir = %q", dir)
			}
			for _, name := range []string{"PI_EVAL_VARIANT", "PI_EVAL_ARTIFACT_DIR"} {
				if value, ok := os.LookupEnv(name); ok {
					t.Errorf("%s = %q, want unset", name, value)
				}
			}
		}()
		if home, ok := os.LookupEnv("HOME"); home != oldHome || ok != oldHomeSet {
			t.Errorf("HOME = %q (%v), want %q (%v)", home, ok, oldHome, oldHomeSet)
		}
		if value := os.Getenv("PI_EVAL_VARIANT"); value != "with_docs" {
			t.Errorf("PI_EVAL_VARIANT = %q, want with_docs", value)
		}
	})

	for _, variant := range []DocumentationVariant{"without_docs", "with_docs"} {
		t.Run("documentation variant › accepts "+string(variant), func(t *testing.T) {
			// upstream: packages/evals/test/harness.test.ts:64
			got, err := ResolveDocumentationVariant(string(variant))
			if err != nil || got != variant {
				t.Fatalf("variant = %q, %v; want %q", got, err, variant)
			}
		})
	}

	for _, variant := range []*string{nil, new(""), new("other")} {
		label := "undefined"
		if variant != nil {
			label = *variant
		}
		t.Run("documentation variant › rejects invalid variant "+label, func(t *testing.T) {
			// upstream: packages/evals/test/harness.test.ts:68
			var err error
			if variant == nil {
				unsetEnv(t, "PI_EVAL_VARIANT")
				_, err = ResolveDocumentationVariant()
			} else {
				_, err = ResolveDocumentationVariant(*variant)
			}
			requireErrorContaining(t, err, "PI_EVAL_VARIANT")
		})
	}

	t.Run("documentation variant › strips only the documentation routing section from the default Pi prompt", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:72
		const docsRoot = "/pig-docs-root"
		prompt := documentationEvalPrompt(docsRoot)
		for _, want := range []string{documentationSectionStart, "\n<rules>\n", "\n<cwd>\n/workspace\n</cwd>", "docs/models.md"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("default prompt lacks %q:\n%s", want, prompt)
			}
		}

		stripped, err := ExcludePiDocumentation(prompt)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"\n<rules>\n", "\n<cwd>\n/workspace\n</cwd>"} {
			if !strings.Contains(stripped, want) {
				t.Fatalf("stripped prompt lacks %q:\n%s", want, stripped)
			}
		}
		// pig additive (D22): the section introduces PiG documentation at the materialized bundle and PiG's examples.
		for _, absent := range []string{"<docs>", "PiG documentation", "docs/models.md", docsRoot + "/README.md", docsRoot, "https://github.com/MichaelKinsy/PiG/tree/main/examples"} {
			if strings.Contains(stripped, absent) {
				t.Fatalf("stripped prompt still contains %q:\n%s", absent, stripped)
			}
		}
		if !strings.HasSuffix(stripped, "\n<cwd>\n/workspace\n</cwd>") || !strings.HasPrefix(prompt, strings.SplitN(stripped, "\n<cwd>", 2)[0]) {
			t.Fatalf("stripping changed more than the docs section:\n%s", stripped)
		}
	})

	t.Run("documentation variant › verifies the prompt that was sent", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:93
		prompt := documentationEvalPrompt("/pig-docs-root")
		stripped, err := ExcludePiDocumentation(prompt)
		if err != nil {
			t.Fatal(err)
		}
		withoutDocs := PiCodingAgentHarnessOptions{Name: "without_docs", ExpectedPiDocumentation: new(false)}
		if verified, err := VerifySystemPrompt(stripped, withoutDocs); err != nil || verified != stripped {
			t.Fatalf("verify stripped = %v", err)
		}
		_, err = VerifySystemPrompt(prompt, withoutDocs)
		requireErrorContaining(t, err, "does not match")
	})

	t.Run("documentation variant › fails closed when prompt markers are missing", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:106
		_, err := ExcludePiDocumentation("Instructions")
		requireErrorContaining(t, err, "no Pi documentation section")
		_, err = ExcludePiDocumentation("\n<docs>\nPi documentation\n</docs>")
		requireErrorContaining(t, err, "no working-directory section")
	})

	t.Run("documentation variant › rejects documentation harnesses outside the container sandbox", func(t *testing.T) {
		// upstream: packages/evals/test/harness.test.ts:113
		for _, name := range []string{"PI_EVAL_CONTAINER", "PI_EVAL_SANDBOX_UID", "PI_EVAL_SANDBOX_GID"} {
			unsetEnv(t, name)
		}
		_, err := CreatePiDocumentationEvalHarness()
		requireErrorContaining(t, err, "isolated container sandbox")
		// The container flag alone is not the sandbox: the harness also requires a sandbox identity.
		t.Setenv("PI_EVAL_CONTAINER", "1")
		_, err = CreatePiDocumentationEvalHarness()
		requireErrorContaining(t, err, "isolated container sandbox")
	})
}

// TestCreatePiDocumentationEvalHarnessInSandbox checks the options the documentation harness passes to
// createPiCodingAgentHarness once the sandbox guard admits it (harness.ts:517-534).
func TestCreatePiDocumentationEvalHarnessInSandbox(t *testing.T) {
	t.Setenv("PI_EVAL_CONTAINER", "1")
	t.Setenv("PI_EVAL_SANDBOX_UID", "1000")
	t.Setenv("PI_EVAL_SANDBOX_GID", "1000")

	t.Setenv("PI_EVAL_VARIANT", "without_docs")
	options, err := CreatePiDocumentationEvalHarness()
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "without_docs" || !reflect.DeepEqual(options.Tools, DocumentationEvalTools[:]) || options.ExpectedPiDocumentation == nil || *options.ExpectedPiDocumentation || options.TransformSystemPrompt == nil {
		t.Fatalf("without_docs options = %+v", options)
	}
	prompt := documentationEvalPrompt("/pig-docs-root")
	transformed, err := options.TransformSystemPrompt(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySystemPrompt(transformed, options); err != nil {
		t.Fatal(err)
	}
	options.Tools[0] = "bash"
	if DocumentationEvalTools[0] != "read" {
		t.Fatal("the harness tools alias DocumentationEvalTools")
	}

	t.Setenv("PI_EVAL_VARIANT", "with_docs")
	options, err = CreatePiDocumentationEvalHarness(PiCodingAgentHarnessOptions{Tools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "with_docs" || !reflect.DeepEqual(options.Tools, []string{"read"}) || options.ExpectedPiDocumentation == nil || !*options.ExpectedPiDocumentation || options.TransformSystemPrompt != nil {
		t.Fatalf("with_docs options = %+v", options)
	}
	if _, err := VerifySystemPrompt(prompt, options); err != nil {
		t.Fatal(err)
	}

	for _, invalid := range []string{"0", "1.5", "x", ""} {
		t.Setenv("PI_EVAL_SANDBOX_UID", invalid)
		_, err = CreatePiDocumentationEvalHarness()
		requireErrorContaining(t, err, "PI_EVAL_SANDBOX_UID must be a positive integer.")
	}
	unsetEnv(t, "PI_EVAL_SANDBOX_UID")
	_, err = CreatePiDocumentationEvalHarness()
	requireErrorContaining(t, err, "Set both PI_EVAL_SANDBOX_UID and PI_EVAL_SANDBOX_GID, or neither.")
}
