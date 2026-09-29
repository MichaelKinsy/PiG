package harness_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func resourceTestEnv(t *testing.T, files map[string]string) (string, *env.NodeExecutionEnv) {
	t.Helper()
	root := t.TempDir()
	environment := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	for path, content := range files {
		if err := environment.WriteFile(t.Context(), path, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	return root, environment
}

type resourceSource struct{ Type string }

func TestUpstreamHarnessPromptTemplates(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/prompt-templates.test.ts:14
	t.Run("loads markdown templates non-recursively from one or more dirs", func(t *testing.T) {
		_, e := resourceTestEnv(t, map[string]string{"a/one.md": "---\ndescription: One template\n---\nHello $1", "a/nested/ignored.md": "Ignored", "b/two.md": "First line description\nBody"})
		got, diagnostics := harness.LoadPromptTemplates(t.Context(), e, []string{"a", "b"})
		want := []harness.PromptTemplate{{Name: "one", Description: "One template", Content: "Hello $1"}, {Name: "two", Description: "First line description", Content: "First line description\nBody"}}
		if len(diagnostics) != 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("templates=%#v diagnostics=%#v; want %#v", got, diagnostics, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/prompt-templates.test.ts:32
	t.Run("preserves source info for sourced prompt templates", func(t *testing.T) {
		_, e := resourceTestEnv(t, map[string]string{"prompts/example.md": "---\ndescription: Example\n---\nExample body"})
		source := resourceSource{Type: "project"}
		got, diagnostics, err := harness.LoadSourcedPromptTemplates(t.Context(), e, []harness.SourcedPath[resourceSource]{{Path: "prompts", Source: source}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []harness.SourcedPromptTemplate[resourceSource]{{PromptTemplate: harness.PromptTemplate{Name: "example", Description: "Example", Content: "Example body"}, Source: source}}
		if len(diagnostics) != 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("templates=%#v diagnostics=%#v; want %#v", got, diagnostics, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/prompt-templates.test.ts:54
	t.Run("attaches source info to diagnostics", func(t *testing.T) {
		root, e := resourceTestEnv(t, map[string]string{"broken.md": "---\ndescription: [unterminated\n---\nBody"})
		source := resourceSource{Type: "user"}
		got, diagnostics, err := harness.LoadSourcedPromptTemplates(t.Context(), e, []harness.SourcedPath[resourceSource]{{Path: "broken.md", Source: source}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 || len(diagnostics) != 1 {
			t.Fatalf("templates=%#v diagnostics=%#v", got, diagnostics)
		}
		if d := diagnostics[0]; d.Type != "warning" || d.Path != filepath.Join(root, "broken.md") || d.Source != source {
			t.Fatalf("diagnostic=%#v", d)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/prompt-templates.test.ts:75
	t.Run("loads explicit markdown files and symlinked files", func(t *testing.T) {
		root, e := resourceTestEnv(t, map[string]string{"target.md": "---\ndescription: Target\n---\nTarget body"})
		testenv.Symlink(t, filepath.Join(root, "target.md"), filepath.Join(root, "link.md"))
		got, _ := harness.LoadPromptTemplates(t.Context(), e, []string{"target.md", "link.md"})
		want := []harness.PromptTemplate{{Name: "target", Description: "Target", Content: "Target body"}, {Name: "link", Description: "Target", Content: "Target body"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("templates=%#v; want %#v", got, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/prompt-templates.test.ts:91
	t.Run("substitutes command arguments", func(t *testing.T) {
		got := harness.FormatPromptTemplateInvocation(harness.PromptTemplate{Name: "one", Content: "$1 ${@:2} $ARGUMENTS"}, []string{"hello world", "test"})
		if want := "hello world test hello world test"; got != want {
			t.Fatalf("got=%q; want %q", got, want)
		}
	})
}

func TestHarnessPromptMapperRetainsApplicationValueAndContext(t *testing.T) {
	_, e := resourceTestEnv(t, map[string]string{"one.md": "body"})
	source := &resourceSource{Type: "project"}
	ctx := t.Context()
	type mapped struct {
		harness.PromptTemplate
		Extra string
	}
	got, diagnostics, err := harness.LoadSourcedPromptTemplates(ctx, e, []harness.SourcedPath[*resourceSource]{{Path: "one.md", Source: source}}, func(template harness.PromptTemplate, s *resourceSource, mapperCtx context.Context) (any, error) {
		if s != source || mapperCtx != ctx {
			t.Fatal("mapper lost source identity or caller context")
		}
		return mapped{PromptTemplate: template, Extra: "mapped"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 0 || len(got) != 1 || got[0].Source != source {
		t.Fatalf("result=%#v warnings=%#v", got, diagnostics)
	}
	if value, ok := got[0].PromptTemplate.(mapped); !ok || value.Extra != "mapped" {
		t.Fatalf("mapped value=%#v", got[0].PromptTemplate)
	}
}

func TestHarnessPromptSemanticsStayIndependentOfCodingAgent(t *testing.T) {
	if got := harness.ParseCommandArgs("first\nsecond third"); !reflect.DeepEqual(got, []string{"first\nsecond", "third"}) {
		t.Fatalf("args=%q", got)
	}
	if got := harness.SubstituteArgs("$1", []string{"$@", "second"}); got != "$@ second" {
		t.Fatalf("multi-pass substitution=%q", got)
	}
	if got := harness.SubstituteArgs("x$ARGUMENTSy", []string{"$&"}); got != "x$ARGUMENTSy" {
		t.Fatalf("replacement tokens=%q", got)
	}
}
