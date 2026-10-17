package codingagent

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// renderCountTUI counts the synchronous renders a caller asks for.
type renderCountTUI struct {
	tui.TUI
	renders atomic.Int32
}

func (r *renderCountTUI) Render() {
	r.renders.Add(1)
	r.TUI.Render()
}

// chunkedOperations writes each chunk to OnData, pausing up to maxPause between chunks so several chunks share a frame and frames fall between them.
type chunkedOperations struct {
	chunks   []string
	maxPause time.Duration
}

func (o chunkedOperations) Exec(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, chunk := range o.chunks {
		options.OnData([]byte(chunk))
		if o.maxPause > 0 {
			time.Sleep(time.Duration(rng.Int64N(int64(o.maxPause))))
		}
	}
	code := 0
	return extension.BashOperationsResult{ExitCode: &code}, nil
}

// frameProbe records, every time the TUI renders its tree, what the bash block drew in that frame. The loader row (its spinner animates on its own
// clock) is left out.
type frameProbe struct {
	mu     sync.Mutex
	m      *InteractiveMode
	frames [][]string
}

func (p *frameProbe) Render(width int) []string {
	if len(p.m.bashOrder) == 1 {
		frame := blockRowsWithoutLoader(p.m.bashOrder[0], width)
		p.mu.Lock()
		p.frames = append(p.frames, frame)
		p.mu.Unlock()
	}
	return nil
}

func (p *frameProbe) Invalidate() {}

func blockRowsWithoutLoader(block *tui.BashExecutionComponent, width int) []string {
	return slices.DeleteFunc(block.Render(width), func(row string) bool { return strings.Contains(row, "Running...") })
}

// runUserBashFrames runs `!cmd` over chunks through the interactive owner loop, with throttled renders dispatched to that loop as in the real mode, and
// returns the synchronous renders asked for and the block's frames.
func runUserBashFrames(t *testing.T, chunks []string, maxPause time.Duration) (int32, [][]string) {
	t.Helper()
	dir := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: dir, Model: model, AgentDir: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	screen := tui.NewWithOutput(io.Discard, 100, 30)
	counter := &renderCountTUI{TUI: screen}
	m.tuiInst = counter
	probe := &frameProbe{m: m}
	screen.AddChild(m.chatContainer)
	screen.AddChild(probe)
	m.installRenderDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx, m.backgroundCtx = ctx, ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for {
			select {
			case fn := <-m.uiTaskCh:
				fn()
			case <-m.renderWakeCh:
				m.runScheduledRender()
			case <-ctx.Done():
				return
			}
		}
	}()
	ops := chunkedOperations{chunks: chunks, maxPause: maxPause}
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "/ext/frames", Handlers: map[string][]extension.HandlerFn{
		"user_bash": {func(...any) (any, error) { return &extension.UserBashEventResult{Operations: ops}, nil }},
	}}}, dir)
	started := make(chan struct{})
	m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "stream", false); close(started) })
	<-started
	m.backgroundTasks.Wait()
	// The completion rendered synchronously; let a throttled frame still pending run, then stop the loop.
	time.Sleep(40 * time.Millisecond)
	settled := make(chan struct{})
	m.runOnMain(ctx, func() { close(settled) })
	<-settled
	cancel()
	<-loopDone
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return counter.renders.Load(), probe.frames
}

func numberedChunks(n int) []string {
	chunks := make([]string, n)
	for i := range chunks {
		chunks[i] = fmt.Sprintf("output line %d\n", i)
	}
	return chunks
}

// Pi's handleBashCommand appends each chunk to the block and calls ui.requestRender(), which coalesces the chunks of a frame into one render at the
// 16 ms frame throttle. A burst of output therefore costs a few frames, not a synchronous full render per chunk: the only synchronous renders are the
// block's creation and its completion.
func TestUserBashChunksCoalesceIntoFrames(t *testing.T) {
	const chunks = 300
	renders, frames := runUserBashFrames(t, numberedChunks(chunks), 0)
	if renders > 2 {
		t.Fatalf("%d synchronous renders for %d chunks; want 2 (the block's creation and completion)", renders, chunks)
	}
	if len(frames) == 0 {
		t.Fatal("no frame was drawn")
	}
}

// Coalescing changes which frames are drawn, not what a frame shows. Rendering after every chunk drew the block as it stood after chunks 1..k for each
// k; a coalesced frame applies every chunk queued before it, in order, so each frame drawn while the command runs must equal one of those per-chunk
// frames, in chunk order, and the last frame must equal the completed block. The frames come from the TUI's own render passes.
func TestUserBashCoalescedFramesMatchPerChunkFrames(t *testing.T) {
	chunks := numberedChunks(150)
	chunks = append(chunks, "partial line without a newline", " continued\n", strings.Repeat("wide ", 40)+"\n")
	_, frames := runUserBashFrames(t, chunks, 3*time.Millisecond)

	const width = 100
	reference := tui.NewBashExecutionComponent("stream", nil, false, NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), Model: &ai.Model{ID: "m"}}).outputPad)
	perChunk := [][]string{blockRowsWithoutLoader(reference, width)}
	for _, chunk := range chunks {
		reference.AppendOutput(chunk)
		perChunk = append(perChunk, blockRowsWithoutLoader(reference, width))
	}
	code := 0
	reference.SetCompleteWithOutput(&code, false, false, strings.Join(chunks, ""), "")
	final := blockRowsWithoutLoader(reference, width)
	t.Logf("%d frames drawn for %d chunks", len(frames), len(chunks))
	if len(frames) < 2 {
		t.Fatalf("%d frames drawn; want the running frames and the completed one", len(frames))
	}
	if last := frames[len(frames)-1]; !slices.Equal(last, final) {
		t.Fatalf("last frame\n%s\nwant the completed block\n%s", strings.Join(last, "\n"), strings.Join(final, "\n"))
	}
	k := 0
	for i, frame := range frames[:len(frames)-1] {
		if slices.Equal(frame, final) {
			continue // the completion's own render
		}
		for k < len(perChunk) && !slices.Equal(perChunk[k], frame) {
			k++
		}
		if k == len(perChunk) {
			t.Fatalf("frame %d matches no per-chunk frame at or after the previous one:\n%s", i, strings.Join(frame, "\n"))
		}
	}
}
