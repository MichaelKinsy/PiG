package sessions_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// toolText is the text of a tool result.
func toolText(t *testing.T, result durable.ToolExecutionResult) string {
	t.Helper()
	var text strings.Builder
	for _, part := range result.Output {
		if content, ok := part.(ai.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	return text.String()
}

func TestRenameSessionToolRenamesThroughTheWorkersRoot(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	id := f.create(t, owner, "model", f.modelArg()).Value("id").(string)

	var client host.Host
	parts := &p2runtime.WorkerParts{Liveness: p2runtime.NewLiveness()}
	parts.Root = func(fn func(host.Ctx) (any, error), ctx context.Context) (any, error) {
		return client.Root(fn, ctx, host.RootOptions{})
	}
	client = f.WorkerHost(id, 1, p2test.WorkerFacet(p2test.HarnessParts(p2test.OpenHarness(t)), parts, nil), sessions.Worker)

	source, err := client.Registry(p2test.Background, p2runtime.Extensions.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var extension *p2runtime.P2Extension
	if err := p2test.Poll(func() (bool, error) {
		table, err := p2runtime.Extensions.Table(source)
		if err != nil {
			return false, nil
		}
		extension, _ = table.Get("p2-session-title")
		return extension != nil, nil
	}, 0, "the rename_session extension"); err != nil {
		t.Fatal(err)
	}
	if len(extension.Tools) != 1 || extension.Tools[0].Name != "rename_session" || extension.Tools[0].Replay != durable.ReplaySafe {
		t.Fatalf("tools = %+v", extension.Tools)
	}
	// Only top-level conversations may rename the session.
	if extension.Tools[0].Scope != "main" {
		t.Fatalf("access = %+v", extension.Tools[0].ToolAccess)
	}
	run := func(title string) string {
		t.Helper()
		result, err := extension.Tools[0].Execute(p2test.Background, map[string]any{"title": title}, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := toolText(t, result)
		if (result.IsError != nil && *result.IsError) != strings.HasPrefix(text, "The title is empty") {
			t.Fatalf("isError = %v for %q", result.IsError, text)
		}
		return text
	}
	if got := run("  Fix   the terminal  on Android "); got != `Renamed the session from "New session" to "Fix the terminal on Android".` {
		t.Fatalf("rename = %q", got)
	}
	if f.catalogRow(t, id).Value("title") != "Fix the terminal on Android" {
		t.Fatal("the catalog keeps the old title")
	}
	if got := run("Fix the terminal on Android"); got != `Session title: "Fix the terminal on Android".` {
		t.Fatalf("same title = %q", got)
	}
	if got := run("   "); got != "The title is empty." {
		t.Fatalf("empty = %q", got)
	}
}
