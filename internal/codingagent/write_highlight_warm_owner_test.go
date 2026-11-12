package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// goroutineID is the id in the header line of the calling goroutine's stack.
func goroutineID() string {
	buf := make([]byte, 64)
	return string(bytes.Fields(buf[:runtime.Stack(buf, false)])[1])
}

// warmProbe stands in for the highlight warm-up of a write call. It records the length of each text warmed, which goroutine
// warmed it and how many warm-ups overlapped. A warm-up that runs off the goroutine that drew the card also waits for release,
// so a test holds the worker where it wants it; one that runs on the drawing goroutine never waits, so a card that still warms
// inside its frame fails the assertions instead of hanging.
type warmProbe struct {
	mu        sync.Mutex
	caller    string
	lengths   []int
	texts     []string
	onCaller  []bool
	active    int
	maxActive int
	entered   chan struct{}
	release   chan struct{}
	resumable bool
	panicWith any
}

func newWarmProbe(t *testing.T) *warmProbe {
	t.Helper()
	probe := &warmProbe{caller: goroutineID(), entered: make(chan struct{}, 64), release: make(chan struct{}), resumable: true}
	previous := writeWarmHighlight
	writeWarmHighlight = probe.warm
	t.Cleanup(func() { writeWarmHighlight = previous })
	return probe
}

func (probe *warmProbe) warm(code, _ string) bool {
	onCaller := goroutineID() == probe.caller
	probe.mu.Lock()
	probe.active++
	probe.maxActive = max(probe.maxActive, probe.active)
	probe.lengths = append(probe.lengths, len(code))
	probe.texts = append(probe.texts, code)
	probe.onCaller = append(probe.onCaller, onCaller)
	panicWith, resumable := probe.panicWith, probe.resumable
	probe.mu.Unlock()
	probe.entered <- struct{}{}
	if !onCaller {
		<-probe.release
	}
	probe.mu.Lock()
	probe.active--
	probe.mu.Unlock()
	if panicWith != nil {
		panic(panicWith)
	}
	return resumable
}

// writeStream draws one write call of a TypeScript file frame by frame.
type writeStream struct {
	t       *testing.T
	context extension.ToolRenderContext
	state   *builtInRenderState
}

func newWriteStream(t *testing.T, lifetime context.Context) *writeStream {
	t.Helper()
	stream := &writeStream{t: t, context: extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work"}}
	stream.state = builtInState(stream.context)
	stream.state.lifetime = lifetime
	return stream
}

func (stream *writeStream) draw(content string, complete bool) []string {
	stream.t.Helper()
	quoted, err := json.Marshal(content)
	if err != nil {
		stream.t.Fatal(err)
	}
	stream.context.ArgsComplete = complete
	stream.context.LastComponent = writeRenderCall(json.RawMessage(`{"path":"big/source.ts","content":`+string(quoted)+`}`), nil, stream.context)
	return stream.context.LastComponent.Render(160)
}

// A warm-up is the work of a goroutine the card owns, not of the frame that falls on the step: highlighting a step took 20-30 ms
// inside ToolExecutionComponent.Render on the owner loop.
func TestWriteWarmUpRunsOffTheRenderingGoroutine(t *testing.T) {
	probe := newWarmProbe(t)
	close(probe.release)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	for end := 1000; end < len(content); end += 1000 {
		stream.draw(content[:end], false)
	}
	stream.state.tasks.Wait()
	if len(probe.lengths) == 0 {
		t.Fatal("a streaming write call warmed nothing")
	}
	for i, onCaller := range probe.onCaller {
		if onCaller {
			t.Errorf("warm-up %d (%d bytes) ran on the goroutine that draws the card", i, probe.lengths[i])
		}
	}
}

// A warm-up costs about what its step does, however long the file is, so a card warms in steps of writeWarmStep bytes from the first to
// the last: a step of a sixteenth of the file, 32 KB at the most, held the highlighter for 20-30 ms inside the stretch of frames it fell in.
func TestWriteWarmUpsFollowTheContentInSmallSteps(t *testing.T) {
	probe := newWarmProbe(t)
	close(probe.release)
	content := streamedWriteTypeScript(300)
	stream := newWriteStream(t, context.Background())
	const frame = 1000
	for end := frame; end < len(content); end += frame {
		stream.draw(content[:end], false)
		stream.state.tasks.Wait()
	}
	if len(probe.lengths) < (len(content)-frame)/(writeWarmStep+frame) {
		t.Fatalf("%d warm-ups over %d bytes: steps larger than %d bytes", len(probe.lengths), len(content), writeWarmStep+frame)
	}
	previous := 0
	for i, length := range probe.lengths {
		if length-previous > writeWarmStep+frame {
			t.Errorf("warm-up %d gained %d bytes, more than a step of %d and a frame", i, length-previous, writeWarmStep)
		}
		previous = length
	}
}

// One warm-up runs at a time and holds only the newest content: steps that arrive while one runs are folded into one, so a slow
// worker costs one pass over the text it has fallen behind, not a pass for each step. The texts warmed grow, each the start of
// the next, so the highlighter's resume state always moves forward.
func TestWriteWarmUpsRunOneAtATimeAndFoldTheStepsThatArriveMeanwhile(t *testing.T) {
	probe := newWarmProbe(t)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	stream.draw(content[:9000], false)
	<-probe.entered // the first warm-up is running and held
	stream.draw(content[:18000], false)
	stream.draw(content[:27000], false)
	stream.draw(content[:36000], false)
	close(probe.release)
	stream.state.tasks.Wait()
	if !slices.Equal(probe.lengths, []int{9000, 36000}) {
		t.Fatalf("warmed texts of %v bytes, want the first step and then the newest content only", probe.lengths)
	}
	if !strings.HasPrefix(probe.texts[1], probe.texts[0]) {
		t.Error("the second warm-up is not an extension of the first")
	}
	if probe.maxActive != 1 {
		t.Errorf("%d warm-ups ran at once", probe.maxActive)
	}
	if stream.state.writeWarmRunning {
		t.Error("the worker is still marked running after it finished")
	}
}

// A complete call draws its file once from the highlighter: warming for it would repeat that work, so a step that has not
// started is dropped when the call completes.
func TestWriteWarmUpIsDroppedWhenTheCallCompletes(t *testing.T) {
	probe := newWarmProbe(t)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	stream.draw(content[:9000], false)
	<-probe.entered
	stream.draw(content[:18000], false)
	stream.draw(content[:18000], true)
	close(probe.release)
	stream.state.tasks.Wait()
	if !slices.Equal(probe.lengths, []int{9000}) {
		t.Fatalf("warmed texts of %v bytes, want only the step that was running", probe.lengths)
	}
}

// The card's disposal and its session's end stop the warm-up: the step that runs is the last, and Dispose, which joins the
// card's tasks, returns once it ends.
func TestWriteWarmUpStopsWhenTheCardEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(*writeStream, context.CancelFunc)
	}{
		{"disposed", func(stream *writeStream, _ context.CancelFunc) {
			stream.state.mu.Lock()
			stream.state.closed = true
			stream.state.mu.Unlock()
		}},
		{"session ended", func(_ *writeStream, cancel context.CancelFunc) { cancel() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := newWarmProbe(t)
			content := streamedWriteTypeScript(60)
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := newWriteStream(t, lifetime)
			stream.draw(content[:9000], false)
			<-probe.entered
			stream.draw(content[:18000], false)
			tc.end(stream, cancel)
			close(probe.release)
			stream.state.tasks.Wait()
			if !slices.Equal(probe.lengths, []int{9000}) {
				t.Fatalf("warmed texts of %v bytes, want only the step that was running", probe.lengths)
			}
			if stream.state.writeWarmRunning {
				t.Error("the worker is still marked running after it stopped")
			}
		})
	}
}

// A card that is disposed starts no worker.
func TestWriteWarmUpDoesNotStartOnADisposedCard(t *testing.T) {
	probe := newWarmProbe(t)
	close(probe.release)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	stream.state.mu.Lock()
	stream.state.closed = true
	stream.state.mu.Unlock()
	stream.draw(content[:9000], false)
	stream.state.tasks.Wait()
	if len(probe.lengths) != 0 {
		t.Fatalf("a disposed card warmed %v bytes", probe.lengths)
	}
}

// A warm-up that cannot resume, and one that fails, end the warm-ups of the call; the frame that completes it highlights the
// file itself and draws what it drew before warm-ups existed. A failure of the worker does not end the process: the next frame
// of the card raises the panic, once, where an inline warm-up raised it.
func TestWriteWarmUpFailureEndsTheWarmUps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*warmProbe)
		raise any
	}{
		{"cannot resume", func(probe *warmProbe) { probe.resumable = false }, nil},
		{"panics", func(probe *warmProbe) { probe.panicWith = "highlight.js failed" }, "highlight.js failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := newWarmProbe(t)
			close(probe.release)
			tc.setup(probe)
			content := streamedWriteTypeScript(60)
			stream := newWriteStream(t, context.Background())
			stream.draw(content[:9000], false)
			stream.state.tasks.Wait()
			var raised any
			func() {
				defer func() { raised = recover() }()
				stream.draw(content[:30000], false)
			}()
			if raised != tc.raise {
				t.Fatalf("the next frame raised %v, want %v", raised, tc.raise)
			}
			stream.state.tasks.Wait()
			stream.draw(content[:31000], false)
			if !slices.Equal(probe.lengths, []int{9000}) {
				t.Fatalf("warmed texts of %v bytes, want one attempt and no more", probe.lengths)
			}
			if !stream.state.writeWarmOff {
				t.Error("the warm-ups were not turned off")
			}
			if stream.state.writeWarmRunning {
				t.Error("the worker is still marked running after it failed")
			}
			if lines := stream.draw(content, true); len(lines) == 0 {
				t.Error("the complete call drew nothing")
			}
		})
	}
}

// In an interactive session the card's worker belongs to the session's background group: the end of the session cancels the
// step that has not started and joins the one that runs, so no warm-up outlives it.
func TestInteractiveWriteWarmUpEndsWithTheSession(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	m.backgroundCtx, m.backgroundCancel = context.WithCancel(t.Context())
	probe := newWarmProbe(t)
	content := streamedWriteTypeScript(60)
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_0", Name: "write"}}}
	message := func(received string) agent.AgentMessage {
		call := ai.ToolCall{ID: "call_0", Name: "write", Arguments: ai.JsonObject{"path": "src/long.ts", "content": received}}
		return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonPending}}
	}
	m.handleAgentEvent(agent.MessageStartEvent{Message: message("")})
	m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(""), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial}})
	card := m.toolByID["call_0"]
	if card == nil {
		t.Fatal("toolcall_start made no tool card")
	}
	frame := func(from, to int) {
		m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(content[:to]), AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: content[from:to], Partial: partial}})
		card.Render(120)
	}
	frame(0, 9000)
	<-probe.entered // the first warm-up runs, held
	frame(9000, 18000)
	m.backgroundCancel()
	close(probe.release)
	m.backgroundTasks.Wait()
	if !slices.Equal(probe.lengths, []int{9000}) {
		t.Fatalf("warmed texts of %v bytes, want only the step that was running when the session ended", probe.lengths)
	}
}
