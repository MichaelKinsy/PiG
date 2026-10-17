package coding

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// types.ts LoadExtensionsResult.runtime ("Shared runtime"): the extensions a load returns share one runtime, and the runner the Session builds over
// them binds it (agent-session.ts _buildRuntime passes extensionsResult.runtime to new ExtensionRunner, as reload() does after loader.reload()).
func TestSessionRunnerBindsTheRuntimeTheLoadedExtensionsShare(t *testing.T) {
	root := t.TempDir()
	var loaded []*extension.ExtensionRuntime
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			runtime := extension.CreateExtensionRuntime()
			loaded = append(loaded, runtime)
			return LoadExtensionsResult{Extensions: []extension.Extension{{Path: "ext"}}, Runtime: runtime}, nil
		},
	})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := loader.GetExtensions().Runtime; got == nil || got != loaded[0] {
		t.Fatalf("GetExtensions().Runtime = %p, want the runtime of the last load %p", got, loaded[0])
	}
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if got := session.ExtensionRunner().Runtime(); got != loaded[0] {
		t.Fatalf("the session's runner runtime = %p, want the loaded runtime %p", got, loaded[0])
	}
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	last := loaded[len(loaded)-1]
	if last == loaded[0] {
		t.Fatal("a reload did not load again")
	}
	if got := session.ExtensionRunner().Runtime(); got != last {
		t.Fatalf("after reload the runner runtime = %p, want the reloaded runtime %p", got, last)
	}
}
