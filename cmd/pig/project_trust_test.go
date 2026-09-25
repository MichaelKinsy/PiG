package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestResolveProjectTrustedDefaultsToDeniedWithoutUI(t *testing.T) {
	project := trustProjectFixture(t)
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Default: "ask", UI: extension.NoopUIContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Fatal("unresolved non-interactive project was trusted")
	}
}

func TestResolveProjectTrustedHonorsOverrideBeforeProjectResources(t *testing.T) {
	project := trustProjectFixture(t)
	for _, trusted := range []bool{false, true} {
		got, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
			CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Override: new(trusted), Default: "ask",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != trusted {
			t.Fatalf("override %v resolved to %v", trusted, got)
		}
	}
}

func TestResolveProjectTrustedUsesFirstExtensionDecision(t *testing.T) {
	project := trustProjectFixture(t)
	ext := extension.Extension{
		Name: "trust", ResolvedPath: "/trust",
		Handlers: map[string][]extension.HandlerFn{
			"project_trust": {
				func(...any) (any, error) {
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustUndecided}, nil
				},
				func(...any) (any, error) {
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes, Remember: new(true)}, nil
				},
			},
		},
	}
	store := codingagent.NewProjectTrustStore(t.TempDir())
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: store, Default: "never", Runner: inproc.NewRunner([]extension.Extension{ext}, project), UI: extension.NoopUIContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Fatal("decisive extension trust result was ignored")
	}
	stored, err := store.Get(project)
	if err != nil || stored == nil || !*stored {
		t.Fatalf("remembered extension decision = %v, err = %v", stored, err)
	}
}

func TestResolveProjectTrustedInteractiveSelectionPersists(t *testing.T) {
	project := trustProjectFixture(t)
	store := codingagent.NewProjectTrustStore(t.TempDir())
	ui := &projectTrustTestUI{UIContext: extension.NoopUIContext, selected: "Trust"}
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: store, Default: "ask", UI: ui,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Fatal("interactive Trust selection was denied")
	}
	stored, err := store.Get(project)
	if err != nil || stored == nil || !*stored {
		t.Fatalf("stored prompt decision = %v, err = %v", stored, err)
	}
}

type projectTrustTestUI struct {
	extension.UIContext
	selected string
}

func (ui *projectTrustTestUI) Select(context.Context, string, []string, extension.ExtensionUIDialogOptions) (string, error) {
	return ui.selected, nil
}

func trustProjectFixture(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, codingagent.CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	return project
}
