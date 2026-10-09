package coding

import (
	"path/filepath"
	"slices"
	"testing"
)

// resource-loader.ts reload records the resolved path of the file the system prompt and each appended prompt came from; text that is not an existing file has no source, and a loader that has not reloaded has none.
// packages/coding-agent/src/core/resource-loader.ts:160-162 `getSystemPromptSource()` and `getAppendSystemPromptSources()` report the files the prompts were read from (implemented at :449).
func TestDefaultLoaderReportsSystemPromptSourcePaths(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeResource(t, filepath.Join(f.agentDir, "SYSTEM.md"), "user system")
	writeResource(t, filepath.Join(f.agentDir, "APPEND_SYSTEM.md"), "user append")
	newLoader := func(t *testing.T, options DefaultResourceLoaderOptions) ResourceLoader {
		t.Helper()
		options.CWD, options.AgentDir = f.cwd, f.agentDir
		loader := NewDefaultResourceLoader(options)
		return loader
	}
	pathsOf := func(sources []ResourceSource) []string {
		var out []string
		for _, source := range sources {
			out = append(out, source.Path)
		}
		return out
	}

	t.Run("not reloaded", func(t *testing.T) {
		loader := newLoader(t, DefaultResourceLoaderOptions{})
		if _, ok := loader.GetSystemPromptSource(); ok || len(loader.GetAppendSystemPromptSources()) != 0 {
			t.Fatal("a loader that has not reloaded reports prompt sources")
		}
	})
	t.Run("discovered files", func(t *testing.T) {
		loader := newLoader(t, DefaultResourceLoaderOptions{})
		if err := loader.Reload(); err != nil {
			t.Fatal(err)
		}
		if source, ok := loader.GetSystemPromptSource(); !ok || source.Path != filepath.Join(f.agentDir, "SYSTEM.md") {
			t.Errorf("system prompt source = %+v, %v", source, ok)
		}
		if got := pathsOf(loader.GetAppendSystemPromptSources()); !slices.Equal(got, []string{filepath.Join(f.agentDir, "APPEND_SYSTEM.md")}) {
			t.Errorf("append sources = %v", got)
		}
	})
	t.Run("literal text has no source", func(t *testing.T) {
		loader := newLoader(t, DefaultResourceLoaderOptions{SystemPrompt: new("not a file"), AppendSystemPrompt: []string{"also literal"}})
		if err := loader.Reload(); err != nil {
			t.Fatal(err)
		}
		if text, ok := loader.GetSystemPrompt(); !ok || text != "not a file" {
			t.Fatalf("system prompt = %q, %v", text, ok)
		}
		if source, ok := loader.GetSystemPromptSource(); ok {
			t.Errorf("literal system prompt has source %+v", source)
		}
		if got := loader.GetAppendSystemPromptSources(); len(got) != 0 {
			t.Errorf("literal append prompt has sources %v", got)
		}
	})
	t.Run("explicit file paths", func(t *testing.T) {
		system := filepath.Join(f.cwd, "custom-system.txt")
		first, second := filepath.Join(f.cwd, "a.txt"), filepath.Join(f.cwd, "b.txt")
		writeResource(t, system, "custom system")
		writeResource(t, first, "first")
		writeResource(t, second, "second")
		loader := newLoader(t, DefaultResourceLoaderOptions{SystemPrompt: &system, AppendSystemPrompt: []string{first, "literal", second}})
		if err := loader.Reload(); err != nil {
			t.Fatal(err)
		}
		if source, ok := loader.GetSystemPromptSource(); !ok || source.Path != system {
			t.Errorf("system prompt source = %+v, %v", source, ok)
		}
		if got := pathsOf(loader.GetAppendSystemPromptSources()); !slices.Equal(got, []string{first, second}) {
			t.Errorf("append sources = %v, want [%s %s]", got, first, second)
		}
		if got := loader.GetAppendSystemPrompt(); !slices.Equal(got, []string{"first", "literal", "second"}) {
			t.Errorf("append text = %v", got)
		}
	})
}

// A consumer holds the loader as the ResourceLoader interface, so the source accessors must answer through it.
// upstream: packages/coding-agent/src/core/resource-loader.ts:160 ResourceLoader.getSystemPromptSource and resource-loader.ts:162
// getAppendSystemPromptSources, which DefaultResourceLoader answers at resource-loader.ts:449 and :457.
func TestResourceLoaderInterfaceReportsPromptSources(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeResource(t, filepath.Join(f.agentDir, "SYSTEM.md"), "user system")
	writeResource(t, filepath.Join(f.agentDir, "APPEND_SYSTEM.md"), "user append")
	concrete := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir})
	if err := concrete.Reload(); err != nil {
		t.Fatal(err)
	}
	var loader ResourceLoader = concrete
	source, ok := loader.GetSystemPromptSource()
	if !ok || source.Path != filepath.Join(f.agentDir, "SYSTEM.md") {
		t.Fatalf("system prompt source = %+v, %v", source, ok)
	}
	appended := loader.GetAppendSystemPromptSources()
	if len(appended) != 1 || appended[0].Path != filepath.Join(f.agentDir, "APPEND_SYSTEM.md") {
		t.Fatalf("append sources = %+v", appended)
	}
}
