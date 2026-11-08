package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/markdown-transform.ts

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// surfaceTestMode is a mode whose one extension transformer, on every surface, runs wait and then redacts "private".
func surfaceTestMode(t *testing.T, ctx context.Context, wait func(extension.MarkdownTransformContext)) *InteractiveMode {
	t.Helper()
	m := &InteractiveMode{backgroundCtx: ctx, tuiInst: tui.NewWithOutput(io.Discard, 80, 24), chatContainer: tui.NewContainer(), outputPad: 1, toolsExpanded: true}
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		MarkdownMessageTypes: []extension.MarkdownMessageType{extension.MarkdownMessageCustom, extension.MarkdownMessageSummary, extension.MarkdownMessageChangelog},
		MarkdownTransformer: func(text string, context extension.MarkdownTransformContext) string {
			wait(context)
			return strings.ReplaceAll(text, "private", "redacted")
		},
	}}, "")
	return m
}

// The custom, summary and changelog surfaces run a subprocess transformer off the render loop, as the user and assistant messages do (D19): a slow
// transformer never blocks a frame, and the raw text is not painted before the chain completes.
func TestSurfaceMarkdownTransformsNeverBlockTheRenderLoop(t *testing.T) {
	mounts := map[string]func(m *InteractiveMode){
		"custom": func(m *InteractiveMode) {
			m.appendCustomMessage(CustomMessageEntry{CustomType: "note", Content: "private", Display: true}, messageMeta{})
		},
		"summary":        func(m *InteractiveMode) { m.addCompactionSummary("private", 10, nil, messageMeta{}) },
		"branch summary": func(m *InteractiveMode) { m.addBranchSummary("private", nil, messageMeta{}) },
		"changelog": func(m *InteractiveMode) {
			m.chatContainer.Add(m.newChangelogMarkdown("private", 1, 0, nil))
		},
	}
	for name, mount := range mounts {
		t.Run(name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var once = make(chan struct{}, 1)
			m := surfaceTestMode(t, ctx, func(extension.MarkdownTransformContext) {
				select {
				case once <- struct{}{}:
					close(started)
				default:
				}
				<-release
			})
			mount(m)
			rendered := make(chan []string, 1)
			go func() { rendered <- m.chatContainer.Render(80) }()
			select {
			case first := <-rendered:
				if strings.Contains(strings.Join(first, "\n"), "private") {
					t.Error("untransformed Markdown reached the first paint")
				}
			case <-time.After(time.Second):
				t.Fatal("a slow transformer blocked the render loop")
			}
			<-started
			close(release)
			m.backgroundTasks.Wait()
			if got := strings.Join(m.chatContainer.Render(80), "\n"); !strings.Contains(got, "redacted") || strings.Contains(got, "private") {
				t.Fatalf("completed chain not painted: %q", got)
			}
		})
	}
}

// The shutdown context reaches a surface's transformer: cancelling it ends a transformer that waits on its context.
func TestSurfaceMarkdownTransformsAreCancelledAtShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	m := surfaceTestMode(t, ctx, func(context extension.MarkdownTransformContext) {
		close(started)
		<-context.Context.Done()
	})
	m.appendCustomMessage(CustomMessageEntry{CustomType: "note", Content: "private", Display: true}, messageMeta{})
	m.chatContainer.Render(80)
	<-started
	cancel()
	done := make(chan struct{})
	go func() { m.backgroundTasks.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not reach the surface's transformer")
	}
}

// Pi re-renders a Markdown only when its text or width changes: a surface is static, with no state to poll, unless an extension registered a
// transformer. Its state reads allocate nothing per frame.
func TestSurfaceMarkdownTransformIsStaticWithoutAnExtensionTransformer(t *testing.T) {
	plain := &InteractiveMode{backgroundCtx: t.Context(), chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	surface := plain.surfaceMarkdownTransform(extension.MarkdownMessageCustom, messageMeta{})
	if surface.Transform == nil || surface.State != nil || surface.Async != nil {
		t.Fatalf("without a transformer: %+v", surface)
	}
	if block := plain.newChangelogMarkdown("notes", 1, 0, nil); block.SurfaceLive() || block.IsDirty() {
		t.Fatal("a changelog Markdown without an extension transformer is polled")
	}

	m := surfaceTestMode(t, t.Context(), func(extension.MarkdownTransformContext) {})
	surface = m.surfaceMarkdownTransform(extension.MarkdownMessageCustom, messageMeta{})
	if surface.State == nil || surface.Async == nil {
		t.Fatalf("with a transformer: %+v", surface)
	}
	first := surface.State()
	if allocs := testing.AllocsPerRun(100, func() { _ = surface.State() }); allocs != 0 {
		t.Errorf("state read allocates %v per frame", allocs)
	}
	m.opts.SettingsManager = NewSettingsManager(t.TempDir(), t.TempDir())
	if err := m.opts.SettingsManager.SetMermaidRenderingMode("off"); err != nil {
		t.Fatal(err)
	}
	if changed := surface.State(); changed == first {
		t.Errorf("state %q did not follow the mermaid mode", changed)
	}
}
