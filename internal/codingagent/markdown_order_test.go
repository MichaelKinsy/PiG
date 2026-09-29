package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's synchronous chain (markdown-transform.ts:18-29) finishes a callback body before any later render starts another chain. Cancelling host correlation does not finish a remote body.
func TestMarkdownModeReplacementWaitsForCallbackBody(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var trace []string
		record := func(value string) { mu.Lock(); trace = append(trace, value); mu.Unlock() }
		started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		secondStarted, secondRelease := make(chan struct{}), make(chan struct{})
		fixture := sdk.New("mode-order")
		fixture.MarkdownTransformer(func(text string, _ sdk.MarkdownTransformContext) string {
			record("A " + text + " entered")
			if text == "first" {
				close(started)
				<-release
				record("A first returned")
				close(returned)
			} else {
				close(secondStarted)
				<-secondRelease
			}
			return text
		})
		host := subprocess.NewHost(t.TempDir())
		defer host.Shutdown("test done")
		remote, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "mode-order", Enabled: true}, fixture.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m := &InteractiveMode{backgroundCtx: ctx, tuiInst: tui.NewWithOutput(io.Discard, 80, 24), uiTaskCh: make(chan func(), 8)}
		m.newRunner = inproc.NewRunner([]extension.Extension{
			{MarkdownTransformer: func(text string, _ extension.MarkdownTransformContext) string { record("B " + text); return text }},
			*remote,
			{MarkdownTransformer: func(text string, _ extension.MarkdownTransformContext) string { record("C " + text); return text }},
		}, "")
		block := m.newAssistantMessageBlock()
		block.SetContent([]tui.AssistantSegment{{Text: "first"}})
		block.Render(80)
		<-started
		block.SetContent([]tui.AssistantSegment{{Text: "second"}})
		block.Render(80)
		synctest.Wait()
		// Release is independent of later callback/request progress.
		close(release)
		<-returned
		<-secondStarted
		interim := strings.Join(block.Render(80), "\n")
		close(secondRelease)
		m.backgroundTasks.Wait()
		if strings.Contains(interim, "first") || strings.Contains(interim, "second") {
			t.Errorf("unfinished replacement published content: %q", interim)
		}
		if final := strings.Join(block.Render(80), "\n"); !strings.Contains(final, "second") || strings.Contains(final, "first") {
			t.Errorf("replacement result = %q; want only second", final)
		}
		mu.Lock()
		got := slices.Clone(trace)
		mu.Unlock()
		want := []string{"B first", "A first entered", "A first returned", "C first", "B second", "A second entered", "C second"}
		logMarkdownOrder(t, "replacement", got)
		if !slices.Equal(got, want) {
			t.Fatalf("callback body order = %q; want %q", got, want)
		}
	})
}

// The render traversal in Pi admits A1,B1,A2 in that order; serializing a whole per-component drain would incorrectly produce A1,A2,B1.
func TestMarkdownModeCrossComponentAdmissionOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var mu sync.Mutex
		var trace []string
		started, release := make(chan struct{}), make(chan struct{})
		m := &InteractiveMode{backgroundCtx: ctx, tuiInst: tui.NewWithOutput(io.Discard, 80, 24), uiTaskCh: make(chan func(), 8)}
		m.newRunner = inproc.NewRunner([]extension.Extension{{MarkdownTransformer: func(text string, _ extension.MarkdownTransformContext) string {
			mu.Lock()
			trace = append(trace, text)
			mu.Unlock()
			if text == "A1" {
				close(started)
				<-release
			}
			return "transformed " + text
		}}}, "")
		a, b := m.newAssistantMessageBlock(), m.newUserMessageBlock("B1")
		a.SetContent([]tui.AssistantSegment{{Text: "A1"}})
		a.Render(80)
		<-started
		b.Render(80)
		a.SetContent([]tui.AssistantSegment{{Text: "A2"}})
		a.Render(80)
		synctest.Wait()
		mu.Lock()
		beforeRelease := slices.Clone(trace)
		mu.Unlock()
		close(release)
		m.backgroundTasks.Wait()
		if !slices.Equal(beforeRelease, []string{"A1"}) {
			t.Errorf("later render overlapped A1: %q", beforeRelease)
		}
		mu.Lock()
		got := strings.Join(trace, ",")
		mu.Unlock()
		logMarkdownOrder(t, "components", strings.Split(got, ","))
		if got != "A1,B1,A2" {
			t.Fatalf("admission order = %s; want A1,B1,A2", got)
		}
	})
}

func logMarkdownOrder(t *testing.T, name string, trace []string) {
	t.Helper()
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MARKDOWN_ORDER %s:%s", name, data)
}
